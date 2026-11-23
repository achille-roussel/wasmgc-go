# 11 — SIMD and `archsimd` on Wasm GC

Can this target use Wasm's 128-bit SIMD together with the Wasm GC memory model,
enough to implement Go's experimental `simd/archsimd` and to replace the
scalar `internal/bytealg` generics with vector kernels?

**Verdict.** The SIMD *instruction set* is available and maps cleanly onto
`archsimd`'s wasm surface. The *memory model* is the problem: `[]byte` and
`string` are `(array i8)` (`03-type-mapping.md`), and there is no instruction
today that loads or stores a `v128` from that array at a byte offset. Building
vectors from sixteen `array.get_u`s is only a mild win. Copying through the
linear bridge to use `v128.load` does not amortize on a single scan. Storing
hot buffers as `(array v128)` would unlock ~10× kernels but breaks slicing,
`unsafe.Slice`, the immutable-header design, and interop.

**Decision (Achille, 2026-11-22).** Option A: defer end-to-end `archsimd` and
SIMD `bytealg` on GC arrays to **gated M11**, which starts only when Multibyte
Array Access (or equivalent) ships in CI's Node without a flag
(`10-open-questions.md` §7, `09-bootstrap-plan.md`). Keep `$go.bytes` as
`(array i8)`; ship scalar generics through M8–M10. Dual-rep, bridge-first
defaults, and lane-build `Load*` are rejected for production.

