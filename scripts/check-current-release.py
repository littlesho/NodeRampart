#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Check intentionally marked current examples against the public release record.

This is an offline documentation check, never an installer fallback. Historical,
rollback, development-build and third-party examples stay outside these regions.
The existing publication table supplies native package versions and source identity.
"""

import argparse
import datetime
import json
from pathlib import Path
import re
import sys


NUMBER = r"(?:0|[1-9][0-9]*)"
PRODUCT_TAG = re.compile(
    rf"v({NUMBER})\.({NUMBER})\.({NUMBER})(?:-(alpha|beta|rc)(?:\.({NUMBER}))?)?\Z"
)
START = "<!-- current-release:start -->"
END = "<!-- current-release:end -->"
REQUIRED = ("README.md", "README.zh-CN.md", "docs/RELEASE_VERIFICATION.md")


def version_key(tag):
    if not isinstance(tag, str) or len(tag) > 128:
        return None
    match = PRODUCT_TAG.fullmatch(tag)
    if not match:
        return None
    major, minor, patch, stage, number = match.groups()
    return (int(major), int(minor), int(patch), stage is None,
            stage or "", -1 if number is None else int(number))


def newest_published(data):
    """Accept one complete list, or gh api --paginate --slurp's page arrays."""
    if not isinstance(data, list):
        raise ValueError("published release listing must be a JSON array")
    if data and all(isinstance(page, list) for page in data):
        if len(data) > 20:
            raise ValueError("published release listing exceeds 20 pages")
        data = [release for page in data for release in page]
    if len(data) > 2000:
        raise ValueError("published release listing exceeds 2000 entries")
    candidates = []
    for release in data:
        if not isinstance(release, dict):
            raise ValueError("invalid release object")
        tag = release.get("tag_name")
        key = version_key(tag)
        if key is None:
            continue
        if type(release.get("draft")) is not bool:
            raise ValueError("release draft field must be a boolean")
        if release["draft"] or release.get("published_at") is None:
            continue
        published = release["published_at"]
        if not isinstance(published, str) or not re.fullmatch(
                r"[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z", published):
            raise ValueError("invalid release publication timestamp")
        datetime.datetime.fromisoformat(published.replace("Z", "+00:00"))
        candidates.append((key, tag))
    if not candidates:
        raise ValueError("no published product release in listing")
    return max(candidates)[1]


def release_pages(raw):
    """Older gh --paginate writes adjacent page arrays, without --slurp."""
    text = raw.decode("utf-8")
    decoder = json.JSONDecoder()
    pages = []
    position = 0
    while position < len(text):
        if text[position].isspace():
            position += 1
            continue
        page, position = decoder.raw_decode(text, position)
        pages.append(page)
        if len(pages) > 20:
            raise ValueError("published release listing exceeds 20 pages")
    if not pages:
        raise ValueError("empty published release JSON")
    return pages[0] if len(pages) == 1 else pages


def release_identity(source, tag):
    identities = set()
    for table in re.findall(r"(?:^\|.*\n)+", source, re.MULTILINE):
        if not re.search(r"^\| Tag \| `" + re.escape(tag) + r"`", table, re.MULTILINE):
            continue
        commit = re.search(r"^\| Source commit \| `([0-9a-f]{40})`", table, re.MULTILINE)
        versions = re.search(r"^\| Project / DEB / RPM \| (.+)$", table, re.MULTILINE)
        if not commit or not versions:
            raise ValueError("current publication table lacks source/package identities")
        fields = re.findall(r"`([^`]+)`", versions[1])
        if len(fields) != 3 or fields[0] != tag[1:]:
            raise ValueError("current publication package version disagrees with LATEST_RELEASE")
        rpm = fields[2].split(".fc", 1)[0].split("%{?dist}", 1)[0]
        identities.add((fields[0], fields[1], rpm, commit[1]))
    if len(identities) != 1:
        raise ValueError("need one consistent publication identity for " + tag)
    return identities.pop()


def check_regions(text, name, identity):
    project, deb, rpm, commit = identity
    errors = []
    regions = []
    opened = None
    for number, line in enumerate(text.splitlines(), 1):
        if START in line:
            if opened is not None or END in line:
                raise ValueError(f"{name}:{number}: nested/inline current-release marker")
            opened = (number, [])
        elif END in line:
            if opened is None:
                raise ValueError(f"{name}:{number}: unmatched current-release end")
            regions.append(opened)
            opened = None
        elif opened is not None:
            opened[1].append(line)
    if opened is not None:
        raise ValueError(f"{name}:{opened[0]}: unclosed current-release region")
    for number, lines in regions:
        body = "\n".join(lines)
        # Match native versions first, so their core is not mistaken for a
        # standalone stable product version. Suffixes name a platform, not a version.
        patterns = (
            (r"(?<![0-9A-Za-z])\d+\.\d+\.\d+-\d+[A-Za-z0-9.~+]*", rpm, "RPM"),
            (r"(?<![0-9A-Za-z])\d+\.\d+\.\d+~(?:alpha|beta|rc)(?:\.\d+)?", deb, "DEB"),
            (r"(?<![0-9A-Za-z])v?\d+\.\d+\.\d+(?:-(?:alpha|beta|rc)(?:\.\d+)?)?", project, "product"),
            (r"\b[0-9a-f]{40}\b", commit, "source"),
        )
        for pattern, expected, kind in patterns:
            def validate(match):
                value = match[0]
                if kind == "RPM":
                    value = value.split(".fc", 1)[0]
                if kind == "product":
                    value = value.removeprefix("v")
                if value != expected:
                    errors.append(f"{name}:{number}: stale {kind} {match[0]} (expected {expected})")
                return " " * len(match[0])
            body = re.sub(pattern, validate, body)
    return len(regions), errors


def check(root, published_file=None):
    tag = (root / "LATEST_RELEASE").read_text().strip()
    if version_key(tag) is None:
        raise ValueError("LATEST_RELEASE must contain one canonical product tag")
    identity = release_identity((root / "docs/RELEASE_VERIFICATION.md").read_text(), tag)
    paths = sorted(set(root.glob("*.md")) | set((root / "docs").rglob("*.md")))
    errors = []
    marked = set()
    total = 0
    for path in paths:
        name = path.relative_to(root).as_posix()
        if name in ("AGENTS.md", "STATUS.md") or name.startswith("docs/ai/"):
            continue
        count, found = check_regions(path.read_text(), name, identity)
        if count:
            marked.add(name)
        total += count
        errors.extend(found)
    for name in REQUIRED:
        if name not in marked:
            errors.append(name + ": missing current-release example region")
    if published_file:
        with open(published_file, "rb") as stream:
            raw = stream.read(20 * 1024 * 1024 + 1)
        if len(raw) > 20 * 1024 * 1024:
            raise ValueError("published release JSON exceeds 20 MiB")
        online_tag = newest_published(release_pages(raw))
        if online_tag != tag:
            errors.append(f"LATEST_RELEASE is {tag}; actual newest published release is {online_tag}")
    if errors:
        raise ValueError("\n".join(errors))
    return f"Current release documentation PASS: {tag}, {total} regions in {len(marked)} documents"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=Path(__file__).resolve().parent.parent)
    parser.add_argument("--published-releases-json", type=Path,
                        help="complete GitHub release listing captured during publication sync")
    args = parser.parse_args()
    try:
        print(check(args.root, args.published_releases_json))
    except (OSError, ValueError) as error:
        print("Current release documentation: " + str(error), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
