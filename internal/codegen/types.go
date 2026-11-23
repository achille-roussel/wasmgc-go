package codegen

import (
	"fmt"
	"strings"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
	"github.com/achille-roussel/wasmgc-go/internal/wasm"
)

// emitPrelude writes the fixed prelude from 03-type-mapping.md.
// Indices are the IdxGo* constants. Always emitted so the intern table
// is deterministic for stream G.
func (g *gen) emitPrelude() {
	// 0: $go.object (sub (struct)) — open, empty
	obj := g.wm.AddType(wasm.SubOpen(nil, wasm.StructType()))
	g.types.ByName["$go.object"] = obj

	// 1: $go.bytes (array (mut i8))
	bytes := g.wm.AddArrayType(wasm.Field(wasm.StorageI8(), true))
	g.types.ByName["$go.bytes"] = bytes

	// 2: $go.i16s (array (mut i16))
	i16s := g.wm.AddArrayType(wasm.Field(wasm.StorageI16(), true))
	g.types.ByName["$go.i16s"] = i16s

	// 3: $go.string (sub $go.object (struct (field $data (ref $go.bytes)) (field $off i32) (field $len i32)))
	str := g.wm.AddType(wasm.SubFinal([]uint32{obj}, wasm.StructType(
		wasm.Field(wasm.StorageVal(wasm.Ref(wasm.TypeIndexHeap(bytes))), false),
		wasm.Field(wasm.StorageVal(wasm.I32()), false),
		wasm.Field(wasm.StorageVal(wasm.I32()), false),
	)))
	g.types.ByName["$go.string"] = str

	// 4: $go.type (keep 4 fields — empty_main size budget; equal is $go.dispatchEq)
	typ := g.wm.AddType(wasm.SubFinal([]uint32{obj}, wasm.StructType(
		wasm.Field(wasm.StorageVal(wasm.I32()), false),                          // id
		wasm.Field(wasm.StorageVal(wasm.I32()), false),                          // kind
		wasm.Field(wasm.StorageVal(wasm.RefNull(wasm.TypeIndexHeap(str))), false), // name
		wasm.Field(wasm.StorageVal(wasm.RefNull(wasm.TypeIndexHeap(obj))), false), // extra
	)))
	g.types.ByName["$go.type"] = typ

	// 5: $go.itab
	itab := g.wm.AddType(wasm.SubOpen([]uint32{obj}, wasm.StructType(
		wasm.Field(wasm.StorageVal(wasm.Ref(wasm.TypeIndexHeap(typ))), false),
	)))
	g.types.ByName["$go.itab"] = itab

	// 6: $go.iface
	iface := g.wm.AddType(wasm.SubFinal([]uint32{obj}, wasm.StructType(
		wasm.Field(wasm.StorageVal(wasm.Ref(wasm.TypeIndexHeap(itab))), false),
		wasm.Field(wasm.StorageVal(wasm.AnyRef()), false),
	)))
	g.types.ByName["$go.iface"] = iface

	// 7: $go.panicObj — self-referential via $next; explicit rec group.
	panicIdx := g.wm.NumTypes()
	panicComp := wasm.StructType(
		wasm.Field(wasm.StorageVal(wasm.RefNull(wasm.TypeIndexHeap(iface))), false),     // value
		wasm.Field(wasm.StorageVal(wasm.I32()), true),                                    // recovered mut
		wasm.Field(wasm.StorageVal(wasm.RefNull(wasm.TypeIndexHeap(panicIdx))), false), // next
	)
	got := g.wm.AddRecGroup(wasm.Rec(wasm.SubFinal([]uint32{obj}, panicComp)))
	g.types.ByName["$go.panicObj"] = got
}

