package lower

import (
	"go/ast"
	"go/token"
	"go/types"

	"github.com/achille-roussel/wasmgc-go/internal/desc"
	"github.com/achille-roussel/wasmgc-go/internal/ir"
)

// sendStmt lowers ch <- v to OpChanSend.
func (fl *funcLower) sendStmt(s *ast.SendStmt) ([]ir.Stmt, error) {
	fl.pkg.ensureChanBaseTypes()
	if fl.pkg.meta != nil {
		fl.pkg.meta.needsSched = true
	}
	chTy := fl.typeOf(s.Chan)
	chIR, err := fl.mapType(chTy)
	if err != nil {
		return nil, fl.errorf(s.Chan, "%v", err)
	}
	ch, stmts, err := fl.expr(s.Chan, chIR)
	if err != nil {
		return nil, err
	}
	elem := chanElem(chTy)
	var val ir.Expr
	var vstmts []ir.Stmt
	if elem != nil {
		emptyIface := types.NewInterfaceType(nil, nil)
		val, vstmts, err = fl.convertToIface(s.Value, fl.typeOf(s.Value), emptyIface, typeIface)
	} else {
		val, vstmts, err = fl.expr(s.Value, typeIface)
	}
	if err != nil {
		return nil, err
	}
	stmts = append(stmts, vstmts...)
	var elemIR ir.Type = typeIface
	if elem != nil {
		if t, err := fl.mapType(elem); err == nil {
			elemIR = t
		}
	}
	stmts = append(stmts, &ir.ExprStmt{X: &ir.Instr{
		Op:   ir.OpChanSend,
		Type: elemIR,
		Args: []ir.Expr{ch, val},
	}})
	return stmts, nil
}

// chanRecvExpr lowers <-ch (single value).
func (fl *funcLower) chanRecvExpr(e *ast.UnaryExpr, want ir.Type) (ir.Expr, []ir.Stmt, error) {
	fl.pkg.ensureChanBaseTypes()
	if fl.pkg.meta != nil {
		fl.pkg.meta.needsSched = true
	}
	chTy := fl.typeOf(e.X)
	chIR, err := fl.mapType(chTy)
	if err != nil {
		return nil, nil, fl.errorf(e.X, "%v", err)
	}
	ch, stmts, err := fl.expr(e.X, chIR)
	if err != nil {
		return nil, nil, err
	}
	elem := chanElem(chTy)
	resTy := want
	if resTy == nil && elem != nil {
		resTy, _ = fl.mapType(elem)
	}
	if resTy == nil {
		resTy = typeIface
	}
	in := &ir.Instr{
		Op:   ir.OpChanRecv,
		Type: resTy,
		Args: []ir.Expr{ch},
	}
	if elem != nil {
		in.Global = desc.TypeKey(elem)
		in.Field = boxKind(elem)
	}
	return in, stmts, nil
}

// chanRecvStmt lowers bare `<-ch` (discard).
func (fl *funcLower) chanRecvStmt(e *ast.UnaryExpr) ([]ir.Stmt, error) {
	fl.pkg.ensureChanBaseTypes()
	if fl.pkg.meta != nil {
		fl.pkg.meta.needsSched = true
	}
	chTy := fl.typeOf(e.X)
	chIR, err := fl.mapType(chTy)
	if err != nil {
		return nil, fl.errorf(e.X, "%v", err)
	}
	ch, stmts, err := fl.expr(e.X, chIR)
	if err != nil {
		return nil, err
	}
	// ExprStmt OpChanRecv (no Lhs) → expandChanRecv discards without unbox.
	stmts = append(stmts, &ir.ExprStmt{X: &ir.Instr{
		Op:   ir.OpChanRecv,
		Args: []ir.Expr{ch},
	}})
	return stmts, nil
}

