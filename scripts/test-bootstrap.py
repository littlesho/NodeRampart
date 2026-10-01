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
    log.write(json.dumps({'command': name, 'LC_ALL': os.environ.get('LC_ALL', '')}) + '\n')
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
    if scenario in ('redirect_bad', 'redirect_http', 'redirect_suffix'):
        destination = 'https://untrusted.example/asset' if scenario == 'redirect_bad' else 'http://github.com/asset'
        if scenario == 'redirect_suffix': destination = 'https://github.com.evil.invalid/littlesho/NodeRampart/releases/download/asset'
        headers.write_text('HTTP/1.1 302 Found\r\nLocation: ' + destination + '\r\n\r\n')
        output.write_bytes(b'')
        print('302', end='')
        sys.exit(0)
    if scenario == 'redirect_loop':
        headers.write_text('HTTP/1.1 302 Found\r\nLocation: ' + args[-1] + '\r\n\r\n')
        output.write_bytes(b'')
        print('302', end='')
        sys.exit(0)
    if scenario in ('redirect_duplicate', 'redirect_control'):
        destination = 'https://objects.githubusercontent.com/synthetic/asset'
        location = 'Location: ' + destination + '\r\n'
        if scenario == 'redirect_duplicate': location *= 2
        else: location = 'Location: ' + destination + '\tinvalid\r\n'
        headers.write_text('HTTP/1.1 302 Found\r\n' + location + '\r\n')
        output.write_bytes(b'')
        print('302', end='')
        sys.exit(0)
    if scenario == 'redirect_allowed' and args[-1].startswith('https://github.com/'):
        host = 'release-assets.githubusercontent.com' if args[-1].endswith('/SHA256SUMS') else 'objects.githubusercontent.com'
        headers.write_text('HTTP/1.1 302 Found\r\nLocation: https://' + host + '/synthetic/' + args[-1].rsplit('/', 1)[1] + '\r\n\r\n')
        output.write_bytes(b'')
        print('302', end='')
        sys.exit(0)
    if scenario == '404' or scenario == '404_package' and not args[-1].endswith('/SHA256SUMS'):
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
    elif scenario == 'package_oversize':
        # Sparse synthetic data exercises the real child-only file-size limit
        # without allocating a 128 MiB fixture in memory or downloading bytes.
        try:
            with output.open('wb') as stream: stream.truncate(134217729)
        except OSError: sys.exit(23)
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
        elif '${Version}' in args[-2]: print(os.environ.get('MOCK_DEB_INSTALLED_VERSION', '9.0.0'))
        else: print(os.environ.get('MOCK_DEB_STATUS', 'install ok installed'))
    else: sys.exit(1)
