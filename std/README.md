# Standard library pin

The compiler's standard library is **go1.27.4**, materialized into a cache GOROOT
by `internal/stdlib` and checked by `wasmgc-go std check`.

## Pin source

| Field | Value |
|---|---|
| Version | `go1.27.4` (see `PIN`) |
| Tarball | https://go.dev/dl/go1.27.4.src.tar.gz |
| SHA-256 | `73cfb8571aec0c01177687db87163d46aa20e9779672772891aa17654d9a6099` |

The upstream tree is **not** committed here. `Materialize` downloads the tarball
into `$(UserCacheDir)/wasmgc-go/`, verifies the checksum, extracts to
`src-go1.27.4/`, then builds `goroot-go1.27.4/` with the manifest applied.

When the running toolchain's `GOROOT` already reports `go1.27.4`, that tree is
used as the upstream source (same pin) to avoid a redundant download.

## Layout

| Path | Role |
|---|---|
| `PIN` | Version, URL, checksum |
| `manifest.json` | Per-package tier: rewrite / vendor / overlay / out |
| `overlay/` | Full-file replacements; first line `// overlay of go1.27.4 …` |
| `src/` | Rewrite packages that replace upstream entirely |

## Commands

```
wasmgc-go std materialize   # build the cache GOROOT
wasmgc-go std check         # type-check + unsafe-check supported packages
wasmgc-go std inventory     # write docs/stdlib/inventory.json
```
