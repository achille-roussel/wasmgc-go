# 10 — Decisions

Achille answered the six open questions on 2026-11-22. The design documents are written to those answers. New design studies may add questions below; until answered they are not product law.

## 1. `int` is 32 bits

**Decision.** `int`, `uint`, and `uintptr` are `i32`. Every length, capacity, and offset in a slice, string, map, or chan header is `i32`. `int64` and `uint64` stay `i64`.

**Date.** 2026-11-22.

**Rationale.** `array.len` and `array.new` are `i32` operations, and a Wasm array cannot be longer than 2^31−1 elements. A 64-bit `len` cannot name more data than a 32-bit one. wasm3 used `i64` for `int` and `i32` for the headers, and extended or wrapped at every `len` (`docs/wasm3-study/01-type-mapping.md`). The 32-bit model matches `GOARCH=386`.

**Compatibility.** An untyped constant that does not fit in 32 bits is a `go/types` error, `constant … overflows int`, the same error 386 produces. A conversion from `int64` or `uint64` to `int` or `uint` that the checker cannot bound is a vet-style warning (`conversion from int64 to int may overflow; int is 32 bits`) and does not fail the build unless `-vet-fail` is set. `time.Duration` is `int64` and is not the problem. `int(d / time.Millisecond)` and `int(hash64)` are. The section is in `00-overview.md`.

## 2. `go` does not yield

**Decision.** `go f()` enqueues `f` and returns. The current goroutine runs until it parks, returns, or calls `runtime.Gosched`. `main` returning exits before a queued goroutine runs.

**Date.** 2026-11-22.

**Rationale.** The `go` statement happens-before the new goroutine starts. It does not happen-before a later statement in the parent. Yielding at every `go` would make racy programs pass for a reason Go does not promise. `Gosched` is the explicit yield. The host side of the exit rule is the macrotask-versus-microtask test in `prototypes/jspi-sched`.

## 3. Finalizers are no-ops

**Decision.** `runtime.SetFinalizer` and `runtime.AddCleanup` return without doing anything. `AddCleanup`'s `Cleanup.Stop` is a no-op. They do not panic.

**Date.** 2026-11-22.

**Rationale.** Wasm GC has no finalizer, and `FinalizationRegistry` does not observe a Wasm struct reference. A panic would reject upstream packages that register a finalizer and also call `Close`. A no-op lets those packages compile. The cost is a silent leak when the finalizer was the only release path.

**What leaks.** Named in `04-runtime-and-abi.md`. The ones that matter for this target: `net/fd_fake.go` (tag `js || wasip1`) will not close a dropped connection; `os/exec.go` will not release a process handle; the `os/exec` `Cmd` leak detector will not panic; `crypto/tls`'s certificate cache will not evict. `os/file_unix.go` is not compiled, because its tag requires `GOARCH=wasm` and ours is `wasmgc`. v1 opens no host file. A later `File` or `net` implementation closes on the explicit path and does not add a finalizer to make up for this.

## 4. No WASI in v1

**Decision.** The only host is JavaScript. There is no `GOOS=wasip1` mode, including a mode that rejects the `go` keyword.

**Date.** 2026-11-22.

**Rationale.** JSPI is a JS API. Core stack switching is not shipping, and the wasm3 WASI port did not link. A goroutine-free WASI target is a second runtime and a second CI matrix. The park import is the seam if that target is built later. It is not part of v1.

## 5. Dynamic `reflect` constructors are compile errors

**Decision.** A reachable call to `reflect.StructOf`, `FuncOf`, `ArrayOf`, `ChanOf`, `MapOf`, `SliceOf`, or `PointerTo` is a compile error when the compiler cannot prove the result type is already a Wasm type in the program. It is not a run-time panic.

**Date.** 2026-11-22.

**Rationale.** A panic fires inside a request, long after the compiler could have said the type cannot exist. Wasm struct types are fixed at module generation. The check has to be smart enough that `encoding/json` and `fmt` still compile, which they do, without overlays.

**How the proof works.** After dead-code elimination, let `B` be the types reachable code writes down. The universe is `B` plus one step: `*T`, `[]T`, and `map[T]struct{}` for a comparable `T`. One step, not the transitive closure, because `*` and `[]` together are infinite. Each `reflect.Type` value in the IR carries the set of Go types it can be. `TypeOf` on a concrete value is a singleton. `TypeOf` on an interface is the set of types that flow there, or all of `B` when that set cannot be bounded. `.Key` and `.Elem` of a type in `B` stay in `B`. A constructor's result is marked constructed and is not treated as a type the user wrote.

