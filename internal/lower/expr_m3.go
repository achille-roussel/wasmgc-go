package lower

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
)

func (fl *funcLower) addrOf(x ast.Expr, want ir.Type) (ir.Expr, []ir.Stmt, error) {
	ptrTy := want
	if ptrTy == nil {
		if xt := fl.typeOf(x); xt != nil {
			var err error
			ptrTy, err = fl.mapType(types.NewPointer(xt))
			if err != nil {
				return nil, nil, err
			}
		}
	}
	// Named structs / arrays are heap refs: *T and T share the same IR type, so
	// &x is just x (the object ref). Fat pointers are only for scalars and for
	// addressable slots of non-object element types.
	if xt := fl.typeOf(x); xt != nil && isObjectPointer(types.NewPointer(xt)) {
		return fl.expr(x, ptrTy)
	}
	switch x := x.(type) {
	case *ast.Ident:
		if g := fl.lookupGlobal(x); g != nil {
			base := &ir.Instr{Op: ir.OpGlobalGet, Global: g.Name}
			return &ir.Instr{Op: ir.OpAddr, Args: []ir.Expr{base}, Type: ptrTy}, nil, nil
		}
		obj := fl.info.Uses[x]
		if obj == nil {
			obj = fl.info.Defs[x]
		}
		v, err := fl.lookupVar(x)
		if err != nil {
			return nil, nil, err
		}
		// Wasm locals are not addressable. Spill into a cell and take the
		// address of cell.v (design 03: escaping &local).
		if obj != nil {
			elemTy := v.Typ
			if fl.cells != nil {
				if cv, ok := fl.cells[obj]; ok {
					return &ir.Instr{
						Op:    ir.OpAddr,
						Args:  []ir.Expr{fl.useVar(cv)},
						Field: "v",
						Type:  ptrTy,
					}, nil, nil
				}
			}
			cellName, err := fl.pkg.ensureCellType(elemTy)
			if err != nil {
				return nil, nil, err
			}
			if err := fl.ensureLocalCell(obj, cellName, elemTy); err != nil {
				return nil, nil, err
			}
			fl.markAssigned(obj)
			cv := fl.cells[obj]
			// Sync plain local → cell (assignments before ensureLocalCell only
			// updated the local), same as closure capture-by-ref.
			var stmts []ir.Stmt
			stmts = append(stmts, &ir.ExprStmt{X: &ir.Instr{
				Op:    ir.OpStructSet,
				Type:  &ir.Ref{Named: cellName},
				Field: "v",
				Args:  []ir.Expr{fl.useVar(cv), fl.useVar(v)},
			}})
			return &ir.Instr{
				Op:    ir.OpAddr,
				Args:  []ir.Expr{fl.useVar(cv)},
				Field: "v",
				Type:  ptrTy,
			}, stmts, nil
		}
		return &ir.Instr{Op: ir.OpAddr, Args: []ir.Expr{fl.useVar(v)}, Type: ptrTy}, nil, nil
	case *ast.SelectorExpr:
		// &s.f or &p.f
		if sel := fl.info.Selections[x]; sel != nil && sel.Kind() == types.FieldVal {
			base, stmts, err := fl.expr(x.X, nil)
			if err != nil {
				return nil, nil, err
			}
			// If X is a pointer to struct, deref first for object base.
			xt := fl.typeOf(x.X)
			if isPointer(xt) && !isObjectPointer(xt) {
				return nil, nil, fl.errorf(x, "cannot take address of field through fat pointer")
			}
			if isPointer(xt) {
				// Object pointer: base is already the struct ref.
			}
			return &ir.Instr{
				Op:    ir.OpAddr,
				Args:  []ir.Expr{base},
				Field: x.Sel.Name,
				Type:  ptrTy,
			}, stmts, nil
		}
		return nil, nil, fl.errorf(x, "cannot take address of selection")
	case *ast.IndexExpr:
		base, stmts, err := fl.expr(x.X, nil)
		if err != nil {
			return nil, nil, err
		}
		idx, istmts, err := fl.expr(x.Index, ir.TypeI32)
		if err != nil {
			return nil, nil, err
		}
		stmts = append(stmts, istmts...)
		return &ir.Instr{
			Op:    ir.OpAddr,
			Args:  []ir.Expr{base, idx},
			Field: "elem",
			Str:   "elem",
			Type:  ptrTy,
		}, stmts, nil
	case *ast.ParenExpr:
		return fl.addrOf(x.X, want)
	case *ast.StarExpr:
		// &*p → p
		return fl.expr(x.X, want)
	case *ast.CompositeLit:
		// &T{...} — composite is already a heap object; *Struct is the same ref.
		return fl.compositeLit(x, want)
	case *ast.UnaryExpr:
		if x.Op == token.AND {
			return fl.addrOf(x.X, want)
		}
		return nil, nil, fl.errorf(x, "cannot take address of %T", x)
	default:
		return nil, nil, fl.errorf(x, "cannot take address of %T", x)
	}
}

