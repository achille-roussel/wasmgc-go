# 10 — Open questions

The rest of the design is decided, and the milestones in `09-bootstrap-plan.md` can start against it. These six are the decisions that change a user-visible guarantee or a long-term commitment. Each one records the option the design is already written against, so a reversal is a change to a named section rather than a blank.

## 1. Width of `int`

**Working decision.** `int`, `uint`, and `uintptr` are 32-bit. Slice, string, map, and chan headers store lengths as `i32`. `int64` stays 64-bit. `00-overview.md`.

**Why.** `array.len` and `array.new` are `i32` operations in the core spec. A Wasm array cannot be longer than 2^31−1 elements, so a 64-bit `len` cannot point at more data. wasm3 used `i64` for `int` and `i32` for headers and paid an extend and a wrap at every `len` and index (`docs/wasm3-study/01-type-mapping.md`). The 32-bit model is also what TinyGo uses on Wasm, so the existing body of "small Go on Wasm" already lives with it.

**The other option.** `int` is `i64`, and the headers are `i64` too. `len` extends `array.len` to `i64`. `make` wraps on the way into `array.new` and panics if the high half is non-zero. This matches `cmd/compile` on amd64 and accepts programs that put a file size in an `int`. It makes every index calculation a 64-bit operation, which is what the size budget is spent on in a compiler whose first number is the empty-main binary.

**Not an option.** wasm3's mix, `i64` values and `i32` headers. It is the bug being closed.

**Recommendation.** Keep 32-bit. Revisit only if a real program, named in an issue, cannot be written because `int` overflowed, and that program is in the set we claim to compile.

## 2. Does `go` yield?

**Working decision.** `go f()` enqueues `f` and returns. The current goroutine runs until it parks, returns, or calls `runtime.Gosched`. `main` returning exits before a queued goroutine runs. `04-runtime-and-abi.md`, and the macrotask test in `prototypes/jspi-sched`.

**Why.** This is the Go memory model. The `go` statement does not happen-before a later statement in the parent. It is also the wasm3 host behavior, which dropped spawned goroutines when `main` never parked (`docs/wasm3-study/02-runtime-and-abi.md`). Writing it down as a test stops it from being an accident of `setImmediate` versus microtasks.

**The other option.** Yield at every `go`: the new goroutine runs until it parks, then the parent continues. Programs that assume "the child got a chance" work more often. Programs that have a race and pass because the child happened to run become flaky in the other direction, and the behavior is no longer Go. A variant is to yield only when the run queue was empty, which is closer to the upstream scheduler and still not a guarantee.

**Recommendation.** Keep the Go rule. `Gosched` is the explicit yield, and it is in M7. A debug flag `-yield-at-go` can exist later for people bringing a program across; it should not be the default.

## 3. Finalizers

**Working decision.** `runtime.SetFinalizer` and `runtime.AddCleanup` panic with a fixed string. `04-runtime-and-abi.md`.

**Why.** The Wasm GC proposal has no finalizer. `FinalizationRegistry` observes JS objects. A Wasm struct reference is not a JS object, and holding a JS wrapper to register it keeps the wrapper, not the Wasm object, alive. wasm3 implemented both as no-ops (`docs/wasm3-study/04-open-problems.md`), which lets a program believe a file descriptor or a buffer will be released.

**The other option.** No-op, documented. Programs that call `SetFinalizer` and also free the resource explicitly keep working. Programs that call it as their only free leak, quietly.

**A third option, rejected.** A best-effort registry that finalizes when the Go runtime drops its own reference. That is not a finalizer. It runs too early whenever another reference remains, and too late whenever the host is the only holder.

**Recommendation.** Panic. A one-line change turns it into a no-op if a program we need to compile cannot be edited, and the panic string is the thing that makes that program visible.

## 4. WASI in v1

**Working decision.** No WASI. The only host is JS. `go` on a non-JS host is not a mode. `07-host-and-js.md`.

**Why.** JSPI is a JS API. Core stack switching is still phase 3, V8's `wasmfx` flag defaults off, and Wasmtime's tracking issue #10248 (updated 2026-11-03) still has GC integration unchecked. wasm3's `GOOS=wasip1` did not link, and the continuation resume it would have needed was a stub (`docs/wasm3-study/04-open-problems.md`). A WASI mode with no goroutines is a second product: every package that parks has to compile-error, and `net` is the reason people ask for WASI.

**The other option.** A single-activation WASI target on which the `go` keyword is a compile error, `time.Sleep` is a wasi poll, and `println` writes to `fd_write`. It serves command-line tools that do not start goroutines. It doubles the runtime's build and the CI matrix before M7 exists.

**Recommendation.** Stay on JS through M9. Reconsider a goroutine-free WASI target only after `fmt` works and someone has a program that cannot run in Node. The park import is the seam, so the IR does not change.

## 5. How hard to fail on `reflect.StructOf`

**Working decision.** `StructOf`, `FuncOf`, `ArrayOf`, `ChanOf`, `MapOf`, and `SliceOf` look the result up in the descriptor table. The table contains every type the program wrote, plus `*T`, `[]T`, and `map[T]struct{}` for each reachable `T`. A miss panics at run time with the type that was asked for. `05-unsafe-and-reflect.md`.

**Why.** `encoding/json` v2 in the Go 1.27.4 pin calls `SliceOf` and `MapOf` with types that come from the user's map. Those calls are reachable in any program that imports `encoding/json`, and the arguments are not constants the compiler can see. A compile error on every reachable `SliceOf` would reject `encoding/json`. The closure covers the calls json actually makes. `StructOf` is not in that set; a program that calls it panics, which is the honest failure for an API that would have to mint a Wasm struct type.

**The other option.** If `StructOf` or `FuncOf` is reachable from `main`, the compile fails, even when the call is data-dependent. `SliceOf` and `MapOf` stay as lookups. This catches the unsupported API earlier and rejects programs that mention `StructOf` in a dead branch the reachability pass cannot delete.

**Recommendation.** Keep the panic. The message names the type. Promote `StructOf` to a compile error only if, in practice, programs fail deep inside a request and the panic is hard to attribute. The lookup path should not change either way.

## 6. Monomorphize everything

**Working decision.** Every generic function and generic method is one copy per instantiation. No dictionaries. `01-architecture.md`.

**Why.** A dictionary erases values to `anyref` and threads a type record through the body. That gives up the typed fields Wasm GC is for, on the exact code (generic `slices`, generic maps) where the types matter. Go 1.27's generic methods are not interface methods, so they do not need a dictionary to be called through an itable.

**The other option.** Dictionaries for functions whose body does not depend on the element representation (a function that only passes `T` through), and monomorphization for everything else. Smaller code when `slices.Sort` is instantiated on many structs. More compiler, and a second calling convention, before there is a measurement that says the size budget was missed because of copies.

**Recommendation.** Monomorphize through M8. If `fmt.Println("hello")` or a program that uses `slices` on a handful of types blows the 128 KiB budget, and the size report shows the copies, then add dictionaries for the functions the report names. Not before.
