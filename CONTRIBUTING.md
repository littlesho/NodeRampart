# Contributing

NodeRampart accepts focused issues and pull requests while the API is still unstable.

1. Discuss large architecture, sensor, storage, packaging, or privilege changes before implementation.
2. Create a narrow branch and include tests for changed parsers, IPC, detector state machines, migrations, and notification formatting.
3. Run `make check`, `make test-race`, and `make build`.
4. Do not add network listeners, telemetry, automatic firewall writes, runtime-downloaded code, shell command templates, or new privileges without an approved threat-model update.
5. Keep dependencies minimal and explain their trust and maintenance implications.
6. Sign commits with `git commit -s` to certify the Developer Certificate of Origin.

Never use public infrastructure or third-party IP addresses for attack simulation. Privileged integration tests must run in an isolated, authorized lab.

Current installation examples must track [LATEST_RELEASE](LATEST_RELEASE), not
development `VERSION`. Run `python3 scripts/check-current-release.py` for current
documentation changes; keep historical/rollback examples explicitly scoped. Follow
the [release completion policy](docs/RELEASE_VERIFICATION.md#current-release-and-installer-policy):
publication includes current-documentation synchronization, required checks,
independent review, a normal PR merge and main readback in the same release task.