func (fl *funcLower) starExpr(e *ast.StarExpr, want ir.Type) (ir.Expr, []ir.Stmt, error) {
	ptrTy, _ := fl.mapType(fl.typeOf(e.X))
	ptr, stmts, err := fl.expr(e.X, ptrTy)
	if err != nil {
		return nil, nil, err
	}
	ty := want
	if ty == nil {
		ty, _ = fl.mapType(fl.typeOf(e))
	}
	// Materialize pointer into a typed local so RuntimeOps expandDeref sees
	// $go.ptr.* (struct.get Type is the container, not the field type).
	if r, ok := ptrTy.(*ir.Ref); ok && strings.HasPrefix(r.Named, "$go.ptr.") {
		v := fl.newVar("pd", ptrTy)
		stmts = append(stmts, fl.assignVar(v, ptr))
		ptr = fl.useVar(v)
	}
	return &ir.Instr{Op: ir.OpDeref, Args: []ir.Expr{ptr}, Type: ty}, stmts, nil
}

// fieldGetChain lowers a FieldVal selection, following Selection.Index() for
// promoted embedded fields (e.g. f.zero → f.fmtFlags.zero).
func (fl *funcLower) fieldGetChain(base ir.Expr, baseTy types.Type, sel *types.Selection) (ir.Expr, error) {
	idx := sel.Index()
	if len(idx) == 0 {
		return nil, fmt.Errorf("empty field selection index")
	}
	cur := base
	ty := baseTy
	for i, fi := range idx {
		st := structOf(ty)
		if st == nil || fi < 0 || fi >= st.NumFields() {
			return nil, fmt.Errorf("field index %d out of range for %s", fi, ty)
		}
		fld := st.Field(fi)
		stName := ""
		if irt, err := fl.mapType(ty); err == nil {
			stName = refTypeName(irt)
		}
		if stName == "" {
			return nil, fmt.Errorf("no IR type for %s", ty)
		}
		cur = &ir.Instr{
			Op:    ir.OpStructGet,
			Type:  &ir.Ref{Named: stName},
			Field: fld.Name(),
			Args:  []ir.Expr{cur},
		}
		if i+1 < len(idx) {
			ty = fld.Type()
			// Embedded pointer fields are rare; unwrap *T → T for the next step.
			if p, ok := types.Unalias(ty).(*types.Pointer); ok {
				ty = p.Elem()
			}
		}
	}
	return cur, nil
}

// fieldSetChain assigns through a FieldVal selection, chaining gets for all
// but the last index, then struct.set on the leaf field.
func (fl *funcLower) fieldSetChain(base ir.Expr, baseTy types.Type, sel *types.Selection, rhs ir.Expr) ([]ir.Stmt, error) {
	idx := sel.Index()
	if len(idx) == 0 {
		return nil, fmt.Errorf("empty field selection index")
	}
	cur := base
	ty := baseTy
	for i, fi := range idx {
		st := structOf(ty)
		if st == nil || fi < 0 || fi >= st.NumFields() {
			return nil, fmt.Errorf("field index %d out of range for %s", fi, ty)
		}
		fld := st.Field(fi)
		stName := ""
		if irt, err := fl.mapType(ty); err == nil {
			stName = refTypeName(irt)
		}
		if stName == "" {
			return nil, fmt.Errorf("no IR type for %s", ty)
		}
		if i+1 == len(idx) {
			return []ir.Stmt{&ir.ExprStmt{X: &ir.Instr{
				Op:    ir.OpStructSet,
				Type:  &ir.Ref{Named: stName},
				Field: fld.Name(),
				Args:  []ir.Expr{cur, rhs},
			}}}, nil
		}
		cur = &ir.Instr{
			Op:    ir.OpStructGet,
			Type:  &ir.Ref{Named: stName},
			Field: fld.Name(),
			Args:  []ir.Expr{cur},
		}
		ty = fld.Type()
		if p, ok := types.Unalias(ty).(*types.Pointer); ok {
			ty = p.Elem()
		}
	}
	return nil, fmt.Errorf("field set: empty path")
}

