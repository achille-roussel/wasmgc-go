# 12 — Hand-written Wasm escape hatch

**Status.** Adopted. Achille decided 2026-11-23 ([`10-open-questions.md`](10-open-questions.md) §§8–12). Prototype evidence in [`prototypes/wasm-asm/`](../../prototypes/wasm-asm/).

**Question.** Can wasmgc-go let hand-written `.wasm` / `.wat` files be included in compilation for performance-sensitive or runtime routines — an escape hatch analogous to Go's assembler (body-less Go declarations, `TEXT` symbols, ABI0 / ABIInternal wrappers, `go vet asmdecl`, build tags / `GOARCH` file suffixes, `//go:linkname`, `//go:noescape`, `NOSPLIT`, and friends)?

**Answer.** Yes. First-class but tightly scoped **Wasm assembly**: official WAT text (finished-proposal dialect), bound to body-less Go funcs under the ABI in [`03-type-mapping.md`](03-type-mapping.md) and [`04-runtime-and-abi.md`](04-runtime-and-abi.md), merged into the single module the compiler already emits. No tool-conventions object / `wasm-ld` model. WAT only in v1 (no checked-in `.wasm`). Compiler intrinsics for 1–3 opcode helpers; WAT for multi-instruction kernels (bytealg, hashing, later linear-bridge SIMD).

This revises the non-goal in [`00-overview.md`](00-overview.md) that rejected "assembly (`.s` files)". Native Go `.s` stays rejected. Wat / wasm assembly is a different mechanism.

---

## Analogy to Go's assembler

