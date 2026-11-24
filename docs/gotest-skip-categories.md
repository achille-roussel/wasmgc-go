# Skip burn-down categories (post-M10)

Inventory of `$GOROOT/test` and stdlib per-test skips. M10 baseline
([PR #22](https://github.com/achille-roussel/wasmgc-go/pull/22)): **380** language
suite passes, **646** skip-listed; stdlib packages green with **~1038** per-test
skips. Priority: functionality first, size later. JSPI always required; CI on
Node 26.13.1.

**Current (this PR):** `$GOROOT/test` **~672** pass / **354** skip-listed; stdlib
per-test **437** (nil-slice Len + struct-array zero + array reflect).

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

Batch PRs: base on latest `main`, unskip roughly **≥15** verified PASSes (or clear
a whole root-cause bucket), report `$GOROOT/test` and stdlib totals vs M10.

---

## 1. `$GOROOT/test` — 354 skips (was 646 at M10)

### By major category (current)

| Category | Count | Fixability | Notes |
|---:|---:|---|---|
| Compile gaps (lower/load/IR) | ~119 | **fixable** | IR verify, typecheck, other compile |
| Runtime panic / trap | ~119 | **fixable** | Wrong panic, nil deref, illegal cast, hang/timeout |
| Output / failed-run mismatch | ~78 | **fixable** | Often panic text, stack traces, print/ABI |
| Codegen / Wasm validate | ~38 | **fixable** | `codegen:` + wasm validate/compile; −15 this PR |
| Unsupported: unsafe | 41 | **by design** | `uintptr`↔`Pointer`, rejected ops |
| Other / mono | ~5 | mixed | |
| Missing stdlib import | 4 | **fixable** (overlays) | |

### By design (keep)

- **unsafe** conversions and pointer arithmetic (41).
- **reflect.StructOf / ArrayOf** with non-constant field lists — closed universe B.

---

## 2. Stdlib per-test skips — **437** (was ~1038 at M10)

| Package | Skips | Dominant root cause | Fixability |
|---|---:|---|---|
| encoding/json | 166 | Blanket + StructOf + http | StructOf **by design**; http/v2 depth |
| time | 145 | Thin rewrite vs `export_test` / zoneinfo | Overlay depth |
| fmt | 31 | scan/regexp + tables/reflect | Fixable |
| slices | **30** | Insert/Delete/Replace generics; iter+rand/v2 | Was 54 |
| sort | 26 | Tables / pdqsort / stable; needs `math/rand/v2` in tests | Runtime |
| math/bits | **11** | Div/Reverse panic+OOB leftovers | Was 25 |
| bytes | **9** | Remaining validate + IndexAny/Split/Repeat | Was 80 |
| strings | **14** | Compare IR; Builder/Map leftovers | Was 61 |
| errors | 2 | `Is`/`AsType` leftovers | **Fixable** |
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
3. **slices** Insert/Delete/Replace unsubstituted type params; `math/rand/v2`.
4. **bytes** IndexAny / Split / validate leftovers.
5. GOROOT compile / wasm-validate buckets.

---

## 3. Suggested PR series

| PR | Category | Status |
|---|---|---|
| Through #49 | GOROOT + fmt/math/TypeKey | **done** (~607/419; stdlib 612) |
| #50 bytes/strings/slices | testenv/B/atomic/rand | **done** (−112 →500) |
| GOROOT validate i64/array/mapType | **this PR** | −15 (~622/404) |
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
| `$GOROOT/test` | **~622** | **404** |
| Stdlib per-test | — | **500** (unchanged) |

Update this section when a burn-down PR lands.
