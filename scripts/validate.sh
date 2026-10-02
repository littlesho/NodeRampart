#!/bin/sh
# SPDX-License-Identifier: MIT
# Shared required checks for local candidates, CI and release validation.
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
PROJECT_DIR=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
cd "$PROJECT_DIR"
[ "$#" -eq 0 ] || { echo 'usage: validate.sh' >&2; exit 1; }

# Hosted checks must validate the checked-out event commit. Local dirty-tree
# validation remains useful but makes no claim of a clean release revision.
if [ -n "${GITHUB_SHA:-}" ]; then
  CHECKED_COMMIT=$(git rev-parse --verify HEAD)
  [ "$CHECKED_COMMIT" = "$GITHUB_SHA" ] || { echo 'validation checkout does not match the workflow commit' >&2; exit 1; }
  git diff --quiet HEAD -- || { echo 'validation source differs from the workflow commit' >&2; exit 1; }
  UNTRACKED_INPUTS=$(git ls-files --others --exclude-standard -- . ':(exclude)release-input')
  [ -z "$UNTRACKED_INPUTS" ] || { echo 'validation source contains untracked inputs' >&2; exit 1; }
fi

make fmt-check
go mod verify
go vet ./...
go test -count=1 ./...
# Bound independent package processes competing for SQLite/CPU resources;
# each package still runs every test with its ordinary goroutine concurrency.
CGO_ENABLED=1 go test -race -count=1 -p=2 ./...
go test -count=1 -coverprofile=coverage.out ./...
CGO_ENABLED=0 go build ./cmd/...
python3 scripts/test-packaging.py
python3 scripts/test-bootstrap.py
python3 scripts/test-release-sbom.py
python3 scripts/test-validation.py
python3 scripts/test-lab-check.py
python3 scripts/test-netns.py --self-test
go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./...
