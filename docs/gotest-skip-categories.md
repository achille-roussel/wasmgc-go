# Skip burn-down categories (post-M10)

Inventory of `$GOROOT/test` and stdlib per-test skips. M10 baseline
([PR #22](https://github.com/achille-roussel/wasmgc-go/pull/22)): **380** language
suite passes, **646** skip-listed; stdlib packages green with **~1038** per-test
skips. Priority: functionality first, size later. JSPI always required; CI on
Node 26.13.1.

**Current (post-[#46](https://github.com/achille-roussel/wasmgc-go/pull/46) + this PR):**
`$GOROOT/test` **~604** pass / **422** skip-listed; stdlib per-test **~1000**.

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

## 1. `$GOROOT/test` — 422 skips (was 646 at M10)

### By major category (current)

| Category | Count | Fixability | Notes |
|---:|---:|---|---|
| Compile gaps (lower/load/IR) | 123 | **fixable** | IR verify 32, typecheck 16, lower other 16, multi-value 3, println 1, other compile ~55 |
| Runtime panic / trap | 118 | **fixable** | Wrong panic, nil deref, illegal cast, hang/timeout |
| Output mismatch | 60 | **fixable** | Often panic text, stack traces, print/ABI |
| Codegen / Wasm validate | 53 | **fixable** | 5 `codegen:` + 48 wasm validate/compile (many former `codegen:` reasons rewritten) |
| Unsupported: unsafe | 41 | **by design** | `uintptr`↔`Pointer`, rejected ops (`05-unsafe-and-reflect.md`) |
| Other / mono / stack overflow | 23 | mixed | Host Go stack overflow, mono pending methods, reflect stubs |
| Missing stdlib import | 4 | **fixable** (overlays) | `os/exec`, `unique`, `runtime/pprof`, `encoding/xml`, … |

M10 had codegen/validate **193** and compile gaps **185** as the largest buckets;
burn-down since then moved the lead to **compile gaps** and **runtime**.

### Compile gaps worth early PRs

| Gap | ~N | Why first |
|---|---:|---|
| IR verify / type mismatches | 32 | Complex nullability, assign shapes |
| Typecheck / load failures | 16 | Package load before lower |
| Remaining multi-value / tuple | 3 | Lower of multi-result calls |
| `println` type coverage | 1 | Nearly cleared |

### Missing stdlib imports (4)

| Import | Approach |
|---|---|
| `os/exec`, `unique`, `runtime/pprof`, `encoding/xml` | Overlay stubs or thin rewrites |

### By design (keep)

- **unsafe** conversions and pointer arithmetic (41) — see `docs/design/05-unsafe-and-reflect.md`.
- **cgo / assembly** language tests — auto-skipped or never targeted.
- **reflect.StructOf / ArrayOf** with non-constant field lists — closed universe B; listed under stdlib.

### Needs WAT / deeper runtime

- Fuller **panic stack traces** (`runtime.Callers` / PC metadata).
- **`$go.rangeExit` multi-tag EH** + defer-inside-range-over-func.
- Some map / concurrent-write / finalizer cases may need runtime WAT beyond Go overlays.

---

## 2. Stdlib per-test skips — ~1000 (was ~1038 at M10)

| Package | Skips | Dominant root cause | Fixability |
|---|---:|---|---|
| math | 365 | Blanket lower/runtime gaps | Large; refine after math runtime |
| encoding/json | 171 | Blanket + 2× StructOf | StructOf **by design**; rest fixable |
| time | 145 | Thin `time` rewrite vs upstream tests | Overlay/rewrite depth |
| bytes | 80 | Test file typecheck/load (65), validate (13) | Compile gaps |
| strings | 61 | Test file typecheck/load (58) | Compile gaps |
| slices | 54 | Typecheck (45), runtime/assert (9) | Compile + runtime |
| fmt | 51 | Blanket lower/runtime (typecheck of `_test`) | Depends on reflect/print |
| sort | 27 | Tables / pdqsort / stable | Runtime/algorithm parity |
| math/bits | 25 | `uint64`→`uint` overflow on 32-bit word | Sizes / build tags |
| strconv | **13** | ParseInt/Atoi/Atof/CountMallocs | Was 26; quote/unicode cleared |
| errors | 5 | `As`/`Is`/`Join` reflectlite / DeepEqual | **Fixable** |
| maps | 1 | `Clone` large keys / `-0` | Overlay/freeze semantics |
| container/heap | 1 | `sync/atomic` fat pointer | unsafe/atomic subset |
| encoding/gob | 1 | StructOf/ArrayOf | **by design** |
| container/ring | **0** | ~~pointer equality / circular links~~ — cleared | **done** |
| unicode/utf8 | **0** | ~~Package-init / UTF-8 range~~ — [PR #24](https://github.com/achille-roussel/wasmgc-go/pull/24) | **done** |
| container/list | 0 | — | Passing |

### Highest-ROI stdlib unlocks (next)

1. **strconv** remaining ParseInt/Atoi/Atof (12) + AllocsPerRun (1) — numeric parse + mallocs helper.
2. **errors** (5) — `As`/`Is`/`Join` reflectlite / DeepEqual descriptors.
3. **maps.CloneLarge** (1) — Clone overlay edge cases.
4. Unlock **bytes/strings/slices** typecheck failures once lower/load gaps clear.
5. **math/bits** (25) — accept 32-bit `uint` or gate tests.
6. Only then un-blanket **fmt / json / time / math**.

### By design (stdlib)

| Skip | Reason |
|---|---|
| `encoding/json` StructOf benches/tests | Dynamic field lists outside closed universe B |
| `encoding/gob` StructOf/ArrayOf | Same |
| Unrestricted `reflect.MakeFunc` / similar | Not in supported reflect subset |

---

## 3. Suggested PR series

| PR | Category | Status / expected delta |
|---|---|---|
| utf8 package-init | stdlib unicode/utf8 | **done** (−15, [PR #24](https://github.com/achille-roussel/wasmgc-go/pull/24)) |
| Micro → batch burn-down | GOROOT clusters | **done through #46** (597/429) |
| strconv quote + ring | stdlib | **this PR** (−21 stdlib; +7 GOROOT accidental) |
| strconv ParseInt/Atof | stdlib | −12…13 |
| errors As/Is/Join | stdlib | −5 |
| GOROOT compile/runtime buckets | GOROOT | −15+ each batch |
| Un-blanket fmt/json/time/math | stdlib | after compile/reflect depth |

Each PR description should report before/after pass and skip counts for
`$GOROOT/test` and affected stdlib packages vs M10 (**380** / **646**).

---

## 4. Baseline counts

### M10 merge

| Layer | Pass | Fail | Skip |
|---|---:|---:|---:|
| `$GOROOT/test` (`// run`) | 380 | 0 | 646 (+ auto skip_action/constraint) |
| Stdlib packages | 16/16 packages ok | 0 | ~1038 per-test |

### After PR #24 (unicode/utf8 unlock)

| Layer | Pass | Fail | Skip |
|---|---:|---:|---:|
| `$GOROOT/test` | 380 | 0 | 646 |
| unicode/utf8 | **15** | 0 | **0** (was 15) |
| Stdlib per-test total | — | — | ~1023 |

### After PR #46 (goto/defer/struct batch)

| Layer | Pass | Fail | Skip |
|---|---:|---:|---:|
| `$GOROOT/test` | **597** | 0 | **429** |
| Stdlib per-test total | — | — | ~1021 |

### After this PR (strconv quote/unicode + container/ring + 7 GOROOT)

| Layer | Pass | Fail | Skip |
|---|---:|---:|---:|
| `$GOROOT/test` | **~604** | 0 | **422** |
| strconv | — | — | **13** (was 26) |
| container/ring | — | — | **0** (was 8) |
| Stdlib per-test total | — | — | **~1000** |

Update this section when a burn-down PR lands.
