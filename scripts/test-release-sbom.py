#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Unprivileged negative tests for release package inspection; no downloads."""

import io
import json
from pathlib import Path
import stat
import struct
import subprocess
import tarfile
import tempfile
import unittest
from unittest.mock import patch

import release_sbom as release


class SBOMTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="noderampart-sbom-test-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.output = self.root / "programs"
        self.output.mkdir()

    def tar(self, entries):
        archive = self.root / "package.tar"
        with tarfile.open(archive, "w") as stream:
            for name, kind in entries:
                entry = tarfile.TarInfo(name)
                entry.type = kind
                entry.linkname = "outside" if kind in (tarfile.SYMTYPE, tarfile.LNKTYPE) else ""
                entry.size = 7 if kind == tarfile.REGTYPE else 0
                stream.addfile(entry, io.BytesIO(b"fixture") if entry.size else None)
        return archive

    def cpio(self, entries):
        archive = self.root / "package.cpio"
        with archive.open("wb") as stream:
            for name, mode, links in [*entries, ("TRAILER!!!", 0, 1)]:
                encoded = name.encode() + b"\0"
                payload = b"fixture" if name != "TRAILER!!!" else b""
                fields = [1, mode, 0, 0, links, 0, len(payload), 0, 0, 0, 0, len(encoded), 0]
                stream.write(b"070701" + b"".join(f"{value:08x}".encode() for value in fields))
                stream.write(encoded)
                stream.write(b"\0" * (-(110 + len(encoded)) % 4))
                stream.write(payload)
                stream.write(b"\0" * (-len(payload) % 4))
        return archive

    def test_only_expected_regular_programs_are_copied(self):
        entries = [(name, tarfile.REGTYPE) for name in sorted(release.TARGETS)]
        entries += [("etc/noderampart/config.json", tarfile.REGTYPE), ("usr/lib/.build-id/link", tarfile.SYMTYPE)]
        release.unpack_tar(self.tar(entries), self.output)
        self.assertEqual({str(path.relative_to(self.output)) for path in self.output.rglob("*") if path.is_file()}, release.TARGETS)
        self.assertFalse((self.output / "etc").exists())

    def test_tar_rejects_escape_symlink_hardlink_duplicate_and_missing_program(self):
        valid = [(name, tarfile.REGTYPE) for name in sorted(release.TARGETS)]
        cases = ([('../escape', tarfile.REGTYPE), *valid],
                 [('usr/bin/noderampart', tarfile.SYMTYPE)],
                 [('usr/bin/noderampart', tarfile.LNKTYPE)],
                 [valid[0], valid[0]], valid[:2])
        for index, entries in enumerate(cases):
            with self.subTest(index=index):
                destination = self.root / str(index)
                destination.mkdir()
                with self.assertRaises(ValueError):
                    release.unpack_tar(self.tar(entries), destination)
        self.assertFalse((self.root / 'escape').exists())

    def test_cpio_validates_names_types_and_complete_program_set(self):
        valid = [(name, stat.S_IFREG | 0o755, 1) for name in sorted(release.TARGETS)]
        release.unpack_cpio(self.cpio(valid), self.output)
        self.assertEqual({str(path.relative_to(self.output)) for path in self.output.rglob("*") if path.is_file()}, release.TARGETS)
        for index, entries in enumerate(([('../escape', stat.S_IFREG, 1)],
                                         [('usr/bin/noderampart', stat.S_IFLNK, 1)],
                                         [('usr/bin/noderampart', stat.S_IFREG, 2)], valid[:2])):
            with self.subTest(index=index):
                destination = self.root / str(index)
                destination.mkdir()
                with self.assertRaises(ValueError):
                    release.unpack_cpio(self.cpio(entries), destination)

    def test_elf_and_go_architecture_are_independently_checked(self):
        binary = self.root / 'noderampart'
        header = bytearray(64)
        header[:7] = b'\x7fELF\x02\x01\x01'
        struct.pack_into('<HH', header, 16, 2, 62)
        binary.write_bytes(header)
        with patch.object(release, 'command') as command:
            with self.assertRaisesRegex(ValueError, 'ELF architecture'):
                release.inspect_binary(binary, 'arm64')
            command.assert_not_called()
            metadata = {'GoVersion': 'go1.26.8', 'Path': 'github.com/littlesho/NodeRampart/cmd/noderampart',
                        'Main': {'Path': 'github.com/littlesho/NodeRampart', 'Version': '(devel)'},
                        'Settings': [{'Key': 'GOOS', 'Value': 'linux'}, {'Key': 'GOARCH', 'Value': 'arm64'}]}
            command.return_value = subprocess.CompletedProcess([], 0, stdout=json.dumps(metadata))
            with self.assertRaisesRegex(ValueError, 'Go binary metadata'):
                release.inspect_binary(binary, 'amd64')
            metadata['Settings'][1]['Value'] = 'amd64'
            command.return_value.stdout = json.dumps(metadata)
            observed = release.inspect_binary(binary, 'amd64')
            self.assertEqual(observed['go_version'], 'go1.26.8')
            self.assertEqual(observed['main_module']['Version'], '(devel)')
            self.assertNotIn('declared_build', observed)
            metadata['GoVersion'] = 'go1.26.8-X:nodwarf5'
            command.return_value.stdout = json.dumps(metadata)
            self.assertEqual(release.inspect_binary(binary, 'amd64')['go_version'], 'go1.26.8-X:nodwarf5')
            metadata['GoVersion'] += 'x' * 128
            command.return_value.stdout = json.dumps(metadata)
            with self.assertRaisesRegex(ValueError, 'Go binary metadata'):
                release.inspect_binary(binary, 'amd64')

    def test_source_rpm_and_arbitrary_package_names_are_rejected(self):
        for name in ('noderampart-0.4.0-0.alpha.8.fc44.src.rpm', 'other_0.4.0~alpha.7_amd64.deb',
                     'noderampart_0.4.0~alpha.7_amd64.deb/../escape', 'noderampart_0.4.0~alpha.7_i386.deb'):
            with self.subTest(name=name), self.assertRaises(ValueError):
                release.package_arch(name)

    def test_wrong_tool_digest_and_existing_outputs_are_rejected(self):
        tool = self.root / 'syft'
        tool.write_text('synthetic wrong tool')
        package = self.root / 'noderampart_0.4.0~alpha.7_amd64.deb'
        package.write_text('synthetic package')
        with patch.object(release, 'command') as command:
            with self.assertRaisesRegex(ValueError, 'verified Syft'):
                release.generate(package, self.root / 'out', tool, '1' * 40, '2026-09-12T00:00:00Z')
            command.assert_not_called()
        output = self.root / 'out'
        output.mkdir()
        existing = output / (package.name + '.spdx.json')
        existing.write_text('retain this previous output')
        with patch.object(release, 'digest', return_value=release.SYFT_BINARY_SHA256), patch.object(release, 'command') as command:
            with self.assertRaisesRegex(ValueError, 'already exists'):
                release.generate(package, output, tool, '1' * 40, '2026-09-12T00:00:00Z')
            command.assert_not_called()
        self.assertEqual(existing.read_text(), 'retain this previous output')

    def test_tool_download_checksum_failure_never_produces_executable(self):
        class Response(io.BytesIO):
            def geturl(self):
                return 'https://github.com/anchore/syft/synthetic-test'
        destination = self.root / 'tools'
        with patch.object(release.urllib.request, 'urlopen', return_value=Response(b'synthetic wrong archive')):
            with self.assertRaisesRegex(ValueError, 'checksum mismatch'):
                release.fetch_syft(destination)
        self.assertFalse((destination / 'syft').exists())

    def test_exact_uploaded_portable_set_and_native_package_identity(self):
        document = {'tag_name': 'v' + release.VERSION, 'draft': True, 'prerelease': True}
        assets = [{'name': name, 'state': 'uploaded'} for name in sorted(release.release_assets())]
        self.assertEqual(len(assets), 22)
        release.validate_uploaded(document, assets)
        for separator in ('-', '~'):
            for arch in ('amd64', 'arm64'):
                name = f'noderampart_0.4.0{separator}alpha.7_{arch}.deb'
                self.assertEqual(release.package_identity(name),
                                 {'name': 'noderampart', 'version': '0.4.0~alpha.7', 'architecture': arch})
                self.assertEqual(name in release.runtime_packages(), separator == '-')
        for fedora in (43, 44):
            self.assertEqual(release.package_identity(f'noderampart-0.4.0-0.alpha.8.fc{fedora}.x86_64.rpm')['version'],
                             f'0.4.0-0.alpha.8.fc{fedora}')
        for changed in (assets[:-1], assets + [assets[0]],
                        [dict(asset, state='starter') if index == 0 else asset for index, asset in enumerate(assets)],
                        [dict(assets[0], name='extra.txt')] + assets[1:],
                        [dict(asset, name=asset['name'].replace('-alpha.7_', '.alpha.7_')) for asset in assets]):
            with self.assertRaises(ValueError):
                release.validate_uploaded(document, changed)
        for name in ('bad~name', '.hidden', 'trailing.', 'a/b', 'a%20b', 'a b'):
            with self.subTest(name=name), self.assertRaises(ValueError):
                release.validate_uploaded(document, [dict(assets[0], name=name)] + assets[1:])
        for changed in ({}, dict(document, draft=False), dict(document, prerelease=False),
                        dict(document, tag_name='v0.4.0-alpha.5'), dict(document, tag_name='v0.4.0-alpha.6')):
            with self.assertRaises(ValueError):
                release.validate_uploaded(changed, assets)
        with self.assertRaises(ValueError):
            release.validate_uploaded(document, {})

    def test_portable_deb_name_cannot_override_native_version(self):
        package = self.root / 'noderampart_0.4.0-alpha.7_amd64.deb'
        package.write_bytes(b'synthetic package')
        for values in (('other', '0.4.0~alpha.7', 'amd64'),
                       ('noderampart', '0.4.0-alpha.7', 'amd64'),
                       ('noderampart', '0.4.0~alpha.1', 'amd64'),
                       ('noderampart', '0.4.0~alpha.7', 'arm64')):
            def response(args, **kwargs):
                self.assertEqual(args[:2], ['dpkg-deb', '-f'])
                return subprocess.CompletedProcess(args, 0, values[('Package', 'Version', 'Architecture').index(args[-1])])
            with self.subTest(values=values), patch.object(release, 'command', side_effect=response):
                with self.assertRaisesRegex(ValueError, 'identity mismatch'):
                    release.inspect_package(package, self.output)

    def test_actual_alpha1_uploaded_names_do_not_match_checksum_assets(self):
        fixture = json.loads((Path(__file__).parent / 'testdata/alpha1-uploaded-assets.json').read_text())
        # The public API reported all assets uploaded, but GitHub replaced '~'
        # in six alpha.1 DEB/SBOM/buildinfo names while checksums kept old names.
        expected = {asset['name'].replace('noderampart_0.4.0.alpha.1_', 'noderampart_0.4.0~alpha.1_')
                    for asset in fixture['assets']}
        self.assertEqual(len(fixture['assets']), 22)
        self.assertTrue(all(asset['state'] == 'uploaded' for asset in fixture['assets']))
        self.assertEqual(sum(asset['name'] not in expected for asset in fixture['assets']), 6)
        with patch.object(release, 'VERSION', '0.4.0-alpha.1'), patch.object(release, 'release_assets', return_value=expected):
            with self.assertRaisesRegex(ValueError, 'exact release set'):
                release.validate_uploaded(fixture, fixture['assets'])


if __name__ == '__main__':
    unittest.main(verbosity=2)
