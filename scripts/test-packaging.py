#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Exercise maintainer scripts in a temporary filesystem with mocked host commands.

No packages are installed and no real services, accounts, or system paths change.
Run with: python3 scripts/test-packaging.py
"""

from contextlib import contextmanager
import json
import os
from pathlib import Path
import re
import shutil
import stat
import subprocess
import sys
import tarfile
import tempfile
import unittest
from unittest import mock


REPO = Path(__file__).resolve().parent.parent
RPM_LUA = shutil.which("rpmlua")
RPM = shutil.which("rpm")
RPMBUILD = shutil.which("rpmbuild")
RPMSPEC = shutil.which("rpmspec")
RPM_GUARD_PATHS = (
    "/etc/noderampart", "/var/lib/noderampart", "/etc/noderampart/config.json",
    "/usr/local/bin/noderampart", "/usr/local/bin/noderampartd",
    "/usr/local/bin/noderampart-sensor", "/usr/local/libexec/noderampart/manage-remove",
    "/etc/systemd/system/noderampartd.service",
    "/etc/systemd/system/noderampart-sensor.service",
)


def rpm_path_macro(name):
    """Require an expanded native RPM path, not just an installed rpm binary."""
    if not RPM:
        return None
    try:
        result = subprocess.run([RPM, "--eval", "%{" + name + "}"],
                                text=True, capture_output=True, check=False, timeout=5)
    except (OSError, subprocess.SubprocessError):
        return None
    value = result.stdout.strip()
    if result.returncode != 0 or not value.startswith("/") or "%" in value:
        return None
    if any(ord(char) < 32 or ord(char) == 127 for char in value):
        return None
    return value


MOCK = r'''
import json, os, pathlib, subprocess, sys, tarfile
name = pathlib.Path(sys.argv[0]).name
args = sys.argv[1:]
lab = pathlib.Path(os.environ["PACKAGING_LAB"])
with (lab / "commands.jsonl").open("a") as log:
    log.write(json.dumps([name, *args]) + "\n")
if name == "id":
    if args == ["-u"]:
        print(os.environ.get("MOCK_UID", "0"))
    elif args == ["-u", "noderampart"]:
        print(os.environ.get("MOCK_NATIVE_SERVICE_UID", str(os.getuid())))
    elif args == ["-g", "noderampart"]:
        print(os.environ.get("MOCK_NATIVE_SERVICE_GID", str(os.getgid())))
elif name == "stat":
    result = subprocess.run(["/usr/bin/stat", *args], text=True, capture_output=True, check=False)
    if result.returncode == 0 and os.environ.get("MOCK_NATIVE_TRUSTED_DIR") and args[:2] == ["-c", "%u:%g"] and pathlib.Path(args[-1]) == lab / "root/etc/noderampart/secrets":
        print("0:" + os.environ.get("MOCK_NATIVE_SERVICE_GID", str(os.getgid())))
    else:
        sys.stdout.write(result.stdout)
    sys.stderr.write(result.stderr)
    sys.exit(result.returncode)
elif name == "systemctl":
    if os.environ.get("MOCK_SYSTEMD_UNAVAILABLE"):
        sys.exit(1)
    unit = args[-1]
    stopped = lab / ("stopped-" + unit)
    if args[0] == "show":
        states = json.loads(os.environ.get("MOCK_UNIT_STATES", "{}"))
        state = states.get(unit, {})
        if "--property=LoadState" in args:
            print(os.environ.get("MOCK_LOAD_STATE", "loaded"))
        elif "--property=UnitFileState" in args:
            print(state.get("enabled", "disabled" if os.environ.get("MOCK_DISABLED") else "enabled"))
        elif "active" in state:
            print(state["active"])
        elif os.environ.get("MOCK_REMAIN_ACTIVE"):
            print("active")
        else:
            print("inactive" if stopped.exists() or os.environ.get("MOCK_INACTIVE") or os.environ.get("MOCK_LOAD_STATE") == "not-found" else "active")
    elif args[0] == "is-active":
        sys.exit(3 if stopped.exists() or os.environ.get("MOCK_INACTIVE") else 0)
    elif args[0] == "is-enabled":
        sys.exit(1 if os.environ.get("MOCK_DISABLED") else 0)
    elif args[0] in ("stop", "disable"):
        if os.environ.get("MOCK_STOP_FAIL"):
            sys.exit(1)
        for unit in args[1:]:
            if unit.endswith((".service", ".timer")):
                (lab / ("stopped-" + unit)).touch()
elif name == "deb-systemd-invoke":
    if os.environ.get("MOCK_POLICY_DENY"):
        sys.exit(0)
    sys.exit(subprocess.run([str(pathlib.Path(sys.argv[0]).parent / "systemctl"), *args], check=False).returncode)
elif name == "dpkg-query":
    if os.environ.get("MOCK_DPKG_INSTALLED"):
        print("install ok installed")
    else:
        sys.exit(1)
elif name == "rpm":
    sys.exit(0 if os.environ.get("MOCK_RPM_INSTALLED") else 1)
elif name == "mountpoint":
    sys.exit(0 if args[-1] == os.environ.get("MOCK_MOUNT") else 32)
elif name == "userdel" and os.environ.get("MOCK_USERDEL_FAIL"):
    sys.exit(1)
elif name in ("install", "rm", "rmdir"):
    # Even a broken path rewrite cannot make these mocks write outside the lab.
    if name == "install":
        filtered = []
        skip = False
        for arg in args:
            if skip:
                skip = False
            elif arg in ("-o", "-g"):
                skip = True
            else:
                filtered.append(arg)
        args = filtered
    for arg in args:
        if arg.startswith("/") and not pathlib.Path(arg).resolve().is_relative_to(lab):
            raise SystemExit("unsafe mock filesystem argument")
    sys.exit(subprocess.run(["/usr/bin/" + name, *args], check=False).returncode)
elif name == "go" and args[:2] == ["version", "-m"]:
    print("build GOOS=linux\nbuild GOARCH=" + os.environ.get("MOCK_GOARCH", "amd64"))
elif name == "go" and args == ["env", "GOARCH"]:
    print(os.environ.get("MOCK_GOARCH", "amd64"))
elif name == "go" and args[:2] == ["mod", "vendor"]:
    assert pathlib.Path.cwd() != lab / "project"
    assert pathlib.Path(args[-1]).parent == pathlib.Path.cwd()
    pathlib.Path(args[-1]).mkdir()
elif name == "go" and args == ["mod", "verify"]:
    sys.exit(1 if os.environ.get("MOCK_MODULE_VERIFY_FAIL") else 0)
elif name == "dpkg":
    print("amd64")
elif name == "dpkg-deb":
    (lab / "deb-control").write_text((pathlib.Path(args[-2]) / "DEBIAN/control").read_text())
    assert (pathlib.Path(args[-2]) / "DEBIAN/preinst").is_file()
    helpers = list(pathlib.Path(args[-2]).rglob("usr/libexec/noderampart/manage-remove"))
    assert len(helpers) == 1 and helpers[0].is_file() and helpers[0].stat().st_mode & 0o111
elif name == "rpmbuild":
    (lab / "rpm-spec").write_text(pathlib.Path(args[-1]).read_text())
    topdir = pathlib.Path(args[args.index("--define") + 1].removeprefix("_topdir "))
    source = next((topdir / "SOURCES").glob("*.tar.gz"))
    with tarfile.open(source) as archive:
        names = archive.getnames()
    assert not any(name.endswith("/docs/ai") or "/docs/ai/" in name for name in names)
    (lab / "rpm-source-files.json").write_text(json.dumps(names))
'''


class PackagingTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="noderampart-packaging-test-")
        self.addCleanup(self.temp.cleanup)
        self.lab = Path(self.temp.name)
        self.project = self.lab / "project"
        self.root = self.lab / "root"
        self.mocks = self.lab / "mocks"
        self.mocks.mkdir()
        self.env = dict(os.environ, PACKAGING_LAB=str(self.lab),
                        PATH=str(self.mocks) + ":/usr/bin:/bin")
        mock = "#!" + sys.executable + "\n" + MOCK
        for name in ("id", "getent", "groupadd", "useradd", "usermod", "userdel",
                     "groupdel", "chown", "chmod", "systemctl", "mountpoint",
                     "install", "rm", "rmdir", "go", "dpkg", "dpkg-deb", "rpmbuild",
                     "dpkg-query", "rpm", "deb-systemd-helper", "deb-systemd-invoke", "stat"):
            self.write(self.mocks / name, mock, executable=True)
        for path in ("etc/noderampart", "var/lib/noderampart", "var/cache/noderampart",
                     "run/systemd/system", "tmp", "usr/local/bin", "etc/systemd/system"):
            (self.root / path).mkdir(parents=True, exist_ok=True)
        for path in ("scripts", "packaging", "bin", "configs", "third_party/licenses"):
            (self.project / path).mkdir(parents=True, exist_ok=True)
        for name in ("noderampart", "noderampartd", "noderampart-sensor"):
            self.write(self.project / "bin" / name, mock, executable=True)
        self.cli_mock = mock
        for path in ("LICENSE", "README.md", "THIRD_PARTY_NOTICES.md", "configs/noderampart.json",
                     "third_party/licenses/example"):
            self.write(self.project / path, "fixture\n")
        self.write(self.root / "etc/noderampart/config.json", "fixture config\n")
        self.write(self.root / "var/lib/noderampart/state", "persistent fixture\n")
        self.write(self.root / "proc/self/mountinfo", "1 0 0:1 / / rw - tmpfs tmpfs rw\n")
        for path in (REPO / "packaging").rglob("*"):
            if path.is_file():
                self.write(self.project / path.relative_to(REPO), path.read_text())
        self.write(self.project / "scripts/manage-remove.sh", (REPO / "scripts/manage-remove.sh").read_text(), executable=True)

    @staticmethod
    def write(path, content, executable=False):
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content)
        if executable:
            path.chmod(0o755)

    def run_script(self, source, *args, section=None):
        content = (REPO / source).read_text()
        if section:
            _, content = self.rpm_section(content, section)
            content = content.split("\n%", 1)[0]
            content = "#!/bin/sh\n" + content.replace("%{_sysconfdir}", "/etc").replace("%{_bindir}", "/usr/bin")
        if source == "packaging/debian/postinst" or section == "post":
            self.write(self.root / "usr/bin/noderampart", self.cli_mock, executable=True)
        # Rewrite literal host paths in the script, including tests and redirections.
        content = re.sub(r"/(?:etc|usr|lib|var|run|proc)/|/tmp/noderampart", lambda m: str(self.root) + m[0], content)
        staged = self.project / source
        if section:
            staged = self.project / ("rpm-" + section + ".sh")
        self.write(staged, content)
        return subprocess.run(["/bin/sh", str(staged), *args], cwd=self.project,
                              env=self.env, text=True, capture_output=True, check=False)

    @staticmethod
    def rpm_section(content, section):
        # Exact names keep %pretrans and %preun from being mistaken for %pre.
        match = re.search(r"(?m)^%" + re.escape(section) + r"(?=[ \t]|\n)([^\n]*)\n", content)
        if not match:
            raise AssertionError("missing RPM section: " + section)
        body = content[match.end():]
        boundary = re.search(r"(?m)^%(?:description|prep|build|install|check|clean|pretrans|pre|post|preun|postun|posttrans|files|changelog)(?:\s|$)", body)
        return match.group(1).strip(), body[:boundary.start()] if boundary else body

    def rpm_pretrans_body(self):
        options, body = self.rpm_section((REPO / "packaging/rpm/noderampart.spec").read_text(), "pretrans")
        self.assertEqual(options, "-p <lua>")
        return body.replace("%{_sysconfdir}", "/etc")

    def filesystem_snapshot(self):
        snapshot = {}
        for path in [self.root, *sorted(self.root.rglob("*"))]:
            metadata = path.lstat()
            content = os.readlink(path) if stat.S_ISLNK(metadata.st_mode) else path.read_bytes() if stat.S_ISREG(metadata.st_mode) else None
            snapshot[str(path.relative_to(self.root))] = (
                metadata.st_mode, metadata.st_ino, metadata.st_uid, metadata.st_gid,
                metadata.st_mtime_ns, metadata.st_ctime_ns, content,
            )
        return snapshot

    def run_rpm_pretrans(self, *, body=None, failure=None):
        # Run production Lua in RPM's own interpreter. Only read-only native
        # posix calls are exposed, and their fixed paths map into the lab root.
        # RPM posix.stat uses lstat, including for dangling symlinks.
        body = self.rpm_pretrans_body() if body is None else body
        self.assert_rpm_pretrans_read_only(body)
        quote = lambda value: json.dumps(value, ensure_ascii=False)
        paths = "{" + ",".join("[" + quote(path) + "]=true" for path in RPM_GUARD_PATHS) + "}"
        injected = ""
        if failure:
            path, operation, number = failure
            injected = ("if path == " + quote(path) + " and operation == " + quote(operation)
                        + " then return nil, 'synthetic inspection failure', " + ("nil" if number is None else str(number)) + " end\n")
        harness = """
