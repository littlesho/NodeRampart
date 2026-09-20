#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Exercise maintainer scripts in a temporary filesystem with mocked host commands.

No packages are installed and no real services, accounts, or system paths change.
Run with: python3 scripts/test-packaging.py
"""

import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
import unittest


REPO = Path(__file__).resolve().parent.parent
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
    (lab / "deb-output").write_text(pathlib.Path(args[-1]).name)
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
                     "dpkg-query", "rpm", "deb-systemd-helper", "deb-systemd-invoke"):
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
            content = content.split("\n%" + section + "\n", 1)[1].split("\n%", 1)[0]
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

    def commands(self):
        path = self.lab / "commands.jsonl"
        return [json.loads(line) for line in path.read_text().splitlines()] if path.exists() else []

    def prepare_rpm_source(self, version="0.4.0-alpha.4"):
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

    def test_source_transition_checks_ownership_and_preserves_state_and_dropins(self):
        result = self.run_script("scripts/install.sh")
        self.assertEqual(result.returncode, 0, result.stderr)
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

    def test_candidate_debian_mapping_and_native_order(self):
        self.write(self.project / "VERSION", "0.4.0-alpha.4\n")
        result = self.run_script("scripts/build-deb.sh")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("Version: 0.4.0~alpha.4\n", (self.lab / "deb-control").read_text())
        self.assertEqual((self.lab / "deb-output").read_text(), "noderampart_0.4.0-alpha.4_amd64.deb")
        if shutil.which("dpkg"):
            for lower, higher in (("0.4.0~alpha.3", "0.4.0~alpha.4"), ("0.4.0~alpha.4", "0.4.0")):
                self.assertEqual(subprocess.run([shutil.which("dpkg"), "--compare-versions", lower, "lt", higher]).returncode, 0)

    def test_candidate_rpm_mapping_preserves_full_program_version(self):
        self.prepare_rpm_source()
        result = self.run_script("scripts/build-rpm.sh")
        self.assertEqual(result.returncode, 0, result.stderr)
        spec = (self.lab / "rpm-spec").read_text()
        for expected in ("%global noderampart_version 0.4.0-alpha.4", "Version:        0.4.0",
                         "Release:        0.alpha.5%{?dist}", "Source0:        %{name}-%{noderampart_version}.tar.gz",
                         "%autosetup -n NodeRampart-%{noderampart_version}",
                         "internal/version.Version=%{noderampart_version}"):
            self.assertIn(expected, spec)
        names = json.loads((self.lab / "rpm-source-files.json").read_text())
        self.assertTrue(all(name.split('/')[0] == 'NodeRampart-0.4.0-alpha.4' for name in names))

    @unittest.skipUnless(shutil.which("rpm"), "native RPM tooling is unavailable")
    def test_candidate_native_rpm_order(self):
        for fedora in (43, 44):
            for lower, higher in (("0.alpha.4", "0.alpha.5"), ("0.alpha.5", "1")):
                result = subprocess.run([shutil.which("rpm"), "--eval",
                    '%{lua: print(rpm.vercmp("0.4.0-' + lower + '.fc' + str(fedora) +
                    '", "0.4.0-' + higher + '.fc' + str(fedora) + '"))}'],
                    text=True, capture_output=True, check=False)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(result.stdout.strip(), '-1')

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
        self.write(self.project / "VERSION", "0.4.0-alpha.4\n")
        result = self.run_script("scripts/build-deb.sh")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("Architecture: arm64\n", (self.lab / "deb-control").read_text())
        self.assertIn("Version: 0.4.0~alpha.4\n", (self.lab / "deb-control").read_text())
        self.assertEqual((self.lab / "deb-output").read_text(), "noderampart_0.4.0-alpha.4_arm64.deb")
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


if __name__ == "__main__":
    unittest.main(verbosity=2)
