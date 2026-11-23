# Stdlib test skips

Per-package skip files for `wasmgc-go test` on vendored / materialized stdlib
packages listed in `../packages.txt`. Format matches `../skip.txt`:

```
<TestName> — <reason>
```

## StructOf / reflect constructor skips

These were previously listed in `testdata/gotest/skip.txt` (the `$GOROOT/test`
list). They belong to the stdlib layer, not the language suite:

| Skip | Reason |
|---|---|
| `BenchmarkTypeFieldsCache` | reflect.StructOf (encoding/json/bench_test.go; field list not a constant struct in B) |
| `TestBenchmarkUnmarshalUnknown` | reflect.StructOf (encoding/json/v2/fold_test.go; Name0… field list not constant) |
| `Structs/OmitEmpty/PathologicalBreadth` | reflect.StructOf (encoding/json/v2/arshal_test.go; X0…X99 field list not constant) |
| `gobencdec StructOf/ArrayOf` | reflect.StructOf/ArrayOf (encoding/gob/gobencdec_test.go; nested type not in B) |

See `docs/design/05-unsafe-and-reflect.md` and `08-testing.md`.
