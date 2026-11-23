// Package desc is stream G: type descriptors and itables.
//
// It consumes the IR module after PackageHigh/escape and before RuntimeOps,
// sharing the type intern table conventions with codegen (IdxGoType etc.).
// See docs/design/03-type-mapping.md and 09-bootstrap-plan.md M4.
package desc

import (
	"fmt"
	"go/types"
	"strconv"
	"strings"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
	"github.com/achille-roussel/wasmgc-go/internal/load"
)

// Kind constants match reflect.Kind / abi.Kind for descriptor field $kind.
const (
	KindBool = iota + 1
	KindInt
	KindInt8
	KindInt16
	KindInt32
	KindInt64
	KindUint
	KindUint8
	KindUint16
	KindUint32
	KindUint64
	KindUintptr
	KindFloat32
	KindFloat64
	KindComplex64
	KindComplex128
	KindArray
	KindChan
	KindFunc
	KindInterface
	KindMap
	KindPointer
	KindSlice
	KindString
	KindStruct
	KindUnsafePointer
)

// Populate emits type descriptor globals, itable types/globals, boxing
// helpers, equal stubs, and method wrappers required by iface ops in mod.
// prog supplies go/types for method sets and embedding.
func Populate(mod *ir.Module, prog *load.Program) error {
	if mod == nil {
		return fmt.Errorf("desc.Populate: nil module")
	}
	g := &gen{
		mod:      mod,
		prog:     prog,
		types:    map[string]*typeRec{},
		itabs:    map[string]*itabRec{},
		ifaces:   map[string]bool{},
		wrappers: map[string]bool{},
		equals:   map[string]bool{},
		boxes:    map[string]bool{},
	}
	g.ensurePreludeShapes()
	g.scanModule()
	if prog != nil {
		g.collectFromProgram()
	}
	if err := g.emitAll(); err != nil {
		return err
	}
	return nil
}

type typeRec struct {
	key        string
	goType     types.Type // may be nil for synthetic
	global     string
	id         int32
	kind       int
	name       string
	comparable bool
	ptrElem    string // type key of element for pointers
}

type itabRec struct {
	key      string // full itab global name
	ifaceKey string
	concKey  string
	iface    *types.Interface
	concrete types.Type
	typeName string // Wasm itab struct type name
	methods  []methodSlot
}

type methodSlot struct {
	name    string
	wrapFn  string
	sigName string // funcref type name
}

type gen struct {
	mod      *ir.Module
	prog     *load.Program
	types    map[string]*typeRec
	itabs    map[string]*itabRec
	ifaces   map[string]bool // iface keys needed for OpIfaceCall (itabtype)
	wrappers map[string]bool
	equals   map[string]bool
	boxes    map[string]bool
	nextID   int32
}

func (g *gen) ensurePreludeShapes() {
	// Mirror codegen prelude so IR verify can resolve struct.get field types.
	// Codegen skips names already present when emitting the real prelude.
	g.mod.InternType(&ir.TypeDef{Name: "$go.object", Kind: ir.TypeStruct})
	g.mod.InternType(&ir.TypeDef{
		Name: "$go.bytes", Kind: ir.TypeArray, Elem: ir.TypeI8, ElemMut: true,
	})
	g.mod.InternType(&ir.TypeDef{
		Name:  "$go.string",
		Kind:  ir.TypeStruct,
		Super: "$go.object",
		Fields: []ir.Field{
			{Name: "data", Type: &ir.Ref{Named: "$go.bytes"}},
			{Name: "off", Type: ir.TypeI32},
			{Name: "len", Type: ir.TypeI32},
		},
	})
	g.mod.InternType(&ir.TypeDef{
		Name:  "$go.type",
		Kind:  ir.TypeStruct,
		Super: "$go.object",
		Fields: []ir.Field{
			{Name: "id", Type: ir.TypeI32},
			{Name: "kind", Type: ir.TypeI32},
			{Name: "name", Type: &ir.Ref{Null: true, Named: "$go.string"}},
			{Name: "extra", Type: &ir.Ref{Null: true, Named: "$go.object"}},
		},
	})
	g.mod.InternType(&ir.TypeDef{
		Name:  "$go.itab",
		Kind:  ir.TypeStruct,
		Super: "$go.object",
		Fields: []ir.Field{
			{Name: "type", Type: &ir.Ref{Named: "$go.type"}},
		},
	})
	g.mod.InternType(&ir.TypeDef{
		Name:  "$go.iface",
		Kind:  ir.TypeStruct,
		Super: "$go.object",
		Fields: []ir.Field{
			{Name: "itab", Type: &ir.Ref{Named: "$go.itab"}},
			{Name: "data", Type: &ir.Ref{Null: true, Heap: ir.HeapAny}},
		},
	})
	// Box types for scalar → interface.
	// $go.box.string is not part of the unconditional prelude (empty_main size
	// budget); intern it lazily via ensureBoxString when reflect New/Set needs it.
	for _, kind := range []struct {
		name string
		ty   ir.Type
	}{
		{"$go.box.i32", ir.TypeI32},
		{"$go.box.i64", ir.TypeI64},
		{"$go.box.f32", ir.TypeF32},
		{"$go.box.f64", ir.TypeF64},
	} {
		g.mod.InternType(&ir.TypeDef{
			Name:  kind.name,
			Kind:  ir.TypeStruct,
			Super: "$go.object",
			Fields: []ir.Field{
				{Name: "v", Type: kind.ty, Mut: true},
			},
		})
	}
	// lower may have interned boxes first with immutable v; patch for Set*.
	// Include $go.box.string when present (added lazily by ensureBoxString).
	for _, name := range []string{"$go.box.i32", "$go.box.i64", "$go.box.f32", "$go.box.f64", "$go.box.string"} {
		if td := g.mod.LookupType(name); td != nil {
			for i := range td.Fields {
				if td.Fields[i].Name == "v" {
					td.Fields[i].Mut = true
				}
			}
		}
	}
}

func (g *gen) scanModule() {
	for _, f := range g.mod.Funcs {
		if f.Body == nil {
			continue
		}
		walkRegion(f.Body, func(e ir.Expr) {
			in, ok := e.(*ir.Instr)
			if !ok {
				return
			}
			switch in.Op {
			case ir.OpIfaceMake:
				if in.Global != "" {
					g.needItabFromName(in.Global)
				}
				if in.Str != "" && strings.HasPrefix(in.Str, "type:") {
					g.needTypeKey(strings.TrimPrefix(in.Str, "type:"))
				}
			case ir.OpIfaceAssert:
				if in.Global != "" {
					g.needTypeKey(in.Global) // type key
				}
			case ir.OpChanRecv, ir.OpChanSend:
				// Typed moves / comma-ok unbox need the element descriptor
				// even when the program never boxes that type via iface.make
				// (e.g. recv-only from a closed chan).
				if in.Global != "" {
					g.needTypeKey(in.Global)
				}
			case ir.OpConv:
				if in.Str == "iface.eq" {
					// equality uses descriptors already registered via makes
				}
			case ir.OpIfaceCall:
				// Need itabtype layout even when no concrete itab was made yet
				// (e.g. type-assert to Formatter then call Format).
				if in.Global != "" {
					g.ifaces[in.Global] = true
					g.needTypeKey(in.Global)
				}
			}
		})
	}
}

// OpIfaceEq may not exist yet — we use a high-level convention via Str on comparisons.
// Register need helpers.

func (g *gen) needTypeKey(key string) {
	key = strings.TrimPrefix(key, "$go.type.")
	if key == "" {
		return
	}
	if _, ok := g.types[key]; ok {
		return
	}
	g.types[key] = &typeRec{
		key:    key,
		global: "$go.type." + key,
		id:     -1,
	}
}

func (g *gen) needItabFromName(name string) {
	// $go.itab.<iface>.<concrete> — concrete may contain dots.
	const prefix = "$go.itab."
	if !strings.HasPrefix(name, prefix) {
		return
	}
	rest := strings.TrimPrefix(name, prefix)
	// Split on first ".@" separator if present; else last-known scheme ifaceKey@concKey
	var ifaceKey, concKey string
	if i := strings.Index(rest, ".@."); i >= 0 {
		ifaceKey = rest[:i]
		concKey = rest[i+3:]
	} else if i := strings.Index(rest, "@"); i >= 0 {
		ifaceKey = rest[:i]
		concKey = rest[i+1:]
	} else {
		// Fallback: split on first dot for iface, rest concrete — fragile.
		ifaceKey, concKey, _ = strings.Cut(rest, ".")
	}
	if _, ok := g.itabs[name]; ok {
		return
	}
	g.itabs[name] = &itabRec{
		key:      name,
		ifaceKey: ifaceKey,
		concKey:  concKey,
		typeName: "$go.itabtype." + sanitize(ifaceKey),
	}
	g.needTypeKey(concKey)
	g.needTypeKey(ifaceKey)
}

