#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Read-only runtime checks for an explicitly authorized disposable local VM.

No traffic, lifecycle commands, firewall changes, raw journal/config output, or
host inventory are produced. The JSON artifact contains fixed check names and
pass/fail values only. Lifecycle and recovery scenarios are run by the operator
between snapshots; this harness does not claim to test them by itself.
"""

import argparse
import datetime
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import sys


UNITS = ("noderampartd.service", "noderampart-sensor.service")
PROPERTIES = ("ActiveState", "MainPID", "FragmentPath", "User", "Group",
              "NoNewPrivileges", "CapabilityBoundingSet", "AmbientCapabilities",
              "ProtectSystem", "ProtectHome", "PrivateDevices")


def command(arguments):
    try:
        result = subprocess.run(arguments, stdout=subprocess.PIPE,
                                stderr=subprocess.DEVNULL, timeout=15,
                                env={**os.environ, "LC_ALL": "C"}, check=False)
        if result.returncode or len(result.stdout) > 256 * 1024:
            return None
        return result.stdout.decode("utf-8")
    except (OSError, subprocess.TimeoutExpired, UnicodeError):
        return None


def properties(text):
    if text is None:
        return {}
    return dict(line.split("=", 1) for line in text.splitlines() if "=" in line)


def process_security(pid):
    if not re.fullmatch(r"[1-9][0-9]{0,9}", pid or ""):
        return {}
    try:
        # Values are reduced to fixed checks below and are never emitted.
        content = Path("/proc", pid, "status").read_text()
        return dict(line.split(":", 1) for line in content.splitlines() if ":" in line)
    except (OSError, UnicodeError):
        return {}


def is_fresh(value, now):
    try:
        at = datetime.datetime.fromisoformat(value.replace("Z", "+00:00"))
        return 0 <= (now - at).total_seconds() <= 15
    except (AttributeError, TypeError, ValueError):
        return False


def evaluate_snapshot(version, doctor, units, processes, install_kind,
                      expected_version, expected_commit, source_files_absent,
                      sockets_safe, now):
    checks = {}
    checks["cli_version"] = isinstance(version, dict) and version.get("version") == expected_version
    checks["cli_commit"] = isinstance(version, dict) and (not expected_commit or version.get("commit") == expected_commit)
    daemon = doctor.get("daemon", {}) if isinstance(doctor, dict) else {}
    if not isinstance(daemon, dict):
        daemon = {}
    daemon_version = daemon.get("version") if isinstance(daemon.get("version"), dict) else {}
    storage = daemon.get("storage") if isinstance(daemon.get("storage"), dict) else {}
    checks["daemon_version"] = daemon_version.get("version") == expected_version
    checks["daemon_commit"] = not expected_commit or daemon_version.get("commit") == expected_commit
    doctor_checks = doctor.get("checks") if isinstance(doctor, dict) and isinstance(doctor.get("checks"), list) else []
    states = {entry.get("name"): entry.get("state") for entry in doctor_checks if isinstance(entry, dict) and isinstance(entry.get("name"), str)}
    checks["doctor_configuration"] = states.get("configuration") == "valid"
    checks["doctor_peer_identity"] = states.get("daemon") == "reachable"
    checks["storage_health"] = storage.get("healthy") is True
    checks["durable_ingest_fresh"] = is_fresh(storage.get("last_durable_ingest_utc"), now)
    checks["sensor_batch_fresh"] = is_fresh(daemon.get("last_sensor_batch_utc"), now)
    journal = daemon.get("journal") if isinstance(daemon.get("journal"), dict) else {}
    checks["journal_reader_process"] = journal.get("state", journal.get("State")) == "running"
    checks["telegram_disabled"] = daemon.get("telegram_enabled") is False
    checks["native_source_conflict_absent"] = install_kind != "package" or source_files_absent
    checks["ipc_socket_permissions"] = sockets_safe
    for unit in UNITS:
        short = "daemon" if unit == UNITS[0] else "sensor"
        unit_data = units.get(unit, {})
        process = processes.get(unit, {})
        expected_user = "noderampart" if short == "daemon" else "noderampart-sensor"
        expected_cap = "" if short == "daemon" else "cap_net_raw"
        expected_mask = 0 if short == "daemon" else 1 << 13
        checks[short + "_active"] = unit_data.get("ActiveState") == "active"
        checks[short + "_account"] = unit_data.get("User") == expected_user and unit_data.get("Group") == "noderampart"
        fragment = unit_data.get("FragmentPath", "")
        expected_fragments = ["/etc/systemd/system/" + unit] if install_kind == "source" else ["/usr/lib/systemd/system/" + unit, "/lib/systemd/system/" + unit]
        checks[short + "_unit_location"] = fragment in expected_fragments
        checks[short + "_unit_capabilities"] = all(unit_data.get(key, "").lower() == expected_cap for key in ("CapabilityBoundingSet", "AmbientCapabilities"))
        checks[short + "_unit_sandbox"] = unit_data.get("NoNewPrivileges") == "yes" and unit_data.get("ProtectSystem") == "strict" and unit_data.get("ProtectHome") == "yes" and unit_data.get("PrivateDevices") == "yes"
        try:
            uids = [int(value) for value in process.get("Uid", "").split()]
            checks[short + "_runtime_privileges"] = len(uids) == 4 and all(value > 0 for value in uids) and int(process.get("CapEff", "-1"), 16) == expected_mask and int(process.get("CapBnd", "-1"), 16) == expected_mask and process.get("NoNewPrivs", "").strip() == "1"
        except (TypeError, ValueError):
            checks[short + "_runtime_privileges"] = False
    return {"schema_version": 1, "checks": {name: "pass" if value else "fail" for name, value in sorted(checks.items())}, "passed": all(checks.values()),
            "scope": "local_runtime_snapshot_only", "traffic_generated": False,
            "privileged_lifecycle_tested": False, "raw_configuration_or_journal_included": False}


def load_json(text):
    try:
        parsed = json.loads(text) if text is not None else {}
        return parsed if isinstance(parsed, dict) else {}
    except (ValueError, TypeError):
        return {}


def main(arguments=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--authorized-disposable-lab", action="store_true", required=True)
    parser.add_argument("--install-kind", choices=("source", "package"), required=True)
    parser.add_argument("--expected-version", required=True)
    parser.add_argument("--expected-commit", default="")
    parser.add_argument("--output", type=Path)
    args = parser.parse_args(arguments)
    if os.geteuid() != 0 or command(["/usr/bin/systemd-detect-virt", "--vm", "--quiet"]) is None:
        parser.error("runtime checks require root in an explicitly authorized disposable VM")
    if not re.fullmatch(r"[0-9A-Za-z.+_-]{1,64}", args.expected_version) or (args.expected_commit and not re.fullmatch(r"[0-9a-f]{7,64}", args.expected_commit)):
        parser.error("invalid expected build identity")
    binary = "/usr/local/bin/noderampart" if args.install_kind == "source" else "/usr/bin/noderampart"
    units = {unit: properties(command(["/usr/bin/systemctl", "show", "--property=" + ",".join(PROPERTIES), unit])) for unit in UNITS}
    processes = {unit: process_security(data.get("MainPID")) for unit, data in units.items()}
    sockets_safe = True
    for name in ("sensor.sock", "control.sock"):
        try:
            info = Path("/run/noderampart", name).lstat()
            sockets_safe &= stat.S_ISSOCK(info.st_mode) and info.st_mode & 0o007 == 0
        except OSError:
            sockets_safe = False
    source_absent = all(not os.path.lexists("/usr/local/bin/" + name) for name in ("noderampart", "noderampartd", "noderampart-sensor"))
    snapshot = evaluate_snapshot(load_json(command([binary, "version"])), load_json(command([binary, "doctor"])), units, processes,
                                 args.install_kind, args.expected_version, args.expected_commit, source_absent, sockets_safe, datetime.datetime.now(datetime.timezone.utc))
    encoded = json.dumps(snapshot, indent=2, sort_keys=True) + "\n"
    if args.output:
        if not args.output.is_absolute() or args.output.parent.resolve() != args.output.parent.absolute():
            parser.error("output parent must not contain symlinks")
        try:
            descriptor = os.open(args.output, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
            with os.fdopen(descriptor, "w") as output:
                output.write(encoded)
        except OSError:
            parser.error("output must be a new file in an existing safe directory")
    else:
        sys.stdout.write(encoded)
    return 0 if snapshot["passed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
