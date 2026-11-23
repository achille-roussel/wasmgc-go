package check

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"strings"
)

const KindReflect = Kind("reflect")

// CheckReflectConstructors reports reachable reflect type-constructor calls
// whose result is not in the closed Wasm type universe.
// See docs/design/05-unsafe-and-reflect.md.
//
// Analyzes SliceOf, PointerTo/PtrTo, MapOf, ArrayOf, ChanOf, FuncOf, and StructOf.
// Dead code (functions never called from main/init) is skipped when reachable
// is provided; if reachable is nil, every call site is checked (conservative).
func CheckReflectConstructors(fset *token.FileSet, info *types.Info, files []*ast.File, reachable map[*types.Func]bool) []Diagnostic {
	a := newReflectAnalysis(info, files)
	var diags []Diagnostic
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			fd, ok := n.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				return true
			}
			if reachable != nil {
				obj, _ := info.Defs[fd.Name].(*types.Func)
				if obj != nil && !reachable[obj] {
					return false
				}
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				ce, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if d := a.checkCall(fset, ce); d != nil {
					diags = append(diags, *d)
				}
				return true
			})
			return false
		})
	}
	return diags
}

// absType is one possible reflect.Type value: a Go type, optionally marked as
// produced by a constructor (in U but not thereby in B).
type absType struct {
	typ         types.Type
	constructed bool
}

// absSet is the abstract set of types a reflect.Type expression may hold.
// allB means the set is every type in B (unbounded interface TypeOf / Value.Type).
type absSet struct {
	types []absType
	allB  bool
}

func (s absSet) empty() bool { return !s.allB && len(s.types) == 0 }

func (s absSet) union(o absSet) absSet {
	if s.allB || o.allB {
		return absSet{allB: true}
	}
	out := absSet{types: append([]absType{}, s.types...)}
	for _, t := range o.types {
		out = out.add(t)
	}
	return out
}

func (s absSet) add(t absType) absSet {
	if s.allB || t.typ == nil {
		return s
	}
	for _, e := range s.types {
		if types.Identical(e.typ, t.typ) && e.constructed == t.constructed {
			return s
		}
	}
	s.types = append(s.types, t)
	return s
}

type reflectAnalysis struct {
	info *types.Info
	B    []types.Type          // universe base types from the program
	env  map[*types.Var]absSet // local/param bindings of reflect.Type
}

func newReflectAnalysis(info *types.Info, files []*ast.File) *reflectAnalysis {
	a := &reflectAnalysis{
		info: info,
		env:  map[*types.Var]absSet{},
	}
	a.B = collectUniverseB(info)
	a.seedEnv(files)
	// Iterate assignment propagation a few times for local Type bindings.
	for i := 0; i < 4; i++ {
		changed := a.propagate(files)
		if !changed {
			break
		}
	}
	return a
}

func collectUniverseB(info *types.Info) []types.Type {
	seen := map[string]types.Type{}
	var add func(t types.Type)
	add = func(t types.Type) {
		if t == nil {
			return
		}
		t = types.Unalias(t)
		if _, isTP := t.(*types.TypeParam); isTP {
			return
		}
		key := typeKey(t)
		if key == "" {
			return
		}
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = t
		switch u := t.Underlying().(type) {
		case *types.Pointer:
			add(u.Elem())
		case *types.Slice:
			add(u.Elem())
		case *types.Array:
			add(u.Elem())
		case *types.Map:
			add(u.Key())
			add(u.Elem())
		case *types.Chan:
			add(u.Elem())
		case *types.Struct:
			for i := 0; i < u.NumFields(); i++ {
				add(u.Field(i).Type())
			}
		case *types.Signature:
			if u.Recv() != nil {
				add(u.Recv().Type())
			}
			if p := u.Params(); p != nil {
				for i := 0; i < p.Len(); i++ {
					add(p.At(i).Type())
				}
			}
			if r := u.Results(); r != nil {
				for i := 0; i < r.Len(); i++ {
					add(r.At(i).Type())
				}
			}
		case *types.Interface:
			for i := 0; i < u.NumMethods(); i++ {
				add(u.Method(i).Type())
			}
		}
		if n, ok := t.(*types.Named); ok {
			add(n.Underlying())
		}
	}
	if info != nil {
		for _, tv := range info.Types {
			add(tv.Type)
		}
		for _, obj := range info.Defs {
			if obj != nil {
				add(obj.Type())
			}
		}
		for _, obj := range info.Uses {
			if obj != nil {
				add(obj.Type())
			}
		}
	}
	out := make([]types.Type, 0, len(seen))
	for _, t := range seen {
		out = append(out, t)
	}
	return out
}

