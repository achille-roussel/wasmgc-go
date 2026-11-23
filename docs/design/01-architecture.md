# 01 — Architecture

The compiler is one process. It loads every package the program reaches, type-checks it, lowers it to one structured IR, and emits one Wasm module. There is no object file and no separate linker in v1. The wasm3 linker existed because `cmd/compile` emits one object per package and the linker concatenated per-function type tables; that concatenation is why channel slot arrays were stored as `anyref` and cast at every access (`docs/wasm3-study/01-type-mapping.md`, `collectChanStruct`). A compiler that owns the module interns each Wasm type once.

## Pipeline

```
go list -json -deps          module graph, file names
        │
        ▼
build-tag re-filter          GOOS=js, GOARCH=wasmgc, tag wasmgc
        │
        ▼
manifest substitution        rewrite / overlay / vendor  (06)
        │
        ▼
go/parser + go/types         types.Config.GoVersion from the package
        │                    Sizes: WordSize 4, MaxAlign 4
        ▼
unsafe and feature check     05, before any IR
        │
        ▼
monomorphize                 generic functions and generic methods
        │
        ▼
lower to IR                  structured regions, Go-level instructions
        │
        ▼
IR passes                    escape, devirtualize, inline, dead code
        │
        ▼
lower runtime ops            maps, chans, panic, convT become primitives
        │
        ▼
codegen                      Wasm instructions, still structured
        │
        ▼
emit                         one module, interned types, JS glue
```

Each stage reads and writes a value the next stage can test without the previous one. The IR text format is the contract between lowering and codegen. The import list in `04-runtime-and-abi.md` is the contract between codegen and the runtime. The type shapes in `03-type-mapping.md` are the contract both of them share with the runtime's Go types.

### Loading

`golang.org/x/tools/go/packages` calls `go list`. `go list` rejects an unknown `GOARCH`, and `GOARCH=wasm` is the wrong arch for this compiler: it selects `*_wasm.go` and the assembly stubs in `internal/bytealg` (`indexbyte_native.go` is tagged `wasm`, `indexbyte_generic.go` is tagged `!wasm`). On Go 1.27.4 every official `GOARCH` selects the native bytealg file.

The loader therefore uses `go list` only as a module-aware file inventory:

