# wasm3 branch — overview

Study of Achille Roussel's `wasm3` experiment on fork `github.com/achille-roussel/go`, HEAD `c8232bb660` (`add ssa.html`, 2026-08-11). This document records what the branch is, what it is based on, how big the change is, how to build it, and what actually runs. Later documents cover type mapping, ABI/runtime, stdlib, open problems, and a file index.

Nothing here was inferred from memory of upstream Go. Citations are paths and lines on this branch. Where a design doc disagrees with the code, the code wins and the doc is called out as stale. Binary sizes quoted from Achille's own audits are historical measurements, not re-runs, unless the "Measured on this study" section says otherwise.

## What the branch is

`GOARCH=wasm3` is a second WebAssembly architecture, registered next to `GOARCH=wasm`. It targets the WebAssembly GC proposal: Go heap objects are wasm struct and array references, and the host engine collects them. The Go garbage collector, allocator, write barriers, and goroutine stacks in linear memory are not linked.

The original design (`doc/wasm3-design.md`) also planned:

- the stack-switching proposal (`cont.new` / `resume` / `suspend`) for goroutines
- the exception-handling proposal (`try_table` / `throw`) for panic/recover

Those two did not become the path that runs. Goroutines on `GOOS=js` are JS Promise Integration (JSPI): each `go` starts a `WebAssembly.promising` activation, and blocking is a suspending import. Wasmfx continuations are encoded in the toolchain and then stubbed, because V8 and wasmtime did not execute `resume` with a handler (see `doc/wasm3-m4-status.md` and `src/runtime/proc_sched_wasm3.go`). Panic/recover is a runtime helper that runs a global defer list and then either `exit(0)` or overflows the wasm stack. Exception-handling opcodes exist; user code does not emit `try_table`.

`doc/wasm3-design.md` lines 64–70 state the compatibility goal explicitly: a TinyGo-style restricted subset. `unsafe.Pointer` arithmetic, `uintptr`-as-pointer, cgo, and runtime-minted types (`reflect.StructOf` and friends) are out of scope.

The fork's `origin/master` is **not** the base of this work. That branch is still Go 1.23-dev, commit `16ce8b3925` (2024-04-29, "net: fix lookupHost on Plan 9"), `internal/goversion.Version = 23`. The wasm3 experiment was developed on top of a much later upstream tree that this fork never merged back to `master`.

## Base Go version

| Fact | Value |
|---|---|
| First wasm3 commit | `0ae47fcbc6` (2026-05-14) `all: add GOARCH=wasm3 target as a wasm clone` |
| Parent (upstream base) | `7601c4bf422b92ceb77bfd99c83aa9d392906858` |
| Parent subject | `net/http/internal/http2: reject STREAM_ENDED + Content-Length request` |
| Parent author / date | Brad Fitzpatrick, 2026-05-13 13:05 -0700 |
| Gerrit | `https://go-review.googlesource.com/c/go/+/777180` (Change-Id `I01e3ec3bddc4cc6c43f8bcdcfa40b0dd7d9d7f55`, in the parent commit message) |
| `src/internal/goversion/goversion.go` | `const Version = 27` (Go 1.27 development) |
| Bootstrap required to build this tree | `go1.24.6` (`src/cmd/dist/buildtool.go:128`) |
| Commits after the base | **612**, all authored by Achille Roussel, no merge commits |
| HEAD | `c8232bb660` (2026-08-11). Last code commit is `e7762ce701` (2026-06-08); the August commit only adds `ssa.html` dumps |

`git merge-base wasm3 origin/master` is `16ce8b3925` only because this fork's `master` stopped in April 2024. Diffing wasm3 against that commit mixes two years of upstream Go into the delta and is the wrong base. The experiment's diff is `7601c4bf..HEAD`.

Word size copied from `GOARCH=wasm`: `ArchWasm3.PtrSize = 8`, `RegSize = 8`, little-endian, `Family = Wasm` so family-keyed checks still fire (`src/cmd/internal/sys/arch.go:273–288`). `MinFrameSize = 0` (`src/internal/goarch/goarch_wasm3.go:17`). Go `int` / `uintptr` are therefore 64-bit at the type-checker, even though many wasm struct fields that hold lengths were later narrowed to `i32` (see `01-type-mapping.md`).