func (g *gen) collectFromProgram() {
	if g.prog == nil {
		return
	}
	for _, pkg := range g.prog.Packages {
		if pkg == nil || pkg.Types == nil {
			continue
		}
		scope := pkg.Types.Scope()
		for _, name := range scope.Names() {
			obj := scope.Lookup(name)
			if tn, ok := obj.(*types.TypeName); ok {
				key := TypeKey(tn.Type())
				g.needTypeKey(key)
				if tr := g.types[key]; tr != nil {
					tr.goType = tn.Type()
				}
			}
		}
		// Anonymous structs/slices/maps appear in TypesInfo but not in the
		// package scope — attach them so struct extras (NumField/getField) work.
		if pkg.Info != nil {
			for _, tv := range pkg.Info.Types {
				if tv.Type == nil {
					continue
				}
				g.attachGoType(tv.Type)
			}
			for _, obj := range pkg.Info.Defs {
				if obj == nil {
					continue
				}
				g.attachGoType(obj.Type())
			}
			for _, obj := range pkg.Info.Uses {
				if obj == nil {
					continue
				}
				g.attachGoType(obj.Type())
			}
		}
	}
	// Resolve go types for itab keys.
	for _, it := range g.itabs {
		it.concrete = g.lookupType(it.concKey)
		it.iface = g.lookupIface(it.ifaceKey)
		if tr := g.types[it.concKey]; tr != nil && tr.goType == nil {
			tr.goType = it.concrete
		}
		if tr := g.types[it.ifaceKey]; tr != nil && tr.goType == nil && it.iface != nil {
			tr.goType = it.iface
		}
	}
	g.closeUniverse()
}

// closeUniverse extends the descriptor set with one constructor step:
// *T, []T, and map[T]struct{} for every comparable T already in B.
// See docs/design/05-unsafe-and-reflect.md (universe U = B ∪ …).
func (g *gen) closeUniverse() {
	type pair struct {
		key string
		typ types.Type
	}
	var base []pair
	for key, tr := range g.types {
		if tr.goType == nil {
			continue
		}
		base = append(base, pair{key: key, typ: tr.goType})
	}
	empty := types.NewStruct(nil, nil)
	for _, p := range base {
		t := p.typ
		// Skip runtime layout aliases and other runtime package types: their
		// Wasm shapes ($go.hmap, $go.semaEntry, …) are owned by lower/rtlink
		// and are not part of the user-visible reflect universe U.
		if n, ok := types.Unalias(t).(*types.Named); ok {
			if runtimeStructAliasName(n) != "" {
				continue
			}
			if obj := n.Obj(); obj != nil && obj.Pkg() != nil &&
				(obj.Pkg().Path() == "runtime" || obj.Pkg().Name() == "runtime") {
				continue
			}
		}
		// *T
		ptr := types.NewPointer(t)
		pk := TypeKey(ptr)
		g.needTypeKey(pk)
		if tr := g.types[pk]; tr != nil && tr.goType == nil {
			tr.goType = ptr
		}
		// []T
		sl := types.NewSlice(t)
		sk := TypeKey(sl)
		g.needTypeKey(sk)
		if tr := g.types[sk]; tr != nil && tr.goType == nil {
			tr.goType = sl
		}
		// map[T]struct{} when T is comparable
		if types.Comparable(t) {
			m := types.NewMap(t, empty)
			mk := TypeKey(m)
			g.needTypeKey(mk)
			if tr := g.types[mk]; tr != nil && tr.goType == nil {
				tr.goType = m
			}
		}
	}
}

func (g *gen) attachGoType(t types.Type) {
	if t == nil {
		return
	}
	key := TypeKey(t)
	if key == "" {
		return
	}
	// Only fill types we already need (from iface.make / itabs / asserts).
	tr := g.types[key]
	if tr == nil {
		return
	}
	if tr.goType == nil {
		tr.goType = t
	}
}

func (g *gen) lookupType(key string) types.Type {
	if tr, ok := g.types[key]; ok && tr.goType != nil {
		return tr.goType
	}
	if g.prog == nil {
		return nil
	}
	for _, pkg := range g.prog.Packages {
		if pkg == nil || pkg.Types == nil {
			continue
		}
		if t := findTypeByKey(pkg.Types, key); t != nil {
			return t
		}
	}
	// Builtins / universe.
	return builtinType(key)
}

func (g *gen) lookupIface(key string) *types.Interface {
	t := g.lookupType(key)
	if t == nil {
		if key == "error" {
			return errorInterface()
		}
		return nil
	}
	u := t.Underlying()
	if i, ok := u.(*types.Interface); ok {
		return i
	}
	return nil
}

func (g *gen) emitAll() error {
	// Assign dense ids and fill kind/name; extras may discover more types.
	for {
		keys := make([]string, 0, len(g.types))
		for k := range g.types {
			keys = append(keys, k)
		}
		sortStrings(keys)
		before := len(keys)
		for _, k := range keys {
			tr := g.types[k]
			if tr.id < 0 {
				tr.id = g.nextID
				g.nextID++
			}
			if tr.goType != nil {
				tr.kind = kindOf(tr.goType)
				if n, ok := types.Unalias(tr.goType).(*types.Named); ok {
					tr.name = n.Obj().Name()
				} else {
					tr.name = tr.goType.String()
				}
				tr.comparable = types.Comparable(tr.goType)
			} else if tr.kind == 0 {
				tr.kind, tr.name, tr.comparable = kindFromKey(k)
			}
		}
		// Discover extras' dependent types without emitting globals yet.
		for _, k := range keys {
			tr := g.types[k]
			g.discoverExtraDeps(tr)
		}
		if len(g.types) == before {
			break
		}
	}
	keys := make([]string, 0, len(g.types))
	for k := range g.types {
		keys = append(keys, k)
	}
	sortStrings(keys)
	for _, k := range keys {
		tr := g.types[k]
		if err := g.emitTypeGlobal(tr); err != nil {
			return err
		}
		if err := g.emitEqualFunc(tr); err != nil {
			return err
		}
	}
	if err := g.fillReflectHelpers(); err != nil {
		return err
	}
	if err := g.emitDispatchEq(); err != nil {
		return err
	}
	// Group itabs by iface for Wasm type; also emit itabtypes for ifaces that
	// are only called (OpIfaceCall) without a concrete make.
	ifaceMethods := map[string][]*types.Func{}
	for _, it := range g.itabs {
		if it.iface == nil && it.ifaceKey == "error" {
			it.iface = errorInterface()
		}
		if it.iface == nil {
			// Empty interface: type-only itab.
			it.typeName = "$go.itab"
			continue
		}
		n := it.iface.NumMethods()
		ms := make([]*types.Func, n)
		for i := 0; i < n; i++ {
			ms[i] = it.iface.Method(i)
		}
		ifaceMethods[it.ifaceKey] = ms
		it.typeName = "$go.itabtype." + sanitize(it.ifaceKey)
	}
	for ifaceKey := range g.ifaces {
		if _, ok := ifaceMethods[ifaceKey]; ok {
			continue
		}
		iface := g.lookupIface(ifaceKey)
		if iface == nil && ifaceKey == "error" {
			iface = errorInterface()
		}
		if iface == nil || iface.Empty() {
			continue
		}
		n := iface.NumMethods()
		ms := make([]*types.Func, n)
		for i := 0; i < n; i++ {
			ms[i] = iface.Method(i)
		}
		ifaceMethods[ifaceKey] = ms
	}
	for ifaceKey, ms := range ifaceMethods {
		if err := g.emitItabType(ifaceKey, ms); err != nil {
			return err
		}
	}
	for _, name := range sortedItabKeys(g.itabs) {
		it := g.itabs[name]
		if err := g.emitItab(it); err != nil {
			return err
		}
	}
	return nil
}

