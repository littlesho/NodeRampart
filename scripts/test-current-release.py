#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Offline current-documentation and release-publication synchronization regressions."""

import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
import subprocess
import sys


SPEC = importlib.util.spec_from_file_location("current_release", Path(__file__).with_name("check-current-release.py"))
CHECK = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(CHECK)
IDENTITY = ("0.4.0-alpha.9", "0.4.0~alpha.9", "0.4.0-0.alpha.10", "9" * 40)


def region(body):
    return CHECK.START + "\n" + body + "\n" + CHECK.END + "\n"


def release(tag, **values):
    return dict(tag_name=tag, draft=False, published_at="2026-10-04T04:17:49Z", **values)


class CurrentReleaseTests(unittest.TestCase):
    def test_complete_matching_current_artifact_set(self):
        text = region("\n".join((
            "--version v0.4.0-alpha.9", "noderampart_0.4.0-alpha.9_amd64.deb.spdx.json",
            "0.4.0~alpha.9", "noderampart-0.4.0-0.alpha.10.fc44.x86_64.rpm",
            "--source-digest " + "9" * 40,
        )))
        self.assertEqual(CHECK.check_regions(text, "guide.md", IDENTITY), (1, []))

    def test_each_stale_current_identity_is_rejected(self):
        for stale in ("--version v0.4.0-alpha.8", "0.4.0~alpha.8",
                      "noderampart_0.4.0-alpha.8_arm64.deb.spdx.json",
                      "noderampart-0.4.0-0.alpha.9.fc43.aarch64.rpm.buildinfo.json",
                      "--source-digest " + "8" * 40):
            with self.subTest(stale=stale):
                self.assertTrue(CHECK.check_regions(region(stale), "guide.md", IDENTITY)[1])

    def test_historical_rollback_and_third_party_versions_are_allowed(self):
        text = ("Historical alpha.5: --version v0.4.0-alpha.5\n"
                "Intentional rollback: noderampart_0.4.0-alpha.7_amd64.deb\n"
                "Tools: Go 1.26.8, Syft v1.32.0, actions/checkout@" + "1" * 40 + "\n"
                + region("--version v0.4.0-alpha.9"))
        self.assertEqual(CHECK.check_regions(text, "guide.md", IDENTITY), (1, []))

    def test_bad_markers_fail(self):
        for text in (CHECK.START, CHECK.END, CHECK.START + "\n" + CHECK.START,
                     CHECK.START + CHECK.END):
            with self.subTest(text=text), self.assertRaises(ValueError):
                CHECK.check_regions(text, "guide.md", IDENTITY)

    def test_published_semantic_order_including_prereleases_and_pages(self):
        draft = release("v9.0.0")
        draft["draft"] = True
        unpublished = release("v8.0.0")
        unpublished["published_at"] = None
        data = [[release("v0.4.0-alpha.10"), draft, release("misc-tool-v10")],
                [release("v0.4.0-alpha.9"), unpublished]]
        self.assertEqual(CHECK.newest_published(data), "v0.4.0-alpha.10")
        self.assertEqual(CHECK.newest_published(data + [[release("v0.4.0")]]), "v0.4.0")
        self.assertEqual(CHECK.newest_published([release("v0.4.0-rc.1"),
                                               release("v0.4.0-beta.20")]), "v0.4.0-rc.1")

    def test_publication_sync_rejects_stale_local_record_and_ignores_development_version(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "docs").mkdir()
            (root / "LATEST_RELEASE").write_text("v0.4.0-alpha.9\n")
            (root / "VERSION").write_text("99.0.0-alpha.1\n")
            table = ("| Tag | `v0.4.0-alpha.9` |\n| Source commit | `" + "9" * 40 + "` |\n"
                     "| Project / DEB / RPM | `0.4.0-alpha.9` / `0.4.0~alpha.9` / `0.4.0-0.alpha.10.fc43/fc44` |\n")
            for name in CHECK.REQUIRED:
                (root / name).write_text(region("--version v0.4.0-alpha.9") + "\n" + table)
            listing = root / "releases.json"
            listing.write_text(json.dumps([release("v0.4.0-alpha.9")]))
            self.assertIn("PASS", CHECK.check(root, listing))
            listing.write_text(json.dumps([release("v0.4.0-alpha.10")]))
            with self.assertRaisesRegex(ValueError, "actual newest.*alpha.10"):
                CHECK.check(root, listing)

    def test_invalid_or_missing_publication_data_fails(self):
        for data in ([], {}, [None], [{"tag_name": "v1.0.0", "draft": "false"}],
                     [dict(release("v1.0.0"), published_at="2026-02-31T00:00:00Z")]):
            with self.subTest(data=data), self.assertRaises(ValueError):
                CHECK.newest_published(data)

    def test_older_gh_paginated_json_requires_every_page_to_parse(self):
        raw = json.dumps([release("v0.4.0-alpha.9")]).encode() + b"\n" + json.dumps([release("v0.4.0-alpha.10")]).encode()
        self.assertEqual(CHECK.newest_published(CHECK.release_pages(raw)), "v0.4.0-alpha.10")
        for invalid in (raw + b"\n[", b"", b"[]\n" * 21):
            with self.subTest(invalid=invalid), self.assertRaises(ValueError):
                CHECK.release_pages(invalid)