func structOf(t types.Type) *types.Struct {
	if t == nil {
		return nil
	}
	if p, ok := types.Unalias(t).(*types.Pointer); ok {
		t = p.Elem()
	}
	st, _ := types.Unalias(t).Underlying().(*types.Struct)
	return st
}

func (fl *funcLower) selector(e *ast.SelectorExpr, want ir.Type) (ir.Expr, []ir.Stmt, error) {
	sel := fl.info.Selections[e]
	if sel == nil {
		// package.Name constant or var — handled via Uses on Sel
		if obj := fl.info.Uses[e.Sel]; obj != nil {
			if c, ok := obj.(*types.Const); ok {
				ty, err := fl.mapType(c.Type())
				if err != nil {
					return nil, nil, err
				}
				// Prefer contextual want for rune/int consts used as uint64
				// (e.g. u <= utf8.MaxRune in fmt.(*fmt).fmtUnicode).
				if want != nil && irNumWidth(want) == ir.I64 && irNumWidth(ty) == ir.I32 {
					ty = want
				}
				ex, err := fl.constVal(c.Val(), ty)
				return ex, nil, err
			}
			if _, ok := obj.(*types.Var); ok {
				if g := fl.lookupGlobal(e.Sel); g != nil {
					return &ir.Instr{Op: ir.OpGlobalGet, Global: g.Name}, nil, nil
				}
				// Cross-package var: Qual.Name → pkg.Name global.
				if id, ok := e.X.(*ast.Ident); ok {
					if pn, ok := fl.info.Uses[id].(*types.PkgName); ok && pn.Imported() != nil {
						gname := pn.Imported().Name() + "." + e.Sel.Name
						if g := fl.pkg.mod.LookupGlobal(gname); g != nil {
							return &ir.Instr{Op: ir.OpGlobalGet, Global: g.Name}, nil, nil
						}
						// Global may be declared when the package is lowered;
						// emit the qualified name anyway.
						return &ir.Instr{Op: ir.OpGlobalGet, Global: gname}, nil, nil
					}
				}
			}
		}
		return nil, nil, fl.errorf(e, "unsupported selector")
	}
	switch sel.Kind() {
	case types.FieldVal:
		base, stmts, err := fl.expr(e.X, nil)
		if err != nil {
			return nil, nil, err
		}
		xt := fl.typeOf(e.X)
		ty := want
		if ty == nil {
			ty, _ = fl.mapType(fl.typeOf(e))
		}
		// Direct struct.get when base is a struct / *struct object ref.
		// Promoted embedded fields use Selection.Index() as a chain of gets.
		if xt != nil && (isStruct(xt) || isObjectPointer(xt)) {
			ex, err := fl.fieldGetChain(base, xt, sel)
			if err != nil {
				return nil, nil, fl.errorf(e, "%v", err)
			}
			return ex, stmts, nil
		}
		// Fat-pointer path for other bases: set ptr kind from field type.
		kind, _ := fatPtrKind(sel.Type())
		if kind == "" {
			kind = "i32"
		}
		addr := &ir.Instr{
			Op:    ir.OpAddr,
			Type:  &ir.Ref{Null: true, Named: "$go.ptr." + kind},
			Args:  []ir.Expr{base},
			Field: e.Sel.Name,
		}
		return &ir.Instr{Op: ir.OpDeref, Args: []ir.Expr{addr}, Type: ty}, stmts, nil
	case types.MethodVal:
		return fl.methodValue(e, sel, want)
	case types.MethodExpr:
		fn := sel.Obj().(*types.Func)
		return fl.methodExpr(fn)
	default:
		return nil, nil, fl.errorf(e, "unsupported selection kind %v", sel.Kind())
	}
}