func (a *reflectAnalysis) inB(t types.Type) bool {
	if t == nil {
		return false
	}
	for _, b := range a.B {
		if types.Identical(b, t) {
			return true
		}
	}
	return false
}

// inU reports whether t is in the closed universe U = B ∪ {*T,[]T,map[T]struct{}}.
func (a *reflectAnalysis) inU(t types.Type) bool {
	if a.inB(t) {
		return true
	}
	t = types.Unalias(t)
	switch u := t.(type) {
	case *types.Pointer:
		return a.inB(u.Elem())
	case *types.Slice:
		return a.inB(u.Elem())
	case *types.Map:
		if !isEmptyStruct(u.Elem()) {
			return false
		}
		return a.inB(u.Key()) && types.Comparable(u.Key())
	}
	// Named / underlying forms already covered via Identical on B entries.
	if n, ok := t.(*types.Named); ok {
		return a.inU(n.Underlying())
	}
	return false
}

func isEmptyStruct(t types.Type) bool {
	if t == nil {
		return false
	}
	st, ok := types.Unalias(t).Underlying().(*types.Struct)
	return ok && st.NumFields() == 0
}

func (a *reflectAnalysis) seedEnv(files []*ast.File) {
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			as, ok := n.(*ast.AssignStmt)
			if !ok {
				return true
			}
			a.noteAssign(as)
			return true
		})
	}
}

func (a *reflectAnalysis) propagate(files []*ast.File) bool {
	before := len(a.env)
	var beforeSizes int
	for _, s := range a.env {
		beforeSizes += len(s.types)
		if s.allB {
			beforeSizes += 1000
		}
	}
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.AssignStmt:
				a.noteAssign(n)
			case *ast.ValueSpec:
				for i, name := range n.Names {
					v, _ := a.info.Defs[name].(*types.Var)
					if v == nil || !a.isReflectType(v.Type()) {
						continue
					}
					var rhs absSet
					if n.Values != nil && i < len(n.Values) {
						rhs = a.evalTypeExpr(n.Values[i])
					}
					a.env[v] = a.env[v].union(rhs)
				}
			}
			return true
		})
	}
	afterSizes := 0
	for _, s := range a.env {
		afterSizes += len(s.types)
		if s.allB {
			afterSizes += 1000
		}
	}
	return len(a.env) != before || afterSizes != beforeSizes
}

func (a *reflectAnalysis) noteAssign(as *ast.AssignStmt) {
	if len(as.Lhs) != len(as.Rhs) && len(as.Rhs) != 1 {
		return
	}
	for i, lhs := range as.Lhs {
		id, ok := lhs.(*ast.Ident)
		if !ok {
			continue
		}
		v, _ := a.info.Defs[id].(*types.Var)
		if v == nil {
			v, _ = a.info.Uses[id].(*types.Var)
		}
		if v == nil || !a.isReflectType(v.Type()) {
			continue
		}
		var rhs absSet
		if len(as.Rhs) == 1 && len(as.Lhs) > 1 {
			// multi-value; skip precise tracking
			continue
		}
		if i < len(as.Rhs) {
			rhs = a.evalTypeExpr(as.Rhs[i])
		}
		a.env[v] = a.env[v].union(rhs)
	}
}