Primary sources: [A Quick Guide to Go's Assembler](https://go.dev/doc/asm), [`cmd/compile` function directives](https://pkg.go.dev/cmd/compile#hdr-Function_Directives), [`golang.org/x/tools/go/analysis/passes/asmdecl`](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/asmdecl), [ABIInternal notes](https://go.dev/src/cmd/compile/abi-internal) (hand-written asm is ABI0; ABIInternal asm without `NOSPLIT` is rejected by `cmd/asm`).

| Go assembler | wasmgc-go Wasm assembly |
|---|---|
| Body-less `func` in `.go`; body in `.s` | Body-less `func` in `.go`; body in `.wat` |
| `TEXT pkg·name(SB),NOSPLIT,$0-16` | `(func $pkg.name …)` exported or named to match the Go symbol |
| Frame / arg size checked by `asmdecl` | Wasm signature checked by **wasmdecl** against the ABI lowering of the Go decl |
| `ABI0` vs `ABIInternal` wrappers | One ABI only: the Wasm signature of [`04`](04-runtime-and-abi.md). No register ABI, no stack frame |
| `NOSPLIT` / `//go:nosplit` | No Go stack. Optional `//go:wasmgc nosuspend` = must not call a suspending import (see §2) |
| `//go:noescape` | Same directive: escape analysis treats pointer / ref args as non-escaping |
| `//go:linkname` | Same: alias a Go name to an asm or runtime symbol (requires `unsafe`) |
| `*_amd64.s`, `//go:build` | `*_wasmgc.wat`, `//go:build wasmgc` (and optional `simd` / feature tags) |
| `go vet asmdecl` | `wasmgc-go` built-in wasmdecl (and `std check` for vendored asm) |

What does **not** transfer: stack maps, morestack, SP/FP offsets, `NOSPLIT` spare-space rules, leaf-vs-wrapper ABI splits. Wasm has no Go stack. The useful transfer is the **binding model** (declaration without body) and the **signature check**.

---

## 1. Source format

### Source format: WAT text in v1 (decision 10)

| Format | Role |
|---|---|
| **`.wat` text** | **v1 source.** Diffable, reviewable; name-based types; symbolic `$go.…` refs |
| **`.wasm` binary** | **Not in v1** (decision 10). A later release may accept decoded binaries with wasmdecl; do not check them into `std/` now |

**Adopt `.wat` only for v1.**

### Dialect

Official WebAssembly text format for **finished proposals** only ([finished proposals list](https://github.com/WebAssembly/proposals/blob/main/finished-proposals.md), including GC and exception handling in spec 3.0). No custom instruction mnemonics. No Binaryen S-expression extensions that are not in the WAT grammar.

Allowed feature set matches what codegen emits ([`01-architecture.md`](01-architecture.md), [`02-ir.md`](02-ir.md)): GC aggregates, typed refs, `call_ref`, exception handling (`try_table` / `throw` / `throw_ref`), bulk memory for the linear bridge, and later SIMD (`v128`) when [`11-simd.md`](11-simd.md) lands. Active proposals (JSPI has no Wasm opcodes; WasmFX is out) are not written in asm files.

**Custom surface (preprocessor, not a new Wasm dialect):**

- Symbolic type / global / tag / function names under the `$go.` prefix (or `$go:` before rewrite — see §2).
- Optional `(; wasmgc: … ;)` annotations for wasmdecl metadata. Comments only; stripped before encode.

### Parser: embed one, do not write a toy subset

Compiler binary size is irrelevant ([task constraint](00-overview.md)). Options:

| Parser | Language | GC / EH | Notes |
|---|---|---|---|
| **[watgo](https://github.com/eliben/watgo)** | Pure Go | Aims at all finished proposals; GC + EH in tree | Best fit to embed beside `internal/wasm`. Zero CGO. |
| [wasm-tools `wat`](https://github.com/bytecodealliance/wasm-tools) | Rust | Spec-complete | Shell-out only unless we vendor via CGO / WASM. Prototypes already use the CLI. |
| Hand-written subset parser | Go | Only what we need | Rejected: GC type syntax, rec groups, folded instructions, and EH are not a small grammar. |

**Decided (decision 9).** Embed watgo beside `internal/wasm`. Until it is vendored and wired, shell out to `wasm-tools parse` (prototypes and a temporary driver path). wabt 1.0.42's `wat2wasm` still rejects `struct.get`; do not depend on wabt.

### File naming and build constraints

Mirror Go's rules ([`go help buildconstraint`](https://pkg.go.dev/cmd/go#hdr-Build_constraints)), with `GOARCH=wasmgc`:

| Pattern | Meaning |
|---|---|
| `foo_wasmgc.wat` | Implicit `wasmgc` arch constraint (like `foo_amd64.s`) |
| `foo_js_wasmgc.wat` | `js` + `wasmgc` |
| `//go:build wasmgc` near the top of a `.wat` | Explicit tag; evaluated by the loader |
| `foo.s` / `*_wasm.s` | Still **rejected** (or dropped by the materializer). Not this escape hatch. |

Optional later tags: `wasmgc.simd` (or a user `-tags simd`) once SIMD kernels exist, analogous to `amd64.v2`.

### Discovery (loader and stdlib materializer)

Today ([`01-architecture.md`](01-architecture.md)):

1. `go list -json -deps` inventories Go files (and ignored ones).
2. Re-filter with `go/build/constraint` under `{GOOS: js, GOARCH: wasmgc, BuildTags: [wasmgc, …]}`.
3. Manifest substitution ([`06-stdlib-strategy.md`](06-stdlib-strategy.md)).
4. Assembly `.s` that survives is a compile error.

**Change.**

- `internal/load` also collects `OtherFiles` / directory scan for `*.wat` (and later `*.wasm`) that pass the same constraint evaluation. `go list` reports non-Go files inconsistently across versions; do not rely on it alone — scan the package dir after the Go file set is known, the same way `asmdecl` reads `pass.OtherFiles`.
- Body-less funcs are legal when a matching `.wat` export / named func exists in the same package (or via `//go:linkname`), not only when a `.s` exists.
- Materializer tiers:
  - **Vendor:** copy matching `*_wasmgc.wat` if present (unusual upstream).
  - **Overlay / rewrite:** `std/src/…` and `std/overlay/…` may include `.wat` beside Go.
  - **Generic Go stays default** for `internal/bytealg` until a WAT kernel is added; the re-filter that prefers `!wasm` generics remains load-bearing ([`06`](06-stdlib-strategy.md)).

```
package dir
  indexbyte.go          // body-less decl, //go:noescape
  indexbyte_wasmgc.wat  // implementation
```

---

## 2. Declaration and binding model

### Go side

```go
package bytealg

//go:noescape
func MemEqual(a, b string) bool
```

```wat
;; indexbyte_wasmgc.wat  (after symbolic resolution)
(module
  ;; types come from the compiler prelude — see below
  (func $internal/bytealg.MemEqual (export "internal/bytealg.MemEqual")
        (param $a (ref $go.string)) (param $b (ref $go.string)) (result i32)
    …)
)
```

Binding rules:

1. A body-less Go `func` in a package that has Wasm assembly is an **asm candidate**.
2. The linker looks up a Wasm function whose export name or linker name equals the Go linker symbol (`import/path.Name` or the `//go:linkname` target).
3. If none matches → compile error naming the Go func and the expected symbol (like a missing `TEXT`).
4. If a `.wat` defines an unreferenced function → dead-code elimination may drop it (§3).

`//go:linkname` works as in [`cmd/compile`](https://pkg.go.dev/cmd/compile#hdr-Linkname_Directive): push / pull / export forms. `//go:noescape` is honored by escape analysis for asm calls the same way as for body-less funcs backed by `.s`.

There is no `NOSPLIT`. Use:

```go
//go:wasmgc nosuspend
func memmove(…)
```

Meaning: the body must not call a suspending import (`go.runtime.park`, `gosched`, `js.Await`, …). Enforced by a static call-graph check over the asm module's imports and any Go funcs it calls. **Default in `runtime` and `internal/bytealg`** (decision 11); opt out explicitly if a kernel must park. Elsewhere, opt in.

### ABI the asm function sees

Asm receives **already-lowered** Wasm values. There is no flat ABI and no stack slots ([`04`](04-runtime-and-abi.md)).

| Go type | Wasm parameter / result |
|---|---|
| `bool`, `int8`…`int32`, `int`, `uint`, `uintptr` | `i32` |
| `int64`, `uint64` | `i64` |
| `float32` / `float64` | `f32` / `f64` |
| `string` | `(ref $go.string)` non-null |
| `[]T` | `(ref null $go.slice.T)` |
| `struct` / array value | one `(ref null …)` |
| `*Struct` / `*[N]T` | object pointer ref |
| other `*T` | `(ref null $go.ptr.…)` fat pointer |
| `interface` / `error` | `(ref null $go.iface)` |
| `func` | `(ref null $go.clos.…)` |
| multi-result | Wasm multi-value |
| `complex128` result | two `f64` (same as Go→Wasm) |

String / slice headers are the immutable structs of [`03`](03-type-mapping.md): `$data` is `(ref $go.bytes)` / array ref, `$off` / `$len` / `$cap` are `i32`. Packed element arrays are `(array (mut i8))` / `(array (mut i16))`. Asm that mutates a published string's bytes is undefined behavior the same way as in Go asm that writes through `StringData`.

### Panics and JSPI

- **Panic:** throw tag `$go.panic` with `(ref $go.panicObj)`, or `call` `runtime.throwPanic`. Do not invent a second tag. Uncaught exceptions propagate like Go ([`04`](04-runtime-and-abi.md), `prototypes/eh-panic`).
- **Calling back into Go:** allowed. Asm may `call` any reachable Go or runtime function by linker name once the merge has fused the import, or by direct name if the compiler emits the callee into the same module before asm merge. Prefer named `$runtime.…` / `$pkg.Func` imports that the merger resolves.
- **Yield / park:** only through the normal suspending imports. An asm function marked `nosuspend` must not reach them. An unmarked asm function may call `runtime.gopark` like ordinary Go; JSPI state (`$g` save/restore) is the callee's job ([`04`](04-runtime-and-abi.md)). Asm must not invent its own suspending imports.

### Symbolic references to compiler-defined entities

Wasm GC type indices are **module-global**. Recursion groups are the unit of isorecursive identity ([Wasm 3.0 validation conventions](https://webassembly.github.io/spec/core/valid/conventions.html); [wasmparser canonicalization](https://github.com/bytecodealliance/wasm-tools/pull/1275)). Hand-written asm must not hard-code numeric type indices from a previous compile.

**Mechanism.**

1. Asm source refers to prelude and program types by **stable names**: `$go.string`, `$go.bytes`, `$go.slice.u8`, `$go.iface`, `$go.panic`, `$go.panicObj`, `$g`, …
2. Optional dialect sugar `$go:string` is rewritten to `$go.string` before parse (prototype does this).
3. The asm file may either:
   - **(A) Omit local definitions** of those types and import functions only — not enough alone, because Wasm cannot import a type definition; or
   - **(B) Redeclare identical shapes** (what the prototype does), and let the merger canonicalize by structure; or
   - **(C) Receive a generated prelude** the compiler writes (`go_prelude.wat`) that asm `(include …)` — WAT has no include, so the driver concatenates or the merger injects types.

**Adopt (B)+(C):** the driver injects the compiler's interned prelude (and any program types the asm file names) into the asm module before validate, **or** asm redeclares the prelude shapes and the merger requires structural equality with the compiler's types. Mismatched shapes are a compile error (prototype: Binaryen `wasm-merge` reports a type mismatch on the fused import).

Same naming for:

| Kind | Examples |
|---|---|
| Types | `$go.string`, `$go.bytes`, `$go.slice.i32`, `$go.ptr.i32` |
| Globals | `$g`, `$go.emptyString`, type descriptor / itab globals by symbol |
| Tags | `$go.panic`, `$go.goexit`, `$go.rangeExit` |
| Functions | `$runtime.throwPanic`, `$runtime.printstring` |
| Data | passive segments named `$go.data.…` if asm needs `array.new_data` |

Field immediates (`struct.get $go.string $len`) are part of the ABI contract and belong in a generated `asm_prelude.md` / header comment kept in sync with [`03`](03-type-mapping.md).

---

## 3. Linking (merging into one module)

The compiler emits **one module** ([`01`](01-architecture.md)). "Linking" asm means merging functions into that module, not producing a second instantiateable module.

### What must happen

1. Parse / decode asm → module fragment.
2. Resolve symbolic names to the compiler module's type / global / tag / func indices.
3. **Type canonicalization / rec-group merge:** isorecursive equality at rec-group granularity. Extra types in a group change identity ([wasm-tools notes](https://github.com/bytecodealliance/wasm-tools/pull/1275)). Prefer injecting the compiler's exact type section into asm over hoping two independently written rec groups match.
4. Relocate all type / func / global / tag immediates in asm code.
5. Fuse asm exports to Go body-less decls (and fuse asm imports to compiler / runtime exports).
6. Union host imports (`go` module) that asm declares (e.g. a linear-bridge helper).
7. Dead-code eliminate asm functions not reached from `main`, `//go:wasmexport`, live itables, or a body-less Go decl that survived DCE.
8. Validate the merged module.

### Existing tooling vs `internal/wasm`

| Tool | Role | Fit |
|---|---|---|
| **Binaryen [`wasm-merge`](https://github.com/WebAssembly/binaryen)** | Fuse imports↔exports by module name; rewritten for WasmGC (v113+) | **Works** for the prototype. Needs `--enable-gc --enable-reference-types` (and often multivalue). **Temporary optional path for M6 hash kernels** (decision 12). Not a permanent required dependency. |
| **wasm-tools** | parse / validate / print | Prototypes, CI, and the temporary parse shell-out (decision 9). No `$go.*`-aware merge command. |
| **Component model / `wasm-tools compose`** | WIT components | Wrong layer. We emit one core module for JS hosts ([`07`](07-host-and-js.md)). |
| **Tool-conventions [`Linking.md`](https://github.com/WebAssembly/tool-conventions/blob/main/Linking.md) + `wasm-ld`** | Relocatable objects, `linking` / `reloc.*` sections, linear-memory C ABI | **Out.** Spec'd for MVP/C-style linking; no GC type / rec-group story. |

**Decided.** Product end state: merge in **`internal/wasm` (or sibling `internal/wasmasm`)** that owns the type intern table. Until that lands, M6 may use `wasm-merge` for std hash kernels only (decision 12); maps must still pass without it. Do not adopt `wasm-ld` object files.

| Approach | Role |
|---|---|
| In-tree merge | Product end state |
| Temporary `wasm-merge` | Optional M6 std kernels (decision 12) |
| `wasm-ld` objects | Rejected |

---

## 4. Safety and verification

### Must validate

The merged module must pass Wasm validation (engine and `wasm-tools validate`). That is the baseline memory / type safety net.

### wasmdecl (vet analogue)

After binding, check each asm function against its Go declaration under the ABI of §2:

- Parameter count and Wasm types match the lowering of the Go signature.
- Result count and types match.
- `string` is non-null `(ref $go.string)`, not `(ref null $go.string)`.
- `nosuspend` functions do not import / call suspending functions.
- Optional: refuse `memory.grow` outside the runtime package; refuse creating `$go.panic` tags other than the prelude's.

Unlike [`asmdecl`](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/asmdecl), there are no FP offsets or `RET` write checks — the signature **is** the contract.

### Optimizations

Asm functions are **opaque** to IR passes:

- No inlining of asm into Go (unless a later opt explicitly understands WAT — out of scope).
- Escape analysis uses `//go:noescape` or assumes the worst.
- Devirtualize / DCE treat them as ordinary direct `call` targets once bound.
- Runtime-lowering does not rewrite their bodies.

Go functions marked `//go:wasmgc inline` that only wrap an asm call can still inline the **call**, leaving a direct `call $asm`.

### Debugging

- `-names` / `run` / `test` emit the name section including asm function and local names from WAT.
- Source map side file can map Wasm offsets to `.wat` lines when the encoder preserves locations (**inferred** capability; not required for M0–M5).

---

## 5. Use cases and SIMD / intrinsics

### Where hand-written WAT pays off

| Area | Why asm |
|---|---|
| `internal/bytealg` (`IndexByte`, `Compare`, `Count`) | Tight loops over `(array i8)`; generics are correct but leave performance on the table |
| `runtime.memequal` / string compare | Same |
| `runtime` map hashing of bytes | Hot path in M6+ |
| `array.copy` wrappers / `memmove` | Often one instruction — see intrinsics below |
| Crypto field ops | Later; large kernels |
| SIMD kernels (`v128` + linear bridge or GC array loops) | Complements [`11-simd.md`](11-simd.md): archsimd-style Go APIs with WAT bodies, or WAT that uses `v128` over bridge memory |

Interaction with the SIMD study: prefer exposing **Go APIs** (typed helpers) and implementing bodies either as recognized intrinsics or as `*_wasmgc.wat` / `*_wasmgc_simd.wat`. Do not require users to write WAT for ordinary SIMD; keep WAT for kernels the intrinsic language cannot express cleanly (multi-block loops, EH edges, shared runtime helpers).

### Complementary mechanism: intrinsics / pragma package

| Mechanism | Ergonomics | Verification | Maintenance | Size |
|---|---|---|---|---|
| **Hand-written WAT** | Full Wasm; steep | validate + wasmdecl | Diffable but specialist | Only linked if referenced |
| **Compiler intrinsics** (`array.copy` as IR, recognized `runtime.memmove`) | Best for 1–3 instruction patterns | IR verifier | Lives in compiler | Inlined, can shrink |
| **`wasm` intrinsic package** (typed ops as Go funcs, like `internal/runtime/atomic` or archsimd) | Familiar Go call syntax | Signatures are Go; bodies compiler-known | Grows a Go API surface | Per-call or inlined |

**Decided (decision 8).** Ship both:

1. **Intrinsics** for anything that is already a Wasm opcode or a 1–3 instruction sequence (`array.copy`, `array.len`, `ref.eq`, later `v128.load` via bridge). The IR and runtime-lowering passes already want this ([`02`](02-ir.md), [`04`](04-runtime-and-abi.md)).
2. **WAT assembly** for real kernels (bytealg, hash, linear-bridge SIMD loops, crypto).

An open-coded `import "wasm"` package of every opcode is optional and lower priority: it duplicates WAT with worse multi-block ergonomics. Prefer a small `wasmgc` / `archsimd` intrinsic set over a full opcode DSL.

---

## 6. Prototype results

Location: [`prototypes/wasm-asm/`](../../prototypes/wasm-asm/).

**What it does.**

1. Hand-written `asm/memequal.wat` uses symbolic `$go:string` / `$go:bytes` / `$go:object` names and compares two string headers' byte ranges on `(array (mut i8))`.
2. `compiler/stub.wat` is a stand-in for compiler output: defines the real `$go.*` types, builds `"abc"`-like strings, imports `goasm.memequal`.
3. `run.mjs` rewrites `$go:…` → `$go.…`, parses with `wasm-tools`, merges with Binaryen `wasm-merge` (`--enable-gc --enable-reference-types`), validates, and runs in Node.

**Results (2026-11-23, Node v22.23.4, wasm-tools 1.246.2, Binaryen 133).**

| Check | Result |
|---|---|
| Equal strings via merged asm | `run() == 1` |
| Unequal strings | `run_ne() == 0` |
| Substring / offset path | `run_sub() == 1` (same bytes, different headers) |
| Asm → compiler callback module | `indexbyte` finds `'e'`; `panic_bounds` export fused |
| Divergent type shapes | `wasm-merge` fails: import/export type mismatch |
| Tool-conventions / `wasm-ld` | Not used; Linking.md has no GC type merge story |

**What was easy.** Structural GC type merge when both sides declare the same prelude; import/export fusion; Node GC instantiate without JSPI.

**What was hard / sharp edges.**

1. **Feature flags:** `wasm-merge` requires `--enable-gc` **and** `--enable-reference-types` (and often bulk-memory / multivalue). Missing a flag fails validation of otherwise valid modules.
2. **Symbolic names are not in the binary.** Name section helps humans; the merger must work by export names and structural types, or the driver must inject the compiler type section before encode.
3. **Exactness:** Binaryen's validator can phrase mismatches in terms of `(ref (exact …))` — noisy but fail-closed.
4. **No `$go:foo` in stock WAT.** Needs a tiny rewrite step (prototype) or prelude injection (production).
5. **wabt is unusable** for this dialect; wasm-tools / watgo / Binaryen only.

---

## 7. Adopted design and milestones

Decisions 8–12 in [`10-open-questions.md`](10-open-questions.md):

1. **Wasm assembly escape hatch** — `.wat` + body-less Go decls + wasmdecl + merge into the one module; intrinsics for 1–3 opcode helpers (decision 8).
2. **Embed watgo**; shell out to `wasm-tools` until ready (decision 9).
3. **No checked-in `.wasm` in v1** (decision 10).
4. **`nosuspend` default** in `runtime` and `internal/bytealg` (decision 11).
5. **Temporary `wasm-merge` OK for M6 hash kernels**; in-tree merger is the end state; no `wasm-ld` (decision 12).
6. **Prelude names (`$go.string`, …) are an ABI** — document them next to [`03`](03-type-mapping.md) and generate a header for asm authors.

### Milestone placement

| Milestone | Asm / intrinsics work |
|---|---|
| M0–M2 | None required. Keep rejecting `.s`. |
| M3–M5 | Optional: wire `//go:noescape` body-less → intrinsic only (`array.copy`). |
| **M6+ (maps, byte paths)** | Optional WAT hash / `memequal` via temporary `wasm-merge` or in-tree merge when ready; materializer allows `*_wasmgc.wat` under `std/src/runtime` and `internal/bytealg` (`nosuspend` by default). Maps must pass without WAT. |
| M8 `fmt` | Not blocked on asm. |
| M11 / linear-bridge SIMD | `*_wasmgc.wat` kernels for bridge buffers may land without Multibyte Array Access; GC-array SIMD still waits on decision 7 / M11 ([`11-simd.md`](11-simd.md)). |

Runtime work in M6+ **may** use the hatch for hashing and memequal; it must **not** require it to ship maps — generic Go loops are correct. Treat asm as an optimization lane so CI can compile with and without WAT kernels.

---

## References (primary)

- Go assembler: https://go.dev/doc/asm  
- `cmd/compile` directives (`noescape`, `nosplit`, `linkname`): https://pkg.go.dev/cmd/compile  
- `asmdecl`: https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/asmdecl  
- ABIInternal vs ABI0: https://go.dev/src/cmd/compile/abi-internal  
- Build constraints: `go help buildconstraint`  
- Wasm validation / rec types: https://webassembly.github.io/spec/core/valid/conventions.html  
- Finished proposals (GC, EH, SIMD): https://github.com/WebAssembly/proposals/blob/main/finished-proposals.md  
- Tool-conventions linking: https://github.com/WebAssembly/tool-conventions/blob/main/Linking.md  
- Binaryen `wasm-merge` (GC rewrite, v113+): https://github.com/WebAssembly/binaryen  
- watgo: https://github.com/eliben/watgo  
- wasm-tools: https://github.com/bytecodealliance/wasm-tools  

Items marked **inferred** in this doc are engineering judgment, not spec quotes: source-map line mapping for WAT; exact watgo API stability for embedding; whether Binaryen exactness will stay visible in error messages.
