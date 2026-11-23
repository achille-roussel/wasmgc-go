# wasm3 calling convention, runtime, and JS host

HEAD `c8232bb660`. This is the ABI and the runtime that sit on the type mapping in `01-type-mapping.md`.

## Calling convention

`wasmabi.go:18–23`:

> a wasm3 function is an ordinary typed wasm function: Go parameters and results map directly to wasm function parameters and results, with no Go-stack-in-linear-memory frame, no PC_B block-id parameter, and no unwind-flag result.

`GOARCH=wasm` compiles every function to `(i32) -> (i32)`: the argument is a block id to resume at, the result is an "unwind now" flag, and every call spills the Go stack because a goroutine switch unwinds the wasm stack. wasm3 deletes that. Direct calls are wasm `call`. Indirect calls are `call_ref` on a funcref stored in the closure struct.

`attachWasmType` runs after SSA and writes `obj.WasmType` on the symbol for the linker. Three attempts, in order (`wasmabi.go` around 166–212):

1. `tryPrimitiveAttach` / `wasm3Fields` — scalars, and a string as one `anyref`.
2. `tryCollectorAttach` — build a per-function `wasmgc.Table` and use reference-typed fields from the object model. `topLevelFields` (`wasmabi.go:93–100`) passes a struct as one `$go.struct.T` ref.
3. `tryFlatPrimitiveAttach` / `flatPrimitiveFields` — explode to leaf wasm slots.

`//go:wasmimport` stubs skip attachment; the linker builds the import. A `//go:wasmexport` wrapper still attaches a typed signature for calls from inside the module (`wasmabi.go:150–164`).

### What crosses a call

From `flatPrimitiveFields` (`wasmabi.go:245–356`), which is what call sites were aligned to:

| Go parameter or result | Wasm slots |
|---|---|
| `bool`, integers ≤ 32 bits | one `i32` |
| `int`, `int64`, `uint`, `uintptr` | one `i64` |
| `float32` / `float64` | one `f32` / `f64` |
| `complex64` / `complex128` | two floats |
| `string`, slice, interface, `func`, `map`, `chan`, `*T`, `unsafe.Pointer` | one `anyref` |
| struct | recursive leaf slots, or the collector's one-ref form if the flat path fails |
| `[N]integer`, `1 ≤ N ≤ 8` | N integer slots |
| `[0]T` | nothing |

Multiple results are wasm multi-value. `BlockRet` pushes each `OpMakeResult` argument, then `return` (`src/cmd/compile/internal/wasm3/ssa.go` around 278–287). After a call, results are popped into per-value locals (`ssa.go` around 651–676).

The wasm type at the boundary is often `anyref` even when the compiler knows `$go.string` or `$go.slice.T`. The body `ref.cast`s before `struct.get`. That cast is not optional: "anyref isn't a subtype of `(ref null $go.object)`", and the validator rejects the `struct.set` without it (`ssa.go:984–990`). A new compiler can use the precise ref type in the function type and delete a lot of casts. The branch used `anyref` because each function's type table has its own indices and the register-ABI local is untyped from wasm's point of view (`wasmabi.go:335–342`).

`callClobbers` is an empty register mask (`src/cmd/compile/internal/ssa/_gen/Wasm3Ops.go` around 103–110). A wasm `call` does not clobber the caller's locals. Nothing is spilled across a call for register reasons.

### Locals instead of registers

`regalloc` returns immediately when `arch == "wasm3"` (`src/cmd/compile/internal/ssa/regalloc.go:149–157`). Before that, `wasm3PlaceValues` (`src/cmd/compile/internal/ssa/wasm3place.go`) assigns every SSA value its own wasm local: parameters occupy locals `0 .. n-1`, other values get `len(params) + Wasm3ValueLocals[id]`.

Phis get their own locals too. On each CFG edge, `emitPhiCopies` (`ssa.go` around 3897–3908) emits `local.get src; local.set Lphi`, with temps to break cycles. That replaces regalloc's "everyone shares the destination register" trick.