func (g *gen) emitTypeGlobal(tr *typeRec) error {
	if g.mod.LookupGlobal(tr.global) != nil {
		return nil
	}
	// Slice/map/pointer extras embed global.get of element descriptors in their
	// init, so those deps must appear earlier in the globals list.
	switch tr.kind {
	case KindSlice, KindArray, KindPointer:
		if ek, _ := g.elemOf(tr); ek != "" {
			if dep := g.types[ek]; dep != nil {
				if err := g.emitTypeGlobal(dep); err != nil {
					return err
				}
			}
		}
	case KindMap:
		if tr.goType != nil {
			if m, ok := tr.goType.Underlying().(*types.Map); ok {
				for _, t := range []types.Type{m.Key(), m.Elem()} {
					if dep := g.types[TypeKey(t)]; dep != nil {
						if err := g.emitTypeGlobal(dep); err != nil {
							return err
						}
					}
				}
			}
		}
	}
	// Keep $go.type at 4 fields (empty_main size budget). Heavy metadata in $extra.
	// Extra structs subtype $go.object — pass them directly (no ref.cast: Wasm
	// constant initializers disallow cast).
	extra := g.emitTypeExtra(tr)
	init := &ir.Instr{
		Op:   ir.OpStructNew,
		Type: &ir.Ref{Named: "$go.type"},
		Args: []ir.Expr{
			&ir.Const{Op: ir.OpI32Const, I64: int64(tr.id)},
			&ir.Const{Op: ir.OpI32Const, I64: int64(tr.kind)},
			g.nameExpr(tr),
			extra,
		},
	}
	g.mod.Globals = append(g.mod.Globals, &ir.Global{
		Name: tr.global,
		Typ:  &ir.Ref{Named: "$go.type"},
		Mut:  false,
		Init: init,
	})
	return nil
}

// discoverExtraDeps registers type keys that extras will need, without emitting.
func (g *gen) discoverExtraDeps(tr *typeRec) {
	if tr.goType == nil {
		return
	}
	switch tr.kind {
	case KindStruct:
		st, _ := g.structShape(tr)
		if st == nil {
			return
		}
		for i := 0; i < st.NumFields(); i++ {
			f := st.Field(i)
			if isZeroSizeType(f.Type()) {
				continue
			}
			fk := TypeKey(f.Type())
			g.needTypeKey(fk)
			if t := g.types[fk]; t != nil && t.goType == nil {
				t.goType = f.Type()
			}
		}
	case KindSlice, KindArray, KindPointer:
		ek, et := g.elemOf(tr)
		if ek != "" {
			g.needTypeKey(ek)
			if t := g.types[ek]; t != nil && t.goType == nil {
				t.goType = et
			}
		}
	case KindMap:
		if m, ok := tr.goType.Underlying().(*types.Map); ok {
			kk, ek := TypeKey(m.Key()), TypeKey(m.Elem())
			g.needTypeKey(kk)
			g.needTypeKey(ek)
			if t := g.types[kk]; t != nil && t.goType == nil {
				t.goType = m.Key()
			}
			if t := g.types[ek]; t != nil && t.goType == nil {
				t.goType = m.Elem()
			}
		}
	}
}

func (g *gen) emitEqualFunc(tr *typeRec) error {
	name := "$go.eq." + sanitize(tr.key)
	if g.equals[name] || g.mod.LookupFunc(name) != nil {
		return nil
	}
	g.equals[name] = true
	a := &ir.Var{Name: "a", Typ: &ir.Ref{Null: true, Heap: ir.HeapAny}}
	b := &ir.Var{Name: "b", Typ: &ir.Ref{Null: true, Heap: ir.HeapAny}}
	var body ir.Region
	if !tr.comparable {
		body = &ir.Seq{Stmts: []ir.Stmt{
			&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: "runtime.panicUncomparable"}},
			&ir.Unreachable{},
		}}
	} else {
		// Pointer / ref identity, boxed scalar compare, or struct field-wise.
		switch tr.kind {
		case KindInt, KindInt8, KindInt16, KindInt32, KindUint, KindUint8, KindUint16, KindUint32, KindUintptr, KindBool:
			body = g.boxEqualBody("$go.box.i32", ir.TypeI32, a, b)
		case KindInt64, KindUint64:
			body = g.boxEqualBody("$go.box.i64", ir.TypeI64, a, b)
		case KindFloat32:
			body = g.boxEqualBody("$go.box.f32", ir.TypeF32, a, b)
		case KindFloat64:
			body = g.boxEqualBody("$go.box.f64", ir.TypeF64, a, b)
		case KindString:
			body = g.stringEqualBody(a, b)
		case KindStruct:
			body = g.structEqualBody(tr, a, b)
		default:
			// Pointers, chans, interfaces, arrays: identity on the data word.
			eqT := &ir.Ref{Null: true, Heap: ir.HeapEq}
			ca := &ir.Instr{Op: ir.OpRefCast, Type: eqT, Args: []ir.Expr{&ir.Name{Raw: "a", Var: a}}}
			cb := &ir.Instr{Op: ir.OpRefCast, Type: eqT, Args: []ir.Expr{&ir.Name{Raw: "b", Var: b}}}
			body = &ir.Seq{Stmts: []ir.Stmt{
				&ir.Return{Results: []ir.Expr{&ir.Instr{
					Op:   ir.OpRefEq,
					Args: []ir.Expr{ca, cb},
				}}},
			}}
		}
	}
	g.mod.Funcs = append(g.mod.Funcs, &ir.Func{
		Name:    name,
		Params:  []*ir.Var{a, b},
		Results: []ir.Type{ir.TypeI32},
		Body:    body,
	})
	return nil
}

func (g *gen) boxEqualBody(box string, val ir.Type, a, b *ir.Var) ir.Region {
	ca := &ir.Instr{Op: ir.OpRefCast, Type: &ir.Ref{Named: box}, Args: []ir.Expr{&ir.Name{Raw: "a", Var: a}}}
	cb := &ir.Instr{Op: ir.OpRefCast, Type: &ir.Ref{Named: box}, Args: []ir.Expr{&ir.Name{Raw: "b", Var: b}}}
	va := &ir.Instr{Op: ir.OpStructGet, Type: &ir.Ref{Named: box}, Field: "v", Args: []ir.Expr{ca}}
	vb := &ir.Instr{Op: ir.OpStructGet, Type: &ir.Ref{Named: box}, Field: "v", Args: []ir.Expr{cb}}
	var eq ir.Op
	switch val {
	case ir.TypeI64:
		eq = ir.OpI64Eq
	case ir.TypeF32:
		eq = ir.OpF32Eq
	case ir.TypeF64:
		eq = ir.OpF64Eq
	default:
		eq = ir.OpI32Eq
	}
	return &ir.Seq{Stmts: []ir.Stmt{
		&ir.Return{Results: []ir.Expr{&ir.Instr{Op: eq, Args: []ir.Expr{va, vb}}}},
	}}
}

func (g *gen) stringEqualBody(a, b *ir.Var) ir.Region {
	// Content compare is iface.eq's stringDataEq → runtime.stringeq.
	// Descriptor equal for strings is identity (dispatchEq does not use KindString).
	eqT := &ir.Ref{Null: true, Heap: ir.HeapEq}
	ca := &ir.Instr{Op: ir.OpRefCast, Type: eqT, Args: []ir.Expr{&ir.Name{Raw: "a", Var: a}}}
	cb := &ir.Instr{Op: ir.OpRefCast, Type: eqT, Args: []ir.Expr{&ir.Name{Raw: "b", Var: b}}}
	return &ir.Seq{Stmts: []ir.Stmt{
		&ir.Return{Results: []ir.Expr{&ir.Instr{Op: ir.OpRefEq, Args: []ir.Expr{ca, cb}}}},
	}}
}

// emitDispatchEq builds $go.dispatchEq(id, a, b) → call $go.eq.* by dense id.
// iface.eq uses this for KindStruct so $go.type stays 4 fields (size budget).
func (g *gen) emitDispatchEq() error {
	if g.mod.LookupFunc("$go.dispatchEq") != nil {
		return nil
	}
	need := false
	for _, tr := range g.types {
		if tr.kind == KindStruct || tr.kind == KindArray {
			need = true
			break
		}
	}
	if !need {
		return nil
	}
	id := &ir.Var{Name: "id", Typ: ir.TypeI32}
	a := &ir.Var{Name: "a", Typ: &ir.Ref{Null: true, Heap: ir.HeapAny}}
	b := &ir.Var{Name: "b", Typ: &ir.Ref{Null: true, Heap: ir.HeapAny}}
	keys := make([]string, 0, len(g.types))
	for k := range g.types {
		keys = append(keys, k)
	}
	sortStrings(keys)
	// if/else chain of returns; Unreachable after so codegen sees no fallthru.
	var chain ir.Stmt = &ir.Return{Results: []ir.Expr{&ir.Const{Op: ir.OpI32Const, I64: 0}}}
	for i := len(keys) - 1; i >= 0; i-- {
		tr := g.types[keys[i]]
		if tr.kind != KindStruct && tr.kind != KindArray {
			continue
		}
		eqName := "$go.eq." + sanitize(tr.key)
		call := &ir.Instr{Op: ir.OpCall, Func: eqName, Args: []ir.Expr{
			&ir.Name{Raw: "a", Var: a}, &ir.Name{Raw: "b", Var: b},
		}}
		chain = &ir.If{
			Cond: &ir.Instr{Op: ir.OpI32Eq, Args: []ir.Expr{
				&ir.Name{Raw: "id", Var: id},
				&ir.Const{Op: ir.OpI32Const, I64: int64(tr.id)},
			}},
			Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{call}}}},
			Else: &ir.Seq{Stmts: []ir.Stmt{chain}},
		}
	}
	g.mod.Funcs = append(g.mod.Funcs, &ir.Func{
		Name:    "$go.dispatchEq",
		Params:  []*ir.Var{id, a, b},
		Results: []ir.Type{ir.TypeI32},
		Body:    &ir.Seq{Stmts: []ir.Stmt{chain, &ir.Unreachable{}}},
	})
	return nil
}

