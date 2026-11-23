package wasm

// EmptyRunModule builds the M0 module: a type section, one function that
// returns immediately, and an export named "run". No imports, no memory,
// no tags. Suitable for Node 22 (no Suspending).
func EmptyRunModule() *Module {
	m := NewModule()
	ft := m.AddFuncType(nil, nil)
	enc := NewEncoder()
	// Empty body: fall off the end (Encode appends End).
	m.DefineFunc(ft, CodeFrom(nil, enc))
	m.ExportFunc("run", 0)
	return m
}
