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
| `golang.org/x/sys` | `v0.47.0` | BSD-3-Clause |
| `golang.org/x/term` | `v0.28.0` | BSD-3-Clause |
| `golang.org/x/text` | `v0.21.0` | BSD-3-Clause |
| `modernc.org/libc` | `v1.74.4` | BSD-3-Clause |
| `modernc.org/mathutil` | `v1.7.1` | BSD-3-Clause |
| `modernc.org/memory` | `v1.11.0` | BSD-3-Clause |
| `modernc.org/sqlite` | `v1.56.0` | BSD-3-Clause |

This inventory reflects the modules linked into the `v0.4.0-alpha` command binaries, including the local terminal interface. Test-only and build-tool dependencies are not part of the distributed binaries. Regenerate and review the inventory whenever `go.mod` changes. The original license and applicable patent-grant files for the added terminal modules are bundled unchanged.
