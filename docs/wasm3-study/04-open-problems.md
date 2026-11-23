# wasm3 open problems, dead ends, and what to carry

HEAD `c8232bb660`. Quotes are from the tree. "Verified this study" means a program was compiled and run with the toolchain built here (Go 1.24.6 bootstrap, `make.bash`, Node v22.23.4). Everything else is from source or from Achille's notes in `doc/`.

## Dead ends

### Wasmfx goroutines

The toolchain emits `cont.new`, `resume`, `suspend`, a tag section, and `$go.cont`. Nothing in the Go scheduler resumes a continuation.

`src/runtime/proc_sched_wasm3.go:22–28` and `:64`:

```
// Engine viability note (see doc/wasm3-m4-status.md): the Go-side
// scaffolding here is complete, but V8 December 2025 and wasmtime 44
// do not yet implement the suspend / resume-with-handler runtime that
// these shims rely on.
throw("gogo_wasm3: wasm.Resume not yet implemented (Phase 4 needs resume-with-handler-vec)")
```

`src/cmd/internal/wasmgc/type.go:459–465` records that V8 would not resume a continuation whose body was `(func (param anyref) (result anyref))`, so the entry type was narrowed to a void `(func)`. `doc/wasm3-m4-status.md:91–96` quotes V8's `unimplemented code` fatal inside `GetContinuationResumeDescriptor`. wasmtime 44: `the wasm_stack_switching feature is not supported on this compiler configuration`. wasmfxtime ran a hand-written WAT and crashed on Go modules that mixed GC refs with continuations (`doc/wasm3-m4-status.md:154–166`).

JSPI replaced this for `GOOS=js`. The wasmfx code is still compiled into every module's type section unless the linker downgrades `$go.cont` (`0a737d78d7`). A new JS-hosted compiler should not emit continuation types at all.

### Stage J "flip the string ABI and fix the fallout"

`doc/wasm3-stage-j-progress.md` describes two cutover attempts (`f8e3685877`, `5fad563f7e`) that changed a string from exploded fields to a ref and were reverted the same day because every helper still emitted `i64` operations on the string. The boxed string ABI did land later, by changing the helpers and the SSA ops together rather than by flipping the signature first. The lesson in that doc is right: an interior-pointer primitive has to exist before the helpers move. The specific Stage J task list is obsolete; the boxed ABI is in `flatPrimitiveFields` now.

### Register allocation

`doc/wasm3-m3-no-regalloc.md` rejects a 16-register wasm ABI as duplicated work the engine's JIT already does. `regalloc` is a no-op (`src/cmd/compile/internal/ssa/regalloc.go:149–157`). Do not revive it. Also do not feel obliged to copy `wasm3place.go`: it exists because this compiler starts from Go's SSA, which assumes registers. A new backend can allocate a wasm local when it creates the value.

### `(i32)->(i32)` unwind ABI, PC_F/PC_B, linear Go stacks, the Go GC

Removed on purpose. `wasmabi.go:18–23`. `gc_stubs_wasm3.go:14–22`. There is no reason to bring them back if the host collects wasm objects and JSPI (or a future stack-switching proposal that actually ships) switches stacks.

### Selective boxing of every scalar (`$go.box.scalar`) as the only pointer

Design doc §7. Superseded by `$go.ptr.poly.*` for `*scalar`. The boxed-scalar collector remains (`wasmtype.go:662–677`) and `pointerStorage` does not use it (`wasmtype.go:301–311`).

### Single-slot spawn

`doc/wasm3-js-go-keyword-status.md:65–84` says a loop of `go` keeps only the last function. `src/runtime/wasm/spawn_jswasm3.go:12–29` is a FIFO and says the queue exists so that loop works. The doc was not updated. Trust the code.

### `asm3.go` header

Lines 16–24 say reference signatures panic and there is no table section. `writeTypeSec3` and `writeTableSec3` are called at lines 625 and 634, and this study linked programs with maps, slices, and channels. The header is the M2 status comment left in place.

## Unfinished codegen