// structEqualBody compares named/anon struct fields (Go ==). Empty structs are equal.
func (g *gen) structEqualBody(tr *typeRec, a, b *ir.Var) ir.Region {
	wasmName := ""
	var st *types.Struct
	if tr.goType != nil {
		t := types.Unalias(tr.goType)
		if n, ok := t.(*types.Named); ok {
			wasmName = structName(n)
			st, _ = n.Underlying().(*types.Struct)
		} else if s, ok := t.(*types.Struct); ok {
			st = s
			wasmName = anonStructWasmName(s)
		}
	}
	// Runtime structs alias to $go.g / $go.hmap / … with field layouts that
	// may not match go/types names; identity is enough (they are not map keys).
	if tr.goType != nil {
		if n, ok := types.Unalias(tr.goType).(*types.Named); ok {
			if runtimeStructAliasName(n) != "" {
				st = nil
				wasmName = ""
			}
		}
	}
	if st == nil || wasmName == "" || g.mod.LookupType(wasmName) == nil {
		// Fall back to identity if we lack a concrete Wasm struct shape.
		eqT := &ir.Ref{Null: true, Heap: ir.HeapEq}
		ca := &ir.Instr{Op: ir.OpRefCast, Type: eqT, Args: []ir.Expr{&ir.Name{Raw: "a", Var: a}}}
		cb := &ir.Instr{Op: ir.OpRefCast, Type: eqT, Args: []ir.Expr{&ir.Name{Raw: "b", Var: b}}}
		return &ir.Seq{Stmts: []ir.Stmt{
			&ir.Return{Results: []ir.Expr{&ir.Instr{Op: ir.OpRefEq, Args: []ir.Expr{ca, cb}}}},
		}}
	}
	sty := &ir.Ref{Named: wasmName}
	sa := &ir.Var{Name: "sa", Typ: sty}
	sb := &ir.Var{Name: "sb", Typ: sty}
	seq := &ir.Seq{Stmts: []ir.Stmt{
		&ir.VarDef{Var: sa}, &ir.VarDef{Var: sb},
		&ir.Assign{Lhs: []ir.Dest{{Name: "sa", Var: sa}}, Rhs: &ir.Instr{Op: ir.OpRefCast, Type: sty, Args: []ir.Expr{&ir.Name{Raw: "a", Var: a}}}},
		&ir.Assign{Lhs: []ir.Dest{{Name: "sb", Var: sb}}, Rhs: &ir.Instr{Op: ir.OpRefCast, Type: sty, Args: []ir.Expr{&ir.Name{Raw: "b", Var: b}}}},
	}}
	var cmp ir.Expr = &ir.Const{Op: ir.OpI32Const, I64: 1}
	for i := 0; i < st.NumFields(); i++ {
		f := st.Field(i)
		if isZeroSizeType(f.Type()) {
			continue
		}
		fa := &ir.Instr{Op: ir.OpStructGet, Type: sty, Field: f.Name(), Args: []ir.Expr{&ir.Name{Raw: "sa", Var: sa}}}
		fb := &ir.Instr{Op: ir.OpStructGet, Type: sty, Field: f.Name(), Args: []ir.Expr{&ir.Name{Raw: "sb", Var: sb}}}
		fieldEq := g.fieldEqExpr(f.Type(), fa, fb)
		cmp = &ir.Instr{Op: ir.OpI32And, Args: []ir.Expr{cmp, fieldEq}}
	}
	seq.Stmts = append(seq.Stmts, &ir.Return{Results: []ir.Expr{cmp}})
	return seq
}

func anonStructWasmName(t *types.Struct) string {
	var b strings.Builder
	b.WriteString("$go.struct.anon")
	for i := 0; i < t.NumFields(); i++ {
		f := t.Field(i)
		b.WriteByte('.')
		b.WriteString(f.Name())
		b.WriteByte('.')
		b.WriteString(strings.ReplaceAll(f.Type().String(), " ", "_"))
	}
	return b.String()
}

func (g *gen) fieldEqExpr(ft types.Type, a, b ir.Expr) ir.Expr {
	ft = types.Unalias(ft)
	switch u := ft.(type) {
	case *types.Basic:
		switch u.Kind() {
		case types.Float32:
			return &ir.Instr{Op: ir.OpF32Eq, Args: []ir.Expr{a, b}}
		case types.Float64:
			return &ir.Instr{Op: ir.OpF64Eq, Args: []ir.Expr{a, b}}
		case types.Int64, types.Uint64:
			return &ir.Instr{Op: ir.OpI64Eq, Args: []ir.Expr{a, b}}
		case types.String:
			return &ir.Instr{Op: ir.OpCall, Func: "runtime.stringeq", Args: []ir.Expr{a, b}}
		case types.Complex64, types.Complex128, types.UntypedComplex:
			// complex is a Wasm GC struct ref ($go.complex64/128), not an i32.
			eqT := &ir.Ref{Null: true, Heap: ir.HeapEq}
			return &ir.Instr{Op: ir.OpRefEq, Args: []ir.Expr{
				&ir.Instr{Op: ir.OpRefCast, Type: eqT, Args: []ir.Expr{a}},
				&ir.Instr{Op: ir.OpRefCast, Type: eqT, Args: []ir.Expr{b}},
			}}
		case types.UnsafePointer:
			// anyref — reference identity, not i32.
			eqT := &ir.Ref{Null: true, Heap: ir.HeapEq}
			return &ir.Instr{Op: ir.OpRefEq, Args: []ir.Expr{
				&ir.Instr{Op: ir.OpRefCast, Type: eqT, Args: []ir.Expr{a}},
				&ir.Instr{Op: ir.OpRefCast, Type: eqT, Args: []ir.Expr{b}},
			}}
		default:
			return &ir.Instr{Op: ir.OpI32Eq, Args: []ir.Expr{a, b}}
		}
	case *types.Named:
		if _, ok := u.Underlying().(*types.Struct); ok {
			// Nested struct values are refs; field-wise via that type's equal.
			eqName := "$go.eq." + sanitize(TypeKey(u))
			anyT := &ir.Ref{Null: true, Heap: ir.HeapAny}
			return &ir.Instr{Op: ir.OpCall, Func: eqName, Args: []ir.Expr{
				&ir.Instr{Op: ir.OpRefCast, Type: anyT, Args: []ir.Expr{a}},
				&ir.Instr{Op: ir.OpRefCast, Type: anyT, Args: []ir.Expr{b}},
			}}
		}
		return g.fieldEqExpr(u.Underlying(), a, b)
	default:
		// Pointers, slices, maps, chans, ifaces: reference identity.
		eqT := &ir.Ref{Null: true, Heap: ir.HeapEq}
		return &ir.Instr{Op: ir.OpRefEq, Args: []ir.Expr{
			&ir.Instr{Op: ir.OpRefCast, Type: eqT, Args: []ir.Expr{a}},
			&ir.Instr{Op: ir.OpRefCast, Type: eqT, Args: []ir.Expr{b}},
		}}
	}
}

func (g *gen) emitItabType(ifaceKey string, ms []*types.Func) error {
	name := "$go.itabtype." + sanitize(ifaceKey)
	if g.mod.LookupType(name) != nil {
		return nil
	}
	fields := []ir.Field{
		{Name: "type", Type: &ir.Ref{Named: "$go.type"}},
	}
	for i, m := range ms {
		sigName := "$go.ifacefn." + sanitize(ifaceKey) + "." + m.Name()
		g.ensureIfaceFnType(sigName, m)
		fields = append(fields, ir.Field{
			Name: fmt.Sprintf("m%d", i),
			Type: &ir.Ref{Named: sigName},
		})
	}
	g.mod.InternType(&ir.TypeDef{
		Name:   name,
		Kind:   ir.TypeStruct,
		Super:  "$go.itab",
		Fields: fields,
	})
	return nil
}

