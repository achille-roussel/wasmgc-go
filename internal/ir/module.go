package ir

// Module is one compilation unit: types, globals, data, functions.
type Module struct {
	Types   []*TypeDef
	Globals []*Global
	Data    []*Data
	Funcs   []*Func
	// Low is false until the runtime-lowering pass finishes.
	// Codegen refuses a module that is not Low.
	Low bool
	// AssertItabs lists $go.itab.<J>.@.<C> globals that iface→iface asserts
	// may global.get. Nil means unrestricted (tests / pre-desc modules).
	AssertItabs map[string]bool
	// Filename is the source path for diagnostics (set by the parser).
	Filename string
}

// Global is a module-level mutable or immutable value.
type Global struct {
	Name string
	Typ  Type
	Mut  bool
	Init Expr // may be nil (zero / null)
	Pos  Pos
}

// Data is a passive data segment for array.new_data (string / []byte literals).
type Data struct {
	Name  string
	Bytes []byte
	Pos   Pos
}

// Func is one function.
type Func struct {
	Name    string
	Export  string // empty if not exported; otherwise export name
	Params  []*Var
	Results []Type
	Body    Region // typically *Seq
	// HasDispatch is set when Body contains a Dispatch region.
	HasDispatch bool
	Pos         Pos

	// Locals collected from var declarations in the body (not including params).
	// Filled by the parser / lowerer; the verifier checks consistency.
	Locals []*Var
}

// LookupType returns the TypeDef with the given name, or nil.
func (m *Module) LookupType(name string) *TypeDef {
	for _, t := range m.Types {
		if t.Name == name {
			return t
		}
	}
	return nil
}

// LookupFunc returns the Func with the given name, or nil.
func (m *Module) LookupFunc(name string) *Func {
	for _, f := range m.Funcs {
		if f.Name == name {
			return f
		}
	}
	return nil
}

// LookupExport returns the first Func with the given Wasm export name, or nil.
func (m *Module) LookupExport(export string) *Func {
	if m == nil || export == "" {
		return nil
	}
	for _, f := range m.Funcs {
		if f.Export == export {
			return f
		}
	}
	return nil
}

// RunEntryName is the preferred IR name for the Wasm export "run" wrapper.
// When the program defines func run (also IR-named main.run), Finish uses
// AltRunEntryName instead so the user function is not mistaken for the entry.
const (
	RunEntryName    = "main.run"
	AltRunEntryName = "$go.run"
)

// LookupGlobal returns the Global with the given name, or nil.
func (m *Module) LookupGlobal(name string) *Global {
	for _, g := range m.Globals {
		if g.Name == name {
			return g
		}
	}
	return nil
}

// LookupData returns the Data segment with the given name, or nil.
func (m *Module) LookupData(name string) *Data {
	for _, d := range m.Data {
		if d.Name == name {
			return d
		}
	}
	return nil
}

// InternType adds t if no type of that name exists, or returns the existing one.
func (m *Module) InternType(t *TypeDef) *TypeDef {
	if existing := m.LookupType(t.Name); existing != nil {
		return existing
	}
	m.Types = append(m.Types, t)
	return t
}
