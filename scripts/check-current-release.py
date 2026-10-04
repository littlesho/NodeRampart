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
MAX_RELEASES_BYTES = 20 * 1024 * 1024
MAX_LATEST_BYTES = 1024 * 1024


def version_key(tag):
    if not isinstance(tag, str) or len(tag) > 128:
        return None
    match = PRODUCT_TAG.fullmatch(tag)
    if not match:
        return None
    major, minor, patch, stage, number = match.groups()
    return (int(major), int(minor), int(patch), stage is None,
            stage or "", -1 if number is None else int(number))


def published_objects(data):
    """Accept one complete list or bounded page arrays."""
    if not isinstance(data, list):
        raise ValueError("published release listing must be a JSON array")
    if data and all(isinstance(page, list) for page in data):
        if len(data) > 20:
            raise ValueError("published release listing exceeds 20 pages")
        data = [release for page in data for release in page]
    if len(data) > 2000:
        raise ValueError("published release listing exceeds 2000 entries")
    if any(not isinstance(release, dict) for release in data):
        raise ValueError("invalid release object")
    return data


def publication_timestamp(value):
    if not isinstance(value, str) or not re.fullmatch(
            r"[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z", value):
        raise ValueError("invalid release publication timestamp")
    datetime.datetime.fromisoformat(value.replace("Z", "+00:00"))
    return value


def newest_published(data):
    """Choose the highest published product version, including prereleases."""
    candidates = []
    for release in published_objects(data):
        tag = release.get("tag_name")
        key = version_key(tag)
        if key is None:
            continue
        if type(release.get("draft")) is not bool:
            raise ValueError("release draft field must be a boolean")
        if release["draft"] or release.get("published_at") is None:
            continue
        publication_timestamp(release["published_at"])
        candidates.append((key, tag))
    if not candidates:
        raise ValueError("no published product release in listing")
    return max(candidates)[1]


def unique_object(pairs):
    result = {}
    for name, value in pairs:
        if name in result:
            raise ValueError("duplicate JSON object field")
        result[name] = value
    return result


def release_pages(raw, strict_objects=False):
    """Older gh --paginate writes adjacent page arrays, without --slurp."""
    text = raw.decode("utf-8")
    decoder = json.JSONDecoder(object_pairs_hook=unique_object if strict_objects else None)
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


def positive_release_id(value):
    if type(value) is not int or value <= 0:
        raise ValueError("release ID must be a positive integer")
    return value


def check_published_latest(latest, listing, tag):
    """Cross-check actual Latest and complete-list captures, not request metadata."""
    if not isinstance(latest, dict):
        raise ValueError("Latest release must be a single JSON object")
    release_id = positive_release_id(latest.get("id"))
    if latest.get("draft") is not False or latest.get("prerelease") is not False:
        raise ValueError("Latest release must have draft=false and prerelease=false")
    publication_timestamp(latest.get("published_at"))
    newest = newest_published(listing)
    if latest.get("tag_name") != tag or newest != tag:
        raise ValueError("Latest tag must match LATEST_RELEASE and the newest published product version")
    matches = []
    seen_ids = set()
    seen_tags = set()
    for release in published_objects(listing):
        product_tag = release.get("tag_name")
        candidate_id = positive_release_id(release.get("id"))
        if candidate_id == release_id or product_tag == tag:
            matches.append(release)
        if version_key(product_tag) is None:
            continue
        if release.get("draft") is False and release.get("published_at") is not None:
            if candidate_id in seen_ids or product_tag in seen_tags:
                raise ValueError("duplicate or conflicting published release identity")
            seen_ids.add(candidate_id)
            seen_tags.add(product_tag)
    if len(matches) != 1:
        raise ValueError("Latest must identify exactly one object in the complete release listing")
    corresponding = matches[0]
    for name in ("id", "tag_name", "draft", "prerelease", "published_at"):
        if corresponding.get(name) != latest.get(name) or type(corresponding.get(name)) is not type(latest.get(name)):
            raise ValueError("Latest identity/state/publication timestamp disagrees with the release listing")


def read_bounded(path, limit, description):
    with open(path, "rb") as stream:
        raw = stream.read(limit + 1)
    if len(raw) > limit:
        raise ValueError(description + " exceeds " + ("20 MiB" if limit == MAX_RELEASES_BYTES else "1 MiB"))
    return raw


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


def check(root, published_file=None, latest_file=None):
    if latest_file is not None and published_file is None:
        raise ValueError("--published-latest-json requires --published-releases-json")
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
        raw = read_bounded(published_file, MAX_RELEASES_BYTES, "published release JSON")
        listing = release_pages(raw, strict_objects=latest_file is not None)
        online_tag = newest_published(listing)
        if online_tag != tag:
            errors.append(f"LATEST_RELEASE is {tag}; actual newest published release is {online_tag}")
        if latest_file is not None:
            latest_raw = read_bounded(latest_file, MAX_LATEST_BYTES, "Latest release JSON")
            latest = json.loads(latest_raw, object_pairs_hook=unique_object)
            check_published_latest(latest, listing, tag)
    if errors:
        raise ValueError("\n".join(errors))
    return f"Current release documentation PASS: {tag}, {total} regions in {len(marked)} documents"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=Path(__file__).resolve().parent.parent)
    parser.add_argument("--published-releases-json", type=Path,
                        help="complete GitHub release listing captured during publication sync")
    parser.add_argument("--published-latest-json", type=Path,
                        help="actual GitHub Latest response; requires the complete release listing")
    args = parser.parse_args()
    try:
        print(check(args.root, args.published_releases_json, args.published_latest_json))
    except (OSError, ValueError) as error:
        print("Current release documentation: " + str(error), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
