# 07 — Host and JS

The host is JavaScript: a browser, or Node.js. The module imports from a module named `go` and exports `run`, `_sched`, and `memory`. The glue is `lib/wasmgc.js`, one file. It reads `WebAssembly.Module.imports` and installs only the `go.*` names the module declares; `WebAssembly.Suspending` is constructed only for imports in the suspending set (`runtime.park`, `runtime.gosched`, and a later `js.Await`). A program that never parks therefore never mentions `Suspending` and instantiates on any engine that has Wasm GC, including Node 22. `wasmgc-go build -target node` may still emit a specialized runner that inlines that import set; the shared file must stay correct when used as-is.

wasm3's glue was two copies (`misc/wasm/wasm_exec_wasm3.js` at 233 lines and `lib/wasm/wasm_exec_wasm3.js` at 521) and it constructed `WebAssembly.Suspending` unconditionally. Node v22.23.4 throws `WebAssembly.Suspending is not a constructor` (`docs/wasm3-study/00-overview.md`). This glue does not.

## What the engine has to implement

Wasm GC, including `struct.new`, `array.new`, `array.new_data`, `array.copy`, `call_ref`, and `ref.cast`. Shipped in Chrome 119 (October 2023) and in every Node that this project can be built with. The aggregate instructions are in the core spec draft of 20 November 2026 ([W3C TR](https://www.w3.org/TR/wasm-core-2/)).

Exception handling: `try_table`, `throw`, `throw_ref`, `exnref`. In the spec since the July 2025 working-group merge (version 3.0). `prototypes/eh-panic` instantiates on Node 22.23.4 and Node 26.13.1.

JSPI, only when the program parks or the glue wraps `run` because a suspending import is present. The API is `WebAssembly.Suspending` and `WebAssembly.promising`, described in the [JSPI overview](https://github.com/WebAssembly/js-promise-integration/blob/main/proposals/js-promise-integration/Overview.md) and in the JS API draft. It adds no Wasm instructions. Verified behavior, from `prototypes/jspi-sched` on Node v26.13.1 (released 2026-11-10):

- A local `i32` and a local `(ref $box)` survive a suspend, including two suspends in one activation.
- A second `promising` call runs while the first activation is suspended.
- A mutable global is shared. The parking activation restores it from a local.
- An import wrapped in `Suspending` that returns a number rather than a `Promise` does not suspend.
- An import can return a Wasm struct reference (the JS function called an export that did `struct.new`).
- A suspending import can return an `externref`, and a non-suspending import can round-trip one.
- A rejected promise rejects the `promising` export.
- A direct call that reaches a suspending import throws `SuspendError: trying to suspend without WebAssembly.promising`.
- A `setTimeout(0)` callback runs after the microtask reactions of the main export's promise.

## Node and browser versions

| Host | JSPI |
|---|---|
| Node.js 26, tested v26.13.1 | on by default |
| Node.js 25 | on by default ([wasm-bindgen JSPI table](https://wasm-bindgen.github.io/wasm-bindgen/reference/jspi.html); the v25 line took it from V8 directly) |
| Node.js 24 | `--experimental-wasm-jspi`, and only if `typeof WebAssembly.Suspending === "function"`. The v24 backport is nodejs/node#65123, merged 2026-08-13. |
| Node.js 22, tested v22.23.4 | not supported. `--experimental-wasm-jspi` exposes `WebAssembly.Suspender` and does not expose `Suspending` or `promising`. |
| Chrome and Edge 137+ | on by default |
| Firefox 153+ | on by default |
| Safari 27+ | on by default |

JSPI across those three browser engines is recorded as newly available on 2026-09-14. `wasmgc-go run` checks `process.version` and the constructor and exits with the table above when the host is too old. A module with no suspending import skips the check.

The glue never references `WebAssembly.Suspender`. That constructor is the earlier API the wasm3 notes were already migrating off (`docs/wasm3-study/04-open-problems.md`).

## Imports

All of them are in the module `go`. Names are stable. Types are Wasm types; `i32` lengths match the ABI.

| Import | Suspending | Signature | Behavior |
|---|---|---|---|
| `runtime.write` | no | `(fd i32, ptr i32, n i32) -> i32` | copy `n` bytes from linear memory at `ptr` to fd 1 or 2. Return bytes written. |
| `runtime.exit` | no | `(code i32)` | Node: `process.exit`. Browser: throw `GoExit(code)` the glue catches. |
| `runtime.walltime` | no | `() -> (i64, i32)` | seconds, nanoseconds. |
| `runtime.nanotime` | no | `() -> i64` | monotonic nanoseconds. |
| `runtime.rand` | no | `(ptr i32, n i32)` | fill linear memory from `crypto.getRandomValues`. |
| `runtime.park` | yes | `(id i32) -> i32` | the park in `04`. |
| `runtime.ready` | no | `(id i32)` | the ready in `04`. |
| `runtime.spawn` | no | `()` | `setTimeout` a macrotask that calls `promising(exports._sched)`. |
| `runtime.gosched` | yes | `()` | resolves on a macrotask. |
| `runtime.scheduleTimer` | no | `(id i32, lo i32, hi i32)` | `ns` is the `i64` split in two `i32`s because a JS number is not an `i64`. `setTimeout` of `ns/1e6` milliseconds, then `ready(id)`. |
| `runtime.clearTimer` | no | `(id i32)` | clear that timeout. |
| `runtime.getArgc` / `getArg` / `getEnv` | no | linear-memory copies | `os.Args` and `os.Environ`. |
| `js.get` `js.set` `js.call` `js.new` `js.value` | no, except `js.call` which is suspending when the JS result is a Promise the Go side chooses to await | `externref` in and out | `syscall/js`, below. |

`scheduleTimer` takes the delay as two `i32` limbs so the import stays inside the types JS can pass without a BigInt conversion at every call. The runtime splits the `i64`. This is a concession to the JS API, not to the Wasm ABI: inside the module the duration is `i64`.

Returning a reference from an import works, both for a Wasm struct and for an `externref`. wasm3's `jspi_jswasm3.go` still called a returned reference a gap (`docs/wasm3-study/02-runtime-and-abi.md`). The prototype closes it on Node 26.

## Exports

| Export | Who calls it |
|---|---|
| `run` | the glue, under `promising` if the module imports any suspending function. Otherwise the glue calls it directly. |
| `_sched` | the glue, under `promising`, once per spawned goroutine. |
| `memory` | the glue, to build a `Uint8Array` for `runtime.write` and `runtime.rand`. |
| each `//go:wasmexport` | the host page, under `promising` if the body can park. The compiler knows which exports reach a suspending import and records it in a custom section `wasmgc-exports` of JSON, so the glue does not have to guess. |

There is no `_start`. WASI is not a v1 target.

`run` calls the generated init functions in dependency order and then `main.main`. When `main.main` returns, `run` returns. The glue's promise reaction exits. See `04` for why the spawn is a macrotask and the reaction is a microtask.

## Linear memory

One memory, one page to start, grown by the bridge when a write does not fit. Global 0 is the bump `i32`. The Go-side bridge (today: codegen intrinsic for body-less `runtime.printstring` in `std/src/runtime`) copies a string's bytes into the memory with `array` loads and `i32.store8`, then calls the host import `go.runtime.write`, then resets the bump. The memory is not a heap and not a place a Go pointer can point. The study's rule holds: a pointer handed to the host is a `uint32` offset (`docs/wasm3-study/03-stdlib-and-unsafe.md`). When `//go:wasmimport` is wired, the same import will be declared from Go as `runtime.write`.

The copy is explicit because the host cannot read a Wasm GC array. The JS string builtins proposal is a possible later replacement for the UTF-8 transcode; v1 does the copy in Wasm and a `TextDecoder` on the other side only for `syscall/js` strings. `println` does not decode. It writes bytes.

## `syscall/js`

`js.Value` is `externref`. Null is the JS `null`. `undefined` is a sentinel the package keeps as a package-level `externref` global obtained once from `js.value("undefined")`. `js.ValueOf` of a Go string copies to a JS string through an import. `js.ValueOf` of a number is a JS number. `js.ValueOf` of a bool is a JS bool.

```go
package js

type Value externref // the real type is the compiler's extern mapping

func (v Value) Get(name string) Value
func (v Value) Set(name string, x any)
func (v Value) Call(name string, args ...any) Value
func (v Value) Invoke(args ...any) Value
func (v Value) New(args ...any) Value
func Global() Value
```

`Call` is not suspending by default: the glue calls the function and, if the result is a `Promise`, returns it as an `externref` the Go code can hold. `js.Await(v Value) (Value, error)` is the suspending operation. It wraps `Promise.resolve(v)` and parks until it settles. A rejection becomes the `error`. This split is deliberate. wasm3 could not represent `js.Value` in the import ABI at all (`docs/wasm3-study/03-stdlib-and-unsafe.md`), because the import checker wanted numbers. `externref` is the representation that makes `Get` and `Call` one import each.

`js.CopyBytesToGo` and `CopyBytesToJS` copy between a `[]byte` and a JS `Uint8Array` through the linear bridge. They are the bulk path. `func` values passed to JS are `//go:wasmexport` wrappers or a single imported `js.makeFunc` that registers a closure id; the host calls back through the export `_jsCallback(id i32)`, which the glue invokes as its own promising turn when no activation is running, and queues when one is. The callback does not preempt. That is the same single-activation rule as the mutex (`04`).

## Browser and Node differences

The glue branches on `typeof process`. Node writes with `fs.writeSync`. The browser writes with `console.log` of a decoded chunk for fd 2 and a registered `write` callback for fd 1, defaulting to `console.log` as well. `exit` in the browser throws instead of killing the page. Arguments in the browser are `location` search parameters only if the page set `globalThis.goArgs`; otherwise the slice is empty. The Node path uses `process.argv.slice(2)` when the runner was started as `node prog.wasm.js args...`.

`fetch` for the HTTP client is `globalThis.fetch` in both. Node 26 has it.

## Hand-written Wasm and host imports

Asm modules ([`12-wasm-assembly.md`](12-wasm-assembly.md), decision 8) may declare additional imports from the `go` module (for example a bridge helper). The glue still only installs names the merged module actually imports. An asm function must not invent a second suspending protocol; park and `js.Await` stay the imports in the table above. `//go:wasmgc nosuspend` forbids those imports in that body and is the **default** for asm in `runtime` and `internal/bytealg` (decision 11).

## What is not in the glue

A Go garbage collector, a thread pool, `Atomics.wait`, a working finalizer, and any WASI import. `SetFinalizer` and `AddCleanup` are no-ops in the runtime (`04-runtime-and-abi.md`), not host calls. `GOOS=wasip1` in the wasm3 tree did not link (`docs/wasm3-study/04-open-problems.md`), and a WASI host still has no JSPI and no shipping stack switching. No WASI target in v1 was decided on 2026-11-22.

WasmFX is not feature-detected and not emitted. The flag `wasmfx` in V8 defaults to off, and the proposal was still phase 3 in August 2026, with Wasmtime's implementation tracking issue still listing GC integration as open in November 2026. The runtime's park import is the seam a future stack-switching host would replace, inside `runtime.gopark`, without a change to the IR.
