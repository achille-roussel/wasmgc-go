# Runtime functions that want `*_wasmgc.wat` later

The WAT escape hatch ([`12-wasm-assembly.md`](../../../docs/design/12-wasm-assembly.md)) is adopted but not wired. Until then, bodies live in Go (M1/M2 subset) or as codegen intrinsics. Default `nosuspend` in this package (decision 11).

| Symbol | Why WAT | Current stand-in |
|---|---|---|
| `printstring` | Bridge loop: `array.get_u` → `i32.store8` → `go.runtime.write` | Codegen intrinsic on empty IR body |
| `printint` (optional) | Compact digit buffer in linear memory (smaller than Go digit strings) | Go `printuint` via `printstring` (linked) |
| `write` (bridge half) | Same loop; host import is already `go.runtime.write` | Folded into printstring intrinsic |
| `memequal` / `memmove` | Tight `(array i8)` loops | Not yet linked |
| map hash of `string` / `[]byte` | Hot M6 path | Go scaffold when M6 lands |
| `deferRun` catch / `throw` edges | EH-heavy; clearer in WAT | Go scaffold + `testdata/m5_defer_panic.wat` |
| `mapAssign` / `mapAccess` hash kernels | Hot byte/string hashing | Go scaffold + `testdata/m6_map.wat` |

Generic Go remains correct; WAT is an optimization lane, not a link requirement.
