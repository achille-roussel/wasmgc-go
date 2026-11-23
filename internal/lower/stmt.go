package lower

import (
	"go/ast"
	"go/token"
	"go/types"

	"github.com/achille-roussel/wasmgc-go/internal/desc"
	"github.com/achille-roussel/wasmgc-go/internal/ir"
)

func (fl *funcLower) stmtBlock(b *ast.BlockStmt) (ir.Region, error) {
	if b == nil {
		return &ir.Seq{}, nil
	}
	seq := &ir.Seq{}
	for _, s := range b.List {
		stmts, err := fl.stmt(s)
		if err != nil {
			return nil, err
		}
		seq.Stmts = append(seq.Stmts, stmts...)
	}
	return seq, nil
}

func (fl *funcLower) stmt(s ast.Stmt) ([]ir.Stmt, error) {
	switch s := s.(type) {
	case *ast.BlockStmt:
		r, err := fl.stmtBlock(s)
		if err != nil {
			return nil, err
		}
		return []ir.Stmt{r}, nil
	case *ast.AssignStmt:
		return fl.assignStmt(s)
	case *ast.DeclStmt:
		return fl.declStmt(s)
	case *ast.ExprStmt:
		return fl.exprStmt(s)
	case *ast.ReturnStmt:
		return fl.returnStmt(s)
	case *ast.IfStmt:
		return fl.ifStmt(s)
	case *ast.ForStmt:
		return fl.forStmt(s, "")
	case *ast.RangeStmt:
		return fl.rangeStmt(s, "")
	case *ast.SwitchStmt:
		return fl.switchStmt(s, "")
	case *ast.TypeSwitchStmt:
		return fl.typeSwitchStmt(s)
	case *ast.BranchStmt:
		return fl.branchStmt(s)
	case *ast.LabeledStmt:
		return fl.labeledStmt(s)
	case *ast.IncDecStmt:
		return fl.incDecStmt(s)
	case *ast.EmptyStmt:
		return nil, nil
	case *ast.GoStmt:
		return fl.goStmt(s)
	case *ast.DeferStmt:
		return fl.deferStmt(s)
	case *ast.SelectStmt:
		return fl.selectStmt(s)
	case *ast.SendStmt:
		return fl.sendStmt(s)
	default:
		return nil, fl.errorf(s, "unsupported statement %T", s)
	}
}

func (fl *funcLower) declStmt(s *ast.DeclStmt) ([]ir.Stmt, error) {
	gd, ok := s.Decl.(*ast.GenDecl)
	if !ok || gd.Tok != token.VAR {
		return nil, fl.errorf(s, "only var declarations supported in functions")
	}
	var out []ir.Stmt
	for _, sp := range gd.Specs {
		vs := sp.(*ast.ValueSpec)
		for i, name := range vs.Names {
			if name.Name == "_" {
				continue
			}
			obj := fl.info.Defs[name]
			if obj == nil {
				continue
			}
			ty, err := fl.mapType(obj.Type())
			if err != nil {
				return nil, fl.errorf(name, "%v", err)
			}
			v := &ir.Var{Name: name.Name, Typ: ty}
			fl.vars[obj] = v
			fl.extraLocals = append(fl.extraLocals, v)
			// VarDef is emitted once in lowerFunc preamble.
			if vs.Values != nil && i < len(vs.Values) {
				ex, stmts, err := fl.coerceExpr(vs.Values[i], obj.Type(), ty)
				if err != nil {
					return nil, err
				}
				out = append(out, stmts...)
				out = append(out, fl.assignVar(v, ex))
			} else if needsZeroInit(ty) {
				z, err := fl.zeroValue(ty)
				if err != nil {
					return nil, fl.errorf(name, "%v", err)
				}
				out = append(out, fl.assignVar(v, z))
			}
		}
	}
	return out, nil
}

func needsZeroInit(ty ir.Type) bool {
	r, ok := ty.(*ir.Ref)
	if !ok {
		return false
	}
	// Non-nullable refs have no Wasm default; initialize to the Go zero value.
	return !r.Null && r.Heap == 0 && r.Named != ""
}

func (fl *funcLower) assignStmt(s *ast.AssignStmt) ([]ir.Stmt, error) {
	if len(s.Rhs) == 1 && len(s.Lhs) > 1 {
		return fl.multiAssign(s)
	}
	if len(s.Lhs) != len(s.Rhs) {
		return nil, fl.errorf(s, "assignment count mismatch")
	}
	var out []ir.Stmt
	if s.Tok == token.DEFINE {
		for _, lhs := range s.Lhs {
			id, ok := lhs.(*ast.Ident)
			if !ok {
				return nil, fl.errorf(lhs, "non-name on left side of :=")
			}
			_, defs, err := fl.defineLocal(id)
			if err != nil {
				return nil, err
			}
			out = append(out, defs...)
		}
	}
	for i := range s.Lhs {
		want, err := fl.lhsType(s.Lhs[i])
		if err != nil {
			return nil, err
		}
		var rhs ir.Expr
		if s.Tok != token.ASSIGN && s.Tok != token.DEFINE {
			lhsEx, ls, err := fl.expr(s.Lhs[i], want)
			if err != nil {
				return nil, err
			}
			out = append(out, ls...)
			rhsEx, rs, err := fl.expr(s.Rhs[i], want)
			if err != nil {
				return nil, err
			}
			out = append(out, rs...)
			op, err := fl.binaryOpFromAssign(s.Tok, fl.typeOf(s.Lhs[i]))
			if err != nil {
				return nil, fl.errorf(s, "%v", err)
			}
			rhs = &ir.Instr{Op: op, Args: []ir.Expr{lhsEx, rhsEx}}
		} else if call, ok := s.Rhs[i].(*ast.CallExpr); ok && !fl.isConversion(call) && !fl.isBuiltinPrint(call) && !fl.isBuiltinCall(call) {
			// x = f(...) → single assign from call (no extra temp).
			if sig := fl.funcValueSig(call.Fun); sig != nil {
				ex, astmts, err := fl.callFuncValue(call.Fun, call.Args, sig)
				if err != nil {
					return nil, err
				}
				out = append(out, astmts...)
				if sig.Results().Len() != 1 {
					return nil, fl.errorf(call, "call returns %d values, want 1", sig.Results().Len())
				}
				rhs = ex
			} else {
				name, results, err := fl.resolveCallee(call)
				if err != nil {
					return nil, err
				}
				args, astmts, err := fl.lowerArgs(call)
				if err != nil {
					return nil, err
				}
				out = append(out, astmts...)
				if len(results) != 1 {
					return nil, fl.errorf(call, "call returns %d values, want 1", len(results))
				}
				rhs = &ir.Instr{Op: ir.OpCall, Func: name, Args: args}
			}
		} else {
			var stmts []ir.Stmt
			var err error
			dstGo := fl.typeOf(s.Lhs[i])
			if dstGo == nil {
				if id, ok := s.Lhs[i].(*ast.Ident); ok {
					if obj := fl.info.Defs[id]; obj != nil {
						dstGo = obj.Type()
					} else if obj := fl.info.Uses[id]; obj != nil {
						dstGo = obj.Type()
					}
				}
			}
			rhs, stmts, err = fl.coerceExpr(s.Rhs[i], dstGo, want)
			if err != nil {
				return nil, err
			}
			out = append(out, stmts...)
		}
		as, err := fl.assignTo(s.Lhs[i], rhs)
		if err != nil {
			return nil, err
		}
		out = append(out, as...)
	}
	return out, nil
}

