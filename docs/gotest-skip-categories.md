# Skip burn-down categories (post-M10)

Inventory of `$GOROOT/test` and stdlib per-test skips. M10 baseline
([PR #22](https://github.com/achille-roussel/wasmgc-go/pull/22)): **380** language
suite passes, **646** skip-listed; stdlib packages green with **~1038** per-test
skips. Priority: functionality first, size later. JSPI always required; CI on
Node 26.13.1.

**Current (this PR):** `$GOROOT/test` **~738** pass / **288** skip-listed; stdlib
per-test **353** (array/struct value clone on assign; reflectlite.Swapper;
canonical empty string backing; recoverable nil/`panicIndex` on deref/index;
MinInt/−1 div; sub-width `^`/`-` truncate; complex/iface field eq).

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

## 1. `$GOROOT/test` — 288 skips (was 646 at M10)

### By major category (current)

| Category | Count | Fixability | Notes |
|---:|---:|---|---|
| Compile gaps (lower/load/IR) | ~110 | **fixable** | IR verify, typecheck, other compile |
| Runtime panic / trap | ~100 | **fixable** | Wrong panic, nil deref, illegal cast, hang/timeout |
| Output / failed-run mismatch | ~55 | **fixable** | Often panic text, stack traces, print/ABI |
| Codegen / Wasm validate | ~38 | **fixable** | `codegen:` + wasm validate/compile |
| Unsupported: unsafe | 41 | **by design** | `uintptr`↔`Pointer`, rejected ops |
| Other / mono | ~5 | mixed | |
| Missing stdlib import | 4 | **fixable** (overlays) | |

### By design (keep)

- **unsafe** conversions and pointer arithmetic (41).
- **reflect.StructOf / ArrayOf** with non-constant field lists — closed universe B.
- **alloc-counting** stdlib tests that assert exact `AllocsPerRun` under host GC.

---

## 2. Stdlib per-test skips — **353** (was ~1038 at M10)

| Package | Skips | Dominant root cause | Fixability |
|---|---:|---|---|
| encoding/json | 162 | Blanket + StructOf + http | StructOf **by design**; http/v2 depth |
| time | 145 | Thin rewrite vs `export_test` / zoneinfo | Overlay depth |
| fmt | **26** | scan/regexp + tables/reflect + AllocsPerRun | Fixable |
| strings | **8** | Compare IR; Finder leftovers; AllocsPerRun | Was 61 |
| slices | **8** | Overlap/Inference/Grow leftovers | Was 54 |
| sort | **2** | Interface-wrap wasm validate leftovers | Was 25 |
| strconv | 1 | ParseComplex | complex reflect |
| maps | **0** | — | **done** |
| encoding/gob | 1 | leftover | Fixable |
| errors | **0** | — | **done** |
| bytes | **0** | — | **done** |
| math/bits | **0** | — | **done** |
| container/heap | **0** | — | **done** |
| math | **0** | — | **done** |
| container/ring | 0 | — | **done** |
| unicode/utf8 | 0 | — | **done** |
| container/list | 0 | — | Passing |

### Highest-ROI stdlib unlocks (next)

1. **json / time** — http stubs, export_test, zoneinfo depth.
2. **fmt** scan_test / regexp IR; shared `fmtTests`.
3. **slices** Overlap/Inference typecheck; Stability (itab wrap).
4. GOROOT compile / wasm-validate buckets.

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
| #59 fat-ptr `==` + append overlap + recover cells | **done** | →312 / 379 |
| #60 range value capture + float NaN + As/Implements | **done** | →308 / 358 |
| value clone + Swapper + empty string + panicIndex | **this PR** | −20 GOROOT + 5 stdlib →288 / 353 |
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
| `$GOROOT/test` | **~738** | **288** |
| Stdlib per-test | — | **353** |