elif name == 'dpkg-deb':
    values = {'Package':os.environ.get('MOCK_DEB_NAME', 'noderampart'), 'Version':os.environ.get('MOCK_DEB_VERSION', '0.4.0~alpha.5'), 'Architecture':os.environ.get('MOCK_DEB_ARCH', os.environ.get('MOCK_USER_ARCH', 'amd64'))}
    print('malformed' if scenario == 'identity' else values[args[-1]])
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

    def alpha8_target(self, distribution='debian', version='13', machine='x86_64'):
        """Select one synthetic platform; never probe the actual host."""
        for key in ('SCENARIO', 'MOCK_DEB_INSTALLED', 'MOCK_DEB_INSTALLED_VERSION',
                    'MOCK_RPM_INSTALLED', 'MOCK_RPM_VERSION', 'MOCK_DEB_NAME', 'MOCK_DEB_ARCH'):
            self.env.pop(key, None)
        arch = 'arm64' if machine in ('aarch64', 'arm64') else 'amd64'
        rpm_arch = 'aarch64' if arch == 'arm64' else 'x86_64'
        asset = ('noderampart_0.4.0-alpha.8_' + arch + '.deb' if distribution == 'debian' else
                 'noderampart-0.4.0-0.alpha.9.fc' + version + '.' + rpm_arch + '.rpm')
        self.write(self.root / 'etc/os-release', 'ID=' + distribution + '\nVERSION_ID=' + version + '\n')
        self.env.update(MOCK_MACHINE=machine, MOCK_USER_ARCH=arch, MOCK_ASSET=asset,
                        MOCK_DEB_VERSION='0.4.0~alpha.8',
                        MOCK_RPM_IDENTITY='noderampart:0.4.0:0.alpha.9.fc' + version + ':' + rpm_arch)
        (self.lab / 'commands.jsonl').unlink(missing_ok=True)
        return asset

    def assert_alpha8_downloads(self, count=2):
        downloads = [row for row in self.commands() if row[0] == 'curl']
        self.assertEqual(len(downloads), count)
        base = 'https://github.com/littlesho/NodeRampart/releases/download/v0.4.0-alpha.8/'
        self.assertEqual([row[-1] for row in downloads],
                         [base + 'SHA256SUMS', base + self.env['MOCK_ASSET']][:count])
        for row in downloads:
            self.assertIn('--disable', row)
            self.assertEqual(row[row.index('--proto') + 1], '=https')
            self.assertEqual(row[row.index('--tlsv1.2')], '--tlsv1.2')
            self.assertNotIn('--insecure', row)
            self.assertEqual(row[row.index('--max-time') + 1], '180')
        self.assertEqual(downloads[0][downloads[0].index('--max-filesize') + 1], '65536')
        if count == 2:
            self.assertEqual(downloads[1][downloads[1].index('--max-filesize') + 1], '134217728')

    def test_alpha8_explicit_platform_matrix_keeps_state_and_service_choices(self):
        platforms = [('debian', '12'), ('debian', '13'), ('fedora', '43'), ('fedora', '44')]
        for distribution, version in platforms:
            for machine in ('x86_64', 'aarch64'):
                with self.subTest(distribution=distribution, version=version, machine=machine):
                    asset = self.alpha8_target(distribution, version, machine)
                    config = (self.root / 'etc/noderampart/config.json').read_bytes()
                    state = (self.root / 'var/lib/noderampart/state').read_bytes()
                    result = self.run_script('bootstrap.sh', '--version', 'v0.4.0-alpha.8', '--no-setup')
                    self.assertEqual(result.returncode, 0, result.stderr)
                    self.assert_alpha8_downloads()
                    command = 'apt-get' if distribution == 'debian' else 'dnf'
                    installs = [row for row in self.commands() if row[:2] == [command, 'install']]
                    self.assertEqual(len(installs), 1)
                    self.assertTrue(installs[0][-1].endswith('/' + asset))
                    self.assertNotIn('--nogpgcheck', installs[0])
                    self.assertFalse(any(row[0] in ('noderampart', 'systemctl') for row in self.commands()))
                    self.assertEqual((self.root / 'etc/noderampart/config.json').read_bytes(), config)
                    self.assertEqual((self.root / 'var/lib/noderampart/state').read_bytes(), state)
                    self.assertFalse(list((self.root / 'tmp').iterdir()))
                    self.assertIn('Setup was skipped.', result.stdout)

    def test_alpha8_404_never_falls_back_to_other_release_or_asset(self):
        for distribution, version in (('debian', '12'), ('debian', '13'), ('fedora', '43'), ('fedora', '44')):
            for machine in ('x86_64', 'aarch64'):
                for scenario in ('404', '404_package'):
                    with self.subTest(distribution=distribution, version=version, machine=machine, scenario=scenario):
                        self.alpha8_target(distribution, version, machine)
                        self.env['SCENARIO'] = scenario
                        result = self.run_script('bootstrap.sh', '--version', 'v0.4.0-alpha.8', '--no-setup')
                        self.assertNotEqual(result.returncode, 0)
                        self.assertIn('not available yet', result.stderr)
                        self.assert_alpha8_downloads(1 if scenario == '404' else 2)
                        self.assert_no_install()
                        self.assertFalse(any(row[0] in ('noderampart', 'systemctl') for row in self.commands()))
                        self.assertFalse(list((self.root / 'tmp').iterdir()))

    def test_alpha8_checksum_manifest_and_payload_size_fail_closed(self):
        for distribution, version, machine in (('debian', '13', 'x86_64'), ('fedora', '44', 'aarch64')):
            for scenario in ('missing_asset', 'duplicate', 'manifest', 'hash', 'oversize', 'package_oversize'):
                with self.subTest(distribution=distribution, scenario=scenario):
                    self.alpha8_target(distribution, version, machine)
                    self.env['SCENARIO'] = scenario
                    result = self.run_script('bootstrap.sh', '--version', 'v0.4.0-alpha.8', '--no-setup')
                    self.assertNotEqual(result.returncode, 0)
                    self.assert_no_install()
                    self.assertFalse(list((self.root / 'tmp').iterdir()))
                    downloads = [row for row in self.commands() if row[0] == 'curl']
                    self.assertTrue(downloads)
                    self.assertTrue(all('/v0.4.0-alpha.8/' in row[-1] for row in downloads))
                    if scenario in ('hash', 'package_oversize'):
                        self.assert_alpha8_downloads()
                    else:
                        self.assert_alpha8_downloads(1)

    def test_alpha8_individual_native_package_identity_fields_are_checked(self):
        for distribution, version, machine in (('debian', '12', 'aarch64'), ('debian', '13', 'x86_64'),
                                                ('fedora', '43', 'aarch64'), ('fedora', '44', 'x86_64')):
            fields = ('name', 'version', 'arch') if distribution == 'debian' else ('name', 'version', 'release', 'arch')
            for field in fields:
                with self.subTest(distribution=distribution, version=version, machine=machine, field=field):
                    self.alpha8_target(distribution, version, machine)
                    wrong_arch = 'amd64' if machine == 'aarch64' else 'arm64'
                    if distribution == 'debian':
                        overrides = {'name': ('MOCK_DEB_NAME', 'other-package'),
                                     'version': ('MOCK_DEB_VERSION', '0.4.0~alpha.7'),
                                     'arch': ('MOCK_DEB_ARCH', wrong_arch)}
                        key, value = overrides[field]
                        self.env[key] = value
                    else:
                        identity = self.env['MOCK_RPM_IDENTITY'].split(':')
                        index, value = {'name': (0, 'other-package'), 'version': (1, '0.4.1'),
                                        'release': (2, '0.alpha.8.fc' + version),
                                        'arch': (3, 'x86_64' if wrong_arch == 'amd64' else 'aarch64')}[field]
                        identity[index] = value
                        self.env['MOCK_RPM_IDENTITY'] = ':'.join(identity)
                    result = self.run_script('bootstrap.sh', '--version', 'v0.4.0-alpha.8', '--no-setup')
                    self.assertNotEqual(result.returncode, 0)
                    self.assertIn('identity', result.stderr)
                    self.assert_alpha8_downloads()
                    self.assert_no_install()
                    self.assertFalse(list((self.root / 'tmp').iterdir()))

    def test_alpha8_upgrade_and_downgrade_guards_use_native_versions(self):
        for current, refused in (('0.4.0~alpha.7', False), ('0.4.0~alpha.8', False),
                                  ('0.4.0~alpha.9', True), ('0.4.0', True)):
            with self.subTest(kind='deb', current=current):
                self.alpha8_target()
                self.env.update(MOCK_DEB_INSTALLED='1', MOCK_DEB_INSTALLED_VERSION=current,
                                SCENARIO='downgrade' if refused else '')
                result = self.run_script('bootstrap.sh', '--version', 'v0.4.0-alpha.8', '--no-setup')
                self.assertEqual(result.returncode != 0, refused, result.stderr)
                self.assertIn(['dpkg', '--compare-versions', current, 'le', '0.4.0~alpha.8'], self.commands())
                if refused:
                    self.assertIn('downgrade', result.stderr)
                    self.assert_no_install()
                    self.assertFalse(any(row[0] == 'curl' for row in self.commands()))
                else:
                    self.assert_alpha8_downloads()
        for version in ('43', '44'):
            for current, refused in (('0.4.0-0.alpha.8.fc' + version, False),
                                      ('0.4.0-0.alpha.9.fc' + version, False),
                                      ('0.4.0-0.alpha.10.fc' + version, True),
                                      ('0.4.0-1.fc' + version, True)):
                with self.subTest(kind='rpm', version=version, current=current):
                    self.alpha8_target('fedora', version)
                    self.env.update(MOCK_RPM_INSTALLED='1', MOCK_RPM_VERSION=current)
                    result = self.run_script('bootstrap.sh', '--version', 'v0.4.0-alpha.8', '--no-setup')
                    self.assertEqual(result.returncode != 0, refused, result.stderr)
                    if refused:
                        self.assert_no_install()
                        self.assertFalse(any(row[0] == 'curl' for row in self.commands()))
                    else:
                        self.assert_alpha8_downloads()

    def test_alpha8_help_and_invalid_argument_boundaries(self):
        result = self.run_script('bootstrap.sh', '--help')
        self.assertEqual(result.returncode, 0, result.stderr)
        for version in ('v0.4.0-alpha', 'v0.4.0-alpha.5', 'v0.4.0-alpha.6', 'v0.4.0-alpha.7', 'v0.4.0-alpha.8'):
            self.assertIn(version, result.stdout)
        self.assertEqual(self.commands(), [])
        for args in (('--version',), ('--version', 'latest'), ('--version', '0.4.0-alpha.8'),
                     ('--version', 'v0.4.0-alpha.8', '--unknown'), ('--version', 'v0.4.0-alpha.8', 'extra')):
            with self.subTest(args=args):
                result = self.run_script('bootstrap.sh', *args)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(self.commands(), [])

    def test_alpha8_redirects_keep_the_existing_https_boundary(self):
        for scenario in ('redirect_bad', 'redirect_http', 'redirect_suffix', 'redirect_duplicate', 'redirect_control', 'redirect_loop'):
            with self.subTest(scenario=scenario):
                self.alpha8_target()
                self.env['SCENARIO'] = scenario
                result = self.run_script('bootstrap.sh', '--version', 'v0.4.0-alpha.8', '--no-setup')
                self.assertNotEqual(result.returncode, 0)
                self.assert_no_install()
                downloads = [row for row in self.commands() if row[0] == 'curl']
                self.assertEqual(len(downloads), 5 if scenario == 'redirect_loop' else 1)
                self.assertTrue(all(row[-1].startswith('https://github.com/littlesho/NodeRampart/releases/download/v0.4.0-alpha.8/') for row in downloads))
                self.assertFalse(list((self.root / 'tmp').iterdir()))
        self.alpha8_target()
        self.env['SCENARIO'] = 'redirect_allowed'
        result = self.run_script('bootstrap.sh', '--version', 'v0.4.0-alpha.8', '--no-setup')
        self.assertEqual(result.returncode, 0, result.stderr)
        downloads = [row[-1] for row in self.commands() if row[0] == 'curl']
        self.assertEqual(downloads, [
            'https://github.com/littlesho/NodeRampart/releases/download/v0.4.0-alpha.8/SHA256SUMS',
            'https://release-assets.githubusercontent.com/synthetic/SHA256SUMS',
            'https://github.com/littlesho/NodeRampart/releases/download/v0.4.0-alpha.8/' + self.env['MOCK_ASSET'],
            'https://objects.githubusercontent.com/synthetic/' + self.env['MOCK_ASSET']])

    def test_alpha8_all_source_owned_paths_and_broken_links_refuse_before_download(self):
        self.alpha8_target()
        for relative in ('usr/local/bin/noderampart', 'usr/local/bin/noderampartd',
                         'usr/local/bin/noderampart-sensor', 'usr/local/libexec/noderampart/manage-remove'):
            for kind in ('file', 'broken_link'):
                with self.subTest(path=relative, kind=kind):
                    path = self.root / relative
                    path.parent.mkdir(parents=True, exist_ok=True)
                    if kind == 'file': self.write(path, 'preserve source-owned fixture')
                    else: path.symlink_to(self.root / 'absent-source-file')
                    (self.lab / 'commands.jsonl').unlink(missing_ok=True)
                    result = self.run_script('bootstrap.sh', '--version', 'v0.4.0-alpha.8', '--no-setup')
                    self.assertNotEqual(result.returncode, 0)
                    self.assertIn('source installation', result.stderr)
                    self.assert_no_install()
                    self.assertFalse(any(row[0] in ('curl', 'noderampart', 'systemctl') for row in self.commands()))
                    self.assertTrue(path.is_symlink() if kind == 'broken_link' else path.read_text() == 'preserve source-owned fixture')
                    path.unlink()

    def test_alpha8_tty_utf8_and_noninteractive_do_not_change_service_choices(self):
        self.alpha8_target()
        result = self.run_script('bootstrap.sh', '--version', 'v0.4.0-alpha.8')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('--no-setup', result.stderr)
        self.assert_no_install()
        self.assertFalse(any(row[0] == 'curl' for row in self.commands()))
        master, slave = pty.openpty()
        self.addCleanup(os.close, master)
        self.addCleanup(os.close, slave)
        for distribution, version, machine in (('debian', '13', 'x86_64'), ('fedora', '44', 'aarch64')):
            for locale, expected in (('C', 'C.UTF-8'), ('zh_CN.UTF-8', 'zh_CN.UTF-8')):
                with self.subTest(distribution=distribution, locale=locale):
                    self.alpha8_target(distribution, version, machine)
                    self.env.update(LC_ALL=locale, PYTHONCOERCECLOCALE='0', PYTHONUTF8='0')
                    log = self.lab / 'locales.jsonl'
                    log.unlink(missing_ok=True)
                    result = self.run_script('bootstrap.sh', '--version', 'v0.4.0-alpha.8', tty=os.ttyname(slave), pipe=True)
                    self.assertEqual(result.returncode, 0, result.stderr)
                    self.assertEqual([row for row in self.commands() if row[0] == 'noderampart'], [['noderampart', 'setup']])
                    self.assertFalse(any(row[0] == 'systemctl' for row in self.commands()))
                    locales = [json.loads(line) for line in log.read_text().splitlines()]
                    self.assertEqual([row['LC_ALL'] for row in locales if row['command'] == 'noderampart'], [expected])
                    self.assertTrue(all(row['LC_ALL'] == 'C' for row in locales if row['command'] != 'noderampart'))
            self.alpha8_target(distribution, version, machine)
            result = self.run_script('bootstrap.sh', '--version', 'v0.4.0-alpha.8', '--non-interactive')
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertFalse(any(row[0] in ('noderampart', 'systemctl') for row in self.commands()))

    def test_debian_install_downloads_checks_then_uses_dependencies(self):
        result = self.run_script('bootstrap.sh', '--no-setup')
        self.assertEqual(result.returncode, 0, result.stderr)
        commands = self.commands()
        installs = [row for row in commands if row[:2] == ['apt-get', 'install']]
        self.assertEqual(len(installs), 1)
        self.assertIn('Dpkg::Options::=--force-confold', installs[0])
        self.assertEqual([row[0] for row in commands].count('curl'), 2)
        self.assertFalse(list((self.root / 'tmp').iterdir()))

    def test_default_release_stays_public_alpha5(self):
        self.env.update(MOCK_ASSET='noderampart_0.4.0-alpha.5_amd64.deb',
                        MOCK_DEB_VERSION='0.4.0~alpha.5')
        result = self.run_script('bootstrap.sh', '--no-setup')
        self.assertEqual(result.returncode, 0, result.stderr)
        downloads = [row[-1] for row in self.commands() if row[0] == 'curl']
        self.assertEqual(downloads, [
            'https://github.com/littlesho/NodeRampart/releases/download/v0.4.0-alpha.5/SHA256SUMS',
            'https://github.com/littlesho/NodeRampart/releases/download/v0.4.0-alpha.5/noderampart_0.4.0-alpha.5_amd64.deb'])

    def test_explicit_alpha6_candidate_has_native_identity_and_downgrade_guard(self):
        self.env.update(MOCK_ASSET='noderampart_0.4.0-alpha.6_amd64.deb',
                        MOCK_DEB_VERSION='0.4.0~alpha.6')
        result = self.run_script('bootstrap.sh', '--version', 'v0.4.0-alpha.6', '--no-setup')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(any(row[0] == 'apt-get' and row[1] == 'install' for row in self.commands()))
        self.assertTrue(all('/v0.4.0-alpha.6/' in row[-1] for row in self.commands() if row[0] == 'curl'))
        (self.lab / 'commands.jsonl').unlink()
        self.env['MOCK_DEB_VERSION'] = '0.4.0~alpha.5'
        result = self.run_script('bootstrap.sh', '--version', 'v0.4.0-alpha.6', '--no-setup')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('identity', result.stderr)
        self.assert_no_install()
        self.write(self.root / 'etc/os-release', 'ID=fedora\nVERSION_ID=44\n')
        self.env.update(MOCK_ASSET='noderampart-0.4.0-0.alpha.7.fc44.x86_64.rpm',
                        MOCK_RPM_INSTALLED='1',
                        MOCK_RPM_IDENTITY='noderampart:0.4.0:0.alpha.7.fc44:x86_64')
        for installed, accepted in (('0.4.0-0.alpha.6.fc44', True),
                                    ('0.4.0-0.alpha.7.fc44', True),
                                    ('0.4.0-0.alpha.8.fc44', False)):
            with self.subTest(installed=installed):
                self.env['MOCK_RPM_VERSION'] = installed
                (self.lab / 'commands.jsonl').unlink(missing_ok=True)
                result = self.run_script('bootstrap.sh', '--version', 'v0.4.0-alpha.6', '--no-setup')
                self.assertEqual(result.returncode == 0, accepted, result.stderr)
                if accepted:
                    self.assertTrue(any(row[0] == 'dnf' and row[1] == 'install' for row in self.commands()))
                    self.assertTrue(all('/v0.4.0-alpha.6/' in row[-1] for row in self.commands() if row[0] == 'curl'))
                else:
                    self.assertIn('downgrade', result.stderr)
                    self.assert_no_install()

    def test_explicit_alpha7_candidate_keeps_native_identity_and_rejects_downgrade(self):
        self.env.update(MOCK_ASSET='noderampart_0.4.0-alpha.7_amd64.deb',
                        MOCK_DEB_VERSION='0.4.0~alpha.7')
        result = self.run_script('bootstrap.sh', '--version', 'v0.4.0-alpha.7', '--no-setup')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(any(row[:2] == ['apt-get', 'install'] for row in self.commands()))
        self.assertTrue(all('/v0.4.0-alpha.7/' in row[-1] for row in self.commands() if row[0] == 'curl'))
        (self.lab / 'commands.jsonl').unlink()
        self.env['MOCK_DEB_VERSION'] = '0.4.0~alpha.6'
        result = self.run_script('bootstrap.sh', '--version', 'v0.4.0-alpha.7', '--no-setup')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('identity', result.stderr)
        self.assert_no_install()
        self.write(self.root / 'etc/os-release', 'ID=fedora\nVERSION_ID=44\n')
        self.env.update(MOCK_ASSET='noderampart-0.4.0-0.alpha.8.fc44.x86_64.rpm',
                        MOCK_RPM_INSTALLED='1',
                        MOCK_RPM_IDENTITY='noderampart:0.4.0:0.alpha.8.fc44:x86_64')
        for installed, accepted in (('0.4.0-0.alpha.7.fc44', True),
                                    ('0.4.0-0.alpha.8.fc44', True),
                                    ('0.4.0-0.alpha.9.fc44', False)):
            with self.subTest(installed=installed):
                self.env['MOCK_RPM_VERSION'] = installed
                (self.lab / 'commands.jsonl').unlink(missing_ok=True)
                result = self.run_script('bootstrap.sh', '--version', 'v0.4.0-alpha.7', '--no-setup')
                self.assertEqual(result.returncode == 0, accepted, result.stderr)
                if accepted:
                    self.assertTrue(any(row[:2] == ['dnf', 'install'] for row in self.commands()))
                else:
                    self.assertIn('downgrade', result.stderr)
                    self.assert_no_install()

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
        self.env.update(MOCK_ASSET='noderampart-0.4.0-0.alpha.6.fc44.x86_64.rpm', MOCK_RPM_INSTALLED='1',
                        MOCK_RPM_IDENTITY='noderampart:0.4.0:0.alpha.6.fc44:x86_64')
        for installed, accepted in (('0.4.0-0.alpha.1.fc44', True), ('0.4.0-0.alpha.2.fc44', True),
                                    ('0.4.0-0.alpha.3.fc44', True), ('0.4.0-0.alpha.4.fc44', True),
                                    ('0.4.0-0.alpha.5.fc44', True), ('0.4.0-0.alpha.6.fc44', True),
                                    ('0.4.0-0.alpha.7.fc44', False)):
            with self.subTest(installed=installed):
                self.env['MOCK_RPM_VERSION'] = installed
                (self.lab / 'commands.jsonl').unlink(missing_ok=True)
                result = self.run_script('bootstrap.sh', '--version', 'v0.4.0-alpha.5', '--no-setup')
                self.assertEqual(result.returncode == 0, accepted, result.stderr)
                if not accepted:
                    self.assertIn('downgrade', result.stderr)
                    self.assert_no_install()

    def test_arm64_package_selection(self):
        self.env.update(MOCK_MACHINE='aarch64', MOCK_USER_ARCH='arm64', MOCK_ASSET='noderampart_0.4.0-alpha.5_arm64.deb')
        result = self.run_script('bootstrap.sh', '--no-setup')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(any(row[0] == 'curl' and row[-1].endswith('_arm64.deb') for row in self.commands()))

    def test_download_failures_never_install(self):
        for scenario in ('404', 'transport', 'redirect_bad', 'redirect_http', 'redirect_loop', 'hash', 'duplicate', 'manifest', 'oversize', 'missing_asset', 'identity'):
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
        for locale, expected in (({'LC_ALL': 'C', 'LANG': 'en_US.UTF-8'}, 'C.UTF-8'),
                                 ({'LC_ALL': 'POSIX'}, 'C.UTF-8'),
                                 ({'LC_CTYPE': 'C', 'LANG': 'zh_CN.UTF-8'}, 'C.UTF-8'),
                                 ({'LC_ALL': 'C.utf8'}, 'C.utf8'),
                                 ({'LANG': 'en_US.UTF-8'}, 'en_US.UTF-8'),
                                 ({'LANG': 'zh_CN.UTF-8'}, 'zh_CN.UTF-8'),
                                 ({}, 'C.UTF-8'),
                                 ({'LC_ALL': 'en_US.ISO-8859-1'}, 'en_US.ISO-8859-1')):
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
        (self.project / 'VERSION').write_text('0.4.0-alpha.8\n')
        self.input = self.project / 'input'
        self.input.mkdir()
        names = [f'noderampart_0.4.0-alpha.8_{arch}.deb' for arch in ('amd64', 'arm64')]
        names += [f'noderampart-0.4.0-0.alpha.9.fc{fedora}.{arch}.rpm'
                  for fedora in (43, 44) for arch in ('x86_64', 'aarch64')]
        names += ['noderampart-0.4.0-0.alpha.9.fc44.src.rpm']
        for name in names:
            (self.input / name).write_text('synthetic package\n')
        # This is a synthetic clean revision, not a Git mutation or an assertion
        # about the dirty developer checkout containing this test.
        self.commit = '1' * 40
        mocks = self.project / 'mocks'
        mocks.mkdir()
        git = mocks / 'git'
        git.write_text('#!' + sys.executable + '\n' + f'''import sys
args = sys.argv[1:]
if args[:1] == ['-c']:
    if args[1] != 'safe.directory=' + {str(self.project)!r}:
        sys.exit('unsafe fixture Git source scope')
    args = args[2:]
if args == ['rev-parse', '--verify', 'HEAD']:
    print({self.commit!r})
elif args[:1] == ['show']:
    print('2026-09-12T00:00:00Z')
elif args[:1] not in (['diff'], ['ls-files']):
    sys.exit('unexpected fixture Git operation')
''')
        git.chmod(0o755)
        self.env = dict(os.environ, COMMIT=self.commit, BUILD_DATE='2026-09-12T00:00:00Z',
                        PATH=str(mocks) + ':/usr/bin:/bin')
        scope = 'Go programs embedded in this package only; runtime system dependencies are not inventoried.'
        for name in names[:-1]:
            sha = hashlib.sha256((self.input / name).read_bytes()).hexdigest()
            arch = 'arm64' if any(part in name for part in ('arm64', 'aarch64')) else 'amd64'
            paths = ['usr/bin/' + binary for binary in ('noderampart', 'noderampartd', 'noderampart-sensor')]
            binaries = [{'path': path, 'sha256': 'a' * 64, 'elf_architecture': arch,
                         'build_settings': {'GOARCH': arch, 'GOOS': 'linux'}} for path in paths]
            (self.input / (name + '.buildinfo.json')).write_text(json.dumps({'format': 1, 'package': name,
                'sha256': sha, 'architecture': arch, 'scope': scope, 'binaries': binaries,
                'declared_build': {'version': '0.4.0-alpha.8', 'commit': self.env['COMMIT'], 'build_date': self.env['BUILD_DATE']}}))
            (self.input / (name + '.spdx.json')).write_text(json.dumps({'spdxVersion': 'SPDX-2.3',
                'dataLicense': 'CC0-1.0', 'comment': f'{scope} Package: {name}; SHA256: {sha}.',
                'packages': [{'name': name, 'versionInfo': '0.4.0~alpha.8' if name.endswith('.deb') else name.removeprefix('noderampart-').rsplit('.', 2)[0]}],
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
        for line in checksums:
            digest, name = line.split()
            self.assertEqual(digest, hashlib.sha256((release / name).read_bytes()).hexdigest())
        metadata = json.loads((release / 'release.json').read_text())
        self.assertEqual(metadata['version'], '0.4.0-alpha.8')
        self.assertEqual(metadata['commit'], self.commit)
        self.assertEqual(metadata['build_date'], self.env['BUILD_DATE'])
        self.assertEqual(metadata['package_count'], 6)
        self.assertEqual(json.loads((release / 'release.json').read_text())['sbom_count'], 6)
        self.assertIn('nothing was published', result.stdout)

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
        for field, value in (('sha256', '0' * 64), ('architecture', 'wrong'), ('declared_build', {})):
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
            real_git = shutil.which('git')
            self.assertIsNotNone(real_git)
            env = dict(os.environ, GIT_CONFIG_NOSYSTEM='1', GIT_CONFIG_GLOBAL=os.devnull,
                       GIT_AUTHOR_DATE='2026-09-01T00:00:00+00:00',
                       GIT_COMMITTER_DATE='2026-09-02T00:00:00+00:00')
            env.pop('GIT_TEST_ASSUME_DIFFERENT_OWNER', None)
            wrapper_dir = Path(tmp) / 'git-wrapper'
            wrapper_dir.mkdir()
            wrapper = wrapper_dir / 'git'
            synthetic_tag_oid = '2' * 40
            # Keep a real commit/date source, but do not create any tag object
            # or ref. Only this annotated-tag peel is synthetic; all other
            # permitted Git operations execute the actual Git binary.
            wrapper.write_text('#!' + sys.executable + '\n' + f'''import os, sys
args = sys.argv[1:]
operation = list(args)
while operation[:1] == ['-c']:
    if len(operation) < 3 or operation[1] not in ('user.name=Fixture', 'user.email=fixture@example.invalid'):
        sys.exit('unexpected fixture Git configuration')
    operation = operation[2:]
if not operation or operation[0] not in ('init', 'commit', 'rev-parse', 'show', 'for-each-ref', 'cat-file'):
    sys.exit('fixture Git tag creation is prohibited')
if operation == ['rev-parse', '--verify', {synthetic_tag_oid!r} + '^{{commit}}']:
    print(os.environ['METADATA_FIXTURE_COMMIT'])
else:
    os.execv({real_git!r}, [{real_git!r}, *args])
''')
            wrapper.chmod(0o755)
            env['PATH'] = str(wrapper_dir) + os.pathsep + env['PATH']
            def git(*args):
                return subprocess.check_output(['git', '-c', 'user.name=Fixture', '-c',
                    'user.email=fixture@example.invalid', *args], cwd=tmp, env=env, text=True).strip()
            git('init', '-q')
            git('commit', '--allow-empty', '-qm', 'synthetic metadata fixture')
            commit = git('rev-parse', 'HEAD')
            env['METADATA_FIXTURE_COMMIT'] = commit
            self.assertNotEqual(commit, synthetic_tag_oid)
            different_tag_date = '2026-09-03T00:00:00+00:00'
            env['GIT_COMMITTER_DATE'] = different_tag_date
            for expected in (commit, synthetic_tag_oid):
                env['EXPECTED_COMMIT'] = expected
                result = subprocess.run(['/bin/sh', str(REPO / 'scripts/release-metadata.sh')],
                    cwd=tmp, env=env, text=True, capture_output=True, timeout=10)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(result.stdout.splitlines(),
                    ['commit=' + commit, 'build_date=' + git('show', '-s', '--format=%cI', commit)])
                self.assertNotIn(different_tag_date, result.stdout)
            git('commit', '--allow-empty', '-qm', 'second synthetic commit')
            for expected in (commit, synthetic_tag_oid, '', 'unknown', commit[:12]):
                env['EXPECTED_COMMIT'] = expected
                result = subprocess.run(['/bin/sh', str(REPO / 'scripts/release-metadata.sh')],
                    cwd=tmp, env=env, text=True, capture_output=True, timeout=10)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(result.stdout, '')
            self.assertEqual(git('for-each-ref', '--format=%(refname)', 'refs/tags'), '')
            self.assertNotIn('tag', git('cat-file', '--batch-all-objects',
                '--batch-check=%(objecttype)').splitlines())


if __name__ == '__main__':
    unittest.main(verbosity=2)