func (fl *funcLower) multiAssign(s *ast.AssignStmt) ([]ir.Stmt, error) {
	var out []ir.Stmt
	if s.Tok == token.DEFINE {
		for _, lhs := range s.Lhs {
			id, ok := lhs.(*ast.Ident)
			if !ok {
				return nil, fl.errorf(lhs, "non-name on left side of :=")
			}
			_, defs, err := fl.defineLocal(id)
			if err != nil {
				return nil, err
			}
			out = append(out, defs...)
		}
	}
	if ta, ok := s.Rhs[0].(*ast.TypeAssertExpr); ok {
		return fl.multiAssignTypeAssert(s, ta, out)
	}
	if u, ok := s.Rhs[0].(*ast.UnaryExpr); ok && u.Op == token.ARROW {
		return fl.multiAssignRecv(s, u, out)
	}
	call, ok := s.Rhs[0].(*ast.CallExpr)
	if !ok {
		return nil, fl.errorf(s, "multi-value assignment requires a call, type assert, or channel receive")
	}
	resultTypes := make([]ir.Type, len(s.Lhs))
	for i, lhs := range s.Lhs {
		ty, err := fl.lhsType(lhs)
		if err != nil {
			return nil, err
		}
		resultTypes[i] = ty
	}
	temps, stmts, err := fl.callMaterialize(call, resultTypes)
	if err != nil {
		return nil, err
	}
	out = append(out, stmts...)
	for i, t := range temps {
		as, err := fl.assignTo(s.Lhs[i], fl.useTemp(t))
		if err != nil {
			return nil, err
		}
		out = append(out, as...)
	}
	return out, nil
}

func (fl *funcLower) multiAssignTypeAssert(s *ast.AssignStmt, ta *ast.TypeAssertExpr, out []ir.Stmt) ([]ir.Stmt, error) {
	if len(s.Lhs) != 2 {
		return nil, fl.errorf(s, "type assert comma-ok wants 2 LHS")
	}
	ex, stmts, err := fl.typeAssert(ta, nil, true)
	if err != nil {
		return nil, err
	}
	out = append(out, stmts...)
	// Materialize two results: value and ok.
	valTy, err := fl.lhsType(s.Lhs[0])
	if err != nil {
		return nil, err
	}
	okTy := ir.TypeI32
	vt := fl.newTemp(valTy)
	ot := fl.newTemp(okTy)
	out = append(out, &ir.Assign{
		Lhs: []ir.Dest{
			{Temp: vt, Name: vt.Name},
			{Temp: ot, Name: ot.Name},
		},
		Rhs: ex,
	})
	as0, err := fl.assignTo(s.Lhs[0], fl.useTemp(vt))
	if err != nil {
		return nil, err
	}
	as1, err := fl.assignTo(s.Lhs[1], fl.useTemp(ot))
	if err != nil {
		return nil, err
	}
	out = append(out, as0...)
	out = append(out, as1...)
	return out, nil
}