`src/cmd/compile/internal/wasm3/ssa.go:1005–1024`:

```
// TODO(polyptr-step-2): emit the construction sequence
//   global.get $class.array.<T>
//   i32.const 1; array.new_default $go.array.<T>
//   i32.const 0  ;; offset
//   struct.new $go.ptr.array.<X>
v.Fatalf("OpWasm3NewScalarPtr: codegen not yet implemented (polyptr migration step 2)")
```

The same fatal for `OpWasm3PolyPtrLoad` (line 1020) and `OpWasm3PolyPtrStore` (line 1024). `OpWasm3PtrLoad`, `OpWasm3PtrStore`, and `OpWasm3MakeFieldPtr` are implemented (`ssa.go:1026+`, `2272–2319`). Escaping `&local` of a scalar, which needs a one-element array to point at, is the hole `NewScalarPtr` was supposed to fill. A program that takes the address of a local integer and lets it escape will hit this fatal or a different lowering that traps. This study did not compile that program.

`ssa.go:647`: `TODO: emit return_call_indirect once supported.` Tail calls are not used.

`internal/abi/type_wasm3.go:200`:

```
internal/abi: pointer-arithmetic reflect helpers are not yet supported on GOARCH=wasm3 (M6 work)
```

M6 (per-type reflect accessors from design doc §10) was not done. `reflect.StructOf` cannot be done at all: wasm struct types are static (`doc/wasm3-design.md` §10.3).

Interface values are still `struct.new $go.iface` (`ssa.go:2269–2270`) while `$go.iface.<I>` and vtables are also registered (`type.go:245–249`). Dispatch and representation diverged. See `01`.

`collectBacking` does not reserve a table slot before recursing, so `struct X { ks []X }` is "a future refinement" (`wasmtype.go:397–400`).

`wasm3FlatStride` (`wasmtype.go:637–659`) still describes `[]string` as two `i64`s. `collectBacking` boxes the string. One of these is dead. Callers in `ssa.go` should be checked before deleting the function; the type of the array is the collector's.

Closure context field 1 is still an `i64` "linear-memory captures-block pointer" (`wasmabi.go:1668–1673`) next to a newer per-closure struct. `CTXT` (global 1) and `CTXT_REF` (global 2) are both live.

Channel slots are `anyref` plus a `ref.cast` at each access, to make structural type dedup work (`wasmtype.go:618–630`). A compiler with one type table for the whole program can store `(ref null (array T))` directly and drop the cast and the dedup workaround.

## Runtime limits that are still true at HEAD

Defer list is one package global. `src/runtime/defer_wasm3.go:18–26`:

```
// PHASE 1 LIMITATION (single-goroutine only): the list head lives
// in a package-level global, not on g. That's because runtime.getg()
// has no wasm3-backend lowering
```

Panic does not unwind. `defer_wasm3.go:113–116` and `:134–145`:

```
// PHASE 2 SIMPLIFICATION: the wasm activation frames between the
// panic call site and the recovering caller are NOT unwound
exit(0)   // recovered
```

Unrecovered panic is `wasm3PanicTrap` recursing until the engine stack overflows (`defer_wasm3.go:153–172`), because `throw` and `abort` call back into `wasm3Gopanic`. The panic value is not printed. `try_table` was prototyped in `doc/wasm3-eh-defer-derisk.wat` and not emitted.

Channels store one `sendParkID` and one `recvParkID` (`wasmtype.go:576–582`). A second waiter is dropped. `select` blocks by `SleepMs(1)` (`chan_jswasm3.go`, `wasm3SelectYield`). `runtime/select.go` is not compiled.

`getg` is not implemented. `schedule` and `execute` throw (`proc_wasm3.go`, commit `3431c8f7fb`). There is no preemption, no `P`, no `sysmon`.

When `main` returns, the host `process.exit`s (`misc/wasm/wasm_exec_wasm3.js:221–229`). Goroutines queued with `setImmediate` do not run if `main` never parked.

