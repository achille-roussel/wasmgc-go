# Skip burn-down categories (post-M10)

Inventory of `$GOROOT/test` and stdlib per-test skips. M10 baseline
([PR #22](https://github.com/achille-roussel/wasmgc-go/pull/22)): **380** language
suite passes, **646** skip-listed; stdlib packages green with **~1038** per-test
skips. Priority: functionality first, size later. JSPI always required; CI on
Node 26.13.1.

**Current (this PR):** `$GOROOT/test` **~607** pass / **419** skip-listed; stdlib
per-test **612** (was 624 after #48).

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

## 1. `$GOROOT/test` — 419 skips (was 646 at M10)

### By major category (current)

| Category | Count | Fixability | Notes |
|---:|---:|---|---|
| Compile gaps (lower/load/IR) | ~119 | **fixable** | IR verify, typecheck, other compile |
| Runtime panic / trap | ~119 | **fixable** | Wrong panic, nil deref, illegal cast, hang/timeout |
| Output / failed-run mismatch | ~78 | **fixable** | Often panic text, stack traces, print/ABI |
| Codegen / Wasm validate | ~53 | **fixable** | `codegen:` + wasm validate/compile |
| Unsupported: unsafe | 41 | **by design** | `uintptr`↔`Pointer`, rejected ops |
| Other / mono | ~5 | mixed | |
| Missing stdlib import | 4 | **fixable** (overlays) | |

### By design (keep)

- **unsafe** conversions and pointer arithmetic (41).
- **reflect.StructOf / ArrayOf** with non-constant field lists — closed universe B.

---

## 2. Stdlib per-test skips — **612** (was ~1038 at M10)

| Package | Skips | Dominant root cause | Fixability |
|---|---:|---|---|
| encoding/json | 171 | Blanket + StructOf | StructOf **by design**; rest fixable |
| time | 145 | Thin rewrite vs `export_test` / zoneinfo | Overlay depth |
| bytes | 80 | Test file typecheck/load | Compile gaps |
| strings | 61 | Test file typecheck/load | Compile gaps |
| slices | 54 | Typecheck / runtime | Compile + runtime |
| fmt | **41** | scan_test→regexp; shared tables; reflect | Was 51; 10 printers unskipped |
| sort | 27 | Tables / pdqsort / stable | Runtime |
| math/bits | 25 | `uint64`→`uint` overflow on 32-bit word | Sizes / build tags |
| errors | 4 | `As`/`Is`/`AsType` reflectlite | **Fixable** |
| strconv | 3 | ParseComplex + AllocsPerRun | complex reflect |
| maps | 1 | `Clone` large keys / `-0` | Overlay |
| container/heap | 1 | `sync/atomic` fat pointer | unsafe/atomic |
| encoding/gob | 1 | StructOf/ArrayOf | **by design** |
| math | **0** | ~~MaxUint wrap~~ — truncateInt | **done** |
| container/ring | 0 | — | **done** |
| unicode/utf8 | 0 | — | **done** |
| container/list | 0 | — | Passing |

### Highest-ROI stdlib unlocks (next)

1. **fmt** scan_test / regexp IR temp-outside-region; shared `fmtTests` `array.new_fixed`.
2. **errors** `As`/`Is` (4) — reflectlite Set / Implements.
3. **strconv** ParseComplex — complex128 reflect Kind.
4. Unlock **bytes/strings/slices** typecheck failures.
5. Un-blanket **json / time** after export_test / http / zoneinfo depth.

---

## 3. Suggested PR series

| PR | Category | Status |
|---|---|---|
| Through #48 | GOROOT + math/strconv/errors.Join | **done** (~604/422; stdlib 624) |
| fmt printers + math wrap + byte/rune TypeKey | stdlib + GOROOT | **this PR** |
| fmt scan/regexp + json/time | stdlib | next |
| GOROOT compile/runtime buckets | GOROOT | −15+ each batch |

---

## 4. Baseline counts

### M10 merge

| Layer | Pass | Skip |
|---|---:|---:|
| `$GOROOT/test` | 380 | 646 |
| Stdlib per-test | — | ~1038 |

### After PR #48

| Layer | Pass | Skip |
|---|---:|---:|
| `$GOROOT/test` | ~604 | 422 |
| Stdlib per-test | — | 624 |

### After this PR

| Layer | Pass | Skip |
|---|---:|---:|
| `$GOROOT/test` | **~607** | **419** |
| fmt | 10 more | **41** (was 51) |
| math | full package | **0** (was 2) |
| Stdlib per-test | — | **612** |

Update this section when a burn-down PR lands.
