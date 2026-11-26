# 06 — Standard library strategy

The standard library is the Go 1.27.4 tree, filtered and patched, plus a small set of packages written for this compiler. It is not a second copy of `src/runtime` with `*_wasm3.go` twins. That copy is most of the 519 files in the wasm3 diff (`docs/wasm3-study/05-file-index.md`), and it exists because a fork of `cmd/compile` still had to compile the upstream library. This compiler does not.

The pin is the source tarball of `go1.27.4` (released 2026-11-10, `VERSION` file `go1.27.4`). `wasmgc-go std check` refuses a different GOROOT. A bump is a deliberate change to the pin, a re-run of the checker, and an update of every overlay whose header names the old version.

## Three tiers

**Rewrite.** The package in the module replaces the upstream package entirely. The file list comes from `std/src/<import>/`. These packages talk about stacks, the heap, the scheduler, or a byte layout of a type descriptor.

**Vendor.** The upstream files that survive the build-tag re-filter (`01-architecture.md`) are compiled as they are. The checker from `05-unsafe-and-reflect.md` must accept them.

**Overlay.** One or more upstream files are replaced by a file in `std/overlay/<import>/<file>.go`. The replacement is a full file, not a diff. The first line is a comment `// overlay of go1.27.4 <import>/<file>.go`. `std check` fails when the pin and the comment disagree. A full file rebases more honestly than a patch that applied fuzzy: either it still compiles, or it does not.

Anything else is **out**. Importing it is a compile error that names the package and the tier it would need. `cgo`, `plugin`, `runtime/cgo`, and `syscall` on a real operating system are out. The user's own module is never in a tier; it is just compiled, and the checker still runs.

## The materializer

`internal/stdlib` builds a GOROOT-shaped tree in the user cache, `$(UserCacheDir)/wasmgc-go/goroot-go1.27.4/`, and `internal/load` points `go list` at it via `GOROOT`.

For each package in `std/manifest.json`:

| Tier | What lands in the cache tree |
|---|---|
| `rewrite` | only `std/src/<import>/*.go` |
| `vendor` | upstream `.go` files, after the re-filter. No `.s` files. |
| `overlay` | the vendor set, with named files replaced from `std/overlay` |
| `out` | the directory is empty and the import is an error |

The manifest is the list. A package that is not listed and that lives in the upstream tree is `vendor` by default, then demoted when the checker finds a rejected `unsafe` operation. `std check` prints the demotion and a non-zero status. CI runs it. A new Go release does not silently compile a file that started doing pointer arithmetic.

Build tags inside a vendored file are evaluated as `GOOS=js`, `GOARCH=wasmgc`, tags `wasmgc`. The practical consequences, checked against `go1.27.4` `internal/bytealg`:

- `indexbyte_native.go` is tagged `wasm` among other archs, so it drops.
- `indexbyte_generic.go` is tagged `!wasm` (and `!386`, `!amd64`, …), so it comes back.
- `*_wasm.s` is not a Go file and is not copied.

So `internal/bytealg` vendors the generic implementations with no overlay, as long as the re-filter runs. The loader's re-filter is load-bearing for that. Compiling with raw `GOARCH=wasm` and no re-filter would select the assembly stub and fail.

Hand-written **Wasm** kernels (`*_wasmgc.wat`) are a later optimization lane for `internal/bytealg` and runtime hashing, not a requirement for vendoring the generics. Discovery, binding, and merge are specified in [`12-wasm-assembly.md`](12-wasm-assembly.md). Native `.s` stays out of the cache tree.

## Rewrites