func (g *gen) emitProgramTypes() error {
	// Emit IR TypeDefs dependency-first. Mutual recursion (waiter ↔ select,
	// chan ↔ waitq, …) is emitted as one Wasm rec group per SCC so field
	// heap types resolve to forward indices.
	deps := g.typeDepGraph()
	sccs := g.orderedTypeSCCs(deps)
	for _, members := range sccs {
		var pending []string
		for _, m := range members {
			if _, ok := g.types.ByName[m]; ok {
				continue
			}
			if g.m.LookupType(m) == nil {
				continue
			}
			pending = append(pending, m)
		}
		if len(pending) == 0 {
			continue
		}
		if len(pending) == 1 && !typeSelfRef(g.m.LookupType(pending[0])) {
			td := g.m.LookupType(pending[0])
			idx, err := g.addTypeDef(td)
			if err != nil {
				return err
			}
			g.types.ByName[td.Name] = idx
			continue
		}
		if err := g.emitTypeRecGroup(pending); err != nil {
			return err
		}
	}
	for _, td := range g.m.Types {
		if _, ok := g.types.ByName[td.Name]; ok {
			continue
		}
		idx, err := g.addTypeDef(td)
		if err != nil {
			return err
		}
		g.types.ByName[td.Name] = idx
	}
	return nil
}

func typeRefNames(t ir.Type) []string {
	r, ok := t.(*ir.Ref)
	if !ok || r.Named == "" {
		return nil
	}
	return []string{r.Named}
}

func typeSelfRef(td *ir.TypeDef) bool {
	if td == nil {
		return false
	}
	check := func(t ir.Type) bool {
		r, ok := t.(*ir.Ref)
		return ok && r.Named == td.Name
	}
	for _, f := range td.Fields {
		if check(f.Type) {
			return true
		}
	}
	if check(td.Elem) {
		return true
	}
	for _, p := range td.Params {
		if check(p) {
			return true
		}
	}
	for _, r := range td.Results {
		if check(r) {
			return true
		}
	}
	return false
}

func (g *gen) typeDepGraph() map[string][]string {
	deps := map[string][]string{}
	add := func(from, to string) {
		if from == "" || to == "" || from == to {
			return
		}
		if g.m.LookupType(to) == nil {
			return // prelude / abstract
		}
		deps[from] = append(deps[from], to)
	}
	for _, td := range g.m.Types {
		if td.Super != "" {
			add(td.Name, td.Super)
		}
		for _, f := range td.Fields {
			for _, n := range typeRefNames(f.Type) {
				add(td.Name, n)
			}
		}
		for _, p := range td.Params {
			for _, n := range typeRefNames(p) {
				add(td.Name, n)
			}
		}
		for _, r := range td.Results {
			for _, n := range typeRefNames(r) {
				add(td.Name, n)
			}
		}
		for _, n := range typeRefNames(td.Elem) {
			add(td.Name, n)
		}
		if _, ok := deps[td.Name]; !ok {
			deps[td.Name] = nil
		}
	}
	return deps
}

// orderedTypeSCCs returns SCCs in dependency-first order. Clos/cell types
// are preferred early so open-base subtypes see them.
func (g *gen) orderedTypeSCCs(deps map[string][]string) [][]string {
	var names []string
	seen := map[string]bool{}
	prefer := func(n string) bool {
		return n == "$go.clos" || strings.HasPrefix(n, "$go.cell.")
	}
	for _, td := range g.m.Types {
		if prefer(td.Name) && !seen[td.Name] {
			names = append(names, td.Name)
			seen[td.Name] = true
		}
	}
	for _, td := range g.m.Types {
		if !seen[td.Name] {
			names = append(names, td.Name)
			seen[td.Name] = true
		}
	}
	sccs := tarjanSCC(names, deps)
	indexOf := map[string]int{}
	for i, scc := range sccs {
		for _, n := range scc {
			indexOf[n] = i
		}
	}
	indeg := make([]int, len(sccs))
	edges := make([][]int, len(sccs))
	edgeSet := map[[2]int]bool{}
	for from, tos := range deps {
		fi, fok := indexOf[from]
		if !fok {
			continue
		}
		for _, to := range tos {
			ti, tok := indexOf[to]
			if !tok || fi == ti {
				continue
			}
			key := [2]int{fi, ti}
			if edgeSet[key] {
				continue
			}
			edgeSet[key] = true
			// Dependency "from → to" means to must be emitted before from.
			edges[ti] = append(edges[ti], fi)
			indeg[fi]++
		}
	}
	var q []int
	for i, d := range indeg {
		if d == 0 {
			q = append(q, i)
		}
	}
	var ordered [][]string
	seenSCC := make([]bool, len(sccs))
	for len(q) > 0 {
		i := q[0]
		q = q[1:]
		if seenSCC[i] {
			continue
		}
		seenSCC[i] = true
		ordered = append(ordered, sccs[i])
		for _, j := range edges[i] {
			indeg[j]--
			if indeg[j] == 0 {
				q = append(q, j)
			}
		}
	}
	for i, scc := range sccs {
		if !seenSCC[i] {
			ordered = append(ordered, scc)
		}
	}
	return ordered
}

