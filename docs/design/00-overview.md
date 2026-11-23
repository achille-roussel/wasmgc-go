# 00 — Overview

`wasmgc-go` is a standalone Go compiler and driver. Its only code generator emits WebAssembly GC: structs, arrays, and typed references, collected by the host. It exists because a Go program on `GOARCH=wasm` carries a garbage collector, an allocator, and a goroutine scheduler in linear memory, and a program on the `wasm3` experiment did not.

## What shipped in the experiment

The `wasm3` branch (`github.com/achille-roussel/go`, HEAD `c8232bb660`, 612 commits on Go 1.27-dev) is studied in [`docs/wasm3-study/`](../wasm3-study/). The numbers this design treats as load-bearing, all from that study:

| Program | `GOARCH=wasm` | `GOARCH=wasm3` |
|---|---:|---:|
| empty `main`, study re-run, `GOOS=js`, `-ldflags='-s -w'` | 1,824,076 bytes | 3,640 bytes |
| `println("hello, world")` | 1,824,287 | 3,718 |
| earlier M2 snapshot, empty `main`, wasip1, almost no runtime | 1,829,590 | 1,775 |

The ratio on the re-run is about 501×. The code section of the wasm3 empty main was 1,488 bytes. The M2 snapshot's code section was 17 bytes, before JSPI, maps, and defer existed. The size win survived the later runtime because a program that does not call the runtime does not link it. That only works if the compiler never emits a call to a garbage collector.

Two things in that branch are the design this repository keeps, and they are properties of the object model rather than of `cmd/compile`:

