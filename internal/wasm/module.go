package wasm

import "fmt"

// Section IDs.
const (
	SectionCustom    byte = 0
	SectionType      byte = 1
	SectionImport    byte = 2
	SectionFunction  byte = 3
	SectionTable     byte = 4
	SectionMemory    byte = 5
	SectionGlobal    byte = 6
	SectionExport    byte = 7
	SectionStart     byte = 8
	SectionElement   byte = 9
	SectionCode      byte = 10
	SectionData      byte = 11
	SectionDataCount byte = 12
	SectionTag       byte = 13
)

// External kinds.
const (
	ExtFunc   byte = 0x00
	ExtTable  byte = 0x01
	ExtMem    byte = 0x02
	ExtGlobal byte = 0x03
	ExtTag    byte = 0x04
)

// Import describes an imported item.
type Import struct {
	Module string
	Name   string
	Kind   byte
	// Kind-specific:
	TypeIdx    uint32     // ExtFunc, ExtTag
	TableType  TableType  // ExtTable
	MemType    Limits     // ExtMem
	GlobalType GlobalType // ExtGlobal
}

// TableType is an element reftype plus limits.
type TableType struct {
	Elem   ValType
	Limits Limits
}

func (t TableType) encode(buf []byte) []byte {
	buf = t.Elem.encode(buf)
	return t.Limits.encode(buf)
}

// Tag is an exception tag (attribute 0 + type index of a func type).
type Tag struct {
	TypeIdx uint32
}

// Global is a global definition with an init expression.
type Global struct {
	Type GlobalType
	Init []byte // encoded const expr ending with End
}

// ExportKind aliases Ext* for clarity at export sites.
type ExportKind = byte

// Export is a named export.
type Export struct {
	Name  string
	Kind  ExportKind
	Index uint32
}

// Element modes.
const (
	ElemActive byte = iota
	ElemPassive
	ElemDeclarative
)

// Element is an element segment.
type Element struct {
	Mode     byte
	TableIdx uint32  // active
	Offset   []byte  // active init expr
	Type     ValType // passive/declarative/active with explicit type
	FuncIdxs []uint32
	Exprs    [][]byte // encoded ref exprs; if non-nil, used instead of FuncIdxs
	HasType  bool
}

// Data is a data segment.
type Data struct {
	Passive  bool
	MemIdx   uint32
	Offset   []byte // active
	Bytes    []byte
}

// Code is a function body.
type Code struct {
	Locals []Local
	Body   []byte // instructions, without the trailing End (Encode adds it)
}

// Local is a run of identical local types.
type Local struct {
	Count uint32
	Type  ValType
}

// NameSection is the optional custom "name" section.
type NameSection struct {
	ModuleName string
	Functions  []FuncName
	Locals     []LocalName
	Types      []TypeName
}

// FuncName maps a function index to a name.
type FuncName struct {
	Index uint32
	Name  string
}

// LocalName maps (func, local) to a name.
type LocalName struct {
	FuncIndex  uint32
	LocalIndex uint32
	Name       string
}

// TypeName maps a type index to a name.
type TypeName struct {
	Index uint32
	Name  string
}

// Module is a Wasm module under construction.
type Module struct {
	Types    []RecGroup
	Imports  []Import
	Funcs    []uint32 // type index per defined function
	Tables   []TableType
	Memories []Limits
	Tags     []Tag
	Globals  []Global
	Exports  []Export
	Start    *uint32
	Elements []Element
	Codes    []Code
	Datas    []Data
	Names    *NameSection

	// EmitDataCount controls whether a DataCount section is written.
	// Default true when Datas is non-empty (required when code uses data indexes).
	EmitDataCount bool
	dataCountSet  bool
}

// NewModule returns an empty module.
func NewModule() *Module {
	return &Module{}
}

// NumTypes returns the total number of type indices defined so far.
func (m *Module) NumTypes() uint32 {
	var n uint32
	for _, g := range m.Types {
		n += uint32(len(g.Types))
	}
	return n
}

// AddRecGroup appends a recursive type group and returns the starting type index.
func (m *Module) AddRecGroup(g RecGroup) uint32 {
	idx := m.NumTypes()
	m.Types = append(m.Types, g)
	return idx
}

// AddType appends a singleton (non-rec) type and returns its index.
func (m *Module) AddType(t SubType) uint32 {
	return m.AddRecGroup(Singleton(t))
}