func (g *gen) ensureIfaceFnType(name string, m *types.Func) {
	if g.mod.LookupType(name) != nil {
		return
	}
	sig := m.Type().(*types.Signature)
	params := []ir.Type{&ir.Ref{Null: true, Heap: ir.HeapAny}} // data word receiver
	for i := 0; i < sig.Params().Len(); i++ {
		pt, err := mapTypeApprox(sig.Params().At(i).Type())
		if err != nil {
			pt = &ir.Ref{Null: true, Heap: ir.HeapAny}
		}
		params = append(params, pt)
	}
	var results []ir.Type
	for i := 0; i < sig.Results().Len(); i++ {
		rt, err := mapTypeApprox(sig.Results().At(i).Type())
		if err != nil {
			rt = &ir.Ref{Null: true, Heap: ir.HeapAny}
		}
		results = append(results, rt)
	}
	g.mod.InternType(&ir.TypeDef{
		Name:    name,
		Kind:    ir.TypeFunc,
		Params:  params,
		Results: results,
	})
}

func (g *gen) emitItab(it *itabRec) error {
	if g.mod.LookupGlobal(it.key) != nil {
		return nil
	}
	tr := g.types[it.concKey]
	if tr == nil {
		g.needTypeKey(it.concKey)
		tr = g.types[it.concKey]
		_ = g.emitTypeGlobal(tr)
	}
	typeName := it.typeName
	if typeName == "" || typeName == "$go.itab" {
		// Empty interface itab: only type field, use base $go.itab.
		init := &ir.Instr{
			Op:   ir.OpStructNew,
			Type: &ir.Ref{Named: "$go.itab"},
			Args: []ir.Expr{
				&ir.Instr{Op: ir.OpGlobalGet, Global: tr.global},
			},
		}
		g.mod.Globals = append(g.mod.Globals, &ir.Global{
			Name: it.key,
			Typ:  &ir.Ref{Named: "$go.itab"},
			Mut:  false,
			Init: init,
		})
		return nil
	}
	if it.iface == nil {
		return fmt.Errorf("desc: itab %s: missing interface type", it.key)
	}
	args := []ir.Expr{
		&ir.Instr{Op: ir.OpGlobalGet, Global: tr.global},
	}
	for i := 0; i < it.iface.NumMethods(); i++ {
		m := it.iface.Method(i)
		wrap, err := g.ensureWrapper(it, m, i)
		if err != nil {
			return err
		}
		args = append(args, &ir.Instr{Op: ir.OpRefFunc, Func: wrap})
	}
	init := &ir.Instr{
		Op:   ir.OpStructNew,
		Type: &ir.Ref{Named: typeName},
		Args: args,
	}
	g.mod.Globals = append(g.mod.Globals, &ir.Global{
		Name: it.key,
		Typ:  &ir.Ref{Named: typeName},
		Mut:  false,
		Init: init,
	})
	return nil
}

func (g *gen) ensureWrapper(it *itabRec, m *types.Func, idx int) (string, error) {
	name := "$go.wrap." + sanitize(it.ifaceKey) + "." + sanitize(it.concKey) + "." + m.Name()
	if g.wrappers[name] || g.mod.LookupFunc(name) != nil {
		return name, nil
	}
	g.wrappers[name] = true

	concrete := it.concrete
	if concrete == nil {
		return "", fmt.Errorf("desc: no concrete type for %s", it.concKey)
	}
	// Find the method implementation (handles promotion).
	sel := types.NewMethodSet(concrete).Lookup(m.Pkg(), m.Name())
	if sel == nil {
		// Try pointer if concrete is not pointer.
		if _, ok := concrete.Underlying().(*types.Pointer); !ok {
			sel = types.NewMethodSet(types.NewPointer(concrete)).Lookup(m.Pkg(), m.Name())
		}
	}
	if sel == nil {
		return "", fmt.Errorf("desc: %s does not implement %s.%s", it.concKey, it.ifaceKey, m.Name())
	}
	fn := sel.Obj().(*types.Func)
	recvPath := sel.Index() // embedding indices
	sigName := "$go.ifacefn." + sanitize(it.ifaceKey) + "." + m.Name()
	g.ensureIfaceFnType(sigName, m)

	data := &ir.Var{Name: "data", Typ: &ir.Ref{Null: true, Heap: ir.HeapAny}}
	msig := m.Type().(*types.Signature)
	params := []*ir.Var{data}
	for i := 0; i < msig.Params().Len(); i++ {
		pt, _ := mapTypeApprox(msig.Params().At(i).Type())
		params = append(params, &ir.Var{Name: fmt.Sprintf("a%d", i), Typ: pt})
	}
	var results []ir.Type
	for i := 0; i < msig.Results().Len(); i++ {
		rt, _ := mapTypeApprox(msig.Results().At(i).Type())
		results = append(results, rt)
	}

	// Build receiver from data via embedding path.
	recvGo := fn.Type().(*types.Signature).Recv().Type()
	recvIR, _ := mapTypeApprox(recvGo)
	stmts, recvExpr, err := g.loadRecv(concrete, recvPath, recvGo, recvIR, data)
	if err != nil {
		return "", err
	}
	callArgs := []ir.Expr{recvExpr}
	for i := 0; i < msig.Params().Len(); i++ {
		callArgs = append(callArgs, &ir.Name{Raw: fmt.Sprintf("a%d", i), Var: params[i+1]})
	}
	var call ir.Expr
	// Promoted method from an embedded interface: call through the itab.
	if _, ok := types.Unalias(recvGo).Underlying().(*types.Interface); ok {
		ifaceKey := TypeKey(recvGo)
		methodIdx := 0
		if iface, ok := types.Unalias(recvGo).Underlying().(*types.Interface); ok {
			for i := 0; i < iface.NumMethods(); i++ {
				if iface.Method(i).Name() == m.Name() {
					methodIdx = i
					break
				}
			}
		}
		fnSig := "$go.ifacefn." + sanitize(ifaceKey) + "." + m.Name()
		g.ensureIfaceFnType(fnSig, m)
		var res ir.Type
		if len(results) > 0 {
			res = results[0]
		}
		call = &ir.Instr{
			Op:     ir.OpIfaceCall,
			Type:   res,
			Type2:  &ir.Ref{Named: "$go.itabtype." + sanitize(ifaceKey)},
			Global: ifaceKey,
			Func:   m.Name(),
			Field:  strconv.Itoa(methodIdx),
			Str:    fnSig,
			Args:   callArgs,
		}
	} else {
		call = &ir.Instr{Op: ir.OpCall, Func: methodIRName(fn), Args: callArgs}
	}
	if len(results) == 0 {
		stmts = append(stmts, &ir.ExprStmt{X: call}, &ir.Return{})
	} else {
		// Assign call to N temps typed as ifacefn results. Call may produce a
		// more specific ref ([]byte vs any); cast on the return edge.
		temps := make([]*ir.Temp, len(results))
		lhs := make([]ir.Dest, len(results))
		rets := make([]ir.Expr, len(results))
		for i, rt := range results {
			temps[i] = &ir.Temp{Name: fmt.Sprintf("%%wr%d", i), Typ: rt}
			lhs[i] = ir.Dest{Temp: temps[i], Name: temps[i].Name}
			var ex ir.Expr = &ir.Name{Raw: temps[i].Name, Temp: temps[i]}
			if _, ok := rt.(*ir.Ref); ok {
				ex = &ir.Instr{Op: ir.OpRefCast, Type: rt, Args: []ir.Expr{ex}}
			}
			rets[i] = ex
		}
		stmts = append(stmts, &ir.Assign{Lhs: lhs, Rhs: call})
		stmts = append(stmts, &ir.Return{Results: rets})
	}
	g.mod.Funcs = append(g.mod.Funcs, &ir.Func{
		Name:    name,
		Params:  params,
		Results: results,
		Body:    &ir.Seq{Stmts: stmts},
	})
	return name, nil
}

