# `$GOROOT/test` runner (M10)

Compiles and runs the Go language suite under Node 26.13.1 with JSPI
(`WebAssembly.Suspending`) always required. See `docs/design/08-testing.md`
and `09-bootstrap-plan.md` (M10).

## Quick start

```bash
export PATH="/home/ubuntu/.local/go/bin:/home/ubuntu/.local/node-v26.13.1/bin:$PATH"
export WASMGC_GOROOT="${WASMGC_GOROOT:-/home/ubuntu/.local/go-src}"

# Smoke subset (CI-friendly)
go test ./testdata/gotest/ -count=1 -short

# Full // run suite (requires WASMGC_GOTEST=1)
WASMGC_GOTEST=1 go test ./testdata/gotest/ -run TestGOROOT -count=1

# Stdlib package tests (wasmgc-go test)
go test ./testdata/gotest/ -run TestStdlibPackages -count=1 -short
WASMGC_STDLIB_TEST=1 go test ./testdata/gotest/ -run TestStdlibPackages -count=1
```

## What runs

- Action `run` on top-level `*.go` and under `fixedbugs/`, `ken/`, `chan/`, …
- Expected stdout: sibling `.out` if present, else empty
- Exit code 0 unless the recipe uses `-1`
- Unsupported actions (`errorcheck*`, `compile*`, `rundir`, `asmcheck`, `build*`, …)
  are auto-skipped and counted in the summary (not listed in `skip.txt`)

## skip.txt

Format (em dash required):

```
<name> — <reason>
```

A skip without a reason fails `TestSkipListFormat`. Growing the list relative to
`origin/main` (or `WASMGC_SKIP_BASE`) fails `TestSkipListGrowth` unless
`skip_bump.txt` exists with a one-line reviewed justification.

Regenerate skips after a broad sweep:

```bash
WASMGC_UPDATE_SKIP=1 WASMGC_GOTEST=1 go test ./testdata/gotest/ -count=1 -update-skip -short=false
```

## packages.txt / stdlib/

`packages.txt` lists stdlib packages the stdlib-test layer should cover.
Per-package skips (including historical StructOf entries) live under `stdlib/`.

## Environment

| Variable | Meaning |
|---|---|
| `WASMGC_GOROOT` | Go source tree with `test/` (preferred) |
| `GOROOT` | Fallback source tree |
| `WASMGC_GOTEST=1` | Force full $GOROOT/test suite even under `-short` |
| `WASMGC_STDLIB_TEST=1` | Force full packages.txt stdlib suite even under `-short` |
| `WASMGC_UPDATE_SKIP=1` | Allow `-update-skip` to rewrite `skip.txt` |
| `WASMGC_SKIP_BASE` | Git ref for skip growth comparison (default `origin/main`) |