func (a *reflectAnalysis) isReflectType(t types.Type) bool {
	if t == nil {
		return false
	}
	n, ok := types.Unalias(t).(*types.Named)
	if !ok {
		// interface Type from package reflect may appear as Named or as interface named Type
		if iface, ok := types.Unalias(t).(*types.Interface); ok {
			// Heuristic: method Elem() Type
			for i := 0; i < iface.NumMethods(); i++ {
				if iface.Method(i).Name() == "Elem" {
					return true
				}
			}
		}
		return false
	}
	obj := n.Obj()
	if obj == nil || obj.Pkg() == nil {
		return false
	}
	return obj.Pkg().Path() == "reflect" && obj.Name() == "Type"
}

func (a *reflectAnalysis) evalTypeExpr(e ast.Expr) absSet {
	if e == nil {
		return absSet{}
	}
	switch e := e.(type) {
	case *ast.Ident:
		if v, _ := a.info.Uses[e].(*types.Var); v != nil {
			if s, ok := a.env[v]; ok {
				return s
			}
		}
		if v, _ := a.info.Defs[e].(*types.Var); v != nil {
			if s, ok := a.env[v]; ok {
				return s
			}
		}
		return absSet{}
	case *ast.CallExpr:
		return a.evalCall(e)
	case *ast.SelectorExpr:
		// t.Elem / t.Key / sf.Type — method or field
		if sel := a.info.Selections[e]; sel != nil {
			recv := a.evalTypeExpr(e.X)
			switch sel.Kind() {
			case types.MethodVal, types.MethodExpr:
				switch e.Sel.Name {
				case "Elem":
					return a.mapComponent(recv, true)
				case "Key":
					return a.mapComponent(recv, false)
				case "Type":
					// Value.Type() — treat as all of B (conservative) unless we track Values.
					return absSet{allB: true}
				}
			case types.FieldVal:
				if e.Sel.Name == "Type" {
					// StructField.Type — unknown field type → B
					return absSet{allB: true}
				}
			}
		}
		// Package-level: handled via CallExpr Fun.
		return absSet{}
	case *ast.IndexExpr:
		// TypeFor[T]() is CallExpr with Fun = IndexExpr{TypeFor, T}
		return absSet{}
	case *ast.ParenExpr:
		return a.evalTypeExpr(e.X)
	}
	return absSet{}
}

func (a *reflectAnalysis) mapComponent(recv absSet, elem bool) absSet {
	if recv.allB {
		// Components of types in B are in B.
		return absSet{allB: true}
	}
	var out absSet
	for _, at := range recv.types {
		if at.typ == nil {
			continue
		}
		u := at.typ.Underlying()
		var comp types.Type
		switch u := u.(type) {
		case *types.Pointer:
			if elem {
				comp = u.Elem()
			}
		case *types.Slice, *types.Array:
			if elem {
				if s, ok := u.(*types.Slice); ok {
					comp = s.Elem()
				} else {
					comp = u.(*types.Array).Elem()
				}
			}
		case *types.Map:
			if elem {
				comp = u.Elem()
			} else {
				comp = u.Key()
			}
		case *types.Chan:
			if elem {
				comp = u.Elem()
			}
		}
		if comp != nil {
			// Component of a type in B is in B; of a constructed type stays constructed
			// only if we synthesized it — components of constructed are still "from B
			// structure" when the constructed wrapper was one-step. Mark not constructed
			// when the component itself is in B.
			out = out.add(absType{typ: comp, constructed: !a.inB(comp)})
		}
	}
	return out
}