func (fl *funcLower) assignTo(lhs ast.Expr, rhs ir.Expr) ([]ir.Stmt, error) {
	switch lhs := lhs.(type) {
	case *ast.Ident:
		if lhs.Name == "_" {
			return []ir.Stmt{&ir.ExprStmt{X: &ir.Instr{Op: ir.OpDrop, Args: []ir.Expr{rhs}}}}, nil
		}
		obj := fl.info.Uses[lhs]
		if obj == nil {
			obj = fl.info.Defs[lhs]
		}
		if obj != nil {
			fl.markAssigned(obj)
			if fl.cells != nil {
				if _, ok := fl.cells[obj]; ok {
					return fl.writeVar(obj, rhs)
				}
			}
			if g, ok := fl.pkg.globals[obj]; ok {
				return []ir.Stmt{&ir.ExprStmt{X: &ir.Instr{
					Op:     ir.OpGlobalSet,
					Global: g.Name,
					Args:   []ir.Expr{rhs},
				}}}, nil
			}
		}
		if v, err := fl.lookupVar(lhs); err == nil && v != nil {
			return []ir.Stmt{fl.assignVar(v, rhs)}, nil
		}
		v, err := fl.lookupVar(lhs)
		if err != nil {
			return nil, err
		}
		return []ir.Stmt{fl.assignVar(v, rhs)}, nil
	case *ast.StarExpr:
		ptr, stmts, err := fl.expr(lhs.X, nil)
		if err != nil {
			return nil, err
		}
		stmts = append(stmts, &ir.ExprStmt{X: &ir.Instr{Op: ir.OpStore, Args: []ir.Expr{ptr, rhs}}})
		return stmts, nil
	case *ast.SelectorExpr:
		sel := fl.info.Selections[lhs]
		if sel == nil || sel.Kind() != types.FieldVal {
			return nil, fl.errorf(lhs, "assignment to non-field selector")
		}
		base, stmts, err := fl.expr(lhs.X, nil)
		if err != nil {
			return nil, err
		}
		// Prefer struct.set for mutable fields when base is a struct ref.
		baseTy := fl.typeOf(lhs.X)
		if baseTy != nil && (isStruct(baseTy) || isObjectPointer(baseTy)) {
			stName := ""
			if irt, err := fl.mapType(baseTy); err == nil {
				stName = refTypeName(irt)
			}
			if stName != "" {
				stmts = append(stmts, &ir.ExprStmt{X: &ir.Instr{
					Op:    ir.OpStructSet,
					Type:  &ir.Ref{Named: stName},
					Field: lhs.Sel.Name,
					Args:  []ir.Expr{base, rhs},
				}})
				return stmts, nil
			}
		}
		fieldTy := sel.Type()
		kind, _ := fatPtrKind(fieldTy)
		addr := &ir.Instr{
			Op:    ir.OpAddr,
			Type:  &ir.Ref{Null: true, Named: "$go.ptr." + kind},
			Args:  []ir.Expr{base},
			Field: lhs.Sel.Name,
		}
		stmts = append(stmts, &ir.ExprStmt{X: &ir.Instr{Op: ir.OpStore, Type: addr.Type, Args: []ir.Expr{addr, rhs}}})
		return stmts, nil
	case *ast.IndexExpr:
		base, stmts, err := fl.expr(lhs.X, nil)
		if err != nil {
			return nil, err
		}
		idx, istmts, err := fl.expr(lhs.Index, ir.TypeI32)
		if err != nil {
			return nil, err
		}
		stmts = append(stmts, istmts...)
		xt := fl.typeOf(lhs.X)
		if isSlice(xt) {
			stmts = append(stmts, &ir.ExprStmt{X: &ir.Instr{Op: ir.OpSliceSet, Args: []ir.Expr{base, idx, rhs}}})
			return stmts, nil
		}
		addr := &ir.Instr{Op: ir.OpAddr, Args: []ir.Expr{base, idx}, Field: "elem", Str: "elem"}
		stmts = append(stmts, &ir.ExprStmt{X: &ir.Instr{Op: ir.OpStore, Args: []ir.Expr{addr, rhs}}})
		return stmts, nil
	case *ast.ParenExpr:
		return fl.assignTo(lhs.X, rhs)
	default:
		return nil, fl.errorf(lhs, "assignment to %T not supported in M3", lhs)
	}
}

func (fl *funcLower) lhsType(lhs ast.Expr) (ir.Type, error) {
	if id, ok := lhs.(*ast.Ident); ok && id.Name == "_" {
		return ir.TypeI32, nil
	}
	t := fl.typeOf(lhs)
	if t == nil {
		if id, ok := lhs.(*ast.Ident); ok {
			if obj := fl.info.Defs[id]; obj != nil {
				t = obj.Type()
			} else if obj := fl.info.Uses[id]; obj != nil {
				t = obj.Type()
			}
		}
	}
	if t == nil {
		return nil, fl.errorf(lhs, "no type for LHS")
	}
	return fl.mapType(t)
}

func (fl *funcLower) exprStmt(s *ast.ExprStmt) ([]ir.Stmt, error) {
	if u, ok := s.X.(*ast.UnaryExpr); ok && u.Op == token.ARROW {
		return fl.chanRecvStmt(u)
	}
	call, ok := s.X.(*ast.CallExpr)
	if !ok {
		return nil, fl.errorf(s, "expression statement must be a call or channel receive")
	}
	_, stmts, err := fl.callExpr(call, false)
	return stmts, err
}

func (fl *funcLower) returnStmt(s *ast.ReturnStmt) ([]ir.Stmt, error) {
	if len(s.Results) == 0 {
		if len(fl.namedResults) > 0 {
			var results []ir.Expr
			for _, v := range fl.namedResults {
				results = append(results, fl.useVar(v))
			}
			return []ir.Stmt{&ir.Return{Results: results}}, nil
		}
		return []ir.Stmt{&ir.Return{}}, nil
	}
	if len(s.Results) == 1 && len(fl.results) > 1 {
		call, ok := s.Results[0].(*ast.CallExpr)
		if !ok {
			return nil, fl.errorf(s, "expected multi-value call in return")
		}
		temps, stmts, err := fl.callMaterialize(call, fl.results)
		if err != nil {
			return nil, err
		}
		var results []ir.Expr
		for _, t := range temps {
			results = append(results, fl.useTemp(t))
		}
		stmts = append(stmts, &ir.Return{Results: results})
		return stmts, nil
	}
	var out []ir.Stmt
	var results []ir.Expr
	for i, r := range s.Results {
		var want ir.Type
		var dstGo types.Type
		if i < len(fl.results) {
			want = fl.results[i]
		}
		if fl.sig != nil && i < fl.sig.Results().Len() {
			dstGo = fl.sig.Results().At(i).Type()
		}
		ex, stmts, err := fl.coerceExpr(r, dstGo, want)
		if err != nil {
			return nil, err
		}
		out = append(out, stmts...)
		results = append(results, ex)
	}
	out = append(out, &ir.Return{Results: results})
	return out, nil
}

