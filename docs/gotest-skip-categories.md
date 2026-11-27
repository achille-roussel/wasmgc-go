# Skip burn-down categories (post-M10)

Inventory of `$GOROOT/test` and stdlib per-test skips. M10 baseline
([PR #22](https://github.com/achille-roussel/wasmgc-go/pull/22)): **380** language
suite passes, **646** skip-listed; stdlib packages green with **~1038** per-test
skips. Priority: functionality first, size later. JSPI always required; CI on
Node 26.13.1.

**Current (this PR):** `$GOROOT/test` **149** skip-listed; stdlib per-test **171**.
Root-cause unlocks: zero-size embed field gets, nil iface assert panic text,
mono iface subst for type switches, panicwrap for nil `*T` value-method iface
calls, mixed float/int struct-array packing. Size: `time.unitLookup` (no
package-level `unitMap`) drops map runtime from os→io/fs→time import edges
(hello ~248 KiB → ~230 KiB). issue11256 skipped after that DCE exposes a
Goexit/Sleep timing deadlock (no stack barriers on Wasm GC).

Auto-skipped `$GOROOT/test` actions (`errorcheck*`, `compile*`, `rundir`,
`asmcheck`, …) and build-tag exclusions are **not** in `skip.txt`. This doc
covers committed skip lists only.

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

## 1. `$GOROOT/test` — 149 skips (was 646 at M10)

### By design (keep)

- **unsafe** conversions and pointer arithmetic.
- **reflect.StructOf / ArrayOf** with non-constant field lists — closed universe B.
- **alloc-counting** stdlib tests that assert exact `AllocsPerRun` under host GC.

---

## 2. Stdlib per-test skips — **171** (was ~1038 at M10)

| Package | Skips | Notes |
|---|---:|---|
| encoding/json | 69 | −3 this PR (NumberIsValid, TimeDurations, UnmarshalRecursivePointer) |
| time | 91 | (unitLookup size fix; no test unskips — zoneinfo/export_test gaps) |
| fmt | 3 | −1 TestPanics; AllocsPerRun + scan leftovers |
| slices | 5 | Inference/Nilness mono; AllocsPerRun; Concat/Repeat runtime |
| strings | 2 | AllocsPerRun only |
| encoding/gob | 1 | StructOf/ArrayOf |

### Highest-ROI unlocks (next)

1. Closure/composite-lit nil-deref (nilptr2) — traps instead of panicNil.
2. AssertItabs “missing method” (issue77779 / issue47925b).
3. json encode_test / v2_* batches; time zoneinfo/export_test depth.
4. GOROOT compile/runtime buckets.

---

## 3. This PR

| Layer | Before (#73 / #71) | After |
|---|---:|---:|
| `$GOROOT/test` skips | 159 | **149** (−11 unskip +1 issue11256) |
| Stdlib per-test skips | 175 | **171** (−4) |
| m8_fmt_hello (names off) | ~248 KiB | **~230 KiB** |