func (a *reflectAnalysis) evalCall(ce *ast.CallExpr) absSet {
	name, ok := reflectCtorName(a.info, ce)
	if !ok {
		// Method call: t.Elem() etc.
		if sel, ok := ce.Fun.(*ast.SelectorExpr); ok {
			return a.evalTypeExpr(sel)
		}
		return absSet{}
	}
	switch name {
	case "TypeOf":
		if len(ce.Args) != 1 {
			return absSet{}
		}
		tv, ok := a.info.Types[ce.Args[0]]
		if !ok || tv.Type == nil {
			return absSet{allB: true}
		}
		t := types.Unalias(tv.Type)
		if _, isIface := t.Underlying().(*types.Interface); isIface {
			return absSet{allB: true}
		}
		return absSet{types: []absType{{typ: t}}}
	case "TypeFor":
		if t := typeForArg(a.info, ce); t != nil {
			return absSet{types: []absType{{typ: t}}}
		}
		return absSet{}
	case "SliceOf":
		if len(ce.Args) != 1 {
			return absSet{}
		}
		arg := a.evalTypeExpr(ce.Args[0])
		return a.constructSlice(arg)
	case "PointerTo", "PtrTo":
		if len(ce.Args) != 1 {
			return absSet{}
		}
		arg := a.evalTypeExpr(ce.Args[0])
		return a.constructPointer(arg)
	case "MapOf":
		if len(ce.Args) != 2 {
			return absSet{}
		}
		k := a.evalTypeExpr(ce.Args[0])
		v := a.evalTypeExpr(ce.Args[1])
		return a.constructMap(k, v)
	default:
		// StructOf / ArrayOf / ChanOf / FuncOf — result unknown constructed.
		return absSet{types: []absType{{constructed: true}}}
	}
}

func (a *reflectAnalysis) constructSlice(arg absSet) absSet {
	if arg.allB {
		var out absSet
		for _, t := range a.B {
			out = out.add(absType{typ: types.NewSlice(t), constructed: true})
		}
		return out
	}
	var out absSet
	for _, at := range arg.types {
		if at.typ == nil {
			continue
		}
		out = out.add(absType{typ: types.NewSlice(at.typ), constructed: true})
	}
	return out
}

func (a *reflectAnalysis) constructPointer(arg absSet) absSet {
	if arg.allB {
		var out absSet
		for _, t := range a.B {
			out = out.add(absType{typ: types.NewPointer(t), constructed: true})
		}
		return out
	}
	var out absSet
	for _, at := range arg.types {
		if at.typ == nil {
			continue
		}
		out = out.add(absType{typ: types.NewPointer(at.typ), constructed: true})
	}
	return out
}

func (a *reflectAnalysis) constructMap(k, v absSet) absSet {
	var keys []absType
	if k.allB {
		for _, t := range a.B {
			keys = append(keys, absType{typ: t})
		}
	} else {
		keys = k.types
	}
	var vals []absType
	if v.allB {
		for _, t := range a.B {
			vals = append(vals, absType{typ: t})
		}
	} else {
		vals = v.types
	}
	var out absSet
	for _, kk := range keys {
		for _, vv := range vals {
			if kk.typ == nil || vv.typ == nil {
				continue
			}
			out = out.add(absType{typ: types.NewMap(kk.typ, vv.typ), constructed: true})
		}
	}
	return out
}

func (a *reflectAnalysis) checkCall(fset *token.FileSet, ce *ast.CallExpr) *Diagnostic {
	name, ok := reflectCtorName(a.info, ce)
	if !ok {
		return nil
	}
	switch name {
	case "StructOf":
		return a.checkStructOf(fset, ce)
	case "SliceOf":
		return a.checkSliceOf(fset, ce)
	case "PointerTo", "PtrTo":
		return a.checkPointerTo(fset, ce, name)
	case "MapOf":
		return a.checkMapOf(fset, ce)
	case "ArrayOf":
		return a.checkArrayOf(fset, ce)
	case "ChanOf":
		return a.checkChanOf(fset, ce)
	case "FuncOf":
		return a.checkFuncOf(fset, ce)
	}
	return nil
}