func (fl *funcLower) ifStmt(s *ast.IfStmt) ([]ir.Stmt, error) {
	var out []ir.Stmt
	if s.Init != nil {
		init, err := fl.stmt(s.Init)
		if err != nil {
			return nil, err
		}
		out = append(out, init...)
	}
	cond, cstmts, err := fl.boolExpr(s.Cond)
	if err != nil {
		return nil, err
	}
	out = append(out, cstmts...)
	thenR, err := fl.stmtBlock(s.Body)
	if err != nil {
		return nil, err
	}
	var elseR ir.Region
	if s.Else != nil {
		switch e := s.Else.(type) {
		case *ast.BlockStmt:
			elseR, err = fl.stmtBlock(e)
		case *ast.IfStmt:
			es, err2 := fl.ifStmt(e)
			if err2 != nil {
				return nil, err2
			}
			elseR = &ir.Seq{Stmts: es}
		default:
			return nil, fl.errorf(s.Else, "unsupported else")
		}
		if err != nil {
			return nil, err
		}
	}
	out = append(out, &ir.If{Cond: cond, Then: thenR, Else: elseR})
	return out, nil
}

func (fl *funcLower) forStmt(s *ast.ForStmt, goLabel string) ([]ir.Stmt, error) {
	var out []ir.Stmt
	if s.Init != nil {
		init, err := fl.stmt(s.Init)
		if err != nil {
			return nil, err
		}
		out = append(out, init...)
	}

	var breakL, contL, loopL string
	if goLabel != "" {
		breakL = "$" + goLabel + ".break"
		contL = "$" + goLabel + ".cont"
		loopL = "$" + goLabel
	} else {
		breakL = fl.freshLabel("break")
		contL = fl.freshLabel("cont")
		loopL = fl.freshLabel("L")
	}

	fl.breakStack = append(fl.breakStack, breakL)
	fl.continueStack = append(fl.continueStack, contL)
	prevDeferLoop := fl.deferInLoop
	fl.deferInLoop = true
	if goLabel != "" {
		fl.labels[goLabel] = &labelTargets{breakLabel: breakL, continueLabel: contL}
	}
	defer func() {
		fl.breakStack = fl.breakStack[:len(fl.breakStack)-1]
		fl.continueStack = fl.continueStack[:len(fl.continueStack)-1]
		fl.deferInLoop = prevDeferLoop
		if goLabel != "" {
			delete(fl.labels, goLabel)
		}
	}()

	bodySeq := &ir.Seq{}
	if s.Cond != nil {
		cond, cstmts, err := fl.boolExpr(s.Cond)
		if err != nil {
			return nil, err
		}
		bodySeq.Stmts = append(bodySeq.Stmts, cstmts...)
		bodySeq.Stmts = append(bodySeq.Stmts, &ir.Br{
			Label: breakL,
			Cond:  &ir.Instr{Op: ir.OpI32Eqz, Args: []ir.Expr{cond}},
		})
	}

	inner, err := fl.stmtBlock(s.Body)
	if err != nil {
		return nil, err
	}
	bodySeq.Stmts = append(bodySeq.Stmts, &ir.Block{Name: contL, Body: inner})

	if s.Post != nil {
		post, err := fl.stmt(s.Post)
		if err != nil {
			return nil, err
		}
		bodySeq.Stmts = append(bodySeq.Stmts, post...)
	}
	bodySeq.Stmts = append(bodySeq.Stmts, &ir.Br{Label: loopL})

	loop := &ir.Loop{Name: loopL, Body: bodySeq}
	out = append(out, &ir.Block{Name: breakL, Body: &ir.Seq{Stmts: []ir.Stmt{loop}}})
	return out, nil
}

func (fl *funcLower) labeledStmt(s *ast.LabeledStmt) ([]ir.Stmt, error) {
	name := s.Label.Name
	switch body := s.Stmt.(type) {
	case *ast.ForStmt:
		return fl.forStmt(body, name)
	case *ast.SwitchStmt:
		return fl.switchStmt(body, name)
	case *ast.RangeStmt:
		return fl.rangeStmt(body, name)
	default:
		breakL := "$" + name
		fl.labels[name] = &labelTargets{breakLabel: breakL}
		fl.breakStack = append(fl.breakStack, breakL)
		defer func() {
			delete(fl.labels, name)
			fl.breakStack = fl.breakStack[:len(fl.breakStack)-1]
		}()
		stmts, err := fl.stmt(body)
		if err != nil {
			return nil, err
		}
		return []ir.Stmt{&ir.Block{Name: breakL, Body: &ir.Seq{Stmts: stmts}}}, nil
	}
}

