#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Build-time only: inspect final packages and inventory their Go programs."""

import argparse
import datetime
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import resource
import stat
import struct
import subprocess
import tarfile
import tempfile
import urllib.request


VERSION = "0.4.0-alpha"
SYFT_VERSION = "1.51.1"
SYFT_ARCHIVE_SHA256 = "8fcb33017a0dc1058298c923c436d19dfa68ae93968e0b423248542e3afb9fc3"
SYFT_BINARY_SHA256 = "abca2def61de9952fa06d3977bb1e064818facb9badfce502b450d3d6846a91f"
PROGRAMS = ("noderampart", "noderampartd", "noderampart-sensor")
TARGETS = {"usr/bin/" + name for name in PROGRAMS}
SCOPE = "Go programs embedded in this package only; runtime system dependencies are not inventoried."
LIMIT = 512 * 1024 * 1024
SYFT_CONFIG = """check-for-app-update: false
parallelism: 2
golang:
  search-local-mod-cache-licenses: false
  search-local-vendor-licenses: false
  search-remote-licenses: false
  use-packages-lib: false
  capture-symbols: none
  main-module-version:
    from-ld-flags: false
    from-contents: false
    from-build-settings: false
file:
  metadata:
    selection: owned-by-package
    digests: [sha256]
"""


def package_arch(name):
    match = re.fullmatch(r"noderampart_0\.4\.0~alpha_(amd64|arm64)\.deb", name)
    if match:
        return match[1]
    match = re.fullmatch(r"noderampart-0\.4\.0-0\.alpha\.1\.fc(43|44)\.(x86_64|aarch64)\.rpm", name)
    if match:
        return {"x86_64": "amd64", "aarch64": "arm64"}[match[2]]
    raise ValueError("unsupported runtime package name")


def runtime_packages():
    names = {f"noderampart_0.4.0~alpha_{arch}.deb" for arch in ("amd64", "arm64")}
    names.update(f"noderampart-0.4.0-0.alpha.1.fc{fedora}.{arch}.rpm"
                 for fedora in (43, 44) for arch in ("x86_64", "aarch64"))
    return names


def digest(path, limit=256 * 1024 * 1024):
    info = path.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1 or info.st_size > limit:
        raise ValueError("unsafe or oversized release input")
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def safe_member(name):
    while name.startswith("./"):
        name = name[2:]
    if name in ("", "."):
        return ""
    parts = name.rstrip("/").split("/")
    if any(part in ("", ".", "..") for part in parts) or PurePosixPath(name).is_absolute():
        raise ValueError("unsafe package member path")
    return "/".join(parts)


def save_program(name, payload, destination, seen):
    if name in seen or len(payload) > 128 * 1024 * 1024:
        raise ValueError("duplicate or oversized packaged program")
    seen.add(name)
    path = destination / name
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("xb") as output:
        output.write(payload)
    path.chmod(0o600)  # Inspection never executes a target architecture binary.


def unpack_tar(archive, destination):
    seen = set()
    with tarfile.open(archive, "r:") as stream:
        for count, member in enumerate(stream):
            if count >= 4096:
                raise ValueError("too many package members")
            name = safe_member(member.name)
            if name not in TARGETS:
                continue
            if not member.isfile() or member.size > 128 * 1024 * 1024:
                raise ValueError("packaged program must be a bounded regular file")
            with stream.extractfile(member) as source:
                save_program(name, source.read(), destination, seen)
    if seen != TARGETS:
        raise ValueError("package must contain all three programs")


def unpack_cpio(archive, destination):
    seen = set()
    with archive.open("rb") as stream:
        for _ in range(4097):
            header = stream.read(110)
            if len(header) != 110 or header[:6] not in (b"070701", b"070702"):
                raise ValueError("invalid newc package payload")
            fields = [int(header[i:i + 8], 16) for i in range(6, 110, 8)]
            mode, links, size, name_size = fields[1], fields[4], fields[6], fields[11]
            if not 1 <= name_size <= 4096 or size > LIMIT:
                raise ValueError("oversized package member")
            raw = stream.read(name_size)
            if len(raw) != name_size or raw[-1:] != b"\0":
                raise ValueError("invalid package member name")
            name = safe_member(raw[:-1].decode("utf-8"))
            stream.seek(-(110 + name_size) % 4, 1)
            if name == "TRAILER!!!":
                if size or seen != TARGETS:
                    raise ValueError("package must contain all three programs")
                return
            if stream.tell() + size > archive.stat().st_size:
                raise ValueError("truncated package member")
            if name in TARGETS:
                if not stat.S_ISREG(mode) or links != 1 or size > 128 * 1024 * 1024:
                    raise ValueError("packaged program must be a bounded regular file")
                save_program(name, stream.read(size), destination, seen)
            else:
                stream.seek(size, 1)
            stream.seek(-size % 4, 1)
    raise ValueError("too many package members or missing trailer")


def command(args, **kwargs):
    return subprocess.run(args, check=True, timeout=60, **kwargs)


