package lower

import (
	"fmt"
	"go/ast"
	"go/types"

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
	switch x := x.(type) {
	case *ast.Ident:
		if g := fl.lookupGlobal(x); g != nil {
			base := &ir.Instr{Op: ir.OpGlobalGet, Global: g.Name}
			return &ir.Instr{Op: ir.OpAddr, Args: []ir.Expr{base}, Type: ptrTy}, nil, nil
		}
		v, err := fl.lookupVar(x)
		if err != nil {
			return nil, nil, err
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
	default:
		return nil, nil, fl.errorf(x, "cannot take address of %T", x)
	}
}

func (fl *funcLower) starExpr(e *ast.StarExpr, want ir.Type) (ir.Expr, []ir.Stmt, error) {
	ptr, stmts, err := fl.expr(e.X, nil)
	if err != nil {
		return nil, nil, err
	}
	ty := want
	if ty == nil {
		ty, _ = fl.mapType(fl.typeOf(e))
	}
	return &ir.Instr{Op: ir.OpDeref, Args: []ir.Expr{ptr}, Type: ty}, stmts, nil
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
				ex, err := fl.constVal(c.Val(), ty)
				return ex, nil, err
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
		// Field of pointer: use object pointer as base (same ref).
		_ = xt
		ty := want
		if ty == nil {
			ty, _ = fl.mapType(fl.typeOf(e))
		}
		// Emit addr+deref so escape can rewrite non-escaping field loads.
		addr := &ir.Instr{Op: ir.OpAddr, Args: []ir.Expr{base}, Field: e.Sel.Name}
		return &ir.Instr{Op: ir.OpDeref, Args: []ir.Expr{addr}, Type: ty}, stmts, nil
	case types.MethodVal:
		return nil, nil, fl.errorf(e, "method value not supported in M3; call the method")
	default:
		return nil, nil, fl.errorf(e, "unsupported selection kind %v", sel.Kind())
	}
}

func (fl *funcLower) indexExpr(e *ast.IndexExpr, want ir.Type) (ir.Expr, []ir.Stmt, error) {
	xt := fl.typeOf(e.X)
	base, stmts, err := fl.expr(e.X, nil)
	if err != nil {
		return nil, nil, err
	}
	idx, istmts, err := fl.expr(e.Index, ir.TypeI32)
	if err != nil {
		return nil, nil, err
	}
	stmts = append(stmts, istmts...)
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
	if isArray(xt) {
		// Array index via addr+deref of elem, or direct — use slice.get-like high op
		// Represent as addr elem + deref.
		addr := &ir.Instr{Op: ir.OpAddr, Args: []ir.Expr{base, idx}, Field: "elem", Str: "elem"}
		return &ir.Instr{Op: ir.OpDeref, Args: []ir.Expr{addr}, Type: ty}, stmts, nil
	}
	return nil, nil, fl.errorf(e, "index of type %s not supported", xt)
}

func (fl *funcLower) sliceExpr(e *ast.SliceExpr, want ir.Type) (ir.Expr, []ir.Stmt, error) {
	base, stmts, err := fl.expr(e.X, nil)
	if err != nil {
		return nil, nil, err
	}
	var lo, hi, max ir.Expr
	if e.Low != nil {
		lo, _, err = fl.expr(e.Low, ir.TypeI32)
		if err != nil {
			return nil, nil, err
		}
	} else {
		lo = fl.i32const(0)
	}
	if e.High != nil {
		var hstmts []ir.Stmt
		hi, hstmts, err = fl.expr(e.High, ir.TypeI32)
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
		max, mstmts, err = fl.expr(e.Max, ir.TypeI32)
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
	default:
		return nil, nil, fl.errorf(e, "composite literal of type %s not supported", ty)
	}
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
			ft, _ := fl.mapType(st.Field(idx).Type())
			ex, stmts2, err := fl.expr(kv.Value, ft)
			if err != nil {
				return nil, nil, err
			}
			stmts = append(stmts, stmts2...)
			vals[idx] = ex
		}
	} else {
		for i, el := range e.Elts {
			ft, _ := fl.mapType(st.Field(i).Type())
			ex, stmts2, err := fl.expr(el, ft)
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
		return &ir.Instr{Op: ir.OpRefNull, Type: t}, nil
	default:
		return nil, fmt.Errorf("no zero value for %s", t)
	}
}