## Scale

`git diff --shortstat 7601c4bf..HEAD`:

```
519 files changed, 80502 insertions(+), 1477 deletions(-)
```

225 files added, 294 modified. Roughly:

| Area | Files |
|---|---|
| `src/runtime` (including `runtime/wasm`) | 212 |
| `src/internal` | 87 |
| `src/cmd/compile` | 72 |
| `doc/wasm3-*` | 36 |
| `src/os` | 25 |
| `src/cmd/internal` (obj/wasm, wasmgc, sys, objabi) | 20 |
| `src/cmd/link` | 8 |
| JS glue (`lib/wasm`, `misc/wasm`) | 4 |
| other stdlib (`syscall`, `sync`, `math`, `crypto`, `net`, …) | the rest |

The compiler backend that matters is new, not a patch of `cmd/compile/internal/wasm`:

- `src/cmd/compile/internal/wasm3/` — type collection, ABI, SSA gen, CFG/relooper (`ssa.go` is 5444 lines)
- `src/cmd/internal/wasmgc/` — the GC type model shared with the linker
- `src/cmd/internal/obj/wasm/wasm3obj.go` (2077 lines) plus `wasm3check.go`
- `src/cmd/link/internal/wasm/asm3.go` (1371 lines)
- `src/cmd/compile/internal/ssa/wasm3place.go` — one wasm local per SSA value; `regalloc` returns immediately on wasm3 (`src/cmd/compile/internal/ssa/regalloc.go:149–157`)

The runtime change is mostly **mirrors**: `*_wasm3.go` copies of files whose upstream versions are build-tagged `!wasm3`, with the pointer-arithmetic and assembly removed. That pattern exists because this is a fork of `cmd/compile` that still has to compile the existing standard library. A standalone compiler should not copy it (see `04-open-problems.md`).

`doc/wasm3-triage.md` is the M0 inventory of every `GOARCH==wasm` check that had to be widened so wasm3 would even build as a clone. It is historical: later milestones diverged from wasm on purpose, and many of those widened sites were then split again into wasm3-specific files.

## How the experiment was sequenced

The commit log is the design history. The docs under `doc/wasm3-*.md` were written as status notes during the work and were not all updated when the code moved on. Read them as a timeline, then check the file they name.

| When | SHA | What landed |
|---|---|---|
| 2026-05-14 | `0ae47fcbc6` | `GOARCH=wasm3` registered as a wasm clone (M0). Toolchain builds, output still linear-memory wasm. |
| 2026-05-14–15 | `9d7bce37d9` … `afc48ca883` | Forked SSA backend, GC type section, typed function ABI, branches, then a real `println` Hello World (`afc48ca883`). |
| 2026-05-16 | `078b1ca922` | M2 size audit. Empty `main` measured at 1.7 KB. |
| 2026-05-16 | `086a8c24d1`, `c975780b36` | Regalloc bypass: one SSA value, one wasm local. The 16-register wasm model was rejected in `doc/wasm3-m3-no-regalloc.md`. |
| 2026-05-20 era | (see `doc/wasm3-stage-j-progress.md`) | Stage J (retire linear-memory shims behind fat pointers) was attempted twice and reverted. Fat pointers landed later in a narrower form. |
| 2026-05-30 | `0e73fe4570` | JSPI suspend/resume runs on Node. |
| 2026-05-30 | `098fc303ae` | `wasm.Spawn`: a second JSPI stack. |
| 2026-05-30 | `8804f9002a` | The `go` keyword calls `newprocJSWasm3` → `wasm.Spawn`. |
| 2026-05-30 | `6972d70bdc` | Spawn FIFO. The single global slot described in `doc/wasm3-js-go-keyword-status.md` is obsolete; `src/runtime/wasm/spawn_jswasm3.go` is a linked list. |
| 2026-05-31 | `65a5736298`, `165ff60736` | Unbuffered channels (write-before-park) and multi-case `select`. |
| 2026-05-31 | `23ff200e8b` | Polymorphic fat-pointer migration (`$go.ptr.poly.*`). |
| 2026-06-01 | `38164cb190`, `27253aee23` | Defer on normal return; `recover` ends in `exit(0)`. |
| 2026-06-01 | `594566ff4f`, `8756b8cdde` | Interface phase D: per-`(I,T)` vtab globals and `call_ref` dispatch. Construction of the interface value still `struct.new`s the legacy `$go.iface` (see `01-type-mapping.md`). |
| 2026-06-02 → 06-08 | `4a164c573d` onward | "Rung" wave: gate the real runtime (GC, swiss maps, panic, slice, netpoll) off wasm3 and add mirrors that compile without `uintptr(unsafe.Pointer)`. This is the bulk of the +80k lines. |
| 2026-06-03 | `0a737d78d7` | If a module never emits `cont.new`, the linker rewrites `$go.cont` to a plain `(func)` so Node can load it with JSPI only, without `--experimental-wasm-wasmfx`. |