class LatestPublicationTests(unittest.TestCase):
    def setUp(self):
        self.latest = release("v0.4.0-alpha.11", id=11, prerelease=False)

    def test_alpha_maturity_with_regular_latest_metadata(self):
        listing = [[release("v0.4.0-alpha.10", id=10, prerelease=True)], [self.latest]]
        CHECK.check_published_latest(self.latest, listing, "v0.4.0-alpha.11")
        CHECK.check_published_latest(self.latest, [self.latest, release("tools-v99", id=99)],
                                     "v0.4.0-alpha.11")
        # No request-only make_latest field is needed as proof of API identity.
        self.assertNotIn("make_latest", self.latest)

    def test_higher_alpha_wins_product_order_but_old_stable_latest_fails(self):
        alpha = release("v0.5.0-alpha.1", id=51, prerelease=False)
        stable = release("v0.4.0", id=40, prerelease=False)
        listing = [stable, alpha]
        self.assertEqual(CHECK.newest_published(listing), alpha["tag_name"])
        CHECK.check_published_latest(alpha, listing, alpha["tag_name"])
        with self.assertRaisesRegex(ValueError, "Latest tag"):
            CHECK.check_published_latest(stable, listing, alpha["tag_name"])

    def test_latest_object_id_state_and_time_are_strict(self):
        invalid = [[], None, "Latest"]
        invalid += [dict(self.latest, id=value) for value in (True, False, 0, -1, "11", 11.0, None)]
        invalid += [{key: value for key, value in self.latest.items() if key != "id"}]
        invalid += [dict(self.latest, **{key: value}) for key in ("draft", "prerelease")
                    for value in (True, 0, "false", None)]
        invalid += [{key: value for key, value in self.latest.items() if key != missing}
                    for missing in ("draft", "prerelease", "published_at", "tag_name")]
        invalid += [dict(self.latest, published_at=value) for value in
                    ("2026-02-31T00:00:00Z", "yesterday", 0, None)]
        for latest in invalid:
            with self.subTest(latest=latest), self.assertRaises(ValueError):
                CHECK.check_published_latest(latest, [self.latest], self.latest["tag_name"])

    def test_complete_capture_identity_must_be_unique_and_consistent(self):
        conflicting = [dict(self.latest, id=12), dict(self.latest, prerelease=True),
                       dict(self.latest, draft=True),
                       dict(self.latest, published_at="2026-10-04T04:17:50Z"),
                       dict(self.latest, id=True)]
        listings = [[], [self.latest, self.latest], [self.latest, dict(self.latest, id=12)],
                    [self.latest, dict(self.latest, tag_name="v0.4.0-alpha.10")],
                    [self.latest, release("tools-v99", id=11)]]
        listings += [[item] for item in conflicting]
        listings += [[self.latest, dict(self.latest, draft=True)]]
        for listing in listings:
            with self.subTest(listing=listing), self.assertRaises(ValueError):
                CHECK.check_published_latest(self.latest, listing, self.latest["tag_name"])

    def fixture(self, root):
        (root / "docs").mkdir()
        (root / "LATEST_RELEASE").write_text(self.latest["tag_name"] + "\n")
        table = ("| Tag | `v0.4.0-alpha.11` |\n| Source commit | `" + "1" * 40 + "` |\n"
                 "| Project / DEB / RPM | `0.4.0-alpha.11` / `0.4.0~alpha.11` / `0.4.0-0.alpha.12.fc43/fc44` |\n")
        for name in CHECK.REQUIRED:
            (root / name).write_text(region("--version v0.4.0-alpha.11") + "\n" + table)
        releases = root / "releases.json"
        latest = root / "latest.json"
        releases.write_text(json.dumps([[self.latest]]))
        latest.write_text(json.dumps(self.latest))
        return releases, latest

    def test_optional_joint_input_cli_and_ordinary_offline_checks(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            releases, latest = self.fixture(root)
            self.assertIn("PASS", CHECK.check(root))
            self.assertIn("PASS", CHECK.check(root, releases))
            self.assertIn("PASS", CHECK.check(root, releases, latest))
            with self.assertRaisesRegex(ValueError, "requires --published-releases-json"):
                CHECK.check(root, latest_file=latest)
            command = [sys.executable, str(Path(__file__).with_name("check-current-release.py")),
                       "--root", str(root), "--published-releases-json", str(releases),
                       "--published-latest-json", str(latest)]
            result = subprocess.run(command, capture_output=True, text=True, timeout=10)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn("PASS", result.stdout)

    def test_malformed_duplicate_fields_and_bounded_captures(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            releases, latest = self.fixture(root)
            valid = latest.read_bytes()
            for raw in (b"{", b"[]", b"{} {}", b'\xff',
                        valid[:-1] + b', "id": 11}',
                        b" " * (CHECK.MAX_LATEST_BYTES + 1)):
                latest.write_bytes(raw)
                with self.subTest(raw_size=len(raw)), self.assertRaises(ValueError):
                    CHECK.check(root, releases, latest)
            latest.write_bytes(valid)
            releases.write_bytes(b"[" + valid[:-1] + b', "id": 11}]')
            with self.assertRaisesRegex(ValueError, "duplicate JSON"):
                CHECK.check(root, releases, latest)
            releases.write_bytes(b" " * (CHECK.MAX_RELEASES_BYTES + 1))
            with self.assertRaisesRegex(ValueError, "20 MiB"):
                CHECK.check(root, releases, latest)
            for data in ([[self.latest]] * 21, [self.latest] * 2001):
                with self.subTest(pages_or_entries=len(data)), self.assertRaises(ValueError):
                    CHECK.newest_published(data)


if __name__ == "__main__":
    unittest.main()
