# 04 — Runtime and ABI

The runtime is a Go package at `std/src/runtime`, written against the types in `03-type-mapping.md`. It is not a port of `src/runtime`. The wasm3 runtime is a catalog of every file that assumed a linear heap (`docs/wasm3-study/03-stdlib-and-unsafe.md`); copying it is how that branch grew 80,000 lines. What this runtime keeps from that branch is the behavior that was right: host GC, JSPI as the stack, write-before-ready on channels, and a FIFO of goroutines the host runs later.

Generated code calls a small surface. Anything the compiler can emit as `struct.new` or `array.copy` is not a runtime call. wasm3 treated a missed intrinsic as a throw from `mallocgc` (`docs/wasm3-study/02-runtime-and-abi.md`). The same rule holds: if a lowering is supposed to be an instruction, a call that reaches a runtime stub panics with `runtime: lowering missed`.

## Calling convention

A function is a Wasm function. Parameters and results are the lowered types. A struct, an array, a string, a slice, an interface, a map, a chan, a func, and a pointer are each one reference. `int` is one `i32`. `complex128` is two `f64` results when it is a call result, and one struct reference when it is stored (`03-type-mapping.md`). Multi-value returns are Wasm multi-value.

References in the signature are the precise type: `(ref $go.string)` for a string, which is never nil, and `(ref null $go.slice.T)` for a slice, which is. They are not `anyref`. wasm3 widened to `anyref` at the boundary because each function carried its own type-index space (`docs/wasm3-study/02-runtime-and-abi.md`). One intern table removes the reason. `ref.cast` remains at the points where the static type is `anyref`: fat-pointer bases, interface data words, and `unsafe.Pointer`.

A Wasm `call` does not clobber the caller's locals. Nothing is spilled across a call for register reasons. There is no Go stack, no `morestack`, and no stack map. The engine owns the call stack. `debug.SetMaxStack` returns the engine's stack and does not set it.

`//go:wasmimport go name` declares an import. `//go:wasmexport name` declares an export. The signatures use the same lowering. A wasmimport that is suspending is marked `//go:wasmgc suspending` and the glue wraps it in `WebAssembly.Suspending`. The compiler does not emit a suspending call except through those declarations.

### Closures and methods

The closure reference is the first parameter. The body loads captures with `struct.get`. A method's first parameter is the receiver. An interface call is `call_ref` of the itable funcref (`03-type-mapping.md`). A direct call of a concrete method is `call`.

### The current goroutine

```wat
(global $g (mut (ref null $go.g)))
```

`$go.g` holds the goroutine id, the park id, the panic chain, the `recoverOk` flag, and a pointer at the select in progress. It does not hold the defer list. Defers are per frame.

Wasm globals are shared by every JSPI activation. Locals are not. `prototypes/jspi-sched` shows a second activation clobbering a global while the first is suspended, and the first restoring it from a local. The park sequence is the only place that relies on that:

```
local.set $savedG (global.get $g)
call $go.runtime.park (parkID)     ;; may suspend; other activations run
global.set $g (local.get $savedG)
```

User code does not save `$g`. After `park` returns, `$g` is the goroutine that parked. A host callback that enters Wasm saves `$g`, installs the callback's goroutine, and restores on the way out (`07-host-and-js.md`).

wasm3 had no lowering for `getg`, so the defer list was a package global and two goroutines shared it (`docs/wasm3-study/02-runtime-and-abi.md`). The global `$g` is how that stops.

## Defer, panic, recover

