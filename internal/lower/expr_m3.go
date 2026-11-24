package lower

import (
	"fmt"
	"go/ast"
	"go/constant"
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
			obj := fl.info.Uses[x]
			if obj == nil {
				obj = fl.info.Defs[x]
			}
			// Address-taken package scalars live in $go.cell.*; fat-pointer
			// base must be the cell ref (anyref), not the scalar global.get.
			if obj != nil {
				if _, ok := fl.pkg.globalCellElem[obj]; ok {
					base := &ir.Instr{Op: ir.OpGlobalGet, Global: g.Name}
					return &ir.Instr{
						Op:    ir.OpAddr,
						Args:  []ir.Expr{base},
						Field: "v",
						Type:  ptrTy,
					}, nil, nil
				}
			}
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
		// &s.f or &p.f (including promoted embedded fields).
		if sel := fl.info.Selections[x]; sel != nil && sel.Kind() == types.FieldVal {
			base, stmts, err := fl.expr(x.X, nil)
			if err != nil {
				return nil, nil, err
			}
			xt := fl.typeOf(x.X)
			if isPointer(xt) && !isObjectPointer(xt) {
				return nil, nil, fl.errorf(x, "cannot take address of field through fat pointer")
			}
			idx := sel.Index()
			if len(idx) == 0 {
				return nil, nil, fl.errorf(x, "empty field selection for addr")
			}
			cur := base
			ty := xt
			// Walk embedding path except the leaf field.
			for i := 0; i < len(idx)-1; i++ {
				st := structOf(ty)
				if st == nil || idx[i] < 0 || idx[i] >= st.NumFields() {
					return nil, nil, fl.errorf(x, "addr embedding index out of range")
				}
				fld := st.Field(idx[i])
				stName := ""
				if irt, err := fl.mapType(ty); err == nil {
					stName = refTypeName(irt)
				}
				if stName == "" {
					return nil, nil, fl.errorf(x, "no IR type for %s", ty)
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
			st := structOf(ty)
			leaf := x.Sel.Name
			if st != nil {
				fi := idx[len(idx)-1]
				if fi >= 0 && fi < st.NumFields() {
					leaf = st.Field(fi).Name()
				}
			}
			// Str names the struct that owns Field (after embedding walk). Escape
			// rewrite uses it for struct.get/set — Args[0]'s Instr.Type is the
			// outer struct when Args[0] is itself a struct.get of an embed.
			owner := ""
			if irt, err := fl.mapType(ty); err == nil {
				owner = refTypeName(irt)
			}
			return &ir.Instr{
				Op:    ir.OpAddr,
				Args:  []ir.Expr{cur},
				Field: leaf,
				Str:   owner,
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
	xt := fl.typeOf(e.X)
	ptrTy, _ := fl.mapType(xt)
	ptr, stmts, err := fl.expr(e.X, ptrTy)
	if err != nil {
		return nil, nil, err
	}
	ty := want
	if ty == nil {
		ty, _ = fl.mapType(fl.typeOf(e))
	}
	// *Struct / *[N]T: pointer and value share the same object ref.
	if xt != nil && isObjectPointer(xt) {
		return ptr, stmts, nil
	}
	// Materialize pointer into a typed local so RuntimeOps expandDeref sees
	// $go.ptr.* (struct.get Type is the container, not the field type).
	// Skip when the pointer is already a typed Name (Ident load) — avoids a
	// dead `pd = p` after escape rewrites *p to struct.get.
	if r, ok := ptrTy.(*ir.Ref); ok && strings.HasPrefix(r.Named, "$go.ptr.") {
		if _, isName := ptr.(*ir.Name); !isName {
			v := fl.newVar("pd", ptrTy)
			stmts = append(stmts, fl.assignVar(v, ptr))
			ptr = fl.useVar(v)
		}
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
		// package.Name constant, var, or top-level func value — via Uses on Sel
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
			if v, ok := obj.(*types.Var); ok {
				if g := fl.lookupGlobal(e.Sel); g != nil {
					return fl.readGlobal(v, g), nil, nil
				}
				// Cross-package var: Qual.Name → pkg.Name global.
				if id, ok := e.X.(*ast.Ident); ok {
					if pn, ok := fl.info.Uses[id].(*types.PkgName); ok && pn.Imported() != nil {
						gname := pn.Imported().Name() + "." + e.Sel.Name
						if g := fl.pkg.mod.LookupGlobal(gname); g != nil {
							return fl.readGlobal(v, g), nil, nil
						}
						// Global may be declared when the package is lowered;
						// emit the qualified name anyway.
						return &ir.Instr{Op: ir.OpGlobalGet, Global: gname}, nil, nil
					}
				}
			}
			// package.Func as a func value (e.g. unicode.IsSpace passed to FieldsFunc).
			if fn, ok := obj.(*types.Func); ok {
				return fl.topLevelFuncValue(fn)
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
			// Widen i32 fields when the context wants i64 (e.g. uint mantbits
			// as the count of an i64 shift in strconv.(*decimal).floatBits).
			if want != nil && irNumWidth(want) == ir.I64 {
				ft := fl.typeOf(e)
				if ft != nil {
					if ftIR, err := fl.mapType(ft); err == nil && irNumWidth(ftIR) == ir.I32 {
						if isUnsigned(ft) {
							ex = &ir.Instr{Op: ir.OpI64ExtendI32U, Args: []ir.Expr{ex}}
						} else {
							ex = &ir.Instr{Op: ir.OpI64ExtendI32S, Args: []ir.Expr{ex}}
						}
					}
				}
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
		return fl.methodExpr(sel)
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
		// Instr.Type is the ARRAY type (codegen typeIndexOf); lenCapArgType
		// resolves ArrayGet to the element type for len/cap.
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
		// Recoverable panic instead of Wasm array OOB trap (issue4353, etc.).
		arrTy := at
		if arrName != "" {
			arrTy = &ir.Ref{Named: arrName}
		}
		lenV := fl.newVar("alen", ir.TypeI32)
		stmts = append(stmts,
			fl.assignVar(lenV, &ir.Instr{Op: ir.OpArrayLen, Type: arrTy, Args: []ir.Expr{base}}),
			&ir.If{
				Cond: &ir.Instr{Op: ir.OpI32GeU, Args: []ir.Expr{idx, fl.useVar(lenV)}},
				Then: &ir.Seq{Stmts: []ir.Stmt{
					&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: "runtime.panicIndex", Args: []ir.Expr{idx, fl.useVar(lenV)}}},
					&ir.Unreachable{},
				}},
			},
		)
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
	// Prefer the slice expression's mapped type over want: blank `_ = s[i:]`
	// reports want=i32 via lhsType("_") and would corrupt OpSliceSlice typing
	// (slices.Insert/Delete/Replace bounds checks → $rt.data as refnull any).
	ty, _ := fl.mapType(fl.typeOf(e))
	if want != nil && isRefIR(want) {
		ty = want
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
	// Prefer want only when it matches the literal's Go type mapping. Blank
	// `_ = T{…}` passes want=i32 from lhsType("_"), which must not override.
	if want != nil && ir.TypesEqual(want, irt) {
		irt = want
	}
	ut := types.Unalias(ty)
	if n, ok := ut.(*types.Named); ok {
		if isJSValue(n) {
			// Value{} is JS null (externref null), not an empty struct.
			z, err := fl.zeroValue(ir.TypeExtern)
			return z, nil, err
		}
		ut = n.Underlying()
	}
	switch ut := ut.(type) {
	case *types.Slice:
		var args []ir.Expr
		var stmts []ir.Stmt
		elemGo := ut.Elem()
		elemWant, err := fl.mapType(elemGo)
		if err != nil {
			return nil, nil, fl.errorf(e, "%v", err)
		}
		for _, el := range e.Elts {
			if kv, ok := el.(*ast.KeyValueExpr); ok {
				el = kv.Value
			}
			ex, st, err := fl.coerceExpr(el, elemGo, elemWant)
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
		return fl.arrayLit(e, ut, irt)
	case *types.Map:
		return fl.mapLiteral(e, irt)
	case *types.Pointer:
		// Elided pointer composite lit: []*T{{…}} / [N]*T{{x}} — go/types
		// types the element as *T. Object pointers (*NamedStruct / *[N]T) share
		// the pointee IR ref, so lower as the element type with the *T IR type.
		elem := types.Unalias(ut.Elem())
		elemUT := elem
		if n, ok := elem.(*types.Named); ok {
			elemUT = n.Underlying()
		}
		switch elemUT := elemUT.(type) {
		case *types.Struct:
			return fl.structLit(e, elemUT, irt)
		case *types.Array:
			return fl.arrayLit(e, elemUT, irt)
		default:
			return nil, nil, fl.errorf(e, "composite literal of type %s not supported", ty)
		}
	default:
		return nil, nil, fl.errorf(e, "composite literal of type %s not supported", ty)
	}
}

// structLitField lowers a composite-literal field value, boxing into an
// interface when the field type is an interface.
// arrayLit lowers [N]T{...}, including keyed indices (e.g. [256]Kind{'{': '{'}).
// Missing slots are zero so const packing / array.new_fixed have length N.
func (fl *funcLower) arrayLit(e *ast.CompositeLit, at *types.Array, irt ir.Type) (ir.Expr, []ir.Stmt, error) {
	n := int(at.Len())
	if n < 0 {
		n = 0
	}
	elemWant, err := fl.mapType(at.Elem())
	if err != nil {
		return nil, nil, fl.errorf(e, "%v", err)
	}
	args := make([]ir.Expr, n)
	for i := 0; i < n; i++ {
		z, err := fl.zeroValue(elemWant)
		if err != nil {
			return nil, nil, err
		}
		args[i] = z
	}
	var stmts []ir.Stmt
	next := 0
	for _, el := range e.Elts {
		idx := next
		val := el
		if kv, ok := el.(*ast.KeyValueExpr); ok {
			val = kv.Value
			c, ok := fl.constInt(kv.Key)
			if !ok {
				return nil, nil, fl.errorf(kv.Key, "array literal key must be a constant integer")
			}
			if c < 0 || int(c) >= n {
				return nil, nil, fl.errorf(kv.Key, "array literal index %d out of bounds [0:%d]", c, n)
			}
			idx = int(c)
		} else if next >= n {
			return nil, nil, fl.errorf(el, "array literal has too many elements")
		}
		// coerceExpr boxes into interface elements (e.g. [4]any{1, "x", 2.5, nil}).
		ex, st, err := fl.coerceExpr(val, at.Elem(), elemWant)
		if err != nil {
			return nil, nil, err
		}
		stmts = append(stmts, st...)
		args[idx] = ex
		next = idx + 1
	}
	return &ir.Instr{Op: ir.OpLiteral, Type: irt, Args: args}, stmts, nil
}

func (fl *funcLower) constInt(e ast.Expr) (int64, bool) {
	if e == nil {
		return 0, false
	}
	tv, ok := fl.info.Types[e]
	if !ok || tv.Value == nil {
		return 0, false
	}
	if v, exact := constant.Int64Val(tv.Value); exact {
		return v, true
	}
	if v, exact := constant.Uint64Val(tv.Value); exact && v <= uint64(^uint64(0)>>1) {
		return int64(v), true
	}
	return 0, false
}

func (fl *funcLower) structLitField(val ast.Expr, fieldTy types.Type) (ir.Expr, []ir.Stmt, error) {
	ft, err := fl.mapType(fieldTy)
	if err != nil {
		return nil, nil, err
	}
	// coerceExpr types untyped nil into iface/pointer/slice/map fields
	// (errors.Is/As tables: {nil, nil, true}).
	return fl.coerceExpr(val, fieldTy, ft)
}

func (fl *funcLower) structLit(e *ast.CompositeLit, st *types.Struct, irt ir.Type) (ir.Expr, []ir.Stmt, error) {
	n := st.NumFields()
	vals := make([]ir.Expr, n)
	var stmts []ir.Stmt
	// Zero defaults.
	for i := 0; i < n; i++ {
		ftGo := st.Field(i).Type()
		ft, err := fl.mapType(ftGo)
		if err != nil {
			return nil, nil, err
		}
		// *NamedStruct fields share IR with T; zero is null, not empty T.
		if p, ok := types.Unalias(ftGo).(*types.Pointer); ok {
			if _, ok := types.Unalias(p.Elem()).(*types.Named); ok {
				if _, ok := types.Unalias(p.Elem()).Underlying().(*types.Struct); ok {
					vals[i] = &ir.Instr{Op: ir.OpRefNull, Type: ft}
					continue
				}
			}
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
			path, fieldTy, ok := fl.structLitFieldPath(st, name)
			if !ok {
				return nil, nil, fl.errorf(kv.Key, "unknown field %s", name)
			}
			ex, stmts2, err := fl.structLitField(kv.Value, fieldTy)
			if err != nil {
				return nil, nil, err
			}
			stmts = append(stmts, stmts2...)
			if err := fl.structLitSetPath(vals, st, path, ex); err != nil {
				return nil, nil, fl.errorf(kv.Key, "%v", err)
			}
		}
	} else {
		// Positional elements map to fields in order; blank (`_`) fields consume
		// an element (for side effects) but stay zero (issue 31546).
		ei := 0
		for i := 0; i < n && ei < len(e.Elts); i++ {
			el := e.Elts[ei]
			ei++
			ex, stmts2, err := fl.structLitField(el, st.Field(i).Type())
			if err != nil {
				return nil, nil, err
			}
			stmts = append(stmts, stmts2...)
			if st.Field(i).Name() == "_" {
				continue // leave vals[i] as the zero from above
			}
			vals[i] = ex
		}
		if ei != len(e.Elts) {
			return nil, nil, fl.errorf(e, "too many values in struct literal")
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

// structLitFieldPath resolves a keyed composite-literal field name, including
// promoted fields of embedded structs. path is indexes into successive structs.
func (fl *funcLower) structLitFieldPath(st *types.Struct, name string) (path []int, fieldTy types.Type, ok bool) {
	for i := 0; i < st.NumFields(); i++ {
		if st.Field(i).Name() == name {
			return []int{i}, st.Field(i).Type(), true
		}
	}
	var pkg *types.Package
	if fl.pkg != nil {
		pkg = fl.pkg.pkg
	}
	obj, index, _ := types.LookupFieldOrMethod(st, false, pkg, name)
	if obj == nil || len(index) == 0 {
		return nil, nil, false
	}
	v, ok := obj.(*types.Var)
	if !ok {
		return nil, nil, false
	}
	return index, v.Type(), true
}

// structLitSetPath writes ex into vals along an embedding path, rebuilding
// nested OpLiteral structs so A{b:1,c:2} merges into the same embedded B.
func (fl *funcLower) structLitSetPath(vals []ir.Expr, st *types.Struct, path []int, ex ir.Expr) error {
	if len(path) == 0 {
		return fmt.Errorf("empty field path")
	}
	if len(path) == 1 {
		vals[path[0]] = ex
		return nil
	}
	i := path[0]
	ftGo := st.Field(i).Type()
	nestedSt, ok := types.Unalias(ftGo).Underlying().(*types.Struct)
	if !ok {
		return fmt.Errorf("embedded field is not a struct")
	}
	ftIR, err := fl.mapType(ftGo)
	if err != nil {
		return err
	}
	nestedVals, err := fl.structLitArgs(vals[i], nestedSt, ftIR)
	if err != nil {
		return err
	}
	if err := fl.structLitSetPath(nestedVals, nestedSt, path[1:], ex); err != nil {
		return err
	}
	var args []ir.Expr
	for j := 0; j < nestedSt.NumFields(); j++ {
		if isZeroSize(nestedSt.Field(j).Type()) {
			continue
		}
		args = append(args, nestedVals[j])
	}
	vals[i] = &ir.Instr{Op: ir.OpLiteral, Type: ftIR, Args: args}
	return nil
}

// structLitArgs expands a struct value expression into per-field values for
// further keyed updates. Non-literal values are field-selected into temps.
func (fl *funcLower) structLitArgs(ex ir.Expr, st *types.Struct, irt ir.Type) ([]ir.Expr, error) {
	n := st.NumFields()
	if lit, ok := ex.(*ir.Instr); ok && lit.Op == ir.OpLiteral {
		out := make([]ir.Expr, n)
		ai := 0
		for i := 0; i < n; i++ {
			if isZeroSize(st.Field(i).Type()) {
				z, err := fl.zeroValueForStructField(st.Field(i).Type())
				if err != nil {
					return nil, err
				}
				out[i] = z
				continue
			}
			if ai >= len(lit.Args) {
				return nil, fmt.Errorf("struct literal arity mismatch")
			}
			out[i] = lit.Args[ai]
			ai++
		}
		return out, nil
	}
	// Fallback: zero then StructSet would need a temp; keyed lits always start
	// from OpLiteral zeros, so this path should be rare.
	out := make([]ir.Expr, n)
	for i := 0; i < n; i++ {
		z, err := fl.zeroValueForStructField(st.Field(i).Type())
		if err != nil {
			return nil, err
		}
		out[i] = z
	}
	return out, nil
}

func (fl *funcLower) zeroValueForStructField(ftGo types.Type) (ir.Expr, error) {
	ft, err := fl.mapType(ftGo)
	if err != nil {
		return nil, err
	}
	if p, ok := types.Unalias(ftGo).(*types.Pointer); ok {
		if _, ok := types.Unalias(p.Elem()).(*types.Named); ok {
			if _, ok := types.Unalias(p.Elem()).Underlying().(*types.Struct); ok {
				return &ir.Instr{Op: ir.OpRefNull, Type: ft}, nil
			}
		}
	}
	return fl.zeroValue(ft)
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
			elem := ir.TypeF64
			if t.Named == "$go.complex64" {
				elem = ir.TypeF32
			}
			fl.ensureComplexType(t.Named, elem)
			return &ir.Instr{Op: ir.OpStructNewDefault, Type: t}, nil
		}
		// Named structs ($go.struct.*): T and *T share the same IR ref
		// (Null:true). Value zero is an empty heap object; pointer zero is
		// null and must be decided from the Go type at the call site
		// (structLit). Here we always build a value zero.
		if strings.HasPrefix(t.Named, "$go.struct.") {
			if td := fl.mod.LookupType(t.Named); td != nil && td.Kind == ir.TypeStruct {
				return fl.structZero(td, t, map[string]bool{})
			}
		}
		return &ir.Instr{Op: ir.OpRefNull, Type: t}, nil
	case *ir.Extern:
		return &ir.Instr{Op: ir.OpRefNull, Type: ir.TypeExtern}, nil
	default:
		return nil, fmt.Errorf("no zero value for %s", t)
	}
}

// elemZeroIsNull reports whether the Go zero for an array element is a null
// reference (pointer/slice/map/chan/func/iface). Structs, strings, numbers,
// and nested arrays need a concrete zero value instead.
func elemZeroIsNull(goTy types.Type) bool {
	if goTy == nil {
		return true
	}
	switch types.Unalias(goTy).Underlying().(type) {
	case *types.Pointer, *types.Slice, *types.Map, *types.Chan, *types.Signature, *types.Interface:
		return true
	}
	return false
}

// goZeroValue builds the Go zero for a local/global when Wasm's null default
// is wrong. ok is false when null/0 is already correct (pointers, slices, …).
func (fl *funcLower) goZeroValue(goTy types.Type, irTy ir.Type) (ir.Expr, bool) {
	if goTy == nil || irTy == nil {
		return nil, false
	}
	goTy = types.Unalias(goTy)
	switch u := goTy.Underlying().(type) {
	case *types.Array:
		r, ok := irTy.(*ir.Ref)
		if !ok || r.Named == "" {
			return nil, false
		}
		n := u.Len()
		if n < 0 {
			n = 0
		}
		// array.new_default fills ref elements with null. Go zeros for
		// structs/strings/nested arrays are non-null values.
		//
		// array.new(z, n) aliases the same ref for every slot — fine for
		// immutable strings, but mutable structs must each get a fresh
		// zero via a fill helper.
		if !elemZeroIsNull(u.Elem()) {
			elemIR, err := fl.mapType(u.Elem())
			if err == nil {
				if isStruct(u.Elem()) {
					fn := fl.ensureZeroArrayFunc(r.Named, elemIR)
					return &ir.Instr{
						Op:   ir.OpCall,
						Func: fn,
						Args: []ir.Expr{&ir.Const{Op: ir.OpI32Const, I64: n}},
						Type: &ir.Ref{Named: r.Named},
					}, true
				}
				z, zerr := fl.zeroValue(elemIR)
				if zerr == nil {
					return &ir.Instr{
						Op:   ir.OpArrayNew,
						Type: &ir.Ref{Named: r.Named},
						Args: []ir.Expr{z, &ir.Const{Op: ir.OpI32Const, I64: n}},
					}, true
				}
			}
		}
		return &ir.Instr{
			Op:   ir.OpArrayNewDefault,
			Type: &ir.Ref{Named: r.Named},
			Args: []ir.Expr{&ir.Const{Op: ir.OpI32Const, I64: n}},
		}, true
	case *types.Struct:
		z, err := fl.zeroValue(irTy)
		if err != nil {
			return nil, false
		}
		// Only emit when zeroValue allocates (named struct), not bare null.
		if in, ok := z.(*ir.Instr); ok && in.Op == ir.OpRefNull {
			return nil, false
		}
		return z, true
	case *types.Basic:
		if u.Kind() == types.String {
			return &ir.Instr{Op: ir.OpStringLit, Str: ""}, true
		}
		if u.Kind() == types.Complex64 || u.Kind() == types.Complex128 {
			z, err := fl.zeroValue(irTy)
			return z, err == nil
		}
	}
	// Named struct defined type.
	if _, ok := goTy.(*types.Named); ok {
		if _, ok := goTy.Underlying().(*types.Struct); ok {
			z, err := fl.zeroValue(irTy)
			if err != nil {
				return nil, false
			}
			if in, ok := z.(*ir.Instr); ok && in.Op == ir.OpRefNull {
				return nil, false
			}
			return z, true
		}
	}
	return nil, false
}

func (fl *funcLower) structZero(td *ir.TypeDef, t ir.Type, seen map[string]bool) (ir.Expr, error) {
	if seen[td.Name] {
		// Avoid struct.new_default (non-defaultable string fields).
		return &ir.Instr{Op: ir.OpRefNull, Type: t}, nil
	}
	seen[td.Name] = true
	args := make([]ir.Expr, len(td.Fields))
	for i, f := range td.Fields {
		// Nested $go.struct refs: pointer fields (Ptr) and same-type fields
		// are *T (null); other named structs are nested values.
		if r, ok := f.Type.(*ir.Ref); ok && strings.HasPrefix(r.Named, "$go.struct.") {
			if f.Ptr || r.Named == td.Name {
				args[i] = &ir.Instr{Op: ir.OpRefNull, Type: r}
				continue
			}
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
		if f.Ptr {
			if r, ok := f.Type.(*ir.Ref); ok {
				args[i] = &ir.Instr{Op: ir.OpRefNull, Type: r}
				continue
			}
		}
		if r, ok := f.Type.(*ir.Ref); ok && f.Len > 0 &&
			(r.Named == "$go.bytes" || r.Named == "$go.i16s" || strings.HasPrefix(r.Named, "$go.array.")) {
			// (ref $go.string) is non-defaultable — use array.new of "".
			if r.Named == "$go.array.go.string" {
				args[i] = &ir.Instr{
					Op:   ir.OpArrayNew,
					Type: &ir.Ref{Named: r.Named},
					Args: []ir.Expr{
						&ir.Instr{Op: ir.OpStringLit, Str: ""},
						&ir.Const{Op: ir.OpI32Const, I64: f.Len},
					},
				}
			} else {
				args[i] = &ir.Instr{
					Op:   ir.OpArrayNewDefault,
					Type: &ir.Ref{Named: r.Named},
					Args: []ir.Expr{&ir.Const{Op: ir.OpI32Const, I64: f.Len}},
				}
			}
			continue
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

// ensureZeroArrayFunc returns a helper that builds [n]T with a fresh Go-zero
// element in every slot. array.new(z, n) would alias one struct across all
// indices; field stores would then clobber every element.
func (fl *funcLower) ensureZeroArrayFunc(arrName string, elemIR ir.Type) string {
	name := "$go.zeroArray." + strings.TrimPrefix(arrName, "$")
	if fl.mod.LookupFunc(name) != nil {
		return name
	}
	aty := &ir.Ref{Named: arrName}
	nVar := &ir.Var{Name: "n", Typ: ir.TypeI32}
	aVar := &ir.Var{Name: "a", Typ: aty}
	iVar := &ir.Var{Name: "i", Typ: ir.TypeI32}
	mkZero := func() ir.Expr {
		ez, eerr := fl.zeroValue(elemIR)
		if eerr != nil {
			return &ir.Instr{Op: ir.OpRefNull, Type: elemIR}
		}
		return ez
	}
	loopL := fl.freshLabel("zeroArrL")
	breakL := fl.freshLabel("zeroArrB")
	body := &ir.Seq{Stmts: []ir.Stmt{
		&ir.VarDef{Var: aVar},
		&ir.VarDef{Var: iVar},
		&ir.Assign{Lhs: []ir.Dest{{Name: "a", Var: aVar}}, Rhs: &ir.Instr{
			Op:   ir.OpArrayNewDefault,
			Type: aty,
			Args: []ir.Expr{&ir.Name{Raw: "n", Var: nVar}},
		}},
		&ir.Assign{Lhs: []ir.Dest{{Name: "i", Var: iVar}}, Rhs: &ir.Const{Op: ir.OpI32Const, I64: 0}},
		&ir.Block{Name: breakL, Body: &ir.Loop{
			Name: loopL,
			Body: &ir.Seq{Stmts: []ir.Stmt{
				&ir.If{
					Cond: &ir.Instr{Op: ir.OpI32GeU, Args: []ir.Expr{
						&ir.Name{Raw: "i", Var: iVar},
						&ir.Name{Raw: "n", Var: nVar},
					}},
					Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Br{Label: breakL}}},
				},
				&ir.ExprStmt{X: &ir.Instr{
					Op:   ir.OpArraySet,
					Type: aty,
					Args: []ir.Expr{
						&ir.Name{Raw: "a", Var: aVar},
						&ir.Name{Raw: "i", Var: iVar},
						mkZero(),
					},
				}},
				&ir.Assign{Lhs: []ir.Dest{{Name: "i", Var: iVar}}, Rhs: &ir.Instr{
					Op: ir.OpI32Add, Args: []ir.Expr{
						&ir.Name{Raw: "i", Var: iVar},
						&ir.Const{Op: ir.OpI32Const, I64: 1},
					},
				}},
				&ir.Br{Label: loopL},
			}},
		}},
		&ir.Return{Results: []ir.Expr{&ir.Name{Raw: "a", Var: aVar}}},
	}}
	fl.mod.Funcs = append(fl.mod.Funcs, &ir.Func{
		Name:    name,
		Params:  []*ir.Var{nVar},
		Results: []ir.Type{aty},
		Body:    body,
	})
	return name
}
