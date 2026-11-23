# Prototypes

Hand-written Wasm modules that pin down engine behavior the design depends on. They are not the compiler. Each `run.mjs` shells out to [`wasm-tools parse`](https://github.com/bytecodealliance/wasm-tools) (tested with 1.263.0). wabt 1.0.42's `wat2wasm` still rejects `struct.get`, so these modules are not assembled with it.

```sh
export PATH="/path/to/wasm-tools:/path/to/binaryen/bin:$PATH"
node prototypes/eh-panic/run.mjs
node prototypes/fat-pointer/run.mjs
node prototypes/jspi-sched/run.mjs   # Node 25 or newer
node prototypes/simd/run.mjs         # regenerates kernels.wasm via wasm-tools
node prototypes/wasm-asm/run.mjs     # also needs Binaryen wasm-merge ≥113
```

## Results recorded 2026-11-23

Host: Node.js v26.13.1 (JSPI on by default; `WebAssembly.Suspending` and `WebAssembly.promising` are functions, `WebAssembly.Suspender` is absent). The same EH and fat-pointer modules also instantiate on Node.js v22.23.4. Node v22.23.4 does not have `WebAssembly.Suspending` even with `--experimental-wasm-jspi` (it has the older `WebAssembly.Suspender`).

| Module | What it established |
|---|---|
| `eh-panic/panic.wat` | `try_table` / `catch_ref` delivers a `(ref $panic)` plus an `exnref`. A frame with no handler lets the exception pass through. `throw_ref` rethrows that same exception to the caller. A defer recorded on the catch path runs, and skipping `throw_ref` is a recover. A defer also runs when nothing was thrown. |
| `fat-pointer/pointer.wat` | A pointer value `{accessor, base, index}` loads and stores a struct field and a packed `i8` array element through `call_ref`. Nil is a null ref. The struct field index is an immediate inside the getter; the `i32` is the array index. |
| `jspi-sched/sched.wat` | An `i32` local and a `(ref $box)` local survive `WebAssembly.Suspending`, including across two suspends. A second `WebAssembly.promising` activation runs while the first is suspended, observes a shared mutable global, and clobbers it; the first activation restores the global from its local after resume. `ready` before `park` is a returned number, not a `Promise`, so that call does not suspend. A non-suspending import can return a Wasm struct reference. A suspending import can return an `externref`. A rejected promise fails the promising export. A direct call that reaches `park` without `promising` throws. A `setTimeout(0)` spawn runs after the microtask that observes the main export's returned promise. |
| `simd/` | `(array v128)` and `v128` struct fields work on Node 22 and 26. Scalar `array.get_u` loops vs lane-build SIMD vs bridge+`v128.load` vs native `(array v128)` for IndexByte / Equal / Count: true SIMD is ~10–17× scalar at 1 MiB, but building a `v128` from sixteen gets is only ~1.5×, and a one-shot bridge copy does not amortize. Numbers in `simd/README.md`. Design: `docs/design/11-simd.md`. |
| `wasm-asm/` | Hand-written WAT with `$go:string` symbolic types merges into a compiler-stub module via Binaryen `wasm-merge`, compares GC string headers, supports asm→compiler callbacks, and rejects divergent type shapes. Design: [`docs/design/12-wasm-assembly.md`](../docs/design/12-wasm-assembly.md). |

The JSPI run on v26.13.1 printed:

```
ok  direct call without promising: SuspendError: trying to suspend without WebAssembly.promising
ok  i32 local survives suspend = 42
ok  struct ref local survives suspend = 7
ok  local survives two suspends = 11
ok  parent restores g after child clobber = 1
ok  child ran and logged the clobber = true
ok  child was a macrotask = true
ok  ready-before-park result = 5
ok  ready-before-park did not return a Promise = "value"
ok  import can return a wasm struct ref = 5
ok  externref round-trip = true
ok  suspending import can return an externref = "from-js"
ok  rejected promise enters the wasm activation = true
ok  main promise reaction runs before spawn macrotask: microtask:main-then microtask:awaited macrotask:exit
```