A single-use value in the same block can stay on the wasm operand stack (`OnWasmStack` in `wasm3place.go`) and skip the `local.set`/`local.get` pair. `doc/wasm3-m3-no-regalloc.md` is the design note: the 16 general-purpose registers Go's SSA wants are "busy work duplicating the engine JIT". The recommendation there is the model to copy: one SSA value, one wasm local, typed as the value. Do not port Go's regalloc.

Pipeline (`src/cmd/compile/internal/ssa/compile.go` around 503–519): lower → wasm3 final lower → schedule → flagalloc → wasm3 place values → regalloc (no-op) → wasm3 patch values → genssa.

### Control flow

Go's SSA has arbitrary gotos. Wasm has structured `block` / `loop` / `if`. Two lowerings:

- Legacy, in the obj encoder: if the CFG is forward-only, nested `block` plus `br` by depth. If there is any back edge, one big `loop { br_table … }` dispatch. Described in `doc/wasm3-m3-relooper-design.md`.
- Relooper, `src/cmd/compile/internal/wasm3/cfg.go`: `planForFunc` (around 760–774) computes an emit plan of where to open and close `block`/`loop` and what depth each branch uses. `computeEmitPlan` returns nil on irreducible CFGs and non-contiguous loop bodies, and the encoder falls back to dispatch (`cfg.go` around 439–448). Irreducible regions were counted and then deferred; the relooper doc mentions on the order of 175 "missing-branch-depth" bails.

`doc/wasm3-m3-relooper-design.md` argues for a relooper instead of threading wasm structure through every generic SSA pass, because SSA "is designed to discard syntactic structure". A new compiler that never builds a goto-CFG can emit wasm structure directly from the AST or from a structured IR and skip this. If it does use SSA, the relooper-with-dispatch-fallback is the approach that was shown to validate.

### Stack

There is no Go stack in linear memory, no `morestack` prologue, and no stack map. Wasm locals and the wasm call stack are the Go stack. `runtime.stackalloc` throws (`src/runtime/stack_wasm3.go`, the throw string says pre-M4 should not need linear-memory stacks). The engine owns stack growth. `debug.SetMaxStack` has nothing to set. That was an explicit design choice (`doc/wasm3-design.md` §8.2).

`getg` is not lowered. `OpGetG` has no handler in `wasm3/ssa.go`, so a read of `g` is an uninitialized local (`src/runtime/defer_wasm3.go:18–26`). Anything in the standard scheduler that starts with `gp := getg()` is either build-tagged out or rewritten to avoid the deref. There is no current-goroutine pointer in a wasm global at HEAD, despite the defer file saying phase 3 would add one.

### Allocation

`new`, composite literals, `make` of slice/map/chan, and slice growth are SSA ops, not calls to `mallocgc`:

| Kind | Op |
|---|---|
| zero struct | `OpWasm3StructNewDefault` → `struct.new_default` |
| array / slice backing | `OpWasm3ArrayNewDefault` → `array.new_default` |
| boxed composite cell | `OpWasm3BoxNewDefault` |
| slice header | `OpWasm3MakeSlice` |
| map header | `OpWasm3MakeMap` |
| chan | `OpWasm3MakeChan` |
| `&field` | `OpWasm3MakeFieldPtr` |

`runtime.mallocgc` (`src/runtime/mallocgc_wasm3.go:11–28`) throws `"allocation site failed to intrinsify"` if a call reaches it. The symbol stays because other files linkname it. `runtime.newobject` is the same idea. A missed intrinsic is a hard failure, which is what made the bugs visible.

Write barriers are gone. `mbarrier_wasm3.go` drops bulk-barrier calls. `struct.set` is the store. The host traces wasm refs.

`typedmemmove` of a ref-containing value is field-wise `struct.get`/`struct.set` or `array.copy`, not `memmove` of bytes. `runtime.memmove` on wasm3 is Go code over `unsafe.Slice` (`src/runtime/memmove_wasm3.go`). It cannot compare pointer order with `uintptr(unsafe.Pointer)` to detect overlap; the comment at lines 26–32 says that comparison is exactly the pattern SSA-gen rejects.