def inspect_package(package, destination):
    arch = package_arch(package.name)
    digest(package)
    if package.suffix == ".deb":
        actual = [command(["dpkg-deb", "-f", str(package), field], capture_output=True, text=True).stdout.strip()
                  for field in ("Package", "Version", "Architecture")]
        if actual != ["noderampart", "0.4.0~alpha", arch]:
            raise ValueError("Debian package identity mismatch")
        args = ["dpkg-deb", "--fsys-tarfile", str(package)]
        unpack = unpack_tar
    else:
        actual = command(["rpm", "-qp", "--qf", "%{NAME}-%{VERSION}-%{RELEASE}.%{ARCH}.rpm", str(package)],
                         capture_output=True, text=True).stdout
        if actual != package.name:
            raise ValueError("RPM package identity mismatch")
        args = ["rpm2cpio", str(package)]
        unpack = unpack_cpio
    payload = destination.parent / "payload"
    def bound_output():
        resource.setrlimit(resource.RLIMIT_FSIZE, (LIMIT, LIMIT))
    with payload.open("xb") as output:
        command(args, stdout=output, stderr=subprocess.DEVNULL, preexec_fn=bound_output)
    unpack(payload, destination)
    return arch


def inspect_binary(path, arch):
    with path.open("rb") as stream:
        header = stream.read(64)
    if (len(header) != 64 or header[:7] != b"\x7fELF\x02\x01\x01" or
            struct.unpack_from("<H", header, 16)[0] not in (2, 3) or
            struct.unpack_from("<H", header, 18)[0] != {"amd64": 62, "arm64": 183}[arch]):
        raise ValueError("ELF architecture does not match package")
    info = json.loads(command(["go", "version", "-m", "-json", str(path)], capture_output=True, text=True).stdout)
    settings = {item["Key"]: item["Value"] for item in info.get("Settings", [])}
    if (settings.get("GOOS") != "linux" or settings.get("GOARCH") != arch or
            info.get("Path") != "github.com/littlesho/NodeRampart/cmd/" + path.name or
            info.get("Main", {}).get("Path") != "github.com/littlesho/NodeRampart" or
            not re.fullmatch(r"go1\.[0-9]{1,3}(?:\.[0-9]{1,3})?(?:[-+][A-Za-z0-9.:_+-]{1,96})?", info.get("GoVersion", ""))):
        raise ValueError("Go binary metadata does not match package")
    # Go buildinfo -trimpath does not attest the project's custom -X variables.
    return {"path": "usr/bin/" + path.name, "sha256": digest(path), "elf_architecture": arch,
            "go_version": info["GoVersion"], "go_package": info["Path"],
            "main_module": info["Main"], "modules": info.get("Deps", []), "build_settings": settings}


def binding(name, sha256):
    return f"{SCOPE} Package: {name}; SHA256: {sha256}."


def validate_pair(package, spdx_path, buildinfo_path, commit, build_date):
    sha256 = digest(package)
    arch = package_arch(package.name)
    for path in (spdx_path, buildinfo_path):
        digest(path, 4 * 1024 * 1024)
    spdx = json.loads(spdx_path.read_text())
    info = json.loads(buildinfo_path.read_text())
    if (spdx.get("spdxVersion") != "SPDX-2.3" or spdx.get("dataLicense") != "CC0-1.0" or
            spdx.get("comment") != binding(package.name, sha256) or
            not any(entry.get("name") == package.name and entry.get("versionInfo") == VERSION
                    for entry in spdx.get("packages", []))):
        raise ValueError("SBOM is missing its matching package binding or SPDX content")
    if (info.get("format") != 1 or info.get("package") != package.name or info.get("sha256") != sha256 or
            info.get("architecture") != arch or info.get("scope") != SCOPE or
            info.get("declared_build") != {"version": VERSION, "commit": commit, "build_date": build_date}):
        raise ValueError("binary inspection does not match the release package/build declaration")
    binaries = info.get("binaries", [])
    if len(binaries) != 3 or {entry.get("path") for entry in binaries} != TARGETS:
        raise ValueError("binary inspection must contain all three programs")
    files = spdx.get("files", [])
    if len(files) != 3 or {entry.get("fileName") for entry in files} != TARGETS:
        raise ValueError("SBOM must inventory all three inspected program files")
    file_digests = {entry["fileName"]: [checksum.get("checksumValue") for checksum in entry.get("checksums", [])
                                      if checksum.get("algorithm") == "SHA256"] for entry in files}
    for binary in binaries:
        if (binary.get("elf_architecture") != arch or binary.get("build_settings", {}).get("GOARCH") != arch or
                binary.get("build_settings", {}).get("GOOS") != "linux" or
                not re.fullmatch(r"[0-9a-f]{64}", binary.get("sha256", ""))):
            raise ValueError("binary inspection architecture/digest mismatch")
        if file_digests[binary["path"]] != [binary["sha256"]]:
            raise ValueError("SBOM and binary inspection file digests differ")


