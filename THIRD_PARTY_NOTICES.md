# Third-party notices

NodeRampart's original source code is licensed under MIT. Compiled binaries also contain the following Go modules, which remain under their respective licenses. Exact license texts are included under `third_party/licenses/` and are installed with binary packages.

| Module | Version | License |
| --- | --- | --- |
| `github.com/dustin/go-humanize` | `v1.0.1` | MIT |
| `github.com/gdamore/encoding` | `v1.0.1` | Apache-2.0 |
| `github.com/gdamore/tcell/v2` | `v2.8.1` | Apache-2.0 |
| `github.com/google/uuid` | `v1.6.0` | BSD-3-Clause |
| `github.com/lucasb-eyer/go-colorful` | `v1.2.0` | MIT |
| `github.com/mattn/go-runewidth` | `v0.0.16` | MIT |
| `github.com/oschwald/maxminddb-golang` | `v1.13.1` | ISC |
| `github.com/remyoudompheng/bigfft` | `v0.0.0-20230129092748-24d4a6f8daec` | BSD-3-Clause |
| `github.com/rivo/tview` | `v0.42.0` | MIT |
| `github.com/rivo/uniseg` | `v0.4.7` | MIT |
| `golang.org/x/sys` | `v0.48.0` | BSD-3-Clause |
| `golang.org/x/term` | `v0.28.0` | BSD-3-Clause |
| `golang.org/x/text` | `v0.42.0` | BSD-3-Clause |
| `modernc.org/libc` | `v1.77.1` | BSD-3-Clause |
| `modernc.org/mathutil` | `v1.7.1` | BSD-3-Clause |
| `modernc.org/memory` | `v1.12.1` | BSD-3-Clause |
| `modernc.org/sqlite` | `v1.60.1` | BSD-3-Clause |

This inventory lists the modules linked into the command binaries, including the local terminal interface. Test-only and build-tool dependencies are not part of the distributed binaries. Regenerate and review the inventory whenever `go.mod` changes. The original license and applicable patent-grant files for the added terminal modules are bundled unchanged.

SQLite 3.53.4 is public domain; its upstream dedication is bundled unchanged as
`third_party/licenses/modernc.org_sqlite_LICENSE-SQLITE`. The unchanged
`modernc.org/sqlite v1.60.1` upstream inventory is bundled as
`third_party/licenses/modernc.org_sqlite_LICENSE-3RD-PARTY.md`, including the
inherited notices for libc components such as musl. That upstream inventory
also describes optional packages, tests and build tools; it does not imply
those components are linked into NodeRampart. NodeRampart does not import
`modernc.org/sqlite/vec`, `modernc.org/sqlite/vfs` or `modernc.org/sqlite/pcache`.

The offline timezone name/link directory and test TZif files originate from
IANA tzdata 2025c, as distributed in Go 1.26.8's `lib/time/zoneinfo.zip`.
These timezone data are public domain under the [IANA tzdb 2025c license](https://data.iana.org/time-zones/tzdb-2025c/LICENSE); none of the separately licensed C source/man-page files are included.
The source ZIP checksum and update procedure are recorded in
`internal/timezones/names.txt` and [development](docs/DEVELOPMENT.md).
The standard Go `time/tzdata` fallback is part of the Go standard library.

离线时区名称/别名目录及测试 TZif 来自上述 IANA 2025c 数据，属于公有领域；
不包含该数据包中另有许可证的 C 源码或手册文件。来源摘要与更新步骤见上述文件。

The six alpha.8 native notification adapters are original NodeRampart code using
the Go standard library and the unchanged dependency graph. No vendor SDK,
OpenClaw plugin or documentation sample implementation was copied. Official
interface documentation informs protocol behavior; source licenses do not grant
service, distribution or commercial permissions. See the [channel terms and
authority notes](docs/NOTIFICATION_CHANNELS.md#slack), [privacy](docs/PRIVACY.md)
and [user agreement](docs/USER_AGREEMENT.md).
