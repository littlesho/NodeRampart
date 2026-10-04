#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Check the release download Action using synthetic, non-publishable artifacts."""

import argparse
import hashlib
from pathlib import Path
import re
import stat


DOWNLOAD_SHA = "3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c"
FIXTURES = {
    "release-deb-amd64": {
        "synthetic_amd64.deb": b"\x00synthetic Debian artifact fixture\xff\r\n",
        "nested/校验-数据.bin": bytes(range(256)) + "中文文件名\n".encode(),
    },
    "release-rpm-fc44-amd64": {
        "synthetic_amd64.rpm": b"\xffsynthetic RPM artifact fixture\x00\n",
        "nested/校验-数据.bin": bytes(reversed(range(256))) + b"RPM\n",
    },
    "release-sbom": {
        "synthetic.spdx.json": b'{"SPDXID":"SPDXRef-Synthetic-Fixture"}\n',
        "nested/来源.txt": "仅用于下载兼容性检查\n".encode(),
    },
    "compat-decoy": {
        "must-not-download.bin": b"The release-* pattern must exclude this artifact.\n",
        "nested/decoy.txt": b"This is not a release artifact.\n",
    },
}
PACKAGES = ("release-deb-amd64", "release-rpm-fc44-amd64")


def create(root, artifacts):
    root.mkdir(parents=True, exist_ok=False)
    for artifact in artifacts:
        for relative, payload in FIXTURES[artifact].items():
            destination = root / artifact / relative
            destination.parent.mkdir(parents=True, exist_ok=True)
            destination.write_bytes(payload)


def check(root, artifacts):
    if root.is_symlink() or not root.is_dir():
        raise ValueError("download destination must be a regular directory")
    expected = {f"{artifact}/{relative}": payload
                for artifact in artifacts
                for relative, payload in FIXTURES[artifact].items()}
    expected_directories = set()
    for relative in expected:
        expected_directories.update(parent.as_posix()
                                    for parent in Path(relative).parents
                                    if parent != Path("."))
    files, directories = set(), set()
    for path in root.rglob("*"):
        relative = path.relative_to(root).as_posix()
        info = path.lstat()
        if stat.S_ISDIR(info.st_mode):
            directories.add(relative)
        elif stat.S_ISREG(info.st_mode) and info.st_nlink == 1:
            files.add(relative)
        else:
            raise ValueError("download contains a link or non-regular fixture")
    if files != set(expected) or directories != expected_directories:
        raise ValueError("download files/directories differ from the exact fixture set")
    for relative, payload in sorted(expected.items()):
        path = root / relative
        if path.stat().st_size != len(payload):
            raise ValueError("downloaded fixture has the wrong size")
        actual = path.read_bytes()
        digest = hashlib.sha256(actual).hexdigest()
        if actual != payload or digest != hashlib.sha256(payload).hexdigest():
            raise ValueError("downloaded fixture bytes or SHA-256 differ")
        print(f"{digest}  {relative}")


def check_source():
    # Match the reviewed production shape strictly; added inputs or a changed
    # layout require updating this check deliberately, never weakening digest
    # enforcement merely to make a download pass.
    project = Path(__file__).resolve().parent.parent
    source = (project / ".github/workflows/release.yml").read_text()
    for job in ("sbom", "draft"):
        block = re.search(rf"(?ms)^  {job}:\n(.*?)(?=^  [A-Za-z][\w-]*:|\Z)", source)
        if not block:
            raise ValueError("expected release consumer is missing")
        steps = re.findall(
            r"(?m)^      - uses: actions/download-artifact@([0-9a-f]{40})(?: +#.*)?\n"
            r"        with:\n((?:          [^\n]+\n)+)", block.group(1))
        if len(steps) != 1 or steps[0][0] != DOWNLOAD_SHA:
            raise ValueError("release consumer must use the tested download Action SHA")
        inputs = {}
        for line in steps[0][1].splitlines():
            key, separator, value = line.strip().partition(":")
            if not separator or key in inputs:
                raise ValueError("release consumer inputs are malformed or duplicated")
            inputs[key] = value.strip()
        if inputs != {"pattern": "release-*", "path": "release-input"}:
            raise ValueError("release consumer must preserve the tested default download behavior")
    print("Both release consumers retain the tested SHA, pattern, path and digest defaults.")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=("create-packages", "create-sbom", "check-sbom", "check-draft", "check-source"))
    parser.add_argument("directory", type=Path, nargs="?")
    args = parser.parse_args()
    if args.mode == "check-source":
        if args.directory is not None:
            parser.error("check-source does not accept a directory")
        check_source()
        return
    if args.directory is None:
        parser.error("fixture operations require a directory")
    if args.mode == "create-packages":
        create(args.directory, PACKAGES + ("compat-decoy",))
    elif args.mode == "create-sbom":
        create(args.directory, ("release-sbom",))
    elif args.mode == "check-sbom":
        check(args.directory, PACKAGES)
    else:
        check(args.directory, PACKAGES + ("release-sbom",))


if __name__ == "__main__":
    main()