func tarjanSCC(names []string, deps map[string][]string) [][]string {
	inNames := map[string]bool{}
	for _, n := range names {
		inNames[n] = true
	}
	index := map[string]int{}
	lowlink := map[string]int{}
	onstack := map[string]bool{}
	var stack []string
	var sccs [][]string
	idx := 0
	var strongconnect func(v string)
	strongconnect = func(v string) {
		index[v] = idx
		lowlink[v] = idx
		idx++
		stack = append(stack, v)
		onstack[v] = true
		for _, w := range deps[v] {
			if !inNames[w] {
				continue
			}
			if _, ok := index[w]; !ok {
				strongconnect(w)
				if lowlink[w] < lowlink[v] {
					lowlink[v] = lowlink[w]
				}
			} else if onstack[w] {
				if index[w] < lowlink[v] {
					lowlink[v] = index[w]
				}
			}
		}
		if lowlink[v] == index[v] {
			var scc []string
			for {
				n := len(stack) - 1
				w := stack[n]
				stack = stack[:n]
				onstack[w] = false
				scc = append(scc, w)
				if w == v {
					break
				}
			}
			sccs = append(sccs, scc)
		}
	}
	for _, n := range names {
		if _, ok := index[n]; !ok {
			strongconnect(n)
		}
	}
	return sccs
}

func (g *gen) emitTypeRecGroup(members []string) error {
	base := g.wm.NumTypes()
	for i, name := range members {
		g.types.ByName[name] = base + uint32(i)
	}
	subs := make([]wasm.SubType, 0, len(members))
	for _, name := range members {
		td := g.m.LookupType(name)
		if td == nil {
			return fmt.Errorf("codegen: rec group member %s missing", name)
		}
		sub, err := g.subTypeOf(td)
		if err != nil {
			return err
		}
		subs = append(subs, sub)
		if td.Kind == ir.TypeFunc {
			params, _ := g.valTypes(td.Params)
			results, _ := g.valTypes(td.Results)
			g.types.FuncSig[sigKey(params, results)] = base + uint32(len(subs)-1)
		}
	}
	got := g.wm.AddRecGroup(wasm.Rec(subs...))
	if got != base {
		return fmt.Errorf("codegen: rec group base mismatch for %v", members)
	}
	return nil
}

func (g *gen) subTypeOf(td *ir.TypeDef) (wasm.SubType, error) {
	var supers []uint32
	if td.Super != "" {
		sup, ok := g.types.ByName[td.Super]
		if !ok {
			return wasm.SubType{}, fmt.Errorf("codegen: type %s: unknown super %s", td.Name, td.Super)
		}
		supers = append(supers, sup)
	}
	switch td.Kind {
	case ir.TypeStruct:
		fields := make([]wasm.FieldType, 0, len(td.Fields))
		for _, f := range td.Fields {
			st, err := g.storageType(f.Type)
			if err != nil {
				return wasm.SubType{}, fmt.Errorf("codegen: type %s field %s: %w", td.Name, f.Name, err)
			}
			fields = append(fields, wasm.Field(st, f.Mut))
		}
		comp := wasm.StructType(fields...)
		if td.Name == "$go.clos" {
			return wasm.SubOpen(supers, comp), nil
		}
		if len(supers) > 0 {
			return wasm.SubFinal(supers, comp), nil
		}
		return wasm.FinalType(comp), nil
	case ir.TypeArray:
		st, err := g.storageType(td.Elem)
		if err != nil {
			return wasm.SubType{}, err
		}
		return wasm.FinalType(wasm.ArrayType(wasm.Field(st, td.ElemMut))), nil
	case ir.TypeFunc:
		params, err := g.valTypes(td.Params)
		if err != nil {
			return wasm.SubType{}, err
		}
		results, err := g.valTypes(td.Results)
		if err != nil {
			return wasm.SubType{}, err
		}
		return wasm.FinalType(wasm.FuncType(params, results)), nil
	default:
		return wasm.SubType{}, fmt.Errorf("codegen: unknown type kind for %s", td.Name)
	}
}

