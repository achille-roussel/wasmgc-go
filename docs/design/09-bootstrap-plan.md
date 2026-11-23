# 09 — Bootstrap plan

The compiler is built in milestones. Each one has a visible exit and leaves the tree compiling. Workstreams are the boundaries from `01-architecture.md`: a stream owns a directory, and the only things it shares with another stream are the IR text format (`02-ir.md`), the import list and runtime symbols (`04-runtime-and-abi.md`), and the type shapes (`03-type-mapping.md`). Two streams that share a directory are one stream.

Nothing here depends on a calendar. The order is the dependency order.

## Milestones

### M0 — the module is empty and valid

**Scope.** `internal/wasm` writes a module with a type section, a function that returns, and an export `run`. `lib/wasmgc.js` instantiates it and calls `run`. No Go.

**Exit.** `node` on the glue, on Node 22 and Node 26, prints nothing and exits 0. `wasm-tools validate` accepts the bytes. A type index of 64 survives a round trip (the signed-LEB test from `08-testing.md`).

**Artifact.** `internal/wasm.EmptyRunModule` encodes the module; `testdata/m0/empty.wasm` is the checked-in bytes. `node lib/wasmgc.js testdata/m0/empty.wasm` is the host smoke test. CI runs `go vet`, `go test ./...`, `wasm-tools validate`, that Node smoke test on **22.23.4** and **26.13.1**, and the EH/fat-pointer prototypes on both (JSPI prototype on 26 only). Node 26 is the newest line with unflagged JSPI used here; Node 25 would also work for JSPI.

**Streams.** Wasm encoder (A), JS glue (B). No dependency on each other beyond the export name `run`.

### M1 — empty `main`

**Scope.** The driver, the loader, `go/parser`, `go/types` with `StdSizes{4, 4}`, the IR verifier and text format, the lowerer for a function whose body is empty or returns a constant, codegen of `i32` consts and `return`. `runtime` is an init function and `main` call, no print.

**Exit.** `wasmgc-go run` of `package main; func main() {}` exits 0. The module is under 2048 bytes with `-names` off. `dump-ir` of that program matches a golden.

**Streams.** IR (C) and the encoder (A) can land before the lowerer (D). The driver (E) wires them at the end of the milestone. C publishes the text grammar and a parser test before D starts.

### M2 — `println`

**Scope.** Scalars, locals, `if`, `for`, calls, string headers, `array.new_data`, the linear bridge, `runtime.printstring` and `printint`. The integer printer slices only the digits.

**Exit.** The println rows of the execution table in `08-testing.md` pass, including `fib(20)` with no extra NUL and `hello, world`. Size of the hello program is under 8192 bytes. Node 22 runs it, because nothing suspends.

**Streams.** Lowerer (D) for expressions and structured statements. Runtime print and the bridge (F), tested with a hand-written WAT module that calls the same imports, before the lowerer emits them. Glue (B) grows `runtime.write`.

### M3 — slices, structs, pointers

**Scope.** The slice and struct mapping, immutable headers, `append`, fat pointers, the spill of an escaping `&local`, escape analysis for the non-escaping case. `new`, composite literals, method calls on concrete types.