- Heap objects are Wasm GC objects. `struct.new` and `array.new` allocate. There is no linear Go heap and no write barrier.
- On a JS host, a goroutine is a [JS Promise Integration](https://github.com/WebAssembly/js-promise-integration/blob/main/proposals/js-promise-integration/Overview.md) stack. Blocking is a suspending import. The Go scheduler is not linked.

The branch also showed the shape this repository refuses. It was a second copy of the Go compiler and a second copy of the runtime, maintained as `*_wasm3.go` files beside the originals (`docs/wasm3-study/00-overview.md`, `docs/wasm3-study/04-open-problems.md`). The Go SSA backend assumed linear memory, so the wasm3 backend grew a relooper (`src/cmd/compile/internal/wasm3/cfg.go` in that tree). A TinyGo attempt cannot be the answer either: LLVM has no representation of the Wasm GC memory model, so a TinyGo-style LLVM backend cannot emit `struct.get`.

## Goals

- Compile Go source to one Wasm GC module plus a small JS glue file.
- Keep the empty-main binary under 2 KiB and a builtin `println` of a string under 8 KiB, on the order of the M2 and study measurements, without a tag section or a pclntab the target cannot walk.
- Implement the Go language that `go/types` of Go 1.27 accepts, including generics and generic methods, with the exceptions in "Language coverage" below spelled out as diagnostics rather than as silent wrong code.
- Run goroutines, channels, `select`, and `time.Sleep` on JSPI, with a waiter queue and a real park, which the wasm3 runtime did not have (`docs/wasm3-study/02-runtime-and-abi.md`).
- Make `fmt.Println`, `encoding/json`, and a JS `fetch` HTTP client real milestones, in that order (`09-bootstrap-plan.md`).

## Non-goals

- Registering a `GOARCH` with the upstream Go toolchain, or patching `cmd/compile`.
- Linking the Go garbage collector, the Go allocator, or a linear-memory goroutine stack. `runtime.mallocgc` does not exist.
- `cgo`, plugins, or assembly (`.s` files).
- `uintptr(unsafe.Pointer)` and pointer arithmetic. The supported `unsafe` subset is specified in `05-unsafe-and-reflect.md`.
- `reflect.StructOf` and any other API that would mint a Wasm struct type at run time. Wasm types are closed at module generation.
- WasmFX / core stack switching as a goroutine mechanism. As of the November 2026 spec check it is still a phase-3 proposal, V8's `wasmfx` flag defaults off, and Wasmtime's tracking issue ([bytecodealliance/wasmtime#10248](https://github.com/bytecodealliance/wasmtime/issues/10248), updated 2026-11-03) still lists GC integration as unfinished. The wasm3 toolchain emitted `cont.new` and then stubbed resume because engines would not run it (`docs/wasm3-study/04-open-problems.md`). This compiler emits no continuation types.
- WASI as a v1 host. JSPI is a JS API. A non-JS host has no substitute that ships. Decided 2026-11-22 (`10-open-questions.md`).
- Preemptive scheduling. A goroutine runs until it parks. A tight loop holds the thread.

## Relationship to the other compilers

| | `cmd/compile` `GOARCH=wasm` | TinyGo | wasm3 | wasmgc-go |
|---|---|---|---|---|
| Front end | `cmd/compile` | its own, plus LLVM | `cmd/compile` | `go/parser`, `go/types` |
| IR | Go SSA, linear memory | LLVM IR | Go SSA plus a relooper | structured IR, this repo |
| Heap | linear memory, Go GC | linear memory, its GC | Wasm GC | Wasm GC |
| Goroutines | unwind the Wasm stack, JS scheduler | cooperative, or asyncify | JSPI, one waiter per channel side | JSPI, waiter queues |
| `int` | 64-bit | 32-bit on wasm | 64-bit `int`, 32-bit headers | 32-bit, headers included |

wasm3's `int` split is the one this design closes. `ArchWasm3.PtrSize` was 8, so the type checker said `int` was `i64`, while every slice, string, map, and chan header stored lengths as `i32` and codegen inserted `i64.extend_i32_u` / `i32.wrap_i64` at each use (`docs/wasm3-study/01-type-mapping.md`). Wasm `array.len` and `array.new` are `i32` operations ([core spec, aggregate instructions](https://www.w3.org/TR/wasm-core-2/), W3C Candidate Recommendation Draft of 20 November 2026). An array cannot be longer than 2^31−1 elements. A 64-bit `int` cannot represent a longer slice on this target, and it taxes every `len`, index, and `make`.

**`int`, `uint`, and `uintptr` are `i32`. `int64` and `uint64` stay `i64`. Every length, capacity, and offset in a header is `i32`.** Achille confirmed this on 2026-11-22. It is the same integer model as `GOARCH=386`.

### 32-bit `int` compatibility

Code written for amd64 sometimes treats `int` as a 64-bit register. Three patterns show up:

- An untyped constant that fits in `int64` and not in `int32`, used as an `int`: `const n = 1 << 32`, `int(1 << 40)`, a slice length, a shift count stored in an `int`. `go/types` with `StdSizes{WordSize: 4, MaxAlign: 4}` rejects these with the same diagnostic as `GOARCH=386`: `constant 4294967296 overflows int`. That error is a compile failure, not a warning.
- An explicit conversion from `int64` or `uint64` to `int` or `uint`. `time.Duration` is `int64`, so `time.Hour` itself is fine, and `d / time.Millisecond` is an `int64`. `int(d / time.Millisecond)`, `int(time.Now().Unix())`, and `int(h)` where `h` is a 64-bit hash are not constant-checked. They wrap at run time, the same way they do on 386. The compiler emits a vet-style warning at each such conversion: `conversion from int64 to int may overflow; int is 32 bits`. The warning does not fail the build. `-vet-fail` promotes it to an error. Conversions the checker can bound (a constant source, or a value already masked to 32 bits) are silent.
- Code that uses `int` as a hash accumulator or a bit set wider than 32 bits. The warning above is the diagnosis. `uint64` and `hash/maphash` are unchanged and are the right types for that code.

`len`, `cap`, and indexing are not warned on. Their type is `int`, and the Wasm array they index is `i32`, so the widths already match.

## Language and library pin

The compiler is built with **Go 1.27.4** (released 2026-11-10) or a newer 1.27 point release. `go/types` of that toolchain is the language definition. Go 1.27 adds generic methods, struct-literal keys that are embedded selectors, and a wider function-type inference ([release notes](https://go.dev/doc/go1.27)). Generic methods are monomorphized at each instantiation and are not interface method-set entries: the release notes say an interface method cannot declare type parameters and cannot be implemented by a generic method.

The standard library is vendored from the same pin, `go1.27.4`, by the strategy in `06-stdlib-strategy.md`. The user's `go` directive is passed to `go/types` as the language version of that package, so a module that says `go 1.22` does not silently gain 1.27 syntax.

Go 1.27 removed the `asynctimerchan` debug flag. Channels from package `time` are unbuffered. The runtime implements that, not the older buffered timer channel.

## Language coverage

Accepted, and lowered as in `02-ir.md` and `03-type-mapping.md`:

- All pre-1.27 language features `go/types` accepts, plus the 1.27 features above.
- Generics, by monomorphization. No dictionaries.
- Goroutines, channels, `select`, `defer`, `panic`, `recover`.
- Methods, embedding, interfaces, type assertions, type switches, method values.
- Maps, including interface, struct, array, string, and floating-point keys, with NaN keys behaving as in the spec (a NaN is not equal to a NaN, so a lookup does not find one).
- `range` over arrays, slices, strings, maps, integers, and functions.

Rejected at compile time, with the diagnostic in `05-unsafe-and-reflect.md`:

- Converting between `uintptr` and `unsafe.Pointer` in either direction.
- `unsafe.Add` on a pointer that denotes a GC object.
- `import "C"`, and any `.s` file that a package actually needs.
- A reachable call to `reflect.StructOf`, `FuncOf`, `ArrayOf`, `MapOf`, `SliceOf`, `PointerTo`, or `ChanOf` whose result the compiler cannot prove is already a Wasm type in the program. The proof is the whole-program analysis in `05-unsafe-and-reflect.md`. A call the reachability pass deletes is not an error.

Known semantic differences, documented and tested:

- `int` is 32 bits.
- No preemption. `runtime.Gosched` yields; a loop that never parks does not.
- `main` returning exits the process. Other goroutines do not keep it alive. This matches Go. The host implements it by resolving the main export's promise as a microtask and scheduling new goroutines as macrotasks (`07-host-and-js.md`, `prototypes/jspi-sched`).
- `unsafe.Sizeof` reports a 32-bit Go layout. That layout is not the Wasm representation of the value. The compiler rejects the conversion that would turn the number back into an address.
- `sync/atomic` operations are sequentially consistent only because one Wasm activation runs at a time. They are plain loads and stores. There is no shared memory and no `memory.atomic`.
- `runtime.SetFinalizer` and `runtime.AddCleanup` are silent no-ops (decided 2026-11-22). A finalizer does not run. A value whose only release path is a finalizer leaks. `04-runtime-and-abi.md` names the standard-library sites.

## Success metrics

Checked in CI by the corpus in `08-testing.md`. Sizes are uncompressed Wasm, name section off, no `wasm-opt`.

| Program | Budget |
|---|---|
| empty `main` | ≤ 2 KiB. Stretch: the M2 1,775-byte snapshot. |
| `println("hello, world")` using the builtin, not `fmt` | ≤ 8 KiB |
| `fmt.Println("hello")` | ≤ 128 KiB, stretch ≤ 64 KiB |
| any of the above, relative to `GOARCH=wasm` empty main (~1.8 MB) | under 10% for `fmt`, under 1% for the builtin println |

Language metrics, in the order the bootstrap plan demands them:

1. The arithmetic, slice, string, map, interface, and defer programs in the study's run table compile and print the same values, without the trailing NUL `printuint` bug (`docs/wasm3-study/03-stdlib-and-unsafe.md`).
2. `go` test programs: a buffered channel round-trip, an unbuffered channel with the sender parked, a `select` with a default, a `select` that parks, two waiters on one channel, `recover` across a caller, and an unrecovered panic that prints the value and exits non-zero.
3. `fmt.Println` of an int, a string, a struct, and a map.
4. `encoding/json` marshal and unmarshal of a struct with a tag.
5. A growing, explicitly listed, subset of `$GOROOT/test` and of the standard-library tests for the packages we vendor. The skip list is a file, not a comment.

## CLI

The driver is `wasmgc-go`. It does not shell out to `cmd/compile`.

```
wasmgc-go build [-o prog.wasm] [-target node|browser] [-tags a,b]
                [-names] [-opt] [-size] [-vet-fail] [package]
wasmgc-go run   [-tags a,b] [package] [-- args]
wasmgc-go test  [-tags a,b] [-count n] [packages]
wasmgc-go dump-ir  [-o dir] [package]
wasmgc-go dump-wat [package]
wasmgc-go std check
wasmgc-go version
```

`build` writes a Wasm module. `-target node` (the default) also writes the JS runner next to it. `-target browser` writes glue that installs the same imports on `globalThis`. `-names` emits the Wasm name section; `build` leaves it off, `run` and `test` leave it on. `-opt` runs `wasm-opt` if it is on `PATH` and accepts the module; a `wasm-opt` that rejects Wasm GC is skipped with a message, not treated as success. `-size` prints section sizes. `-vet-fail` turns the 32-bit `int` conversion warning into an error.

`run` and `test` require Node.js 25 or newer, where `WebAssembly.Suspending` exists without a flag. Node 24 with `--experimental-wasm-jspi` is accepted if the constructor is present. Node 22 is rejected: on v22.23.4 the flag exposes `WebAssembly.Suspender` and does not expose `Suspending` (`07-host-and-js.md`).

`dump-ir` prints the text format from `02-ir.md`. That format is the golden-test contract between the lowerer and the code generator.

`std check` materializes the standard library for the pinned GOROOT and reports packages whose vendored sources use an unsupported `unsafe` operation. It is how a pin bump is reviewed (`06-stdlib-strategy.md`).

## Repository

```
cmd/wasmgc-go/          driver
internal/load/          go list, build-tag re-filter, overlays
internal/check/         unsupported-feature diagnostics
internal/ir/            IR, text format, verifier
internal/lower/         typed AST to IR
internal/mono/          monomorphization
internal/escape/        escape analysis
internal/codegen/       IR to Wasm
internal/wasm/          Wasm binary writer
std/                    rewritten packages, overlays, manifest
lib/wasmgc.js           host glue
prototypes/             engine spikes
docs/design/            this set
docs/wasm3-study/       the wasm3 study, verbatim
```

Nothing under `internal/` or `cmd/` exists yet. `09-bootstrap-plan.md` is the order in which it gets built, and the boundaries that let more than one person build it.