// rangeStmt lowers for-range over slices/arrays (index loop) and channels
// (recv until !ok). Maps/strings wait for later milestones.
func (fl *funcLower) rangeStmt(s *ast.RangeStmt, goLabel string) ([]ir.Stmt, error) {
	prevDeferLoop := fl.deferInLoop
	fl.deferInLoop = true
	defer func() { fl.deferInLoop = prevDeferLoop }()
	xType := fl.typeOf(s.X)
	if xType == nil {
		return nil, fl.errorf(s.X, "range: no type")
	}
	u := xType.Underlying()
	switch u.(type) {
	case *types.Slice, *types.Array:
		// ok
	case *types.Chan:
		return fl.rangeChanStmt(s, goLabel)
	default:
		return nil, fl.errorf(s.X, "range over %s not supported", xType)
	}
	xIR, err := fl.mapType(xType)
	if err != nil {
		return nil, fl.errorf(s.X, "%v", err)
	}
	x, xstmts, err := fl.expr(s.X, xIR)
	if err != nil {
		return nil, err
	}
	var out []ir.Stmt
	out = append(out, xstmts...)
	xv := fl.newVar("rng", xIR)
	out = append(out, fl.assignVar(xv, x))

	lenEx := &ir.Instr{Op: ir.OpLen, Args: []ir.Expr{fl.useVar(xv)}}
	lenV := fl.newVar("rlen", ir.TypeI32)
	out = append(out, fl.assignVar(lenV, lenEx))

	i := fl.newVar("ri", ir.TypeI32)
	out = append(out, fl.assignVar(i, fl.i32const(0)))

	// Bind range vars if present.
	var keyObj, valObj types.Object
	if s.Key != nil {
		if id, ok := s.Key.(*ast.Ident); ok && id.Name != "_" {
			keyObj = fl.info.Defs[id]
			if keyObj != nil {
				fl.vars[keyObj] = i
			}
		}
	}
	var valVar *ir.Var
	if s.Value != nil {
		if id, ok := s.Value.(*ast.Ident); ok && id.Name != "_" {
			valObj = fl.info.Defs[id]
			if valObj != nil {
				vt, err := fl.mapType(valObj.Type())
				if err != nil {
					return nil, fl.errorf(s.Value, "%v", err)
				}
				valVar = &ir.Var{Name: id.Name, Typ: vt}
				fl.vars[valObj] = valVar
				fl.extraLocals = append(fl.extraLocals, valVar)
			}
		}
	}
	_ = keyObj

	var breakL, contL, loopL string
	if goLabel != "" {
		breakL = "$" + goLabel + ".break"
		contL = "$" + goLabel + ".cont"
		loopL = "$" + goLabel
		fl.labels[goLabel] = &labelTargets{breakLabel: breakL, continueLabel: contL}
	} else {
		breakL = fl.freshLabel("break")
		contL = fl.freshLabel("cont")
		loopL = fl.freshLabel("L")
	}
	fl.breakStack = append(fl.breakStack, breakL)
	fl.continueStack = append(fl.continueStack, contL)
	defer func() {
		fl.breakStack = fl.breakStack[:len(fl.breakStack)-1]
		fl.continueStack = fl.continueStack[:len(fl.continueStack)-1]
		if goLabel != "" {
			delete(fl.labels, goLabel)
		}
	}()

	bodySeq := &ir.Seq{}
	// if i >= len { br break }
	bodySeq.Stmts = append(bodySeq.Stmts, &ir.Br{
		Label: breakL,
		Cond: &ir.Instr{Op: ir.OpI32GeU, Args: []ir.Expr{fl.useVar(i), fl.useVar(lenV)}},
	})
	if valVar != nil {
		elem := &ir.Instr{Op: ir.OpSliceGet, Type: valVar.Typ, Args: []ir.Expr{fl.useVar(xv), fl.useVar(i)}}
		bodySeq.Stmts = append(bodySeq.Stmts, fl.assignVar(valVar, elem))
	}
	inner, err := fl.stmtBlock(s.Body)
	if err != nil {
		return nil, err
	}
	bodySeq.Stmts = append(bodySeq.Stmts, &ir.Block{Name: contL, Body: inner})
	bodySeq.Stmts = append(bodySeq.Stmts, fl.assignVar(i, &ir.Instr{
		Op: ir.OpI32Add, Args: []ir.Expr{fl.useVar(i), fl.i32const(1)},
	}))
	bodySeq.Stmts = append(bodySeq.Stmts, &ir.Br{Label: loopL})

	loop := &ir.Loop{Name: loopL, Body: bodySeq}
	out = append(out, &ir.Block{Name: breakL, Body: &ir.Seq{Stmts: []ir.Stmt{loop}}})
	return out, nil
}

// rangeChanStmt lowers `for v := range ch` / `for range ch` to recv-until-!ok.
func (fl *funcLower) rangeChanStmt(s *ast.RangeStmt, goLabel string) ([]ir.Stmt, error) {
	if s.Key != nil && s.Value != nil {
		return nil, fl.errorf(s, "range over chan allows at most one iteration variable")
	}
	fl.pkg.ensureChanBaseTypes()
	if fl.pkg.meta != nil {
		fl.pkg.meta.needsSched = true
	}
	xType := fl.typeOf(s.X)
	xIR, err := fl.mapType(xType)
	if err != nil {
		return nil, fl.errorf(s.X, "%v", err)
	}
	x, xstmts, err := fl.expr(s.X, xIR)
	if err != nil {
		return nil, err
	}
	var out []ir.Stmt
	out = append(out, xstmts...)
	xv := fl.newVar("rch", xIR)
	out = append(out, fl.assignVar(xv, x))

	elemGo := chanElem(xType)
	var valTy ir.Type = typeIface
	if elemGo != nil {
		if t, err := fl.mapType(elemGo); err == nil {
			valTy = t
		}
	}
	var valVar *ir.Var
	iter := s.Key
	if s.Value != nil {
		iter = s.Value
	}
	if iter != nil {
		if id, ok := iter.(*ast.Ident); ok && id.Name != "_" {
			obj := fl.info.Defs[id]
			if obj != nil {
				vt, err := fl.mapType(obj.Type())
				if err != nil {
					return nil, fl.errorf(iter, "%v", err)
				}
				valVar = &ir.Var{Name: id.Name, Typ: vt}
				fl.vars[obj] = valVar
				fl.extraLocals = append(fl.extraLocals, valVar)
				valTy = vt
			}
		}
	}

	var breakL, contL, loopL string
	if goLabel != "" {
		breakL = "$" + goLabel + ".break"
		contL = "$" + goLabel + ".cont"
		loopL = "$" + goLabel
		fl.labels[goLabel] = &labelTargets{breakLabel: breakL, continueLabel: contL}
	} else {
		breakL = fl.freshLabel("break")
		contL = fl.freshLabel("cont")
		loopL = fl.freshLabel("L")
	}
	fl.breakStack = append(fl.breakStack, breakL)
	fl.continueStack = append(fl.continueStack, contL)
	defer func() {
		fl.breakStack = fl.breakStack[:len(fl.breakStack)-1]
		fl.continueStack = fl.continueStack[:len(fl.continueStack)-1]
		if goLabel != "" {
			delete(fl.labels, goLabel)
		}
	}()

	bodySeq := &ir.Seq{}
	recv := &ir.Instr{
		Op:   ir.OpChanRecv,
		Type: valTy,
		Str:  "ok",
		Args: []ir.Expr{fl.useVar(xv)},
	}
	if elemGo != nil {
		recv.Global = desc.TypeKey(elemGo)
		recv.Field = boxKind(elemGo)
	}
	vt := fl.newTemp(valTy)
	ot := fl.newTemp(ir.TypeI32)
	bodySeq.Stmts = append(bodySeq.Stmts, &ir.Assign{
		Lhs: []ir.Dest{
			{Temp: vt, Name: vt.Name},
			{Temp: ot, Name: ot.Name},
		},
		Rhs: recv,
	})
	bodySeq.Stmts = append(bodySeq.Stmts, &ir.Br{
		Label: breakL,
		Cond: &ir.Instr{Op: ir.OpI32Eqz, Args: []ir.Expr{fl.useTemp(ot)}},
	})
	if valVar != nil {
		bodySeq.Stmts = append(bodySeq.Stmts, fl.assignVar(valVar, fl.useTemp(vt)))
	}
	inner, err := fl.stmtBlock(s.Body)
	if err != nil {
		return nil, err
	}
	bodySeq.Stmts = append(bodySeq.Stmts, &ir.Block{Name: contL, Body: inner})
	bodySeq.Stmts = append(bodySeq.Stmts, &ir.Br{Label: loopL})

	loop := &ir.Loop{Name: loopL, Body: bodySeq}
	out = append(out, &ir.Block{Name: breakL, Body: &ir.Seq{Stmts: []ir.Stmt{loop}}})
	return out, nil
}