local native_stat, native_readlink = posix.stat, posix.readlink
local allowed = PATHS
local fixture_root = ROOT
local reads = 0
-- rpm --eval buffers print output without adding line separators.
local function emit(value) print(value .. '\\n') end
local function inspect(operation, path, selector)
    assert(allowed[path], 'UNEXPECTED_PATH: ' .. tostring(path))
    reads = reads + 1
    assert(reads <= 2 * 9, 'UNBOUNDED_READS')
    if operation == 'stat' then assert(selector == 'type', 'UNEXPECTED_SELECTOR') end
    emit('READ\\t' .. operation .. '\\t' .. path)
    INJECTED
    if operation == 'stat' then return native_stat(fixture_root .. path, selector) end
    return native_readlink(fixture_root .. path)
end
local fixture_posix = setmetatable({
    stat = function(path, selector) return inspect('stat', path, selector) end,
    readlink = function(path) return inspect('readlink', path) end,
}, {__index = function(_, key) error('UNSAFE_POSIX: ' .. tostring(key)) end})
local safe = setmetatable({posix = fixture_posix, error = error, ipairs = ipairs,
    pairs = pairs, type = type, tostring = tostring, string = string, table = table,
}, {__index = function(_, key) error('UNSAFE_GLOBAL: ' .. tostring(key)) end})
local guard = assert(load(BODY, 'noderampart-pretrans', 't', safe))
local ok, message = pcall(guard)
if ok then emit('RESULT\\tOK') else emit('RESULT\\tBLOCKED\\t' .. tostring(message)) end
"""
        harness = (harness.replace("PATHS", paths).replace("ROOT", quote(str(self.root)))
                   .replace("INJECTED", injected).replace("BODY", quote(body)))
        script = self.lab / "rpm-pretrans-fixture.lua"
        self.write(script, harness)
        command = [RPM_LUA, str(script)] if RPM_LUA else [RPM, "--eval", "%{lua:dofile(" + quote(str(script)) + ")}"]
        before = self.filesystem_snapshot()
        result = subprocess.run(command, text=True, capture_output=True, check=False, timeout=15)
        self.assertEqual(self.filesystem_snapshot(), before, "pretrans changed fixture objects")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotRegex(result.stdout + result.stderr, r"UNSAFE_|UNEXPECTED_|UNBOUNDED_")
        self.assertFalse(self.commands(), "pretrans invoked an external mocked command")
        rows = result.stdout.splitlines()
        outcomes = [row.split("\t", 2) for row in rows if row.startswith("RESULT\t")]
        self.assertEqual(len(outcomes), 1, result.stdout + result.stderr)
        self.assertTrue(any(row.startswith("READ\t") for row in rows), "guard made no native posix reads")
        return outcomes[0][1], outcomes[0][2] if len(outcomes[0]) == 3 else ""

    def assert_rpm_pretrans_read_only(self, body):
        calls = set(re.findall(r"\b(posix|os|io|rpm)\s*\.\s*(\w+)\s*\(", body))
        self.assertTrue(calls)
        self.assertLessEqual(calls, {("posix", "stat"), ("posix", "readlink")})
        self.assertNotRegex(body, r"\b(?:require|load|loadfile|dofile)\s*\(")
        self.assertNotIn("%{", body)

    @contextmanager
    def rpm_guard_object(self, relative, kind):
        path = self.root / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        saved = self.lab / "saved-guard-object"
        existed = path.exists() or path.is_symlink()
        if existed:
            path.rename(saved)
        if kind == "regular":
            self.write(path, "unowned fixture\n")
        elif kind == "directory":
            path.mkdir()
        elif kind == "fifo":
            os.mkfifo(path)
        elif kind == "missing":
            pass
        else:
            path.symlink_to({"symlink": self.root / "guard-canary", "dangling": "missing-fixture",
                             "dev_null": "/dev/null", "dev_null_alias": "/dev/../dev/null"}[kind])
        try:
            yield path
        finally:
            if path.is_dir() and not path.is_symlink():
                path.rmdir()
            elif path.exists() or path.is_symlink():
                path.unlink()
            if existed:
                saved.rename(path)

    def commands(self):
        path = self.lab / "commands.jsonl"
        return [json.loads(line) for line in path.read_text().splitlines()] if path.exists() else []

    def run_purge_credential_guard(self, source):
        content = (REPO / source).read_text()
        body = content.split("purge_native_credentials_guard() {", 1)[1].split("\n}\n", 1)[0]
        content = "#!/bin/sh\nset -eu\npurge_native_credentials_guard() {" + body + "\n}\npurge_native_credentials_guard\n"
        content = re.sub(r"/(?:etc|usr|lib|var|run|proc)/", lambda m: str(self.root) + m[0], content)
        staged = self.project / "purge-credential-guard.sh"
        self.write(staged, content)
        return subprocess.run(["/bin/sh", str(staged)], env=self.env, text=True, capture_output=True, check=False)

    def prepare_rpm_source(self, version="0.4.0-alpha"):
        files = ["LICENSE", "README.md", "README.zh-CN.md", "THIRD_PARTY_NOTICES.md", "CHANGELOG.md",
                 "SECURITY.md", "CONTRIBUTING.md", "Makefile", "go.mod", "go.sum", "VERSION",
                 "docs/public-guide.md", "configs/noderampart.json", "packaging/rpm/noderampart.spec",
                 "packaging/source-files.txt", "scripts/build-rpm.sh", "scripts/manage-remove.sh", "scripts/stage-source.py"]
        for path in files:
            if not (self.project / path).exists():
                self.write(self.project / path, "synthetic source fixture\n")
        self.write(self.project / "VERSION", version + "\n")
        self.write(self.project / "scripts/stage-source.py", (REPO / "scripts/stage-source.py").read_text())
        self.write(self.project / "packaging/source-files.txt", "\n".join(files) + "\n")

    def assert_no_mutations(self):
        for command in self.commands():
            if command[0] == "stat":
                self.assertEqual(command[1], "-c", command)
                self.assertIn(command[2], ("%u:%g", "%a", "%h:%u:%g:%a", "%s"), command)
                continue
            self.assertIn(command[0], ("id", "mountpoint"), command)

    def test_installers_reject_config_symlinks_before_mutations(self):
        config = self.root / "etc/noderampart/config.json"
        config.unlink()
        canary = self.root / "canary"
        canary.write_text("untouched")
        config.symlink_to(canary)
        for source, args, section in (("scripts/install.sh", (), None),
                                      ("packaging/debian/preinst", ("install",), None),
                                      ("packaging/debian/postinst", ("configure",), None),
                                      ("packaging/rpm/noderampart.spec", (), "pre"),
                                      ("packaging/rpm/noderampart.spec", (), "post")):
            with self.subTest(source=source, section=section):
                self.assertNotEqual(self.run_script(source, *args, section=section).returncode, 0)
                self.assertEqual(canary.read_text(), "untouched")
                self.assert_no_mutations()

    def test_install_and_active_upgrade_restart_both_services(self):
        for source, args in (("scripts/install.sh", ()), ("packaging/debian/postinst", ("configure", "0.1.0-alpha"))):
            with self.subTest(source=source):
                result = self.run_script(source, *args)
                self.assertEqual(result.returncode, 0, result.stderr)
        restarts = [command for command in self.commands() if command[:2] == ["systemctl", "restart"]]
        self.assertEqual(restarts, [["systemctl", "restart", "noderampartd.service", "noderampart-sensor.service"],
                                    ["systemctl", "restart", "noderampartd.service"],
                                    ["systemctl", "restart", "noderampart-sensor.service"]])

    def test_debian_upgrade_preserves_disabled_and_stopped_services(self):
        self.env.update(MOCK_DISABLED="1", MOCK_INACTIVE="1")
        result = self.run_script("packaging/debian/postinst", "configure", "0.1.0~alpha")
        self.assertEqual(result.returncode, 0, result.stderr)
        commands = self.commands()
        self.assertFalse(any(command[:2] in (["systemctl", "enable"], ["systemctl", "restart"], ["systemctl", "start"], ["deb-systemd-helper", "enable"]) for command in commands))
        self.assertEqual(sum(command[:2] == ["deb-systemd-helper", "update-state"] for command in commands), 2)

    def test_debian_install_and_upgrade_respect_service_policy(self):
        self.env["MOCK_POLICY_DENY"] = "1"
        for args in (("configure",), ("configure", "0.1.0~alpha")):
            result = self.run_script("packaging/debian/postinst", *args)
            self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(any(command[:2] in (["systemctl", "start"], ["systemctl", "restart"]) for command in self.commands()))
        self.assertTrue(any(command[0] == "deb-systemd-invoke" for command in self.commands()))

    def test_source_installer_rejects_disabled_native_files(self):
        self.env.update(MOCK_DISABLED="1", MOCK_INACTIVE="1")
        for path in ("usr/bin/noderampartd", "usr/lib/systemd/system/noderampartd.service",
                     "lib/systemd/system/noderampartd.service"):
            with self.subTest(path=path):
                native = self.root / path
                self.write(native, "package-owned fixture")
                try:
                    result = self.run_script("scripts/install.sh")
                    self.assertNotEqual(result.returncode, 0)
                    self.assertIn("native package", result.stderr)
                    self.assert_no_mutations()
                    self.assertEqual(native.read_text(), "package-owned fixture")
                finally:
                    native.unlink()

    def test_source_installer_rejects_package_database_ownership(self):
        for variable in ("MOCK_DPKG_INSTALLED", "MOCK_RPM_INSTALLED"):
            with self.subTest(variable=variable):
                self.env[variable] = "1"
                result = self.run_script("scripts/install.sh")
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse(any(command[0] in ("install", "systemctl", "groupadd", "useradd") for command in self.commands()))
                del self.env[variable]

    def test_source_upgrade_preserves_each_service_choice(self):
        self.assertEqual(self.run_script("scripts/install.sh").returncode, 0)
        variants = (
            ({"active": "inactive", "enabled": "disabled"}, {"active": "inactive", "enabled": "disabled"}, []),
            ({"active": "active", "enabled": "disabled"}, {"active": "inactive", "enabled": "enabled"}, ["noderampartd.service"]),
            ({"active": "active", "enabled": "enabled"}, {"active": "active", "enabled": "disabled"}, ["noderampartd.service", "noderampart-sensor.service"]),
            ({"active": "inactive", "enabled": "masked-runtime"}, {"active": "inactive", "enabled": "masked-runtime"}, []),
        )
        for daemon, sensor, restarted in variants:
            with self.subTest(daemon=daemon, sensor=sensor):
                (self.lab / "commands.jsonl").unlink()
                self.env["MOCK_UNIT_STATES"] = json.dumps({"noderampartd.service": daemon, "noderampart-sensor.service": sensor})
                result = self.run_script("scripts/install.sh")
                self.assertEqual(result.returncode, 0, result.stderr)
                commands = self.commands()
                self.assertFalse(any(row[:2] in (["systemctl", "enable"], ["systemctl", "disable"], ["systemctl", "unmask"]) for row in commands))
                self.assertEqual([row[2:] for row in commands if row[:2] == ["systemctl", "restart"]], [restarted] if restarted else [])
                first_write = next(i for i, row in enumerate(commands) if row[0] == "install")
                snapshots = [row for row in commands[:first_write] if row[:2] == ["systemctl", "show"]]
                self.assertEqual(len(snapshots), 4)

    def test_source_upgrade_rejects_permanent_mask_before_changes(self):
        self.assertEqual(self.run_script("scripts/install.sh").returncode, 0)
        (self.lab / "commands.jsonl").unlink()
        unit = self.root / "etc/systemd/system/noderampartd.service"
        unit.unlink()
        unit.symlink_to("/dev/null")
        result = self.run_script("scripts/install.sh")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("permanently masked", result.stderr)
        self.assertEqual(os.readlink(unit), "/dev/null")
        self.assert_no_mutations()

    def test_native_installers_reject_source_files_before_mutations(self):
        self.write(self.root / "usr/local/bin/noderampartd", "source-owned binary")
        for source, args, section in (("packaging/debian/preinst", ("install",), None),
                                      ("packaging/rpm/noderampart.spec", (), "pre")):
            result = self.run_script(source, *args, section=section)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("conflicts", result.stderr)
            self.assert_no_mutations()

    def test_native_installers_reject_remaining_source_helper(self):
        helper = self.root / "usr/local/libexec/noderampart/manage-remove"
        helper.parent.mkdir(parents=True)
        canary = self.root / "helper-canary"
        canary.write_text("unowned helper content")
        dropin = self.root / "etc/systemd/system/noderampartd.service.d/local.conf"
        self.write(dropin, "administrator override")
        for kind in ("file", "directory", "symlink", "dangling", "dev_null"):
            if kind == "file":
                helper.write_text("unowned helper content")
            elif kind == "directory":
                helper.mkdir()
            else:
                helper.symlink_to({"symlink": canary, "dangling": "missing-fixture",
                                   "dev_null": "/dev/null"}[kind])
            try:
                for source, args, section in (("packaging/debian/preinst", ("install",), None),
                                              ("packaging/rpm/noderampart.spec", (), "pre")):
                    with self.subTest(kind=kind, source=source):
                        log = self.lab / "commands.jsonl"
                        if log.exists():
                            log.unlink()
                        result = self.run_script(source, *args, section=section)
                        self.assertNotEqual(result.returncode, 0, result.stderr)
                        self.assertIn("conflicts", result.stderr)
                        self.assert_no_mutations()
                        self.assertTrue(helper.exists() or helper.is_symlink())
                        self.assertEqual(canary.read_text(), "unowned helper content")
                        self.assertEqual(dropin.read_text(), "administrator override")
                        self.assertEqual((self.root / "etc/noderampart/config.json").read_text(), "fixture config\n")
                        self.assertEqual((self.root / "var/lib/noderampart/state").read_text(), "persistent fixture\n")
            finally:
                if helper.is_symlink() or helper.is_file():
                    helper.unlink()
                else:
                    helper.rmdir()

    def test_five_entry_transition_preserves_helper_and_blocks_native_install(self):
        self.assertEqual(self.run_script("scripts/install.sh").returncode, 0)
        manifest = self.root / "usr/local/share/doc/noderampart/source-install.manifest"
        entries = [line for line in manifest.read_text().splitlines() if not line.endswith("/manage-remove")]
        self.assertEqual(len(entries), 5)
        manifest.write_text("\n".join(entries) + "\n")
        helper = self.root / "usr/local/libexec/noderampart/manage-remove"
        original = helper.read_bytes()
        result = self.run_script("scripts/source-to-package.sh", "--prepare")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(manifest.exists())
        self.assertEqual(helper.read_bytes(), original)
        self.assertFalse((self.root / "usr/local/bin/noderampart").exists())
        for source, args, section in (("packaging/debian/preinst", ("install",), None),
                                      ("packaging/rpm/noderampart.spec", (), "pre")):
            with self.subTest(source=source):
                (self.lab / "commands.jsonl").unlink(missing_ok=True)
                result = self.run_script(source, *args, section=section)
                self.assertNotEqual(result.returncode, 0, result.stderr)
                self.assertIn("conflicts", result.stderr)
                self.assert_no_mutations()
                self.assertEqual(helper.read_bytes(), original)
        # Only this fixture's explicitly owned helper is reconciled; installers
        # must not guess ownership or remove it on the operator's behalf.
        helper.unlink()
        for source, args, section in (("packaging/debian/preinst", ("install",), None),
                                      ("packaging/rpm/noderampart.spec", (), "pre")):
            result = self.run_script(source, *args, section=section)
            self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue((self.root / "var/lib/noderampart/state").is_file())
        self.assertTrue((self.root / "etc/noderampart/config.json").is_file())

    def test_native_installers_preserve_administrator_masks(self):
        for unit in ("noderampartd.service", "noderampart-sensor.service"):
            (self.root / "etc/systemd/system" / unit).symlink_to("/dev/null")
        for source, args, section in (("packaging/debian/preinst", ("upgrade",), None),
                                      ("packaging/rpm/noderampart.spec", (), "pre")):
            result = self.run_script(source, *args, section=section)
            self.assertEqual(result.returncode, 0, result.stderr)
        for unit in ("noderampartd.service", "noderampart-sensor.service"):
            self.assertEqual(os.readlink(self.root / "etc/systemd/system" / unit), "/dev/null")
        result = self.run_script("packaging/debian/postinst", "configure")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(any(command[0] == "deb-systemd-invoke" or command[:2] == ["deb-systemd-helper", "enable"] for command in self.commands()))

    def test_rpm_pretrans_is_separate_embedded_read_only_lua(self):
        sample = "%pretrans -p <lua>\nerror('guard')\n%pre\nuseradd fixture\n%post\ntrue\n"
        self.assertEqual(self.rpm_section(sample, "pretrans"), ("-p <lua>", "error('guard')\n"))
        self.assertEqual(self.rpm_section(sample, "pre"), ("", "useradd fixture\n"))
        body = self.rpm_pretrans_body()
        self.assert_rpm_pretrans_read_only(body)
        _, prein = self.rpm_section((REPO / "packaging/rpm/noderampart.spec").read_text(), "pre")
        self.assertIn("useradd", prein)
        self.assertNotIn("posix.", prein)
        self.assertNotRegex((REPO / "packaging/rpm/noderampart.spec").read_text(), r"(?mi)^Requires\([^\n)]*pretrans")

    @unittest.skipUnless(RPM_LUA or RPM, "native RPM Lua runtime is unavailable")
    def test_rpm_pretrans_rejects_all_remaining_source_helper_types(self):
        self.write(self.root / "guard-canary", "unowned helper target\n")
        self.write(self.root / "etc/systemd/system/noderampartd.service.d/local.conf", "administrator override\n")
        for kind in ("regular", "directory", "symlink", "dangling", "dev_null"):
            with self.subTest(kind=kind), self.rpm_guard_object("usr/local/libexec/noderampart/manage-remove", kind):
                outcome, message = self.run_rpm_pretrans()
                self.assertEqual(outcome, "BLOCKED", message)
                self.assertIn("conflicts", message)

    @unittest.skipUnless(RPM_LUA or RPM, "native RPM Lua runtime is unavailable")
    def test_rpm_pretrans_rejects_remaining_source_binaries(self):
        for name in ("noderampart", "noderampartd", "noderampart-sensor"):
            for kind in ("regular", "dangling", "dev_null"):
                with self.subTest(name=name, kind=kind), self.rpm_guard_object("usr/local/bin/" + name, kind):
                    outcome, message = self.run_rpm_pretrans()
                    self.assertEqual(outcome, "BLOCKED", message)
                    self.assertIn("conflicts", message)

    @unittest.skipUnless(RPM_LUA or RPM, "native RPM Lua runtime is unavailable")
    def test_rpm_pretrans_rejects_full_units_and_preserves_exact_dev_null_masks(self):
        self.write(self.root / "guard-canary", "administrator unit target\n")
        for name in ("noderampartd.service", "noderampart-sensor.service"):
            for kind in ("regular", "directory", "fifo", "symlink", "dangling", "dev_null_alias", "dev_null"):
                with self.subTest(name=name, kind=kind), self.rpm_guard_object("etc/systemd/system/" + name, kind):
                    outcome, message = self.run_rpm_pretrans()
                    self.assertEqual(outcome, "OK" if kind == "dev_null" else "BLOCKED", message)
                    if kind != "dev_null":
                        self.assertIn("conflicts", message)

    @unittest.skipUnless(RPM_LUA or RPM, "native RPM Lua runtime is unavailable")
    def test_rpm_pretrans_rejects_unsafe_managed_directories_and_config_types(self):
        self.write(self.root / "guard-canary", "configuration target must survive\n")
        for relative in ("etc/noderampart", "var/lib/noderampart"):
            for kind in ("regular", "fifo", "symlink", "dangling"):
                with self.subTest(relative=relative, kind=kind), self.rpm_guard_object(relative, kind):
                    outcome, message = self.run_rpm_pretrans()
                    self.assertEqual(outcome, "BLOCKED", message)
                    self.assertIn("refusing non-directory or symlinked installation directory", message)
        for kind in ("directory", "fifo", "symlink", "dangling", "dev_null"):
            with self.subTest(config=kind), self.rpm_guard_object("etc/noderampart/config.json", kind):
                outcome, message = self.run_rpm_pretrans()
                self.assertEqual(outcome, "BLOCKED", message)
                self.assertIn("refusing non-regular existing configuration", message)

    @unittest.skipUnless(RPM_LUA or RPM, "native RPM Lua runtime is unavailable")
    def test_rpm_pretrans_accepts_valid_and_absent_managed_paths(self):
        self.assertEqual(self.run_rpm_pretrans(), ("OK", ""))
        for relative in ("etc/noderampart/config.json", "etc/noderampart", "var/lib/noderampart"):
            with self.subTest(absent=relative), self.rpm_guard_object(relative, "missing"):
                self.assertEqual(self.run_rpm_pretrans(), ("OK", ""))

    @unittest.skipUnless(RPM_LUA or RPM, "native RPM Lua runtime is unavailable")
    def test_rpm_pretrans_fails_closed_on_inspection_errors(self):
        for path in RPM_GUARD_PATHS:
            for number in (5, 13, None):
                with self.subTest(path=path, errno=number):
                    outcome, message = self.run_rpm_pretrans(failure=(path, "stat", number))
                    self.assertEqual(outcome, "BLOCKED", message)
                    self.assertIn("cannot inspect installation path", message)
        for name in ("noderampartd.service", "noderampart-sensor.service"):
            path = "/etc/systemd/system/" + name
            with self.rpm_guard_object(path.lstrip("/"), "dev_null"):
                for number in (5, 13, None):
                    with self.subTest(path=path, readlink_errno=number):
                        outcome, message = self.run_rpm_pretrans(failure=(path, "readlink", number))
                        self.assertEqual(outcome, "BLOCKED", message)
                        self.assertIn("cannot inspect installation path", message)

    def test_source_transition_checks_ownership_and_preserves_state_and_dropins(self):
        result = self.run_script("scripts/install.sh")
        self.assertEqual(result.returncode, 0, result.stderr)
        manifest = self.root / 'usr/local/share/doc/noderampart/source-install.manifest'
        self.assertEqual(len(manifest.read_text().splitlines()), 6)
        self.assertIn('/usr/local/libexec/noderampart/manage-remove', manifest.read_text())
        override = self.root / "etc/systemd/system/noderampartd.service.d/local.conf"
        self.write(override, "administrator override")
        timer = self.root / "etc/systemd/system/noderampart-geoip-update.timer"
        self.write(timer, "source path update timer")
        managed = override.parent / "90-noderampart-managed.conf"
        self.write(managed, "managed source override")
        result = self.run_script("scripts/source-to-package.sh", "--prepare")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse((self.root / "usr/local/bin/noderampartd").exists())
        self.assertFalse((self.root / "etc/systemd/system/noderampartd.service").exists())
        self.assertFalse((self.root / "usr/local/libexec/noderampart/manage-remove").exists())
        self.assertFalse(timer.exists())
        self.assertFalse(managed.exists())
        self.assertTrue((self.root / "var/lib/noderampart/state").is_file())
        self.assertTrue((self.root / "etc/noderampart/config.json").is_file())
        self.assertEqual(override.read_text(), "administrator override")
        result = self.run_script("packaging/debian/preinst", "install")
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_source_transition_accepts_older_five_entry_manifest(self):
        result = self.run_script('scripts/install.sh')
        self.assertEqual(result.returncode, 0, result.stderr)
        manifest = self.root / 'usr/local/share/doc/noderampart/source-install.manifest'
        entries = [line for line in manifest.read_text().splitlines() if not line.endswith('/manage-remove')]
        self.assertEqual(len(entries), 5)
        manifest.write_text('\n'.join(entries) + '\n')
        helper = self.root / 'usr/local/libexec/noderampart/manage-remove'
        helper.unlink()
        result = self.run_script('scripts/source-to-package.sh', '--prepare')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse((self.root / 'usr/local/bin/noderampart').exists())
        self.assertFalse(manifest.exists())
        self.assertTrue((self.root / 'var/lib/noderampart/state').is_file())
        self.assertTrue((self.root / 'etc/noderampart/config.json').is_file())

    def test_source_transition_rejects_modified_or_symlinked_owned_files(self):
        result = self.run_script("scripts/install.sh")
        self.assertEqual(result.returncode, 0, result.stderr)
        binary = self.root / "usr/local/bin/noderampartd"
        original = binary.read_text()
        binary.write_text("administrator replacement")
        result = self.run_script("scripts/source-to-package.sh", "--prepare")
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(binary.read_text(), "administrator replacement")
        binary.write_text(original)
        canary = self.root / "canary"
        canary.write_text(original)
        binary.unlink()
        binary.symlink_to(canary)
        result = self.run_script("scripts/source-to-package.sh", "--prepare")
        self.assertNotEqual(result.returncode, 0)
        self.assertTrue(binary.is_symlink())
        self.assertEqual(canary.read_text(), original)

    def test_source_transition_requires_exact_legacy_checkout(self):
        result = self.run_script("scripts/install.sh")
        self.assertEqual(result.returncode, 0, result.stderr)
        (self.root / "usr/local/share/doc/noderampart/source-install.manifest").unlink()
        result = self.run_script("scripts/source-to-package.sh", "--prepare")
        self.assertNotEqual(result.returncode, 0)
        result = self.run_script("scripts/source-to-package.sh", "--prepare", "--legacy-checkout", str(self.project))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue((self.root / "var/lib/noderampart/state").is_file())

    def test_installers_reject_state_directory_symlinks(self):
        state = self.root / "var/lib/noderampart"
        target = self.root / "original-state"
        state.rename(target)
        state.symlink_to(target)
        for source, args, section in (("scripts/install.sh", (), None),
                                      ("packaging/debian/preinst", ("install",), None),
                                      ("packaging/debian/postinst", ("configure",), None),
                                      ("packaging/rpm/noderampart.spec", (), "pre")):
            with self.subTest(source=source):
                self.assertNotEqual(self.run_script(source, *args, section=section).returncode, 0)
                self.assert_no_mutations()

    def test_purge_preflights_all_paths_before_removal(self):
        cache = self.root / "var/cache/noderampart"
        for kind in ("symlink", "mount"):
            with self.subTest(kind=kind):
                if kind == "symlink":
                    cache.rmdir()
                    cache.symlink_to(self.root / "var/lib/noderampart")
                else:
                    cache.unlink()
                    cache.mkdir()
                    self.env["MOCK_MOUNT"] = str(cache)
                for source, arg in (("scripts/uninstall.sh", "--purge"), ("packaging/debian/postrm", "purge")):
                    self.assertNotEqual(self.run_script(source, arg).returncode, 0)
                    self.assertTrue((self.root / "etc/noderampart/config.json").is_file())
                    self.assertTrue((self.root / "var/lib/noderampart/state").is_file())
                    self.assert_no_mutations()

    def test_all_purge_entries_protect_manual_native_credentials_before_mutations(self):
        sources = ("scripts/uninstall.sh", "packaging/debian/postrm", "scripts/manage-remove.sh")
        for relative in ("feishu.credential.json", "wecom.credential.json", "discord.credential.json",
                         "slack.credential.json", "teams.credential.json", "google_chat.credential.json",
                         "custom-authorized-hook.json", "secrets/slack-user.secret", "secrets/.manual.secret"):
            with self.subTest(relative=relative):
                path = self.root / "etc/noderampart" / relative
                self.write(path, "SYNTHETIC_PRIVATE_WEBHOOK_CREDENTIAL")
                path.chmod(0o600)
                self.env["MOCK_NATIVE_TRUSTED_DIR"] = "1"
                for source in sources:
                    result = self.run_purge_credential_guard(source)
                    self.assertNotEqual(result.returncode, 0, source)
                    self.assertEqual(path.read_text(), "SYNTHETIC_PRIVATE_WEBHOOK_CREDENTIAL")
                    self.assertNotIn("SYNTHETIC_PRIVATE_WEBHOOK_CREDENTIAL", result.stdout + result.stderr)
                    self.assert_no_mutations()
                path.unlink()
        for source, argument in (("scripts/uninstall.sh", "--purge"), ("packaging/debian/postrm", "purge")):
            path = self.root / "etc/noderampart/slack.credential.json"
            self.write(path, "SYNTHETIC_PRIVATE_WEBHOOK_CREDENTIAL")
            result = self.run_script(source, argument)
            self.assertNotEqual(result.returncode, 0)
            self.assertTrue((self.root / "var/lib/noderampart/state").is_file())
            self.assert_no_mutations()
            path.unlink()

    def test_all_purge_entries_recognize_only_safe_managed_native_secret_files(self):
        self.env["MOCK_NATIVE_TRUSTED_DIR"] = "1"
        directory = self.root / "etc/noderampart/secrets"
        directory.mkdir()
        directory.chmod(0o750)
        sources = ("scripts/uninstall.sh", "packaging/debian/postrm", "scripts/manage-remove.sh")
        paths = []
        for channel in ("telegram", "privacy", "feishu", "wecom", "discord", "slack", "teams", "google_chat", "qqbot", "line", "twilio_sms", "whatsapp_cloud"):
            path = directory / (channel + "-" + "A" * 26 + ".secret")
            self.write(path, "synthetic managed credential")
            path.chmod(0o600)
            paths.append(path)
        for source in sources:
            self.assertEqual(self.run_purge_credential_guard(source).returncode, 0, source)
        target = paths[-1]
        target.chmod(0o644)
        for source in sources:
            self.assertNotEqual(self.run_purge_credential_guard(source).returncode, 0)
        target.chmod(0o600)
        linked = directory / ("slack-" + "B" * 26 + ".secret")
        os.link(target, linked)
        for source in sources:
            self.assertNotEqual(self.run_purge_credential_guard(source).returncode, 0)
        linked.unlink()
        linked.symlink_to(target)
        for source in sources:
            self.assertNotEqual(self.run_purge_credential_guard(source).returncode, 0)
        linked.unlink()
        for target, limit in ((paths[5], 8192), (paths[-1], 16384)):
            target.write_text("x" * limit)
            for source in sources:
                self.assertEqual(self.run_purge_credential_guard(source).returncode, 0)
            target.write_text("x" * (limit + 1))
            for source in sources:
                self.assertNotEqual(self.run_purge_credential_guard(source).returncode, 0)
            target.write_text("synthetic managed credential")
        self.assert_no_mutations()

    def test_purge_guard_runs_again_before_recursive_removal(self):
        for source in ("scripts/uninstall.sh", "packaging/debian/postrm"):
            text = (REPO / source).read_text()
            self.assertGreaterEqual(text.count("purge_native_credentials_guard || exit 1"), 2)
            self.assertLess(text.rindex("purge_native_credentials_guard || exit 1"), text.index("rm -rf --one-file-system"))
        helper = (REPO / "scripts/manage-remove.sh").read_text()
        self.assertIn("purge_native_credentials_guard || remove_die", helper)
        self.assertGreaterEqual(helper.count("remove_validate_purge"), 3)

    def test_purge_guard_rejects_wrong_service_identity_and_excessive_managed_files(self):
        self.env["MOCK_NATIVE_TRUSTED_DIR"] = "1"
        directory = self.root / "etc/noderampart/secrets"
        directory.mkdir()
        directory.chmod(0o750)
        target = directory / ("slack-" + "A" * 26 + ".secret")
        self.write(target, "synthetic managed credential")
        target.chmod(0o600)
        sources = ("scripts/uninstall.sh", "packaging/debian/postrm", "scripts/manage-remove.sh")
        self.env["MOCK_NATIVE_SERVICE_UID"] = str(os.getuid() + 1)
        for source in sources:
            self.assertNotEqual(self.run_purge_credential_guard(source).returncode, 0)
        del self.env["MOCK_NATIVE_SERVICE_UID"]
        alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"
        for index in range(128):
            generation = "Z" + alphabet[index // 32] + alphabet[index % 32] + "A" * 23
            path = directory / ("slack-" + generation + ".secret")
            self.write(path, "synthetic managed credential")
            path.chmod(0o600)
        for source in sources:
            result = self.run_purge_credential_guard(source)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("managed file limit", result.stderr)
        self.assertTrue(target.is_file())
        self.assert_no_mutations()

    def test_normal_removal_keeps_manual_native_credentials(self):
        path = self.root / "etc/noderampart/secrets/slack-user.secret"
        self.write(path, "SYNTHETIC_PRIVATE_WEBHOOK_CREDENTIAL")
        result = self.run_script("scripts/uninstall.sh")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(path.read_text(), "SYNTHETIC_PRIVATE_WEBHOOK_CREDENTIAL")

    def test_stop_failures_prevent_removal(self):
        self.env["MOCK_STOP_FAIL"] = "1"
        for source, arg in (("scripts/uninstall.sh", "--purge"), ("packaging/debian/postrm", "purge"),
                            ("packaging/debian/prerm", "remove")):
            with self.subTest(source=source):
                self.assertNotEqual(self.run_script(source, arg).returncode, 0)
                self.assertFalse(any(command[0] in ("rm", "userdel", "groupdel") for command in self.commands()))

    def test_purge_rejects_nested_same_device_mount_before_any_removal(self):
        # The last purge target contains a bind mount with the same device as /.
        nested = self.root / "var/cache/noderampart/nested bind"
        nested.mkdir()
        canary = nested / "canary"
        canary.write_text("must survive")
        encoded_mount = str(nested).replace(" ", "\\040")
        self.write(self.root / "proc/self/mountinfo",
                   "1 0 0:1 / / rw - tmpfs tmpfs rw\n"
                   + f"2 1 0:1 /canary {encoded_mount} rw - tmpfs tmpfs rw\n")
        for source, arg in (("scripts/uninstall.sh", "--purge"), ("packaging/debian/postrm", "purge")):
            with self.subTest(source=source):
                self.assertNotEqual(self.run_script(source, arg).returncode, 0)
                self.assertEqual(canary.read_text(), "must survive")
                self.assertTrue((self.root / "etc/noderampart/config.json").is_file())
                self.assertTrue((self.root / "var/lib/noderampart/state").is_file())
                self.assert_no_mutations()

    def test_purge_rejects_missing_empty_or_malformed_mountinfo(self):
        mountinfo = self.root / "proc/self/mountinfo"
        for content in (None, "", "malformed record\n", "1 0 0:1 / / rw invalid tmpfs tmpfs rw\n",
                        "invalid 0 0:1 / / rw - tmpfs tmpfs rw\n",
                        "1 0 0:1 / / rw - tmpfs tmpfs rw\nmalformed record\n"):
            with self.subTest(content=content):
                if content is None:
                    mountinfo.unlink()
                else:
                    mountinfo.write_text(content)
                for source, arg in (("scripts/uninstall.sh", "--purge"), ("packaging/debian/postrm", "purge")):
                    self.assertNotEqual(self.run_script(source, arg).returncode, 0)
                    self.assertTrue((self.root / "var/lib/noderampart/state").is_file())
                    self.assert_no_mutations()

    def test_unavailable_systemd_prevents_purge(self):
        self.env["MOCK_SYSTEMD_UNAVAILABLE"] = "1"
        for source, arg in (("scripts/uninstall.sh", "--purge"), ("packaging/debian/postrm", "purge")):
            self.assertNotEqual(self.run_script(source, arg).returncode, 0)
            self.assertTrue((self.root / "var/lib/noderampart/state").is_file())

    def test_active_units_prevent_purge_even_if_stop_returns_success(self):
        self.env["MOCK_REMAIN_ACTIVE"] = "1"
        for source, arg in (("scripts/uninstall.sh", "--purge"), ("packaging/debian/postrm", "purge")):
            self.assertNotEqual(self.run_script(source, arg).returncode, 0)
            self.assertTrue((self.root / "var/lib/noderampart/state").is_file())

    def test_invalid_uninstall_arguments_and_nonroot_fail_before_mutations(self):
        self.assertNotEqual(self.run_script("scripts/uninstall.sh", "--purge", "extra").returncode, 0)
        self.env["MOCK_UID"] = "1000"
        self.assertNotEqual(self.run_script("scripts/uninstall.sh", "--purge").returncode, 0)
        self.assert_no_mutations()

    def test_uninstall_preserves_state_then_purge_removes_it(self):
        result = self.run_script("scripts/uninstall.sh")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue((self.root / "var/lib/noderampart/state").is_file())
        self.env["MOCK_LOAD_STATE"] = "not-found"
        result = self.run_script("scripts/uninstall.sh", "--purge")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse((self.root / "var/lib/noderampart").exists())

    def test_debian_purge_handles_removed_units(self):
        self.env["MOCK_LOAD_STATE"] = "not-found"
        result = self.run_script("packaging/debian/postrm", "purge")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse((self.root / "var/lib/noderampart").exists())

    def test_account_removal_failures_are_reported(self):
        self.env["MOCK_USERDEL_FAIL"] = "1"
        result = self.run_script("scripts/uninstall.sh", "--purge")
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn("service accounts removed", result.stdout)

    def test_debian_alpha_package_version(self):
        self.write(self.project / "VERSION", "0.1.0-alpha\n")
        result = self.run_script("scripts/build-deb.sh")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("Version: 0.1.0~alpha\n", (self.lab / "deb-control").read_text())
        if shutil.which("dpkg"):
            result = subprocess.run([shutil.which("dpkg"), "--compare-versions", "0.1.0~alpha", "lt", "0.1.0"], check=False)
            self.assertEqual(result.returncode, 0)
            # Old preview packages need an explicit one-time version-format migration.
            result = subprocess.run([shutil.which("dpkg"), "--compare-versions", "0.1.0-alpha", "gt", "0.1.0~alpha"], check=False)
            self.assertEqual(result.returncode, 0)

    def test_public_alpha_suffix_native_versions_keep_release_order(self):
        native_versions = []
        for suffix in ('', '.1', '.2', '.3', '.4', '.5', '.6', '.7', '.8', '.9'):
            with self.subTest(suffix=suffix):
                version = '0.4.0-alpha' + suffix
                self.write(self.project / 'VERSION', version + '\n')
                result = self.run_script('scripts/build-deb.sh')
                self.assertEqual(result.returncode, 0, result.stderr)
                native = '0.4.0~alpha' + suffix
                self.assertIn('Version: ' + native + '\n', (self.lab / 'deb-control').read_text())
                self.assertTrue(any(row[0] == 'dpkg-deb' and row[-1].endswith('noderampart_' + native + '_amd64.deb') for row in self.commands()))
                native_versions.append(native)
                self.prepare_rpm_source(version)
                result = self.run_script('scripts/build-rpm.sh')
                self.assertEqual(result.returncode, 0, result.stderr)
                spec = (self.lab / 'rpm-spec').read_text()
                release_number = 1 if not suffix else int(suffix[1:]) + 1
                self.assertIn('Release:        0.alpha.' + str(release_number) + '%{?dist}\n', spec)
                self.assertIn('%global noderampart_version ' + version + '\n', spec)
                self.assertIn('internal/version.Version=%{noderampart_version}', spec)
                self.assertTrue(all(name.startswith('NodeRampart-' + version + '/') or name == 'NodeRampart-' + version for name in json.loads((self.lab / 'rpm-source-files.json').read_text())))
        if shutil.which('dpkg'):
            for previous, current in zip(native_versions, native_versions[1:] + ['0.4.0']):
                result = subprocess.run([shutil.which('dpkg'), '--compare-versions', previous, 'lt', current], check=False)
                self.assertEqual(result.returncode, 0)

    def test_rpm_source_package_retains_build_provenance(self):
        self.env.update(COMMIT="c84af25a7261", BUILD_DATE="2026-09-09T00:00:00Z")
        self.prepare_rpm_source("0.1.0-alpha")
        self.write(self.project / "docs/ai/private-guidance.md", "synthetic local guidance, do not distribute\n")
        self.write(self.project / "docs/public-guide.md", "synthetic public guide\n")
        self.write(self.project / "configs/.env.local", "SYNTHETIC_NOT_A_SECRET=review-canary\n")
        self.write(self.project / "scripts/unlisted-local-file.py", "synthetic ignored local source\n")
        self.write(self.project / "vendor/untrusted-local.go", "synthetic old vendor file\n")
        (self.project / "configs/unlisted-symlink").symlink_to(self.project / "configs/.env.local")
        result = self.run_script("scripts/build-rpm.sh")
        self.assertEqual(result.returncode, 0, result.stderr)
        spec = (self.lab / "rpm-spec").read_text()
        self.assertIn("%global noderampart_commit c84af25a7261\n", spec)
        self.assertIn("%global noderampart_build_date 2026-09-09T00:00:00Z\n", spec)
        self.assertIn("internal/version.Commit=%{noderampart_commit}", spec)
        self.assertIn("internal/version.BuildDate=%{noderampart_build_date}", spec)
        names = json.loads((self.lab / "rpm-source-files.json").read_text())
        self.assertTrue(any(name.endswith("/docs/public-guide.md") for name in names))
        self.assertFalse(any("/docs/ai" in name for name in names))
        self.assertFalse(any(".env.local" in name or "unlisted" in name or "untrusted-local" in name for name in names))
        commands = self.commands()
        self.assertLess(commands.index(["go", "mod", "verify"]), next(i for i, row in enumerate(commands) if row[:3] == ["go", "mod", "vendor"]))

    def test_rpm_manifest_rejects_missing_symlink_and_special_files(self):
        self.prepare_rpm_source()
        target = self.project / "configs/noderampart.json"
        original = target.read_text()
        for kind in ("missing", "symlink", "directory", "fifo"):
            with self.subTest(kind=kind):
                target.unlink()
                if kind == "symlink": target.symlink_to(self.project / "go.mod")
                elif kind == "directory": target.mkdir()
                elif kind == "fifo": os.mkfifo(target)
                result = self.run_script("scripts/build-rpm.sh")
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("Source staging", result.stderr)
                self.assertFalse(any(row[0] == "rpmbuild" or row[:2] == ["go", "mod"] for row in self.commands()))
                (self.lab / "commands.jsonl").unlink()
                if kind == "directory": target.rmdir()
                elif kind != "missing": target.unlink()
                self.write(target, original)

    def test_rpm_manifest_rejects_escape_duplicate_private_and_symlinked_parent(self):
        self.prepare_rpm_source()
        manifest = self.project / "packaging/source-files.txt"
        original = manifest.read_text()
        for extra in ("../outside", "/absolute", "configs/../go.mod", "go.mod", "configs/.env.local", "AGENTS.md", "docs/ai/guide.md"):
            with self.subTest(extra=extra):
                manifest.write_text(original + extra + "\n")
                result = self.run_script("scripts/build-rpm.sh")
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse(any(row[0] == "rpmbuild" or row[:2] == ["go", "mod"] for row in self.commands()))
                (self.lab / "commands.jsonl").unlink()
        manifest.write_text(original)
        (self.project / "configs").rename(self.project / "configs-real")
        (self.project / "configs").symlink_to(self.project / "configs-real")
        result = self.run_script("scripts/build-rpm.sh")
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(any(row[0] == "rpmbuild" or row[:2] == ["go", "mod"] for row in self.commands()))

    def test_rpm_vendor_requires_successful_module_verification(self):
        self.prepare_rpm_source()
        self.env["MOCK_MODULE_VERIFY_FAIL"] = "1"
        result = self.run_script("scripts/build-rpm.sh")
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(any(row[0] == "rpmbuild" or row[:3] == ["go", "mod", "vendor"] for row in self.commands()))

    def test_reviewed_manifest_stages_current_product_without_git(self):
        destination = self.lab / "reviewed-source"
        destination.mkdir()
        result = subprocess.run([sys.executable, str(REPO / "scripts/stage-source.py"), str(REPO), str(destination)],
                                text=True, capture_output=True, check=False, timeout=15)
        self.assertEqual(result.returncode, 0, result.stderr)
        expected = {line for line in (REPO / "packaging/source-files.txt").read_text().splitlines() if line and not line.startswith("#")}
        self.assertEqual({str(path.relative_to(destination)) for path in destination.rglob("*") if path.is_file()}, expected)
        for excluded in ("AGENTS.md", "STATUS.md", ".git", "docs/ai"):
            self.assertFalse((destination / excluded).exists())
        second = self.lab / "source-without-git"
        second.mkdir()
        result = subprocess.run([sys.executable, str(destination / "scripts/stage-source.py"), str(destination), str(second)],
                                text=True, capture_output=True, check=False, timeout=15)
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_debian_arm64_package_checks_binary_architecture(self):
        self.env.update(ARCH="arm64", MOCK_GOARCH="arm64")
        self.write(self.project / "VERSION", "0.4.0-alpha\n")
        result = self.run_script("scripts/build-deb.sh")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("Architecture: arm64\n", (self.lab / "deb-control").read_text())
        self.env["MOCK_GOARCH"] = "amd64"
        result = self.run_script("scripts/build-deb.sh")
        self.assertNotEqual(result.returncode, 0)

    def test_source_uninstall_removes_every_bundled_license_and_patent_notice(self):
        (self.project / "third_party/licenses/example").unlink()
        expected = {path.name for path in (REPO / "third_party/licenses").iterdir() if path.is_file()}
        for name in expected:
            self.write(self.project / "third_party/licenses" / name, "synthetic license fixture\n")
        result = self.run_script("scripts/install.sh")
        self.assertEqual(result.returncode, 0, result.stderr)
        installed = self.root / "usr/local/share/doc/noderampart/third-party"
        self.assertEqual({path.name for path in installed.iterdir()}, expected)
        result = self.run_script("scripts/uninstall.sh")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(installed.exists())
        self.assertTrue((self.root / "var/lib/noderampart/state").is_file())

    def test_rpm_arm64_target_is_explicit(self):
        self.env.update(ARCH="arm64", COMMIT="c84af25a7261", BUILD_DATE="2026-09-12T00:00:00Z")
        self.prepare_rpm_source()
        result = self.run_script("scripts/build-rpm.sh")
        self.assertEqual(result.returncode, 0, result.stderr)
        command = [row for row in self.commands() if row[0] == "rpmbuild"]
        self.assertEqual(len(command), 1)
        self.assertEqual(command[0][1:3], ["--target", "aarch64"])
        spec = (self.lab / "rpm-spec").read_text()
        self.assertIn("aarch64) export GOARCH=arm64", spec)
        self.assertIn("scripts/manage-remove.sh", spec)

    def test_debian_remove_cleans_only_managed_units_and_deconfigure_preserves_them(self):
        unit = self.root / "etc/systemd/system/noderampart-geoip-update.timer"
        own = self.root / "etc/systemd/system/noderampartd.service.d/90-noderampart-managed.conf"
        other = own.parent / "administrator.conf"
        for path in (unit, own, other):
            self.write(path, "retained fixture\n")
        result = self.run_script("packaging/debian/prerm", "deconfigure")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(unit.is_file())
        self.assertTrue(own.is_file())
        result = self.run_script("packaging/debian/prerm", "remove")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(unit.exists())
        self.assertFalse(own.exists())
        self.assertTrue(other.is_file())
        self.assertTrue((self.root / "var/lib/noderampart/state").is_file())

    @unittest.skipUnless(shutil.which("rpmspec"), "native RPM tooling is unavailable")
    def test_native_rpm_does_not_generate_empty_debug_subpackages(self):
        # Let RPM expand its real package macros; no builds or scriptlets run.
        result = subprocess.run([shutil.which("rpmspec"), "-q", "--qf", "%{NAME}\\n",
                                 "--define", "_enable_debug_packages 1",
                                 "--define", "_debugsource_packages 1",
                                 str(REPO / "packaging/rpm/noderampart.spec")],
                                text=True, capture_output=True, check=False)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.splitlines(), ["noderampart"])

    @unittest.skipUnless(RPMBUILD and RPM and RPMSPEC, "native RPM build tooling is unavailable")
    def test_native_rpm_header_keeps_pretrans_prein_and_sysusers_payload(self):
        # A host may have RPM tools without Fedora's systemd-rpm-macros.
        # Only this full build needs these paths; Lua/rpmspec checks still run.
        unitdir = rpm_path_macro("_unitdir")
        sysusersdir = rpm_path_macro("_sysusersdir")
        if unitdir is None or sysusersdir is None:
            self.skipTest("native RPM systemd/sysusers macros are unavailable")
        # Build the real spec without installing it. A synthetic Go command
        # creates inert binaries, keeping this header regression fast/offline.
        version = (REPO / "VERSION").read_text().strip()
        self.prepare_rpm_source(version)
        topdir = self.lab / "native-rpm"
        for directory in ("BUILD", "BUILDROOT", "RPMS", "SOURCES", "SPECS", "SRPMS"):
            (topdir / directory).mkdir(parents=True)
        with tarfile.open(topdir / "SOURCES" / ("noderampart-" + version + ".tar.gz"), "w:gz") as archive:
            archive.add(self.project, arcname="NodeRampart-" + version)
        spec = topdir / "SPECS/noderampart.spec"
        self.write(spec, (REPO / "packaging/rpm/noderampart.spec").read_text())
        tools = self.lab / "native-tools"
        fake_go = """#!/bin/sh