// AddFuncType appends a final function type and returns its index.
func (m *Module) AddFuncType(params, results []ValType) uint32 {
	return m.AddType(FinalType(FuncType(params, results)))
}

// AddStructType appends a final struct type and returns its index.
func (m *Module) AddStructType(fields ...FieldType) uint32 {
	return m.AddType(FinalType(StructType(fields...)))
}

// AddArrayType appends a final array type and returns its index.
func (m *Module) AddArrayType(elem FieldType) uint32 {
	return m.AddType(FinalType(ArrayType(elem)))
}

// ImportFunc adds a function import; returns the function index.
func (m *Module) ImportFunc(module, name string, typeIdx uint32) uint32 {
	idx := m.numImportedFuncs()
	m.Imports = append(m.Imports, Import{Module: module, Name: name, Kind: ExtFunc, TypeIdx: typeIdx})
	return idx
}

// ImportTag adds a tag import; returns the tag index.
func (m *Module) ImportTag(module, name string, typeIdx uint32) uint32 {
	idx := m.numImportedTags()
	m.Imports = append(m.Imports, Import{Module: module, Name: name, Kind: ExtTag, TypeIdx: typeIdx})
	return idx
}

// ImportMemory adds a memory import; returns the memory index.
func (m *Module) ImportMemory(module, name string, lim Limits) uint32 {
	idx := m.numImportedMemories()
	m.Imports = append(m.Imports, Import{Module: module, Name: name, Kind: ExtMem, MemType: lim})
	return idx
}

// ImportGlobal adds a global import; returns the global index.
func (m *Module) ImportGlobal(module, name string, gt GlobalType) uint32 {
	idx := m.numImportedGlobals()
	m.Imports = append(m.Imports, Import{Module: module, Name: name, Kind: ExtGlobal, GlobalType: gt})
	return idx
}

// ImportTable adds a table import; returns the table index.
func (m *Module) ImportTable(module, name string, tt TableType) uint32 {
	idx := m.numImportedTables()
	m.Imports = append(m.Imports, Import{Module: module, Name: name, Kind: ExtTable, TableType: tt})
	return idx
}

func (m *Module) numImportedFuncs() uint32 {
	var n uint32
	for _, im := range m.Imports {
		if im.Kind == ExtFunc {
			n++
		}
	}
	return n
}

func (m *Module) numImportedTags() uint32 {
	var n uint32
	for _, im := range m.Imports {
		if im.Kind == ExtTag {
			n++
		}
	}
	return n
}

func (m *Module) numImportedMemories() uint32 {
	var n uint32
	for _, im := range m.Imports {
		if im.Kind == ExtMem {
			n++
		}
	}
	return n
}

func (m *Module) numImportedGlobals() uint32 {
	var n uint32
	for _, im := range m.Imports {
		if im.Kind == ExtGlobal {
			n++
		}
	}
	return n
}

func (m *Module) numImportedTables() uint32 {
	var n uint32
	for _, im := range m.Imports {
		if im.Kind == ExtTable {
			n++
		}
	}
	return n
}

// AddFunc declares a defined function with the given type index.
// The body must be supplied later via SetCode / AddCode in matching order.
// Returns the function index (imports first).
func (m *Module) AddFunc(typeIdx uint32) uint32 {
	idx := m.numImportedFuncs() + uint32(len(m.Funcs))
	m.Funcs = append(m.Funcs, typeIdx)
	return idx
}

// AddCode appends a function body. Must match AddFunc order.
func (m *Module) AddCode(c Code) {
	m.Codes = append(m.Codes, c)
}

// DefineFunc is AddFunc + AddCode in one step. Returns the function index.
func (m *Module) DefineFunc(typeIdx uint32, c Code) uint32 {
	idx := m.AddFunc(typeIdx)
	m.AddCode(c)
	return idx
}

// AddMemory appends a memory and returns its index.
func (m *Module) AddMemory(lim Limits) uint32 {
	idx := m.numImportedMemories() + uint32(len(m.Memories))
	m.Memories = append(m.Memories, lim)
	return idx
}

// AddTable appends a table and returns its index.
func (m *Module) AddTable(tt TableType) uint32 {
	idx := m.numImportedTables() + uint32(len(m.Tables))
	m.Tables = append(m.Tables, tt)
	return idx
}