func (g *gen) loadRecv(concrete types.Type, index []int, wantRecv types.Type, wantIR ir.Type, data *ir.Var) ([]ir.Stmt, ir.Expr, error) {
	// Start from data as the concrete value boxed in the interface.
	// For pointer concrete types, data is the object ref.
	// For value concrete types that are structs, data is the struct ref.
	cur := types.Type(concrete)
	expr := ir.Expr(&ir.Name{Raw: "data", Var: data})
	var stmts []ir.Stmt

	// Cast data to the concrete IR shape first.
	baseIR, err := mapTypeApprox(derefIfPtr(concrete))
	if err != nil {
		baseIR = &ir.Ref{Null: true, Heap: ir.HeapAny}
	}
	if isPointer(concrete) {
		elem := derefIfPtr(concrete)
		if isStruct(elem) {
			// *Struct: data is already the struct object ref.
			expr = &ir.Instr{Op: ir.OpRefCast, Type: baseIR, Args: []ir.Expr{expr}}
		} else {
			// *scalar / *string / *slice: data is the fat pointer.
			ptrIR, _ := mapTypeApprox(concrete)
			expr = &ir.Instr{Op: ir.OpRefCast, Type: ptrIR, Args: []ir.Expr{expr}}
		}
		cur = concrete
	} else if isStruct(concrete) {
		expr = &ir.Instr{Op: ir.OpRefCast, Type: baseIR, Args: []ir.Expr{expr}}
	} else {
		// Numeric scalars are stored as $go.box.{i32,i64,f32,f64}.
		// Strings / named strings / slices / other refs are stored directly
		// as the value ref (boxKind "ref"), optionally via $go.box.string.
		boxName, elem := scalarBoxOf(wantIR)
		if boxName == "" {
			boxName, elem = scalarBoxOf(baseIR)
		}
		if boxName != "" {
			boxTy := &ir.Ref{Named: boxName}
			boxed := &ir.Instr{Op: ir.OpRefCast, Type: boxTy, Args: []ir.Expr{expr}}
			expr = &ir.Instr{Op: ir.OpStructGet, Type: boxTy, Field: "v", Args: []ir.Expr{boxed}}
			_ = elem
			return stmts, expr, nil
		}
		recvIR := wantIR
		if _, ok := recvIR.(*ir.Ref); !ok {
			recvIR = baseIR
		}
		if _, ok := recvIR.(*ir.Ref); !ok {
			return nil, nil, fmt.Errorf("desc: cannot unbox scalar receiver %s", concrete)
		}
		expr = &ir.Instr{Op: ir.OpRefCast, Type: recvIR, Args: []ir.Expr{expr}}
		return stmts, expr, nil
	}

	// Walk embedding index path except the last (method index on the final type).
	// Selection.Index() is field indices then method index at the end.
	if len(index) == 0 {
		return stmts, expr, nil
	}
	fieldPath := index[:len(index)-1]
	obj := derefNamed(cur)
	for _, fi := range fieldPath {
		st, ok := underlyingStruct(obj)
		if !ok {
			return nil, nil, fmt.Errorf("desc: embedding path through non-struct %s", obj)
		}
		field := st.Field(fi)
		ft := field.Type()
		fieldIR, err := mapTypeApprox(ft)
		if err != nil {
			return nil, nil, err
		}
		// Zero-size fields (empty struct{}) are omitted from the Wasm struct
		// (03-type-mapping). Synthesize a default value for promotion instead.
		if isZeroSizeType(ft) {
			expr = &ir.Instr{Op: ir.OpStructNewDefault, Type: fieldIR}
		} else {
			expr = &ir.Instr{
				Op:    ir.OpStructGet,
				Type:  baseIROf(obj),
				Field: field.Name(),
				Args:  []ir.Expr{expr},
			}
		}
		obj = ft
		baseIR = fieldIR
		_ = baseIR
	}
	// Match wanted receiver pointer-ness.
	havePtr := isPointer(obj)
	needPtr := isPointer(wantRecv)
	if needPtr && !havePtr {
		// Value in iface but method wants pointer — data was addressable; for
		// interface method calls on value types stored as refs, the ref is the object.
		// Struct values in interfaces are boxed as the struct ref; pointer methods
		// can use that same ref.
		expr = &ir.Instr{Op: ir.OpRefCast, Type: wantIR, Args: []ir.Expr{expr}}
	} else if !needPtr && havePtr {
		expr = &ir.Instr{Op: ir.OpRefCast, Type: wantIR, Args: []ir.Expr{expr}}
	} else {
		expr = &ir.Instr{Op: ir.OpRefCast, Type: wantIR, Args: []ir.Expr{expr}}
	}
	return stmts, expr, nil
}

// --- helpers ---

func TypeKey(t types.Type) string {
	t = types.Unalias(t)
	switch t := t.(type) {
	case *types.Basic:
		return t.Name()
	case *types.Named:
		pkg := ""
		if t.Obj().Pkg() != nil {
			pkg = pkgIRPrefix(t.Obj().Pkg()) + "."
		}
		suffix := ""
		if targs := t.TypeArgs(); targs != nil && targs.Len() > 0 {
			var b strings.Builder
			ok := true
			for i := 0; i < targs.Len(); i++ {
				a := targs.At(i)
				if _, isTP := types.Unalias(a).(*types.TypeParam); isTP {
					ok = false
					break
				}
				if i > 0 {
					b.WriteByte('$')
				}
				b.WriteString(TypeKey(a))
			}
			if ok {
				suffix = "$" + b.String()
			}
		}
		return pkg + t.Obj().Name() + suffix
	case *types.Pointer:
		return "*" + TypeKey(t.Elem())
	case *types.Interface:
		if t.Empty() {
			return "any"
		}
		// error is a named interface in universe — handled as Named.
		return "iface." + t.String()
	case *types.Slice:
		return "[]" + TypeKey(t.Elem())
	case *types.Array:
		return fmt.Sprintf("[%d]%s", t.Len(), TypeKey(t.Elem()))
	case *types.Map:
		return "map[" + TypeKey(t.Key()) + "]" + TypeKey(t.Elem())
	case *types.Struct:
		if t.NumFields() == 0 {
			return "struct{}"
		}
		return strings.ReplaceAll(t.String(), " ", "_")
	case *types.Chan:
		return strings.ReplaceAll(t.String(), " ", "_")
	default:
		return strings.ReplaceAll(t.String(), " ", "_")
	}
}

// ItabGlobal returns the IR global name for (iface, concrete).
func ItabGlobal(iface, concrete types.Type) string {
	return "$go.itab." + TypeKey(iface) + ".@." + TypeKey(concrete)
}

// TypeGlobal returns the descriptor global name.
func TypeGlobal(t types.Type) string {
	return "$go.type." + TypeKey(t)
}

func pkgIRPrefix(pkg *types.Package) string {
	if pkg == nil {
		return "main"
	}
	if pkg.Name() == "main" {
		return "main"
	}
	path := pkg.Path()
	if path == "" || path == pkg.Name() {
		return pkg.Name()
	}
	return strings.ReplaceAll(path, "/", ".")
}

func methodIRName(fn *types.Func) string {
	sig := fn.Type().(*types.Signature)
	recv := sig.Recv()
	pkg := pkgIRPrefix(fn.Pkg())
	if recv == nil {
		return pkg + "." + fn.Name()
	}
	rt := recv.Type()
	ptr := false
	if p, ok := types.Unalias(rt).(*types.Pointer); ok {
		ptr = true
		rt = p.Elem()
	}
	named, _ := types.Unalias(rt).(*types.Named)
	tname := "T"
	if named != nil {
		tname = named.Obj().Name()
		if targs := named.TypeArgs(); targs != nil && targs.Len() > 0 {
			var b strings.Builder
			ok := true
			for i := 0; i < targs.Len(); i++ {
				a := targs.At(i)
				if _, isTP := types.Unalias(a).(*types.TypeParam); isTP {
					ok = false
					break
				}
				if i > 0 {
					b.WriteByte('$')
				}
				b.WriteString(TypeKey(a))
			}
			if ok {
				tname += "$" + b.String()
			}
		}
	}
	if ptr {
		return pkg + ".(*" + tname + ")." + fn.Name()
	}
	return pkg + "." + tname + "." + fn.Name()
}