| Package | Why it cannot be vendored |
|---|---|
| `runtime` | The upstream runtime is the linear-memory scheduler and GC. The new one is `04-runtime-and-abi.md`. |
| `internal/abi` | `Type` is a byte structure walked with `unsafe.Add` (`type.go` in the pin). Replaced by the descriptor helpers `reflect` actually calls. `NoEscape` is the identity. |
| `internal/reflectlite` | Same walk. Implemented on the descriptors, for `errors` and `sort`. |
| `reflect` | Same, plus `Value`. `05-unsafe-and-reflect.md`. |
| `internal/runtime/maps` | Swiss tables. Not linked. The compiler's map is the one in `03` and `04`. |
| `internal/runtime/atomic` | Upstream files are per-arch assembly. Replaced by the plain loads and stores of `04`. |
| `sync/atomic` | The API stays. The bodies are the plain operations. `Value` stores one `any`, which is the wasm3 shape (`docs/wasm3-study/03-stdlib-and-unsafe.md`) and is right here because an iface value is one immutable reference. |
| `syscall` | The upstream tree is OS calls as `uintptr`. Replaced by a JS-shaped `syscall` that returns `ENOSYS` for the filesystem and implements `Write` on fd 1 and 2 through `runtime.write`. |
| `syscall/js` | Rewritten on `externref`. `07-host-and-js.md`. The upstream package is the linear-memory value table and does not match this ABI. |
| `internal/bytealg` | Vendored generic Go, listed here only because the runtime and `strings` depend on it and the re-filter is what makes the generic file win. If a future pin deletes the generic file, it becomes an overlay. Optional `*_wasmgc.wat` kernels ([`12-wasm-assembly.md`](12-wasm-assembly.md), decisions 8–12 in `10`) may replace hot paths without changing the Go API; those asm functions default to `nosuspend` (decision 11). SIMD replacements for `IndexByte` / `Equal` / `Count` / `Compare` / `Index` on GC arrays wait for gated **M11** (decision 7): `$go.bytes` stays `(array i8)` until Multibyte Array Access is in CI's Node (`11-simd.md`). Not a pre-M8 rewrite. |

`runtime` is small on purpose. The entrypoint, print, panic, maps' shared path, channels' shared path, park, and the linear bridge. The study's list of seventy `*_wasm3.go` mirrors is the set of files we do not write.

### Linking rewrite `runtime` print without a full GOROOT

`println` / `print` lower to calls named `runtime.printstring` / `printint` / `printnl` without a Go `import "runtime"`. The pipeline's `internal/rtlink` therefore reads `std/src/runtime/print.go` from the compiler module root and appends those funcs to the IR before `RuntimeOps`, even when `-skip-stdlib` skipped materialize. `printnl` and `printint` are ordinary Go; `printstring` is body-less and codegen fills the linear-bridge body until `printstring_wasmgc.wat` (see [`12-wasm-assembly.md`](12-wasm-assembly.md)). A program that `import`s `runtime` still loads the full rewrite from the materialized GOROOT as usual.

## Overlays

Seeded from a scan of `go1.27.4` `src`, excluding `cmd`, `runtime`, and `internal/runtime`, for `uintptr(unsafe.Pointer`, `unsafe.Pointer(uintptr`, and `unsafe.Add(`. These are the hits in packages a normal program imports. The scan is not exhaustive; `std check` is.

| Package | File | What the overlay changes |
|---|---|---|
| `slices` | `slices.go` | `overlaps` returns whether both slices have `ref.eq` backings and the index ranges overlap. Upstream compares integer addresses (`slices.go` around the `overlaps` function). `array.copy` handles overlap inside one array; the function still has to be right for `slices.Insert` and friends. |
| `sync` | `cond.go` | Delete the `copyChecker` that stores `uintptr(unsafe.Pointer(c))`. Copy detection of `Cond` is not available. The rest of `Cond` stays. |
| `sync` | `pool.go` | `indexLocal` cannot advance an `unsafe.Pointer` by `i * sizeof`. `Pool` in v1 is a single local plus the victim list, not a per-P array. `runtime_procPin` returns 0. The pool still caches. |
| `fmt` | `print.go` | `fmtPointer` used `uintptr(value.UnsafePointer())`. Overlay prints `0x0` / `0x?` placeholders. Missed by the text scan in `05`. |
| `errors` | `wrap.go` | Upstream `Is`/`Unwrap`/`Join`; overlay `As` uses interface type-assert targets (e.g. `Timeout`) instead of reflect walks that need more of the universe. |
| `regexp` | `onepass.go` | `compileOnePass` stubbed (`makeOnePass` panics); the NFA engine stays. |
| `math/bits` | `bits_errors.go` | Local `divideError` / `overflowError` (no `var` linkname into the runtime). |
| `strings` | `builder.go` | `Builder` without `abi.NoEscape` / `MakeNoZero`; `String` shares the `[]byte` backing. |
| `encoding/binary` | `native_endian_little.go` | Upstream is tagged with `wasm` among little-endian archs; `GOARCH=wasmgc` drops it. Overlay restores little-endian `NativeEndian` for wasmgc. |
| `internal/cpu` | `cpu_wasmgc.go` | Added. Upstream `cpu_wasm.go` is dropped by the `*_wasm.go` filename rule; wasmgc needs `CacheLinePadSize` / `doinit`. |
| `internal/goarch` | `goarch_wasmgc.go`, `zgoarch_wasmgc.go` | Same twin pattern. `GOARCH=wasmgc`, `IsWasm=0`. |
| `hash/maphash` | `maphash.go` | Deferred: no rejected conversion on this pin (`unsafe.Pointer(&buf[0])` is supported). Overlay later when Comparable needs the descriptor `hash` funcref. |
| `crypto/internal/fips140/alias` | `alias.go` | `AnyOverlap` uses the same backing-identity test as `slices.overlaps`. Upstream compares addresses. |
| `os` | — | Under wasmgc, `file_unix.go` / `stat_js.go` are tagged `js && wasm` and drop. M8 uses a rewrite placeholder for stdio so `fmt` type-checks. M9 replaces it. |
| `math` | `floor_asm.s` and the arch files | Not copied. The generic `floor_noasm.go` path is selected when the arch is `wasmgc`. Stays **vendor**. |