`SetFinalizer` / `AddCleanup` are no-ops (`gc_stubs_wasm3.go`). Design doc §12.4 still says "decision pending". WasmGC has no finalizers. A JS host can use `FinalizationRegistry`. That was not written.

Atomics are plain stores. `src/internal/runtime/atomic/atomic_wasm3.go:5`:

```
// TODO(neelance): implement with actual atomic operations as soon as threads are available
```

`src/runtime/os_wasm3.go:41`: `FIXME: wasm doesn't have atomic yet` on `exitThread`.

`sync/atomic/value_wasm3.go:15–18`:

```
// wasm3 M2 is single-goroutine (real goroutines/atomics arrive with M4 ...)
// so these operate on the boxed interface ref directly and are not yet atomic.
```

M4 arrived as JSPI, which is still one thread. The comment's "not yet atomic" remains accurate for the memory model and inaccurate as a description of M4.

Maps: `MemHash` returns the seed (`maps_wasm3.go:76–79`). `runtime_fast32` / `runtime_fast64` "will trap if reached" (`runtime_faststr_wasm3.go:30–33`). Values in the faststr map are a linear blob (`runtime_faststr_wasm3.go:49–62`). The compiler `$go.map` path is separate and is what `map[int]int` used in the run below.

`convT*` dynamic interface boxing is stubbed (`iface_wasm3.go`). A conversion the compiler does not intrinsify can turn into a nil data word.

`printuint` includes one zero byte past the last digit (`printnum_wasm3.go:19–30`). Verified: `println` of `4` sent the bytes `4`, `0x00`.

`GOOS=wasip1` does not link. `netpoll_wasm3.go:413–418` calls `wasm.MintParkID`, `AwaitFdReadable`, `AwaitFdWritable`, which exist only in js-tagged files. Verified by `go build` of an empty main during this study.

Network, pipes, `exec`, time zones, cgo, and coverage metadata are stubs (`03`).

`internal/poll/fd_poll_jswasm3.go` still has the upstream `TODO(neelance)` about JS callbacks. `src/runtime/wasm/io_jswasm3.go` says the Go side of `AwaitFd*` is not wired.

## What this study actually ran

Toolchain: `src/make.bash` with `GOROOT_BOOTSTRAP` = Go 1.24.6, `CGO_ENABLED=0`, finished in about two and a half minutes (`Installed Go for linux/amd64 in /workspace`). `go version` reports `go1.27-devel_c8232bb660`.

Node v22.23.4 has `WebAssembly.Suspender` and does not have `WebAssembly.Suspending` or `WebAssembly.promising`, even with `--experimental-wasm-jspi`. `misc/wasm/wasm_exec_wasm3.js:45` does `new WebAssembly.Suspending(...)` and throws `TypeError: WebAssembly.Suspending is not a constructor`. Achille's notes used Node 25.9, where that constructor exists. The checked-in glue does not run on this Node.

A 20-line host that implements `gojs.runtime.writeBytes` and `gojs.runtime.Exit` and calls `exports.run` directly ran the programs whose import set is only those two functions. `WebAssembly.compile` plus `Module.imports` showed that empty, hello, fib, slice, map, string-map, and defer import nothing else. The channel program also imports `WasmPark`, `WasmReady`, and `SpawnGoroutine`.

`GOOS=js GOARCH=wasm3`, `-ldflags='-s -w'`, then run:

| Program | Bytes | gzip | Result |
|---|---:|---:|---|
| empty `main` | 3640 | 2005 | `run` returned |
| `println("hello, world")` | 3718 | 2054 | `hello, world` |
| `println(fib(20))` | 4308 | 2419 | `6765` plus a NUL, then newline. `fib(20)` is 6765 |
| slice literal, `append`, `println(len, last)` | 4702 | | `4` and `4`, each followed by a NUL |
| `map[int]int` insert and `delete`, `println(len, m[2], m[3])` | 7145 | 3450 | `2`, `20`, `30` |
| `map[string]int` | 6301 | | `1`, `2`, `2` |
| `defer println("bye"); println("hi")` | 3935 | | `hi` then `bye` |
| `go func(){ ch <- 7 }(); println(<-ch)` with a buffered `chan int` | 7174 | | did not finish. Without a suspending `WasmPark`, the receive does not return; the process hit the JS heap limit |