func mapTypeApprox(t types.Type) (ir.Type, error) {
	t = types.Unalias(t)
	switch t := t.(type) {
	case *types.Basic:
		switch t.Kind() {
		case types.Bool, types.Int, types.Int8, types.Int16, types.Int32,
			types.Uint, types.Uint8, types.Uint16, types.Uint32, types.Uintptr:
			return ir.TypeI32, nil
		case types.Int64, types.Uint64:
			return ir.TypeI64, nil
		case types.Float32:
			return ir.TypeF32, nil
		case types.Float64:
			return ir.TypeF64, nil
		case types.String:
			return &ir.Ref{Named: "$go.string"}, nil
		}
	case *types.Interface:
		return &ir.Ref{Null: true, Named: "$go.iface"}, nil
	case *types.Pointer:
		elem := types.Unalias(t.Elem())
		if n, ok := elem.(*types.Named); ok {
			switch u := n.Underlying().(type) {
			case *types.Struct:
				// *Struct shares the struct ref (matches lower.mapPointer).
				return &ir.Ref{Null: true, Named: structName(n)}, nil
			case *types.Array:
				// *[N]T / *byteReplacer → same array ref as [N]T.
				return mapArrayApprox(u)
			}
		}
		if _, ok := elem.(*types.Struct); ok {
			return &ir.Ref{Null: true, Heap: ir.HeapAny}, nil
		}
		if a, ok := elem.(*types.Array); ok {
			return mapArrayApprox(a)
		}
		// *string / *int / *[]T → fat pointer ($go.ptr.ref / .i32 / …).
		return &ir.Ref{Null: true, Named: "$go.ptr." + fatPtrKindApprox(elem)}, nil
	case *types.Named:
		switch u := t.Underlying().(type) {
		case *types.Struct:
			return &ir.Ref{Null: true, Named: structName(t)}, nil
		case *types.Array:
			return mapArrayApprox(u)
		}
		return mapTypeApprox(t.Underlying())
	case *types.Slice:
		return sliceIRApprox(t.Elem()), nil
	case *types.Array:
		return mapArrayApprox(t)
	case *types.Map, *types.Chan:
		return &ir.Ref{Null: true, Heap: ir.HeapAny}, nil
	case *types.Signature:
		// Func values are (ref null $go.clos) — see design 03 / M5.
		return &ir.Ref{Null: true, Named: "$go.clos"}, nil
	}
	return &ir.Ref{Null: true, Heap: ir.HeapAny}, nil
}

// fatPtrKindApprox mirrors lower.fatPtrKind for descriptor wrappers.
func fatPtrKindApprox(elem types.Type) string {
	elem = types.Unalias(elem)
	switch u := elem.Underlying().(type) {
	case *types.Basic:
		switch u.Kind() {
		case types.Int64, types.Uint64:
			return "i64"
		case types.Float32:
			return "f32"
		case types.Float64:
			return "f64"
		case types.String, types.UnsafePointer,
			types.Complex64, types.Complex128, types.UntypedComplex:
			return "ref"
		default:
			return "i32"
		}
	case *types.Slice, *types.Pointer, *types.Map, *types.Chan, *types.Signature, *types.Interface,
		*types.Struct, *types.Array:
		return "ref"
	case *types.Named:
		return fatPtrKindApprox(u.Underlying())
	}
	return "ref"
}

// mapArrayApprox mirrors lower.mapArray / ensureArrayType for descriptor wrappers.
func mapArrayApprox(t *types.Array) (ir.Type, error) {
	elem := types.Unalias(t.Elem())
	if b, ok := elem.Underlying().(*types.Basic); ok {
		switch b.Kind() {
		case types.Int8, types.Uint8, types.Bool:
			return &ir.Ref{Null: true, Named: "$go.bytes"}, nil
		case types.Int16, types.Uint16:
			return &ir.Ref{Null: true, Named: "$go.i16s"}, nil
		}
	}
	// Other element arrays: approximate as any (rare as method receivers).
	return &ir.Ref{Null: true, Heap: ir.HeapAny}, nil
}

// sliceIRApprox maps []T to the WasmGC slice header name used by the lowerer.
func sliceIRApprox(elem types.Type) ir.Type {
	elem = types.Unalias(elem)
	if b, ok := elem.Underlying().(*types.Basic); ok {
		switch b.Kind() {
		case types.Uint8: // also types.Byte
			return &ir.Ref{Null: true, Named: "$go.slice.i8"}
		case types.Int16, types.Uint16:
			return &ir.Ref{Null: true, Named: "$go.slice.i16"}
		case types.Int, types.Int32, types.Uint, types.Uint32, types.Uintptr, types.Bool:
			return &ir.Ref{Null: true, Named: "$go.slice.i32"}
		case types.Int64, types.Uint64:
			return &ir.Ref{Null: true, Named: "$go.slice.i64"}
		case types.Float32:
			return &ir.Ref{Null: true, Named: "$go.slice.f32"}
		case types.Float64:
			return &ir.Ref{Null: true, Named: "$go.slice.f64"}
		case types.String:
			return &ir.Ref{Null: true, Named: "$go.slice.go.string"}
		}
	}
	if _, ok := elem.Underlying().(*types.Interface); ok {
		return &ir.Ref{Null: true, Named: "$go.slice.go.iface"}
	}
	return &ir.Ref{Null: true, Heap: ir.HeapAny}
}

func structName(t *types.Named) string {
	if alias := runtimeStructAliasName(t); alias != "" {
		return alias
	}
	// Must match lower.structTypeName / pkgIRPrefix (full import path), not
	// pkg.Name() alone — else iface method params get $go.struct.jsontext.T
	// while values are $go.struct.encoding.json.jsontext.T and call_ref fails.
	pkg := "main"
	if t.Obj().Pkg() != nil {
		pkg = pkgIRPrefix(t.Obj().Pkg())
	}
	suffix := ""
	if targs := t.TypeArgs(); targs != nil && targs.Len() > 0 {
		var b strings.Builder
		ok := true
		for i := 0; i < targs.Len(); i++ {
			a := targs.At(i)
			if _, isTP := types.Unalias(a).(*types.TypeParam); isTP {
				ok = false
				break
			}
			if i > 0 {
				b.WriteByte('$')
			}
			b.WriteString(TypeKey(a))
		}
		if ok {
			suffix = "$" + b.String()
		}
	}
	return "$go.struct." + pkg + "." + t.Obj().Name() + suffix
}

// runtimeStructAliasName mirrors lower.runtimeStructAlias for eq bodies.
func runtimeStructAliasName(t *types.Named) string {
	obj := t.Obj()
	if obj == nil || obj.Pkg() == nil {
		return ""
	}
	if obj.Pkg().Path() != "runtime" && obj.Pkg().Name() != "runtime" {
		return ""
	}
	switch obj.Name() {
	case "hchan":
		return "$go.chan"
	case "waiter":
		return "$go.waiter"
	case "waitq":
		return "$go.waitq"
	case "g":
		return "$go.g"
	case "hmap":
		return "$go.hmap"
	case "mapIter":
		return "$go.mapIter"
	case "scase":
		return "$go.scase"
	case "select_":
		return "$go.select"
	case "chanOps":
		return "$go.chanOps"
	case "panicObj":
		return "$go.panicObj"
	case "semaRoot":
		return "$go.semaRoot"
	case "semaEntry":
		return "$go.semaEntry"
	case "Rtype":
		return "$go.type"
	default:
		return ""
	}
}

// Sanitize turns a TypeKey into a Wasm-safe type/global name fragment.
// Lower and desc must use the same rules so itabtype / ifacefn names match.
func Sanitize(s string) string {
	s = strings.ReplaceAll(s, "*", "ptr_")
	s = strings.ReplaceAll(s, "[", "arr_")
	s = strings.ReplaceAll(s, "]", "")
	s = strings.ReplaceAll(s, ".", "_")
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, " ", "_")
	s = strings.ReplaceAll(s, "{}", "empty")
	return s
}

func sanitize(s string) string { return Sanitize(s) }

func kindOf(t types.Type) int {
	t = types.Unalias(t)
	switch u := t.Underlying().(type) {
	case *types.Basic:
		switch u.Kind() {
		case types.Bool:
			return KindBool
		case types.Int:
			return KindInt
		case types.Int8:
			return KindInt8
		case types.Int16:
			return KindInt16
		case types.Int32:
			return KindInt32
		case types.Int64:
			return KindInt64
		case types.Uint:
			return KindUint
		case types.Uint8:
			return KindUint8
		case types.Uint16:
			return KindUint16
		case types.Uint32:
			return KindUint32
		case types.Uint64:
			return KindUint64
		case types.Uintptr:
			return KindUintptr
		case types.Float32:
			return KindFloat32
		case types.Float64:
			return KindFloat64
		case types.String:
			return KindString
		case types.UnsafePointer:
			return KindUnsafePointer
		}
	case *types.Array:
		return KindArray
	case *types.Chan:
		return KindChan
	case *types.Signature:
		return KindFunc
	case *types.Interface:
		return KindInterface
	case *types.Map:
		return KindMap
	case *types.Pointer:
		return KindPointer
	case *types.Slice:
		return KindSlice
	case *types.Struct:
		return KindStruct
	}
	return KindInvalid
}

const KindInvalid = 0