func reflectCtorName(info *types.Info, ce *ast.CallExpr) (string, bool) {
	switch fun := ce.Fun.(type) {
	case *ast.SelectorExpr:
		pkgName, ok := fun.X.(*ast.Ident)
		if !ok {
			return "", false
		}
		obj := info.Uses[pkgName]
		pn, ok := obj.(*types.PkgName)
		if !ok || pn.Imported() == nil || pn.Imported().Path() != "reflect" {
			return "", false
		}
		switch fun.Sel.Name {
		case "StructOf", "SliceOf", "PointerTo", "PtrTo", "MapOf",
			"ArrayOf", "ChanOf", "FuncOf", "TypeOf", "TypeFor":
			return fun.Sel.Name, true
		}
	case *ast.IndexExpr:
		// reflect.TypeFor[T]
		if sel, ok := fun.X.(*ast.SelectorExpr); ok {
			pkgName, ok := sel.X.(*ast.Ident)
			if !ok {
				return "", false
			}
			obj := info.Uses[pkgName]
			pn, ok := obj.(*types.PkgName)
			if !ok || pn.Imported() == nil || pn.Imported().Path() != "reflect" {
				return "", false
			}
			if sel.Sel.Name == "TypeFor" {
				return "TypeFor", true
			}
		}
	case *ast.IndexListExpr:
		if sel, ok := fun.X.(*ast.SelectorExpr); ok {
			pkgName, ok := sel.X.(*ast.Ident)
			if !ok {
				return "", false
			}
			obj := info.Uses[pkgName]
			pn, ok := obj.(*types.PkgName)
			if !ok || pn.Imported() == nil || pn.Imported().Path() != "reflect" {
				return "", false
			}
			if sel.Sel.Name == "TypeFor" {
				return "TypeFor", true
			}
		}
	}
	return "", false
}

func typeForArg(info *types.Info, ce *ast.CallExpr) types.Type {
	switch fun := ce.Fun.(type) {
	case *ast.IndexExpr:
		if tv, ok := info.Types[fun.Index]; ok {
			return tv.Type
		}
		// Type instance may be recorded on the call.
	case *ast.IndexListExpr:
		if len(fun.Indices) == 1 {
			if tv, ok := info.Types[fun.Indices[0]]; ok {
				return tv.Type
			}
		}
	}
	// Fallback: result type of TypeFor[T]() is reflect.Type; look at type args
	// via Instances if present.
	if info.Instances != nil {
		if id, ok := unwrapTypeForIdent(ce.Fun); ok {
			if inst, ok := info.Instances[id]; ok && inst.TypeArgs.Len() == 1 {
				return inst.TypeArgs.At(0)
			}
		}
	}
	return nil
}

func unwrapTypeForIdent(fun ast.Expr) (*ast.Ident, bool) {
	switch fun := fun.(type) {
	case *ast.IndexExpr:
		if sel, ok := fun.X.(*ast.SelectorExpr); ok {
			return sel.Sel, true
		}
	case *ast.IndexListExpr:
		if sel, ok := fun.X.(*ast.SelectorExpr); ok {
			return sel.Sel, true
		}
	}
	return nil, false
}

func (a *reflectAnalysis) checkStructOf(fset *token.FileSet, ce *ast.CallExpr) *Diagnostic {
	if len(ce.Args) != 1 {
		return nil
	}
	if isConstantStructFieldList(a.info, ce.Args[0]) {
		return nil
	}
	expr := exprString(fset, ce)
	msg := fmt.Sprintf("reflect.StructOf result is not a Wasm type in this program\n    %s\n    the field list is not a constant struct type this program already declares.\n    Wasm GC cannot mint a struct type at run time.\n    this program's universe is the types it writes, plus one step of *T, []T,\n    and map[T]struct{} for a comparable T.", expr)
	return reflectDiag(fset, ce, expr, msg)
}

