# Skip burn-down categories (post-M10)

Inventory of `$GOROOT/test` and stdlib per-test skips after M10
([PR #22](https://github.com/achille-roussel/wasmgc-go/pull/22)): **380** language
suite passes, **646** skip-listed; stdlib packages green with **~1038** per-test
skips. Priority: functionality first, size later. JSPI always required; CI on
Node 26.13.1.

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

---

## 1. `$GOROOT/test` — 646 skips

### By major category

| Category | Count | Fixability | Notes |
|---:|---:|---|---|
| Codegen / Wasm validate | 193 | **fixable** | 119 `codegen:`, 74 wasm validate/compile |
| Compile gaps (lower/load/IR) | 185 | **fixable** | typecheck 55, println types 39, IR verify 31, lower other 28, untyped nil 17, multi-value 10, literals 5 |
| Runtime panic / trap | 100 | **fixable** | Wrong panic, nil map, iface assert, index, hang (2) |
| Output mismatch | 52 | **fixable** | Often panic text, stack traces, print/ABI |
| Unsupported: unsafe | 38 | **by design** | `uintptr`↔`Pointer`, rejected ops (`05-unsafe-and-reflect.md`) |
| Missing stdlib import | 31 | **fixable** (overlays) | See import table below |
| Language feature gaps | 29 | **fixable** | generics 12, defer forms 6, goto/break 5, range `*array` 5, select 1 |
| Other / mono / stack overflow | 18 | mixed | Host Go stack overflow during compile, mono pending methods |

### Codegen / Wasm validate (193) — largest bucket

Common clusters inside the opaque `codegen:` / validate reasons (from M10
follow-up sweeps):

| Cluster | ~N | Direction |
|---|---:|---|
| Unresolved method / iface call names | ~28 | Codegen of method values / wrappers |
| Missing `runtime.GC` stub | ~26 | Thin runtime stub (no-op or host) |
| Missing `runtime.Caller` / `Callers` | ~17 | Panic/stack metadata; ties to output mismatch |
| Unknown heap type (`$go.slice.i8`, …) | ~16 | Type registration |
| Unknown itab global | ~11 | Itab discovery for assert sites |
| Other `runtime.*` (`GOMAXPROCS`, `Stack`, `Goexit`, …) | ~11 | Stubs |
| Non-defaultable locals / init order | (subset of validate) | Same class as utf8 package-init bug |

### Compile gaps worth early PRs

| Gap | N | Why first |
|---|---:|---|
| `println` type coverage | 39 | Localized: `printArg` + `runtime.print*` |
| Untyped nil context | 17 listed / ~3 live | `append(s, nil)` + method-expr `T.M(nil)` |
| Multi-value / tuple as value | 10 | Lower of multi-result calls |
| Range over `*[N]T` | 5 | Language feature, small surface |
| Labeled goto / break | 5 | Control-flow lowering |

### Missing stdlib imports (31)

| Import | N | Approach |
|---|---:|---|
| `runtime/debug` | 9 | Overlay stub (`Stack`, `PrintStack`, …) |
| `log` | 7 | Overlay or thin rewrite |
| `context` | 4 | Overlay subset |
| `testing` | 3 | Already have rewrite; wire import for language tests |
| `io/ioutil` | 2 | Overlay → `io` / `os` |
| Others (`go/ast`, `crypto/md5`, `path/filepath`, `unique`, `runtime/pprof`, `encoding/xml`) | 1 each | Case-by-case |

### By design (keep)

- **unsafe** conversions and pointer arithmetic (38) — see `docs/design/05-unsafe-and-reflect.md`.
- **cgo / assembly** language tests — auto-skipped or never targeted.
- **reflect.StructOf / ArrayOf** with non-constant field lists — closed universe B; listed under stdlib, not this file.

### Needs WAT / deeper runtime

- Fuller **panic stack traces** (`runtime.Callers` / PC metadata) — stub header only today; many output-mismatch skips.
- **`$go.rangeExit` multi-tag EH** + defer-inside-range-over-func.
- Some map / concurrent-write / finalizer cases may need runtime WAT beyond Go overlays.

---

## 2. Stdlib per-test skips — ~1038

| Package | Skips | Dominant root cause | Fixability |
|---|---:|---|---|
| math | 365 | Blanket lower/runtime gaps | Large; refine after math runtime |
| encoding/json | 171 | Blanket + 2× StructOf | StructOf **by design**; rest fixable |
| time | 145 | Thin `time` rewrite vs upstream tests | Overlay/rewrite depth |
| bytes | 80 | Test file typecheck/load (65), validate (13) | Compile gaps |
| strings | 61 | Test file typecheck/load (58) | Compile gaps |
| slices | 54 | Typecheck (45), runtime/assert (9) | Compile + runtime |
| fmt | 51 | Blanket lower/runtime | Depends on reflect/print |
| sort | 27 | Tables / pdqsort / stable | Runtime/algorithm parity |
| strconv | 26 | Quote/unicode/alloc (13), null-deref (12) | Runtime + tables |
| math/bits | 25 | `uint64`→`uint` overflow on 32-bit word | Sizes / build tags |
| unicode/utf8 | **0** | ~~Package-init / UTF-8 range~~ — cleared in [PR #24](https://github.com/achille-roussel/wasmgc-go/pull/24) | **done** |
| container/ring | 8 | Circular pointer equality | Runtime pointer identity |
| errors | 7 | `iface` assert → `interface{Unwrap() error}` itabs | **Fixable** (assert-site itabs) |
| maps | 1 | `Clone` large keys / `-0` | Overlay/freeze semantics |
| container/heap | 1 | `sync/atomic` fat pointer | unsafe/atomic subset |
| encoding/gob | 1 | StructOf/ArrayOf | **by design** |
| container/list | 0 | — | Passing |

### Highest-ROI stdlib unlocks

1. **unicode/utf8** (15) — zero-init non-defaultable locals in synthetic package-var init (`internal/lower/pkg.go`). One bug blocks the whole package.
2. **errors** (5–7) — synthesize itabs for iface→iface assert destinations; restore upstream `Is`/`Unwrap`/`Join` overlays.
3. **maps.CloneLarge** (1) — finish Clone overlay edge cases.
4. **container/ring** (8) — pointer equality / circular links.
5. **strconv** parse paths (12+) — null-deref / table cases (M10 follow-up).
6. **math/bits** (25) — accept 32-bit `uint` or gate tests; not a language skip.
7. Unlock **bytes/strings/slices** typecheck failures once lower/load gaps clear.
8. Only then un-blanket **fmt / json / time / math**.

### By design (stdlib)

| Skip | Reason |
|---|---|
| `encoding/json` StructOf benches/tests | Dynamic field lists outside closed universe B |
| `encoding/gob` StructOf/ArrayOf | Same |
| Unrestricted `reflect.MakeFunc` / similar | Not in supported reflect subset |

---

## 3. Suggested PR series (one category each)

Order prefers unlock count and localization over size work.

| PR | Category | Expected skip delta (approx.) |
|---|---|---|
| A | This doc (+ arm skip growth: drop M10 `skip_bump.txt`) | 0 |
| B | Package-init / copy(nil) / switch / UTF-8 range → utf8 | **done (−15 stdlib, [PR #24](https://github.com/achille-roussel/wasmgc-go/pull/24))** |
| C | Untyped nil (`append` + method-expr) | −3…17 GOROOT |
| D | `println` float/complex | −19…39 GOROOT |
| E | errors Unwrap/Is itabs | −5…7 stdlib |
| F | Runtime stubs (`GC`, `Caller`/`Callers`) | −40+ GOROOT codegen |
| G | Panic/stack output parity | subset of 52 output mismatch |
| H | Stdlib overlays (`runtime/debug`, `log`, …) | −31 GOROOT imports |
| I | ring / strconv / maps.Clone edge cases | −10…20 stdlib |

Each PR description should report before/after pass and skip counts for
`$GOROOT/test` and affected stdlib packages.

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

Update this section when a burn-down PR lands.
