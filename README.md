# wasmgc-go

A standalone Go compiler whose only backend is WebAssembly GC. Go values are Wasm struct and array references, and the host engine collects them. The compiler does not embed the Go garbage collector, allocator, or linear-memory goroutine stacks, which is where the binary-size win comes from.

The language front end is `go/parser`, `go/types`, and a module-aware file list from the `go` command. The compiler has its own IR, shaped for structured control flow and typed references, and one Wasm GC backend. It is a new toolchain in the spirit of TinyGo, not a `GOARCH` inside `cmd/compile`.

The design is in [`docs/design/`](docs/design/). The measurements and dead ends of the earlier `GOARCH=wasm3` experiment are kept, unchanged, in [`docs/wasm3-study/`](docs/wasm3-study/).

| Doc | What it decides |
|---|---|
| [00 Overview](docs/design/00-overview.md) | Goals, size and language targets, CLI |
| [01 Architecture](docs/design/01-architecture.md) | Pipeline, repository layout, why one module |
| [02 IR](docs/design/02-ir.md) | Structured IR, lowering, text format |
| [03 Type mapping](docs/design/03-type-mapping.md) | Go types as Wasm GC types |
| [04 Runtime and ABI](docs/design/04-runtime-and-abi.md) | Calls, panic, goroutines, channels, maps |
| [05 unsafe and reflect](docs/design/05-unsafe-and-reflect.md) | The supported subset, and reflection |
| [06 Standard library](docs/design/06-stdlib-strategy.md) | What is rewritten, vendored, or overlaid |
| [07 Host and JS](docs/design/07-host-and-js.md) | Imports, the JS glue, JSPI, Node versions |
| [08 Testing](docs/design/08-testing.md) | IR goldens, Node execution, size tracking |
| [09 Bootstrap plan](docs/design/09-bootstrap-plan.md) | Milestones and parallel workstreams |
| [10 Resolved decisions](docs/design/10-open-questions.md) | The six calls Achille made on 2026-11-22 |

[`prototypes/`](prototypes/) holds three hand-written modules that the design relies on: exception handling for panic, a fat pointer for scalar and element pointers, and a JSPI scheduler. They were run on Node.js 26.13.1; the exception and pointer modules also run on Node.js 22.23.4.

## Status

Milestone **M0** is in tree: `internal/wasm` encodes Wasm GC modules, `lib/wasmgc.js` instantiates them, and `testdata/m0/empty.wasm` is an empty `run` export accepted by `wasm-tools` and by Node 22 / 26.