func (a *reflectAnalysis) checkSliceOf(fset *token.FileSet, ce *ast.CallExpr) *Diagnostic {
	if len(ce.Args) != 1 {
		return nil
	}
	arg := a.evalTypeExpr(ce.Args[0])
	if a.sliceOfLegal(arg) {
		return nil
	}
	expr := exprString(fset, ce)
	msg := fmt.Sprintf("reflect.SliceOf result is not a Wasm type in this program\n    %s\n    the element type is not in this program's type universe B (or a constructed argument whose []T is already in U).\n    Wasm GC cannot mint a slice type at run time.\n    this program's universe is the types it writes, plus one step of *T, []T,\n    and map[T]struct{} for a comparable T.", expr)
	return reflectDiag(fset, ce, expr, msg)
}

func (a *reflectAnalysis) sliceOfLegal(arg absSet) bool {
	if arg.empty() {
		// Unknown — conservative reject would break incomplete tracking; treat
		// empty as "could not pin down" → illegal.
		return false
	}
	if arg.allB {
		return true // every possible t ∈ B
	}
	for _, at := range arg.types {
		if at.typ == nil {
			return false
		}
		if at.constructed {
			// Legal only when []t is already in U.
			sl := types.NewSlice(at.typ)
			if !a.inU(sl) {
				return false
			}
			continue
		}
		if !a.inB(at.typ) {
			return false
		}
	}
	return true
}

func (a *reflectAnalysis) checkPointerTo(fset *token.FileSet, ce *ast.CallExpr, name string) *Diagnostic {
	if len(ce.Args) != 1 {
		return nil
	}
	arg := a.evalTypeExpr(ce.Args[0])
	if a.pointerToLegal(arg) {
		return nil
	}
	expr := exprString(fset, ce)
	msg := fmt.Sprintf("reflect.%s result is not a Wasm type in this program\n    %s\n    the element type is not in this program's type universe B (or a constructed argument whose *T is already in U).\n    Wasm GC cannot mint a pointer type at run time.\n    this program's universe is the types it writes, plus one step of *T, []T,\n    and map[T]struct{} for a comparable T.", name, expr)
	return reflectDiag(fset, ce, expr, msg)
}

func (a *reflectAnalysis) pointerToLegal(arg absSet) bool {
	if arg.empty() {
		return false
	}
	if arg.allB {
		return true
	}
	for _, at := range arg.types {
		if at.typ == nil {
			return false
		}
		if at.constructed {
			if !a.inU(types.NewPointer(at.typ)) {
				return false
			}
			continue
		}
		if !a.inB(at.typ) {
			return false
		}
	}
	return true
}

func (a *reflectAnalysis) checkMapOf(fset *token.FileSet, ce *ast.CallExpr) *Diagnostic {
	if len(ce.Args) != 2 {
		return nil
	}
	k := a.evalTypeExpr(ce.Args[0])
	v := a.evalTypeExpr(ce.Args[1])
	if a.mapOfLegal(k, v) {
		return nil
	}
	expr := exprString(fset, ce)
	msg := fmt.Sprintf("reflect.MapOf result is not a Wasm type in this program\n    %s\n    the map type is not in this program's universe (need v exactly struct{} and k in B, or an exact map already in B).\n    Wasm GC cannot mint a map type at run time.\n    this program's universe is the types it writes, plus one step of *T, []T,\n    and map[T]struct{} for a comparable T.", expr)
	return reflectDiag(fset, ce, expr, msg)
}

func (a *reflectAnalysis) mapOfLegal(k, v absSet) bool {
	if k.empty() || v.empty() {
		return false
	}
	// Expand keys.
	var keys []absType
	if k.allB {
		for _, t := range a.B {
			keys = append(keys, absType{typ: t})
		}
	} else {
		keys = k.types
	}
	var vals []absType
	if v.allB {
		// Unknown value type spanning B is not legal for MapOf (only struct{} closure).
		return false
	} else {
		vals = v.types
	}
	if len(keys) == 0 || len(vals) == 0 {
		return false
	}
	for _, kk := range keys {
		for _, vv := range vals {
			if kk.typ == nil || vv.typ == nil {
				return false
			}
			m := types.NewMap(kk.typ, vv.typ)
			if a.inB(m) {
				continue
			}
			// One-step closure: v exactly struct{} and k in B.
			if isEmptyStruct(vv.typ) && a.inB(kk.typ) {
				continue
			}
			if a.inU(m) {
				continue
			}
			return false
		}
	}
	return true
}