### Additional rewrites (post-M8)

| Package | Why |
|---|---|
| `math/rand/v2` | Minimal `IntN` / `New` / `NewPCG` stub for `sort` / `slices` tests. |
| `internal/testenv` | Thin `Builder` / `OptimizationOff` / `MustHaveExec` stubs; avoids `os/exec` and `SysProcAttr`. |
| `time` | Still a **rewrite**: `Now` / `Since` / `Sub` via `runtime.Walltime`, `Sleep` via `TimeSleep`. `Format` / `Parse` / `AppendFormat` are a thin subset (Kitchen, DateOnly, TimeOnly, RFC3339/RFC3339Nano always Z; other layouts fall back to Unix-nano decimal) — not a full upstream layout engine. |

Upstream `strings.Builder` on this pin used `abi.NoEscape`; the overlay above is what the materializer installs.

### Inventory findings (stream H, go1.27.4)

`wasmgc-go std inventory` writes `docs/stdlib/inventory.json` from the materialized GOROOT (369 packages). After rewrites/overlays:

- Remaining rejected-unsafe sites outside rewrites: `internal/runtime/gc/scan`, `crypto/internal/fips140/subtle`, `crypto/internal/boring/bcache` (not on the M8 path).
- Finalizer / `AddCleanup` sites still in vendored sources: `net`, `os/exec`, `crypto/tls`, `crypto/internal/fips140cache`.
- Dynamic reflect in production non-test files: `encoding/json/v2` (`SliceOf`/`MapOf`/`PointerTo`, accepted by the universe rule), plus `encoding/gob`, `net/rpc`, `text/template`.
- `std check` on the manifest supported set is green for the M8 set once rewrite placeholders exist for `runtime`, `reflect`, `internal/abi`, `internal/reflectlite`, `sync/atomic`, `syscall`, `syscall/js`, and `os`.

## Vendored as-is, and required early

These are the packages the bootstrap plan compiles unchanged, once the rewrites they import exist. Confirmed against the pin by `wasmgc-go std check`: their non-test sources (after overlays) do not contain the rejected conversions.