**Related.** Hand-written WAT (`12-wasm-assembly.md`, decisions 8–12 in `10`, [PR #5](https://github.com/achille-roussel/wasmgc-go/pull/5))
is the likely escape hatch for SIMD kernels over **linear-memory** bridge data
before Multibyte Array Access exists. That does not unblock GC `[]byte`
`archsimd.Load*` and is not a dependency of this decision.

## Primary sources checked

| Claim | Source |
|---|---|
| Go 1.27 adds `simd` and extends `simd/archsimd` to arm64 and **wasm** under `GOEXPERIMENT=simd` | [Go 1.27 release notes](https://go.dev/doc/go1.27), [archsimd blog](https://go.dev/blog/archsimd) (2026-10-02) |
| Pin `go1.27.4` contains `src/simd/archsimd/types_wasm.go`, `ops_wasm.go` | Local GOROOT of the pin |
| Fixed-width SIMD (`v128`) is in Wasm 2.0; relaxed SIMD is in Wasm 3.0 | [Finished proposals](https://github.com/WebAssembly/proposals/blob/main/finished-proposals.md); [Wasm 3.0 announcement](https://webassembly.org/news/2025-09-17-wasm-3.0/); core spec CR 2026-11-20 |
| GC arrays may have numeric, **vector**, or packed element types; `array.new_data` / `array.init_data` / `array.fill` / `array.copy` apply | [GC MVP](https://github.com/WebAssembly/gc/blob/main/proposals/gc/MVP.md); issue [#321](https://github.com/WebAssembly/gc/issues/321) (v128 in `array.new_data` was an oversight, fixed) |
| No MVP instruction loads `v128`/`i64`/… from `(array i8)` at a byte offset | GC MVP instruction list; confirmed by absence in core aggregate ops |
| Multibyte Array Access (incl. `v128.load` on GC arrays) is **phase 2** | [proposals README](https://github.com/WebAssembly/proposals/blob/main/README.md) (active phase-2 table); [Overview](https://github.com/WebAssembly/multibyte-array-access/blob/main/proposals/multibyte-array-access/Overview.md); design issue [#1569](https://github.com/WebAssembly/design/issues/1569) |
| `(array v128)` and `v128` struct fields run on Node 22.23.4 and 26.13.1 | `prototypes/simd` smoke tests, 2026-11-23 |
| Benchmark numbers below | `prototypes/simd/results-node26.json`, same date |

Inferred (marked where used): engine codegen quality for `replace_lane` loops;
whether Multibyte Array Access lands in a Node LTS before our M10; binary-size
delta of linking `archsimd` (no measurement yet — the package is not vendored).

## Go `simd` / `archsimd` on the pin

Go 1.27 (the pin is `go1.27.4`, released 2026-11-10) keeps the experimental
gate `GOEXPERIMENT=simd`. Two packages:

| Package | Role on wasm |
|---|---|
| `simd/archsimd` | Architecture-specific fixed-width vectors. On `GOARCH=wasm`, only **128-bit** types: `Int8x16`, `Uint8x16`, `Int16x8`, `Uint16x8`, `Int32x4`, `Uint32x4`, `Int64x2`, `Uint64x2`, `Float32x4`, `Float64x2`, and masks `Mask8x16` … `Mask64x2`. Methods lower to Wasm SIMD ops (`I8x16Add`, `I8x16Eq`, `V128And`, …) per `ops_wasm.go`. |
| `simd` | Portable, size-agnostic vectors (`Uint8s`, `Float32s`, …). On wasm the underlying width is 16 lanes of `uint8` / 4 of `float32`, matching `v128`. |

`archsimd` began as amd64-only in Go 1.26. Go 1.27 adds arm64 NEON and wasm.
There is no `archsimd.Wasm` feature-check object analogous to `archsimd.X86` /
`ARM64`: Wasm SIMD is either present for the module or the module does not
validate. Optional **relaxed** SIMD ops are a separate Wasm feature; the pin's
`ops_wasm.go` targets the fixed-width set. 256- and 512-bit types exist only
on amd64; on wasm they are absent from `types_wasm.go`, not emulated.

Load/store shape (wasm, from `types_wasm.go`):

```go
func LoadUint8x16(s []uint8) Uint8x16
func LoadUint8x16Array(y *[16]uint8) Uint8x16
func LoadUint8x16Part(s []uint8) (Uint8x16, int)
func (x Uint8x16) Store(s []uint8)
```

Upstream `GOARCH=wasm` implements those as linear-memory `v128.load` /
`v128.store`. On this target a `[]uint8` is a header over `(array i8)`
(`03-type-mapping.md`). The same Go source cannot lower to `v128.load` until
there is a GC-array load or a copy onto the linear bridge.

## What Wasm GC allows today

### `v128` as a field or array element

Allowed. The GC MVP's `array.new_data` text requires the element type to be
"numeric, vector, or packed"; issue #321 confirmed `v128` was intended.
`array.get` / `array.set` of `(array (mut v128))` return and consume `v128`.
A struct field of type `v128` is valid. Both shapes instantiate on Node
22.23.4 and Node 26.13.1 (`prototypes/simd` smoke tests).

`array.copy`, `array.fill`, and `array.init_data` work for `v128` elements:
each element is 16 bytes in the data segment. They move whole vector slots,
not arbitrary byte ranges inside an `(array i8)`.

### Loading a `v128` from `(array i8)`

**There is none.** The aggregate instructions are one element at a time.
`array.get_u` of an `i8` array yields an `i32` lane. There is no
`array.load_v128`, no `v128.load` that takes an array reference, and no
reinterpret cast from `(array i8)` to `(array v128)`.

The Multibyte Array Access proposal (phase 2, champion Brendan Dahl) adds
exactly that: the existing memory load/store opcodes, with a memarg flag bit
selecting a GC array operand and a type index, including `v128.load` /
`v128.store` and the lane/splat variants, with a byte address into arrays
whose element type is numeric, vector, or packed. Until that proposal is
implemented in the engines this project runs on, a compiler cannot emit a
single instruction that feeds SIMD from `$go.bytes`.

Related but distinct:

- **stringref** (phase 1) converts between `stringref` and GC `i8`/`i16`
  arrays; it does not load `v128`.
- **shared-everything threads** (phase 1) may eventually want atomic
  multibyte array ops; the Multibyte Array Access discussion notes that as a
  follow-on, not a blocker for the non-atomic ops.
- GC **post-MVP** bulk sketches became MVP `array.copy` / `array.fill`; they
  do not add cross-width loads.

### Feeding SIMD from scalar gets

`i8x16.replace_lane` / `i8x16.splat` / `i64x2.replace_lane` are ordinary SIMD
ops. A compiler can legally do:

```
v = v128.const 0
v = i8x16.replace_lane 0 v (array.get_u $bytes a i)
…
v = i8x16.replace_lane 15 v (array.get_u $bytes a (i+15))
```

That is sixteen GC loads plus sixteen lane inserts before any useful SIMD
work. The prototype measures it as `lanes`.

There is also no way to read eight bytes at once from an `i8` array into an
`i64`. SWAR must compose eight `array.get_u`s with shifts and ORs (`swar` in
the prototype). An `(array i64)` would give `array.get` of eight bytes, but
that is a different representation of `[]byte`, with the same slicing and
interop problems as `(array v128)`.

## Candidate strategies

### (a) Store hot buffers as `(array v128)` or a hybrid

**Idea.** `$go.bytes` becomes `(array (mut v128))`, or a hybrid header that
points at either `i8` or `v128` chunks.

**Cost.**

- Slicing `s[i:j]` with a non-multiple-of-16 `i` needs a byte offset into a
  vector element. Wasm arrays index elements, not bytes. Without Multibyte
  Array Access, a subslice that is not chunk-aligned cannot share the backing
  array; it must copy. That fights the immutable-header design
  (`03-type-mapping.md`), where a reslice is a new header over the same
  `$data`.
- `cap` and `append` growth are in bytes today. Chunk capacity would be
  rounded up; `unsafe.Slice` / `unsafe.String` fat pointers index bytes
  (`05-unsafe-and-reflect.md`). The fat-pointer class for `*byte` assumes an
  `i8` array.
- Interop with the linear bridge (`07-host-and-js.md`) and with JS
  `CopyBytesToGo` would re-encode every time.
- Literals via `array.new_data` still work (16-byte elements), but every
  string/bytes constant grows padding logic.

**When it helps.** The prototype's `v128arr` path is ~9× faster than scalar
`IndexByte` at 1 MiB. That is the ceiling if every hot buffer were already
chunked. It is not available for arbitrary `[]byte` without representation
surgery.

**Recommendation.** Do not change `$go.bytes`. Keep packed `i8`.

### (b) Copy through the linear bridge, then `v128.load`

**Idea.** The host already cannot read GC arrays; `runtime.write` copies into
linear memory (`07`). A kernel can `array.get_u` / `i32.store8` the slice into
the bridge, then run a normal Wasm SIMD loop with `v128.load`.

**Cost.** Measured `bridge_copy_only` at 1 MiB is ~357 µs, comparable to one
scalar `IndexByte` over the same buffer (~462 µs). One-shot `bridge` (copy +
SIMD) is therefore **slower than scalar** for IndexByte, Equal, and Count at
every size measured. Amortization requires many SIMD passes over a buffer that
stays in the bridge without being mutated through the GC array — a narrow
pattern (e.g. a CRC over a freshly decoded buffer that will be written to the
host anyway).

**When it helps.** `mem` (SIMD after a prior copy) is ~15–17× faster than
scalar at 1 MiB. Useful if the compiler or runtime already bridged the bytes
for another reason, or for a multi-pass API that pins a view. Not the default
`bytealg` path. Kernels that *start* from bridge memory (host I/O, codecs that
copy once) are a natural fit for hand-written WAT with `v128.load` on linear
memory (`12-wasm-assembly.md`); that is orthogonal to M11's GC-array gate.

### (c) Build `v128` from scalar `array.get` + lane ops

**Idea.** No representation change. Emit the sixteen-get sequence, then SIMD.

**Cost.** ~1.5× faster than scalar for IndexByte and Count on large buffers;
**slower** than scalar for Equal (two buffers ⇒ thirty-two gets per 16 bytes).
Binary size grows: each vector load is a long instruction sequence unless a
runtime helper is shared (call overhead then eats the 1.5×).

**Recommendation.** Acceptable as a micro-optimization for a hand-written
hot helper if Multibyte Array Access is still far away; not worth a general
`archsimd.Load` lowering.

### (d) i64 SWAR composed from eight gets

**Idea.** Classic haszero / count tricks on `i64` without SIMD.

**Cost.** Still eight GC loads per eight bytes. Measured ~1.3× vs scalar
IndexByte at 1 MiB — worse than `lanes`, far from `mem`. There is no MVP way
to load those eight bytes in one instruction from `(array i8)`.

**Recommendation.** Skip.

### (e) Host-side JS / engine intrinsics

**Idea.** Bridge once, call `Uint8Array.prototype.indexOf`, or a JS SIMD
library; or import a host builtin.

**Cost.** `indexOf` on an already-bridged buffer beats scalar Wasm at large
sizes (~1.7× at 1 MiB) but loses to Wasm `v128.load` SIMD by ~10×. A JS byte
loop for Count is **slower** than scalar Wasm. Crossing into JS on every
`bytes.IndexByte` also fights the "small runtime" goal and needs a stable
import surface.

**Recommendation.** Not for `bytealg`. Keep the bridge for host I/O only.

### (f) Wait on / propose a spec extension

**Idea.** Depend on Multibyte Array Access (or an `array.load_v128` variant).
Tell the Wasm CG that Go's `[]byte` / `string` / `archsimd.Load*` are blocked
on byte-addressable loads from packed GC arrays — the same use case Dart
reported ([multibyte-array-access#5](https://github.com/WebAssembly/multibyte-array-access/issues/5)).

**Status.** Phase 2 as of the proposals list checked 2026-11-23. Spec text
exists. Engine implementation is in progress in discussion (Binaryen/V8
mentioned in that issue); **not** available in Node 22 or 26. This project
should not emit the new opcodes until the engines in `07-host-and-js.md`
accept them without a flag, matching the JSPI bar.

**Recommendation.** This is the right long-term fix. Track it; do not block
M8 on it.

## Prototype measurements

Methodology and raw tables: `prototypes/simd/README.md`. Summary ratios on
Node v26.13.1 at **1 MiB**, relative to scalar `(array i8)`:

| Kernel | lanes | bridge (copy+SIMD) | mem (SIMD only) | v128arr | swar |
|---|---:|---:|---:|---:|---:|
| IndexByte | 1.48× | 1.10× | **16.6×** | 8.7× | 1.29× |
| Equal | 0.90× | 0.79× | **11.8×** | 7.6× | — |
| Count | 1.54× | 1.15× | **17.1×** | 10.1× | — |

(× > 1 means faster than scalar.) Node 22.23.4 shows the same ranking; absolute
ns/op are within ~15%.

## `archsimd` mapping on this ABI

### Ops that map 1:1

Once a `v128` is in a local, almost all wasm `archsimd` methods are direct
Wasm SIMD instructions: arithmetic, comparisons to masks, `BitSelect`,
shuffles/`LookupOrZero` (`i8x16.swizzle`), conversions, `ToBits` /
`ReshapeToUint*`. Relaxed SIMD is optional and can stay gated off until the
glue feature-detects it.

### Loads and stores

| Go | Upstream wasm lowering | wasmgc-go lowering today |
|---|---|---|
| `LoadUint8x16(s []uint8)` | `v128.load` from slice ptr | **Blocked** on Multibyte Array Access, or expand to 16× `array.get_u` + `replace_lane`, or copy to bridge |
| `LoadUint8x16Array(y *[16]uint8)` | `v128.load` | Same; `*[16]uint8` is an `(array i8)` of len 16 (`03`) |
| `Store` / `StorePart` | `v128.store` | Symmetric store problem |

`Load*Part` / `StorePart` need a masked or partial path; with lane builds the
partial path is just fewer gets.

### Wider vectors

`Int8x32`, `Float32x8`, `Float32x16`, … do not exist on wasm `archsimd`. Do
not emulate them as pairs of `v128` under the wasm type names — that would
fork the API. Portable `simd` already abstracts width; on wasm its lane count
is the 128-bit width. User code that imports amd64-only types simply does not
build for this GOARCH, as with upstream.

### Feature detection

No runtime check for baseline Wasm SIMD. If the module uses `v128`, engines
without SIMD reject it at validate time. Document that `GOARCH=wasmgc`
binaries that import `simd/archsimd` require SIMD (true of every engine in
`07`). Relaxed ops, if exposed later, need a glue-side `WebAssembly.validate`
probe or an import stub.

### Composition with the stdlib strategy

`06-stdlib-strategy.md` vendors `internal/bytealg` as the **generic** Go
files via the `GOARCH=wasmgc` re-filter. That remains correct for M8:

1. **M8–M10 path:** generic `IndexByte`, `Equal`, `Compare`, `Count`,
   `Index` — scalar `array.get_u` loops. No overlay.
2. **M11 (gated on Multibyte Array Access in CI Node):** either
   - compiler intrinsics for those five functions, or
   - an overlay/`rewrite` of `internal/bytealg` written in `archsimd` once
     `LoadUint8x16` lowers to a single GC-array load.
3. **Do not** rewrite GC `bytealg` in `archsimd` before loads are cheap — the
   prototype shows the lane-build form is not enough. Optional earlier WAT
   kernels over linear bridge memory are a `12` concern, not a substitute for
   this gate.

Hashes, hex/base64, UTF-8 validation, and map hashing follow the same gate:
SIMD helps only after loads from `$go.bytes` are wide.

## Binary size

Not measured on a real `wasmgc-go` binary (the compiler does not exist yet).
Inferred from the study goals and the wasm3 empty-main budget:

- Emitting SIMD opcodes in a few `bytealg` helpers is small (kilobytes).
- Linking a user-imported `archsimd` surface is large if monomorphized method
  wrappers are generated per call site; prefer compiler intrinsics recognized
  from the `archsimd` method names, like upstream.
- A always-on lane-build helper for every `Load*` would add code size for
  little speed; avoid.

Empty-main and builtin-println budgets in `00-overview.md` must not regress:
SIMD is behind an import of `simd/archsimd` or a use of a vectorized
`bytealg` that dead-code eliminates when unused.

## Milestone fit

| Milestone | SIMD work |
|---|---|
| M0–M7 | None. Scalar memory ops only. |
| **M8** (`fmt`, vendored `bytealg` generics) | Keep generics. No `archsimd` in the materializer. |
| M9–M10 | Scalar GC `bytealg`. Optional: WAT SIMD over linear bridge only, if `12` lands (`12-wasm-assembly.md`). |
| **M11 (gated)** | Enable `GOEXPERIMENT=simd` user code; lower `archsimd` ops; vectorize GC `bytealg` once GC-array `v128.load` exists in CI's Node without a flag. |

M8 is the wrong place to pay for this: `fmt` does not need SIMD, and the
stdlib strategy's first deliverable is correctness under the checker.

## Recommendation (adopted)

1. **Feasible now for register-to-register SIMD**, not for `[]byte`-backed
   `archsimd.Load` / GC `bytealg`.
2. **Do not** change `$go.bytes` to `(array v128)`.
3. **Do not** make one-shot bridge copying the default kernel path.
4. **Do** keep scalar generics through M8–M10; Multibyte Array Access is the
   external dependency; speak up in the CG that Go GC-array languages need it
   (Dart already did).
5. **Do** run gated M11: `archsimd` intrinsics + vector `bytealg` when engines
   ship the proposal, with a prototype re-run that adds a `v128.load (type
   $bytes)` cell the day CI's Node can validate it without a flag.
6. Lane-build and SWAR are fallback curiosities, not the design.
7. **Do** treat `12-wasm-assembly.md` (PR #5) as the place for SIMD over
   linear-memory data when a kernel already lives on the bridge — without
   waiting on M11 and without changing this deferral for GC arrays.

Recorded as decision 7 in `10-open-questions.md`.