1. Run `go list -json -deps -tags=wasmgc` with `GOOS=js` and `GOARCH=wasm`. The JSON includes `GoFiles` and, when non-empty, `IgnoredGoFiles` (confirmed with Go 1.22's `go list` on `internal/bytealg`, and the field is still part of `go list -json`).
2. Re-evaluate every candidate file with `go/build/constraint` under `{GOOS: js, GOARCH: wasmgc, BuildTags: [wasmgc, ...user tags]}`. Filename suffixes follow the same rule: `*_js.go` and `*_wasmgc.go` stay, `*_wasm.go` and `*_amd64.go` go. A file tagged `!wasm` comes back. A file tagged `wasm` drops. Practical consequence (stream H): packages that only shipped arch bits as `*_wasm.go` (`internal/cpu`, `internal/goarch`) need a `*_wasmgc.go` twin in `std/overlay`, or they lose `CacheLinePadSize` / `GOARCH` constants.
3. Apply the stdlib manifest (`06-stdlib-strategy.md`). A rewritten package replaces the whole file list. An overlay replaces named files. A deleted file (upstream path mapped to empty) disappears.
4. Parse with `go/parser` and type-check with `go/types`. Do not use export data, and do not use the type check `go list` did.

`go/types.Config.Sizes` is `StdSizes{WordSize: 4, MaxAlign: 4}`. Those sizes are the `unsafe.Sizeof` fiction of `05-unsafe-and-reflect.md`. They are not field offsets in a Wasm struct. The lowerer never calls `Sizes.Offsetsof` to compute a `struct.get` immediate; it uses the field index in the Wasm struct type from `03-type-mapping.md`. wasm3 had the opposite bug available: `go/types.StdSizes` still reported a string as two words while the backend passed one reference (`docs/wasm3-study/03-stdlib-and-unsafe.md`). One size table, used only for the three `unsafe` builtins, removes that split.

Cgo files are a compile error naming the file. Assembly files that survive the filter are a compile error naming the file and the package; the materializer is supposed to have dropped them before a user package is compiled. A user package that ships only a `.s` implementation of a function fails here, on purpose.

### The check before IR

The checker walks the type-checked AST. It rejects the `unsafe` operations in `05-unsafe-and-reflect.md`, and it warns on `int64`/`uint64` to `int`/`uint` conversions (`00-overview.md`). Doing this in SSA, which is where wasm3 emitted `wasm3: conversion between unsafe.Pointer and uintptr is not supported`, produces the right failure and a bad location (`docs/wasm3-study/03-stdlib-and-unsafe.md`). The diagnostic cites the source expression. The reflect-constructor check is not this pass. It needs whole-program reachability, so it runs after dead code, below.

### Monomorphization

A generic function or a generic method becomes one IR function per instantiation that the program reaches. The instantiation is a concrete type list. Identical instantiations share one function. A generic method is not entered in an itable; a call on a concrete receiver monomorphizes, and a call through an interface is an ordinary method call whose signature has no type parameters.

Dictionaries are not a mechanism we will add on a guess. They erase the value to `anyref` and put a type record beside it, which fights the reason to use Wasm GC. Achille confirmed monomorphization on 2026-11-22. A later size report can still force a dictionary for a named hot function; that would be a new design change, not an open default.

### IR and passes

`02-ir.md` specifies the IR. The passes, in order, and the reason each exists:

| Pass | Effect |
|---|---|
| Verify | Types and regions match the grammar. Temps do not cross regions. |
| Escape | Rewrites a non-escaping `&` to a direct access. Spills an escaping scalar local to a one-element array. Correctness does not depend on this pass: the spill lowering is valid for every `&`. |
| Devirtualize | An interface call whose concrete type is a single reaching type becomes a direct `call`. Whole-program information is what makes this common. |
| Inline | Small leaf functions, including the runtime helpers that are one Wasm instruction (`array.copy`, `array.len`). Budget: a function of at most 20 IR instructions, not recursive, called from one site or marked `//go:wasmgc inline`. |
| Dead code | Drop functions, methods, and types not reached from `main`, from a `//go:wasmexport`, or from a live itable. An itable is live only when some live conversion or type assertion mentions that pair. |
| Reflect constructors | On the functions dead code kept, run the `reflect.Type` flow in `05-unsafe-and-reflect.md`. A reachable `StructOf`, `FuncOf`, `ArrayOf`, `ChanOf`, `MapOf`, `SliceOf`, or `PointerTo` whose result is not in the universe is a compile error. |
| Lower runtime | Replace `map.get`, `chan.send`, `panic`, slice `append` with the primitive sequences in `04-runtime-and-abi.md`. After this pass the only calls are real functions. |
| Verify again | Codegen's input. High-level instructions are gone. |

There is no register allocation. wasm3 turned Go's allocator off and assigned one Wasm local per SSA value, because a 16-register ABI duplicates the engine's JIT (`docs/wasm3-study/02-runtime-and-abi.md`, `doc/wasm3-m3-no-regalloc.md` in that tree). This IR allocates a Wasm local when it creates a `var`, and a temp that is used once in the same straight-line region stays on the Wasm stack. There is nothing to allocate onto.

There is no relooper on the structured path. Go's SSA throws away the AST's nesting, and wasm3 then rebuilt `block` / `loop` / `br` from the CFG, with a `br_table` dispatch fallback for irreducible functions (`docs/wasm3-study/02-runtime-and-abi.md`). The lowerer emits the nesting it was given. `goto` that is not nested is the one case that becomes a dispatch loop, and it is confined to that function (`02-ir.md`).

### Codegen and emission

Codegen walks regions in source order and emits Wasm instructions. A `block` region is a Wasm `block`, a `loop` region is a Wasm `loop`, an `if` region is a Wasm `if`, a dense `switch` is `br_table`. The mapping is in `02-ir.md`. The binary writer in `internal/wasm` is the only place that knows LEB128.

The module, in order:

| Section | Contents |
|---|---|
| Type | Interned rec groups. Prelude from `03-type-mapping.md`, then program types. One table for the module. |
| Import | The `go` module, `07-host-and-js.md`. |
| Function | Type index per function. |
| Table | Only if a `call_indirect` survives. v1 uses `call_ref` and may emit an empty table. wasm3 kept a table for leftover trampolines. |
| Memory | One linear memory, minimum 1 page, for the host byte bridge. Not the Go heap. |
| Tag | One tag, `$go.panic`, type `(param (ref $go.panicObj))`. No tag is emitted for a program that cannot panic; a program with no `panic`, no index check, and no nil check still has index checks, so the tag is present as soon as any check exists. An empty `main` that cannot trap for a language reason omits it. wasm3 emitted a continuation tag in every module (`docs/wasm3-study/00-overview.md`). |
| Global | `$g` the current goroutine, the linear-memory bump pointer, one global per type descriptor and per itable, reference-typed package variables. No `CTXT` global. |
| Export | `run`, `_sched`, `memory`, each `//go:wasmexport`. |
| Start | None. The host calls `run`. |
| Element | `ref.func` declarations. |
| Code | Bodies. |
| Data | Passive data segments for string and `[]byte` literals, consumed by `array.new_data` during init, then `data.drop`. |

Heap-type immediates (`ref.cast`, `ref.null`, `struct.get`'s type index, block results) are signed LEB128. A type index of 64 encoded as the single byte `0x40` is the signed value −64, and the validator reports an unknown heap type. wasm3 hit this (`docs/wasm3-study/02-runtime-and-abi.md`). The binary writer has a test that round-trips a type index above 63.

The name section is a custom section and is omitted unless `-names` is set or the command is `run` or `test`. There is no pclntab and no `runtime.moduledata`. The M2 audit found file paths in the data section were most of the remaining bytes (`docs/wasm3-study/04-open-problems.md`). Source positions live in the name section and in a side file `prog.wasm.map` that `run` can print from, not in the module a browser downloads.

### Why one module

Wasm GC types are nominal and closed. An itable for `(error, *os.PathError)` has to exist exactly when that conversion is reachable, and it has to be one global so that type identity is `ref.eq` on the descriptor. Separate compilation would reintroduce a linker that merges type sections and deduplicates descriptors, which is the `mergeTable` machinery wasm3 needed and the reason its channel buffer was `anyref`. Whole-program compilation is also the dead-code elimination that keeps the binary small: a method that no live type's live interface mentions is not emitted, and a type that is never allocated is not in the type section.

The cost is compile time. It is the same cost TinyGo pays, and it is the right cost for a compiler whose point is the size of the module. A cache of lowered IR per package, keyed by the source hash and the instantiation set, is a later build-speed project. It is not a v1 interface. Workstreams in `09-bootstrap-plan.md` share the IR text and the import list, not object files.

## Package layout

The module path is `github.com/achille-roussel/wasmgc-go`.

| Path | Owns | Does not own |
|---|---|---|
| `cmd/wasmgc-go` | flag parsing, subcommands, process exit codes | type checking, Wasm bytes |
| `internal/load` | `go list`, the re-filter, reading the manifest | diagnostics about `unsafe` |
| `internal/check` | the pre-IR diagnostic | lowering |
| `internal/ir` | the IR data structure, parser, printer, verifier | knowledge of `go/ast` |
| `internal/mono` | instantiation | Wasm types |
| `internal/lower` | AST to IR | Wasm opcodes |
| `internal/escape` | the escape pass | codegen |
| `internal/codegen` | IR to a Wasm module value | LEB128 details beyond what `internal/wasm` exports |
| `internal/wasm` | binary encoding, validation-by-construction of sections | Go types |
| `std/src/runtime` and the other rewrites | the Go runtime the generated code calls | the compiler |
| `lib/wasmgc.js` | imports the module expects | anything about IR |

`internal/ir` imports nothing from `internal/lower` or `internal/codegen`. `internal/wasm` imports nothing from `internal/ir`. That split is what lets the encoder and the runtime be built before the lowerer exists (`09-bootstrap-plan.md`).

## Host output

`build -target node` writes `prog.wasm` and `prog.wasm.js`. The JS file is `lib/wasmgc.js` with the list of imports the module actually uses filled in, so a program that does not park does not mention `WebAssembly.Suspending`. The study's glue always constructed `Suspending`, and Node 22 could not load it (`docs/wasm3-study/00-overview.md`). A module with no suspending import runs on any engine with Wasm GC, including Node 22.

`run` executes `node prog.wasm.js [args]`. The glue passes `process.argv` after the program name as `runtime.args`.