func (g *gen) addTypeDef(td *ir.TypeDef) (uint32, error) {
	var supers []uint32
	if td.Super != "" {
		sup, ok := g.types.ByName[td.Super]
		if !ok {
			return 0, fmt.Errorf("codegen: type %s: unknown super %s", td.Name, td.Super)
		}
		supers = append(supers, sup)
	}
	switch td.Kind {
	case ir.TypeStruct:
		// $go.defer is self-referential via link; emit as a rec group.
		if td.Name == "$go.defer" {
			obj := g.types.ByName["$go.object"]
			clos, ok := g.types.ByName["$go.clos"]
			if !ok {
				return 0, fmt.Errorf("codegen: type $go.defer: $go.clos not emitted yet")
			}
			deferIdx := g.wm.NumTypes()
			comp := wasm.StructType(
				wasm.Field(wasm.StorageVal(wasm.Ref(wasm.TypeIndexHeap(clos))), false),
				wasm.Field(wasm.StorageVal(wasm.RefNull(wasm.TypeIndexHeap(deferIdx))), true),
			)
			return g.wm.AddRecGroup(wasm.Rec(wasm.SubFinal([]uint32{obj}, comp))), nil
		}
		fields := make([]wasm.FieldType, 0, len(td.Fields))
		for _, f := range td.Fields {
			st, err := g.storageType(f.Type)
			if err != nil {
				return 0, fmt.Errorf("codegen: type %s field %s: %w", td.Name, f.Name, err)
			}
			fields = append(fields, wasm.Field(st, f.Mut))
		}
		comp := wasm.StructType(fields...)
		if len(supers) > 0 {
			// $go.clos is an open base for concrete closures (M5).
			if td.Name == "$go.clos" {
				return g.wm.AddType(wasm.SubOpen(supers, comp)), nil
			}
			return g.wm.AddType(wasm.SubFinal(supers, comp)), nil
		}
		return g.wm.AddType(wasm.FinalType(comp)), nil
	case ir.TypeArray:
		st, err := g.storageType(td.Elem)
		if err != nil {
			return 0, err
		}
		return g.wm.AddArrayType(wasm.Field(st, td.ElemMut)), nil
	case ir.TypeFunc:
		params, err := g.valTypes(td.Params)
		if err != nil {
			return 0, err
		}
		results, err := g.valTypes(td.Results)
		if err != nil {
			return 0, err
		}
		idx := g.wm.AddFuncType(params, results)
		g.types.FuncSig[sigKey(params, results)] = idx
		return idx, nil
	default:
		return 0, fmt.Errorf("codegen: unknown type kind for %s", td.Name)
	}
}

func (g *gen) storageType(t ir.Type) (wasm.StorageType, error) {
	if n, ok := t.(*ir.NumType); ok {
		switch n.Kind {
		case ir.I8:
			return wasm.StorageI8(), nil
		case ir.I16:
			return wasm.StorageI16(), nil
		}
	}
	vt, err := g.valType(t)
	if err != nil {
		return wasm.StorageType{}, err
	}
	return wasm.StorageVal(vt), nil
}

func (g *gen) valTypes(ts []ir.Type) ([]wasm.ValType, error) {
	out := make([]wasm.ValType, len(ts))
	for i, t := range ts {
		vt, err := g.valType(t)
		if err != nil {
			return nil, err
		}
		out[i] = vt
	}
	return out, nil
}