The mechanism is the exception-handling proposal: one tag, `try_table`, `catch_ref`, `throw`, `throw_ref`, `exnref`. The proposal was merged into the spec at the July 2025 working-group meeting (spec version 3.0; the explainer is [Exceptions.md](https://github.com/WebAssembly/spec/blob/main/proposals/exception-handling/Exceptions.md), the October 2023 revision that replaced the old `try`/`catch` with `try_table`). `prototypes/eh-panic/panic.wat` runs the shape below on Node 22.23.4 and Node 26.13.1: a frame with no `try_table` lets the exception pass, `throw_ref` rethrows the same exception, a defer runs on the catch path and on the normal path, and skipping `throw_ref` is a recover.

```wat
(tag $go.panic (param (ref $go.panicObj)))
```

`$go.panicObj` is `{value iface, recovered i32, next}`. `throw` carries the object. `catch_ref` delivers the object and the `exnref`. The handler that recovers drops the `exnref`. The handler that does not calls `throw_ref`.

### Frame shape

A function that contains a `defer` lowers to the nesting in the prototype. The defer list is a local, a linked list of closures pushed at the `defer` statement. Argument expressions are evaluated at the `defer`, not at the call, and stored in the closure. On the normal return the function calls `runtime.deferRun` and then returns. On the catch path it calls `runtime.deferRun` and then returns or rethrows.

`runtime.deferRun` walks the list from the head (LIFO). For each node it sets `$g.recoverOk` to 1, clears it in the deferred function's prologue, and calls the closure. The prologue rule, which every function that contains `recover` executes:

```
local $allow = struct.get recoverOk of $g
struct.set recoverOk of $g to 0
```

`recover` returns the current panic value only when its function's `$allow` local is 1, and sets `recovered` on the panic object. A helper called from a deferred function sees `$allow == 0` because the deferred function cleared the global before the call. That is the spec rule that `recover` works only when called directly by a deferred function. Functions that do not contain `recover` and whose callees are all known not to contain it skip the prologue; the dead-code pass deletes it.

A panic inside a deferred call is a new `$go.panic` thrown from inside `deferRun`'s own `try`. The new object becomes the one `deferRun` is propagating, and the remaining defers still run. A `recover` in a later defer (an earlier one, since the walk is LIFO) observes the new panic. This is the Go nesting, with the exception as the unwinder.

A function with no defer and no `recover` has no `try`. Index and nil panics are `throw` of a panic object built at the check.

### What an unrecovered panic does

The goroutine entry, which is the function the host calls under `WebAssembly.promising`, is wrapped in `try_table` / `catch_all_ref`. If the exception is `$go.panic` and `recovered` is clear, the entry prints the panic value and calls the host `go.runtime.exit` with status 2. An unrecovered panic in a child goroutine exits the process. It does not return from that goroutine and leave the others running. That matches Go.

wasm3 ran every defer on a global list and then called `exit(0)` on a recovered panic, because the panicking function was marked noreturn and there was no `try_table` to return through (`docs/wasm3-study/02-runtime-and-abi.md`). An unrecovered panic recursed until the engine stack overflowed, and the value was not printed. Neither behavior is kept. The prototype's `propagate` case is the cross-function path wasm3 could not do.

`runtime.Goexit` throws a distinct tag `$go.goexit` that `deferRun` does not treat as a recoverable panic. Defers run. `recover` returns nil. The goroutine entry swallows it and returns. `Goexit` in `main` exits the process with status 0, after defers.

## Goroutines

JSPI is the scheduler. There is no `P`, no run queue inside the Go sense of a stealing scheduler, and no `schedule` function. The JS event loop is the scheduler. The proposal adds no Wasm instructions ([JSPI overview](https://github.com/WebAssembly/js-promise-integration/blob/main/proposals/js-promise-integration/Overview.md)): `WebAssembly.promising` wraps an export, `WebAssembly.Suspending` wraps an import, and a suspending import that returns a `Promise` suspends the activation until the promise settles. A return that is not a `Promise` does not suspend. A rejected promise propagates an exception into the activation. A suspending import reached without `promising` throws `SuspendError`. `prototypes/jspi-sched` checks each of those on Node 26.13.1, including that an `i32` local and a struct reference survive a suspend, and that a suspending import may return an `externref`.

### State

```wat
(type $go.g (sub $go.object (struct
  (field $id        i32)
  (field $park      (mut i32))
  (field $panic     (mut (ref null $go.panicObj)))
  (field $recoverOk (mut i32))
  (field $fn        (mut (ref null $go.clos.void))))))
```

The host-side structures, owned by `lib/wasmgc.js`: a `Map` from park id to resolver, a `Set` of park ids that were readied before the park, and a queue of closures waiting for a `_sched` turn. Park ids are `i32`, minted by the runtime, never reused while a waiter exists.

### `go f()`

`runtime.go` appends the closure to a FIFO on `$go.g`'s scheduler struct (a single global `$runq`, manipulated only while an activation is running, so it needs no lock) and calls the non-suspending import `go.runtime.spawn`. The host schedules a macrotask, `setTimeout(fn, 0)`, that calls `WebAssembly.promising(exports._sched)`. `_sched` dequeues one closure, installs it as `$g`, and calls it.

The current goroutine does not yield at `go`. It keeps running until it parks, returns, or calls `runtime.Gosched`. That is legal Go: the `go` statement happens-before the new goroutine starts, and it does not happen-before any later statement in the parent in the sense of the child having run. Achille confirmed this on 2026-11-22. There is no yield-at-`go` mode.

### Park and ready

`go.runtime.park(id i32) -> i32` is suspending.

- If `id` is in the already-ready set, the glue removes it and returns `id` as a number. The activation does not suspend. The prototype records this as `parkShape == "value"`.
- Otherwise the glue returns a `Promise` and stores the resolver under `id`.

`go.runtime.ready(id i32)` is not suspending. If a resolver is stored, it is called. Otherwise `id` is added to the already-ready set. Ready-before-park is the case the set exists for. On a single cooperative thread the window is small, and the prototype forces it anyway, because a host timer can ready an id the runtime has published but not yet parked on.

`runtime.gopark` is the Go-side wrapper that saves `$g`, calls `park`, and restores `$g`. `runtime.goready` calls `ready`.

### When `main` returns

The host wraps `run` in `promising`. The returned promise's reactions are microtasks. `spawn` uses a macrotask. The prototype's `exit` case records `microtask:main-then` before `macrotask:exit`. The glue's reaction calls `go.runtime.exit`, which is `process.exit` in Node and throws an exit exception in the browser. Goroutines queued and not yet started do not run. Goroutines already suspended stay suspended and the process is gone. This matches Go, and it matches the wasm3 glue's `setImmediate`-versus-microtask behavior (`docs/wasm3-study/02-runtime-and-abi.md`) with the ordering written down as a test.

### `Gosched`, deadlock, preemption

`runtime.Gosched` is a suspending import that resolves on a macrotask (`setTimeout(0)`), so spawned goroutines and timers run. A microtask yield would not reach them.

Before parking for a channel, a mutex, or a nil channel, the runtime counts runnable goroutines, the run queue, and pending timers. If this park would leave nothing runnable and nothing that can call `ready`, it panics `all goroutines are asleep - deadlock`. A park whose reason is "host callback" (a `syscall/js` call, a `fetch`) is not a deadlock: the host may ready it.

There is no preemption. The IR reserves a call `runtime.preemptCheck()` that v1 inlines to nothing, so a later pass can insert a real yield on loop back-edges without changing the lowerer. v1 does not insert it.

### `sync.Mutex`, `WaitGroup`, `Once`

These are written in Go on top of `gopark` and `goready`.

`Mutex`: an `i32` state and a waiter list. `Lock` of an unlocked mutex stores 1 and returns. `Lock` of a locked mutex enqueues a waiter and parks. `Unlock` of a mutex with waiters readies one. There is no spin. One activation runs at a time, so the state load and store do not race with another goroutine. They race with a host callback only if a callback enters while a goroutine is running, which the host contract forbids (`07-host-and-js.md`): callbacks run as their own turn, when no activation is on the stack, or they are queued until the current activation parks.

`WaitGroup` is a counter and a waiter list. `Once` is a state word. `Cond` does not use the `uintptr(unsafe.Pointer)` copy check in the upstream `sync.Cond` (`docs/wasm3-study/03-stdlib-and-unsafe.md` and `06-stdlib-strategy.md`); the overlay drops that check.

## Channels and select

The layout is `$go.chan.T` in `03-type-mapping.md`. Send and receive of the payload are generated per element type. Queue manipulation is shared, on `$go.chan`.

### Unbuffered send

If a receiver is queued, the sender writes the value into the receiver's destination (a one-element array the receiver allocated before parking), dequeues that waiter, and readies it. The write happens before `ready`. wasm3 reversed this once and `select`'s probe observed a ready channel with an empty slot (`docs/wasm3-study/02-runtime-and-abi.md`, commit `65a5736298`). The order is part of the protocol.

If no receiver is queued, the sender enqueues itself with the value stored in the slot the waiter carries, then parks. A receiver that finds a queued sender reads the slot and readies the sender.

### Buffered send

The buffer is a ring. `sendx`, `recvx`, and `qcount` behave as in `hchan`. A send into a non-full buffer does not park. A send into a full buffer queues a waiter. Close sets `$closed`, readies every receiver (they observe zero, `ok == false`) and every sender (they panic "send on closed channel").

### Select

`select` is not a 1 ms poll. wasm3's blocking `select` called `SleepMs(1)` (`docs/wasm3-study/02-runtime-and-abi.md`). The algorithm:

1. Shuffle the case order with `runtime.fastrand`.
2. For each case, in that order, try the operation without parking. The first that can proceed wins.
3. If none can and there is a default, take the default.
4. Otherwise allocate one `select` object, enqueue a waiter on each channel with that object and the case index, and park once.
5. The partner that completes an operation sets `sel.winner`, dequeues that waiter, and readies the select's park id. On wake, the select dequeues its other waiters and runs the winning case.

Single-threaded execution is what makes step 4 safe: between the enqueue and the park, no other goroutine runs, so a wakeup cannot be lost in that window. The ready-before-park set still covers a timer that fires at the park boundary.

A receive from a nil channel and a send to a nil channel are cases that never succeed in step 2. A `select` of only nil channels with no default parks and then deadlocks if nothing else can run.

## Timers

`time.Sleep` and `time.Timer` share one host import, `go.runtime.scheduleTimer(id i32, ns i64)`. The glue calls `setTimeout` and, on fire, `ready(id)` if the timer is still armed. `go.runtime.clearTimer(id)` cancels it. Timer channels are unbuffered, matching Go 1.27. The fire path runs as a macrotask, which is when Wasm is suspended or idle, and does a send on the timer's channel using the same send path as user code. If the receiver is not waiting, the send parks the timer turn; the turn is a short-lived promising activation whose only job is that send. It does not spin.

`time.Now` reads `go.runtime.walltime`, which returns `(sec i64, nsec i32)`. `runtime.nanotime` reads `go.runtime.nanotime` and returns `i64`.

## Maps

Operations other than the generated hash and the generated equality are shared. The compiler emits a call to `runtime.mapAssign`, `runtime.mapAccess`, `runtime.mapDelete` with the key and the value as `anyref` plus a funcref pair `(hash, eq)` stored on the map at `make` time, or it emits the operation inline when the key type is a scalar. Inlining scalars matters for `map[int]T` and `map[string]T`; the shared path is for struct and interface keys.

`make(map[K]V, hint)` allocates `ctrl`, `keys`, and `vals` at the next power of two above `hint / 0.875`, or leaves them null when `hint` is 0. The first insert allocates a table of 8.

Iteration is the snapshot in `03-type-mapping.md`. The iterator is a small struct the `range` loop holds as a local.

## Strings and bytes

`runtime.concatstrings`, `runtime.slicebytetostring`, and `runtime.stringtoslicebyte` are the calls the lowerer emits when it cannot inline the copy. They are ordinary Go over `array.new` and `array.copy`. `runtime.memmove` and `runtime.memequal` for byte arrays are `array.copy` and a loop of `array.get_u`. There is no `uintptr` overlap test. Two slices alias when their `$data` is `ref.eq` and the ranges overlap; `array.copy` is defined to handle overlap, so the runtime copy does not need the test. wasm3's `slices.overlaps` returned false for the same reason (`docs/wasm3-study/03-stdlib-and-unsafe.md`).

`println` of a string writes the bytes through the linear bridge (`07-host-and-js.md`). `println` of an integer formats into a stack array of 20 bytes and slices exactly the digits. The wasm3 formatter included one zero byte past the last digit (`docs/wasm3-study/03-stdlib-and-unsafe.md`). The slice starts at the index of the first digit.

## Allocation

`struct.new`, `struct.new_default`, `array.new`, `array.new_default`, `array.new_data`. No size classes, no `mallocgc`, no write barrier. The host traces references.

Package-level variables of reference type are Wasm globals, one per variable, initialized in the generated init function. Scalar package variables are globals of that scalar type. The init functions run, in dependency order, at the start of `run`, before `main.main`.

## Finalizers

`runtime.SetFinalizer` and `runtime.AddCleanup` are silent no-ops. Achille decided this on 2026-11-22, reversing the earlier plan to panic. `SetFinalizer` returns. `AddCleanup` returns a `Cleanup` whose `Stop` is also a no-op. There is no finalizer hook in the Wasm GC proposal, and a JS `FinalizationRegistry` observes JS objects, not Wasm struct references, so the functions cannot be implemented. They are no-ops rather than panics so that upstream packages which register a finalizer and also `Close` keep compiling and keep working when the caller does close.

The leak is the caller's. A value that is never closed, and whose only release path was the finalizer, is not released. The host object (an fd, a process handle, a cache entry) stays alive for the life of the page. wasm3 had the same no-op (`docs/wasm3-study/04-open-problems.md`). The difference from a panic is that the leak is silent. The runtime does not print, and it does not count skipped finalizers.

Sites in the go1.27.4 tree, and whether our build tags compile them:

| Site | Built for `GOOS=js`, `GOARCH=wasmgc`? | What the no-op leaks |
|---|---|---|
| `os/file_unix.go` `SetFinalizer(f.file, (*file).close)` | No. The tag is `unix \|\| (js && wasm) \|\| wasip1`, and `wasm` is not our arch. | Nothing in v1. `os` does not open files (`06-stdlib-strategy.md`). A later `File` that opens a host resource must `Close` it in `Close`, not in a finalizer. |
| `net/fd_fake.go` `SetFinalizer(fd, (*netFD).Close)` | Yes. The tag is `js \|\| wasip1`. | A `net` connection that is dropped without `Close` keeps its host resource. `net` is not in v1. When it is vendored, `Close` is mandatory. |
| `os/exec.go` `AddCleanup` on a process handle | Yes. The file has no build tag. | A `Process` that is not `Release`d or `Wait`ed keeps the handle. `os/exec` is not in v1. |
| `os/exec/exec.go` `SetFinalizer` on `*Cmd` | Yes, if `os/exec` is built. | This finalizer is a leak detector: it panics when a started `Cmd` is collected without `Wait`. The no-op means that panic never fires. A leaked process is silent. |
| `crypto/tls/cache.go` `AddCleanup` on a parsed certificate | Yes, if `crypto/tls` is built. | The weak certificate cache never evicts. Entries accumulate for the life of the page. `crypto/tls` is not in v1, and it also uses `weak.Pointer`, which v1 does not implement. |

`weak.Pointer` and `weak.Make` are not implemented. A package that calls them does not vendor. That is independent of the finalizer no-op, and it is why `crypto/tls` stays out.

## Atomics

`internal/runtime/atomic` and `sync/atomic` compile to plain loads and stores. One activation runs at a time, and a host callback does not preempt it, so a load or a store of a word is atomic with respect to other goroutines. `CompareAndSwap` is a compare and a store in Go. This is the wasm3 conclusion (`docs/wasm3-study/03-stdlib-and-unsafe.md`) stated as the memory model rather than as a TODO. It is false if a future mode enables Wasm threads. v1 has no such mode. `atomic.Value` stores one interface reference; the fields of `$go.iface` are immutable, so the reference swap is the atomicity.

## Runtime surface the compiler calls

These names are stable. Codegen and the runtime workstream share this list.

| Symbol | Role |
|---|---|
| `runtime.go(fn func())` | enqueue and `spawn` |
| `runtime.gopark(id int32, reason int32)` | save `$g`, park, restore |
| `runtime.goready(id int32)` | ready |
| `runtime.deferRun(d *defer_)` | LIFO, with the recover protocol |
| `runtime.throwPanic(v interface{})` | `throw` |
| `runtime.gorecover() interface{}` | the `recover` builtin |
| `runtime.goexit()` | the `Goexit` tag |
| `runtime.mapAccess` / `mapAssign` / `mapDelete` | shared map path |
| `runtime.chanSend` / `chanRecv` / `chanClose` | shared queue path; payload moves stay in generated code |
| `runtime.selectGo(cases *select_) int32` | the select algorithm |
| `runtime.concatstrings` and the slice/string copies | when the lowerer does not inline |
| `runtime.fastrand() int32` | xorshift on a global, seeded from `go.runtime.rand` |
| `runtime.nanotime() int64` / `runtime.walltime() (int64, int32)` | host clocks |
| `runtime.printstring` / `printint` / `printnl` | the builtins |
| `runtime.exit(code int32)` | host exit |

**M3 note.** Until M5, slice/array index failures and `growslice` overflow are emitted as Wasm `unreachable` (trap), not as calls to `runtime.panicIndex` / `runtime.growslice`. Those symbols are reserved for M5 and are not synthesized by codegen today. Temporary print stubs remain in codegen (same as M2) until stream F lands `std/src/runtime`.

`runtime.GOMAXPROCS` returns 1. `runtime.NumGoroutine` returns the count of goroutines that have been started and have not returned. `runtime.KeepAlive` is a call the escape pass treats as a use. `runtime.GC` is a no-op: the host collects when it collects.