**Exit.** Execution tests: append, a struct passed by value (the callee's field write does not change the caller), `&s.f` stored in a global and written through, `new(int)` written through. The fat-pointer prototype stays green. An IR golden shows a non-escaping `&s.f` as `struct.get` with no `struct.new` of a pointer.

**Streams.** Lowerer (D) and codegen (A') split the fat-pointer work at the IR: D emits `addr` / `deref`, the escape pass rewrites them, A' emits `call_ref`. The escape pass can land in C's package as a pass that only sees IR, with D supplying the addresses.

### M4 — interfaces and type descriptors

**Scope.** Descriptors as globals, itables, conversions, calls, assertions, type switches, embedding. `internal/reflectlite` enough for `errors`. Comparability of interfaces.

**Exit.** A value stored in an `error` and compared to `nil` works. A type switch on a concrete type and on `default` works. A method promoted through an embedded struct is callable through the interface. `errors.New` and `fmt.Errorf`-free wrapping (`errors.Join` can wait) link. A reachable `reflect.StructOf` on a non-constant field list is a compile error with the diagnostic in `05-unsafe-and-reflect.md`. A call in a function that nothing reaches is not.

**Streams.** Codegen of `call_ref` (A') is already required by M3. The descriptor generator is a new stream (G) that consumes the same type intern table as codegen. G does not lower function bodies.

### M5 — defer, panic, recover

**Scope.** The `try` region, `runtime.deferRun`, the `recoverOk` prologue, unrecovered panic printing the value and exiting 2, `Goexit`.

**Exit.** The panic rows of `08-testing.md`. A test with two goroutines is not in this milestone; defer is per frame, and M5 runs single-goroutine. The EH prototype stays green.

**Streams.** Runtime (F) owns `deferRun` and can test it with WAT that throws the tag. The lowerer's defer pass (D) is the only compiler change. They meet at `runtime.deferRun` and the tag's type, both in `04`.

### M6 — maps

**Scope.** The open-addressed map, per-type hash and equal, seed, tombstones, iteration snapshot, NaN keys, interface keys, delete during range.

**Exit.** The map rows of `08-testing.md`. A test that a NaN key is not found. A test that two maps with the same keys iterate from different seeds often enough that a fixed-order implementation fails the test (run the iterator start index through `fastrand` and check it is not a constant).

**Streams.** Runtime map bodies (F) and the generated hash functions (D). F's tests pass keys as `i32` and `string` through a WAT driver first, then D replaces the driver.

### M7 — goroutines, channels, select, timers

**Scope.** JSPI park/ready/spawn, `$g` save and restore, waiter queues, the channel protocol, `select`, deadlock detection, `time.Sleep`, `sync.Mutex` and `WaitGroup`.

**Exit.** The channel and select rows of `08-testing.md`, including two waiters. A test that `main` returning does not run a queued goroutine (the macrotask ordering). A test that a parked `main` does run it. A deadlock test. `time.Sleep` of a few milliseconds returns. `sync.Mutex` under two goroutines, one of which parks. The JSPI prototype stays green. This milestone requires Node 25+.

**Streams.** Glue (B) and runtime park (F) land together and are tested by extending `prototypes/jspi-sched`, before the lowerer emits `go`. The channel algorithm is F, with payload moves supplied by D as generated functions. Select is D's rewrite plus F's `runtime.selectGo`. The split is the symbol list in `04`.

### M8 — `fmt.Println`

**Scope.** `reflect` for the operations `fmt` uses, the overlays for `sync` and `slices`, vendored `strconv`, `unicode/utf8`, `strings`, `bytes`, `fmt`.

**Exit.** `fmt.Println("hello")`, `fmt.Println(42)`, `fmt.Println(struct{ A int }{3})`, and `fmt.Printf("%v", map[string]int{"b": 2})` produce the upstream text, modulo map order which the test sorts. Size of `fmt.Println("hello")` is under 128 KiB. `wasmgc-go test errors strconv strings bytes` passes, with skips only for tests that the upstream file itself skips on `GOOS=js`. A program that calls `reflect.SliceOf` and `reflect.MapOf` the way `encoding/json` does (`SliceOf` of a type in the program, `MapOf` of that type with `struct{}`) compiles. A program that calls `reflect.StructOf` on a built field list does not.

**Streams.** Reflect (G) and the stdlib materializer (H) start as soon as M4's descriptors exist. They do not wait for M7. `fmt` of an int does not need channels. H's first deliverable is `std check` on the pin, which can land before any of this compiles.

### M9 — JSON, time, os, fetch

**Scope.** Vendored `encoding/json`, `time` wired to the host clock and timers, `os` for stdio and args, the `net/http` overlay that does `fetch`.

**Exit.** Marshal and unmarshal of a struct with a `json` tag, of `map[string]int`, and of `map[int]string`. The `SliceOf` and `MapOf` calls in `encoding/json/v2/arshal_default.go` are accepted by the constructor pass with no overlay (`05-unsafe-and-reflect.md`). `BenchmarkTypeFieldsCache` is a compile error and is skip-listed. `time.Now` moves forward across a `Sleep`. `os.Args` matches the process arguments. `os.Write` on a file returns an error. An HTTP `Get` of a local test server (Node's `http`) returns the body. `ListenAndServe` is not part of the exit.

**Streams.** H owns the overlays. F owns timers, which M7 already started. The HTTP overlay is H, using `syscall/js` from the glue stream (B). B can land `externref` imports in M2's glue without waiting, and the prototype already covers the import returning `externref`.

### M10 — the suites

**Scope.** `$GOROOT/test` runner, `skip.txt` with reasons, stdlib tests for every package marked done, the size corpus enforced.

**Exit.** CI runs the four layers from `08-testing.md`. The skip list is reviewed in the milestone's change and does not contain a test that actually passes. Growing the skip list fails CI from this milestone on.

## Workstreams

| Stream | Directory | Starts after | Publishes |
|---|---|---|---|
| A Wasm encoder | `internal/wasm` | nothing | module bytes, the LEB test |
| B JS glue | `lib/wasmgc.js` | nothing | the import names in `07` |
| C IR | `internal/ir` | nothing | the text grammar and the verifier |
| D Lowerer | `internal/lower`, `internal/mono`, `internal/escape` | C | IR text for each milestone's language subset |
| A' Codegen | `internal/codegen` | A and C | WAT for that IR |
| E Driver | `cmd/wasmgc-go`, `internal/load`, `internal/check` | C, and the loader can start alone | `wasmgc-go` subcommands |
| F Runtime | `std/src/runtime` | B's import names, which are frozen in `04` and `07` now | the symbols in `04` |
| G Descriptors and reflect | `std/reflect`, `std/internal/abi`, the descriptor half of codegen | the type shapes in `03`, M4's itable layout | `reflect.Type` of a struct |
| H Stdlib materializer | `internal/stdlib`, `std/manifest.json`, `std/overlay` | nothing; `std check` needs only the checker | a cache GOROOT and a green `std check` |

A, B, C, F, and H start together. D waits on C's grammar and nothing else; it can lower to text and test goldens before A' exists. A' waits on A and C. E waits on a lowerer that can produce an empty `main`, which is M1. G waits on the type intern table having the operations codegen needs, which is the end of M3. M8's `fmt` waits on G and H. M7's channels wait on F and on D's ability to emit a call, which exists at M2.

The rule when a stream needs a change in a contract: change `02`, `03`, `04`, or `07` in the same commit as the code, and update the prototype or the golden the contract names. A stream does not invent an import.

## The first commit of code

M0 and the IR parser are the first code, and they are independent. The execution tests of M2 are the first time the whole pipeline runs. Until that exists, a prototype in `prototypes/` is the execution test for the runtime and the glue, which is why F and B can move before E does.