func (a *reflectAnalysis) checkArrayOf(fset *token.FileSet, ce *ast.CallExpr) *Diagnostic {
	if len(ce.Args) != 2 {
		return nil
	}
	// n must be constant; [n]T must be in B.
	tv, ok := a.info.Types[ce.Args[0]]
	if !ok || tv.Value == nil {
		return arrayOfDiag(fset, ce)
	}
	n, ok := constant.Int64Val(tv.Value)
	if !ok {
		return arrayOfDiag(fset, ce)
	}
	elem := a.evalTypeExpr(ce.Args[1])
	if elem.allB || elem.empty() {
		return arrayOfDiag(fset, ce)
	}
	for _, at := range elem.types {
		if at.typ == nil {
			return arrayOfDiag(fset, ce)
		}
		arr := types.NewArray(at.typ, n)
		if !a.inB(arr) {
			return arrayOfDiag(fset, ce)
		}
	}
	return nil
}

func (a *reflectAnalysis) checkChanOf(fset *token.FileSet, ce *ast.CallExpr) *Diagnostic {
	// Exact chan type must be in B. Without precise dir+elem tracking of all
	// pairs, require a singleton elem in B and that some chan of that elem is in B.
	if len(ce.Args) != 2 {
		return nil
	}
	elem := a.evalTypeExpr(ce.Args[1])
	if elem.allB || elem.empty() {
		return chanOfDiag(fset, ce)
	}
	for _, at := range elem.types {
		if at.typ == nil || at.constructed {
			return chanOfDiag(fset, ce)
		}
		found := false
		for _, b := range a.B {
			ch, ok := b.Underlying().(*types.Chan)
			if ok && types.Identical(ch.Elem(), at.typ) {
				found = true
				break
			}
		}
		if !found {
			return chanOfDiag(fset, ce)
		}
	}
	return nil
}

func (a *reflectAnalysis) checkFuncOf(fset *token.FileSet, ce *ast.CallExpr) *Diagnostic {
	// Non-constant slices of parameter types are not legal.
	if len(ce.Args) < 2 {
		return nil
	}
	if !isConstantTypeSlice(ce.Args[0]) || !isConstantTypeSlice(ce.Args[1]) {
		return funcOfDiag(fset, ce)
	}
	// Constant shape still must match a func in B — without reconstructing the
	// signature precisely, accept constant lits (same leniency as StructOf lit).
	return nil
}

func isConstantTypeSlice(e ast.Expr) bool {
	switch e.(type) {
	case *ast.CompositeLit:
		return true
	default:
		return false
	}
}

func isConstantStructFieldList(info *types.Info, e ast.Expr) bool {
	switch e.(type) {
	case *ast.CompositeLit:
		return true
	default:
		return false
	}
}

func reflectDiag(fset *token.FileSet, ce *ast.CallExpr, expr, msg string) *Diagnostic {
	return &Diagnostic{
		Pos:        fset.Position(ce.Pos()),
		Severity:   SeverityError,
		Kind:       KindReflect,
		Message:    msg,
		Expression: expr,
	}
}

func arrayOfDiag(fset *token.FileSet, ce *ast.CallExpr) *Diagnostic {
	expr := exprString(fset, ce)
	msg := fmt.Sprintf("reflect.ArrayOf result is not a Wasm type in this program\n    %s\n    the array length is not a constant or [n]T is not a type this program already declares.\n    Wasm GC cannot mint an array type at run time.\n    this program's universe is the types it writes, plus one step of *T, []T,\n    and map[T]struct{} for a comparable T.", expr)
	return reflectDiag(fset, ce, expr, msg)
}