| Needed for | Packages |
|---|---|
| errors and sentinel wrapping | `errors`, `internal/reflectlite` (rewrite) |
| `fmt.Println` of numbers and strings | `fmt` (overlay), `strconv`, `unicode/utf8`, `io`, `sync` (overlay), `reflect` (rewrite) |
| `fmt` of structs and maps | the same, plus the reflect field and map walks |
| sorting | `sort`, `slices` (overlay), `cmp` |
| strings | `strings`, `internal/bytealg` (generic), `unicode` |
| bytes | `bytes` |
| JSON | `encoding/json`, `encoding/json/v2`, `encoding/json/jsontext`. The v1 API in this pin is backed by v2 ([Go 1.27 release notes](https://go.dev/doc/go1.27)). The reflect uses that implies are the closure in `05`. |
| time | `time`, once `runtime.walltime` and timers exist. Zone data: UTC unless the program imports `time/tzdata`. No `syscall/js` timezone lookup in v1. |
| math used by strconv and json | `math`, generic paths |

`encoding/binary` is an overlay only for the native-endian file selection under wasmgc; the rest vendors. It uses `reflect`, not an in-memory overlay, on this pin. A test file casts a pointer at an integer; tests are not the library.

`encoding/json` is compiled with `goexperiment.jsonv2` set, which is the Go 1.27 default. Files tagged `!goexperiment.jsonv2` (`encode.go`, `decode.go`) are not in the build. The v2 files call `reflect.PointerTo`, `SliceOf`, and `MapOf`. The analysis in `05-unsafe-and-reflect.md` accepts every one of those production calls, because the results are the one-step closure (`*T`, `[]T`, `map[T]struct{}`). `fmt` calls none of the seven constructors. Neither package gets an overlay to delete a reflect constructor. The tests that call `StructOf` (`BenchmarkTypeFieldsCache`, `TestBenchmarkUnmarshalUnknown`, the topological-breadth json case, and `encoding/gob`'s `StructOf`/`ArrayOf` test) are compile errors and are listed in `skip.txt`, not patched.

## Priority, as a build order

The order is the bootstrap plan's library axis. Each row is usable when the row above it is.

1. **Builtin print.** `runtime` print and the linear bridge. No `fmt`. This is the study's `println("hello, world")` at 3,718 bytes.
2. **`errors`.** Needs `internal/reflectlite`.
3. **`strconv` and `unicode/utf8`.** Pure once `bytealg` generics link. `math` is a dependency of `strconv` for `FormatFloat`. UTF-8 validation stays scalar until the SIMD milestone in `11-simd.md`.
4. **`strings`, `bytes`, `sort`, `slices`.** `slices` needs its overlay. `strings.Builder` needs `NoEscape`. `bytes` / `strings` call `bytealg`; they inherit the scalar generics until `11` says otherwise.
5. **`sync`.** Overlay on `Cond` and `Pool`. `Mutex` is the runtime's mutex, exposed by `sync` as the usual API. `sync.Once`, `WaitGroup`, `Mutex` are the ones `fmt` and tests touch.
6. **`reflect`, then `fmt`.** `fmt` has no `unsafe` of its own. It does not work until `reflect.Type` and `Value` can describe a struct, walk its fields, and print a map. That is the long pole.
7. **`encoding/json`.** After `fmt` and `reflect`, including `SliceOf` and `MapOf` of the closed universe. Marshal and unmarshal of a struct, a `map[string]int`, and a `map[int]string` (the non-string key path).
8. **`time`.** `Now`, `Since`, `Sleep`, `NewTimer`, `Tick`. Unbuffered timer channels.
9. **`os`.** `Stdout`, `Stderr`, `Stdin`, `Args`, `Environ`, `Exit`. File operations return an error that wraps `syscall.ENOSYS`. `os.Args` comes from the host. `os/file_unix.go` is not compiled: its tag is `unix || (js && wasm)`, and `wasm` is not our arch, so the `SetFinalizer` that closes a collected `*File` is not in the binary. v1 opens no host file. A later implementation that does must `Close` in `Close`. `SetFinalizer` would not run (`04-runtime-and-abi.md`). `os/exec.go` is built (no tag) and its `AddCleanup` on a process handle is a no-op; `os/exec` stays out of v1, and a leaked `Process` would keep the handle.
10. **`net/http` client via `fetch`.** Not the upstream dialer. A small `net/http` transport overlay uses `syscall/js` to call `fetch` and read the body. `ListenAndServe` is out of v1. Upstream `net/http` is large and pulls `net`, which pulls sockets. The v1 HTTP client is an overlay package path `net/http` that implements `Get`, `Head`, `Post`, and `Do` for `http` and `https` URLs and returns `ErrUnsupported` for anything that needs a dial. The rest of `net` stays out until that overlay is honest about what it does not do. When `net` is vendored, `net/fd_fake.go` (tag `js || wasip1`) is part of the build and its `SetFinalizer(fd, (*netFD).Close)` does nothing. A connection that is not `Close`d leaks the host resource. The overlay or the vendored `net` must `Close` on the explicit path and must not grow a finalizer to compensate.

`crypto`, `net` for real sockets, `os/exec`, and `database/sql` are past v1. They are not rejected forever. They wait on a package-by-package checker run, not on a new ABI.

## Updating the pin

1. Change the pin in `std/PIN` and in `go.mod`'s `toolchain` line together.
2. Run `wasmgc-go std check` against the new GOROOT. It prints every package that moved from vendor to a checker failure.
3. For each failure, either add an overlay (full file, header naming the new version) or move the package to `out` or `rewrite`.
4. Re-run the stdlib tests the bootstrap plan lists for the packages already marked done.
5. The old overlay headers fail the version check until they are rebased. There is no mode that skips the header.

Go 1.27's `encoding/json` is the v2 implementation behind the v1 API, with `GOEXPERIMENT=nojsonv2` as an upstream escape hatch. We do not carry that hatch. The vendored sources are the default v2-backed files. A pin bump that changes JSON error strings updates the tests that match those strings; behavior differences are upstream's release notes, not a quiet fork.

## What we will not do

We will not reimplement the standard library in advance of a program that imports it. A package stays upstream, compiled by our checker, until the checker says it cannot. The wasm3 mirrors reimplemented packages so the existing build tags would compile. That constraint is gone, and the mirrors do not come back.
