#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Unprivileged package-download and removal tests with synthetic fixtures.

Absolute installation paths are redirected into a private directory. No real
package manager, network client, account command or systemctl is executed.
"""
import fcntl
import hashlib
import json
import os
from pathlib import Path
import pty
import re
import shlex
import shutil
import subprocess
import sys
import tempfile
import unittest

REPO = Path(__file__).resolve().parent.parent
MOCK = r'''
import hashlib, json, os, pathlib, subprocess, sys
name = pathlib.Path(sys.argv[0]).name
args = sys.argv[1:]
lab = pathlib.Path(os.environ['BOOTSTRAP_LAB'])
root = lab / 'root'
with (lab / 'commands.jsonl').open('a') as log:
    log.write(json.dumps([name, *args]) + '\n')
with (lab / 'locales.jsonl').open('a') as log:
    log.write(json.dumps({'command': name, 'LC_ALL': os.environ.get('LC_ALL'),
                          'LC_CTYPE': os.environ.get('LC_CTYPE'), 'LANG': os.environ.get('LANG')}) + '\n')
scenario = os.environ.get('SCENARIO', '')
if name == 'id':
    if args == ['-u']: print(os.environ.get('MOCK_UID', '0'))
    else: sys.exit(1)
elif name == 'uname':
    print(os.environ.get('MOCK_MACHINE', 'x86_64') if args == ['-m'] else 'Linux')
elif name == 'curl':
    output = pathlib.Path(args[args.index('--output') + 1])
    headers = pathlib.Path(args[args.index('--dump-header') + 1])
    assert output.is_relative_to(root / 'tmp') and headers.is_relative_to(root / 'tmp')
    if scenario == 'transport': sys.exit(28)
    if scenario in ('redirect_bad', 'redirect_http'):
        destination = 'https://untrusted.example/asset' if scenario == 'redirect_bad' else 'http://github.com/asset'
        headers.write_text('HTTP/1.1 302 Found\r\nLocation: ' + destination + '\r\n\r\n')
        output.write_bytes(b'')
        print('302', end='')
        sys.exit(0)
    if scenario == 'redirect_loop':
        headers.write_text('HTTP/1.1 302 Found\r\nLocation: ' + args[-1] + '\r\n\r\n')
        output.write_bytes(b'')
        print('302', end='')
        sys.exit(0)
    if scenario == '404':
        headers.write_text('HTTP/1.1 404 Not Found\r\n\r\n')
        output.write_bytes(b'not found')
        print('404', end='')
        sys.exit(0)
    headers.write_text('HTTP/1.1 200 OK\r\n\r\n')
    payload = b'synthetic package\n'
    if args[-1].endswith('/SHA256SUMS'):
        digest = hashlib.sha256(payload).hexdigest()
        if scenario == 'hash': digest = '0' * 64
        asset = os.environ.get('MOCK_ASSET', 'noderampart_0.4.0-alpha.5_amd64.deb')
        text = f'{digest}  {asset}\n'
        if scenario == 'duplicate': text *= 2
        if scenario == 'manifest': text += '../not-a-digest bad\n'
        if scenario == 'oversize': text = 'a' * 65537
        if scenario == 'missing_asset': text = f'{digest}  another.deb\n'
        output.write_text(text)
    else: output.write_bytes(payload)
    print('200', end='')
elif name == 'dpkg':
    if args == ['--print-architecture']: print(os.environ.get('MOCK_USER_ARCH', 'amd64'))
    else: sys.exit(1 if scenario == 'downgrade' else 0)
elif name == 'dpkg-query':
    if os.environ.get('MOCK_DEB_INSTALLED'):
        if args[0] == '-S':
            if os.environ.get('MOCK_DEB_UNOWNED'): sys.exit(1)
            print(os.environ.get('MOCK_DEB_OWNER', 'noderampart') + ': ' + args[-1])
        elif '${Version}' in args[-2]: print('9.0.0')
        else: print(os.environ.get('MOCK_DEB_STATUS', 'install ok installed'))
    else: sys.exit(1)
elif name == 'dpkg-deb':
    values = {'Package':'noderampart', 'Version':'0.4.0~alpha.5', 'Architecture':os.environ.get('MOCK_USER_ARCH', 'amd64')}
    print('malformed' if scenario == 'identity' or scenario == 'wrong-' + args[-1].lower() else values[args[-1]])
elif name == 'rpm':
    if '-qp' in args:
        print('malformed' if scenario == 'identity' else os.environ.get('MOCK_RPM_IDENTITY', 'noderampart:0.4.0:0.alpha.6.fc44:x86_64'))
    elif not os.environ.get('MOCK_RPM_INSTALLED'):
        print('package noderampart is not installed')
        sys.exit(1)
    elif '--qf' in args: print(os.environ.get('MOCK_RPM_VERSION', '0.3.0-0.alpha.1.fc44'))
elif name in ('apt-get', 'dnf'):
    if scenario == 'package_fail': sys.exit(1)
    if args[0] == 'install' and any(a.endswith(('.deb', '.rpm')) for a in args):
        cli = root / 'usr/bin/noderampart'
        cli.parent.mkdir(parents=True, exist_ok=True)
        cli.write_text('#!' + sys.executable + '\n' + pathlib.Path(sys.argv[0]).read_text().split('\n', 1)[1])
        cli.chmod(0o755)
    if args[0] in ('remove', 'purge'):
        if os.environ.get('MOCK_UNLINK_HELPER'): pathlib.Path(os.environ['MOCK_UNLINK_HELPER']).unlink()
        for binary in ('noderampart', 'noderampartd', 'noderampart-sensor'):
            (root / 'usr/bin' / binary).unlink(missing_ok=True)
elif name == 'noderampart':
    if args == ['setup']:
        assert os.isatty(0), 'setup was not attached to controlling tty'
elif name == 'systemctl':
    unit = args[-1]
    stopped = lab / ('stopped-' + unit)
    if args[0] == 'show':
        if '--property=LoadState' in args: print('loaded')
        else: print('active' if scenario == 'active' or not stopped.exists() else 'inactive')
    elif args[0] in ('disable', 'stop'):
        if scenario == 'stop_fail': sys.exit(1)
        stopped.touch()
elif name == 'mountpoint':
    sys.exit(0 if args[-1] == os.environ.get('MOCK_MOUNT') else 32)
elif name == 'stat':
    path = pathlib.Path(args[-1])
    info = path.stat()
    fmt = args[args.index('-c') + 1]
    owner = os.environ.get('MOCK_TMP_UID', '0') if path == root / 'tmp' else '0'
    if str(path) == os.environ.get('MOCK_NONROOT_PATH'): owner = '1000'
    print({'%u': owner, '%a': oct(info.st_mode & 0o7777)[2:], '%h:%u':str(info.st_nlink)+':'+owner}[fmt])
elif name in ('rm', 'rmdir'):
    for arg in args:
        if arg.startswith('/'):
            assert pathlib.Path(arg).is_relative_to(lab), 'unsafe mock removal'
    sys.exit(subprocess.run(['/usr/bin/' + name, *args], check=False).returncode)
elif name in ('userdel', 'groupdel'):
    if scenario == 'account_fail': sys.exit(1)
elif name == 'getent': sys.exit(1)
else: raise SystemExit('unexpected mock command: ' + name)
'''


class InstallerTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='noderampart-installer-test-')
        self.addCleanup(self.temp.cleanup)
        self.lab = Path(self.temp.name)
        self.root = self.lab / 'root'
        self.mocks = self.lab / 'mocks'
        self.mocks.mkdir()
        self.env = dict(os.environ, BOOTSTRAP_LAB=str(self.lab))
        for name in ('id', 'uname', 'curl', 'dpkg', 'dpkg-query', 'dpkg-deb', 'rpm', 'apt-get', 'dnf',
                     'systemctl', 'mountpoint', 'stat', 'rm', 'rmdir', 'userdel', 'groupdel', 'getent'):
            self.write(self.mocks / name, '#!' + sys.executable + '\n' + MOCK, 0o755)
        for path in ('run/systemd/system', 'tmp', 'usr/bin', 'etc/noderampart', 'var/lib/noderampart',
                     'var/cache/noderampart', 'etc/systemd/system/noderampartd.service.d'):
            (self.root / path).mkdir(parents=True, exist_ok=True)
        self.write(self.root / 'etc/os-release', 'ID=debian\nVERSION_ID="13"\n')
        self.write(self.root / 'etc/ssl/certs/ca-certificates.crt', 'synthetic trust marker\n')
        self.write(self.root / 'etc/noderampart/config.json', '{"fixture": true}\n')
        self.write(self.root / 'var/lib/noderampart/state', 'retained\n')
        self.write(self.root / 'proc/self/mountinfo', '1 0 0:1 / / rw - tmpfs tmpfs rw\n')

    @staticmethod
    def write(path, body, mode=0o644):
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(body)
        path.chmod(mode)

    def stage(self, script, tty=None):
        content = (REPO / 'scripts' / script).read_text()
        content = re.sub(r'(?<![A-Za-z0-9_/])/(?:etc|usr|lib|var|run|proc|tmp)(?=/|[\s;)"\'])',
                         lambda m: str(self.root) + m[0], content)
        content = re.sub(r'^  PATH=.*$', '  PATH=' + shlex.quote(str(self.mocks)) + ':/usr/sbin:/usr/bin:/sbin:/bin', content, flags=re.M)
        if tty:
            content = content.replace('/dev/tty', tty)
        else:
            content = content.replace('/dev/tty', str(self.root / 'no-tty/tty'))
        output = self.lab / script
        self.write(output, content, 0o755)
        return output

    def run_script(self, script='bootstrap.sh', *args, tty=None, pipe=False):
        staged = self.stage(script, tty)
        if pipe:
            return subprocess.run(['/bin/sh', '-s', '--', *args], input=staged.read_text(),
                                  env=self.env, text=True, capture_output=True, timeout=15, check=False)
        return subprocess.run(['/bin/sh', str(staged), *args], env=self.env,
                              text=True, capture_output=True, timeout=15, check=False)

    def commands(self):
        path = self.lab / 'commands.jsonl'
        return [json.loads(line) for line in path.read_text().splitlines()] if path.exists() else []

    def assert_no_install(self):
        self.assertFalse(any(row[0] in ('apt-get', 'dnf') for row in self.commands()), self.commands())

    def test_debian_install_downloads_checks_then_uses_dependencies(self):
        result = self.run_script('bootstrap.sh', '--no-setup')
        self.assertEqual(result.returncode, 0, result.stderr)
        commands = self.commands()
        installs = [row for row in commands if row[:2] == ['apt-get', 'install']]
        self.assertEqual(len(installs), 1)
        self.assertIn('Dpkg::Options::=--force-confold', installs[0])
        self.assertEqual([row[0] for row in commands].count('curl'), 2)
        self.assertFalse(list((self.root / 'tmp').iterdir()))

    def test_fedora_fresh_install_preserves_signature_policy(self):
        self.write(self.root / 'etc/os-release', 'ID=fedora\nVERSION_ID=44\n')
        self.env['MOCK_ASSET'] = 'noderampart-0.4.0-0.alpha.6.fc44.x86_64.rpm'
        result = self.run_script('bootstrap.sh', '--non-interactive')
        self.assertEqual(result.returncode, 0, result.stderr)
        install = [row for row in self.commands() if row[:2] == ['dnf', 'install']]
        self.assertEqual(len(install), 1)
        self.assertFalse(any('gpg' in arg for row in install for arg in row))

    def test_fedora_candidate_upgrade_and_downgrade(self):
        self.write(self.root / 'etc/os-release', 'ID=fedora\nVERSION_ID=44\n')
        self.env.update(MOCK_ASSET='noderampart-0.4.0-0.alpha.6.fc44.x86_64.rpm', MOCK_RPM_INSTALLED='1')
        for installed, accepted in (('0.4.0-0.alpha.1.fc44', True),
                                    ('0.4.0-0.alpha.2.fc44', True),
                                    ('0.4.0-0.alpha.3.fc44', True),
                                    ('0.4.0-0.alpha.4.fc44', True),
                                    ('0.4.0-0.alpha.5.fc44', True),
                                    ('0.4.0-0.alpha.6.fc44', True),
                                    ('0.4.0-0.alpha.7.fc44', False)):
            with self.subTest(installed=installed):
                self.env['MOCK_RPM_VERSION'] = installed
                result = self.run_script('bootstrap.sh', '--no-setup')
                self.assertEqual(result.returncode == 0, accepted, result.stderr)
                if not accepted:
                    self.assertIn('downgrade', result.stderr)

    def test_arm64_package_selection(self):
        self.env.update(MOCK_MACHINE='aarch64', MOCK_USER_ARCH='arm64', MOCK_ASSET='noderampart_0.4.0-alpha.5_arm64.deb')
        result = self.run_script('bootstrap.sh', '--no-setup')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(any(row[0] == 'curl' and row[-1].endswith('_arm64.deb') for row in self.commands()))

    def test_download_failures_never_install(self):
        for scenario in ('404', 'transport', 'redirect_bad', 'redirect_http', 'redirect_loop', 'hash', 'duplicate', 'manifest', 'oversize', 'missing_asset', 'identity', 'wrong-package', 'wrong-version', 'wrong-architecture'):
            with self.subTest(scenario=scenario):
                self.env['SCENARIO'] = scenario
                result = self.run_script('bootstrap.sh', '--no-setup')
                self.assertNotEqual(result.returncode, 0)
                self.assert_no_install()
                self.assertFalse(list((self.root / 'tmp').iterdir()))
                (self.lab / 'commands.jsonl').unlink(missing_ok=True)

    def test_missing_terminal_prevents_changes(self):
        result = self.run_script('bootstrap.sh')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('--no-setup', result.stderr)
        self.assert_no_install()

    def test_pipe_setup_uses_separate_terminal(self):
        master, slave = pty.openpty()
        self.addCleanup(os.close, master)
        self.addCleanup(os.close, slave)
        result = self.run_script('bootstrap.sh', tty=os.ttyname(slave), pipe=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn(['noderampart', 'setup'], self.commands())

    def test_tui_locale_is_separate_from_machine_locale(self):
        master, slave = pty.openpty()
        self.addCleanup(os.close, master)
        self.addCleanup(os.close, slave)
        for locale, expected in [
            ({'LC_ALL': 'C', 'LANG': 'en_US.UTF-8'}, 'C.UTF-8'),
            ({'LC_ALL': 'POSIX'}, 'C.UTF-8'),
            ({'LC_CTYPE': 'C', 'LANG': 'zh_CN.UTF-8'}, 'C.UTF-8'),
            ({'LC_ALL': 'C.utf8'}, 'C.utf8'),
            ({'LANG': 'en_US.UTF-8'}, 'en_US.UTF-8'),
            ({'LANG': 'zh_CN.UTF-8'}, 'zh_CN.UTF-8'),
            ({}, 'C.UTF-8'),
            # An explicit legacy encoding reaches the TUI's readable refusal;
            # the installer does not pretend to change the SSH client's encoding.
            ({'LC_ALL': 'en_US.ISO-8859-1'}, 'en_US.ISO-8859-1'),
        ]:
            with self.subTest(locale=locale):
                for key in ('LC_ALL', 'LC_CTYPE', 'LANG'):
                    self.env.pop(key, None)
                self.env.update(locale, PYTHONCOERCECLOCALE='0', PYTHONUTF8='0')
                log = self.lab / 'locales.jsonl'
                log.unlink(missing_ok=True)
                result = self.run_script('bootstrap.sh', tty=os.ttyname(slave), pipe=True)
                self.assertEqual(result.returncode, 0, result.stderr)
                rows = [json.loads(line) for line in log.read_text().splitlines()]
                tui = [row for row in rows if row['command'] == 'noderampart']
                self.assertEqual(len(tui), 1)
                self.assertEqual(tui[0]['LC_ALL'], expected)
                machine = [row for row in rows if row['command'] != 'noderampart']
                self.assertTrue(machine)
                self.assertTrue(all(row['LC_ALL'] == 'C' for row in machine))

    def test_unsupported_version_and_architecture(self):
        result = self.run_script('bootstrap.sh', '--version', 'v9.0.0', '--no-setup')
        self.assertNotEqual(result.returncode, 0)
        self.env['MOCK_MACHINE'] = 'mips'
        result = self.run_script('bootstrap.sh', '--no-setup')
        self.assertNotEqual(result.returncode, 0)
        self.assert_no_install()

    def test_userland_mismatch(self):
        self.env['MOCK_USER_ARCH'] = 'arm64'
        result = self.run_script('bootstrap.sh', '--no-setup')
        self.assertNotEqual(result.returncode, 0)
        self.assert_no_install()

    def test_unsupported_distribution(self):
        self.write(self.root / 'etc/os-release', 'ID=debian\nVERSION_ID=11\n')
        result = self.run_script('bootstrap.sh', '--no-setup')
        self.assertNotEqual(result.returncode, 0)
        self.assert_no_install()

    def test_nonroot_and_missing_systemd(self):
        self.env['MOCK_UID'] = '1000'
        result = self.run_script('bootstrap.sh', '--no-setup')
        self.assertNotEqual(result.returncode, 0)
        self.env['MOCK_UID'] = '0'
        (self.root / 'run/systemd/system').rmdir()
        result = self.run_script('bootstrap.sh', '--no-setup')
        self.assertNotEqual(result.returncode, 0)
        self.assert_no_install()

    def test_source_conflict(self):
        self.write(self.root / 'usr/local/bin/noderampart', 'fixture')
        result = self.run_script('bootstrap.sh', '--no-setup')
        self.assertNotEqual(result.returncode, 0)
        self.assert_no_install()

    def test_downgrade_is_rejected(self):
        self.env.update(SCENARIO='downgrade', MOCK_DEB_INSTALLED='1')
        result = self.run_script('bootstrap.sh', '--no-setup')
        self.assertNotEqual(result.returncode, 0)
        self.assert_no_install()

    def test_package_failure_does_not_open_setup(self):
        self.env['SCENARIO'] = 'package_fail'
        result = self.run_script('bootstrap.sh', '--no-setup')
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn('Package installed.', result.stdout)
        self.assertFalse(list((self.root / 'tmp').iterdir()))

    def test_untrusted_tmp_is_rejected_before_dependencies(self):
        directory = self.root / 'tmp'
        directory.chmod(0o777)
        result = self.run_script('bootstrap.sh', '--no-setup')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('sticky', result.stderr)
        directory.chmod(0o1777)
        self.env['MOCK_TMP_UID'] = '1000'
        result = self.run_script('bootstrap.sh', '--no-setup')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('owned by root', result.stderr)
        directory.rmdir()
        canary = self.root / 'alternate-temp'
        canary.mkdir()
        directory.symlink_to(canary)
        result = self.run_script('bootstrap.sh', '--no-setup')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('symlink', result.stderr)
        self.assert_no_install()
        self.assertFalse(list(canary.iterdir()))

    def test_root_owned_sticky_tmp_accepts_private_staging(self):
        (self.root / 'tmp').chmod(0o1777)
        result = self.run_script('bootstrap.sh', '--no-setup')
        self.assertEqual(result.returncode, 0, result.stderr)

    def native(self, kind='deb'):
        self.env['MOCK_' + kind.upper() + '_INSTALLED'] = '1'
        for name in ('noderampart', 'noderampartd', 'noderampart-sensor'):
            self.write(self.root / 'usr/bin' / name, 'fixture', 0o755)
        for name in ('noderampart-geoip-update.timer', 'noderampart-geoip-update.service'):
            self.write(self.root / 'etc/systemd/system' / name, 'fixture managed unit')
        self.write(self.root / 'etc/systemd/system/noderampartd.service.d/90-noderampart-managed.conf', 'fixture owned dropin')
        self.write(self.root / 'etc/systemd/system/noderampartd.service.d/admin.conf', 'preserve')
        self.write(self.root / 'etc/tmpfiles.d/noderampart-management.conf', 'f /run/noderampart-management.lock 0600 root root - -\n')
        self.write(self.root / 'etc/tmpfiles.d/local-administrator.conf', 'preserve this rule\n')

    def test_remove_preserves_state_and_other_dropins(self):
        self.native()
        lock = self.root / 'run/noderampart-management.lock'
        self.write(lock, '', 0o600)
        inode = lock.stat().st_ino
        result = self.run_script('manage-remove.sh')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue((self.root / 'var/lib/noderampart/state').is_file())
        self.assertTrue((self.root / 'etc/noderampart/config.json').is_file())
        self.assertTrue((self.root / 'etc/systemd/system/noderampartd.service.d/admin.conf').is_file())
        self.assertFalse((self.root / 'etc/systemd/system/noderampartd.service.d/90-noderampart-managed.conf').exists())
        self.assertFalse((self.root / 'etc/tmpfiles.d/noderampart-management.conf').exists())
        self.assertEqual((self.root / 'etc/tmpfiles.d/local-administrator.conf').read_text(), 'preserve this rule\n')
        self.assertEqual(lock.stat().st_ino, inode)
        self.assertIn(['apt-get', 'remove', '-y', '--no-auto-remove', 'noderampart'], self.commands())

    def test_remove_accepts_partial_debian_package_states(self):
        for state in ('half-installed', 'unpacked', 'half-configured', 'triggers-awaited', 'triggers-pending'):
            with self.subTest(state=state):
                self.native()
                self.env['MOCK_DEB_STATUS'] = 'install ok ' + state
                result = self.run_script('manage-remove.sh')
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertIn(['apt-get', 'remove', '-y', '--no-auto-remove', 'noderampart'], self.commands())
                self.assertTrue((self.root / 'var/lib/noderampart/state').exists())
                (self.lab / 'commands.jsonl').unlink()

    def test_purge_accepts_debian_config_files_only_state(self):
        self.env.update(MOCK_DEB_INSTALLED='1', MOCK_DEB_STATUS='deinstall ok config-files')
        result = self.run_script('manage-remove.sh', '--purge')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn(['apt-get', 'purge', '-y', '--no-auto-remove', 'noderampart'], self.commands())
        self.assertFalse((self.root / 'etc/noderampart').exists())

    def test_partial_debian_state_does_not_claim_unowned_binaries(self):
        self.native()
        self.env.update(MOCK_DEB_STATUS='install ok half-configured', MOCK_DEB_UNOWNED='1')
        result = self.run_script('manage-remove.sh', '--purge')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('unowned native binary', result.stderr)
        self.assertFalse(any(row[0] in ('apt-get', 'systemctl', 'rm') for row in self.commands()))

    def test_remove_rejects_invalid_debian_state_and_other_owner(self):
        self.native()
        for status, owner in (('install ok unknown-state', 'noderampart'),
                              ('invalid ok installed', 'noderampart'),
                              ('install ok installed', 'other-package')):
            with self.subTest(status=status, owner=owner):
                self.env.update(MOCK_DEB_STATUS=status, MOCK_DEB_OWNER=owner)
                result = self.run_script('manage-remove.sh')
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse(any(row[0] in ('apt-get', 'systemctl', 'rm') for row in self.commands()))
                (self.lab / 'commands.jsonl').unlink()

    def test_remove_rejects_unsafe_managed_tmpfiles_rule(self):
        self.native()
        rule = self.root / 'etc/tmpfiles.d/noderampart-management.conf'
        canary = self.root / 'rule-canary'
        self.write(canary, 'preserve this target\n')
        for kind in ('symlink', 'hardlink', 'writable', 'nonroot', 'directory'):
            with self.subTest(kind=kind):
                rule.unlink()
                if kind == 'symlink': rule.symlink_to(canary)
                elif kind == 'hardlink': os.link(canary, rule)
                elif kind == 'directory': rule.mkdir()
                else: self.write(rule, 'owned rule fixture\n', 0o666 if kind == 'writable' else 0o644)
                if kind == 'nonroot': self.env['MOCK_NONROOT_PATH'] = str(rule)
                result = self.run_script('manage-remove.sh')
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse(any(row[0] in ('apt-get', 'dnf', 'systemctl', 'rm') for row in self.commands()))
                self.assertEqual(canary.read_text(), 'preserve this target\n')
                self.env.pop('MOCK_NONROOT_PATH', None)
                (self.lab / 'commands.jsonl').unlink(missing_ok=True)
                if kind == 'directory':
                    rule.rmdir()
                    self.write(rule, 'owned rule fixture\n')

    def test_remove_rejects_symlinked_tmpfiles_parent(self):
        self.native()
        directory = self.root / 'etc/tmpfiles.d'
        canary = self.root / 'tmpfiles-canary'
        directory.rename(canary)
        directory.symlink_to(canary)
        result = self.run_script('manage-remove.sh')
        self.assertNotEqual(result.returncode, 0)
        self.assertTrue((canary / 'noderampart-management.conf').is_file())
        self.assertTrue((canary / 'local-administrator.conf').is_file())
        self.assertFalse(any(row[0] in ('apt-get', 'dnf', 'systemctl', 'rm') for row in self.commands()))

    def test_source_remove_cleans_fixed_notices_and_retains_extra_files(self):
        targets = ['usr/local/bin/' + name for name in ('noderampart', 'noderampartd', 'noderampart-sensor')]
        targets += ['etc/systemd/system/' + name for name in ('noderampartd.service', 'noderampart-sensor.service')]
        targets += ['usr/local/libexec/noderampart/manage-remove']
        manifest = []
        for target in targets:
            path = self.root / target
            self.write(path, 'owned source fixture\n', 0o755)
            manifest.append(hashlib.sha256(path.read_bytes()).hexdigest() + '  ' + str(path) + '\n')
        doc = self.root / 'usr/local/share/doc/noderampart'
        self.write(doc / 'source-install.manifest', ''.join(manifest))
        names = [path.name for path in (REPO / 'third_party/licenses').iterdir() if path.is_file()]
        for name in names:
            self.write(doc / 'third-party' / name, 'license fixture\n')
        extra = doc / 'third-party/local-administrator-note.txt'
        self.write(extra, 'preserve this unrelated document\n')
        result = self.run_script('manage-remove.sh')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(extra.is_file())
        self.assertTrue(all(not (doc / 'third-party' / name).exists() for name in names))
        self.assertTrue(all(not (self.root / target).exists() for target in targets))
        self.assertTrue((self.root / 'var/lib/noderampart/state').is_file())
        self.assert_no_install()

    def test_purge_survives_package_unlinking_its_own_helper(self):
        self.native('rpm')
        lock = self.root / 'run/noderampart-management.lock'
        self.write(lock, '', 0o600)
        inode = lock.stat().st_ino
        staged = self.stage('manage-remove.sh')
        self.env['MOCK_UNLINK_HELPER'] = str(staged)
        result = subprocess.run(['/bin/sh', str(staged), '--purge'], env=self.env, text=True, capture_output=True, timeout=15, check=False)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(staged.exists())
        self.assertFalse((self.root / 'var/lib/noderampart').exists())
        self.assertFalse((self.root / 'etc/tmpfiles.d/noderampart-management.conf').exists())
        self.assertEqual((self.root / 'etc/tmpfiles.d/local-administrator.conf').read_text(), 'preserve this rule\n')
        self.assertEqual(lock.stat().st_ino, inode)
        self.assertIn('were purged', result.stdout)
        self.assertIn(['dnf', 'remove', '-y', '--setopt=clean_requirements_on_remove=False', 'noderampart'], self.commands())

    def test_purge_preflights_nested_mounts_before_package_manager(self):
        self.native()
        self.write(self.root / 'proc/self/mountinfo', '1 0 0:1 / / rw - tmpfs tmpfs rw\n2 1 0:1 / ' + str(self.root / 'var/cache/noderampart/mounted') + ' rw - tmpfs tmpfs rw\n')
        result = self.run_script('manage-remove.sh', '--purge')
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(any(row[0] in ('apt-get', 'dnf', 'rm') for row in self.commands()))
        self.assertTrue((self.root / 'var/lib/noderampart/state').is_file())

    def test_purge_rejects_symlink_and_malformed_mountinfo(self):
        self.native()
        state = self.root / 'var/lib/noderampart'
        state.rename(self.root / 'saved')
        state.symlink_to(self.root / 'saved')
        result = self.run_script('manage-remove.sh', '--purge')
        self.assertNotEqual(result.returncode, 0)
        self.assertTrue((self.root / 'saved/state').is_file())
        state.unlink()
        self.write(self.root / 'proc/self/mountinfo', 'invalid\n')
        result = self.run_script('manage-remove.sh', '--purge')
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(any(row[0] in ('apt-get', 'dnf', 'rm') for row in self.commands()))

    def test_stop_failure_keeps_package(self):
        self.native()
        self.env['SCENARIO'] = 'stop_fail'
        result = self.run_script('manage-remove.sh')
        self.assertNotEqual(result.returncode, 0)
        self.assert_no_install()
        self.assertTrue((self.root / 'usr/bin/noderampart').exists())

    def test_symlinked_or_hardlinked_management_lock(self):
        self.native()
        lock = self.root / 'run/noderampart-management.lock'
        canary = self.root / 'canary'
        self.write(canary, 'unchanged', 0o600)
        lock.symlink_to(canary)
        result = self.run_script('manage-remove.sh')
        self.assertNotEqual(result.returncode, 0)
        lock.unlink()
        os.link(canary, lock)
        result = self.run_script('manage-remove.sh')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(canary.read_text(), 'unchanged')
        self.assert_no_install()

    def test_management_lock_is_exclusive(self):
        self.native()
        lock = self.root / 'run/noderampart-management.lock'
        self.write(lock, '', 0o600)
        with lock.open('r+') as held:
            fcntl.flock(held, fcntl.LOCK_EX)
            staged = self.stage('manage-remove.sh')
            staged.write_text(staged.read_text().replace('flock -x -w 30', 'flock -x -w 0'))
            result = subprocess.run(['/bin/sh', str(staged)], env=self.env, text=True, capture_output=True, timeout=15, check=False)
            self.assertNotEqual(result.returncode, 0)
        self.assert_no_install()

    def test_root_and_argument_boundary_for_removal(self):
        self.native()
        result = self.run_script('manage-remove.sh', '--purge', '/etc')
        self.assertNotEqual(result.returncode, 0)
        self.env['MOCK_UID'] = '1000'
        result = self.run_script('manage-remove.sh')
        self.assertNotEqual(result.returncode, 0)
        self.assert_no_install()

    def test_remove_rejects_symlinked_dropin_parent(self):
        self.native()
        directory = self.root / 'etc/systemd/system/noderampartd.service.d'
        directory.rename(self.root / 'dropin-canary')
        directory.symlink_to(self.root / 'dropin-canary')
        result = self.run_script('manage-remove.sh')
        self.assertNotEqual(result.returncode, 0)
        self.assertTrue((self.root / 'dropin-canary/admin.conf').is_file())
        self.assert_no_install()


class ReleaseAssetTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='noderampart-release-test-')
        self.addCleanup(self.temp.cleanup)
        self.project = Path(self.temp.name)
        (self.project / 'scripts').mkdir()
        for name in ('build-release.sh', 'bootstrap.sh', 'release_sbom.py'):
            shutil.copyfile(REPO / 'scripts' / name, self.project / 'scripts' / name)
        (self.project / 'VERSION').write_text('0.4.0-alpha.5\n')
        self.input = self.project / 'input'
        self.input.mkdir()
        names = [f'noderampart_0.4.0-alpha.5_{arch}.deb' for arch in ('amd64', 'arm64')]
        names += [f'noderampart-0.4.0-0.alpha.6.fc{fedora}.{arch}.rpm'
                  for fedora in (43, 44) for arch in ('x86_64', 'aarch64')]
        names += ['noderampart-0.4.0-0.alpha.6.fc44.src.rpm']
        for name in names:
            (self.input / name).write_text('synthetic package\n')
        self.env = dict(os.environ, COMMIT='1234567890abcdef1234567890abcdef12345678', BUILD_DATE='2026-09-12T00:00:00Z')
        scope = 'Go programs embedded in this package only; runtime system dependencies are not inventoried.'
        for name in names[:-1]:
            native_version = '0.4.0~alpha.5' if name.endswith('.deb') else name.removeprefix('noderampart-').rsplit('.', 2)[0]
            native_arch = name.removesuffix('.deb').rsplit('_', 1)[1] if name.endswith('.deb') else name.split('.')[-2]
            sha = hashlib.sha256((self.input / name).read_bytes()).hexdigest()
            arch = 'arm64' if any(part in name for part in ('arm64', 'aarch64')) else 'amd64'
            paths = ['usr/bin/' + binary for binary in ('noderampart', 'noderampartd', 'noderampart-sensor')]
            binaries = [{'path': path, 'sha256': 'a' * 64, 'elf_architecture': arch,
                         'build_settings': {'GOARCH': arch, 'GOOS': 'linux'}} for path in paths]
            (self.input / (name + '.buildinfo.json')).write_text(json.dumps({'format': 1, 'package': name,
                'sha256': sha, 'architecture': arch, 'scope': scope, 'binaries': binaries,
                'package_identity': {'name': 'noderampart', 'version': native_version, 'architecture': native_arch},
                'declared_build': {'version': '0.4.0-alpha.5', 'commit': self.env['COMMIT'], 'build_date': self.env['BUILD_DATE']}}))
            (self.input / (name + '.spdx.json')).write_text(json.dumps({'spdxVersion': 'SPDX-2.3',
                'dataLicense': 'CC0-1.0', 'comment': f'{scope} Package: {name}; SHA256: {sha}.',
                'packages': [{'name': name, 'versionInfo': native_version}],
                'files': [{'fileName': path, 'checksums': [{'algorithm': 'SHA256', 'checksumValue': 'a' * 64}]} for path in paths]}))

    def run_collect(self):
        return subprocess.run(['/bin/sh', str(self.project / 'scripts/build-release.sh'), '--collect', str(self.input)],
                              env=self.env, text=True, capture_output=True, check=False, timeout=15)

    def test_complete_assets_have_exact_checksums_and_no_publication(self):
        result = self.run_collect()
        self.assertEqual(result.returncode, 0, result.stderr)
        release = self.project / 'dist/release'
        checksums = (release / 'SHA256SUMS').read_text().splitlines()
        self.assertEqual(len(checksums), 21)
        self.assertEqual(subprocess.run(['sha256sum', '-c', 'SHA256SUMS'], cwd=release, capture_output=True).returncode, 0)
        for line in checksums:
            digest, name = line.split()
            self.assertEqual(digest, hashlib.sha256((release / name).read_bytes()).hexdigest())
        self.assertEqual(json.loads((release / 'release.json').read_text())['package_count'], 6)
        self.assertEqual(json.loads((release / 'release.json').read_text())['sbom_count'], 6)
        self.assertIn('nothing was published', result.stdout)

    def test_release_metadata_is_required_and_valid(self):
        for field, values in (('COMMIT', ('', 'unknown', 'a' * 12)),
                              ('BUILD_DATE', ('', 'unknown', '2026-99-99T00:00:00Z'))):
            original = self.env[field]
            for value in values:
                with self.subTest(field=field, value=value):
                    self.env[field] = value
                    self.assertNotEqual(self.run_collect().returncode, 0)
                    self.assertFalse((self.project / 'dist/release').exists())
            del self.env[field]
            self.assertNotEqual(self.run_collect().returncode, 0)
            self.env[field] = original

    def test_explicit_metadata_never_reads_container_git(self):
        fake = self.project / 'fake-bin'
        fake.mkdir()
        git = fake / 'git'
        git.write_text('#!/bin/sh\necho "unexpected git metadata read" >&2\nexit 128\n')
        git.chmod(0o755)
        self.env['PATH'] = str(fake) + os.pathsep + self.env['PATH']
        self.env['GIT_TEST_ASSUME_DIFFERENT_OWNER'] = '1'
        result = self.run_collect()
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_missing_sbom_or_inspection_prevents_collection(self):
        for suffix in ('.spdx.json', '.buildinfo.json'):
            with self.subTest(suffix=suffix):
                path = next(self.input.glob('*' + suffix))
                content = path.read_text()
                path.unlink()
                result = self.run_collect()
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse((self.project / 'dist/release').exists())
                path.write_text(content)

    def test_sbom_binding_architecture_and_binary_digest_must_match(self):
        package = next(self.input.glob('*.deb'))
        inspection = self.input / (package.name + '.buildinfo.json')
        original = inspection.read_text()
        for field, value in (('sha256', '0' * 64), ('architecture', 'wrong'), ('declared_build', {}), ('package_identity', {}), ('package', 'other.deb')):
            with self.subTest(field=field):
                content = json.loads(original)
                content[field] = value
                inspection.write_text(json.dumps(content))
                self.assertNotEqual(self.run_collect().returncode, 0)
                self.assertFalse((self.project / 'dist/release').exists())
        content = json.loads(original)
        content['binaries'][0]['sha256'] = 'b' * 64
        inspection.write_text(json.dumps(content))
        self.assertNotEqual(self.run_collect().returncode, 0)
        inspection.write_text(original)
        sbom = self.input / (package.name + '.spdx.json')
        content = json.loads(sbom.read_text())
        content['comment'] = 'different package'
        sbom.write_text(json.dumps(content))
        self.assertNotEqual(self.run_collect().returncode, 0)

    def test_missing_asset_does_not_publish_partial_output(self):
        next(self.input.glob('*.deb')).unlink()
        result = self.run_collect()
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((self.project / 'dist/release').exists())

    def test_unknown_local_commit_cannot_be_collected_as_release(self):
        self.env['COMMIT'] = 'unknown'
        result = self.run_collect()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('invalid commit', result.stderr)
        self.assertFalse((self.project / 'dist/release').exists())

    def test_duplicate_and_symlink_assets_rejected(self):
        target = next(self.input.glob('*.deb'))
        other = self.input / 'other'
        other.mkdir()
        duplicate = other / target.name
        shutil.copyfile(target, duplicate)
        result = self.run_collect()
        self.assertNotEqual(result.returncode, 0)
        duplicate.unlink()
        duplicate.symlink_to(target)
        result = self.run_collect()
        self.assertNotEqual(result.returncode, 0)

    def test_existing_output_is_preserved(self):
        release = self.project / 'dist/release'
        release.mkdir(parents=True)
        canary = release / 'retain'
        canary.write_text('original')
        result = self.run_collect()
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(canary.read_text(), 'original')


class SourceMetadataTests(unittest.TestCase):
    def test_verified_commit_and_annotated_tag_use_commit_date(self):
        with tempfile.TemporaryDirectory(prefix='noderampart-metadata-test-') as tmp:
            env = dict(os.environ, GIT_CONFIG_NOSYSTEM='1', GIT_CONFIG_GLOBAL=os.devnull,
                       GIT_AUTHOR_DATE='2026-09-01T00:00:00+00:00',
                       GIT_COMMITTER_DATE='2026-09-02T00:00:00+00:00')
            env.pop('GIT_TEST_ASSUME_DIFFERENT_OWNER', None)
            def git(*args):
                return subprocess.check_output(['git', '-c', 'user.name=Fixture', '-c',
                    'user.email=fixture@example.invalid', *args], cwd=tmp, env=env, text=True).strip()
            git('init', '-q')
            git('commit', '--allow-empty', '-qm', 'synthetic metadata fixture')
            commit = git('rev-parse', 'HEAD')
            env['GIT_COMMITTER_DATE'] = '2026-09-03T00:00:00+00:00'
            git('tag', '-a', 'fixture', '-m', 'different tag date')
            for expected in (commit, git('rev-parse', 'refs/tags/fixture')):
                env['EXPECTED_COMMIT'] = expected
                result = subprocess.run(['/bin/sh', str(REPO / 'scripts/release-metadata.sh')],
                    cwd=tmp, env=env, text=True, capture_output=True)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(result.stdout.splitlines(),
                    ['commit=' + commit, 'build_date=' + git('show', '-s', '--format=%cI', commit)])
            git('commit', '--allow-empty', '-qm', 'second synthetic commit')
            for expected in (commit, '', 'unknown', commit[:12]):
                env['EXPECTED_COMMIT'] = expected
                result = subprocess.run(['/bin/sh', str(REPO / 'scripts/release-metadata.sh')],
                    cwd=tmp, env=env, text=True, capture_output=True)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(result.stdout, '')


if __name__ == '__main__':
    unittest.main(verbosity=2)
