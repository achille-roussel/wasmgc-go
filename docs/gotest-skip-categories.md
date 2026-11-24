# Skip burn-down categories (post-M10)

Inventory of `$GOROOT/test` and stdlib per-test skips. M10 baseline
([PR #22](https://github.com/achille-roussel/wasmgc-go/pull/22)): **380** language
suite passes, **646** skip-listed; stdlib packages green with **~1038** per-test
skips. Priority: functionality first, size later. JSPI always required; CI on
Node 26.13.1.

**Current (this PR):** `$GOROOT/test` **~604** pass / **422** skip-listed; stdlib
per-test **626** (was ~1000 after #47).

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
| Compile gaps (lower/load/IR) | ~119 | **fixable** | IR verify 32, typecheck 16, other compile |
| Runtime panic / trap | ~119 | **fixable** | Wrong panic, nil deref, illegal cast, hang/timeout |
| Output / failed-run mismatch | ~81 | **fixable** | Often panic text, stack traces, print/ABI |
| Codegen / Wasm validate | 53 | **fixable** | 5 `codegen:` + 48 wasm validate/compile |
| Unsupported: unsafe | 41 | **by design** | `uintptr`↔`Pointer`, rejected ops (`05-unsafe-and-reflect.md`) |
| Other / mono / stack overflow | ~8 | mixed | Host Go stack overflow, mono pending methods, reflect stubs |
| Missing stdlib import | 4 | **fixable** (overlays) | `os/exec`, `unique`, `runtime/pprof`, `encoding/xml`, … |

M10 had codegen/validate **193** and compile gaps **185** as the largest buckets;
burn-down since then moved the lead to **compile gaps** and **runtime**.

### Compile gaps worth early PRs

| Gap | ~N | Why first |
|---|---:|---|
| IR verify / type mismatches | 32 | Complex nullability, assign shapes |
| Typecheck / load failures | 16 | Package load before lower |
| Remaining multi-value / tuple | few | Lower of multi-result calls |

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

## 2. Stdlib per-test skips — **626** (was ~1038 at M10)

| Package | Skips | Dominant root cause | Fixability |
|---|---:|---|---|
| encoding/json | 171 | Blanket + 2× StructOf | StructOf **by design**; rest fixable |
| time | 145 | Thin `time` rewrite vs upstream tests | Overlay/rewrite depth |
| bytes | 80 | Test file typecheck/load (65), validate (13) | Compile gaps |
| strings | 61 | Test file typecheck/load (58) | Compile gaps |
| slices | 54 | Typecheck (45), runtime/assert (9) | Compile + runtime |
| fmt | 51 | Blanket lower/runtime (typecheck of `_test`) | Depends on reflect/print |
| sort | 27 | Tables / pdqsort / stable | Runtime/algorithm parity |
| math/bits | 25 | `uint64`→`uint` overflow on 32-bit word | Sizes / build tags |
| errors | **4** | `As`/`Is`/`AsType` reflectlite | **Fixable**; Join cleared |
| strconv | **3** | ParseComplex + AllocsPerRun | ParseInt/Atof cleared |
| math | **2** | Sub-width int wrap (`MaxUint`/`MaxInt`) | Truncate i32 adds to Go width |
| maps | 1 | `Clone` large keys / `-0` | Overlay/freeze semantics |
| container/heap | 1 | `sync/atomic` fat pointer | unsafe/atomic subset |
| encoding/gob | 1 | StructOf/ArrayOf | **by design** |
| container/ring | **0** | — | **done** |
| unicode/utf8 | **0** | — | **done** ([PR #24](https://github.com/achille-roussel/wasmgc-go/pull/24)) |
| container/list | 0 | — | Passing |

### Highest-ROI stdlib unlocks (next)

1. **math** `TestMaxUint`/`TestMaxInt` (2) — truncate sub-i32 arithmetic to Go width.
2. **strconv** `TestParseComplex` (1) — complex128 reflect Kind + DeepEqual; AllocsPerRun (2).
3. **errors** `As`/`Is` (4) — reflectlite Set / Implements / Go 1.22 range vars.
4. Unlock **bytes/strings/slices** typecheck failures once lower/load gaps clear.
5. **math/bits** (25) — accept 32-bit `uint` or gate tests.
6. Un-blanket **fmt / json / time** after compile/reflect/overlay depth.

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
| Micro → batch burn-down | GOROOT clusters | **done through #47** (~604/422) |
| strconv Parse* + math blanket + errors.Join | stdlib | **this PR** (−~376 stdlib skips) |
| math MaxUint wrap + complex reflect | stdlib | −3… |
| errors As/Is | stdlib | −4 |
| GOROOT compile/runtime buckets | GOROOT | −15+ each batch |
| Un-blanket fmt/json/time | stdlib | after compile/reflect depth |

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

### After PR #47 (strconv quote/unicode + container/ring)

| Layer | Pass | Fail | Skip |
|---|---:|---:|---:|
| `$GOROOT/test` | **~604** | 0 | **422** |
| Stdlib per-test total | — | — | **~1000** |

### After this PR (bounded shifts + iface DeepEqual + math/strconv/errors)

| Layer | Pass | Fail | Skip |
|---|---:|---:|---:|
| `$GOROOT/test` | **~604** | 0 | **422** |
| math | **~74** package tests | 0 | **2** (was 365) |
| strconv | — | 0 | **3** (was 13) |
| errors | — | 0 | **4** (was 5) |
| Stdlib per-test total | — | — | **626** |

Update this section when a burn-down PR lands.