set -eu
[ "$1" = build ] || exit 1
output=
while [ "$#" -gt 0 ]; do
  if [ "$1" = -o ]; then shift; output=$1; fi
  shift
done
[ -n "$output" ] || exit 1
mkdir -p "$(dirname "$output")"
printf '#!/bin/sh\\nexit 0\\n' > "$output"
chmod 0755 "$output"
"""
        self.write(tools / "go", fake_go, executable=True)
        native_env = dict(os.environ, PATH=str(tools) + ":/usr/bin:/bin")
        result = subprocess.run([RPMBUILD, "--nodeps", "-bb", "--define", "_topdir " + str(topdir),
                                 "--define", "_buildhost packaging-fixture.invalid", str(spec)],
                                env=native_env, text=True, capture_output=True, check=False, timeout=120)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        packages = list((topdir / "RPMS").rglob("*.rpm"))
        self.assertEqual(len(packages), 1, packages)

        def query(format):
            result = subprocess.run([RPM, "-qp", "--qf", format, str(packages[0])],
                                    text=True, capture_output=True, check=False, timeout=15)
            self.assertEqual(result.returncode, 0, result.stderr)
            return result.stdout

        pretrans = query("%{PRETRANS}")
        self.assertEqual(pretrans.strip(), self.rpm_pretrans_body().strip())
        self.assertEqual(query("[%{PRETRANSPROG}\\n]").splitlines(), ["<lua>"])
        self.assert_rpm_pretrans_read_only(pretrans)
        prein = query("%{PREIN}")
        self.assertIn("getent group noderampart", prein)
        self.assertIn("useradd", prein)
        self.assertEqual(query("[%{PREINPROG}\\n]").splitlines(), ["/bin/sh"])
        self.assertIn(sysusersdir + "/noderampart.conf", query("[%{FILENAMES}\\n]").splitlines())
        # RPMSENSE_PRETRANS is bit 7 in RPM's public rpmds.h. Only internal
        # rpmlib capabilities may be required before transaction payloads exist.
        dependencies = query("[%{REQUIRENAME}\\t%{REQUIREFLAGS}\\n]").splitlines()
        external_pretrans = [name for name, flags in (line.split("\t") for line in dependencies)
                             if int(flags) & (1 << 7) and not name.startswith("rpmlib(")]
        self.assertEqual(external_pretrans, [])
        self.assertEqual(self.run_rpm_pretrans(body=pretrans), ("OK", ""))
        with self.rpm_guard_object("usr/local/libexec/noderampart/manage-remove", "dangling"):
            outcome, message = self.run_rpm_pretrans(body=pretrans)
            self.assertEqual(outcome, "BLOCKED", message)
            self.assertIn("conflicts", message)

class RPMMacroCapabilityTests(unittest.TestCase):
    def test_rpm_path_macro_requires_expanded_absolute_path(self):
        with mock.patch(__name__ + ".RPM", "/fixture/rpm"):
            for value in ("", "%{_unitdir}", "relative/path", "/%{unresolved}", "/%_unresolved",
                          "/usr/lib\n/systemd/system", "/usr/lib\x00/systemd/system"):
                with self.subTest(value=value), mock.patch(__name__ + ".subprocess.run") as run:
                    run.return_value = subprocess.CompletedProcess([], 0, value, "")
                    self.assertIsNone(rpm_path_macro("_unitdir"))
            with mock.patch(__name__ + ".subprocess.run") as run:
                run.return_value = subprocess.CompletedProcess([], 0, " /usr/lib/systemd/system\n", "")
                self.assertEqual(rpm_path_macro("_unitdir"), "/usr/lib/systemd/system")
                run.assert_called_once_with(["/fixture/rpm", "--eval", "%{_unitdir}"],
                                            text=True, capture_output=True, check=False, timeout=5)

    def test_rpm_path_macro_fails_closed_on_tool_errors(self):
        with mock.patch(__name__ + ".RPM", None), mock.patch(__name__ + ".subprocess.run") as run:
            self.assertIsNone(rpm_path_macro("_unitdir"))
            run.assert_not_called()
        with mock.patch(__name__ + ".RPM", "/fixture/rpm"):
            for error in (OSError("synthetic unavailable tool"), subprocess.TimeoutExpired("rpm", 5),
                          subprocess.SubprocessError("synthetic process failure")):
                with self.subTest(error=type(error).__name__), mock.patch(__name__ + ".subprocess.run", side_effect=error):
                    self.assertIsNone(rpm_path_macro("_sysusersdir"))
            with mock.patch(__name__ + ".subprocess.run") as run:
                run.return_value = subprocess.CompletedProcess([], 1, "/usr/lib/sysusers.d", "")
                self.assertIsNone(rpm_path_macro("_sysusersdir"))

    def test_native_header_skips_only_when_required_path_macro_is_unavailable(self):
        method = PackagingTests.test_native_rpm_header_keeps_pretrans_prein_and_sysusers_payload
        method = getattr(method, "__wrapped__", method)
        for paths in ((None, "/usr/lib/sysusers.d"), ("/usr/lib/systemd/system", None), (None, None)):
            with self.subTest(paths=paths), mock.patch(__name__ + ".rpm_path_macro", side_effect=paths) as macro:
                native_test = PackagingTests("test_native_rpm_header_keeps_pretrans_prein_and_sysusers_payload")
                # Missing capabilities must stop before creating/building a package.
                with self.assertRaisesRegex(unittest.SkipTest, "native RPM systemd/sysusers macros are unavailable"):
                    method(native_test)
                self.assertEqual(macro.call_args_list, [mock.call("_unitdir"), mock.call("_sysusersdir")])


if __name__ == "__main__":
    unittest.main(verbosity=2)
