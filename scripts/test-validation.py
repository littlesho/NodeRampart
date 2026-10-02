#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Verify validation fail-closed behavior and release revision binding with mocks.

No Go checks, Git writes, package installs, downloads or host actions run here.
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
HEAD = "1" * 40
MOCK = r'''
import json, os, pathlib, subprocess, sys
name = pathlib.Path(sys.argv[0]).name
args = sys.argv[1:]
with pathlib.Path(os.environ["VALIDATION_TEST_LOG"]).open("a") as stream:
    stream.write(json.dumps([name, args, os.environ.get("CGO_ENABLED")]) + "\n")
if name == "git":
    if args[:1] == ['-c']:
        if args[1] != 'safe.directory=' + str(pathlib.Path.cwd()):
            sys.exit('only the exact source root may be trusted')
        args = args[2:]
    if args[:1] == ["rev-parse"]:
        revision = args[-1].removesuffix('^{commit}')
        print(os.environ["VALIDATION_TEST_HEAD"] if revision == 'HEAD' else revision)
    elif args[:1] == ["show"]:
        print(os.environ.get('VALIDATION_TEST_DATE', '2026-09-30T00:00:00Z'))
    elif args[:1] == ["diff"]:
        sys.exit(1 if os.environ.get("VALIDATION_TEST_DIRTY") else 0)
    elif args[:1] == ["ls-files"]:
        if os.environ.get("VALIDATION_TEST_UNTRACKED"):
            print("internal/synthetic/untracked.go")
    else:
        sys.exit("unexpected fixture git command")
if name == 'python3' and args[:1] == ['-']:
    sys.exit(subprocess.run([sys.executable, *args], check=False).returncode)
if name + ":" + " ".join(args) == os.environ.get("VALIDATION_TEST_FAIL"):
    sys.exit(23)
'''


def workflow_jobs(source):
    """Read this repository's two-space job headers, without a YAML dependency."""
    section = source.split("\njobs:\n", 1)[1]
    headers = list(re.finditer(r"^  ([a-z][a-z0-9_-]*):\s*$", section, re.MULTILINE))
    jobs = {}
    for index, header in enumerate(headers):
        name = header.group(1)
        if name in jobs:
            raise ValueError("duplicate workflow job")
        end = headers[index + 1].start() if index + 1 < len(headers) else len(section)
        jobs[name] = section[header.end():end]
    return jobs


class ValidationTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="noderampart-validation-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.project = self.root / "project"
        (self.project / "scripts").mkdir(parents=True)
        (self.project / "VERSION").write_text("0.4.0-alpha.9\n")
        for name in ("validate.sh", "build-release.sh", "release-metadata.sh"):
            shutil.copyfile(REPO / "scripts" / name, self.project / "scripts" / name)
        package = self.project / "scripts/build-deb.sh"
        package.write_text("#!/bin/sh\nmkdir -p dist\nprintf 'synthetic package\\n' > dist/noderampart_0.4.0~alpha.9_${ARCH}.deb\nprintf 'package fixture reached\\n'\n")
        package.chmod(0o755)
        self.mocks = self.root / "mocks"
        self.mocks.mkdir()
        for name in ("go", "make", "python3", "git"):
            path = self.mocks / name
            path.write_text("#!" + sys.executable + "\n" + MOCK)
            path.chmod(0o755)
        self.log = self.root / "commands.jsonl"
        self.env = dict(os.environ, PATH=str(self.mocks) + ":/usr/bin:/bin",
                        VALIDATION_TEST_LOG=str(self.log), VALIDATION_TEST_HEAD=HEAD,
                        COMMIT=HEAD, BUILD_DATE="2026-09-30T00:00:00Z")
        self.env.pop("GITHUB_SHA", None)
        self.env.pop("CGO_ENABLED", None)

    def run_script(self, name, *arguments):
        return subprocess.run(["/bin/sh", str(self.project / "scripts" / name), *arguments],
                              env=self.env, capture_output=True, text=True, check=False, timeout=10)

    def commands(self):
        return [json.loads(line) for line in self.log.read_text().splitlines()] if self.log.exists() else []

    def test_required_checks_and_pinned_scanner_run_with_failures_propagated(self):
        result = self.run_script("validate.sh")
        self.assertEqual(result.returncode, 0, result.stderr)
        commands = self.commands()
        self.assertEqual(commands, [
            ["make", ["fmt-check"], None],
            ["go", ["mod", "verify"], None],
            ["go", ["vet", "./..."], None],
            ["go", ["test", "-count=1", "./..."], None],
            ["go", ["test", "-race", "-count=1", "-p=2", "-timeout=15m", "./..."], "1"],
            ["go", ["test", "-count=1", "-coverprofile=coverage.out", "./..."], None],
            ["go", ["build", "./cmd/..."], "0"],
            *[["python3", ["scripts/" + name], None] for name in (
                "test-packaging.py", "test-bootstrap.py", "test-release-sbom.py",
                "test-validation.py", "test-lab-check.py")],
            ["python3", ["scripts/test-netns.py", "--self-test"], None],
            ["go", ["run", "golang.org/x/vuln/cmd/govulncheck@v1.7.0", "./..."], None],
        ])
        self.log.unlink()
        self.env["VALIDATION_TEST_FAIL"] = "go:vet ./..."
        result = self.run_script("validate.sh")
        self.assertEqual(result.returncode, 23)
        self.assertFalse(any(row[0] == "go" and row[1][:1] == ["test"] for row in self.commands()))

    def test_bounded_race_checks_all_packages_and_propagates_failure(self):
        self.env["VALIDATION_TEST_FAIL"] = "go:test -race -count=1 -p=2 -timeout=15m ./..."
        result = self.run_script("validate.sh")
        self.assertEqual(result.returncode, 23)
        commands = self.commands()
        race = [row for row in commands if row[0] == "go" and "-race" in row[1]]
        self.assertEqual(race, [["go", ["test", "-race", "-count=1", "-p=2", "-timeout=15m", "./..."], "1"]])
        self.assertEqual(commands[-1], race[0])
        self.assertFalse(any(flag in row[1] for row in commands for flag in ("-short", "-run", "-skip")))
        required = (REPO / "scripts/validate.sh").read_text()
        for override in ("GOMAXPROCS=", "GORACE=", "GOFLAGS="):
            self.assertNotIn(override, required)
        self.assertEqual(required.count("-timeout"), 1)
        self.assertEqual(required.count("-timeout=15m"), 1)

    def test_make_race_uses_the_same_fixed_package_budget(self):
        make = shutil.which("make")
        self.assertIsNotNone(make, "make is required by the validation entry")
        result = subprocess.run(
            [make, "-f", str(REPO / "Makefile"), "test-race",
             "VERSION=0.4.0-alpha.9", "COMMIT=synthetic", "BUILD_DATE=synthetic"],
            cwd=self.project, env=self.env, capture_output=True, text=True,
            check=False, timeout=10)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.commands(), [
            ["go", ["test", "-race", "-p=2", "-timeout=15m", "./..."], "1"]])
        source = (REPO / "Makefile").read_text()
        self.assertEqual(source.count("-timeout"), 1)
        for override in ("GOMAXPROCS=", "GORACE=", "GOFLAGS="):
            self.assertNotIn(override, source)

    def test_fixed_budget_is_scoped_to_the_selected_workflow_jobs(self):
        budgets = {
            "ci.yml": {"safety": None, "test": 30, "build": 15,
                       "deb-package": 15, "rpm-package": 25},
            "release.yml": {"safety": None, "validate": 30, "deb": 20,
                            "rpm": 30, "sbom": 20, "draft": 10},
            "safety.yml": {"secrets": 5, "fuzz": 10},
        }
        for filename, expected in budgets.items():
            with self.subTest(workflow=filename):
                source = (REPO / ".github/workflows" / filename).read_text()
                jobs = workflow_jobs(source)
                self.assertEqual(set(jobs), set(expected))
                all_timeouts = []
                for name, minutes in expected.items():
                    # Require the job-level indentation as well as the exact
                    # value; duplicate, variable or added step budgets fail.
                    lines = re.findall(r"^[ \t]*timeout-minutes:.*$", jobs[name], re.MULTILINE)
                    wanted = [] if minutes is None else ["    timeout-minutes: " + str(minutes)]
                    self.assertEqual(lines, wanted, name)
                    all_timeouts.extend(lines)
                self.assertEqual(re.findall(r"^[ \t]*timeout-minutes:.*$", source, re.MULTILINE), all_timeouts)

    def test_workflow_budget_reader_distinguishes_jobs_from_steps(self):
        source = "\njobs:\n  test:\n    timeout-minutes: 30\n    steps:\n      - run: true\n        timeout-minutes: 7\n  build:\n    timeout-minutes: 15\n"
        jobs = workflow_jobs(source)
        self.assertEqual(set(jobs), {"test", "build"})
        self.assertEqual(re.findall(r"^    timeout-minutes:.*$", jobs["test"], re.MULTILINE),
                         ["    timeout-minutes: 30"])
        self.assertIn("        timeout-minutes: 7", jobs["test"])
        self.assertNotIn("timeout-minutes: 15", jobs["test"])

    def test_hosted_validation_rejects_wrong_or_changed_source_before_checks(self):
        for condition in ("wrong", "tracked", "untracked"):
            with self.subTest(condition=condition):
                self.env["GITHUB_SHA"] = "2" * 40 if condition == "wrong" else HEAD
                self.env.pop("VALIDATION_TEST_DIRTY", None)
                self.env.pop("VALIDATION_TEST_UNTRACKED", None)
                if condition == "tracked":
                    self.env["VALIDATION_TEST_DIRTY"] = "1"
                elif condition == "untracked":
                    self.env["VALIDATION_TEST_UNTRACKED"] = "1"
                if self.log.exists():
                    self.log.unlink()
                result = self.run_script("validate.sh")
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse(any(row[0] not in ("git", "python3") for row in self.commands()))
                self.assertNotIn("internal/synthetic", result.stderr)

    def test_local_dirty_validation_is_allowed_without_official_source_assertion(self):
        self.env["VALIDATION_TEST_DIRTY"] = "1"
        self.env["VALIDATION_TEST_UNTRACKED"] = "1"
        result = self.run_script("validate.sh")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(any(row[0] == "git" for row in self.commands()))

    def test_official_release_rejects_misbound_short_dirty_or_untracked_source(self):
        for condition in ("wrong", "short", "unknown", "tracked", "untracked"):
            with self.subTest(condition=condition):
                self.env.update(COMMIT=HEAD)
                self.env.pop("VALIDATION_TEST_DIRTY", None)
                self.env.pop("VALIDATION_TEST_UNTRACKED", None)
                if condition == "wrong":
                    self.env["COMMIT"] = "2" * 40
                elif condition == "short":
                    self.env["COMMIT"] = HEAD[:12]
                elif condition == "unknown":
                    self.env["COMMIT"] = "unknown"
                elif condition == "tracked":
                    self.env["VALIDATION_TEST_DIRTY"] = "1"
                else:
                    self.env["VALIDATION_TEST_UNTRACKED"] = "1"
                if self.log.exists():
                    self.log.unlink()
                result = self.run_script("build-release.sh", "--deb", "amd64")
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse(any(row[0] not in ("git", "python3") for row in self.commands()))
                self.assertNotIn("internal/synthetic", result.stderr)

    def test_clean_matching_release_reaches_build_with_fixed_artifact_exclusion(self):
        result = self.run_script("build-release.sh", "--deb", "amd64")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("package fixture reached", result.stdout)
        self.assertIn(["git", ["-c", 'safe.directory=' + str(self.project), "ls-files", "--others", "--exclude-standard", "--", ".", ":(exclude)release-input"], None], self.commands())
        self.assertIn(["make", ["build"], None], self.commands())
        self.assertTrue((self.project / 'dist/noderampart_0.4.0-alpha.9_amd64.deb').is_file())
        self.assertFalse((self.project / 'dist/noderampart_0.4.0~alpha.9_amd64.deb').exists())

    def test_source_metadata_is_verified_once_and_has_a_real_commit_date(self):
        self.env['EXPECTED_COMMIT'] = HEAD
        result = self.run_script('release-metadata.sh')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, 'commit=' + HEAD + '\nbuild_date=2026-09-30T00:00:00Z\n')
        for revision, date in (('', '2026-09-30T00:00:00Z'), ('2' * 40, '2026-09-30T00:00:00Z'),
                               (HEAD[:12], '2026-09-30T00:00:00Z'), (HEAD, '2026-02-30T00:00:00Z'),
                               (HEAD, '2026-09-30T00:00:00')):
            with self.subTest(revision=revision, date=date):
                self.env.update(EXPECTED_COMMIT=revision, VALIDATION_TEST_DATE=date)
                result = self.run_script('release-metadata.sh')
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(result.stdout, '')

    def test_official_release_rejects_missing_or_non_source_metadata(self):
        for commit, date in ((None, '2026-09-30T00:00:00Z'), (HEAD, None),
                             ('', '2026-09-30T00:00:00Z'), (HEAD, ''),
                             (HEAD, '2026-02-30T00:00:00Z'), (HEAD, '2026-09-30T00:00:00'),
                             (HEAD, '2026-09-29T00:00:00Z')):
            with self.subTest(commit=commit, date=date):
                self.env.update(COMMIT=commit, BUILD_DATE=date)
                for variable in ('COMMIT', 'BUILD_DATE'):
                    if self.env[variable] is None:
                        self.env.pop(variable)
                if self.log.exists():
                    self.log.unlink()
                result = self.run_script('build-release.sh', '--deb', 'amd64')
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse(any(row[0] == 'make' for row in self.commands()))

    def test_official_release_preserves_existing_portable_output(self):
        output = self.project / 'dist/noderampart_0.4.0-alpha.9_amd64.deb'
        output.parent.mkdir()
        output.write_bytes(b'retained previous artifact')
        result = self.run_script('build-release.sh', '--deb', 'amd64')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(output.read_bytes(), b'retained previous artifact')
        self.assertFalse(any(row[0] == 'make' for row in self.commands()))

    def test_ci_and_release_share_same_commit_safety_and_metadata_gates(self):
        safety = (REPO / '.github/workflows/safety.yml').read_text()
        ci = (REPO / '.github/workflows/ci.yml').read_text()
        release = (REPO / '.github/workflows/release.yml').read_text()
        for workflow in (ci, release):
            self.assertIn('uses: ./.github/workflows/safety.yml', workflow)
            self.assertIn('ref: ${{ github.sha }}', workflow)
        self.assertIn('fetch-depth: 0', safety)
        self.assertIn('551f6fc83ea457d62a0d98237cbad105af8d557003051f41f3e7ca7b3f2470eb', safety)
        self.assertIn('--ignore-gitleaks-allow --redact=100', safety)
        self.assertIn('--log-opts=\'--all --full-history\'', safety)
        self.assertEqual(safety.count('target: Fuzz'), 7)
        self.assertEqual(safety.count('ref: ${{ github.sha }}'), 2)
        self.assertIn('-fuzztime=30s -parallel=2 -timeout=2m', safety)
        self.assertEqual(release.count('COMMIT: ${{ needs.validate.outputs.commit }}'), 4)
        self.assertEqual(release.count('BUILD_DATE: ${{ needs.validate.outputs.build_date }}'), 4)
        self.assertIn('needs: [validate, safety, sbom]', release)
        self.assertEqual(release.count('needs: [validate, safety]'), 2)
        rpm_ci = ci.split('  rpm-package:', 1)[1]
        self.assertIn('fedora: [43, 44]', rpm_ci)
        self.assertIn('arch: [amd64, arm64]', rpm_ci)
        self.assertIn('container: registry.fedoraproject.org/fedora:${{ matrix.fedora }}', rpm_ci)
        self.assertIn('RELEASE_ARCH: ${{ matrix.arch }}', rpm_ci)
        self.assertIn('GIT_TEST_ASSUME_DIFFERENT_OWNER: "1"', rpm_ci)
        self.assertIn('./scripts/build-release.sh --rpm "$RELEASE_ARCH"', rpm_ci)
        self.assertIn('name: noderampart-fedora${{ matrix.fedora }}-${{ matrix.arch }}-rpm', rpm_ci)
        self.assertIn('--check-upload uploaded-release.json uploaded-assets.json', release)
        self.assertIn('PACKAGE="dist/noderampart_0.4.0-alpha.9_${RELEASE_ARCH}.deb"', release)
        self.assertIn('PACKAGE="dist/rpm/noderampart-0.4.0-0.alpha.10.fc${RELEASE_FEDORA}.${RPM_ARCH}.rpm"', release)
        self.assertEqual(release.count('subject-path: dist/release/noderampart_0.4.0-alpha.9_'), 2)
        self.assertEqual(release.count('subject-path: dist/release/noderampart-0.4.0-0.alpha.10.'), 4)


if __name__ == "__main__":
    unittest.main(verbosity=2)