def fetch_syft(destination):
    destination.mkdir(mode=0o700, parents=True, exist_ok=False)
    url = f"https://github.com/anchore/syft/releases/download/v{SYFT_VERSION}/syft_{SYFT_VERSION}_linux_amd64.tar.gz"
    with urllib.request.urlopen(url, timeout=60) as response:
        if not response.geturl().startswith("https://"):
            raise ValueError("Syft download must remain HTTPS")
        data = response.read(64 * 1024 * 1024 + 1)
    if len(data) > 64 * 1024 * 1024 or hashlib.sha256(data).hexdigest() != SYFT_ARCHIVE_SHA256:
        raise ValueError("Syft archive checksum mismatch")
    archive = destination / "syft.tar.gz"
    archive.write_bytes(data)
    with tarfile.open(archive) as stream:
        members = [member for member in stream if member.name == "syft"]
        if len(members) != 1 or not members[0].isfile() or members[0].size > 128 * 1024 * 1024:
            raise ValueError("unsafe Syft archive")
        binary = destination / "syft"
        with binary.open("xb") as output:
            output.write(stream.extractfile(members[0]).read())
    if digest(binary) != SYFT_BINARY_SHA256:
        raise ValueError("Syft executable checksum mismatch")
    binary.chmod(0o700)
    archive.unlink()


def generate(package, output, syft, commit, build_date):
    if commit != "unknown" and not re.fullmatch(r"[0-9a-f]{12,64}", commit):
        raise ValueError("explicit build commit is required")
    if datetime.datetime.fromisoformat(build_date.replace("Z", "+00:00")).tzinfo is None:
        raise ValueError("explicit timezone-aware build date is required")
    if digest(syft) != SYFT_BINARY_SHA256:
        raise ValueError("use the fixed verified Syft executable")
    output.mkdir(parents=True, exist_ok=True)
    outputs = [output / (package.name + suffix) for suffix in (".spdx.json", ".buildinfo.json")]
    if any(path.exists() or path.is_symlink() for path in outputs):
        raise ValueError("SBOM output already exists; preserve or remove it explicitly")
    with tempfile.TemporaryDirectory(prefix="noderampart-sbom-") as temporary:
        work = Path(temporary)
        extracted = work / "programs"
        extracted.mkdir()
        arch = inspect_package(package, extracted)
        binaries = [inspect_binary(extracted / "usr/bin" / name, arch) for name in PROGRAMS]
        config = work / "syft.yaml"
        config.write_text(SYFT_CONFIG)
        sbom = work / "sbom.spdx.json"
        # Explicit config and a minimal environment prevent personal Syft/Git/Go
        # settings from adding external cache inspection or network enrichment.
        env = {"PATH": "/usr/bin:/bin", "LC_ALL": "C", "SYFT_CHECK_FOR_APP_UPDATE": "false",
               "XDG_CONFIG_HOME": str(work / "config"), "XDG_CACHE_HOME": str(work / "cache")}
        command([str(syft), "scan", "dir:" + str(extracted), "--config", str(config),
                 "--override-default-catalogers", "go-module-binary-cataloger", "--source-name", package.name,
                 "--source-version", VERSION, "--output", "spdx-json=" + str(sbom), "--quiet"], env=env, cwd=work)
        digest(sbom, 4 * 1024 * 1024)
        document = json.loads(sbom.read_text())
        package_sha = digest(package)
        document["comment"] = binding(package.name, package_sha)
        sbom.write_text(json.dumps(document, indent=2) + "\n")
        inspected = work / "buildinfo.json"
        inspected.write_text(json.dumps({"format": 1, "package": package.name, "sha256": package_sha,
            "architecture": arch, "scope": SCOPE,
            "declared_build": {"version": VERSION, "commit": commit, "build_date": build_date},
            "binaries": binaries}, indent=2) + "\n")
        validate_pair(package, sbom, inspected, commit, build_date)
        for source, target in zip((sbom, inspected), outputs):
            with target.open("xb") as stream:
                stream.write(source.read_bytes())
            target.chmod(0o644)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--fetch-syft", type=Path)
    parser.add_argument("--syft", type=Path)
    parser.add_argument("--output", type=Path)
    parser.add_argument("package", nargs="?", type=Path)
    args = parser.parse_args()
    if args.fetch_syft is not None:
        if args.package is not None or args.syft is not None or args.output is not None:
            parser.error("--fetch-syft is a separate operation")
        fetch_syft(args.fetch_syft.absolute())
    else:
        if args.package is None or args.syft is None or args.output is None:
            parser.error("package, --syft and --output are required")
        generate(args.package.absolute(), args.output.absolute(), args.syft.absolute(),
                 os.environ.get("COMMIT", ""), os.environ.get("BUILD_DATE", ""))


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, tarfile.TarError, subprocess.SubprocessError) as error:
        raise SystemExit("Release SBOM failed: " + str(error))
