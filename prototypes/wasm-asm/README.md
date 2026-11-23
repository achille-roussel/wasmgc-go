# Wasm assembly escape hatch

Proof of concept for the adopted design in [`docs/design/12-wasm-assembly.md`](../../docs/design/12-wasm-assembly.md) (decisions 8–12 in `10`): hand-written WAT that operates on `$go.string` / `(array i8)` headers, merged into a separately emitted "compiler" module with symbolic type names resolved. Temporary `wasm-merge` here matches the optional M6 path (decision 12).

## Layout

| Path | Role |
|---|---|
| `asm/memequal.wat` | Asm kernel; uses `$go:string` sugar |
| `asm/indexbyte.wat` | Asm kernel that imports `compiler.panic_bounds` |
| `compiler/stub.wat` | Stand-in compiler output importing `goasm.memequal` |
| `compiler/indexbyte_driver.wat` | Driver + exported `panic_bounds` |
| `go_binding.go` | Documented body-less Go decls (`//go:build ignore`) |
| `run.mjs` | Resolve → parse → `wasm-merge` → validate → Node |

## Run

```sh
export PATH="/path/to/wasm-tools:/path/to/binaryen/bin:$PATH"
node prototypes/wasm-asm/run.mjs
```

Needs `wasm-tools` (tested 1.246.2) and Binaryen `wasm-merge` ≥113 (tested 133). Node 22+ is enough (GC only). wabt's `wat2wasm` cannot parse these files.

## Results (2026-11-23)

Host: Node.js v22.23.4; wasm-tools 1.246.2; Binaryen wasm-merge 133.

```
ok  memequal equal strings = 1
ok  memequal unequal strings = 0
ok  memequal substring headers share bytes = 1
ok  indexbyte finds e = 1
ok  indexbyte missing → -1 = -1
ok  divergent $go.string shape rejected at merge
node v22.23.4: wasm-asm prototype passed
```

### What worked

- Structural merge of identical GC prelude types across two modules.
- Import/export fusion (`goasm.memequal`, `compiler.panic_bounds`).
- `$go:Name` → `$go.Name` rewrite as a stand-in for linker-resolved symbolic types.
- Fail-closed merge when the asm `$go.string` shape diverges.

### What was hard

- `wasm-merge` needs `--enable-gc`, `--enable-reference-types`, and (for multi-value results) `--enable-multivalue`, or it rejects valid modules.
- Symbolic names do not survive into the binary; production merge must inject the compiler type section or match by structure + export names.
- Tool-conventions `wasm-ld` linking was not usable for this GC shape (see design §3).
