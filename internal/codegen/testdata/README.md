# codegen testdata

## Temporary print runtime

`Options.SynthPrintRuntime` synthesizes `runtime.printstring`,
`runtime.printint`, and `runtime.printnl` in Wasm until stream F lands
`std/src/runtime`. The stubs copy bytes through linear memory and call
the host import `go.runtime.write` (see `docs/design/04` and `07`).

Remove `SynthPrintRuntime` and this note once the real runtime package
is linked by the driver.
