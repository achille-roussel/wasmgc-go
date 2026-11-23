// Package codegen lowers verified low IR to a Wasm GC module.
//
// Stream A' of the bootstrap plan (docs/design/09-bootstrap-plan.md).
//
// # API
//
//	bytes, err := codegen.Module(m, codegen.Options{Names: false})
//
// m must have Low set. Module runs ir.Verify (which fills Br.Depth and
// Switch.Dense) then emits a binary module via internal/wasm.
//
// Options.Names controls the name custom section (off for size builds).
// Options.SynthPrintRuntime is temporary: it synthesizes
// runtime.printstring / printint / printnl until stream F lands
// std/src/runtime.
//
// # Type intern table
//
// Every module starts with the prelude from docs/design/03-type-mapping.md
// at fixed indices (IdxGoObject … IdxGoPanicObj). Program TypeDefs follow
// in IR module order. Function signatures are interned structurally.
// Stream G should consume the same indexing.
//
// # Region walk
//
// seq / block / loop / if / switch(dense→br_table) / dispatch map 1:1 to
// Wasm structured control as in docs/design/02-ir.md. try is not emitted
// until M5.
//
// PrepareForCodegen / LowerStrings help tests turn IR goldens that still
// contain high-level string literals into low modules; stream D owns the
// real lowering.
package codegen
