# 08 — Testing

Tests are part of the compiler, not a later project. The wasm3 branch had canaries and notes in `doc/wasm3-*.md` and no suite that ran in CI. Several of those notes described behavior the code had already left behind (`docs/wasm3-study/04-open-problems.md`, the stale-doc list). A test that parses the IR text or instantiates a module does not go stale in the same way.

Four layers, in the order a change runs them.

## 1. IR goldens

`internal/ir` tests round-trip the text format: parse, verify, print, byte-compare. Those tests do not need a Go front end.

`internal/lower` tests compile a Go snippet and compare `dump-ir` to a file in `testdata/ir/`. One file per language construct that has a lowering rule in `02-ir.md`: `if`, `for`, `switch` with `fallthrough`, `goto` forward, `goto` irreducible (the dispatch form), `defer`, `range` over a slice, `range` over a function, a closure, an interface call, a type switch, a map literal, a channel send. The comparison is exact, including temp names, so a printer change is a deliberate update.

A golden that still contains a high-level instruction after the runtime-lowering pass is a failing test. That is how "the pass deleted `map.get`" stays true.

## 2. WAT goldens and validation

`internal/codegen` tests compile a snippet to WAT text (the binary writer's disassembly, or `wasm-tools print`) and compare. The interesting modules are small: a function that returns `i32`, a string header built with `array.new_data`, a fat-pointer load, an interface call, a `try_table` around a `defer`.

Every emitted module is passed to `wasm-tools validate`. A module that does not validate is a test failure even if the bytes match a golden, so a golden cannot freeze a bug the validator would have caught. The type-index-64 encoding has its own test: a module with 64 types, a `ref.cast` of the last, decoded back, with the immediate not equal to the single byte `0x40`. That is the wasm3 bug (`docs/wasm3-study/02-runtime-and-abi.md`).

`prototypes/` is the bottom of this layer, already run:

| Prototype | Node 26.13.1 | Node 22.23.4 |
|---|---|---|
| `prototypes/eh-panic` | pass | pass |
| `prototypes/fat-pointer` | pass | pass |
| `prototypes/jspi-sched` | pass (includes `run_glue.mjs` vs `lib/wasmgc.js`) | exits: no `WebAssembly.Suspending` |
| `std/src/runtime/testdata/run_m7.mjs` | pass (channels, select, timer) | skips: no Suspending |

They are re-run by `node prototypes/eh-panic/run.mjs` and the two siblings. `wasm-tools` 1.263.0 parsed them. wabt 1.0.42's `wat2wasm` does not recognize `struct.get`, so the suite depends on `wasm-tools`, not on wabt.

## 3. Execution tests

A Go program in `testdata/run/`, compiled by the driver, run under Node, stdout and the exit code compared. The first set is the study's run table (`docs/wasm3-study/00-overview.md`), with the output corrected where the study found a bug:

| Program | Expected |
|---|---|
| empty `main` | exit 0 |
| `println("hello, world")` | `hello, world\n` |
| `println(fib(20))` | `6765\n` with no extra NUL |
| slice literal, append, print len and last | `4\n4\n` with no extra NUL |
| `map[int]int` insert, delete, print | `2\n20\n30\n` |
| `map[string]int` | the inserted pairs, in an order the test sorts before printing |
| `defer` print | `hi` then `bye` |
| buffered chan, `go` send, receive | `7\n` |
| unbuffered chan across `go` | the value |
| two waiters on one chan | both values, in send order |
| `select` default | the default arm |
| `select` that parks, then a send from another goroutine | the sent value |
| `recover` in a deferred closure, panic in the callee | the recovered value, and the function returns |
| unrecovered panic | the value on stderr, exit 2 |
| `println` of `-1` and of `0` | the digits, no extra bytes |

Map iteration tests sort. They do not freeze an order.

JSPI tests skip when `WebAssembly.Suspending` is missing, and CI runs them on Node 26. The non-JSPI tests also run on Node 22, which is the check that a program that does not park stayed free of the JSPI import.

## 4. The Go suite and the library tests

`$GOROOT/test` is the language suite, driven by `testdata/gotest/run.go` once the driver can compile a file. Each test is one of:

- `run`, compile and execute, compare output.
- `errorcheck`, compile and match `// ERROR` patterns. Our diagnostics use the prefix `unsupported unsafe operation`, so the errorcheck files we claim to pass are ones we have a pattern for, plus the ones that fail in `go/types` the same way upstream does.
- `skip`, a line in `testdata/gotest/skip.txt` with the reason (`cgo`, `uintptr(unsafe.Pointer)`, `assembly`, `reflect.StructOf`). A skip without a reason fails CI. The `StructOf` skips are the json and gob tests named in `05-unsafe-and-reflect.md`. The production `encoding/json` and `fmt` packages are not skipped: their constructor calls are in the closed universe, and a test compiles `json.Marshal` of a `map[int]string` to prove the `SliceOf` / `MapOf` sites were accepted. A separate compile-fail test is a three-line program that calls `reflect.StructOf` on a non-constant field list and expects the diagnostic in `05`.

The goal is not "all of `$GOROOT/test` on day one". The bootstrap plan names the milestone that turns the skip list red if it grows. Until that milestone, the list is allowed to be long and is committed.

Standard-library tests run as `wasmgc-go test` on the vendored packages, in the order `06-stdlib-strategy.md` lists them. `errors`, `strconv`, `strings`, `bytes`, `sort`, `slices`, `fmt`, `encoding/json`, `time` are the ones whose pass is a milestone. Tests that need the network, the filesystem, or `exec` are skipped by the package's own build tags once `GOOS=js` is in force, and by `skip.txt` where that is not enough.

## Size tracking

`testdata/size/corpus.txt` lists programs. CI compiles each with `-names` off and records section sizes in `testdata/size/sizes.txt`. A commit that changes a size updates the file in the same commit. The budgets from `00-overview.md` are hard failures:

- empty `main` over 2048 bytes
- builtin println hello over 8192 bytes
- `fmt.Println("hello")` over 131072 bytes, once that program compiles

The report prints the section breakdown (type, code, data, custom) so a regression that is "the name section came back" is visible. wasm3's empty main at HEAD carried a tag section it did not use and 922 bytes of data (`docs/wasm3-study/00-overview.md`). The size test is where that kind of leftover gets caught.

`-opt` is not on in the size run. A separate, non-blocking job runs `wasm-opt` when the binary accepts the module and records the number. A `wasm-opt` that errors on Wasm GC does not fail CI.

## What CI runs

On every change, in order: IR goldens, `wasm-tools validate` on the WAT goldens, the execution tests that do not need JSPI, the prototypes, `wasmgc-go std check` against the pinned GOROOT. JSPI execution tests run on Node 26. The size corpus runs once the driver can build the empty main, and not before; until then the job is absent rather than green.

`std check` needs a Go 1.27.4 source tree. CI downloads the tarball and caches it by the pin string. A pin bump without a green `std check` does not merge.
