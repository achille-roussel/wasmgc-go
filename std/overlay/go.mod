// Nested module so the compiler module's `go test ./...` does not try to
// type-check overlay sources in isolation. Overlays are full files meant only
// for the materialized GOROOT (see std/README.md).
module github.com/achille-roussel/wasmgc-go/std/overlay

go 1.27