func kindFromKey(key string) (kind int, name string, comparable bool) {
	name = key
	comparable = true
	switch key {
	case "bool":
		return KindBool, key, true
	case "int":
		return KindInt, key, true
	case "int8":
		return KindInt8, key, true
	case "int16":
		return KindInt16, key, true
	case "int32":
		return KindInt32, key, true
	case "int64":
		return KindInt64, key, true
	case "uint":
		return KindUint, key, true
	case "uint8", "byte":
		return KindUint8, key, true
	case "uint16":
		return KindUint16, key, true
	case "uint32":
		return KindUint32, key, true
	case "uint64":
		return KindUint64, key, true
	case "uintptr":
		return KindUintptr, key, true
	case "float32":
		return KindFloat32, key, true
	case "float64":
		return KindFloat64, key, true
	case "string":
		return KindString, key, true
	case "error", "any":
		return KindInterface, key, true
	}
	if strings.HasPrefix(key, "*") {
		return KindPointer, key, true
	}
	if strings.HasPrefix(key, "[]") {
		return KindSlice, key, false
	}
	if strings.HasPrefix(key, "map[") {
		return KindMap, key, false
	}
	if strings.HasPrefix(key, "chan ") || strings.HasPrefix(key, "chan_") ||
		strings.HasPrefix(key, "<-chan") || strings.HasPrefix(key, "chan<-") {
		return KindChan, key, true
	}
	if strings.HasPrefix(key, "[") {
		// [N]T array
		return KindArray, key, true
	}
	if key == "struct{}" || strings.HasPrefix(key, "struct{") {
		return KindStruct, key, true
	}
	return KindStruct, key, true
}

func errorInterface() *types.Interface {
	// Reconstruct error as interface{ Error() string }.
	sig := types.NewSignatureType(nil, nil, nil, nil,
		types.NewTuple(types.NewVar(0, nil, "", types.Typ[types.String])),
		false)
	m := types.NewFunc(0, nil, "Error", sig)
	return types.NewInterfaceType([]*types.Func{m}, nil).Complete()
}

func builtinType(key string) types.Type {
	switch key {
	case "bool":
		return types.Typ[types.Bool]
	case "int":
		return types.Typ[types.Int]
	case "int8":
		return types.Typ[types.Int8]
	case "int16":
		return types.Typ[types.Int16]
	case "int32":
		return types.Typ[types.Int32]
	case "int64":
		return types.Typ[types.Int64]
	case "uint":
		return types.Typ[types.Uint]
	case "uint8", "byte":
		return types.Typ[types.Uint8]
	case "uint16":
		return types.Typ[types.Uint16]
	case "uint32":
		return types.Typ[types.Uint32]
	case "uint64":
		return types.Typ[types.Uint64]
	case "uintptr":
		return types.Typ[types.Uintptr]
	case "float32":
		return types.Typ[types.Float32]
	case "float64":
		return types.Typ[types.Float64]
	case "string":
		return types.Typ[types.String]
	case "error":
		return errorInterface()
	case "any":
		return types.NewInterfaceType(nil, nil).Complete()
	}
	if strings.HasPrefix(key, "*") {
		elem := builtinType(strings.TrimPrefix(key, "*"))
		if elem != nil {
			return types.NewPointer(elem)
		}
	}
	return nil
}

func findTypeByKey(pkg *types.Package, key string) types.Type {
	want := key
	pkgPrefix := pkgIRPrefix(pkg) + "."
	if strings.HasPrefix(key, "*") {
		elemKey := strings.TrimPrefix(key, "*")
		if t := findTypeByKey(pkg, elemKey); t != nil {
			return types.NewPointer(t)
		}
		return nil
	}
	if strings.HasPrefix(key, pkgPrefix) {
		want = strings.TrimPrefix(key, pkgPrefix)
	} else if strings.Contains(key, ".") && !strings.HasPrefix(key, pkgPrefix) && !strings.HasPrefix(key, pkg.Name()+".") {
		return nil
	} else if strings.HasPrefix(key, pkg.Name()+".") && pkgIRPrefix(pkg) != pkg.Name() {
		// Legacy short keys (pkg.Name only) still resolve.
		want = strings.TrimPrefix(key, pkg.Name()+".")
	}
	if obj := pkg.Scope().Lookup(want); obj != nil {
		if tn, ok := obj.(*types.TypeName); ok {
			return tn.Type()
		}
	}
	return nil
}

// scalarBoxOf returns the $go.box.* type and element type for a scalar IR type.
func scalarBoxOf(t ir.Type) (boxName string, elem ir.Type) {
	switch t {
	case ir.TypeI32, ir.TypeI8, ir.TypeI16:
		return "$go.box.i32", ir.TypeI32
	case ir.TypeI64:
		return "$go.box.i64", ir.TypeI64
	case ir.TypeF32:
		return "$go.box.f32", ir.TypeF32
	case ir.TypeF64:
		return "$go.box.f64", ir.TypeF64
	}
	return "", nil
}

func isPointer(t types.Type) bool {
	_, ok := types.Unalias(t).(*types.Pointer)
	return ok
}

func isStruct(t types.Type) bool {
	_, ok := underlyingStruct(t)
	return ok
}

// isZeroSizeType mirrors lower.isZeroSize: empty structs and arrays of them
// are omitted from Wasm struct field lists (03-type-mapping).
func isZeroSizeType(t types.Type) bool {
	t = types.Unalias(t)
	switch t := t.(type) {
	case *types.Struct:
		for i := 0; i < t.NumFields(); i++ {
			if !isZeroSizeType(t.Field(i).Type()) {
				return false
			}
		}
		return true
	case *types.Array:
		return t.Len() == 0 || isZeroSizeType(t.Elem())
	case *types.Named:
		return isZeroSizeType(t.Underlying())
	default:
		return false
	}
}

func underlyingStruct(t types.Type) (*types.Struct, bool) {
	t = types.Unalias(t)
	if p, ok := t.(*types.Pointer); ok {
		t = types.Unalias(p.Elem())
	}
	if n, ok := t.(*types.Named); ok {
		t = n.Underlying()
	}
	st, ok := t.(*types.Struct)
	return st, ok
}

func derefIfPtr(t types.Type) types.Type {
	if p, ok := types.Unalias(t).(*types.Pointer); ok {
		return p.Elem()
	}
	return t
}

func derefNamed(t types.Type) types.Type {
	return t
}

func baseIROf(t types.Type) ir.Type {
	t = derefIfPtr(t)
	if n, ok := types.Unalias(t).(*types.Named); ok {
		if _, ok := n.Underlying().(*types.Struct); ok {
			return &ir.Ref{Named: structName(n)}
		}
	}
	return &ir.Ref{Null: true, Heap: ir.HeapAny}
}

func sortStrings(s []string) {
	for i := 0; i < len(s); i++ {
		for j := i + 1; j < len(s); j++ {
			if s[j] < s[i] {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
}

func sortedItabKeys(m map[string]*itabRec) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sortStrings(keys)
	return keys
}

func walkRegion(r ir.Region, fn func(ir.Expr)) {
	if r == nil {
		return
	}
	var walkStmt func(ir.Stmt)
	var walkExpr func(ir.Expr)
	var walkReg func(ir.Region)
	walkExpr = func(e ir.Expr) {
		if e == nil {
			return
		}
		fn(e)
		if in, ok := e.(*ir.Instr); ok {
			for _, a := range in.Args {
				walkExpr(a)
			}
		}
	}
	walkStmt = func(s ir.Stmt) {
		switch s := s.(type) {
		case ir.Region:
			walkReg(s)
		case *ir.Assign:
			walkExpr(s.Rhs)
		case *ir.ExprStmt:
			walkExpr(s.X)
		case *ir.Return:
			for _, e := range s.Results {
				walkExpr(e)
			}
		case *ir.Br:
			if s.Cond != nil {
				walkExpr(s.Cond)
			}
			for _, a := range s.Args {
				walkExpr(a)
			}
		}
	}
	walkReg = func(r ir.Region) {
		switch r := r.(type) {
		case *ir.Seq:
			for _, s := range r.Stmts {
				walkStmt(s)
			}
		case *ir.Block:
			walkReg(r.Body)
		case *ir.Loop:
			walkReg(r.Body)
		case *ir.If:
			walkExpr(r.Cond)
			walkReg(r.Then)
			if r.Else != nil {
				walkReg(r.Else)
			}
		case *ir.Switch:
			walkExpr(r.Tag)
			for _, c := range r.Cases {
				walkReg(c.Body)
			}
			if r.Default != nil {
				walkReg(r.Default)
			}
		case *ir.Try:
			walkReg(r.Body)
			walkReg(r.Handler)
		case *ir.Dispatch:
			for _, a := range r.Arms {
				if a != nil {
					walkReg(a)
				}
			}
		}
	}
	walkReg(r)
}
