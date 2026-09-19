#!/bin/sh
# SPDX-License-Identifier: MIT
# Run in the trusted source-checkout job, before entering build containers.
set -eu
: "${EXPECTED_COMMIT:?expected source revision is required}"
case "$EXPECTED_COMMIT" in *[!0-9a-f]*) echo 'invalid expected revision' >&2; exit 1;; esac
[ "${#EXPECTED_COMMIT}" -eq 40 ] || { echo 'full expected revision required' >&2; exit 1; }
COMMIT=$(git rev-parse --verify HEAD^{commit})
EXPECTED_SOURCE=$(git rev-parse --verify "$EXPECTED_COMMIT^{commit}")
[ "$COMMIT" = "$EXPECTED_SOURCE" ] || { echo 'checkout does not match expected source commit' >&2; exit 1; }
BUILD_DATE=$(git show -s --format=%cI "$COMMIT")
python3 - "$COMMIT" "$BUILD_DATE" <<'PY'
import datetime, re, sys
commit, date = sys.argv[1:]
if not re.fullmatch(r'[0-9a-f]{40}', commit):
    raise SystemExit('invalid source commit')
if not re.fullmatch(r'[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(?:Z|[+-][0-9]{2}:[0-9]{2})', date):
    raise SystemExit('invalid source commit date')
datetime.datetime.fromisoformat(date.replace('Z', '+00:00'))
print('commit=' + commit)
print('build_date=' + date)
PY