`PointerTo` and `SliceOf` of a type in `B` are legal, because that one step was added. `MapOf(k, struct{})` is legal for a key in `B`. `ChanOf`, `ArrayOf`, `StructOf`, and `FuncOf` are legal only when the exact result is already in `B`. A non-constant field list, a non-constant array length, or an unknown map value type is the compile error in `05-unsafe-and-reflect.md`. A call in a deleted function is not an error.

**Standard library.** With `goexperiment.jsonv2` on, the live json calls are `PointerTo` in `arshal_methods.go`, `arshal_funcs.go`, and `fields.go`; `SliceOf(t.Key())` and `SliceOf(t.Elem())` in `arshal_default.go`; and `MapOf(k.Type(), emptyStructType)` in the same file. All of them fall in the one-step closure. `fmt` has none. No overlay removes them. `BenchmarkTypeFieldsCache`, `TestBenchmarkUnmarshalUnknown`, the json topological-breadth case, and gob's `StructOf`/`ArrayOf` test do not fall in the closure. They are skip-listed, not patched.

## 6. Generics are monomorphized

**Decision.** Every generic function and every generic method is one copy per instantiation. There are no dictionaries.

**Date.** 2026-11-22.

**Rationale.** A dictionary erases the value to `anyref`. Go 1.27 generic methods are not interface methods, so they do not need a dictionary to be called through an itable. A size report that later names a specific function can justify a dictionary for that function. It is not the default, and it is not an open choice.

## Follow-ons from the six answers (not open product choices)

- `weak.Pointer` is unimplemented. `crypto/tls` uses it, together with `AddCleanup`, and stays out of v1 for both reasons. Implementing `weak` would be a new design, not a flip of decision 3.
- The vet warning on `int64` to `int` is off the build's failure path unless `-vet-fail`. That is the 386 shape (a hard error for constants, a warning for conversions) and it is part of decision 1, not a second question.

## Open

### 7. SIMD / `archsimd` on Wasm GC

**Question.** Should wasmgc-go try to support Go's experimental `simd/archsimd` (and SIMD `internal/bytealg`) before engines ship Multibyte Array Access, and if so with which representation?

**Options.**

| Option | Meaning | Tradeoff |
|---|---|---|
| **A. Defer (recommended)** | Keep `$go.bytes` as `(array i8)`. Vendor scalar `bytealg` generics through M8–M10. Add M11 when Multibyte Array Access is in Node without a flag: lower `archsimd.Load*` to GC-array `v128.load`, vectorize `bytealg`. | No representation risk. No speedup for byte kernels until the proposal ships. Matches Dart's dependency on the same proposal. |
| **B. Dual representation** | Hot `[]byte` / selected buffers as `(array v128)` chunks; general bytes stay `i8`. | `v128arr` prototype is ~9× scalar at 1 MiB. Breaks unaligned reslice-without-copy, complicates fat pointers, `unsafe.Slice`, and the immutable-header story (`03`, `05`). |
| **C. Bridge-first kernels** | Copy to linear memory, then `v128.load`, for `bytealg` and codecs. | One-shot copy+SIMD **loses** to scalar (`11-simd.md` tables). Only wins when a buffer is already bridged or scanned many times. |
| **D. Lane-build lowering** | Implement `archsimd.Load*` as 16× `array.get_u` + `replace_lane` now. | ~1.5× IndexByte/Count; Equal regresses. Code size up. User `archsimd` code "works" but is a poor advertisement. |

**Evidence.** `docs/design/11-simd.md`, `prototypes/simd/` (Node 26.13.1 and 22.23.4, 2026-11-23). Multibyte Array Access is phase 2 ([proposals list](https://github.com/WebAssembly/proposals/blob/main/README.md)).

**Recommendation.** **A.** Do not block M8. Do not change `$go.bytes`. Track the proposal; speak to the Wasm CG that Go needs byte-addressable loads from packed GC arrays for `[]byte` and `archsimd`. Revisit when a CI Node can run `v128.load` on an `(array i8)`.

**Date opened.** 2026-11-23.