func (fl *funcLower) indexExpr(e *ast.IndexExpr, want ir.Type) (ir.Expr, []ir.Stmt, error) {
	xt := fl.typeOf(e.X)
	if isMap(xt) {
		return fl.mapIndexExpr(e, want)
	}
	base, stmts, err := fl.expr(e.X, nil)
	if err != nil {
		return nil, nil, err
	}
	idx, istmts, err := fl.expr(e.Index, ir.TypeI32)
	if err != nil {
		return nil, nil, err
	}
	stmts = append(stmts, istmts...)
	// Wasm array/slice indices are i32; Go may compute them as int64/uint64.
	if it := fl.typeOf(e.Index); it != nil {
		if irt, err := fl.mapType(it); err == nil && irNumWidth(irt) == ir.I64 {
			idx = &ir.Instr{Op: ir.OpI32WrapI64, Args: []ir.Expr{idx}}
		}
	}
	ty := want
	if ty == nil {
		ty, _ = fl.mapType(fl.typeOf(e))
	}
	if isString(xt) {
		return &ir.Instr{Op: ir.OpStringGet, Args: []ir.Expr{base, idx}}, stmts, nil
	}
	if isSlice(xt) {
		return &ir.Instr{Op: ir.OpSliceGet, Args: []ir.Expr{base, idx}, Type: ty}, stmts, nil
	}
	if isArray(xt) || isPtrToArray(xt) {
		// Value read: array.get (structs/refs included). Address-of uses OpAddr elsewhere.
		arrName := ""
		elemT := xt
		if isPtrToArray(xt) {
			if p, ok := types.Unalias(xt).Underlying().(*types.Pointer); ok {
				elemT = p.Elem()
			}
		}
		if irt, err := fl.mapType(elemT); err == nil {
			if r, ok := irt.(*ir.Ref); ok {
				arrName = r.Named
			}
		}
		at := ty
		if arrName != "" {
			at = &ir.Ref{Named: arrName}
		}
		op := ir.OpArrayGet
		switch arrName {
		case "$go.bytes":
			op = ir.OpArrayGetU // uint8/byte → i32
		case "$go.i16s":
			// Signedness from element Go type.
			if et := arrayElemType(elemT); et != nil && isUnsigned(et) {
				op = ir.OpArrayGetU
			} else {
				op = ir.OpArrayGetS
			}
		}
		return &ir.Instr{Op: op, Args: []ir.Expr{base, idx}, Type: at}, stmts, nil
	}
	return nil, nil, fl.errorf(e, "index of type %s not supported", xt)
}

func (fl *funcLower) sliceBoundI32(e ast.Expr) (ir.Expr, []ir.Stmt, error) {
	ex, stmts, err := fl.expr(e, ir.TypeI32)
	if err != nil {
		return nil, nil, err
	}
	// Wasm slice bounds are i32; Go may use int64 (e.g. io.LimitedReader.N).
	if t := fl.typeOf(e); t != nil {
		if irt, err := fl.mapType(t); err == nil && irNumWidth(irt) == ir.I64 {
			ex = &ir.Instr{Op: ir.OpI32WrapI64, Args: []ir.Expr{ex}}
		}
	}
	return ex, stmts, nil
}