Same tree, `GOOS=wasip1 GOARCH=wasm` empty main: **1,824,076** bytes (gzip 555,175). Code section 1,146,704; data section 672,923. wasm3 empty main code section is **1,488** bytes; data section 922. Ratio of the two empty binaries: 1,824,076 / 3,640 ≈ **501×**. Hello is 1,824,287 vs 3,718.

Section sizes for the js/wasm3 empty main: type 532, import 47, function 22, table 5, memory 3, tag 3, global 198, export 29, element 28, code 1488, data 922, two custom sections 114 and 163. The tag section is present even though the program never suspends a continuation. The M2 audit's 1,775-byte empty main (code section 17 bytes, `doc/wasm3-m2-size-audit.md`) was wasip1 at commit `2ddf5c0f0b`, before JSPI, maps, defer, and the tag/table sections. HEAD is larger and still three orders of magnitude under `GOARCH=wasm`.

`GOARCH=wasm` empty code section is still ~1.15 MB. The size win is the deleted runtime, and it is still there after the June mirror wave, because those mirrors are mostly not linked into a program that does not call them. The 1,488 bytes of code in an empty main is the init/entry/`run` path, not `proc.go`.

## Performance, only what was written down

No benchmark suite was run on this branch. The notes that exist:

- Fat pointers allocate a `struct.new` per escaping `&`. `doc/wasm3-fat-pointers-design.md:290–295` says the cost is comparable to the old `$go.box.scalar` and was accepted.
- Nested structs are each their own object (`wasmtype.go:212–217`). The comment says spawn's allocation count grows with the depth of `g`. `g` itself is not really used on the JSPI path, so this cost shows up when the runtime mirrors allocate scheduler structs, not when user code allocates a flat struct.
- Composite slice elements are one object per element (`wasmtype.go:430–433`). `[]struct{X, Y int}` is an array of refs, not a packed array of ints. That is the expensive representation. `[]int` and `[]byte` are packed and are the ones to optimize for.
- `i32` header fields are extended to `i64` SSA locals on every `len` (`wasmtype.go:460–463`). Measurable in a tight loop, not measured here.
- `select` sleeps 1 ms per poll (`wasm3SelectYield`). That dominates any channel microbenchmark.
- `map` in the faststr runtime is O(n) per operation (`runtime_faststr_wasm3.go:6–10`). The compiler `$go.map` is also a flat array pair; neither is a swiss table.
- One wasm local per SSA value increases the local count. Engines handle that. The design note's claim (`doc/wasm3-m3-no-regalloc.md`) is that this is cheaper than pretending to have 16 registers. Not re-measured.
- JSPI stack size on this Node defaults to `--wasm-stack-switching-stack-size=984` (kB), from `node --v8-options`. Per-goroutine footprint was never measured on HEAD. `doc/wasm3-m4-status.md` describes the measurement (N parked goroutines, `process.memoryUsage`) and says it was blocked on the engine. The `memSnap` import exists in the glue for that measurement.

## What to carry into a standalone compiler

Carry the decisions, not the files.

1. **Host GC, no linear Go heap.** Allocate with `struct.new` / `array.new`. Do not link a garbage collector. Keep a small linear memory only for host calls, behind an explicit copy. This is the entire size result (501× on an empty main at HEAD, 1000× at the M2 snapshot).

2. **One wasm ref per composite Go value.** String = `{array i8, i32 off, i32 len}`. Slice = `{array, i32 off, i32 len, i32 cap}`. Nil is a null ref, and a non-nil header's array is non-null. Substrings share the array. `[]byte` uses packed `i8`. Composite elements are refs. This is `lowerFieldsImpl` after the flattening retreat (`wasmtype.go:180–233`).