func (g *gen) valType(t ir.Type) (wasm.ValType, error) {
	if t == nil {
		return wasm.ValType{}, fmt.Errorf("nil type")
	}
	switch t := t.(type) {
	case *ir.NumType:
		switch t.Kind {
		case ir.I32:
			return wasm.I32(), nil
		case ir.I64:
			return wasm.I64(), nil
		case ir.F32:
			return wasm.F32(), nil
		case ir.F64:
			return wasm.F64(), nil
		case ir.I8, ir.I16:
			return wasm.ValType{}, fmt.Errorf("packed type %s is not a value type", t)
		}
	case *ir.Exn:
		return wasm.ExnRef(), nil
	case *ir.Extern:
		return wasm.ExternRef(), nil
	case *ir.Ref:
		ht, err := g.heapType(t)
		if err != nil {
			return wasm.ValType{}, err
		}
		if t.Null {
			return wasm.RefNull(ht), nil
		}
		return wasm.Ref(ht), nil
	case *ir.TypeDef:
		return wasm.ValType{}, fmt.Errorf("type def %s is not a value type; use ref", t.Name)
	case *ir.High:
		return wasm.ValType{}, fmt.Errorf("high type %s remains in low module", t)
	}
	return wasm.ValType{}, fmt.Errorf("unsupported type %s", t)
}

func (g *gen) heapType(r *ir.Ref) (wasm.HeapType, error) {
	if r.Func != nil {
		idx, ok := g.types.ByName[r.Func.Name]
		if !ok {
			return wasm.HeapType{}, fmt.Errorf("unknown func type %s", r.Func.Name)
		}
		return wasm.TypeIndexHeap(idx), nil
	}
	if r.Named != "" {
		if idx, ok := g.types.ByName[r.Named]; ok {
			return wasm.TypeIndexHeap(idx), nil
		}
		// Allow abstract names written as Named for heap kinds.
		switch r.Named {
		case "any":
			return wasm.HeapTypeAny(), nil
		case "eq":
			return wasm.HeapTypeEq(), nil
		case "i31":
			return wasm.HeapTypeI31(), nil
		case "struct":
			return wasm.HeapTypeStruct(), nil
		case "array":
			return wasm.HeapTypeArray(), nil
		case "none":
			return wasm.HeapTypeNone(), nil
		case "nofunc":
			return wasm.HeapTypeNoFunc(), nil
		case "noextern":
			return wasm.HeapTypeNoExtern(), nil
		case "func":
			return wasm.HeapTypeFunc(), nil
		}
		return wasm.HeapType{}, fmt.Errorf("unknown heap type %s", r.Named)
	}
	switch r.Heap {
	case ir.HeapAny:
		return wasm.HeapTypeAny(), nil
	case ir.HeapEq:
		return wasm.HeapTypeEq(), nil
	case ir.HeapI31:
		return wasm.HeapTypeI31(), nil
	case ir.HeapStruct:
		return wasm.HeapTypeStruct(), nil
	case ir.HeapArray:
		return wasm.HeapTypeArray(), nil
	case ir.HeapNone:
		return wasm.HeapTypeNone(), nil
	case ir.HeapNoFunc:
		return wasm.HeapTypeNoFunc(), nil
	case ir.HeapNoExtern:
		return wasm.HeapTypeNoExtern(), nil
	case ir.HeapFunc:
		return wasm.HeapTypeFunc(), nil
	}
	return wasm.HeapType{}, fmt.Errorf("invalid heap type")
}

func (g *gen) heapTypeOf(t ir.Type) (wasm.HeapType, error) {
	switch t := t.(type) {
	case *ir.Ref:
		return g.heapType(t)
	case *ir.TypeDef:
		idx, ok := g.types.ByName[t.Name]
		if !ok {
			return wasm.HeapType{}, fmt.Errorf("unknown type %s", t.Name)
		}
		return wasm.TypeIndexHeap(idx), nil
	default:
		// Type immediate may be written as a name-only Ref via parse.
		name := ir.TypeString(t)
		if idx, ok := g.types.ByName[name]; ok {
			return wasm.TypeIndexHeap(idx), nil
		}
		if strings.HasPrefix(name, "$") {
			return wasm.HeapType{}, fmt.Errorf("unknown type immediate %s", name)
		}
		return g.heapType(&ir.Ref{Named: name})
	}
}