func (fl *funcLower) sliceExpr(e *ast.SliceExpr, want ir.Type) (ir.Expr, []ir.Stmt, error) {
	base, stmts, err := fl.expr(e.X, nil)
	if err != nil {
		return nil, nil, err
	}
	var lo, hi, max ir.Expr
	if e.Low != nil {
		var lstmts []ir.Stmt
		lo, lstmts, err = fl.sliceBoundI32(e.Low)
		if err != nil {
			return nil, nil, err
		}
		stmts = append(stmts, lstmts...)
	} else {
		lo = fl.i32const(0)
	}
	if e.High != nil {
		var hstmts []ir.Stmt
		hi, hstmts, err = fl.sliceBoundI32(e.High)
		if err != nil {
			return nil, nil, err
		}
		stmts = append(stmts, hstmts...)
	} else {
		// hi = len(base)
		hi = &ir.Instr{Op: ir.OpLen, Args: []ir.Expr{base}}
	}
	args := []ir.Expr{base, lo, hi}
	if e.Max != nil {
		var mstmts []ir.Stmt
		max, mstmts, err = fl.sliceBoundI32(e.Max)
		if err != nil {
			return nil, nil, err
		}
		stmts = append(stmts, mstmts...)
		args = append(args, max)
	}
	ty := want
	if ty == nil {
		ty, _ = fl.mapType(fl.typeOf(e))
	}
	if isString(fl.typeOf(e.X)) {
		return &ir.Instr{Op: ir.OpStringSlice, Args: args, Type: ty}, stmts, nil
	}
	return &ir.Instr{Op: ir.OpSliceSlice, Args: args, Type: ty}, stmts, nil
}

func (fl *funcLower) compositeLit(e *ast.CompositeLit, want ir.Type) (ir.Expr, []ir.Stmt, error) {
	ty := fl.typeOf(e)
	irt, err := fl.mapType(ty)
	if err != nil {
		return nil, nil, fl.errorf(e, "%v", err)
	}
	if want != nil {
		irt = want
	}
	ut := types.Unalias(ty)
	if n, ok := ut.(*types.Named); ok {
		ut = n.Underlying()
	}
	switch ut := ut.(type) {
	case *types.Slice:
		var args []ir.Expr
		var stmts []ir.Stmt
		elemWant, _ := fl.mapType(ut.Elem())
		for _, el := range e.Elts {
			if kv, ok := el.(*ast.KeyValueExpr); ok {
				el = kv.Value
			}
			ex, st, err := fl.expr(el, elemWant)
			if err != nil {
				return nil, nil, err
			}
			stmts = append(stmts, st...)
			args = append(args, ex)
		}
		return &ir.Instr{Op: ir.OpLiteral, Type: irt, Args: args}, stmts, nil
	case *types.Struct:
		return fl.structLit(e, ut, irt)
	case *types.Array:
		var args []ir.Expr
		var stmts []ir.Stmt
		elemWant, _ := fl.mapType(ut.Elem())
		for _, el := range e.Elts {
			if kv, ok := el.(*ast.KeyValueExpr); ok {
				el = kv.Value
			}
			ex, st, err := fl.expr(el, elemWant)
			if err != nil {
				return nil, nil, err
			}
			stmts = append(stmts, st...)
			args = append(args, ex)
		}
		return &ir.Instr{Op: ir.OpLiteral, Type: irt, Args: args}, stmts, nil
	case *types.Map:
		return fl.mapLiteral(e, irt)
	default:
		return nil, nil, fl.errorf(e, "composite literal of type %s not supported", ty)
	}
}

// structLitField lowers a composite-literal field value, boxing into an
// interface when the field type is an interface.
func (fl *funcLower) structLitField(val ast.Expr, fieldTy types.Type) (ir.Expr, []ir.Stmt, error) {
	if isIface(fieldTy) && !isIface(fl.typeOf(val)) && !isNilIdent(val) {
		return fl.convertToIface(val, fl.typeOf(val), fieldTy, typeIface)
	}
	ft, _ := fl.mapType(fieldTy)
	return fl.expr(val, ft)
}