## How to build and run

Host bootstrap must be **Go 1.24.6 or newer** (`src/cmd/dist/buildtool.go:128`). The Go 1.22 toolchain that ships on many images is rejected. This study used a downloaded `go1.24.6` as `GOROOT_BOOTSTRAP` (see the measurement section).

```sh
cd src
GOROOT_BOOTSTRAP=/path/to/go1.24.6 ./make.bash
```

Cross-compile:

```sh
cd /path/to/module
GOOS=js GOARCH=wasm3 /path/to/this/tree/bin/go build -o prog.wasm .
```

`GOOS=wasip1 GOARCH=wasm3` also builds. It has no JSPI scheduler: `newprocJSWasm3` is the js-only file, and `gogo_wasm3` throws (`src/runtime/proc_sched_wasm3.go:54–64`). wasip1 wasm3 is the single-activation target.

Run on Node (the glue's own header, `misc/wasm/wasm_exec_wasm3.js:16`):

```sh
node --experimental-wasm-stack-switching \
     misc/wasm/wasm_exec_wasm3.js prog.wasm
```

Node 22 on this machine also exposes `--experimental-wasm-jspi`. The glue calls `WebAssembly.promising` and `WebAssembly.Suspending`, which need the JSPI flag that Node documents as stack-switching. Programs that emit a real `(cont …)` type additionally need `--experimental-wasm-wasmfx` (and, per `doc/wasm3-m4-status.md:73`, `--experimental-wasm-custom-descriptors` and `--experimental-wasm-exnref` on the Node 25 Achille used). `writeTypeSec3` downgrades `$go.cont` when no body uses `cont.new` (`src/cmd/internal/wasmgc/type.go:475–480`), so a program that only uses JSPI `go` should not need wasmfx.

Launchers checked in:

- `lib/wasm/go_js_wasm3_exec` — Node + the JS glue
- `lib/wasm/go_wasip1_wasm3_exec` — wasmtime/wasmedge style, no JSPI
- `misc/wasm/wasm_exec_wasm3.js` — 233-line host (clocks, write, park/ready, spawn, exit)
- `lib/wasm/wasm_exec_wasm3.js` — 521-line host; adds an fd registry and `AwaitFd*` imports for the unfinished netpoll rung

`//go:wasmimport gojs …` is the host ABI. Early notes (`doc/wasm3-js-status.md:54–73`) say multi-param imports collapsed to `(param i32)`. Commit `f4174a1232` ("wasm3 gojs imports use the normal multi-param ABI") is later than that note. The current glue passes several numeric arguments (`runtime.writeBytes(fd, off, n)`, `runtime.WasmPark(parkID)`), so the multi-param numeric case works. Returning a reference from an import is still called out as a gap in `src/runtime/wasm/jspi_jswasm3.go:7–24`.

## What works, from Achille's own run notes

These are his measurements and canaries, not this study's. Dates matter: the runtime grew a lot after the May size audit.

### M2 size audit (`doc/wasm3-m2-size-audit.md`, toolchain tip `2ddf5c0f0b`, 2026-05-16)

`GOOS=wasip1`, `-ldflags='-s -w'`.

| Program | `GOARCH=wasm` | `GOARCH=wasm3` | Ratio |
|---|---:|---:|---:|
| empty `main` | 1,829,590 B | 1,775 B | 1031× |
| empty `main`, gzip | 553,211 B | 951 B | 581× |
| fib(20) + print | 1,830,517 B | 6,562 B | 279× |
| bubble-sort + gcd + fib(25) + print | 1,832,064 B | 7,827 B | 234× |

Empty wasm3 module: code section **17 bytes**, data section 1,344 bytes (pclntab paths). The audit's own caveat (lines 76–83): no interfaces, maps, closures, channels, panic, or escaped heap allocation in those programs. The 1.7 KB figure is the lower bound when almost no runtime is linked, not the size of HEAD.

### Later, still small, more language (`doc/wasm3-m4-status.md:70–82`)

Node 25.9, non-suspending programs:

| Program | Bytes | Result |
|---|---:|---|
| empty `main` | 2647 | ok |
| `println("hello, world")` | 9411 | prints |
| `map[int]int` delete/range/clear | 14112 | prints entries |
| `map[string]int` | 14820 | prints |
| `wasm.RunInCont(body)` that does not suspend | 9766 | prints |

`wasm.Suspend()` inside a continuation fataled in V8 (`unimplemented code` in `GetContinuationResumeDescriptor`). wasmtime 44 refused the module. A hand-written WAT suspend/resume ran on wasmfxtime; Go-produced modules that mix GC refs and continuations hit wasmfxtime bugs (`doc/wasm3-m4-status.md:154–166`).

### JSPI canaries (late May 2026, documented as run)

- `go func(){ logInt(42) }()` prints `0 / 1 / 42` with `log 1` before `log 42`, i.e. the goroutine ran on a later task (`doc/wasm3-js-go-keyword-status.md:88–114`). Captured locals worked for that test.
- `make(chan int32, 1)` plus a spawned send, then receive, returns 7 (`doc/wasm3-js-chan-status.md:44–52`).
- Generic `wasm.Chan[int32]` also ran (`doc/wasm3-js-chan-status.md:7–22`), after `new(T)` with a dynamic type was lowered to `struct.new_default` instead of `runtime.newobject`.
- `SleepMs(150)` and `WasmPark` / `WasmReady` via `setTimeout` (`doc/wasm3-js-status.md:16–19`).

### What the code at HEAD says is wired, even where this study has not re-run it

Treating comments and the lowering as the source, not a test log:

| Feature | HEAD state |
|---|---|
| Scalars, arithmetic, floats, `print`/`println` | Compiles to wasm ops. Print of strings goes through a linear-memory scratch buffer and `runtime.writeBytes` (the JS glue, lines 147–161). |
| Strings, slices, `append`/`make` of slices | WasmGC structs and arrays. `runtime.growslice` on wasm3 throws if the intrinsic misses (`src/runtime/slice_wasm3.go`, per the runtime survey). |
| Structs, nested structs, arrays | One wasm ref per composite value. See `01`. |
| Maps as `$go.map.K.V` | Compiler type and ops exist. A second, older `map[string]V` runtime (`runtime_faststr_wasm3.go`) still describes a linear-memory value array. Both are in the tree. |
| `go` on `GOOS=js` | `ssagen` calls `runtime.newprocJSWasm3` (`src/runtime/spawn_jswasm3.go:11–26`), which calls `wasm.Spawn`. FIFO of closures. Host schedules `_run_wasm3_js` with `setImmediate`. |
| `chan` send/recv | Compiler trampolines in `reflectdata/wasm3_changen.go` plus `runtime/chan_jswasm3.go`. At most one parked sender and one parked receiver per channel. |
| `select` | Compiler rewrite (`src/cmd/compile/internal/walk/select.go`). Blocking multi-case calls `wasm3SelectYield` → `SleepMs(1)` (`src/runtime/chan_jswasm3.go`). Not `runtime.selectgo`. |
| `defer` on the way out of a function that returns normally | `wasm3DeferPush` / `wasm3DeferRunAll`. The list head is a package global (`src/runtime/defer_wasm3.go:18–26`). Safe for one goroutine. |
| `panic` / `recover` | Defers run in place. A recovered panic calls `exit(0)`. An unrecovered panic recurses until the engine stack overflows (`src/runtime/defer_wasm3.go:107–172`). Frames are not unwound. |
| Allocation | `struct.new` / `array.new` intrinsics. `runtime.mallocgc` throws if a call reaches it (`src/runtime/mallocgc_wasm3.go:11–28`). |
| `GOOS=wasip1` goroutines | Do not run. Continuation resume is unimplemented. |
| `reflect`, `cgo`, finalizers, real atomics, `net` | Stubs or compile errors. See `03` and `04`. |

When `main` returns, the JS host calls `process.exit(0)` (`misc/wasm/wasm_exec_wasm3.js:221–229`). Goroutines queued with `setImmediate` are dropped if `main` never parked. That matches Go's rule that other goroutines do not keep the process alive, and it is stricter about scheduling: a `go` does not run unless the caller yields to the JS event loop.

## Measured on this study

The image's `go` is 1.22.2. `src/cmd/dist/buildtool.go:128` requires `go1.24.6` or newer. A stock `go1.24.6` linux/amd64 toolchain was the bootstrap. `src/make.bash` with `GOMAXPROCS=2` and `CGO_ENABLED=0` finished successfully (`Installed Go for linux/amd64 in /workspace`). `go version` reports `go1.27-devel_c8232bb660`.

`GOOS=js GOARCH=wasm3 go build -ldflags='-s -w'` of small programs:

| Program | wasm3 bytes | gzip | `GOARCH=wasm` bytes |
|---|---:|---:|---:|
| empty `main` | 3640 | 2005 | 1,824,076 (gzip 555,175) |
| `println("hello, world")` | 3718 | 2054 | 1,824,287 |
| `println(fib(20))` | 4308 | 2419 | |
| slice literal + `append` + `println` | 4702 | | |
| `map[int]int` with `delete` | 7145 | 3450 | |
| `map[string]int` | 6301 | | |
| `defer` + two `println`s | 3935 | | |
| buffered `chan int` + `go` | 7174 | | |

Empty-main ratio against `GOARCH=wasm` on this same toolchain: 1,824,076 / 3,640 ≈ **501×**. wasm3 empty-main sections: code **1488**, data **922**, type 532, global 198, plus a 3-byte tag section even though the program never uses a continuation. The M2 audit's 1,775-byte / 17-byte-code empty main was wasip1 at `2ddf5c0f0b`, before this runtime existed. HEAD is larger and still about 500× under wasm. The June mirror wave did not pull the Go GC back into a hello-world binary.

`GOOS=wasip1 GOARCH=wasm3` does not link. `src/runtime/netpoll_wasm3.go:413` calls `wasm.MintParkID`, `AwaitFdReadable`, and `AwaitFdWritable`, which are declared only in js-tagged files.

Node v22.23.4 cannot run `misc/wasm/wasm_exec_wasm3.js`. With `--experimental-wasm-jspi` the process throws `WebAssembly.Suspending is not a constructor`. This Node has `WebAssembly.Suspender` and does not have `Suspending` or `promising`. Achille's notes used Node 25.9.

A small host that only implements `gojs.runtime.writeBytes` and `gojs.runtime.Exit`, and calls `exports.run` directly, ran the programs that import nothing else:

| Program | Observed output |
|---|---|
| empty | `run` returned |
| hello | `hello, world` |
| fib(20) | `6765` followed by a NUL, then a newline. 6765 is the right value |
| slice | `4` and `4`, each followed by a NUL |
| `map[int]int` | `2`, `20`, `30` after deleting key 1 |
| `map[string]int` | `1`, `2`, `2` |
| defer | `hi` then `bye` |

The NUL is `printuint` slicing one byte past the last digit (`src/runtime/printnum_wasm3.go:21–30`). The channel program also imports `WasmPark`, `WasmReady`, and `SpawnGoroutine`. Calling `run` with plain JS functions for those imports does not suspend, the receive does not return, and the process hit the JS heap limit. JSPI itself was not re-executed on this Node.

## Why the fork is hard to upstream

`doc/wasm3-triage.md` shows the M0 approach: duplicate every `*_wasm.go` as `*_wasm3.go` and widen every arch check. By June the runtime package is a parallel copy. `proc.go`, `iface.go`, `panic.go`, `slice.go`, the swiss-table map implementation, and the GC files are build-tagged off, and a sibling file reimplements or stubs them. That is a second runtime, maintained as text beside the first. The compiler side is the same shape: `Wasm3.rules` / `Wasm3Ops.go` / `wasm3/ssa.go` sit beside the wasm backend instead of replacing a shared IR lowering. Both are the "parallel code path" the reboot is trying to leave behind.

What is worth keeping is the object model and the JSPI scheduling sketch, not the file layout. Those are the subject of the next four documents.
