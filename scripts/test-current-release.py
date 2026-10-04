#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Offline current-documentation and release-publication synchronization regressions."""

import importlib.util
import json
from pathlib import Path
import tempfile
import unittest


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


if __name__ == "__main__":
    unittest.main()