// AddTag appends a tag and returns its index.
func (m *Module) AddTag(typeIdx uint32) uint32 {
	idx := m.numImportedTags() + uint32(len(m.Tags))
	m.Tags = append(m.Tags, Tag{TypeIdx: typeIdx})
	return idx
}

// NextGlobalIndex returns the index the next AddGlobal would assign.
func (m *Module) NextGlobalIndex() uint32 {
	return m.numImportedGlobals() + uint32(len(m.Globals))
}

// AddGlobal appends a global and returns its index.
func (m *Module) AddGlobal(gt GlobalType, init []byte) uint32 {
	idx := m.NextGlobalIndex()
	m.Globals = append(m.Globals, Global{Type: gt, Init: init})
	return idx
}

// ExportFunc exports a function by name.
func (m *Module) ExportFunc(name string, funcIdx uint32) {
	m.Exports = append(m.Exports, Export{Name: name, Kind: ExtFunc, Index: funcIdx})
}

// ExportMemory exports a memory by name.
func (m *Module) ExportMemory(name string, memIdx uint32) {
	m.Exports = append(m.Exports, Export{Name: name, Kind: ExtMem, Index: memIdx})
}

// ExportGlobal exports a global by name.
func (m *Module) ExportGlobal(name string, globalIdx uint32) {
	m.Exports = append(m.Exports, Export{Name: name, Kind: ExtGlobal, Index: globalIdx})
}

// ExportTable exports a table by name.
func (m *Module) ExportTable(name string, tableIdx uint32) {
	m.Exports = append(m.Exports, Export{Name: name, Kind: ExtTable, Index: tableIdx})
}

// ExportTag exports a tag by name.
func (m *Module) ExportTag(name string, tagIdx uint32) {
	m.Exports = append(m.Exports, Export{Name: name, Kind: ExtTag, Index: tagIdx})
}

// SetStart sets the start function index.
func (m *Module) SetStart(funcIdx uint32) {
	m.Start = &funcIdx
}

// AddDeclarativeFuncs adds a declarative element segment of funcrefs
// (needed so ref.func is valid for those indices).
func (m *Module) AddDeclarativeFuncs(funcIdxs ...uint32) {
	m.Elements = append(m.Elements, Element{
		Mode:     ElemDeclarative,
		Type:     FuncRef(),
		FuncIdxs: append([]uint32(nil), funcIdxs...),
		HasType:  true,
	})
}

// AddPassiveData adds a passive data segment; returns its index.
func (m *Module) AddPassiveData(b []byte) uint32 {
	idx := uint32(len(m.Datas))
	m.Datas = append(m.Datas, Data{Passive: true, Bytes: append([]byte(nil), b...)})
	m.EmitDataCount = true
	m.dataCountSet = true
	return idx
}

// AddActiveData adds an active data segment for memory 0.
func (m *Module) AddActiveData(offset []byte, b []byte) uint32 {
	idx := uint32(len(m.Datas))
	m.Datas = append(m.Datas, Data{Offset: offset, Bytes: append([]byte(nil), b...)})
	return idx
}

// Validate performs cheap structural checks that make invalid modules hard
// to construct. It does not implement the full Wasm validator.
func (m *Module) Validate() error {
	if len(m.Funcs) != len(m.Codes) {
		return fmt.Errorf("wasm: %d function types but %d code bodies", len(m.Funcs), len(m.Codes))
	}
	nTypes := m.NumTypes()
	for i, t := range m.Funcs {
		if t >= nTypes {
			return fmt.Errorf("wasm: function %d type index %d out of range", i, t)
		}
	}
	for i, im := range m.Imports {
		if im.Kind == ExtFunc || im.Kind == ExtTag {
			if im.TypeIdx >= nTypes {
				return fmt.Errorf("wasm: import %d type index %d out of range", i, im.TypeIdx)
			}
		}
	}
	for i, tag := range m.Tags {
		if tag.TypeIdx >= nTypes {
			return fmt.Errorf("wasm: tag %d type index %d out of range", i, tag.TypeIdx)
		}
	}
	names := map[string]bool{}
	for _, e := range m.Exports {
		if names[e.Name] {
			return fmt.Errorf("wasm: duplicate export %q", e.Name)
		}
		names[e.Name] = true
	}
	return nil
}
