#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Bounded AF_PACKET fixture for an explicitly authorized disposable Linux VM.

Build beforehand (the harness never builds or downloads code):
  CGO_ENABLED=0 go test -c -o /tmp/noderampart-sensor.test ./internal/sensor
Run only in the disposable VM:
  sudo python3 scripts/test-netns.py --authorized-disposable-lab \
    --test-binary /tmp/noderampart-sensor.test
Requires Python 3, iproute2, systemd-detect-virt, and root. Twenty tiny UDP and
twenty TCP request/reply exchanges stay within two new namespaces. No parent
routes, firewall settings, services, or production capabilities are changed.
The execution budget is 22 seconds, plus at most 5 seconds of cleanup. This is
one isolated capture scenario, not the privileged packaging/lifecycle matrix.
Run --self-test without privileges to check fail-closed authorization guards.
"""

import argparse
import os
from pathlib import Path
import secrets
import shutil
import signal
import subprocess
import sys
import tempfile
import time


class LabFailure(RuntimeError):
    pass


def authorize_lab(authorized, deadline):
    if not authorized:
        raise LabFailure("explicit --authorized-disposable-lab is required")
    if os.geteuid() != 0:
        raise LabFailure("the fixture requires root in an authorized disposable VM")
    try:
        result = subprocess.run(
            ["/usr/bin/systemd-detect-virt", "--vm", "--quiet"],
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
            timeout=max(0.001, min(2.0, deadline - time.monotonic())), check=False)
    except (OSError, subprocess.TimeoutExpired) as error:
        raise LabFailure("VM detection is unavailable") from error
    if result.returncode != 0:
        raise LabFailure("the fixture requires a detected VM; containers and hosts are refused")


def run_fixture(binary, deadline):
    ip = shutil.which("ip")
    if not ip:
        raise LabFailure("iproute2 is required")
    namespaces = []
    processes = []

    def remaining():
        value = deadline - time.monotonic()
        if value <= 0:
            raise LabFailure("fixture execution deadline exceeded")
        return value

    def command(arguments):
        result = subprocess.run([ip, *arguments], stdout=subprocess.DEVNULL,
                                stderr=subprocess.DEVNULL,
                                timeout=min(3.0, remaining()), check=False)
        if result.returncode:
            raise LabFailure("isolated namespace setup failed")

    def interrupted(_number, _frame):
        raise LabFailure("fixture interrupted")

    previous = {number: signal.signal(number, interrupted)
                for number in (signal.SIGINT, signal.SIGTERM)}
    try:
        with tempfile.TemporaryDirectory(prefix="noderampart-netns-") as directory:
            suffix = secrets.token_hex(12)
            client, server = "nr-test-" + suffix + "-c", "nr-test-" + suffix + "-s"
            for name in (client, server):
                # A failed create never authorizes deleting an existing namespace.
                command(["netns", "add", name])
                namespaces.append(name)
            # Both veth endpoints are created inside the new namespaces.
            command(["-n", client, "link", "add", "nrclient", "type", "veth",
                     "peer", "name", "nrserver", "netns", server])
            for name, interface, address in ((client, "nrclient", "192.0.2.1/30"),
                                             (server, "nrserver", "192.0.2.2/30")):
                command(["-n", name, "address", "add", address, "dev", interface])
                command(["-n", name, "link", "set", "lo", "up"])
                command(["-n", name, "link", "set", interface, "up"])

            def start(role, namespace):
                env = {**os.environ,
                       "NODERAMPART_NETNS_LAB": "authorized-disposable-vm-v1",
                       "NODERAMPART_NETNS_ROLE": role,
                       "NODERAMPART_NETNS_NAME": namespace,
                       "NODERAMPART_NETNS_DIRECTORY": directory}
                process = subprocess.Popen(
                    [ip, "netns", "exec", namespace, str(binary),
                     "-test.run=^TestNetNSNormalReplies$", "-test.v", "-test.timeout=18s"],
                    env=env)
                processes.append(process)
                return process

            server_process = start("server", server)
            ready = Path(directory, "server-ready")
            while not ready.is_file():
                remaining()
                if server_process.poll() is not None:
                    raise LabFailure("isolated server did not become ready")
                time.sleep(min(0.025, remaining()))
            client_process = start("client", client)
            if client_process.wait(timeout=remaining()) != 0:
                raise LabFailure("isolated capture/reply assertions failed")
            if server_process.wait(timeout=remaining()) != 0:
                raise LabFailure("isolated server exchanges failed")
    finally:
        # Each Popen is our own unreaped child; no host PID discovery or killall.
        for number in previous:
            signal.signal(number, signal.SIG_IGN)
        cleanup_failed = False
        for process in processes:
            if process.poll() is None:
                process.terminate()
                try:
                    process.wait(timeout=0.25)
                except subprocess.TimeoutExpired:
                    process.kill()
                    try:
                        process.wait(timeout=0.25)
                    except subprocess.TimeoutExpired:
                        cleanup_failed = True
        for name in reversed(namespaces):
            try:
                result = subprocess.run([ip, "netns", "delete", name],
                                        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                                        timeout=2, check=False)
                cleanup_failed |= result.returncode != 0
            except (OSError, subprocess.TimeoutExpired):
                cleanup_failed = True
        for number, handler in previous.items():
            signal.signal(number, handler)
        if cleanup_failed:
            raise LabFailure("fixture cleanup failed; discard the disposable VM")


def self_test():
    import unittest
    from unittest import mock

    class GuardTests(unittest.TestCase):
        def test_missing_authorization_never_executes(self):
            with mock.patch.object(subprocess, "run") as execute:
                with self.assertRaises(LabFailure):
                    authorize_lab(False, time.monotonic() + 2)
                execute.assert_not_called()

        def test_nonroot_never_executes(self):
            with mock.patch.object(os, "geteuid", return_value=1000), \
                    mock.patch.object(subprocess, "run") as execute:
                with self.assertRaises(LabFailure):
                    authorize_lab(True, time.monotonic() + 2)
                execute.assert_not_called()

        def test_host_or_container_refused(self):
            with mock.patch.object(os, "geteuid", return_value=0), \
                    mock.patch.object(subprocess, "run", return_value=mock.Mock(returncode=1)):
                with self.assertRaises(LabFailure):
                    authorize_lab(True, time.monotonic() + 2)

        def test_detection_failure_refused(self):
            for error in (OSError(), subprocess.TimeoutExpired("detect", 2)):
                with self.subTest(error=type(error).__name__), \
                        mock.patch.object(os, "geteuid", return_value=0), \
                        mock.patch.object(subprocess, "run", side_effect=error):
                    with self.assertRaises(LabFailure):
                        authorize_lab(True, time.monotonic() + 2)

        def test_vm_detection_is_required(self):
            with mock.patch.object(os, "geteuid", return_value=0), \
                    mock.patch.object(subprocess, "run", return_value=mock.Mock(returncode=0)) as execute:
                authorize_lab(True, time.monotonic() + 2)
                self.assertEqual(execute.call_args.args[0],
                                 ["/usr/bin/systemd-detect-virt", "--vm", "--quiet"])
                self.assertLessEqual(execute.call_args.kwargs["timeout"], 2)

        def test_failed_create_cleans_only_successful_creates(self):
            # All external commands are mocked. A failed second create must not
            # authorize deleting that name (which could already have existed).
            with mock.patch.object(shutil, "which", return_value="/mock/ip"), \
                    mock.patch.object(secrets, "token_hex", return_value="a" * 24), \
                    mock.patch.object(subprocess, "run", side_effect=[
                        mock.Mock(returncode=0), mock.Mock(returncode=1), mock.Mock(returncode=0)]) as execute:
                with self.assertRaises(LabFailure):
                    run_fixture(Path("/mock/test-binary"), time.monotonic() + 2)
                self.assertEqual([call.args[0] for call in execute.call_args_list], [
                    ["/mock/ip", "netns", "add", "nr-test-" + "a" * 24 + "-c"],
                    ["/mock/ip", "netns", "add", "nr-test-" + "a" * 24 + "-s"],
                    ["/mock/ip", "netns", "delete", "nr-test-" + "a" * 24 + "-c"],
                ])

    result = unittest.TextTestRunner().run(unittest.defaultTestLoader.loadTestsFromTestCase(GuardTests))
    return 0 if result.wasSuccessful() else 1


def main(arguments=None):
    arguments = sys.argv[1:] if arguments is None else arguments
    if arguments == ["--self-test"]:
        return self_test()
    deadline = time.monotonic() + 22
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--authorized-disposable-lab", action="store_true", required=True)
    parser.add_argument("--test-binary", type=Path, required=True)
    args = parser.parse_args(arguments)
    try:
        authorize_lab(args.authorized_disposable_lab, deadline)
        binary = args.test_binary.resolve(strict=True)
        if not binary.is_file() or not os.access(binary, os.X_OK):
            raise LabFailure("test binary must be a prebuilt executable regular file")
        run_fixture(binary, deadline)
    except (LabFailure, OSError, subprocess.TimeoutExpired) as error:
        # Do not dump command output, process lists, or host configuration.
        message = str(error) if isinstance(error, LabFailure) else "fixture command failed or timed out"
        print("FAIL: " + message, file=sys.stderr)
        return 1
    print("PASS: isolated AF_PACKET tuple capture and normal TCP/UDP reply suppression")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