func (fl *funcLower) branchStmt(s *ast.BranchStmt) ([]ir.Stmt, error) {
	switch s.Tok {
	case token.BREAK:
		if s.Label != nil {
			lt, ok := fl.labels[s.Label.Name]
			if !ok || lt.breakLabel == "" {
				return nil, fl.errorf(s, "break label %s not in scope", s.Label.Name)
			}
			return []ir.Stmt{&ir.Br{Label: lt.breakLabel}}, nil
		}
		if len(fl.breakStack) == 0 {
			return nil, fl.errorf(s, "break outside loop or switch")
		}
		return []ir.Stmt{&ir.Br{Label: fl.breakStack[len(fl.breakStack)-1]}}, nil
	case token.CONTINUE:
		if s.Label != nil {
			lt, ok := fl.labels[s.Label.Name]
			if !ok || lt.continueLabel == "" {
				return nil, fl.errorf(s, "continue label %s not in scope", s.Label.Name)
			}
			return []ir.Stmt{&ir.Br{Label: lt.continueLabel}}, nil
		}
		if len(fl.continueStack) == 0 {
			return nil, fl.errorf(s, "continue outside loop")
		}
		return []ir.Stmt{&ir.Br{Label: fl.continueStack[len(fl.continueStack)-1]}}, nil
	case token.FALLTHROUGH:
		return nil, fl.errorf(s, "fallthrough not yet lowered")
	case token.GOTO:
		return nil, fl.errorf(s, "goto not supported in M1/M2")
	default:
		return nil, fl.errorf(s, "unsupported branch %v", s.Tok)
	}
}

func (fl *funcLower) incDecStmt(s *ast.IncDecStmt) ([]ir.Stmt, error) {
	ty := fl.typeOf(s.X)
	want, err := fl.mapType(ty)
	if err != nil {
		return nil, fl.errorf(s, "%v", err)
	}
	lhs, stmts, err := fl.expr(s.X, want)
	if err != nil {
		return nil, err
	}
	var one ir.Expr
	var op ir.Op
	switch irNumWidth(want) {
	case ir.I64:
		one = fl.i64const(1)
		if s.Tok == token.INC {
			op = ir.OpI64Add
		} else {
			op = ir.OpI64Sub
		}
	case ir.F32:
		one = fl.f32const(1)
		if s.Tok == token.INC {
			op = ir.OpF32Add
		} else {
			op = ir.OpF32Sub
		}
	case ir.F64:
		one = fl.f64const(1)
		if s.Tok == token.INC {
			op = ir.OpF64Add
		} else {
			op = ir.OpF64Sub
		}
	default:
		one = fl.i32const(1)
		if s.Tok == token.INC {
			op = ir.OpI32Add
		} else {
			op = ir.OpI32Sub
		}
	}
	rhs := &ir.Instr{Op: op, Args: []ir.Expr{lhs, one}}
	as, err := fl.assignTo(s.X, rhs)
	if err != nil {
		return nil, err
	}
	return append(stmts, as...), nil
}

func (fl *funcLower) switchStmt(s *ast.SwitchStmt, goLabel string) ([]ir.Stmt, error) {
	var out []ir.Stmt
	if s.Init != nil {
		init, err := fl.stmt(s.Init)
		if err != nil {
			return nil, err
		}
		out = append(out, init...)
	}

	breakL := fl.freshLabel("sw")
	if goLabel != "" {
		breakL = "$" + goLabel + ".break"
		fl.labels[goLabel] = &labelTargets{breakLabel: breakL}
		defer delete(fl.labels, goLabel)
	}
	fl.breakStack = append(fl.breakStack, breakL)
	defer func() { fl.breakStack = fl.breakStack[:len(fl.breakStack)-1] }()

	// Tag expression; default true for switch { case cond: }.
	var tag ir.Expr
	var tagType types.Type
	if s.Tag != nil {
		tagType = fl.typeOf(s.Tag)
		want, err := fl.mapType(tagType)
		if err != nil {
			return nil, fl.errorf(s.Tag, "%v", err)
		}
		var stmts []ir.Stmt
		tag, stmts, err = fl.expr(s.Tag, want)
		if err != nil {
			return nil, err
		}
		out = append(out, stmts...)
		// Materialize tag into a var so case compares can reuse it.
		tv := fl.newVar("tag", want)
		out = append(out, fl.assignVar(tv, tag))
		tag = fl.useVar(tv)
	}

	// Collect cases.
	var arms []caseArm
	for _, clause := range s.Body.List {
		cc := clause.(*ast.CaseClause)
		arm := caseArm{body: &ast.BlockStmt{List: cc.Body}, def: cc.List == nil}
		if cc.List == nil {
			arms = append(arms, arm)
			continue
		}
		if s.Tag == nil {
			arm.conds = cc.List
		} else {
			for _, c := range cc.List {
				want, _ := fl.mapType(tagType)
				ex, stmts, err := fl.expr(c, want)
				if err != nil {
					return nil, err
				}
				if len(stmts) > 0 {
					return nil, fl.errorf(c, "case value must be side-effect free in M1/M2")
				}
				arm.values = append(arm.values, ex)
			}
		}
		arms = append(arms, arm)
	}

	// Dense integer switch → IR Switch; else if-chain.
	if s.Tag != nil && isInteger(tagType) && canDenseSwitch(arms) {
		sw, err := fl.emitDenseSwitch(tag, arms, breakL)
		if err != nil {
			return nil, err
		}
		out = append(out, &ir.Block{Name: breakL, Body: &ir.Seq{Stmts: []ir.Stmt{sw}}})
		return out, nil
	}

	chain, err := fl.emitSwitchIfChain(tag, s.Tag == nil, tagType, arms, breakL)
	if err != nil {
		return nil, err
	}
	out = append(out, &ir.Block{Name: breakL, Body: &ir.Seq{Stmts: chain}})
	return out, nil
}

