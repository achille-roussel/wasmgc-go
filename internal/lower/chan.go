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
		// Box to the channel element interface when it is one (issue43292:
		// chan I must carry I-itabs so recv+method call can cast itabtype).
		// Concrete element types still travel as any and are asserted on recv.
		var dstIface types.Type = types.NewInterfaceType(nil, nil)
		if isIface(elem) {
			dstIface = elem
		}
		val, vstmts, err = fl.convertToIface(s.Value, fl.typeOf(s.Value), dstIface, typeIface)
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
	// LHS destinations before the channel operand (bug242: *f(), ok = <-e1(c, n)).
	preps, pstmts, err := fl.prepareParallelDests(s.Lhs)
	if err != nil {
		return nil, err
	}
	out = append(out, pstmts...)
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
	elemGo := chanElem(chTy)
	// Recv into a concrete element temp (not the LHS type). When LHS is
	// interface{}, expandChanRecv would otherwise treat a closed/empty
	// receive as a nil iface; Go requires a typed zero boxed into the
	// interface (typeparam/mdempsky/18).
	valTy, err := fl.mapType(elemGo)
	if err != nil || valTy == nil {
		valTy, err = fl.lhsType(s.Lhs[0])
		if err != nil {
			return nil, err
		}
	}
	ex := &ir.Instr{
		Op:   ir.OpChanRecv,
		Type: valTy,
		Str:  "ok",
		Args: []ir.Expr{ch},
	}
	if elemGo != nil {
		ex.Global = desc.TypeKey(elemGo)
		ex.Field = boxKind(elemGo)
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
	v0, err := fl.maybeBoxExpr(fl.useTemp(vt), elemGo, s.Lhs[0])
	if err != nil {
		return nil, err
	}
	v1, err := fl.maybeBoxExpr(fl.useTemp(ot), types.Typ[types.Bool], s.Lhs[1])
	if err != nil {
		return nil, err
	}
	as0, err := fl.commitParallelDest(preps[0], v0)
	if err != nil {
		return nil, err
	}
	as1, err := fl.commitParallelDest(preps[1], v1)
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
				if u, ok := ast.Unparen(c.X).(*ast.UnaryExpr); ok && u.Op == token.ARROW {
					meta.kind = 0
					meta.recvExpr = u
				} else {
					return nil, fl.errorf(c, "unsupported select case")
				}
			case *ast.AssignStmt:
				if len(c.Rhs) == 1 {
					// Accept parenthesized receives: case x := (<-ch):
					if u, ok := ast.Unparen(c.Rhs[0]).(*ast.UnaryExpr); ok && u.Op == token.ARROW {
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
								// Spill to a cell so each case execution can
								// allocate a fresh one (loop &x; issue48289).
								obj := fl.info.Defs[id]
								if obj == nil {
									continue
								}
								v := fl.vars[obj]
								if v == nil {
									continue
								}
								cellName, err := fl.pkg.ensureCellType(v.Typ)
								if err != nil {
									return nil, err
								}
								if err := fl.ensureLocalCell(obj, cellName, v.Typ); err != nil {
									return nil, err
								}
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
			elemGo := chanElem(chTy)
			var dstIface types.Type = types.NewInterfaceType(nil, nil)
			if elemGo != nil && isIface(elemGo) {
				dstIface = elemGo
			}
			ev, estmts, err := fl.convertToIface(meta.send.Value, fl.typeOf(meta.send.Value), dstIface, typeIface)
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
		// Fresh cells for DEFINE recv vars each time this case runs so
		// &x across loop iterations does not alias (issue48289).
		if meta.kind == 0 && meta.assign != nil && meta.assign.Tok == token.DEFINE {
			for _, lhs := range meta.assign.Lhs {
				id, ok := lhs.(*ast.Ident)
				if !ok || id.Name == "_" {
					continue
				}
				obj := fl.info.Defs[id]
				if obj == nil || fl.cells == nil {
					continue
				}
				cv, ok := fl.cells[obj]
				if !ok {
					continue
				}
				cellTy, _ := cv.Typ.(*ir.Ref)
				if cellTy == nil {
					continue
				}
				z, err := fl.zeroValue(fl.vars[obj].Typ)
				if err != nil {
					return nil, err
				}
				body.Stmts = append(body.Stmts, fl.assignVar(cv, &ir.Instr{
					Op:   ir.OpStructNew,
					Type: &ir.Ref{Named: cellTy.Named},
					Args: []ir.Expr{z},
				}))
			}
		}
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
				elemGo := chanElem(fl.typeOf(meta.recvExpr.X))
				dstGo := fl.typeOf(lhs0)
				switch {
				case elemGo != nil && isIface(elemGo):
					// Chan of interface: selectGo already returns the element
					// iface. Do not concrete-assert to the iface type name
					// (powser2: chan item → it = <-dat[i] was "ptr, not item").
					valTy, err := fl.lhsType(lhs0)
					if err != nil {
						return nil, err
					}
					z, err := fl.zeroValue(valTy)
					if err != nil {
						return nil, err
					}
					as0, err := fl.assignTo(lhs0, z)
					if err != nil {
						return nil, err
					}
					body.Stmts = append(body.Stmts, as0...)
					var got ir.Expr = fl.useVar(recvIface)
					if dstGo != nil && isIface(dstGo) && !types.Identical(types.Unalias(elemGo), types.Unalias(dstGo)) {
						got = &ir.Instr{
							Op:     ir.OpIfaceAssert,
							Type:   valTy,
							Global: desc.TypeKey(dstGo),
							Str:    "iface",
							Args:   []ir.Expr{fl.useVar(recvIface)},
						}
					}
					asOK, err := fl.assignTo(lhs0, got)
					if err != nil {
						return nil, err
					}
					body.Stmts = append(body.Stmts, &ir.If{
						Cond: fl.useVar(recvOK),
						Then: &ir.Seq{Stmts: asOK},
						Else: &ir.Seq{},
					})
				case dstGo != nil && isIface(dstGo) && elemGo != nil && !isIface(elemGo):
					// Interface LHS, concrete chan elem: selectGo returns nil
					// iface on closed recv, but Go wants a typed zero. Start
					// with boxed zero; overwrite with the runtime iface when
					// the receive succeeded.
					elemIR, err := fl.mapType(elemGo)
					if err != nil {
						return nil, err
					}
					z, err := fl.zeroValue(elemIR)
					if err != nil {
						return nil, err
					}
					as0, err := fl.assignTo(lhs0, fl.boxIRToIface(z, elemGo, dstGo))
					if err != nil {
						return nil, err
					}
					body.Stmts = append(body.Stmts, as0...)
					asOK, err := fl.assignTo(lhs0, fl.useVar(recvIface))
					if err != nil {
						return nil, err
					}
					body.Stmts = append(body.Stmts, &ir.If{
						Cond: fl.useVar(recvOK),
						Then: &ir.Seq{Stmts: asOK},
						Else: &ir.Seq{},
					})
				default:
					// Concrete LHS: closed recv may yield a nil iface
					// (defaultChanOps.zero). Match expandChanRecv — assign
					// element zero, then iface-assert only when recvOK.
					valTy, err := fl.lhsType(lhs0)
					if err != nil {
						return nil, err
					}
					z, err := fl.zeroValue(valTy)
					if err != nil {
						return nil, err
					}
					as0, err := fl.assignTo(lhs0, z)
					if err != nil {
						return nil, err
					}
					body.Stmts = append(body.Stmts, as0...)
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
					asOK, err := fl.assignTo(lhs0, assert)
					if err != nil {
						return nil, err
					}
					body.Stmts = append(body.Stmts, &ir.If{
						Cond: fl.useVar(recvOK),
						Then: &ir.Seq{Stmts: asOK},
						Else: &ir.Seq{},
					})
				}
			}
			if meta.recvOk {
				as1, err := fl.assignToMaybeBox(meta.assign.Lhs[1], fl.useVar(recvOK), types.Typ[types.Bool])
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