// multiAssignRecv lowers v, ok := <-ch.
func (fl *funcLower) multiAssignRecv(s *ast.AssignStmt, recv *ast.UnaryExpr, out []ir.Stmt) ([]ir.Stmt, error) {
	if len(s.Lhs) != 2 {
		return nil, fl.errorf(s, "channel receive comma-ok wants 2 LHS")
	}
	fl.pkg.ensureChanBaseTypes()
	if fl.pkg.meta != nil {
		fl.pkg.meta.needsSched = true
	}
	chTy := fl.typeOf(recv.X)
	chIR, err := fl.mapType(chTy)
	if err != nil {
		return nil, fl.errorf(recv.X, "%v", err)
	}
	ch, stmts, err := fl.expr(recv.X, chIR)
	if err != nil {
		return nil, err
	}
	out = append(out, stmts...)
	valTy, err := fl.lhsType(s.Lhs[0])
	if err != nil {
		return nil, err
	}
	ex := &ir.Instr{
		Op:   ir.OpChanRecv,
		Type: valTy,
		Str:  "ok",
		Args: []ir.Expr{ch},
	}
	if elem := chanElem(chTy); elem != nil {
		ex.Global = desc.TypeKey(elem)
		ex.Field = boxKind(elem)
	}
	vt := fl.newTemp(valTy)
	ot := fl.newTemp(ir.TypeI32)
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

// selectStmt rewrites select into building []scase and calling runtime.selectGo.
func (fl *funcLower) selectStmt(s *ast.SelectStmt) ([]ir.Stmt, error) {
	fl.pkg.ensureChanBaseTypes()
	if fl.pkg.meta != nil {
		fl.pkg.meta.needsSched = true
	}
	if s.Body == nil {
		return nil, fl.errorf(s, "empty select")
	}
	n := len(s.Body.List)
	scaseTy := &ir.Ref{Null: true, Named: "$go.scase"}
	arrName := "$go.array.go.scase"
	fl.mod.InternType(&ir.TypeDef{
		Name:    arrName,
		Kind:    ir.TypeArray,
		Elem:    scaseTy,
		ElemMut: true,
	})
	sliceName := "$go.slice.go.scase"
	fl.mod.InternType(&ir.TypeDef{
		Name:  sliceName,
		Kind:  ir.TypeStruct,
		Super: "$go.object",
		Fields: []ir.Field{
			{Name: "data", Type: &ir.Ref{Named: arrName}},
			{Name: "off", Type: ir.TypeI32},
			{Name: "len", Type: ir.TypeI32},
			{Name: "cap", Type: ir.TypeI32},
		},
	})
	sliceTy := &ir.Ref{Null: true, Named: sliceName}

	var out []ir.Stmt
	// cases = make([]scase, n)
	cases := fl.newVar("scases", sliceTy)
	out = append(out, fl.assignVar(cases, &ir.Instr{
		Op:   ir.OpMakeSlice,
		Type: sliceTy,
		Args: []ir.Expr{fl.i32const(int64(n)), fl.i32const(int64(n))},
	}))
	opsCall := &ir.Instr{Op: ir.OpCall, Func: "runtime.defaultChanOps"}
	opsT := fl.newTemp(&ir.Ref{Null: true, Named: "$go.chanOps"})
	out = append(out, fl.assignTemp(opsT, opsCall))

	type selCase struct {
		kind     int32 // 0=recv 1=send 2=default
		clause   *ast.CommClause
		recvOk   bool // lhs has ok
		assign   *ast.AssignStmt
		recvExpr *ast.UnaryExpr
		send     *ast.SendStmt
	}
	var casesMeta []selCase
	for i, stmt := range s.Body.List {
		_ = i
		cc := stmt.(*ast.CommClause)
		meta := selCase{clause: cc, kind: 2}
		if cc.Comm == nil {
			meta.kind = 2
		} else {
			switch c := cc.Comm.(type) {
			case *ast.SendStmt:
				meta.kind = 1
				meta.send = c
			case *ast.ExprStmt:
				if u, ok := c.X.(*ast.UnaryExpr); ok && u.Op == token.ARROW {
					meta.kind = 0
					meta.recvExpr = u
				} else {
					return nil, fl.errorf(c, "unsupported select case")
				}
			case *ast.AssignStmt:
				if len(c.Rhs) == 1 {
					if u, ok := c.Rhs[0].(*ast.UnaryExpr); ok && u.Op == token.ARROW {
						meta.kind = 0
						meta.recvExpr = u
						meta.assign = c
						meta.recvOk = len(c.Lhs) == 2
						if c.Tok == token.DEFINE {
							for _, lhs := range c.Lhs {
								id, ok := lhs.(*ast.Ident)
								if !ok || id.Name == "_" {
									continue
								}
								_, defs, err := fl.defineLocal(id)
								if err != nil {
									return nil, err
								}
								out = append(out, defs...)
							}
						}
					} else {
						return nil, fl.errorf(c, "unsupported select assign")
					}
				} else {
					return nil, fl.errorf(c, "unsupported select assign")
				}
			default:
				return nil, fl.errorf(cc.Comm, "unsupported select comm %T", cc.Comm)
			}
		}
		casesMeta = append(casesMeta, meta)

		// Build scase struct and store into slice.
		var kindEx ir.Expr = fl.i32const(int64(meta.kind))
		var chEx ir.Expr = &ir.Instr{Op: ir.OpRefNull, Type: &ir.Ref{Null: true, Named: "$go.chan"}}
		var elemEx ir.Expr = &ir.Instr{Op: ir.OpRefNull, Type: typeIface}
		switch meta.kind {
		case 1: // send
			chTy := fl.typeOf(meta.send.Chan)
			chIR, err := fl.mapType(chTy)
			if err != nil {
				return nil, err
			}
			ch, stmts, err := fl.expr(meta.send.Chan, chIR)
			if err != nil {
				return nil, err
			}
			out = append(out, stmts...)
			chEx = ch
			emptyIface := types.NewInterfaceType(nil, nil)
			ev, estmts, err := fl.convertToIface(meta.send.Value, fl.typeOf(meta.send.Value), emptyIface, typeIface)
			if err != nil {
				return nil, err
			}
			out = append(out, estmts...)
			elemEx = ev
		case 0: // recv
			chTy := fl.typeOf(meta.recvExpr.X)
			chIR, err := fl.mapType(chTy)
			if err != nil {
				return nil, err
			}
			ch, stmts, err := fl.expr(meta.recvExpr.X, chIR)
			if err != nil {
				return nil, err
			}
			out = append(out, stmts...)
			chEx = ch
		}
		sc := &ir.Instr{
			Op:   ir.OpStructNew,
			Type: &ir.Ref{Named: "$go.scase"},
			Args: []ir.Expr{
				kindEx,
				chEx,
				fl.useTemp(opsT),
				elemEx,
				fl.i32const(0), // ok
			},
		}
		out = append(out, &ir.ExprStmt{X: &ir.Instr{
			Op:   ir.OpSliceSet,
			Args: []ir.Expr{fl.useVar(cases), fl.i32const(int64(i)), sc},
		}})
	}

	// chosen, recv, recvOK := selectGo(cases)
	// Recv payload is returned directly: mutating cases[i].elem across a park
	// is not reliably visible to the caller (struct value / slice slot).
	chosen := fl.newVar("chosen", ir.TypeI32)
	recvIface := fl.newVar("selRecv", typeIface)
	recvOK := fl.newVar("selOK", ir.TypeI32)
	callSel := &ir.Instr{
		Op:   ir.OpCall,
		Func: "runtime.selectGo",
		Args: []ir.Expr{fl.useVar(cases)},
	}
	out = append(out, &ir.Assign{
		Lhs: []ir.Dest{
			{Var: chosen, Name: chosen.Name},
			{Var: recvIface, Name: recvIface.Name},
			{Var: recvOK, Name: recvOK.Name},
		},
		Rhs: callSel,
	})

	// Dispatch on chosen index.
	breakL := fl.freshLabel("sel")
	fl.breakStack = append(fl.breakStack, breakL)
	defer func() { fl.breakStack = fl.breakStack[:len(fl.breakStack)-1] }()

	var build func(i int) (ir.Region, error)
	build = func(i int) (ir.Region, error) {
		if i >= len(casesMeta) {
			return &ir.Seq{}, nil
		}
		meta := casesMeta[i]
		body := &ir.Seq{}
		// Bind recv results from selectGo's returned iface/ok when assign form.
		if meta.kind == 0 && meta.assign != nil {
			lhs0 := meta.assign.Lhs[0]
			if id, ok := lhs0.(*ast.Ident); ok && id.Name == "_" {
				// Discard; do not iface-assert a possibly-unused recv.
				body.Stmts = append(body.Stmts, &ir.ExprStmt{X: &ir.Instr{
					Op:   ir.OpDrop,
					Args: []ir.Expr{fl.useVar(recvIface)},
				}})
			} else {
				valTy, err := fl.lhsType(lhs0)
				if err != nil {
					return nil, err
				}
				elemGo := chanElem(fl.typeOf(meta.recvExpr.X))
				assert := &ir.Instr{
					Op:    ir.OpIfaceAssert,
					Type:  valTy,
					Field: boxKind(elemGo),
					Str:   "concrete",
					Args:  []ir.Expr{fl.useVar(recvIface)},
				}
				if elemGo != nil {
					assert.Global = desc.TypeKey(elemGo)
				}
				as0, err := fl.assignTo(lhs0, assert)
				if err != nil {
					return nil, err
				}
				body.Stmts = append(body.Stmts, as0...)
			}
			if meta.recvOk {
				as1, err := fl.assignTo(meta.assign.Lhs[1], fl.useVar(recvOK))
				if err != nil {
					return nil, err
				}
				body.Stmts = append(body.Stmts, as1...)
			}
		}
		for _, st := range meta.clause.Body {
			ss, err := fl.stmt(st)
			if err != nil {
				return nil, err
			}
			body.Stmts = append(body.Stmts, ss...)
		}
		bodyR := appendBr(body, breakL)
		elseR, err := build(i + 1)
		if err != nil {
			return nil, err
		}
		cond := &ir.Instr{Op: ir.OpI32Eq, Args: []ir.Expr{fl.useVar(chosen), fl.i32const(int64(i))}}
		return &ir.Seq{Stmts: []ir.Stmt{&ir.If{Cond: cond, Then: bodyR, Else: elseR}}}, nil
	}
	r, err := build(0)
	if err != nil {
		return nil, err
	}
	out = append(out, &ir.Block{Name: breakL, Body: r})
	return out, nil
}

func chanElem(t types.Type) types.Type {
	if t == nil {
		return nil
	}
	c, ok := types.Unalias(t).Underlying().(*types.Chan)
	if !ok {
		return nil
	}
	return c.Elem()
}

func isChan(t types.Type) bool {
	if t == nil {
		return false
	}
	_, ok := types.Unalias(t).Underlying().(*types.Chan)
	return ok
}