3. **Do not flatten a struct or a string into the enclosing object's fields.** That lost to the "SSA local is anyref, struct.get returned i64" validator errors (`wasmtype.go:197–210`). Empty structs contribute no fields (`wasmtype.go:220–231`).

4. **Pointers to whole objects are those refs. Pointers to scalar slots are `{class, base, offset}` with a shared get/set funcref pair.** One class per (container type, field), not per pointer. Elide `&` when escape analysis can. Finish the one-element-array spill for escaping `&local` (`NewScalarPtr`), which this branch left as a fatal. Do not keep `$go.iptr`, `$go.ptr.i64`, and `$go.ptr.poly` together.

5. **Refuse `uintptr(unsafe.Pointer)` in the type checker**, with an error that names the rewrite (`unsafe.Slice` / a typed accessor). Doing it in SSA after lowering, as `ssagen/ssa.go:3537` does, works but the diagnostic is better at type-check time. Offer no compatibility shim that returns a fake integer; callers will add to it.

6. **Maps are two wasm arrays plus a length**, not a swiss table. Put `used` at field 0 if `len` is lowered as "load offset 0", or lower `len` explicitly and put the fields in whatever order is convenient. Hash for real before shipping interface keys. Delete the linear-memory value blob.

7. **Channels are a wasm struct: counters, a slots array, and a waiter queue.** The branch's state machine (write-before-park, `65a5736298`) is worth reading. The single park id is not. `select` should park on the channels involved, not `SleepMs(1)`.

8. **Goroutines on JS are JSPI.** `go f()` passes the func value, not `*funcval` (`spawn_jswasm3.go:11–26`). The host wraps the entry in `WebAssembly.promising` and blocking imports in `WebAssembly.Suspending`. Ready-before-park must be a stored flag (`wasm_exec_wasm3.js:57–70`). A FIFO of pending funcs is enough until something needs a run queue. Do not call `runtime.schedule`. Document that `main` returning exits the process. Pin the Node version: the API name changed between Node 22 (`Suspender`) and Node 25 (`Suspending` / `promising`).

9. **Wasm multi-value returns, precise types in the function signature where the producer allows, `ref.cast` before every `struct.get` on an `anyref`.** Signed LEB128 for heap-type immediates (`R_WASMHEAPTYPE`, `doc/wasm3-js-go-keyword-status.md:41–63`). Unsigned LEB128 silently becomes "unknown heap type -64" at index 64.

10. **One wasm local per value if the IR is SSA.** Structured control flow from a structured IR if you can avoid a relooper. The relooper in `cfg.go` with a `br_table` fallback is the known-good answer if the IR has gotos. Irreducible CFGs were deferred (`doc/wasm3-m3-relooper-design.md`).

11. **Intrinsics for `math` floor/ceil, float bit casts, `memmove`/`memequal` of byte arrays, `array.copy`.** Self-recursive Go bodies as a way to declare an intrinsic (`float_wasm3.go`) are a cmd/compile trick. A new compiler just emits the opcode.

12. **Type-descriptor identity is a wasm global**, `struct.new_default` of an empty object, compared with `ref.eq`. Do not emit `abi.Type` as bytes. If you need `reflect`, hang per-type getter/setter funcrefs off a real descriptor object (design doc §10) and generate them with the type. Do not promise `reflect.StructOf`.

## What to revisit rather than copy

1. **The parallel runtime.** `proc_wasm3.go` and the rest of the mirrors exist so a fork of `cmd/compile` can still compile `package runtime`. A new compiler should start from a few hundred lines: entry, print, slice/string helpers, chan, spawn, park. The June 2026 commit subjects ("mirror", "stub", "elide ptr-arith") are the cost of the other approach.

2. **`int` width.** `PtrSize` is 8, so `int` is `i64`, but lengths in every header are `i32` with an extend/wrap at each use (`wasmtype.go:457–463` versus `scalarPrim` lines 114–116). Pick one. `i32` lengths match wasm's `array.len` and the 2^31 element limit. `i64` `int` matches Go on 64-bit hosts. The branch accidentally has both.