Package-level variables of reference type are wasm globals (`OpWasm3GlobalGet` / `GlobalSet`), not linear addresses. Descriptor symbols use `R_WASMDESCRIPTOR`. Heap-type immediates (`ref.cast`, `ref.null`, `ref.test`, block results of `(ref null T)`) use `R_WASMHEAPTYPE`, which the linker writes as signed LEB128. Unsigned LEB128 is wrong once the type index is ≥ 64: the validator reads `0x40` as −64 and reports `Unknown heap type -64` (`doc/wasm3-js-go-keyword-status.md:41–63`). Copy that relocation split. It is a one-time bug that looks like a compiler crash in V8.

## Linker output

`src/cmd/link/internal/wasm/asm3.go` `asmb2_3` writes a wasm module. The file's header (lines 16–24) still says reference-typed signatures panic and that there is no table section. That header is the M2 status and is stale: `writeTypeSec3` (line 625), `writeTableSec3` (line 634), and programs with maps and interfaces were linked months later. Trust the functions, not the header.

Sections actually written:

| Section | Role |
|---|---|
| Type | One merged `wasmgc.Table`: prelude, GC structs/arrays, every function signature |
| Import | `//go:wasmimport` (the `gojs` module, or WASI) |
| Function | Type index per defined function |
| Table | Funcref table for the `call_indirect` paths that still exist (interface trampolines) |
| Memory | Linear memory. Bump pointer is wasm global 0. Not the Go heap |
| Tag | One tag, `Wasm3TagIndexPark`, type `(func)`, for the wasmfx `suspend` that JS does not use |
| Global | Fixed globals 0..2 (bump, `CTXT` i64, `CTXT_REF` anyref) plus descriptor globals, boxed package vars, dict globals |
| Export | `run` (`runtime._rt0_wasm3_js`) or `_start` (`runtime._rt0_wasm3_wasip1`), `memory`, `//go:wasmexport` |
| Element | Table initializers |
| Code | Bodies from `encodeWasm3Body` |
| Data | Whatever linear data is left (pclntab paths, the bridge arena's initial image) |

Entry on js is `_rt0_wasm3_js` in `src/runtime/rt0_jswasm3.go`: it calls `wasm3RunInit` (the linker patches the body; the Go source does not walk `moduledata` function pointers) and then `main.main`.

## Goroutines: JSPI, not the Go scheduler

Two designs are in the tree. Only one runs.

### The one that runs (`GOOS=js`)

`go fn()` does not call `runtime.newproc`. `cmd/compile/internal/ssagen/ssa.go` routes `OGO` to `runtime.newprocJSWasm3(fn func())` (`src/runtime/spawn_jswasm3.go:11–26`). The argument is a Go func value, the closure ref, not a `*funcval`. The standard runtime does `*(*func())(unsafe.Pointer(&fn))`, which wasm3 cannot lower because a typed ref local is not addressable. Passing `func()` avoids that cast. Copy this.

`newprocJSWasm3` calls `runtime/wasm.Spawn` (`src/runtime/wasm/spawn_jswasm3.go`):

```wat
;; conceptual, not a single function
;; Spawn: append fn to a FIFO linked list, then
call $gojs.runtime.SpawnGoroutine (i32 0)     ;; returns immediately
;; later, on a new JSPI stack:
call $_run_wasm3_js                            ;; dequeue one fn and call it
```

The FIFO (`spawnHead` / `spawnTail`, lines 40–77) replaced a single global slot. `doc/wasm3-js-go-keyword-status.md:65–84` still describes the slot ("a tight loop prints only the last value"). The code is the list. The doc is stale. `wasmSpawn` ignores its `id` argument; each host callback dequeues one node (lines 79–106).

Host side, `misc/wasm/wasm_exec_wasm3.js:166–197`: `SpawnGoroutine` does `setImmediate(() => promising(_run_wasm3_js)(id))`. `setImmediate` rather than `queueMicrotask` is deliberate (lines 176–189). `main()`'s returned promise resolves, and `process.exit(0)` is queued as a microtask (lines 221–229). Microtasks run before `setImmediate`. If `main` returns without parking, spawned goroutines never run. If `main` is parked on a Promise, the event loop runs the spawn while `main` is suspended. That is the whole scheduler.

Blocking:

```go
//go:wasmimport gojs runtime.WasmPark
func WasmPark(parkID int32)
//go:wasmimport gojs runtime.WasmReady
func WasmReady(parkID int32)
//go:wasmimport gojs runtime.SleepMs
func SleepMs(ms int32)
```

(`src/runtime/wasm/jspi_jswasm3.go`). The JS side wraps park and sleep in `WebAssembly.Suspending`. `WasmPark` (`wasm_exec_wasm3.js:63–76`) stores a resolver in a `Map` keyed by `parkID`, or returns immediately if `WasmReady` already recorded `"ready"`. Ready-before-park is required; the Go scheduler allows that race and the comment says so (lines 57–61).

`runtime.gopark` on js/wasm3 calls `wasm3Park` and returns (`src/runtime/proc_wasm3.go` around the `goos.IsJs && goarch.IsWasm3` branch). `wasm3Park` (`src/runtime/sched_jswasm3.go`) unlocks, then `WasmPark(parkID)`. `goready` from the normal scheduler throws `"unexpected caller on js/wasm3"`; channel wake uses `wasm3Goready(sg)`, which reads `sg.wasm3ParkID` and calls `WasmReady`. `schedule` and `execute` throw `"JSPI drives execution"` (`proc_wasm3.go`, the throws added in `3431c8f7fb`).

There is no run queue, no `P`, no `sysmon`, no preemption. A goroutine runs until it calls a suspending import. `Gosched` is not a real yield unless it was wired to `SleepMs(0)` or similar; the standard body is not the one that runs.

Wasm is single-threaded under JSPI in the sense the runtime comments rely on: "a suspender either holds the engine or is suspended on a Promise — never two at once" (`spawn_jswasm3.go:43–46`). The FIFO is not locked. Atomics are plain loads and stores (`src/internal/runtime/atomic/atomic_wasm3.go:5–6`). That assumption is true for one wasm instance on one JS thread. It is false if the host ever enables wasm threads.

### The one that does not run (wasmfx)

`src/runtime/wasm/sched_wasm3.go` declares `RunInCont`, `Suspend`, `RunInContCatchSuspend`. The Go bodies panic; SSA is supposed to replace them. `RunInCont` lowers to `ref.func; cont.new; resume` with an empty handler vector. `Suspend` is `suspend $tag`.

`gogo_wasm3` (`src/runtime/proc_sched_wasm3.go:54–64`) throws `"wasm.Resume not yet implemented (Phase 4 needs resume-with-handler-vec)"`. The file header (lines 22–28) says V8 fatals `unimplemented code` and wasmtime 44 has no stack switching. `doc/wasm3-m4-status.md` records a hand-written WAT that did suspend and resume on wasmfxtime, and Go modules that mixed GC refs with continuations crashing that engine.

Opcodes, the tag section, and the `(cont …)` type are in the toolchain so the experiment could be turned back on. A JS-hosted compiler does not need them. A WASI-hosted compiler has no substitute today: JSPI is a JS host feature. `doc/wasm3-js-status.md` says this directly.

## Channels and select

`runtime/chan.go` is not the implementation (`//go:build !wasm3` on the standard file). `src/runtime/chan_jswasm3.go` is.

`runtime.channel` matches `$go.chan` field for field (`chan_jswasm3.go:23–34`):

```go
type channel struct {
    qcount, dataqsiz, sendx, recvx int32
    closed bool          // i32 at the wasm level
    sendParkID, recvParkID int32
}
```

The compiler generates a per-element-type trampoline (`src/cmd/compile/internal/reflectdata/wasm3_changen.go`). The trampoline `ref.cast`s `(ref $go.chan.<T>)` down to `*channel` to run the state machine in ordinary Go, and `ref.cast`s back to `$go.chan.<T>` to `array.get`/`array.set` slot `i`. Slots access helpers (`wasm3ChanSlotsGet/Set/Alloc`, lines 51–58) are intrinsics; the Go bodies return nil and must not run.

Unbuffered send (`chan_jswasm3.go:64–78`): the sender writes slot 0 **before** publishing `sendParkID`. A receiver that observes `sendParkID != 0` may read slot 0 with no further synchronization. An earlier order (publish the park id, then write) let `select`'s non-blocking probe report ready while the slot was empty. Commit `65a5736298`.

Buffered send uses `sendx` / `recvx` / `qcount` like `hchan`, with the buffer in the slots array. Capacity is fixed at `make`. One blocked sender, one blocked receiver, stored as park ids, not as a `sudog` list (`wasmtype.go:576–582`). A second blocked sender is a lost wakeup. `select` with several waiters on one channel is outside this design.

`close` is a generic helper on `*channel` (`90f3355f53`).

`select` is a compiler walk rewrite (`src/cmd/compile/internal/walk/select.go`, the `buildcfg.GOARCH == "wasm3"` branch). `runtime/select.go` is tagged off, with a comment that select is intrinsified through `wasm3SelectStart` / `wasm3SelectYield`. Multi-case select polls in a rotated order. The blocking loop calls `wasm3SelectYield`, which is `wasm.SleepMs(1)` (`chan_jswasm3.go` around 205–214). That is a 1 ms spin, not a park on the set of channels. It works. It is not the Go scheduler's select. `reflect`'s `rselect` still wants `selectgo` and is not on this path.

`hchan` in `chan_wasm3.go` remains only so timer and reflect code have a type to mention. `timerchandrain` returns false.

## Defer, panic, recover

`src/runtime/defer_wasm3.go`. Commit `38164cb190` (phase 1A), `a041209da5` (phase 2), `27253aee23` (recover returns via `exit(0)`).

Normal defer:

- Each `defer fn()` is a call to `wasm3DeferPush(fn)`.
- A function that contains a defer calls `wasm3DeferRunAll()` in its exit block, instead of `deferreturn`.
- The stack is a linked list of `{fn func(); prev *wasm3DeferRec}` in a package-level global `wasm3DeferHead` (lines 44–61).
- A linked list rather than `[]func()` because indexing `[]func()` was not in `wasm3SliceElemInteriorOK` and fell through to linear-memory addressing (lines 36–43).

The global means two goroutines' defers share one list (lines 18–26). The file says this is phase 1, single-goroutine only, because `getg` does not work.

Panic (`wasm3Gopanic`, lines 107–151):

1. Store the panic value in `wasm3PanicValue`.
2. Set `wasm3InRecoverableDefer` and run **every** deferred function on the global list, not the defers of the panicking frame.
3. `recover` (`wasm3Gorecover`, lines 181–187) returns the value only when that flag is set, and sets `wasm3PanicSwallowed`.
4. If swallowed, call `exit(0)`. The comment explains why: `wasm3Gopanic` is marked noreturn, so the caller's next block is `BlockExit` / unreachable. Without `try_table`, the function cannot return to the caller and continue. Exiting is the approximation that still lets the defer's prints happen.
5. If not swallowed, `wasm3PanicTrap` recurses until the engine reports a stack overflow (lines 153–172). `throw` and `abort` are not usable: they route back through `sigpanic` → `wasm3Gopanic` and overflow in a less obvious way. The panic value is not printed.

`try_table` / `throw_ref` was de-risked with a hand-written WAT (`doc/wasm3-eh-defer-derisk.wat`, commit `9b0292d66d`) and was not wired into genssa. `OpWasm3ResumeThrow` exists as an SSA name (`Wasm3Ops.go` around 853). User code does not throw wasm exceptions.

A new compiler that wants Go's panic semantics should emit exception handling, which the WAT spike showed the engine accepts, or it should accept "panic terminates the activation" and document `recover` as best-effort inside the same function. The global list plus `exit(0)` is a bring-up hack. The reason it exists (no `getg`, noreturn marking, no try/delegate) is worth remembering; the mechanism is not.

`runningPanicDefers` is not updated. `panic_wasm3.go` prints some runtime panics and `exit(2)`s. Index panics still call `panic(...)`.

## Maps at run time

If the compiler emitted `OpWasm3Map*`, the operation is `array.get` / `array.set` on `$go.map`'s two arrays (see `01`). Growth is `array.new_default` of a bigger array plus a copy.

If a map operation is a call to `runtime.mapaccess` / `mapassign`, the swiss table is not compiled. `maps_wasm3.go` panics from `Iter` methods. `runtime_faststr_wasm3.go` handles `map[string]V` by scanning a `[]string` of keys (WasmGC, intrinsified `make`) and a linear-memory value blob (`values unsafe.Pointer`, lines 49–62). Other key widths trap (lines 30–33). Hashes return the seed (`maps_wasm3.go:76–79`).

`len(m)` is `struct.get` field 0 in the compiler model, because the generic SSA lowering loads byte offset 0 of the map pointer (`wasmtype.go:515–519`). Keeping `used` at field 0 is load-bearing. Do not rearrange it without changing that lowering.

## Strings at run time

Concatenation and slicing are wasm3 ops or small runtime helpers that allocate a new `(array i8)` and `array.copy` or loop `array.get_u`/`array.set`. They do not call the linear-memory `concatstrings` that indexes raw bytes.

`gostringnocopy` on wasm3 uses `unsafe.String`, not a pointer reinterpret (`6f466710d3`). `rawbyteslice` / `gobytes` build the slice with `unsafe.Slice` (`fc66c00062`). Those APIs are the supported way to view a byte array as a string or slice **when the compiler intrinsifies them**. They are not a license to take the address of element 0 and add.

Printing a string copies bytes into the linear bridge and calls `runtime.writeBytes(fd, off, n)`. The JS glue reads `instance.exports.mem` (`wasm_exec_wasm3.js:147–161`). That bridge is why linear memory still exists.

## Interfaces at run time

Construction: `OpWasm3IfaceMake` → `struct.new $go.iface`. Descriptor globals as in `01`.

Dispatch that phase D added: load a funcref from the per-`(I,T)` vtab global and `call_ref` (`594566ff4f`, `8756b8cdde`). The receiver is the data word.

Type assert failure calls `panicdottypeE` / `panicdottypeI` / `panicdottypeNil`, which the wasm3 iface mirror keeps (`iface_wasm3.go:44–50`) and which call `panic` with a `TypeAssertionError`.

The runtime itab table and its cache, which store type pointers as `uintptr`, are not compiled (`iface_wasm3.go:11–18`).

## Reflection

Not supported as a language feature.

`internal/abi/type_wasm3.go:7–16`: the functions that walk a type descriptor (`UncommonType.Methods`, `FuncType.InSlice`, `Name.Data`, …) use `unsafe.Pointer(uintptr(p)+n)` and panic with `wasm3PtrArithUnsupported` (line 200) "until M6 wires up compiler-generated per-type accessors".

`internal/reflectlite/reflectlite_wasm3.go` exists so packages that import it (the `errors` → `os` chain was the one called out) can link. The header says none of reflectlite's pointer arithmetic translates, and a real implementation needs the fat-pointer work plus M6.

`src/reflect` has no wasm3 rewrite. It links only where it happens to call runtime symbols that were stubbed (`GC` no-op, `SetFinalizer` no-op, `Stack` empty). `reflect.Value.Pointer`, `Addr`, `Slice`, `Interface` on arbitrary values, and `StructOf`/`FuncOf`/`ArrayOf` are the casualties `doc/wasm3-design.md` §10.3 already listed. WasmGC types are declared in the type section; there is no instruction that mints a new struct type at run time. `reflect.StructOf` cannot be implemented. Do not plan on it.

The design that was going to make `reflect.Value.Field(i)` work is compiler-generated per-type getter/setter functions hung off the type descriptor, dispatched by `call_ref` (`doc/wasm3-design.md` §10.1). The same mechanism is what `$go.ptr.poly` accessors are. M6 never connected that to `reflect`. A new compiler can expose those accessors as the reflect implementation, or it can ship without `reflect` and be honest about it. The branch did the second, with stubs so the build would link.

## Finalizers, timers, netpoll

`runtime.SetFinalizer` and `AddCleanup` are no-ops in `gc_stubs_wasm3.go`. WasmGC has no finalizer hook. `doc/wasm3-design.md` §12.4 leaves the decision open: drop them, or call `FinalizationRegistry` on a JS host. Neither was implemented. A JS-hosted compiler can do the registry. A WASI-hosted one cannot, unless the engine grows a hook.

Timers still want `gopark`. On js that becomes `WasmPark`, so `time.Sleep` can work if the timer goroutine is actually started and the park id is the one `WasmReady` fires. The timer+channel integration was not finished (`timerchandrain` returns false). `SleepMs` is the primitive that was demonstrated (`doc/wasm3-js-status.md`).

`netpoll` on js/wasm3 returns an empty poller (`runtime/netpoll_jswasm3.go`). `src/runtime/wasm/io_jswasm3.go` declares `AwaitFdReadable` / `AwaitFdWritable` and says "no Go-side I/O wiring yet" (Rungs 22–23). `lib/wasm/wasm_exec_wasm3.js` has the matching JS. `internal/poll` on js/wasm3 is a simplified `pollDesc` whose `wait` is still the old `TODO(neelance)` callback comment. Network dial is not a working feature. `doc/wasm3-netpoll-design.md` defers raw TCP/UDP.

## What the host provides vs what is in the module

In the wasm module:

- All Go code the compiler emitted, as typed functions.
- WasmGC objects for the heap.
- A small linear memory and global 0 as a bump pointer for host FFI only (`src/runtime/wasm/linearmem_wasm3.go:7–16`: "a pointer handed to the host is a uint32 linear-memory offset").
- Exports: `run`, `_run_wasm3_js`, `mem`, plus any `//go:wasmexport`.
- Imports listed below.

In `misc/wasm/wasm_exec_wasm3.js` (the minimal host):

| Import | Behavior |
|---|---|
| `gojs.runtime.nanotime1` / `walltime` | `process.hrtime` |
| `gojs.runtime.resetMemoryDataView` | refresh a DataView after `memory.grow` |
| `gojs.runtime.SleepMs` | suspending `setTimeout` |
| `gojs.runtime.WasmPark` / `WasmReady` | suspending promise map, with ready-before-park |
| `gojs.runtime.SelfWakeMs` | test helper, `setTimeout` then ready |
| `gojs.runtime.LogInt` | debug print |
| `gojs.runtime.Exit` | `process.exit` |
| `gojs.runtime.memSnap` | `process.memoryUsage` to stderr |
| `gojs.runtime.writeBytes` | copy from linear memory to stdout/stderr |
| `gojs.runtime.SpawnGoroutine` | `setImmediate` + `WebAssembly.promising(_run_wasm3_js)` |

The host also wraps `run` and `_run_wasm3_js` in `WebAssembly.promising` (lines 205–216). Without that, the first suspending import throws `SuspendError: trying to suspend without WebAssembly.promising` (comment at lines 205–209).

`lib/wasm/wasm_exec_wasm3.js` adds fd registration and read/write imports for the unfinished poller. `GOOS=wasip1` imports `wasi_snapshot_preview1.fd_write` instead of `gojs`, and has no spawn import.

Not provided by the host, and not implemented in wasm: a Go garbage collector, a preemptive scheduler, thread atomics, finalizers, cgo, and a linear address for a GC object.

## Globals the code generator relies on

From the obj backend (fixed indices):

| Global | Type | Use |
|---|---|---|
| 0 | `i32` or `i64` bump | next free offset in linear memory |
| 1 | `i64` `CTXT` | legacy closure captures pointer |
| 2 | `anyref` `CTXT_REF` | closure context for the captures-in-struct path |

Plus one global per type descriptor, itab, vtab instance, and reference-typed package variable, created at link time.

`runtime/wasm.WriteLinearMemory`, `ReadLinearMemory`, and `ResetLinearMemory` are the only supported way to touch global 0. They are compiler intrinsics (`OpWasm3WriteLinearMemory` and siblings in `Wasm3Ops.go`). `doc/wasm3-design.md` §12.6 marks the "scratch arena, grown with `memory.grow`" question resolved. Programs that never cross the host boundary still get a memory section; the M2 empty main had zero WASI imports and a tiny memory (`doc/wasm3-m2-size-audit.md`).
