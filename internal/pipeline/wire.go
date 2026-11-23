package pipeline

// Default is the backends used by cmd/wasmgc-go.
//
// Codegen (A') and stdlib (H) are real. Lower is stubbed until stream D lands.
//
// Integration when D lands (one-file change):
//
//	Lower: lowerAdapter{}, // calls lower.Package(...)
//
// Then Backends.Real() becomes true and TestM1EmptyMainE2E stops skipping.
var Default = Backends{
	Lower:   &stubLowerer{},
	Codegen: codegenBackend{},
	Stdlib:  &stdlibBackend{},
}