3. **Interface representation.** Pick the vtab model (type identity is the vtab singleton, methods are funcrefs, nil is a null vtab) or the `{type, data}` pair. The branch has both, and construction uses the pair (`ssa.go:2269`) while phase D started the vtab (`type.go:213–249`). The vtab model is the one that makes method dispatch and type asserts the same instruction. The pair is simpler if interfaces are rare.

4. **Defer and panic.** Use wasm exception handling, which the WAT spike already validated, and store the defer list on the goroutine (a wasm global "current g", or a JS-side record per promising activation). `exit(0)` on `recover` (`defer_wasm3.go:134–145`) is not Go.

5. **Closure context.** One struct: funcref plus typed capture fields. No `i64` captures pointer, no `CTXT` global (`wasmabi.go:1668–1673`).

6. **`printuint` off-by-one** if that code is copied (`printnum_wasm3.go:21–30`). Slice `buf[i:printnumScratchSize-1]` or start `i` at `len(buf)`.

7. **Linker type merging.** `mergeTable` and the `anyref` chan-slot workaround (`wasmtype.go:618–630`) are artifacts of per-function type tables being concatenated. A single compiler that owns the whole module can intern types once.

8. **pclntab.** The M2 audit found most of the remaining bytes were file paths in the data section (`doc/wasm3-m2-size-audit.md:50–55`). At HEAD the empty main's data section is 922 bytes and the code section is the larger one (1,488). Still worth not emitting a full `runtime.moduledata` the target cannot parse (`getg` and traceback are stubs, `traceback_wasm3.go`).

9. **Build tags and `*_wasm3.go` clones.** M0 (`doc/wasm3-triage.md`) duplicated the wasm port so the tools would build. That duplication is the reason the experiment became a fork the Go team would not take. A new module does not register a `GOARCH`.

10. **Node API drift.** Generate the JS glue against one documented JSPI surface, and test it. This study could not run `misc/wasm/wasm_exec_wasm3.js` unmodified on Node 22.23.4.

## Docs that are useful, and docs that are stale

Useful as design rationale, checked against the code before trusting a shape:

- `doc/wasm3-design.md` — motivation, the three constraints (no interior refs, no uintptr, host stack growth). §6.3's flattened string-in-struct picture is not what shipped.
- `doc/wasm3-m2-size-audit.md` — the size argument, with the capability caveat.
- `doc/wasm3-m3-no-regalloc.md` and `doc/wasm3-m3-relooper-design.md` — why locals and a relooper.
- `doc/wasm3-slice-boxing.md` — the boxed string/slice/interface ABI that did ship.
- `doc/wasm3-fat-pointers-design.md` — the problem statement. The `{container, i32}` encoding was not the final pointer.
- `doc/wasm3-chan-subtyping.md` — read the implementation-deltas section, not the first diagram.
- `doc/wasm3-m4-status.md` and `doc/wasm3-js-status.md` — why JSPI, and the engine bugs.

Stale relative to HEAD:

- `doc/wasm3-js-go-keyword-status.md` single-slot spawn.
- `doc/wasm3-js-status.md` "scheduler isn't wired yet" and the wasmimport-collapses-to-one-i32 claim. Later commits wired `go` and multi-param numeric imports (`f4174a1232`). Returning a ref from an import is still called a gap in `jspi_jswasm3.go`.
- `doc/wasm3-stage-j-progress.md` "branch is back to the pre-Stage-J baseline".
- `doc/wasm3-triage.md` "wasm3 behaves identically to wasm". That was M0.
- `src/cmd/link/internal/wasm/asm3.go:16–24`.
- `loweredSignature`'s comment that a string is three slots (`wasmabi.go:69–75`).
- `collectChanStruct`'s comment block (`wasmtype.go:563–573`).
- `OpWasm3MakeFieldPtr`'s comment that says `$go.ptr.i64` (`ssa.go:2273`).

`src/ssa.html` and `/ssa.html` (commit `c8232bb660`) are SSA dumps, not a design document.