type caseArm struct {
	values []ir.Expr  // for tagged switch
	conds  []ast.Expr // for switch without tag
	body   *ast.BlockStmt
	def    bool
}

func canDenseSwitch(arms []caseArm) bool {
	var vals []int64
	for _, a := range arms {
		if a.def {
			continue
		}
		for _, v := range a.values {
			c, ok := v.(*ir.Const)
			if !ok || (c.Op != ir.OpI32Const && c.Op != ir.OpI64Const) {
				return false
			}
			vals = append(vals, c.I64)
		}
	}
	if len(vals) == 0 {
		return false
	}
	min, max := vals[0], vals[0]
	for _, v := range vals[1:] {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	width := max - min + 1
	return width >= 0 && width <= int64(4*len(vals))
}

func (fl *funcLower) emitDenseSwitch(tag ir.Expr, arms []caseArm, breakL string) (ir.Stmt, error) {
	sw := &ir.Switch{Tag: tag, Dense: true}
	for _, a := range arms {
		body, err := fl.stmtBlock(a.body)
		if err != nil {
			return nil, err
		}
		// End each case with br break (no fallthrough).
		body = appendBr(body, breakL)
		if a.def {
			sw.Default = body
			continue
		}
		for _, v := range a.values {
			c := v.(*ir.Const)
			sw.Cases = append(sw.Cases, ir.Case{Value: c.I64, Body: body})
		}
	}
	return sw, nil
}

func appendBr(r ir.Region, label string) ir.Region {
	seq, ok := r.(*ir.Seq)
	if !ok {
		seq = &ir.Seq{Stmts: []ir.Stmt{r}}
	}
	seq.Stmts = append(seq.Stmts, &ir.Br{Label: label})
	return seq
}

func (fl *funcLower) emitSwitchIfChain(tag ir.Expr, untag bool, tagType types.Type, arms []caseArm, breakL string) ([]ir.Stmt, error) {
	// Build nested if/else from first to last; default last.
	var def *caseArm
	var cases []caseArm
	for i := range arms {
		if arms[i].def {
			def = &arms[i]
		} else {
			cases = append(cases, arms[i])
		}
	}
	var build func(i int) (ir.Region, error)
	build = func(i int) (ir.Region, error) {
		if i >= len(cases) {
			if def == nil {
				return &ir.Seq{}, nil
			}
			body, err := fl.stmtBlock(def.body)
			if err != nil {
				return nil, err
			}
			return appendBr(body, breakL), nil
		}
		a := cases[i]
		body, err := fl.stmtBlock(a.body)
		if err != nil {
			return nil, err
		}
		body = appendBr(body, breakL)
		elseR, err := build(i + 1)
		if err != nil {
			return nil, err
		}
		var cond ir.Expr
		if untag {
			// OR of conditions
			if len(a.conds) == 0 {
				return nil, fl.errorf(a.body, "empty case")
			}
			var cstmts []ir.Stmt
			cond, cstmts, err = fl.boolExpr(a.conds[0])
			if err != nil {
				return nil, err
			}
			if len(cstmts) > 0 {
				return nil, fl.errorf(a.conds[0], "case condition side effects not supported")
			}
			for _, c := range a.conds[1:] {
				ex, st, err := fl.boolExpr(c)
				if err != nil {
					return nil, err
				}
				if len(st) > 0 {
					return nil, fl.errorf(c, "case condition side effects not supported")
				}
				cond = &ir.Instr{Op: ir.OpI32Or, Args: []ir.Expr{cond, ex}}
			}
		} else {
			want, _ := fl.mapType(tagType)
			eqOp := ir.OpI32Eq
			if irNumWidth(want) == ir.I64 {
				eqOp = ir.OpI64Eq
			}
			cond = &ir.Instr{Op: eqOp, Args: []ir.Expr{tag, a.values[0]}}
			for _, v := range a.values[1:] {
				cmp := &ir.Instr{Op: eqOp, Args: []ir.Expr{tag, v}}
				cond = &ir.Instr{Op: ir.OpI32Or, Args: []ir.Expr{cond, cmp}}
			}
		}
		return &ir.Seq{Stmts: []ir.Stmt{&ir.If{Cond: cond, Then: body, Else: elseR}}}, nil
	}
	r, err := build(0)
	if err != nil {
		return nil, err
	}
	if seq, ok := r.(*ir.Seq); ok {
		return seq.Stmts, nil
	}
	return []ir.Stmt{r}, nil
}

func (fl *funcLower) typeSwitchStmt(s *ast.TypeSwitchStmt) ([]ir.Stmt, error) {
	var out []ir.Stmt
	if s.Init != nil {
		init, err := fl.stmt(s.Init)
		if err != nil {
			return nil, err
		}
		out = append(out, init...)
	}
	// Assign form: switch x := v.(type)  or  switch v.(type)
	var tagExpr ast.Expr
	var bind *ast.Ident
	switch a := s.Assign.(type) {
	case *ast.ExprStmt:
		ta, ok := a.X.(*ast.TypeAssertExpr)
		if !ok || ta.Type != nil {
			return nil, fl.errorf(s, "bad type switch assign")
		}
		tagExpr = ta.X
	case *ast.AssignStmt:
		if len(a.Lhs) != 1 || len(a.Rhs) != 1 {
			return nil, fl.errorf(s, "bad type switch assign")
		}
		id, ok := a.Lhs[0].(*ast.Ident)
		if !ok {
			return nil, fl.errorf(s, "bad type switch bind")
		}
		bind = id
		ta, ok := a.Rhs[0].(*ast.TypeAssertExpr)
		if !ok || ta.Type != nil {
			return nil, fl.errorf(s, "bad type switch rhs")
		}
		tagExpr = ta.X
	default:
		return nil, fl.errorf(s, "unsupported type switch assign %T", s.Assign)
	}

	ifaceEx, stmts, err := fl.expr(tagExpr, typeIface)
	if err != nil {
		return nil, err
	}
	out = append(out, stmts...)
	ifaceV := fl.newVar("ts", typeIface)
	out = append(out, fl.assignVar(ifaceV, ifaceEx))

	// If-else chain implements case exclusivity; no outer break block needed
	// (a trailing br after return left functions with results failing validation).
	breakL := fl.freshLabel("tsbreak")
	fl.breakStack = append(fl.breakStack, breakL)
	defer func() { fl.breakStack = fl.breakStack[:len(fl.breakStack)-1] }()

	var build func(i int) (ir.Region, error)
	build = func(i int) (ir.Region, error) {
		if i >= len(s.Body.List) {
			return &ir.Seq{}, nil
		}
		cc := s.Body.List[i].(*ast.CaseClause)
		bodySeq := &ir.Seq{}
		// Bind variable for this case when present.
		if bind != nil && bind.Name != "_" {
			var caseTy types.Type
			if len(cc.List) == 1 {
				caseTy = fl.typeOf(cc.List[0])
			} else if len(cc.List) == 0 {
				caseTy = fl.typeOf(tagExpr) // default: interface type
			}
			if caseTy != nil {
				irTy, err := fl.mapType(caseTy)
				if err != nil {
					return nil, err
				}
				v := &ir.Var{Name: bind.Name, Typ: irTy}
				// Shadow per-case — use unique IR name.
				v.Name = bind.Name + "_" + itoa(i)
				fl.extraLocals = append(fl.extraLocals, v)
				obj := fl.info.Defs[bind]
				if obj != nil {
					fl.vars[obj] = v
				}
				if len(cc.List) == 1 {
					assert := &ir.Instr{
						Op:     ir.OpIfaceAssert,
						Type:   irTy,
						Global: desc.TypeKey(caseTy),
						Str:    "concrete,ok",
						I32:    1,
						Args:   []ir.Expr{fl.useVar(ifaceV)},
					}
					// Only use value; ok checked by case guard.
					vt := fl.newTemp(irTy)
					ot := fl.newTemp(ir.TypeI32)
					bodySeq.Stmts = append(bodySeq.Stmts, &ir.Assign{
						Lhs: []ir.Dest{{Temp: vt, Name: vt.Name}, {Temp: ot, Name: ot.Name}},
						Rhs: assert,
					})
					bodySeq.Stmts = append(bodySeq.Stmts, fl.assignVar(v, fl.useTemp(vt)))
				} else {
					bodySeq.Stmts = append(bodySeq.Stmts, fl.assignVar(v, fl.useVar(ifaceV)))
				}
			}
		}
		for _, st := range cc.Body {
			ss, err := fl.stmt(st)
			if err != nil {
				return nil, err
			}
			bodySeq.Stmts = append(bodySeq.Stmts, ss...)
		}
		if !regionEndsWithReturn(bodySeq) {
			bodySeq.Stmts = append(bodySeq.Stmts, &ir.Br{Label: breakL})
		}

		if len(cc.List) == 0 {
			// default
			return bodySeq, nil
		}
		// Match: any of the listed types.
		var cond ir.Expr
		for j, texpr := range cc.List {
			ct := fl.typeOf(texpr)
			key := desc.TypeKey(ct)
			cmp := &ir.Instr{
				Op:     ir.OpIfaceAssert,
				Type:   ir.TypeI32,
				Global: key,
				Str:    "test",
				Args:   []ir.Expr{fl.useVar(ifaceV)},
			}
			if j == 0 {
				cond = cmp
			} else {
				cond = &ir.Instr{Op: ir.OpI32Or, Args: []ir.Expr{cond, cmp}}
			}
		}
		elseR, err := build(i + 1)
		if err != nil {
			return nil, err
		}
		return &ir.Seq{Stmts: []ir.Stmt{&ir.If{Cond: cond, Then: bodySeq, Else: elseR}}}, nil
	}
	// Find default and non-default order: Go evaluates in order; default last in chain.
	var cases []*ast.CaseClause
	var def *ast.CaseClause
	for _, st := range s.Body.List {
		cc := st.(*ast.CaseClause)
		if len(cc.List) == 0 {
			def = cc
		} else {
			cases = append(cases, cc)
		}
	}
	ordered := append([]*ast.CaseClause{}, cases...)
	if def != nil {
		ordered = append(ordered, def)
	}
	orig := s.Body.List
	s.Body.List = make([]ast.Stmt, len(ordered))
	for i, c := range ordered {
		s.Body.List[i] = c
	}
	r, err := build(0)
	s.Body.List = orig
	if err != nil {
		return nil, err
	}
	out = append(out, &ir.Block{Name: breakL, Body: r})
	return out, nil
}

func regionEndsWithReturn(r ir.Region) bool {
	seq, ok := r.(*ir.Seq)
	if !ok || len(seq.Stmts) == 0 {
		return false
	}
	_, ok = seq.Stmts[len(seq.Stmts)-1].(*ir.Return)
	return ok
}