func chanOfDiag(fset *token.FileSet, ce *ast.CallExpr) *Diagnostic {
	expr := exprString(fset, ce)
	msg := fmt.Sprintf("reflect.ChanOf result is not a Wasm type in this program\n    %s\n    the exact chan type is not a type this program already declares.\n    Wasm GC cannot mint a chan type at run time.\n    this program's universe is the types it writes, plus one step of *T, []T,\n    and map[T]struct{} for a comparable T.", expr)
	return reflectDiag(fset, ce, expr, msg)
}

func funcOfDiag(fset *token.FileSet, ce *ast.CallExpr) *Diagnostic {
	expr := exprString(fset, ce)
	msg := fmt.Sprintf("reflect.FuncOf result is not a Wasm type in this program\n    %s\n    the parameter list is not a constant signature this program already declares.\n    Wasm GC cannot mint a func type at run time.\n    this program's universe is the types it writes, plus one step of *T, []T,\n    and map[T]struct{} for a comparable T.", expr)
	return reflectDiag(fset, ce, expr, msg)
}

// typeKey mirrors desc.TypeKey for universe membership strings.
func typeKey(t types.Type) string {
	t = types.Unalias(t)
	switch t := t.(type) {
	case *types.Basic:
		return t.Name()
	case *types.Named:
		pkg := ""
		if t.Obj().Pkg() != nil {
			pkg = t.Obj().Pkg().Path() + "."
		}
		return pkg + t.Obj().Name()
	case *types.Pointer:
		return "*" + typeKey(t.Elem())
	case *types.Slice:
		return "[]" + typeKey(t.Elem())
	case *types.Array:
		return fmt.Sprintf("[%d]%s", t.Len(), typeKey(t.Elem()))
	case *types.Map:
		return "map[" + typeKey(t.Key()) + "]" + typeKey(t.Elem())
	case *types.Struct:
		if t.NumFields() == 0 {
			return "struct{}"
		}
		return strings.ReplaceAll(t.String(), " ", "_")
	case *types.Interface:
		if t.Empty() {
			return "any"
		}
		return "iface." + t.String()
	case *types.Chan:
		return strings.ReplaceAll(t.String(), " ", "_")
	case *types.Signature:
		return strings.ReplaceAll(t.String(), " ", "_")
	default:
		return strings.ReplaceAll(t.String(), " ", "_")
	}
}

// ReachableFuncs returns functions reachable from main.main / init in prog packages.
// Conservative: if analysis fails, returns nil (check all).
func ReachableFuncs(pkgs []*types.Package, info *types.Info, files []*ast.File) map[*types.Func]bool {
	reachable := map[*types.Func]bool{}
	var queue []*types.Func
	for _, f := range files {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			fn, _ := info.Defs[fd.Name].(*types.Func)
			if fn == nil {
				continue
			}
			if fd.Name.Name == "main" || fd.Name.Name == "init" {
				if !reachable[fn] {
					reachable[fn] = true
					queue = append(queue, fn)
				}
			}
		}
	}
	bodies := map[*types.Func]*ast.BlockStmt{}
	for _, f := range files {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			if fn, _ := info.Defs[fd.Name].(*types.Func); fn != nil {
				bodies[fn] = fd.Body
			}
		}
	}
	for len(queue) > 0 {
		fn := queue[0]
		queue = queue[1:]
		body := bodies[fn]
		if body == nil {
			continue
		}
		ast.Inspect(body, func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if !ok {
				return true
			}
			cal, _ := info.Uses[id].(*types.Func)
			if cal == nil || reachable[cal] {
				return true
			}
			reachable[cal] = true
			queue = append(queue, cal)
			return true
		})
	}
	_ = pkgs
	return reachable
}