func (g *gen) typeIndexOf(t ir.Type) (uint32, error) {
	name := typeName(t)
	if name == "" {
		return 0, fmt.Errorf("no type index for %s", t)
	}
	idx, ok := g.types.ByName[name]
	if !ok {
		return 0, fmt.Errorf("unknown type %s", name)
	}
	return idx, nil
}

func typeName(t ir.Type) string {
	switch t := t.(type) {
	case *ir.TypeDef:
		return t.Name
	case *ir.Ref:
		if t.Named != "" {
			return t.Named
		}
		if t.Func != nil {
			return t.Func.Name
		}
	}
	s := ir.TypeString(t)
	if strings.HasPrefix(s, "$") {
		return s
	}
	return ""
}

func (g *gen) internFuncType(params, results []wasm.ValType) uint32 {
	key := sigKey(params, results)
	if idx, ok := g.types.FuncSig[key]; ok {
		return idx
	}
	idx := g.wm.AddFuncType(params, results)
	g.types.FuncSig[key] = idx
	return idx
}

func sigKey(params, results []wasm.ValType) string {
	var b strings.Builder
	b.WriteByte('(')
	for i, p := range params {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%d:%d", p.Kind(), p.HeapCode())
	}
	b.WriteByte(')')
	for i, r := range results {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%d:%d", r.Kind(), r.HeapCode())
	}
	return b.String()
}

func (g *gen) fieldIndex(typeName, field string) (uint32, error) {
	td := g.m.LookupType(typeName)
	if td == nil {
		// Prelude types not in IR module — use fixed field layouts.
		switch typeName {
		case "$go.string":
			switch field {
			case "data", "0":
				return 0, nil
			case "off", "1":
				return 1, nil
			case "len", "2":
				return 2, nil
			}
		case "$go.iface":
			switch field {
			case "itab", "0":
				return 0, nil
			case "data", "1":
				return 1, nil
			}
		case "$go.panicObj":
			switch field {
			case "value", "0":
				return 0, nil
			case "recovered", "1":
				return 1, nil
			case "next", "2":
				return 2, nil
			}
		case "$go.defer":
			switch field {
			case "fn", "0":
				return 0, nil
			case "link", "1":
				return 1, nil
			}
		case "$go.g":
			switch field {
			case "atomicstatus", "0":
				return 0, nil
			case "throwsplit", "1":
				return 1, nil
			case "_panic", "2":
				return 2, nil
			case "racing", "3":
				return 3, nil
			}
		}
		if strings.HasPrefix(typeName, "$go.slice.") {
			switch field {
			case "data", "0":
				return 0, nil
			case "off", "1":
				return 1, nil
			case "len", "2":
				return 2, nil
			case "cap", "3":
				return 3, nil
			}
		}
		if strings.HasPrefix(typeName, "$go.class.") {
			switch field {
			case "get", "0":
				return 0, nil
			case "set", "1":
				return 1, nil
			}
		}
		if strings.HasPrefix(typeName, "$go.ptr.") {
			switch field {
			case "class", "0":
				return 0, nil
			case "base", "1":
				return 1, nil
			case "index", "2":
				return 2, nil
			}
		}
		return 0, fmt.Errorf("unknown type %s for field %s", typeName, field)
	}
	if n, err := parseUint(field); err == nil {
		return n, nil
	}
	for i, f := range td.Fields {
		if f.Name == field {
			return uint32(i), nil
		}
	}
	return 0, fmt.Errorf("type %s has no field %s", typeName, field)
}

func parseUint(s string) (uint32, error) {
	var n uint32
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("not a number")
		}
		n = n*10 + uint32(c-'0')
	}
	return n, nil
}