func (fl *funcLower) structLit(e *ast.CompositeLit, st *types.Struct, irt ir.Type) (ir.Expr, []ir.Stmt, error) {
	n := st.NumFields()
	vals := make([]ir.Expr, n)
	var stmts []ir.Stmt
	// Zero defaults.
	for i := 0; i < n; i++ {
		ft, err := fl.mapType(st.Field(i).Type())
		if err != nil {
			return nil, nil, err
		}
		z, err := fl.zeroValue(ft)
		if err != nil {
			return nil, nil, err
		}
		vals[i] = z
	}
	keyed := false
	for _, el := range e.Elts {
		if _, ok := el.(*ast.KeyValueExpr); ok {
			keyed = true
			break
		}
	}
	if keyed {
		for _, el := range e.Elts {
			kv := el.(*ast.KeyValueExpr)
			name := kv.Key.(*ast.Ident).Name
			idx := -1
			for i := 0; i < n; i++ {
				if st.Field(i).Name() == name {
					idx = i
					break
				}
			}
			if idx < 0 {
				return nil, nil, fl.errorf(kv.Key, "unknown field %s", name)
			}
			ex, stmts2, err := fl.structLitField(kv.Value, st.Field(idx).Type())
			if err != nil {
				return nil, nil, err
			}
			stmts = append(stmts, stmts2...)
			vals[idx] = ex
		}
	} else {
		for i, el := range e.Elts {
			ex, stmts2, err := fl.structLitField(el, st.Field(i).Type())
			if err != nil {
				return nil, nil, err
			}
			stmts = append(stmts, stmts2...)
			vals[i] = ex
		}
	}
	// Skip zero-size fields in IR args — mapType/structFields already skipped them.
	var args []ir.Expr
	for i := 0; i < n; i++ {
		if isZeroSize(st.Field(i).Type()) {
			continue
		}
		args = append(args, vals[i])
	}
	return &ir.Instr{Op: ir.OpLiteral, Type: irt, Args: args}, stmts, nil
}

func (fl *funcLower) zeroValue(t ir.Type) (ir.Expr, error) {
	switch t := t.(type) {
	case *ir.NumType:
		switch t.Kind {
		case ir.I64:
			return fl.i64const(0), nil
		case ir.F32:
			return fl.f32const(0), nil
		case ir.F64:
			return fl.f64const(0), nil
		default:
			return fl.i32const(0), nil
		}
	case *ir.Ref:
		if t.Named == "$go.string" {
			return &ir.Instr{Op: ir.OpStringLit, Str: ""}, nil
		}
		if t.Named == "$go.complex64" || t.Named == "$go.complex128" {
			fl.pkg.mod.InternType(&ir.TypeDef{
				Name: t.Named, Kind: ir.TypeStruct, Super: "$go.object",
				Fields: []ir.Field{
					{Name: "real", Type: ir.TypeF64, Mut: true},
					{Name: "imag", Type: ir.TypeF64, Mut: true},
				},
			})
			return &ir.Instr{Op: ir.OpStructNewDefault, Type: t}, nil
		}
		// User named structs ($go.struct.*) are values: zero is an empty heap
		// object. Nested struct fields are filled by structZero (non-null).
		// Runtime / iface / chan / slice / clos zeros stay null refs.
		if strings.HasPrefix(t.Named, "$go.struct.") {
			if td := fl.mod.LookupType(t.Named); td != nil && td.Kind == ir.TypeStruct {
				return fl.structZero(td, t, map[string]bool{})
			}
		}
		return &ir.Instr{Op: ir.OpRefNull, Type: t}, nil
	default:
		return nil, fmt.Errorf("no zero value for %s", t)
	}
}

func (fl *funcLower) structZero(td *ir.TypeDef, t ir.Type, seen map[string]bool) (ir.Expr, error) {
	if seen[td.Name] {
		return &ir.Instr{Op: ir.OpStructNewDefault, Type: t}, nil
	}
	seen[td.Name] = true
	args := make([]ir.Expr, len(td.Fields))
	for i, f := range td.Fields {
		if r, ok := f.Type.(*ir.Ref); ok && strings.HasPrefix(r.Named, "$go.struct.") {
			ntd := fl.mod.LookupType(r.Named)
			if ntd != nil && ntd.Kind == ir.TypeStruct {
				z, err := fl.structZero(ntd, r, seen)
				if err != nil {
					return nil, err
				}
				args[i] = z
				continue
			}
		}
		z, err := fl.zeroValue(f.Type)
		if err != nil {
			return nil, err
		}
		args[i] = z
	}
	delete(seen, td.Name)
	return &ir.Instr{Op: ir.OpStructNew, Type: t, Args: args}, nil
}
