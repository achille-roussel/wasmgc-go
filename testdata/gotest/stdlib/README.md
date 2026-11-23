# Stdlib test skips

Per-package skip files for `wasmgc-go test` on packages listed in
`../packages.txt`. Layout:

```
stdlib/<import/path>/skip.txt
```

Format (em dash required):

```
<TestName> — <reason>
```

## How to run

```bash
export PATH="/home/ubuntu/.local/go/bin:/home/ubuntu/.local/node-v26.13.1/bin:$PATH"
export WASMGC_GOROOT="${WASMGC_GOROOT:-/home/ubuntu/.local/go-src}"

# One package
go build -o /tmp/wasmgc-go ./cmd/wasmgc-go
/tmp/wasmgc-go test -v errors

# Smoke (errors, unicode/utf8, container/ring)
go test ./testdata/gotest/ -run TestStdlibPackages -count=1 -short

# Full packages.txt suite
WASMGC_STDLIB_TEST=1 go test ./testdata/gotest/ -run TestStdlibPackages -count=1
```

## StructOf / reflect constructor skips

Historical StructOf entries for encoding/json (and gob) live under
`encoding/json/skip.txt` and `encoding/gob/skip.txt`. See
`docs/design/05-unsafe-and-reflect.md`.
