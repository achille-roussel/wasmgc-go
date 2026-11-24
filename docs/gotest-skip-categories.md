# Skip burn-down categories (post-M10)

Inventory of `$GOROOT/test` and stdlib per-test skips. M10 baseline
([PR #22](https://github.com/achille-roussel/wasmgc-go/pull/22)): **380** language
suite passes, **646** skip-listed; stdlib packages green with **~1038** per-test
skips. Priority: functionality first, size later. JSPI always required; CI on
Node 26.13.1.

**Current (this PR):** `$GOROOT/test` **~714** pass / **312** skip-listed; stdlib
per-test **379** (fat-pointer base+index `==`; append overlap-safe spread;
recover catch reads cell-boxed named results; slices Insert/Delete/Replace
cluster + Compact/Chunk/MinMax panics; fmt Append*; strings Map; bytes
RepeatCatchesOverflow).

Auto-skipped `$GOROOT/test` actions (`errorcheck*`, `compile*`, `rundir`,
`asmcheck`, …) and build-tag exclusions are **not** in `skip.txt` (M10 reported
~1569 skip_action + ~49 skip_constraint). This doc covers committed skip lists
only.

## How to read this

| Tag | Meaning |
|---|---|
| **fixable** | Compiler/runtime/overlay work can remove the skip |
| **by design** | Closed-universe / Wasm GC limits; keep skip with reason |
| **needs WAT** | Likely needs hand-written Wasm / runtime WAT escape hatch |
| **blanket** | Package still largely blocked; reasons not yet per-test refined |

Do not add new skips without a one-line reason. Growing `$GOROOT/test` `skip.txt`
fails CI unless `skip_bump.txt` justifies it.

Batch PRs: base on latest `main`, unskip roughly **≥25** verified PASSes (or clear
a whole root-cause bucket), report `$GOROOT/test` and stdlib totals vs M10.

---

## 1. `$GOROOT/test` — 312 skips (was 646 at M10)

### By major category (current)

| Category | Count | Fixability | Notes |
|---:|---:|---|---|
| Compile gaps (lower/load/IR) | ~119 | **fixable** | IR verify, typecheck, other compile |
| Runtime panic / trap | ~115 | **fixable** | Wrong panic, nil deref, illegal cast, hang/timeout |
| Output / failed-run mismatch | ~61 | **fixable** | Often panic text, stack traces, print/ABI |
| Codegen / Wasm validate | ~38 | **fixable** | `codegen:` + wasm validate/compile |
| Unsupported: unsafe | 41 | **by design** | `uintptr`↔`Pointer`, rejected ops |
| Other / mono | ~5 | mixed | |
| Missing stdlib import | 4 | **fixable** (overlays) | |

### By design (keep)

- **unsafe** conversions and pointer arithmetic (41).
- **reflect.StructOf / ArrayOf** with non-constant field lists — closed universe B.

---

## 2. Stdlib per-test skips — **379** (was ~1038 at M10)

| Package | Skips | Dominant root cause | Fixability |
|---|---:|---|---|
| encoding/json | 166 | Blanket + StructOf + http | StructOf **by design**; http/v2 depth |
| time | 145 | Thin rewrite vs `export_test` / zoneinfo | Overlay depth |
| fmt | **28** | scan/regexp + tables/reflect | Fixable |
| slices | **16** | *Panics range-capture; Overlap/Inference leftovers | Was 54 |
| strings | **12** | Compare IR; Finder leftovers | Was 61 |
| sort | **4** | Interface-wrap wasm validate leftovers | Was 25 |
| errors | 2 | `Is`/`AsType` leftovers | **Fixable** |
| bytes | **1** | Grow data | Was 80 |
| math/bits | **2** | Add/Sub64 overflow recover illegal cast | Was 11 |
| strconv | 1 | ParseComplex | complex reflect |
| maps | 1 | `Clone` large keys / `-0` | Overlay |
| container/heap | **0** | — | **done** |
| math | **0** | — | **done** |
| container/ring | 0 | — | **done** |
| unicode/utf8 | 0 | — | **done** |
| container/list | 0 | — | Passing |

### Highest-ROI stdlib unlocks (next)

1. **json / time** — http stubs, export_test, zoneinfo depth.
2. **fmt** scan_test / regexp IR; shared `fmtTests`.
3. **slices** Insert/Delete/Replace unsubstituted type params.
4. **errors.TestIs** — Go 1.22 per-iter value capture without breaking `TestAs`.
5. GOROOT compile / wasm-validate buckets.

---

## 3. Suggested PR series

| PR | Category | Status |
|---|---|---|
| Through #49 | GOROOT + fmt/math/TypeKey | **done** (~607/419; stdlib 612) |
| #50 bytes/strings/slices | testenv/B/atomic/rand | **done** (−112 →500) |
| #55 array/reflect Len/Index | **done** (−23 →354; stdlib 437) |
| #56 array `==` + Div panic | **done** (−16 →344; stdlib 431) |
| #57 range assign-form + parallel assign | **done** (−22 →322; stdlib 425) |
| #58 named-result defer + rand/v2 | **done** (−4 →318; stdlib 398) |
| fat-ptr `==` + append overlap + recover cells | **this PR** | −6 GOROOT + 19 stdlib →312 / 379 |
| fmt scan/regexp + json/time | stdlib | next |
| GOROOT compile/runtime buckets | GOROOT | −15+ each batch |

---

## 4. Baseline counts

### M10 merge

| Layer | Pass | Skip |
|---|---:|---:|
| `$GOROOT/test` | 380 | 646 |
| Stdlib per-test | — | ~1038 |

### After PR #50

| Layer | Pass | Skip |
|---|---:|---:|
| `$GOROOT/test` | ~607 | 419 |
| Stdlib per-test | — | 500 |

### After this PR

| Layer | Pass | Skip |
|---|---:|---:|
| `$GOROOT/test` | **~714** | **312** |
| Stdlib per-test | — | **379** |
