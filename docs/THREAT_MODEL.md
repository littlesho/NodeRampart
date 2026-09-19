# Threat model

## Assets

- VPS availability and network access.
- Root and service-account boundaries.
- Telegram Bot token and privacy hash key.
- Remote IP observations, authentication usernames, traffic history, and security events.
- Integrity of binaries, configuration, systemd units, MMDB files, billing profiles, and SQLite state.
- Accuracy indicators that prevent missing data from appearing as zero attacks or zero traffic.

## Adversaries

- An unauthenticated remote host generating malformed or high-cardinality traffic.
- A remote host repeatedly attempting SSH authentication.
- A local unprivileged user trying to inject sensor/control messages or read security data.
- A compromised notification endpoint returning malicious text or excessive responses.
- A malicious or compromised dependency/build action.
- An operator mistake involving paths, permissions, thresholds, price profiles, or purge.

An attacker who already has root is outside the confidentiality boundary. NodeRampart should still avoid making persistence, credential discovery, or lateral movement easier.

## Major threats and controls

| Threat | Control in `v0.2.0-alpha` | Residual risk |
| --- | --- | --- |
| Packet parser memory corruption | Go bounds checks, explicit header/length checks, no C parser | Logical parser bugs and CPU load remain possible |
| Flow-cardinality exhaustion | Fixed per-batch key cap, overflow counters, global protocol totals, and kernel packet/drop statistics | Drops remain possible; statistics-read failures and reconciliation gaps are surfaced |
| Sensor IPC interruption | Bounded pending health counters, IPC send-loss estimates, atomic parser-error accounting, and a write deadline | No daemon persistence acknowledgment; pending counters are lost on sensor restart and attributed to the recovery hour |
| Privileged sensor compromise | Separate user, only `CAP_NET_RAW`, no AF_INET/AF_INET6, no payload persistence | `CAP_NET_RAW` remains sensitive; independent review is pending |
| Fake sensor messages | Socket mode `0660`, explicit sensor UID, strict frames and verified daemon UID | A compromised sensor UID can inject summaries |
| Unauthorized local control | Socket mode `0600`, root/daemon UID peer check, 16 concurrent handlers | Root can control or replace the service by definition |
| Config/path substitution | Absolute clean paths, regular-file and symlink checks, strict JSON | Parent path races need more adversarial testing |
| Secret disclosure | Token only in mode-`0600` file, no CLI token, redacted delivery errors, no token in DB | Host root and process memory can access it |
| Notification injection | HTML escaping, control-character removal, 4,096-byte cap | Social engineering within legitimate field text is still possible |
| Notification outage/flood | Persistent deduplicated outbox, retry backoff, 10,000-message/32-MiB pending caps | New notifications are dropped with a logged error once either cap is reached |
| SQLite corruption/full disk | Enforced active-file budget, reserved critical capacity, bounded retention/pruning, verified snapshots and independent failure health | Backups/filesystem overhead are outside the quota; corruption repair and power-loss behavior need further validation |
| Billing misinformation | Disabled by default, required source URL/effective date, “estimate” warning | Guest traffic cannot reproduce provider billing categories |
| Unsafe uninstall | Fixed purge targets, root check, symlink and nested-mount rejection before deletion, and service-stop checks | Privileged concurrent path/mount changes can still race preflight checks; package-manager behavior requires VM validation |
| Supply-chain action drift | GitHub Actions pinned to full commit SHAs, read-only workflow permissions, weekly dependency update proposals | Go module provenance and release attestations need expansion |

## Security invariants

1. Network content beyond headers is not sent from the sensor or stored.
2. Only the sensor has a Linux capability; the daemon and CLI have none.
3. Sensor input is bounded before allocation and before state growth.
4. A data-collection failure must be visible in status or report quality fields.
5. NodeRampart never makes firewall changes in the alpha.
6. Notification credentials never appear in process arguments, config dumps, SQLite, or normal errors.
7. Billing output always identifies itself as an estimate and carries a profile effective date.

## Required reviews before stable release

- AF_PACKET parser fuzzing and prolonged high-cardinality traffic.
- Unix socket ownership and peer behavior across Debian/Fedora package installs.
- systemd sandbox compatibility and `systemd-analyze security` review.
- SELinux enforcing policy on Fedora.
- SQLite migration, disk-full, power-loss, and corruption recovery.
- Telegram 401/403/429/5xx, TLS, DNS, and prolonged outage handling.
- Install, upgrade, remove, purge, symlink, and mount-point adversarial tests.
- Independent review of the sensor, IPC, packaging, release workflow, and secret handling.

## v0.2 reliability boundaries

Journal-assigned root UID, executable and service identity authenticate supported
OpenSSH origins. Complete log grammar isolates arbitrary usernames from endpoint
fields. Custom SSH units/paths need explicit compatibility work; trusting a
syslog tag or renamed process alone is insufficient.

The storage budget limits configured active database/journal files and rejects
writes before reserved capacity is exhausted. Independent health preserves
failed-operation evidence when a different kind of write succeeds. Consistent
backups contain the same sensitive data as the database and live outside the
active-file quota. Restore creates a new file, validates schema/integrity and
requires offline operator selection; it does not repair arbitrary corruption.

Static exports escape stored text and use no executable or remote content.
Notification errors omit upstream response bodies, request URLs and secrets.
Destination cooldown, quarantine and expiry are durable; queued is not sent.

## Disposable-VM network test boundary

The test-only namespace fixture requires explicit disposable-lab authorization,
root and VM detection. It verifies the namespace differs from PID 1 and matches
its harness handle, then restricts interfaces, addresses and routes to a fresh
pair with no host uplink. Traffic and execution time are bounded; cleanup owns
only the namespaces and child processes created by that invocation. This adds
no production capabilities and does not establish privileged lifecycle, packet
rate, or complete VM-matrix acceptance.
