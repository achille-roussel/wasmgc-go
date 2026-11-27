package lower

import (
	"errors"
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"github.com/achille-roussel/wasmgc-go/internal/desc"
	"github.com/achille-roussel/wasmgc-go/internal/ir"
)

var errShiftOpAssignOnce = errors.New("shift op-assign once not used")

func (fl *funcLower) stmtBlock(b *ast.BlockStmt) (ir.Region, error) {
	if b == nil {
		return &ir.Seq{}, nil
	}
	if blockHasForwardGoto(b) {
		return fl.stmtBlockForwardGoto(b)
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

func blockHasForwardGoto(b *ast.BlockStmt) bool {
	var walk func(ast.Stmt) bool
	walk = func(s ast.Stmt) bool {
		if s == nil {
			return false
		}
		switch s := s.(type) {
		case *ast.BranchStmt:
			return s.Tok == token.GOTO && s.Label != nil
		case *ast.BlockStmt:
			for _, c := range s.List {
				if walk(c) {
					return true
				}
			}
		case *ast.IfStmt:
			return walk(s.Body) || walk(s.Else)
		case *ast.ForStmt:
			return walk(s.Body)
		case *ast.RangeStmt:
			return walk(s.Body)
		case *ast.SwitchStmt:
			for _, c := range s.Body.List {
				if walk(c) {
					return true
				}
			}
		case *ast.TypeSwitchStmt:
			for _, c := range s.Body.List {
				if walk(c) {
					return true
				}
			}
		case *ast.CaseClause:
			for _, c := range s.Body {
				if walk(c) {
					return true
				}
			}
		case *ast.SelectStmt:
			for _, c := range s.Body.List {
				if walk(c) {
					return true
				}
			}
		case *ast.CommClause:
			for _, c := range s.Body {
				if walk(c) {
					return true
				}
			}
		case *ast.LabeledStmt:
			return walk(s.Stmt)
		}
		return false
	}
	for _, s := range b.List {
		if walk(s) {
			return true
		}
	}
	return false
}

type gotoSeg struct {
	label string
	stmts []ast.Stmt
}

// stmtBlockForwardGoto lowers a block that contains goto to statement labels
// (docs/design/02). Forward-only gotos use nested blocks. Any backward goto
// uses a Dispatch state machine so irreducible CFGs (ken/label) validate.
func (fl *funcLower) stmtBlockForwardGoto(b *ast.BlockStmt) (ir.Region, error) {
	var segs []gotoSeg
	cur := gotoSeg{}
	for _, s := range b.List {
		if ls, ok := s.(*ast.LabeledStmt); ok {
			segs = append(segs, cur)
			cur = gotoSeg{label: ls.Label.Name, stmts: []ast.Stmt{ls.Stmt}}
			continue
		}
		cur.stmts = append(cur.stmts, s)
	}
	segs = append(segs, cur)

	labelSeg := map[string]int{}
	for i, sg := range segs {
		if sg.label != "" {
			labelSeg[sg.label] = i
		}
	}
	hasBackward := false
	var walkGoto func(ast.Stmt, int)
	walkGoto = func(s ast.Stmt, segIdx int) {
		if s == nil {
			return
		}
		switch s := s.(type) {
		case *ast.BranchStmt:
			if s.Tok == token.GOTO && s.Label != nil {
				if li, ok := labelSeg[s.Label.Name]; ok && segIdx >= li {
					hasBackward = true
				}
			}
		case *ast.BlockStmt:
			for _, c := range s.List {
				walkGoto(c, segIdx)
			}
		case *ast.IfStmt:
			walkGoto(s.Body, segIdx)
			walkGoto(s.Else, segIdx)
		case *ast.ForStmt:
			walkGoto(s.Body, segIdx)
		case *ast.RangeStmt:
			walkGoto(s.Body, segIdx)
		case *ast.SwitchStmt:
			for _, c := range s.Body.List {
				walkGoto(c, segIdx)
			}
		case *ast.TypeSwitchStmt:
			for _, c := range s.Body.List {
				walkGoto(c, segIdx)
			}
		case *ast.CaseClause:
			for _, c := range s.Body {
				walkGoto(c, segIdx)
			}
		case *ast.SelectStmt:
			for _, c := range s.Body.List {
				walkGoto(c, segIdx)
			}
		case *ast.CommClause:
			for _, c := range s.Body {
				walkGoto(c, segIdx)
			}
		case *ast.LabeledStmt:
			walkGoto(s.Stmt, segIdx)
		}
	}
	for i, sg := range segs {
		for _, s := range sg.stmts {
			walkGoto(s, i)
		}
	}

	if hasBackward {
		return fl.stmtBlockGotoDispatch(segs)
	}
	return fl.stmtBlockGotoForward(segs)
}

func (fl *funcLower) lowerGotoSegBody(sg gotoSeg) ([]ir.Stmt, error) {
	var body []ir.Stmt
	for j, s := range sg.stmts {
		if j == 0 && sg.label != "" {
			switch s.(type) {
			case *ast.ForStmt, *ast.RangeStmt, *ast.SwitchStmt:
				st, err := fl.labeledStmt(&ast.LabeledStmt{
					Label: &ast.Ident{Name: sg.label, NamePos: s.Pos()},
					Stmt:  s,
				})
				if err != nil {
					return nil, err
				}
				body = append(body, st...)
				continue
			}
		}
		st, err := fl.stmt(s)
		if err != nil {
			return nil, err
		}
		body = append(body, st...)
	}
	return body, nil
}

// stmtBlockGotoForward: nested blocks for forward-only goto (docs/design/02 §1).
func (fl *funcLower) stmtBlockGotoForward(segs []gotoSeg) (ir.Region, error) {
	var registered []string
	for _, sg := range segs {
		if sg.label == "" {
			continue
		}
		lab := "$goto." + sg.label
		fl.labels[sg.label] = &labelTargets{gotoLabel: lab, gotoArm: -1}
		registered = append(registered, sg.label)
	}
	defer func() {
		for _, name := range registered {
			delete(fl.labels, name)
		}
	}()

	var stmts []ir.Stmt
	for _, sg := range segs {
		if sg.label != "" {
			lab := fl.labels[sg.label].gotoLabel
			stmts = []ir.Stmt{&ir.Block{Name: lab, Body: &ir.Seq{Stmts: stmts}}}
		}
		body, err := fl.lowerGotoSegBody(sg)
		if err != nil {
			return nil, err
		}
		stmts = append(stmts, body...)
	}
	return &ir.Seq{Stmts: stmts}, nil
}

// stmtBlockGotoDispatch: state-machine for any backward/irreducible goto.
func (fl *funcLower) stmtBlockGotoDispatch(segs []gotoSeg) (ir.Region, error) {
	state := fl.newVar("goto.st", ir.TypeI32)
	loopName := fl.freshLabel("goto.d")
	arms := map[string]int{}
	var registered []string
	for i, sg := range segs {
		if sg.label != "" {
			arms[sg.label] = i
			fl.labels[sg.label] = &labelTargets{
				gotoLabel: loopName,
				gotoArm:   i,
			}
			registered = append(registered, sg.label)
		}
	}
	defer func() {
		for _, name := range registered {
			delete(fl.labels, name)
		}
	}()

	prev := fl.gotoDispatch
	fl.gotoDispatch = &gotoDispatchState{Loop: loopName, State: state, Arms: arms}
	defer func() { fl.gotoDispatch = prev }()

	irArms := make([]ir.Region, len(segs))
	for i, sg := range segs {
		body, err := fl.lowerGotoSegBody(sg)
		if err != nil {
			return nil, err
		}
		// Fall through to the next arm unless the segment ends in return/goto.
		if i+1 < len(segs) && !segEndsWithTransfer(sg.stmts) {
			body = append(body,
				fl.assignVar(state, fl.i32const(int64(i+1))),
				&ir.Br{Label: loopName},
			)
		}
		irArms[i] = &ir.Seq{Stmts: body}
	}

	return &ir.Seq{Stmts: []ir.Stmt{
		fl.assignVar(state, fl.i32const(0)),
		&ir.Dispatch{Name: loopName, State: state, Arms: irArms},
	}}, nil
}

func segEndsWithTransfer(stmts []ast.Stmt) bool {
	if len(stmts) == 0 {
		return false
	}
	switch s := stmts[len(stmts)-1].(type) {
	case *ast.ReturnStmt:
		return true
	case *ast.BranchStmt:
		return s.Tok == token.GOTO || s.Tok == token.BREAK || s.Tok == token.CONTINUE
	case *ast.BlockStmt:
		return segEndsWithTransfer(s.List)
	case *ast.LabeledStmt:
		return segEndsWithTransfer([]ast.Stmt{s.Stmt})
	}
	return false
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
	if !ok {
		return nil, fl.errorf(s, "only var/const/type declarations supported in functions")
	}
	// Local consts/types are compile-time; Idents resolve via types.Info.
	if gd.Tok == token.CONST || gd.Tok == token.TYPE {
		return nil, nil
	}
	if gd.Tok != token.VAR {
		return nil, fl.errorf(s, "only var/const/type declarations supported in functions")
	}
	var out []ir.Stmt
	for _, sp := range gd.Specs {
		vs := sp.(*ast.ValueSpec)
		// var a, b = f() / var v, ok = m[k] — one RHS, multiple LHS.
		if len(vs.Names) > 1 && len(vs.Values) == 1 {
			stmts, err := fl.valueSpecMultiInit(vs)
			if err != nil {
				return nil, err
			}
			out = append(out, stmts...)
			continue
		}
		for i, name := range vs.Names {
			if name.Name == "_" {
				continue
			}
			v, defs, err := fl.defineLocal(name)
			if err != nil {
				return nil, err
			}
			out = append(out, defs...)
			if v == nil {
				continue
			}
			obj := fl.info.Defs[name]
			if vs.Values != nil && i < len(vs.Values) {
				ex, stmts, err := fl.coerceExpr(vs.Values[i], obj.Type(), v.Typ)
				if err != nil {
					return nil, err
				}
				out = append(out, stmts...)
				out = append(out, fl.assignDeclValue(obj, v, ex)...)
			} else if z, ok := fl.goZeroValue(obj.Type(), v.Typ); ok {
				// Arrays / named structs: Wasm refnull default is null, but
				// Go zero is an allocated empty value (e.g. var arr [32]byte).
				out = append(out, fl.assignDeclValue(obj, v, z)...)
			} else if fl.deferInLoop {
				// Inside a loop, `var x` must reset each iteration — including
				// when x is later cell-captured by range-over-func (Collect).
				// Outside loops, defineLocal's zero-init is enough; re-zeroing
				// every DeclStmt broke encoding/json string fields.
				z, err := fl.declZero(obj.Type(), v.Typ)
				if err != nil {
					return nil, fl.errorf(name, "zero init: %v", err)
				}
				out = append(out, fl.assignDeclValue(obj, v, z)...)
			}
			// else: defineLocal already zero-inits non-defaultable refs.
		}
	}
	return out, nil
}

// valueSpecMultiInit lowers `var a, b = f()` / `var v, ok = m[k]` inside a function.
func (fl *funcLower) valueSpecMultiInit(vs *ast.ValueSpec) ([]ir.Stmt, error) {
	var out []ir.Stmt
	lhs := make([]ast.Expr, len(vs.Names))
	for i, name := range vs.Names {
		lhs[i] = name
		if name.Name == "_" {
			continue
		}
		_, defs, err := fl.defineLocal(name)
		if err != nil {
			return nil, err
		}
		out = append(out, defs...)
	}
	fake := &ast.AssignStmt{Tok: token.ASSIGN, Lhs: lhs, Rhs: vs.Values}
	return fl.multiAssignAppend(fake, out)
}

// multiAssignAppend is multiAssign without DEFINE handling (locals already defined).
func (fl *funcLower) multiAssignAppend(s *ast.AssignStmt, out []ir.Stmt) ([]ir.Stmt, error) {
	if ta, ok := s.Rhs[0].(*ast.TypeAssertExpr); ok {
		return fl.multiAssignTypeAssert(s, ta, out)
	}
	if u, ok := s.Rhs[0].(*ast.UnaryExpr); ok && u.Op == token.ARROW {
		return fl.multiAssignRecv(s, u, out)
	}
	if ix, ok := s.Rhs[0].(*ast.IndexExpr); ok && isMap(fl.typeOf(ix.X)) {
		return fl.mapIndexCommaOk(s, ix, out)
	}
	call, ok := s.Rhs[0].(*ast.CallExpr)
	if !ok {
		return nil, fl.errorf(s, "multi-value assignment requires a call, type assert, or channel receive")
	}
	// Evaluate LHS destinations before the RHS call (Go assignment order:
	// m[f()], *g() = Atoi(h()) must call f,g before h — bug262).
	nPrep := len(s.Lhs)
	preps, pstmts, err := fl.prepareParallelDests(s.Lhs)
	if err != nil {
		return nil, err
	}
	out = append(out, pstmts...)
	// Materialize with callee result types (nil), not LHS types — blank `_`
	// reports i32 via lhsType and would corrupt non-int discarded results.
	temps, stmts, err := fl.callMaterialize(call, nil)
	if err != nil {
		return nil, err
	}
	out = append(out, stmts...)
	srcGos := fl.callResultGoTypes(call)
	n := len(temps)
	if n > nPrep {
		n = nPrep
	}
	for i := 0; i < n; i++ {
		var ex ir.Expr = fl.useTemp(temps[i])
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
		var srcGo types.Type
		if i < len(srcGos) {
			srcGo = srcGos[i]
		}
		if fl.pkg != nil && len(fl.pkg.subst) > 0 {
			if dstGo != nil {
				dstGo = fl.pkg.subst.Apply(dstGo)
			}
			if srcGo != nil {
				srcGo = fl.pkg.subst.Apply(srcGo)
			}
		}
		if dstGo != nil && isIface(dstGo) && !isTypeParam(dstGo) &&
			srcGo != nil && !isIface(srcGo) && !isTypeParam(srcGo) {
			ex = fl.boxIRToIface(ex, srcGo, dstGo)
		}
		as, err := fl.commitParallelDest(preps[i], ex)
		if err != nil {
			return nil, err
		}
		out = append(out, as...)
	}
	return out, nil
}

// assignDeclValue writes rhs for a var declaration. If the local is already
// cell-boxed, update the cell; otherwise write the plain local and mark it
// dirty so a later closure/range-func capture syncs plain→cell (needed when
// `var x` is lowered before the capture site that calls ensureLocalCell).
func (fl *funcLower) assignDeclValue(obj types.Object, v *ir.Var, rhs ir.Expr) []ir.Stmt {
	if fl.cells != nil {
		if _, ok := fl.cells[obj]; ok {
			ws, err := fl.writeVar(obj, rhs)
			if err == nil {
				return ws
			}
		}
	}
	fl.markPlainDirty(obj)
	return []ir.Stmt{fl.assignVar(v, rhs)}
}

// declZero is the Go zero for a DeclStmt. Unlike zeroValue alone, pointer /
// slice / map / chan / iface / func zeros are typed null — named structs and
// their pointers share one IR shape, so zeroValue would allocate a value.
func (fl *funcLower) declZero(goTy types.Type, irTy ir.Type) (ir.Expr, error) {
	if goTy != nil && fl.pkg != nil && len(fl.pkg.subst) > 0 {
		goTy = fl.pkg.subst.Apply(goTy)
	}
	if z, ok := fl.goZeroValue(goTy, irTy); ok {
		return z, nil
	}
	// Type params' constraints are interfaces; never treat an unsubstituted
	// param as iface (cmp.Or[string]'s `var zero T` must be "").
	if _, isTP := types.Unalias(goTy).(*types.TypeParam); !isTP {
		if isPointer(goTy) || isSlice(goTy) || isMap(goTy) || isChan(goTy) || isIface(goTy) || isSignature(goTy) {
			return &ir.Instr{Op: ir.OpRefNull, Type: irTy}, nil
		}
	}
	return fl.zeroValue(irTy)
}

func needsZeroInit(ty ir.Type) bool {
	r, ok := ty.(*ir.Ref)
	if !ok {
		return false
	}
	// Non-nullable refs have no Wasm default; initialize to the Go zero value.
	return !r.Null && r.Heap == 0 && r.Named != ""
}

// isNamedStructRef reports a user named-struct IR type ($go.struct.*).
// These are lowered as refnull but still need a heap zero object for `var S`.
func isNamedStructRef(ty ir.Type) bool {
	r, ok := ty.(*ir.Ref)
	return ok && strings.HasPrefix(r.Named, "$go.struct.")
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
	// Parallel assignment (a, b = b, a): evaluate LHS destinations, then every
	// RHS, then write (Go order — bug242: *f(), *f() = g(), g()). Pairwise
	// assign would also corrupt swaps used by slices.Sort*.
	parallel := len(s.Lhs) > 1
	type rhsSlot struct {
		expr ir.Expr
		tmp  *ir.Temp
	}
	var slots []rhsSlot
	var preps []parallelDest
	if parallel {
		slots = make([]rhsSlot, len(s.Lhs))
		var pstmts []ir.Stmt
		var err error
		preps, pstmts, err = fl.prepareParallelDests(s.Lhs)
		if err != nil {
			return nil, err
		}
		out = append(out, pstmts...)
	}
	for i := range s.Lhs {
		want, err := fl.lhsType(s.Lhs[i])
		if err != nil {
			return nil, err
		}
		var rhs ir.Expr
		if s.Tok != token.ASSIGN && s.Tok != token.DEFINE {
			// *p / x[i] / m[k] op= y: evaluate the destination before y
			// (issue 21687 — y must not be able to redirect the store).
			if as, ok, err := fl.opAssignAddrOnce(s.Lhs[i], s.Tok, s.Rhs[i], want); ok || err != nil {
				if err != nil {
					return nil, err
				}
				out = append(out, as...)
				continue
			}
			lhsEx, ls, err := fl.expr(s.Lhs[i], want)
			if err != nil {
				return nil, err
			}
			out = append(out, ls...)
			if s.Tok == token.SHL_ASSIGN || s.Tok == token.SHR_ASSIGN {
				// Count keeps its own type (same as binary << / >>), then
				// extend to LHS width; Wasm masks must not apply for ≥width.
				lhsGo := fl.typeOf(s.Lhs[i])
				tok := token.SHL
				if s.Tok == token.SHR_ASSIGN {
					tok = token.SHR
				}
				yGo := fl.typeOf(s.Rhs[i])
				yWant, yerr := fl.mapType(yGo)
				if yerr != nil || yWant == nil {
					yWant = want
				}
				rhsEx, rs, err := fl.expr(s.Rhs[i], yWant)
				if err != nil {
					return nil, err
				}
				out = append(out, rs...)
				var sstmts []ir.Stmt
				rhs, sstmts, err = fl.shiftBounded(tok, lhsEx, rhsEx, want, yWant, lhsGo, yGo, nil)
				if err != nil {
					return nil, fl.errorf(s, "%v", err)
				}
				out = append(out, sstmts...)
			} else {
				lhsGo := fl.typeOf(s.Lhs[i])
				// complex64/128 op=: arithmetic on real/imag (Ref has no numeric width).
				if isComplex(lhsGo) && (s.Tok == token.ADD_ASSIGN || s.Tok == token.SUB_ASSIGN ||
					s.Tok == token.MUL_ASSIGN || s.Tok == token.QUO_ASSIGN) {
					binTok := token.ADD
					switch s.Tok {
					case token.SUB_ASSIGN:
						binTok = token.SUB
					case token.MUL_ASSIGN:
						binTok = token.MUL
					case token.QUO_ASSIGN:
						binTok = token.QUO
					}
					cx, cst, err := fl.complexBinary(&ast.BinaryExpr{X: s.Lhs[i], Op: binTok, Y: s.Rhs[i]}, want)
					if err != nil {
						return nil, err
					}
					out = append(out, cst...)
					rhs = cx
				} else {
					rhsEx, rs, err := fl.expr(s.Rhs[i], want)
					if err != nil {
						return nil, err
					}
					out = append(out, rs...)
					// s += t for strings is concatenation, not i32.add.
					if s.Tok == token.ADD_ASSIGN && isString(lhsGo) {
						rhs = &ir.Instr{Op: ir.OpCall, Func: "runtime.concatstrings", Type: typeString, Args: []ir.Expr{lhsEx, rhsEx}}
					} else if s.Tok == token.AND_NOT_ASSIGN {
						// x &^= y → x = x & (^y); binOp has no AND_NOT for i32.
						width := irNumWidth(want)
						var ynot ir.Expr
						if width == ir.I64 {
							ynot = &ir.Instr{Op: ir.OpI64Xor, Args: []ir.Expr{rhsEx, fl.i64const(-1)}}
							rhs = &ir.Instr{Op: ir.OpI64And, Args: []ir.Expr{lhsEx, ynot}}
						} else {
							ynot = &ir.Instr{Op: ir.OpI32Xor, Args: []ir.Expr{rhsEx, fl.i32const(-1)}}
							rhs = &ir.Instr{Op: ir.OpI32And, Args: []ir.Expr{lhsEx, ynot}}
						}
					} else {
						op, err := fl.binaryOpFromAssign(s.Tok, lhsGo)
						if err != nil {
							return nil, fl.errorf(s, "%v", err)
						}
						rhs = &ir.Instr{Op: op, Args: []ir.Expr{lhsEx, rhsEx}}
						switch s.Tok {
						case token.ADD_ASSIGN, token.SUB_ASSIGN, token.MUL_ASSIGN, token.QUO_ASSIGN,
							token.REM_ASSIGN, token.AND_ASSIGN, token.OR_ASSIGN, token.XOR_ASSIGN:
							rhs = fl.truncateInt(rhs, lhsGo)
						}
					}
				}
			}
		} else if call, ok := s.Rhs[i].(*ast.CallExpr); ok && !fl.isConversion(call) && !fl.isBuiltinPrint(call) && !fl.isBuiltinCall(call) {
			// x = f(...) → single assign from call (no extra temp).
			// unsafe.Sizeof/Alignof/Offsetof are builtins via SelectorExpr; they
			// must not fall through to resolveCallee (which expects *types.Func).
			var callSrc types.Type
			if ex, astmts, ok, err := fl.unsafeBuiltinCall(call, want); ok || err != nil {
				if err != nil {
					return nil, err
				}
				out = append(out, astmts...)
				rhs = ex
				callSrc = fl.typeOf(call)
			} else if sig := fl.funcValueSig(call.Fun); sig != nil {
				ex, astmts, err := fl.callFuncValue(call, sig)
				if err != nil {
					return nil, err
				}
				out = append(out, astmts...)
				if sig.Results().Len() != 1 {
					return nil, fl.errorf(call, "call returns %d values, want 1", sig.Results().Len())
				}
				rhs = ex
				callSrc = sig.Results().At(0).Type()
			} else if fun, ok := call.Fun.(*ast.SelectorExpr); ok {
				if sel, isIface := fl.ifaceMethod(fun); isIface {
					ex, astmts, err := fl.ifaceCall(call, fun, sel)
					if err != nil {
						return nil, err
					}
					out = append(out, astmts...)
					rhs = ex
					callSrc = fl.typeOf(call)
					_ = sel
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
					rhs = &ir.Instr{Op: ir.OpCall, Func: name, Args: args, Type: results[0]}
					callSrc = fl.typeOf(call)
				}
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
				rhs = &ir.Instr{Op: ir.OpCall, Func: name, Args: args, Type: results[0]}
				callSrc = fl.typeOf(call)
			}
			// Box concrete call results into interface LHS (gcgort: a = str(i)).
			if callSrc == nil {
				callSrc = fl.typeOf(call)
			}
			boxed, berr := fl.maybeBoxExpr(rhs, callSrc, s.Lhs[i])
			if berr != nil {
				return nil, berr
			}
			rhs = boxed
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
		if parallel {
			tmpTy := want
			if tmpTy == nil {
				switch r := rhs.(type) {
				case *ir.Instr:
					tmpTy = r.Type
				case *ir.Name:
					if r.Var != nil {
						tmpTy = r.Var.Typ
					} else if r.Temp != nil {
						tmpTy = r.Temp.Typ
					}
				}
			}
			if tmpTy == nil {
				tmpTy = ir.TypeI32
			}
			tmp := fl.newTemp(tmpTy)
			out = append(out, fl.assignTemp(tmp, rhs))
			slots[i] = rhsSlot{expr: fl.useTemp(tmp), tmp: tmp}
			continue
		}
		as, err := fl.assignTo(s.Lhs[i], rhs)
		if err != nil {
			return nil, err
		}
		out = append(out, as...)
	}
	if parallel {
		for i := range slots {
			as, err := fl.commitParallelDest(preps[i], slots[i].expr)
			if err != nil {
				return nil, err
			}
			out = append(out, as...)
		}
	}
	return out, nil
}

// parallelDest captures an LHS destination evaluated before parallel writes.
type parallelDest struct {
	kind   int // 0=assignTo, 1=slice/array index, 2=star store, 3=map, 4=field
	lhs    ast.Expr
	base   *ir.Temp
	idx    *ir.Temp
	arr    ir.Type
	keyT   *ir.Temp
	mT     *ir.Temp
	keyS   string
	elem   string
	box    string
	sel      *types.Selection
	baseTy   types.Type
	nilCheck bool // kind 4: panicNil if base is null before struct.set
}

// prepareParallelDests evaluates IndexExpr/StarExpr/SelectorExpr addresses before
// any write so `i, x[i] = 1, 100`, `p, *p = fp()`, and `p, p.i = new(T), 4`
// (issue23017) use the pre-assignment destinations.
func (fl *funcLower) prepareParallelDests(lhs []ast.Expr) ([]parallelDest, []ir.Stmt, error) {
	preps := make([]parallelDest, len(lhs))
	var out []ir.Stmt
	for i, e := range lhs {
		switch lx := e.(type) {
		case *ast.SelectorExpr:
			sel := fl.info.Selections[lx]
			if sel != nil && sel.Kind() == types.FieldVal {
				baseTy := fl.typeOf(lx.X)
				if baseTy != nil && (isStruct(baseTy) || isObjectPointer(baseTy)) {
					base, bstmts, err := fl.expr(lx.X, nil)
					if err != nil {
						return nil, nil, err
					}
					out = append(out, bstmts...)
					// Save the pointer/base now; nil-check at commit so earlier
					// destinations in the same parallel assign still write
					// (issue23017: m[2], p.i = … must set m before panicking).
					ptrBase := isPointer(baseTy) && isObjectPointer(baseTy)
					saveTy := baseTy
					if ptrBase {
						if p, ok := types.Unalias(baseTy).(*types.Pointer); ok {
							saveTy = p.Elem()
						}
					}
					baseIR, _ := fl.mapType(baseTy) // keep pointer IR for nil check
					if !ptrBase {
						baseIR, _ = fl.mapType(saveTy)
					}
					if baseIR == nil {
						baseIR = &ir.Ref{Null: true}
					}
					baseT := fl.newTemp(baseIR)
					out = append(out, fl.assignTemp(baseT, base))
					preps[i] = parallelDest{
						kind: 4, lhs: e, base: baseT, sel: sel, baseTy: saveTy, nilCheck: ptrBase,
					}
					continue
				}
			}
		case *ast.IndexExpr:
			xt := fl.typeOf(lx.X)
			if isSlice(xt) || isArray(xt) || isPtrToArray(xt) {
				base, bstmts, err := fl.expr(lx.X, nil)
				if err != nil {
					return nil, nil, err
				}
				idx, istmts, err := fl.expr(lx.Index, ir.TypeI32)
				if err != nil {
					return nil, nil, err
				}
				out = append(out, bstmts...)
				out = append(out, istmts...)
				if it := fl.typeOf(lx.Index); it != nil {
					if irt, err := fl.mapType(it); err == nil && irNumWidth(irt) == ir.I64 {
						idx = &ir.Instr{Op: ir.OpI32WrapI64, Args: []ir.Expr{idx}}
					}
				}
				baseTy, _ := fl.mapType(xt)
				if baseTy == nil {
					baseTy = &ir.Ref{Null: true}
				}
				baseT := fl.newTemp(baseTy)
				idxT := fl.newTemp(ir.TypeI32)
				out = append(out, fl.assignTemp(baseT, base), fl.assignTemp(idxT, idx))
				arrTy := ir.Type(ir.TypeI32)
				if isArray(xt) || isPtrToArray(xt) {
					elemT := xt
					if isPtrToArray(xt) {
						if p, ok := types.Unalias(xt).Underlying().(*types.Pointer); ok {
							elemT = p.Elem()
						}
					}
					if irt, err := fl.mapType(elemT); err == nil {
						arrTy = irt
					}
				}
				preps[i] = parallelDest{kind: 1, lhs: e, base: baseT, idx: idxT, arr: arrTy}
				continue
			}
			if isMap(xt) {
				mIR, _ := fl.mapType(xt)
				if mIR == nil {
					mIR = &ir.Ref{Null: true, Named: "$go.hmap"}
				}
				m, mstmts, err := fl.expr(lx.X, mIR)
				if err != nil {
					return nil, nil, err
				}
				out = append(out, mstmts...)
				mT := fl.newTemp(mIR)
				out = append(out, fl.assignTemp(mT, m))
				emptyIface := types.NewInterfaceType(nil, nil)
				key, kstmts, err := fl.convertToIface(lx.Index, fl.typeOf(lx.Index), emptyIface, typeIface)
				if err != nil {
					return nil, nil, err
				}
				out = append(out, kstmts...)
				keyT := fl.newTemp(typeIface)
				out = append(out, fl.assignTemp(keyT, key))
				keyT0, elemT := mapKeyElem(xt)
				dp := parallelDest{kind: 3, lhs: e, mT: mT, keyT: keyT}
				if keyT0 != nil {
					dp.keyS = desc.TypeKey(keyT0)
				}
				if elemT != nil {
					dp.elem = desc.TypeKey(elemT)
					dp.box = boxKind(elemT)
				}
				preps[i] = dp
				continue
			}
		case *ast.StarExpr:
			ptr, pstmts, err := fl.expr(lx.X, nil)
			if err != nil {
				return nil, nil, err
			}
			out = append(out, pstmts...)
			ptrTy, _ := fl.mapType(fl.typeOf(lx.X))
			if ptrTy == nil {
				ptrTy = &ir.Ref{Null: true}
			}
			ptrT := fl.newTemp(ptrTy)
			out = append(out, fl.assignTemp(ptrT, ptr))
			preps[i] = parallelDest{kind: 2, lhs: e, base: ptrT}
			continue
		}
		preps[i] = parallelDest{kind: 0, lhs: e}
	}
	return preps, out, nil
}

func (fl *funcLower) commitParallelDest(p parallelDest, rhs ir.Expr) ([]ir.Stmt, error) {
	switch p.kind {
	case 1:
		xt := fl.typeOf(p.lhs.(*ast.IndexExpr).X)
		if isSlice(xt) {
			return []ir.Stmt{&ir.ExprStmt{X: &ir.Instr{Op: ir.OpSliceSet, Args: []ir.Expr{fl.useTemp(p.base), fl.useTemp(p.idx), rhs}}}}, nil
		}
		return []ir.Stmt{&ir.ExprStmt{X: &ir.Instr{Op: ir.OpArraySet, Type: p.arr, Args: []ir.Expr{fl.useTemp(p.base), fl.useTemp(p.idx), rhs}}}}, nil
	case 2:
		return fl.assignToSavedStar(p.lhs.(*ast.StarExpr), fl.useTemp(p.base), rhs)
	case 4:
		var stmts []ir.Stmt
		base := fl.useTemp(p.base)
		if p.nilCheck {
			stmts = append(stmts, fl.panicIfNilRef(base))
		}
		setStmts, err := fl.fieldSetChain(base, p.baseTy, p.sel, rhs)
		if err != nil {
			return nil, err
		}
		return append(stmts, setStmts...), nil
	case 3:
		val := rhs
		if p.elem != "" && p.box != "" {
			xt := fl.typeOf(p.lhs.(*ast.IndexExpr).X)
			_, elemT := mapKeyElem(xt)
			if elemT != nil && !isIface(elemT) {
				emptyIface := types.NewInterfaceType(nil, nil)
				itab := desc.ItabGlobal(emptyIface, elemT)
				val = &ir.Instr{
					Op: ir.OpIfaceMake, Type: typeIface, Global: itab,
					Str: "type:" + desc.TypeKey(elemT), Field: p.box, Args: []ir.Expr{rhs},
				}
			}
		}
		set := &ir.Instr{Op: ir.OpMapSet, Args: []ir.Expr{fl.useTemp(p.mT), fl.useTemp(p.keyT), val}}
		if p.keyS != "" {
			set.Str = p.keyS
		}
		if p.elem != "" {
			set.Global = p.elem
			set.Field = p.box
		}
		return []ir.Stmt{&ir.ExprStmt{X: set}}, nil
	default:
		return fl.assignTo(p.lhs, rhs)
	}
}

// assignParallel writes rhs[i] into lhs[i] after evaluating destinations first.
func (fl *funcLower) assignParallel(lhs []ast.Expr, rhs []ir.Expr) ([]ir.Stmt, error) {
	preps, out, err := fl.prepareParallelDests(lhs)
	if err != nil {
		return nil, err
	}
	for i := range lhs {
		as, err := fl.commitParallelDest(preps[i], rhs[i])
		if err != nil {
			return nil, err
		}
		out = append(out, as...)
	}
	return out, nil
}

// assignToSavedStar is assignTo for *p when the pointer expression was already
// evaluated into ptr (parallel assignment).
func (fl *funcLower) assignToSavedStar(lhs *ast.StarExpr, ptr, rhs ir.Expr) ([]ir.Stmt, error) {
	var stmts []ir.Stmt
	if xt := fl.typeOf(lhs.X); xt != nil && isObjectPointer(xt) {
		elem := types.Unalias(xt).Underlying().(*types.Pointer).Elem()
		if arr, ok := types.Unalias(elem).Underlying().(*types.Array); ok {
			arrIR, err := fl.mapType(elem)
			if err != nil {
				return nil, err
			}
			r, ok := arrIR.(*ir.Ref)
			if !ok || r.Named == "" {
				return nil, fl.errorf(lhs, "star assign: bad array type")
			}
			if _, _, err := fl.pkg.ensureArrayType(arr.Elem()); err != nil {
				return nil, err
			}
			rhsT := fl.newTemp(arrIR)
			stmts = append(stmts, fl.assignTemp(rhsT, rhs))
			stmts = append(stmts, &ir.ExprStmt{X: &ir.Instr{
				Op: ir.OpArrayCopy, Type: arrIR, Type2: arrIR,
				Args: []ir.Expr{
					ptr, fl.i32const(0),
					fl.useTemp(rhsT), fl.i32const(0),
					fl.i32const(arr.Len()),
				},
			}})
			return stmts, nil
		}
		name, err := fl.mapType(elem)
		if err != nil {
			return nil, err
		}
		r, ok := name.(*ir.Ref)
		if !ok || r.Named == "" {
			return nil, fl.errorf(lhs, "star assign: bad struct type")
		}
		td := fl.mod.LookupType(r.Named)
		if td == nil {
			return nil, fl.errorf(lhs, "star assign: unknown type %s", r.Named)
		}
		rhsT := fl.newTemp(name)
		stmts = append(stmts, fl.assignTemp(rhsT, rhs))
		for _, f := range td.Fields {
			get := &ir.Instr{
				Op: ir.OpStructGet, Type: name, Field: f.Name,
				Args: []ir.Expr{fl.useTemp(rhsT)},
			}
			stmts = append(stmts, &ir.ExprStmt{X: &ir.Instr{
				Op: ir.OpStructSet, Type: name, Field: f.Name,
				Args: []ir.Expr{ptr, get},
			}})
		}
		return stmts, nil
	}
	stmts = append(stmts, &ir.ExprStmt{X: &ir.Instr{Op: ir.OpStore, Args: []ir.Expr{ptr, rhs}}})
	return stmts, nil
}

// assignThroughSliceArrayPtr lowers *(*[N]T)(s) = x when s is a slice, writing
// into s's backing array at s.off (issue54467). ok=false → fall through.
func (fl *funcLower) assignThroughSliceArrayPtr(lhs *ast.StarExpr, rhs ir.Expr) ([]ir.Stmt, bool, error) {
	conv := lhs.X
	for {
		if p, ok := conv.(*ast.ParenExpr); ok {
			conv = p.X
			continue
		}
		break
	}
	call, ok := conv.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return nil, false, nil
	}
	dst := fl.typeOf(call)
	if dst == nil || !isPtrToArray(dst) {
		return nil, false, nil
	}
	argT := fl.typeOf(call.Args[0])
	if argT == nil || !isSlice(argT) {
		return nil, false, nil
	}
	arrPtr := types.Unalias(dst).Underlying().(*types.Pointer)
	arr := types.Unalias(arrPtr.Elem()).Underlying().(*types.Array)
	n := arr.Len()
	arrIR, err := fl.mapType(arrPtr.Elem())
	if err != nil {
		return nil, true, err
	}
	r, ok := arrIR.(*ir.Ref)
	if !ok || r.Named == "" {
		return nil, true, fl.errorf(lhs, "slice array-ptr assign: bad array type")
	}
	if _, _, err := fl.pkg.ensureArrayType(arr.Elem()); err != nil {
		return nil, true, err
	}
	sliceIR, err := fl.mapType(argT)
	if err != nil {
		return nil, true, err
	}
	sv, sstmts, err := fl.expr(call.Args[0], sliceIR)
	if err != nil {
		return nil, true, err
	}
	var stmts []ir.Stmt
	stmts = append(stmts, sstmts...)
	srcV := fl.newVar("sapSrc", sliceIR)
	stmts = append(stmts, fl.assignVar(srcV, sv))
	dataV := fl.newVar("sapData", &ir.Ref{Null: true, Named: r.Named})
	offV := fl.newVar("sapOff", ir.TypeI32)
	lenV := fl.newVar("sapLen", ir.TypeI32)
	stmts = append(stmts,
		fl.assignVar(dataV, &ir.Instr{Op: ir.OpStructGet, Type: sliceIR, Field: "data", Args: []ir.Expr{fl.useVar(srcV)}}),
		fl.assignVar(offV, &ir.Instr{Op: ir.OpStructGet, Type: sliceIR, Field: "off", Args: []ir.Expr{fl.useVar(srcV)}}),
		fl.assignVar(lenV, &ir.Instr{Op: ir.OpStructGet, Type: sliceIR, Field: "len", Args: []ir.Expr{fl.useVar(srcV)}}),
	)
	if n > 0 {
		stmts = append(stmts, &ir.If{
			Cond: &ir.Instr{Op: ir.OpI32LtU, Args: []ir.Expr{fl.useVar(lenV), fl.i32const(n)}},
			Then: &ir.Seq{Stmts: []ir.Stmt{
				&ir.ExprStmt{X: &ir.Instr{
					Op: ir.OpCall, Func: "runtime.panicSliceToArray",
					Args: []ir.Expr{fl.useVar(lenV), fl.i32const(n)},
				}},
				&ir.Unreachable{},
			}},
		})
	}
	rhsT := fl.newTemp(arrIR)
	stmts = append(stmts, fl.assignTemp(rhsT, rhs))
	stmts = append(stmts, &ir.ExprStmt{X: &ir.Instr{
		Op: ir.OpArrayCopy, Type: arrIR, Type2: arrIR,
		Args: []ir.Expr{
			&ir.Instr{Op: ir.OpRefAsNonNull, Args: []ir.Expr{fl.useVar(dataV)}},
			fl.useVar(offV),
			fl.useTemp(rhsT), fl.i32const(0),
			fl.i32const(n),
		},
	}})
	return stmts, true, nil
}

// opAssignAddrOnce lowers *p/x[i]/m[k] op= y with the destination evaluated
// before y. Returns ok=false to fall back to the generic op-assign path.
func (fl *funcLower) opAssignAddrOnce(lhs ast.Expr, tok token.Token, rhsAst ast.Expr, want ir.Type) ([]ir.Stmt, bool, error) {
	return fl.opAssignAddrOnceGet(lhs, tok, want, func() (ir.Expr, []ir.Stmt, error) {
		return fl.expr(rhsAst, want)
	})
}

// opAssignAddrOnceIR is opAssignAddrOnce with an already-lowered RHS (x++).
func (fl *funcLower) opAssignAddrOnceIR(lhs ast.Expr, tok token.Token, rhsEx ir.Expr, want ir.Type) ([]ir.Stmt, bool, error) {
	return fl.opAssignAddrOnceGet(lhs, tok, want, func() (ir.Expr, []ir.Stmt, error) {
		return rhsEx, nil, nil
	})
}

func (fl *funcLower) opAssignAddrOnceGet(lhs ast.Expr, tok token.Token, want ir.Type, getRhs func() (ir.Expr, []ir.Stmt, error)) ([]ir.Stmt, bool, error) {
	combine := func(lhsEx, rhsEx ir.Expr, lhsGo types.Type) (ir.Expr, error) {
		if tok == token.ADD_ASSIGN && lhsGo != nil && isString(lhsGo) {
			return &ir.Instr{Op: ir.OpCall, Func: "runtime.concatstrings", Type: typeString, Args: []ir.Expr{lhsEx, rhsEx}}, nil
		}
		if tok == token.AND_NOT_ASSIGN {
			if irNumWidth(want) == ir.I64 {
				ynot := &ir.Instr{Op: ir.OpI64Xor, Args: []ir.Expr{rhsEx, fl.i64const(-1)}}
				return &ir.Instr{Op: ir.OpI64And, Args: []ir.Expr{lhsEx, ynot}}, nil
			}
			ynot := &ir.Instr{Op: ir.OpI32Xor, Args: []ir.Expr{rhsEx, fl.i32const(-1)}}
			return &ir.Instr{Op: ir.OpI32And, Args: []ir.Expr{lhsEx, ynot}}, nil
		}
		if tok == token.SHL_ASSIGN || tok == token.SHR_ASSIGN {
			return nil, errShiftOpAssignOnce
		}
		op, err := fl.binaryOpFromAssign(tok, lhsGo)
		if err != nil {
			return nil, err
		}
		out := ir.Expr(&ir.Instr{Op: op, Args: []ir.Expr{lhsEx, rhsEx}})
		switch tok {
		case token.ADD_ASSIGN, token.SUB_ASSIGN, token.MUL_ASSIGN, token.QUO_ASSIGN,
			token.REM_ASSIGN, token.AND_ASSIGN, token.OR_ASSIGN, token.XOR_ASSIGN:
			out = fl.truncateInt(out, lhsGo)
		}
		return out, nil
	}

	switch lhs := lhs.(type) {
	case *ast.StarExpr:
		xt := fl.typeOf(lhs.X)
		if xt != nil && isObjectPointer(xt) {
			return nil, false, nil // object-pointer *T uses field copy path
		}
		ptr, pstmts, err := fl.expr(lhs.X, nil)
		if err != nil {
			return nil, true, err
		}
		var out []ir.Stmt
		out = append(out, pstmts...)
		ptrTy, _ := fl.mapType(xt)
		if ptrTy == nil {
			ptrTy = &ir.Ref{Null: true, Named: "$go.ptr.i32"}
		}
		ptrT := fl.newTemp(ptrTy)
		out = append(out, fl.assignTemp(ptrT, ptr))
		loadT := fl.newTemp(want)
		out = append(out, fl.assignTemp(loadT, &ir.Instr{Op: ir.OpDeref, Type: want, Args: []ir.Expr{fl.useTemp(ptrT)}}))
		rhsEx, rs, err := getRhs()
		if err != nil {
			return nil, true, err
		}
		out = append(out, rs...)
		combined, err := combine(fl.useTemp(loadT), rhsEx, fl.typeOf(lhs))
		if err != nil {
			return nil, true, err
		}
		out = append(out, &ir.ExprStmt{X: &ir.Instr{Op: ir.OpStore, Args: []ir.Expr{fl.useTemp(ptrT), combined}}})
		return out, true, nil

	case *ast.IndexExpr:
		xt := fl.typeOf(lhs.X)
		if isMap(xt) {
			mIR, err := fl.mapType(xt)
			if err != nil {
				return nil, true, err
			}
			m, mstmts, err := fl.expr(lhs.X, mIR)
			if err != nil {
				return nil, true, err
			}
			var out []ir.Stmt
			out = append(out, mstmts...)
			mT := fl.newTemp(mIR)
			out = append(out, fl.assignTemp(mT, m))
			emptyIface := types.NewInterfaceType(nil, nil)
			key, kstmts, err := fl.convertToIface(lhs.Index, fl.typeOf(lhs.Index), emptyIface, typeIface)
			if err != nil {
				return nil, true, err
			}
			out = append(out, kstmts...)
			keyT := fl.newTemp(typeIface)
			out = append(out, fl.assignTemp(keyT, key))
			keyT0, elemT := mapKeyElem(xt)
			get := &ir.Instr{Op: ir.OpMapGet, Type: want, Args: []ir.Expr{fl.useTemp(mT), fl.useTemp(keyT)}}
			if keyT0 != nil {
				get.Str = desc.TypeKey(keyT0)
			}
			if elemT != nil {
				get.Global = desc.TypeKey(elemT)
				get.Field = boxKind(elemT)
			}
			oldT := fl.newTemp(want)
			out = append(out, fl.assignTemp(oldT, get))
			rhsEx, rs, err := getRhs()
			if err != nil {
				return nil, true, err
			}
			out = append(out, rs...)
			combined, err := combine(fl.useTemp(oldT), rhsEx, fl.typeOf(lhs))
			if err != nil {
				return nil, true, err
			}
			// Store using saved map+key (not re-evaluating lhs.X).
			var val ir.Expr = combined
			if elemT != nil && !isIface(elemT) {
				box := boxKind(elemT)
				itab := desc.ItabGlobal(emptyIface, elemT)
				val = &ir.Instr{
					Op: ir.OpIfaceMake, Type: typeIface, Global: itab,
					Str: "type:" + desc.TypeKey(elemT), Field: box, Args: []ir.Expr{combined},
				}
			}
			set := &ir.Instr{Op: ir.OpMapSet, Args: []ir.Expr{fl.useTemp(mT), fl.useTemp(keyT), val}}
			if keyT0 != nil {
				set.Str = desc.TypeKey(keyT0)
			}
			if elemT != nil {
				set.Global = desc.TypeKey(elemT)
				set.Field = boxKind(elemT)
			}
			out = append(out, &ir.ExprStmt{X: set})
			return out, true, nil
		}
		if isSlice(xt) || isArray(xt) || isPtrToArray(xt) {
			base, bstmts, err := fl.expr(lhs.X, nil)
			if err != nil {
				return nil, true, err
			}
			idx, istmts, err := fl.expr(lhs.Index, ir.TypeI32)
			if err != nil {
				return nil, true, err
			}
			var out []ir.Stmt
			out = append(out, bstmts...)
			out = append(out, istmts...)
			if it := fl.typeOf(lhs.Index); it != nil {
				if irt, err := fl.mapType(it); err == nil && irNumWidth(irt) == ir.I64 {
					idx = &ir.Instr{Op: ir.OpI32WrapI64, Args: []ir.Expr{idx}}
				}
			}
			baseTy, _ := fl.mapType(xt)
			if baseTy == nil {
				baseTy = &ir.Ref{Null: true}
			}
			baseT := fl.newTemp(baseTy)
			idxT := fl.newTemp(ir.TypeI32)
			out = append(out, fl.assignTemp(baseT, base), fl.assignTemp(idxT, idx))
			var loaded ir.Expr
			if isSlice(xt) {
				loaded = &ir.Instr{Op: ir.OpSliceGet, Type: want, Args: []ir.Expr{fl.useTemp(baseT), fl.useTemp(idxT)}}
			} else {
				arrT := xt
				if isPtrToArray(xt) {
					if p, ok := types.Unalias(xt).Underlying().(*types.Pointer); ok {
						arrT = p.Elem()
					}
				}
				arrName := ""
				arrIR, _ := fl.mapType(arrT)
				if r, ok := arrIR.(*ir.Ref); ok {
					arrName = r.Named
					arrIR = &ir.Ref{Named: arrName}
				}
				op := ir.OpArrayGet
				switch arrName {
				case "$go.bytes":
					op = ir.OpArrayGetU
				case "$go.i16s":
					if et := arrayElemType(arrT); et != nil && isUnsigned(et) {
						op = ir.OpArrayGetU
					} else {
						op = ir.OpArrayGetS
					}
				}
				loaded = &ir.Instr{Op: op, Type: arrIR, Args: []ir.Expr{fl.useTemp(baseT), fl.useTemp(idxT)}}
			}
			loadT := fl.newTemp(want)
			out = append(out, fl.assignTemp(loadT, loaded))
			rhsEx, rs, err := getRhs()
			if err != nil {
				return nil, true, err
			}
			out = append(out, rs...)
			combined, err := combine(fl.useTemp(loadT), rhsEx, fl.typeOf(lhs))
			if err != nil {
				return nil, true, err
			}
			if isSlice(xt) {
				out = append(out, &ir.ExprStmt{X: &ir.Instr{Op: ir.OpSliceSet, Args: []ir.Expr{fl.useTemp(baseT), fl.useTemp(idxT), combined}}})
			} else {
				arrT := xt
				if isPtrToArray(xt) {
					if p, ok := types.Unalias(xt).Underlying().(*types.Pointer); ok {
						arrT = p.Elem()
					}
				}
				arrIR, _ := fl.mapType(arrT)
				out = append(out, &ir.ExprStmt{X: &ir.Instr{Op: ir.OpArraySet, Type: arrIR, Args: []ir.Expr{fl.useTemp(baseT), fl.useTemp(idxT), combined}}})
			}
			return out, true, nil
		}
	}
	return nil, false, nil
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
	return fl.multiAssignAppend(s, out)
}

func (fl *funcLower) multiAssignTypeAssert(s *ast.AssignStmt, ta *ast.TypeAssertExpr, out []ir.Stmt) ([]ir.Stmt, error) {
	if len(s.Lhs) != 2 {
		return nil, fl.errorf(s, "type assert comma-ok wants 2 LHS")
	}
	// LHS destinations before the assert operand (bug242: *e3(&iv), ok = e2(x).(I)).
	preps, pstmts, err := fl.prepareParallelDests(s.Lhs)
	if err != nil {
		return nil, err
	}
	out = append(out, pstmts...)
	ex, stmts, err := fl.typeAssert(ta, nil, true)
	if err != nil {
		return nil, err
	}
	out = append(out, stmts...)
	// Prefer the asserted type over lhsType: blank `_` reports i32 and would
	// mis-type the value temp (e.g. `_, ok := x.(string)` → drop a string
	// through an i32 local).
	var valTy ir.Type
	if in, ok := ex.(*ir.Instr); ok && in.Type != nil {
		valTy = in.Type
	} else {
		valTy, err = fl.lhsType(s.Lhs[0])
		if err != nil {
			return nil, err
		}
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
	assertGo := fl.typeOf(ta.Type)
	v0, err := fl.maybeBoxExpr(fl.useTemp(vt), assertGo, s.Lhs[0])
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

func (fl *funcLower) assignTo(lhs ast.Expr, rhs ir.Expr) ([]ir.Stmt, error) {
	// Array/struct values: clone before reseating a slot (docs/design/03).
	// *p = v for object pointers copies into the existing object — skip here.
	// Blank `_` only needs drop — cloning a failed type-assert null would
	// struct.get/array.get on null (errors.TestIs / errorUncomparable.Is).
	var pre []ir.Stmt
	if !isBlankIdent(lhs) {
		if _, isStar := lhs.(*ast.StarExpr); !isStar {
			if ty := fl.assignLHSGoType(lhs); needsValueClone(ty) && !isFreshValueAlloc(rhs) {
				cloned, stmts, err := fl.cloneValue(rhs, ty)
				if err != nil {
					return nil, err
				}
				pre = stmts
				rhs = cloned
			}
		}
	}
	stmts, err := fl.assignToCloned(lhs, rhs)
	if err != nil {
		return nil, err
	}
	return append(pre, stmts...), nil
}

// assignLHSGoType is typeOf(lhs), falling back to Defs/Uses for := names
// that may be missing from Types.
func (fl *funcLower) assignLHSGoType(lhs ast.Expr) types.Type {
	if ty := fl.typeOf(lhs); ty != nil {
		return ty
	}
	switch e := lhs.(type) {
	case *ast.Ident:
		if obj := fl.info.Defs[e]; obj != nil {
			return obj.Type()
		}
		if obj := fl.info.Uses[e]; obj != nil {
			return obj.Type()
		}
	case *ast.ParenExpr:
		return fl.assignLHSGoType(e.X)
	}
	return nil
}

func (fl *funcLower) assignToCloned(lhs ast.Expr, rhs ir.Expr) ([]ir.Stmt, error) {
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
				return fl.writeGlobal(obj, g, rhs), nil
			}
		}
		if v, err := fl.lookupVar(lhs); err == nil && v != nil {
			fl.markPlainDirty(obj)
			return []ir.Stmt{fl.assignVar(v, rhs)}, nil
		}
		v, err := fl.lookupVar(lhs)
		if err != nil {
			return nil, err
		}
		fl.markPlainDirty(obj)
		return []ir.Stmt{fl.assignVar(v, rhs)}, nil
	case *ast.StarExpr:
		// *(*[N]T)(slice) = x — slice→*[N]T with off≠0 copies into a fresh
		// array (no interior pointers), so stores must write back into the
		// slice's backing at off (fixedbugs/issue54467).
		if stmts, ok, err := fl.assignThroughSliceArrayPtr(lhs, rhs); ok || err != nil {
			return stmts, err
		}
		ptr, stmts, err := fl.expr(lhs.X, nil)
		if err != nil {
			return nil, err
		}
		// Object pointer (*Struct / *[N]T): *p and p share the same ref; copy
		// fields/elements into the existing object (docs/design/03).
		// Fat-pointer stores use OpStore.
		if xt := fl.typeOf(lhs.X); xt != nil && isObjectPointer(xt) {
			pt, err := fl.mapType(xt)
			if err != nil {
				return nil, err
			}
			pv := fl.newVar("pstar", pt)
			stmts = append(stmts, fl.assignVar(pv, ptr))
			ptr = fl.useVar(pv)
			stmts = append(stmts, &ir.If{
				Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{ptr}},
				Then: &ir.Seq{Stmts: []ir.Stmt{
					&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: "runtime.panicNil"}},
					&ir.Unreachable{},
				}},
			})
			elem := types.Unalias(xt).Underlying().(*types.Pointer).Elem()
			// *[N]T: element-wise array.copy into the existing array ref.
			if arr, ok := types.Unalias(elem).Underlying().(*types.Array); ok {
				arrIR, err := fl.mapType(elem)
				if err != nil {
					return nil, err
				}
				r, ok := arrIR.(*ir.Ref)
				if !ok || r.Named == "" {
					return nil, fl.errorf(lhs, "star assign: bad array type")
				}
				if _, _, err := fl.pkg.ensureArrayType(arr.Elem()); err != nil {
					return nil, err
				}
				rhsT := fl.newTemp(arrIR)
				stmts = append(stmts, fl.assignTemp(rhsT, rhs))
				stmts = append(stmts, &ir.ExprStmt{X: &ir.Instr{
					Op: ir.OpArrayCopy, Type: arrIR, Type2: arrIR,
					Args: []ir.Expr{
						ptr, fl.i32const(0),
						fl.useTemp(rhsT), fl.i32const(0),
						fl.i32const(arr.Len()),
					},
				}})
				return stmts, nil
			}
			name, err := fl.mapType(elem)
			if err != nil {
				return nil, err
			}
			r, ok := name.(*ir.Ref)
			if !ok || r.Named == "" {
				return nil, fl.errorf(lhs, "star assign: bad struct type")
			}
			td := fl.mod.LookupType(r.Named)
			if td == nil {
				return nil, fl.errorf(lhs, "star assign: unknown type %s", r.Named)
			}
			rhsT := fl.newTemp(name)
			stmts = append(stmts, fl.assignTemp(rhsT, rhs))
			for _, f := range td.Fields {
				get := &ir.Instr{
					Op: ir.OpStructGet, Type: name, Field: f.Name,
					Args: []ir.Expr{fl.useTemp(rhsT)},
				}
				stmts = append(stmts, &ir.ExprStmt{X: &ir.Instr{
					Op: ir.OpStructSet, Type: name, Field: f.Name,
					Args: []ir.Expr{ptr, get},
				}})
			}
			return stmts, nil
		}
		stmts = append(stmts, &ir.ExprStmt{X: &ir.Instr{Op: ir.OpStore, Args: []ir.Expr{ptr, rhs}}})
		return stmts, nil
	case *ast.SelectorExpr:
		sel := fl.info.Selections[lhs]
		if sel == nil {
			// package.Var = ... (cross-package global assignment).
			if obj := fl.info.Uses[lhs.Sel]; obj != nil {
				if v, ok := obj.(*types.Var); ok {
					fl.markAssigned(v)
					if g := fl.lookupGlobal(lhs.Sel); g != nil {
						return fl.writeGlobal(v, g, rhs), nil
					}
					if id, ok := lhs.X.(*ast.Ident); ok {
						if pn, ok := fl.info.Uses[id].(*types.PkgName); ok && pn.Imported() != nil {
							gname := pn.Imported().Name() + "." + lhs.Sel.Name
							if g := fl.pkg.mod.LookupGlobal(gname); g != nil {
								return fl.writeGlobal(v, g, rhs), nil
							}
							return []ir.Stmt{&ir.ExprStmt{X: &ir.Instr{
								Op: ir.OpGlobalSet, Global: gname, Args: []ir.Expr{rhs},
							}}}, nil
						}
					}
				}
			}
			return nil, fl.errorf(lhs, "assignment to non-field selector %s (kind=nil)", lhs.Sel.Name)
		}
		if sel.Kind() != types.FieldVal {
			return nil, fl.errorf(lhs, "assignment to non-field selector %s (kind=%d)", lhs.Sel.Name, int(sel.Kind()))
		}
		base, stmts, err := fl.expr(lhs.X, nil)
		if err != nil {
			return nil, err
		}
		// Prefer struct.set for mutable fields when base is a struct ref.
		// Promoted embedded fields: chain gets then set the leaf.
		baseTy := fl.typeOf(lhs.X)
		if baseTy != nil && (isStruct(baseTy) || isObjectPointer(baseTy)) {
			// p.f = x on nil *struct must panic (issue23017), same as field get.
			if isPointer(baseTy) && isObjectPointer(baseTy) {
				ptrTy, err := fl.mapType(baseTy)
				if err != nil {
					return nil, err
				}
				pv := fl.newVar("psf", ptrTy)
				stmts = append(stmts, fl.assignVar(pv, base))
				stmts = append(stmts, fl.panicIfNilRef(fl.useVar(pv)))
				base = fl.useVar(pv)
				if p, ok := types.Unalias(baseTy).(*types.Pointer); ok {
					baseTy = p.Elem()
				}
			}
			setStmts, err := fl.fieldSetChain(base, baseTy, sel, rhs)
			if err != nil {
				return nil, fl.errorf(lhs, "%v", err)
			}
			return append(stmts, setStmts...), nil
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
		xt := fl.typeOf(lhs.X)
		if isMap(xt) {
			return fl.mapIndexAssign(lhs, rhs, fl.typeOf(lhs))
		}
		base, stmts, err := fl.expr(lhs.X, nil)
		if err != nil {
			return nil, err
		}
		idx, istmts, err := fl.expr(lhs.Index, ir.TypeI32)
		if err != nil {
			return nil, err
		}
		stmts = append(stmts, istmts...)
		// Match indexExpr: Wasm array/slice indices are i32.
		if it := fl.typeOf(lhs.Index); it != nil {
			if irt, err := fl.mapType(it); err == nil && irNumWidth(irt) == ir.I64 {
				idx = &ir.Instr{Op: ir.OpI32WrapI64, Args: []ir.Expr{idx}}
			}
		}
		if isSlice(xt) {
			stmts = append(stmts, &ir.ExprStmt{X: &ir.Instr{Op: ir.OpSliceSet, Args: []ir.Expr{base, idx, rhs}}})
			return stmts, nil
		}
		if isArray(xt) || isPtrToArray(xt) {
			elemT := xt
			if isPtrToArray(xt) {
				if p, ok := types.Unalias(xt).Underlying().(*types.Pointer); ok {
					elemT = p.Elem()
				}
				pt, err := fl.mapType(xt)
				if err != nil {
					return nil, err
				}
				pv := fl.newVar("parr", pt)
				stmts = append(stmts, fl.assignVar(pv, base))
				base = fl.useVar(pv)
				stmts = append(stmts, &ir.If{
					Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{base}},
					Then: &ir.Seq{Stmts: []ir.Stmt{
						&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: "runtime.panicNil"}},
						&ir.Unreachable{},
					}},
				})
			}
			arrName := ""
			if irt, err := fl.mapType(elemT); err == nil {
				if r, ok := irt.(*ir.Ref); ok {
					arrName = r.Named
				}
			}
			at := ir.Type(ir.TypeI32)
			if arrName != "" {
				at = &ir.Ref{Named: arrName}
			}
			// Recoverable panic instead of Wasm array OOB trap (issue79236).
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
						&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: "runtime.panicIndex", Args: []ir.Expr{
							&ir.Instr{Op: ir.OpI64ExtendI32S, Args: []ir.Expr{idx}}, fl.useVar(lenV),
						}}},
						&ir.Unreachable{},
					}},
				},
				&ir.ExprStmt{X: &ir.Instr{Op: ir.OpArraySet, Type: at, Args: []ir.Expr{base, idx, rhs}}},
			)
			return stmts, nil
		}
		kind, _ := fatPtrKind(fl.typeOf(lhs))
		if kind == "" {
			kind = "i32"
		}
		addr := &ir.Instr{
			Op: ir.OpAddr, Type: &ir.Ref{Null: true, Named: "$go.ptr." + kind},
			Args: []ir.Expr{base, idx}, Field: "elem", Str: "elem",
		}
		stmts = append(stmts, &ir.ExprStmt{X: &ir.Instr{Op: ir.OpStore, Type: addr.Type, Args: []ir.Expr{addr, rhs}}})
		return stmts, nil
	case *ast.ParenExpr:
		return fl.assignToCloned(lhs.X, rhs)
	default:
		return nil, fl.errorf(lhs, "assignment to %T not supported in M3", lhs)
	}
}

func (fl *funcLower) lhsType(lhs ast.Expr) (ir.Type, error) {
	if id, ok := lhs.(*ast.Ident); ok && id.Name == "_" {
		// No destination type: use the RHS's natural IR type. Returning i32
		// mis-typed `_ = &x` / `_ = x.(T)` (fat pointers and non-i32 values).
		return nil, nil
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
	// Inside a range-over-func yield: early-exit the iterator, then return
	// from the enclosing function after the iterator call returns.
	if fl.rangeYield && len(fl.rangeFrames) > 0 {
		return fl.rangeYieldReturn(s)
	}
	if len(s.Results) == 0 {
		if len(fl.namedResults) > 0 {
			// Sync cell-boxed named results (e.g. written by a goroutine
			// closure) into plain locals before returning (escape.go defer1).
			out := fl.syncNamedResultCells()
			return append(out, fl.resultReturn()), nil
		}
		return []ir.Stmt{&ir.Return{}}, nil
	}
	if len(s.Results) == 1 && len(fl.results) > 1 {
		call, ok := s.Results[0].(*ast.CallExpr)
		if !ok {
			return nil, fl.errorf(s, "expected multi-value call in return")
		}
		// Materialize with the callee's concrete result types, then coerce
		// into this function's result types (e.g. map[string]any → any).
		temps, stmts, err := fl.callMaterialize(call, nil)
		if err != nil {
			return nil, err
		}
		srcGos := fl.callResultGoTypes(call)
		var results []ir.Expr
		for i, t := range temps {
			ex := fl.useTemp(t)
			var dstGo types.Type
			var resObj types.Object
			if fl.sig != nil && i < fl.sig.Results().Len() {
				resObj = fl.sig.Results().At(i)
				dstGo = resObj.Type()
			}
			if fl.pkg != nil && len(fl.pkg.subst) > 0 && dstGo != nil {
				dstGo = fl.pkg.subst.Apply(dstGo)
			}
			srcGi := types.Type(nil)
			if i < len(srcGos) {
				srcGi = srcGos[i]
				if fl.pkg != nil && len(fl.pkg.subst) > 0 && srcGi != nil {
					srcGi = fl.pkg.subst.Apply(srcGi)
				}
			}
			if dstGo != nil && isIface(dstGo) && !isTypeParam(dstGo) &&
				srcGi != nil && !isIface(srcGi) && !isTypeParam(srcGi) {
				boxed := fl.boxIRToIface(ex, srcGi, dstGo)
				nt := fl.newTemp(typeIface)
				stmts = append(stmts, fl.assignTemp(nt, boxed))
				ex = fl.useTemp(nt)
			} else if i < len(fl.results) && fl.results[i] != nil {
				// Keep a typed temp matching the declared result when possible.
				want := fl.results[i]
				if t.Typ == nil || !ir.TypesEqual(t.Typ, want) {
					nt := fl.newTemp(want)
					stmts = append(stmts, fl.assignTemp(nt, ex))
					ex = fl.useTemp(nt)
				}
			}
			// Assign into named results before return so defer a++ sees them
			// and bare/result returns observe the post-defer values (bug293).
			if i < len(fl.namedResults) && fl.namedResults[i] != nil {
				stmts = append(stmts, fl.assignLocalSync(resObj, fl.namedResults[i], ex)...)
			}
			results = append(results, ex)
		}
		if len(fl.namedResults) > 0 {
			return append(stmts, fl.resultReturn()), nil
		}
		stmts = append(stmts, &ir.Return{Results: results})
		return stmts, nil
	}
	// When returning into named results, sync cells so deferred closures see
	// the values (issue 32175). For multi-result returns, evaluate every RHS
	// into a temp first — writing named results one-by-one would break
	// `return b, a` (assign into a before reading a for the second result).
	needNamedSync := len(fl.namedResults) > 0
	stageTemps := needNamedSync && len(s.Results) > 1
	var out []ir.Stmt
	var results []ir.Expr
	temps := make([]*ir.Temp, len(s.Results))
	resObjs := make([]types.Object, len(s.Results))
	for i, r := range s.Results {
		var want ir.Type
		var dstGo types.Type
		if i < len(fl.results) {
			want = fl.results[i]
		}
		if fl.sig != nil && i < fl.sig.Results().Len() {
			resObjs[i] = fl.sig.Results().At(i)
			dstGo = resObjs[i].Type()
		}
		ex, stmts, err := fl.coerceExpr(r, dstGo, want)
		if err != nil {
			return nil, err
		}
		out = append(out, stmts...)
		if stageTemps {
			if want == nil {
				want = typeIface
			}
			t := fl.newTemp(want)
			out = append(out, fl.assignTemp(t, ex))
			temps[i] = t
			continue
		}
		if needNamedSync && i < len(fl.namedResults) && fl.namedResults[i] != nil {
			out = append(out, fl.assignLocalSync(resObjs[i], fl.namedResults[i], ex)...)
			if resObjs[i] != nil {
				if rx, err := fl.readVar(resObjs[i]); err == nil {
					ex = rx
				} else {
					ex = fl.useVar(fl.namedResults[i])
				}
			} else {
				ex = fl.useVar(fl.namedResults[i])
			}
		}
		results = append(results, ex)
	}
	if stageTemps {
		for i, t := range temps {
			if i < len(fl.namedResults) && fl.namedResults[i] != nil {
				out = append(out, fl.assignLocalSync(resObjs[i], fl.namedResults[i], fl.useTemp(t))...)
			}
			results = append(results, fl.useTemp(t))
		}
	}
	// Materialize non-Name results into temps before the Return node.
	// applyDeferPass inserts runDefers immediately before Return; RuntimeOps
	// expands high ops (e.g. OpDeref / nil-check for *p) as stmts prepended to
	// their Assign. Leaving high ops in Return.Results made RuntimeOps expand
	// them AFTER runDefers (issue43835: recover defer ran before *p panicked,
	// then the handler saw a nulled defer list and rethrew).
	//
	// Already-Name results (temps/vars) need no staging — re-wrapping them
	// (%t2 = %t1; return %t2) breaks rtlink.specializeMapHashes, which rewrites
	// `h = mapKeyHashFallthrough(...); return h` and would leave the forward
	// assign referencing a deleted temp.
	if len(results) > 0 && !stageTemps {
		// Stage high ops only (RuntimeOps expands OpDeref etc. as stmts before
		// the Assign). applyDeferPass inserts runDefers immediately before
		// Return; leaving high ops in Return.Results made RuntimeOps expand
		// them AFTER runDefers (issue43835). Low ops / Name / Const stay inline.
		needsStage := false
		for _, ex := range results {
			if in, ok := ex.(*ir.Instr); ok && in.Op.IsHigh() {
				needsStage = true
				break
			}
		}
		if needsStage {
			staged := make([]ir.Expr, len(results))
			for i, ex := range results {
				in, isHigh := ex.(*ir.Instr)
				if !isHigh || !in.Op.IsHigh() {
					staged[i] = ex
					continue
				}
				want := ir.Type(ir.TypeI32)
				if i < len(fl.results) && fl.results[i] != nil {
					want = fl.results[i]
				} else if in.Type != nil {
					want = in.Type
				}
				t := fl.newTemp(want)
				out = append(out, fl.assignTemp(t, ex))
				staged[i] = fl.useTemp(t)
			}
			results = staged
		}
	}
	out = append(out, &ir.Return{Results: results})
	return out, nil
}

func ifInitScopeVars(fl *funcLower, init ast.Stmt) []types.Object {
	as, ok := init.(*ast.AssignStmt)
	if !ok || as.Tok != token.DEFINE {
		return nil
	}
	var objs []types.Object
	for _, lhs := range as.Lhs {
		id, ok := lhs.(*ast.Ident)
		if !ok {
			continue
		}
		if obj := fl.info.Defs[id]; obj != nil {
			objs = append(objs, obj)
		}
	}
	return objs
}

func (fl *funcLower) ifStmt(s *ast.IfStmt) ([]ir.Stmt, error) {
	initScope := ifInitScopeVars(fl, s.Init)
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
	for _, obj := range initScope {
		delete(fl.vars, obj)
	}
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
	// Body-declared variables are per-iteration: closures must capture by
	// value (the iteration's binding), not a shared cell updated each trip
	// (escape.go for_escapes3: p := new(int) inside the loop).
	fl.markForBodyIterVars(s.Body)

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
	var savedGoto string
	if goLabel != "" {
		if lt := fl.labels[goLabel]; lt != nil {
			savedGoto = lt.gotoLabel
		}
		fl.labels[goLabel] = &labelTargets{
			breakLabel: breakL, continueLabel: contL, gotoLabel: savedGoto,
		}
	}
	defer func() {
		fl.breakStack = fl.breakStack[:len(fl.breakStack)-1]
		fl.continueStack = fl.continueStack[:len(fl.continueStack)-1]
		fl.deferInLoop = prevDeferLoop
		if goLabel != "" {
			if savedGoto != "" {
				fl.labels[goLabel] = &labelTargets{gotoLabel: savedGoto}
			} else {
				delete(fl.labels, goLabel)
			}
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

// rangeStmt lowers for-range over slices/arrays (index loop), maps, and
// channels (recv until !ok). Strings wait for a later milestone.
func isBlankIdent(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == "_"
}

// emitRangeAssignForm evaluates LHS addresses first, then assigns key/val
// left-to-right (Go parallel assignment; needed for `i, x[i] = range`).
func (fl *funcLower) emitRangeAssignForm(
	s *ast.RangeStmt,
	needKey, needVal bool,
	keyEx, valEx ir.Expr,
	keyObj types.Object, keyVar *ir.Var,
	valObj types.Object, valVar *ir.Var,
) ([]ir.Stmt, error) {
	var out []ir.Stmt
	// Plains may be ahead of cells (cellInit at fn start vs later i:=1). Refresh
	// cells before prepare so IndexExpr indices see the pre-assign values.
	out = append(out, fl.syncPlainToCells()...)
	var keyTmp, valTmp *ir.Temp
	if needKey {
		kt := ir.Type(ir.TypeI32)
		if keyVar != nil {
			kt = keyVar.Typ
		}
		keyTmp = fl.newTemp(kt)
		out = append(out, fl.assignTemp(keyTmp, keyEx))
	}
	if needVal {
		vt := ir.Type(ir.TypeI32)
		if valVar != nil {
			vt = valVar.Typ
		}
		valTmp = fl.newTemp(vt)
		out = append(out, fl.assignTemp(valTmp, valEx))
	}
	type prepared struct {
		lhs  ast.Expr
		prep []ir.Stmt
		base ir.Expr
		idx  ir.Expr
		kind int
		arr  ir.Type
	}
	var preps []prepared
	prepare := func(lhs ast.Expr) error {
		if ix, ok := lhs.(*ast.IndexExpr); ok {
			xt := fl.typeOf(ix.X)
			if isSlice(xt) || isArray(xt) || isPtrToArray(xt) {
				base, stmts, err := fl.expr(ix.X, nil)
				if err != nil {
					return err
				}
				idx, istmts, err := fl.expr(ix.Index, ir.TypeI32)
				if err != nil {
					return err
				}
				stmts = append(stmts, istmts...)
				if it := fl.typeOf(ix.Index); it != nil {
					if irt, err := fl.mapType(it); err == nil && irNumWidth(irt) == ir.I64 {
						idx = &ir.Instr{Op: ir.OpI32WrapI64, Args: []ir.Expr{idx}}
					}
				}
				idxT := fl.newTemp(ir.TypeI32)
				baseTy, _ := fl.mapType(xt)
				if baseTy == nil {
					baseTy = &ir.Ref{Null: true}
				}
				baseT := fl.newTemp(baseTy)
				stmts = append(stmts, fl.assignTemp(baseT, base), fl.assignTemp(idxT, idx))
				arrTy := ir.Type(ir.TypeI32)
				if isArray(xt) || isPtrToArray(xt) {
					elemT := xt
					if isPtrToArray(xt) {
						if p, ok := types.Unalias(xt).Underlying().(*types.Pointer); ok {
							elemT = p.Elem()
						}
					}
					if irt, err := fl.mapType(elemT); err == nil {
						arrTy = irt
					}
				}
				preps = append(preps, prepared{lhs: lhs, prep: stmts, base: fl.useTemp(baseT), idx: fl.useTemp(idxT), kind: 1, arr: arrTy})
				return nil
			}
		}
		preps = append(preps, prepared{lhs: lhs, kind: 0})
		return nil
	}
	if needKey {
		if err := prepare(s.Key); err != nil {
			return nil, err
		}
	}
	if needVal {
		if err := prepare(s.Value); err != nil {
			return nil, err
		}
	}
	for _, p := range preps {
		out = append(out, p.prep...)
	}
	// Index exprs may run closures that update cells (closure2 q++); keep
	// parent plains in sync so a later plain→cell sync does not wipe them.
	out = append(out, fl.syncCellsToPlain()...)
	pi := 0
	commit := func(p prepared, rhs ir.Expr, obj types.Object, v *ir.Var) error {
		if p.kind == 1 {
			if isSlice(fl.typeOf(p.lhs.(*ast.IndexExpr).X)) {
				out = append(out, &ir.ExprStmt{X: &ir.Instr{Op: ir.OpSliceSet, Args: []ir.Expr{p.base, p.idx, rhs}}})
			} else {
				out = append(out, &ir.ExprStmt{X: &ir.Instr{Op: ir.OpArraySet, Type: p.arr, Args: []ir.Expr{p.base, p.idx, rhs}}})
			}
			return nil
		}
		if v != nil {
			out = append(out, fl.assignLocalSync(obj, v, rhs)...)
			return nil
		}
		as, err := fl.assignTo(p.lhs, rhs)
		if err != nil {
			return err
		}
		out = append(out, as...)
		return nil
	}
	if needKey {
		if err := commit(preps[pi], fl.useTemp(keyTmp), keyObj, keyVar); err != nil {
			return nil, err
		}
		pi++
	}
	if needVal {
		if err := commit(preps[pi], fl.useTemp(valTmp), valObj, valVar); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (fl *funcLower) rangeStmt(s *ast.RangeStmt, goLabel string) ([]ir.Stmt, error) {
	prevDeferLoop := fl.deferInLoop
	fl.deferInLoop = true
	defer func() { fl.deferInLoop = prevDeferLoop }()
	xType := fl.typeOf(s.X)
	if xType == nil {
		return nil, fl.errorf(s.X, "range: no type")
	}
	u := xType.Underlying()
	switch u := u.(type) {
	case *types.Slice, *types.Array:
		// ok
	case *types.Pointer:
		// for range over *[N]T — same IR as [N]T (shared array ref).
		if _, ok := types.Unalias(u.Elem()).Underlying().(*types.Array); !ok {
			return nil, fl.errorf(s.X, "range over %s not supported", xType)
		}
	case *types.Map:
		return fl.rangeMapStmt(s, goLabel)
	case *types.Chan:
		return fl.rangeChanStmt(s, goLabel)
	case *types.Signature:
		return fl.rangeFuncStmt(s, goLabel)
	case *types.Basic:
		if u.Info()&types.IsInteger != 0 {
			return fl.rangeIntStmt(s, goLabel)
		}
		if u.Info()&types.IsString != 0 {
			return fl.rangeStringStmt(s, goLabel)
		}
		return nil, fl.errorf(s.X, "range over %s not supported", xType)
	default:
		return nil, fl.errorf(s.X, "range over %s not supported", xType)
	}
	xIR, err := fl.mapType(xType)
	if err != nil {
		return nil, fl.errorf(s.X, "%v", err)
	}
	// range (*p) / range p for *[N]T: length comes from the type.
	// For a variable p, *p is not evaluated (issue72844/73476). For a call
	// or other non-name (*f()), evaluate * and nil-check (testRange1).
	// Value access nil-checks below when needVal even for variable p.
	xExpr := s.X
	for {
		if p, ok := xExpr.(*ast.ParenExpr); ok {
			xExpr = p.X
			continue
		}
		break
	}
	var x ir.Expr
	var xstmts []ir.Stmt
	rangeViaNilableArrayPtr := isPtrToArray(xType)
	if star, ok := xExpr.(*ast.StarExpr); ok {
		if pt := fl.typeOf(star.X); pt != nil && isPtrToArray(pt) {
			ptrTy, _ := fl.mapType(pt)
			if isSimpleName(star.X) {
				x, xstmts, err = fl.expr(star.X, ptrTy)
			} else {
				// Non-name (*f()): evaluate * with nil check (issue72844).
				x, xstmts, err = fl.starExpr(star, nil)
			}
			rangeViaNilableArrayPtr = true
		}
	}
	if x == nil {
		x, xstmts, err = fl.expr(s.X, xIR)
	}
	if err != nil {
		return nil, err
	}
	var out []ir.Stmt
	out = append(out, xstmts...)
	xv := fl.newVar("rng", xIR)
	out = append(out, fl.assignVar(xv, x))

	// [N]T and *[N]T: length is N even when the pointer is nil (Go spec).
	// OpLen on a null array ref would trap; use the type's constant size.
	lenV := fl.newVar("rlen", ir.TypeI32)
	if n, ok := arrayRangeLen(xType); ok {
		out = append(out, fl.assignVar(lenV, fl.i32const(n)))
	} else {
		out = append(out, fl.assignVar(lenV, &ir.Instr{Op: ir.OpLen, Args: []ir.Expr{fl.useVar(xv)}}))
	}

	i := fl.newVar("ri", ir.TypeI32)
	out = append(out, fl.assignVar(i, fl.i32const(0)))

	// Bind range vars if present. := keys can alias the index local when the
	// key is a plain int; assign-form / iface keys need an explicit write each
	// iteration (with boxing when ranging into any/iface).
	var keyVar *ir.Var
	var keyObj, valObj types.Object
	keyAliasIndex := false
	if s.Key != nil {
		if id, ok := s.Key.(*ast.Ident); ok && id.Name != "_" {
			if obj := fl.info.Defs[id]; obj != nil {
				keyObj = obj
				fl.markRangeIterVar(keyObj)
				keyGo := keyObj.Type()
				if len(fl.pkg.subst) > 0 {
					keyGo = fl.pkg.subst.Apply(keyGo)
				}
				if keyGo != nil && !isIface(keyGo) {
					fl.vars[keyObj] = i
					keyAliasIndex = true
				}
			} else if obj := fl.info.Uses[id]; obj != nil {
				keyObj = obj
				fl.markAssigned(obj)
				fl.ensureRangeAssignCell(obj)
			}
			if !keyAliasIndex {
				v, defs, err := fl.defineLocal(id)
				if err != nil {
					return nil, err
				}
				out = append(out, defs...)
				keyVar = v
			}
		}
	}
	var valVar *ir.Var
	if s.Value != nil {
		if id, ok := s.Value.(*ast.Ident); ok && id.Name != "_" {
			if obj := fl.info.Defs[id]; obj != nil {
				valObj = obj
				fl.markRangeIterVar(valObj)
			} else if obj := fl.info.Uses[id]; obj != nil {
				valObj = obj
				fl.markAssigned(obj)
				fl.ensureRangeAssignCell(obj)
			}
			v, defs, err := fl.defineLocal(id)
			if err != nil {
				return nil, err
			}
			out = append(out, defs...)
			valVar = v
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
	// if i >= len { br break }
	bodySeq.Stmts = append(bodySeq.Stmts, &ir.Br{
		Label: breakL,
		Cond:  &ir.Instr{Op: ir.OpI32GeU, Args: []ir.Expr{fl.useVar(i), fl.useVar(lenV)}},
	})
	keyEx := ir.Expr(fl.useVar(i))
	var elem ir.Expr
	needVal := s.Value != nil && !isBlankIdent(s.Value)
	needKey := s.Key != nil && !isBlankIdent(s.Key)
	// Value access over nil *[N]T / (*nil) panics; index-only range does not.
	if needVal && rangeViaNilableArrayPtr {
		bodySeq.Stmts = append(bodySeq.Stmts, &ir.If{
			Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{fl.useVar(xv)}},
			Then: &ir.Seq{Stmts: []ir.Stmt{
				&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: "runtime.panicNil"}},
				&ir.Unreachable{},
			}},
		})
	}
	var elemIR ir.Type
	if needVal || (needKey && keyVar == nil && s.Tok != token.DEFINE) {
		elemGo := rangeElemType(xType)
		elemIR, _ = fl.mapType(elemGo)
		if elemIR == nil && valVar != nil {
			elemIR = valVar.Typ
		}
		if isArray(xType) || isPtrToArray(xType) {
			arrName := ""
			arrT := xType
			if isPtrToArray(xType) {
				if p, ok := types.Unalias(xType).Underlying().(*types.Pointer); ok {
					arrT = p.Elem()
				}
			}
			if irt, err := fl.mapType(arrT); err == nil {
				if r, ok := irt.(*ir.Ref); ok {
					arrName = r.Named
				}
			}
			at := elemIR
			if arrName != "" {
				at = &ir.Ref{Named: arrName}
			}
			op := ir.OpArrayGet
			switch arrName {
			case "$go.bytes":
				op = ir.OpArrayGetU
			case "$go.i16s":
				if et := arrayElemType(arrT); et != nil && isUnsigned(et) {
					op = ir.OpArrayGetU
				} else {
					op = ir.OpArrayGetS
				}
			}
			elem = &ir.Instr{Op: op, Type: at, Args: []ir.Expr{fl.useVar(xv), fl.useVar(i)}}
		} else {
			elem = &ir.Instr{Op: ir.OpSliceGet, Type: elemIR, Args: []ir.Expr{fl.useVar(xv), fl.useVar(i)}}
		}
		if valVar != nil {
			if valGo := fl.goTypeOfLocal(valVar); valGo != nil && isIface(valGo) && !isTypeParam(valGo) &&
				elemGo != nil && !isIface(elemGo) && !isTypeParam(elemGo) {
				elem = fl.boxIRToIface(elem, elemGo, valGo)
			}
		}
	}

	assignForm := s.Tok != token.DEFINE
	if assignForm && (needKey || needVal) {
		var kx ir.Expr = keyEx
		if keyVar != nil {
			if keyGo := fl.goTypeOfLocal(keyVar); keyGo != nil && isIface(keyGo) && !isTypeParam(keyGo) {
				kx = fl.boxIRToIface(keyEx, types.Typ[types.Int], keyGo)
			}
		}
		as, err := fl.emitRangeAssignForm(s, needKey, needVal, kx, elem, keyObj, keyVar, valObj, valVar)
		if err != nil {
			return nil, err
		}
		bodySeq.Stmts = append(bodySeq.Stmts, as...)
	} else {
		if keyVar != nil {
			kx := keyEx
			if keyGo := fl.goTypeOfLocal(keyVar); keyGo != nil && isIface(keyGo) && !isTypeParam(keyGo) {
				kx = fl.boxIRToIface(keyEx, types.Typ[types.Int], keyGo)
			}
			bodySeq.Stmts = append(bodySeq.Stmts, fl.assignVar(keyVar, kx))
		}
		if valVar != nil {
			bodySeq.Stmts = append(bodySeq.Stmts, fl.assignVar(valVar, elem))
		}
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

// rangeStringStmt lowers `for i, r := range s` / `for range s` over UTF-8 runes.
func (fl *funcLower) rangeStringStmt(s *ast.RangeStmt, goLabel string) ([]ir.Stmt, error) {
	strTy := &ir.Ref{Named: "$go.string"}
	x, xstmts, err := fl.expr(s.X, strTy)
	if err != nil {
		return nil, err
	}
	var out []ir.Stmt
	out = append(out, xstmts...)
	sv := fl.newVar("rs", strTy)
	out = append(out, fl.assignVar(sv, x))
	lenV := fl.newVar("rslen", ir.TypeI32)
	out = append(out, fl.assignVar(lenV, &ir.Instr{
		Op: ir.OpStructGet, Type: strTy, Field: "len", Args: []ir.Expr{fl.useVar(sv)},
	}))
	i := fl.newVar("rsi", ir.TypeI32)
	out = append(out, fl.assignVar(i, fl.i32const(0)))

	needRune := s.Value != nil && !isBlankIdent(s.Value)
	assignForm := s.Tok != token.DEFINE
	var runeV *ir.Var
	var keyObj, valObj types.Object
	var keyVar, valVar *ir.Var
	keyAliasIndex := false
	if s.Key != nil {
		if id, ok := s.Key.(*ast.Ident); ok && id.Name != "_" {
			if obj := fl.info.Defs[id]; obj != nil {
				keyObj = obj
				fl.markRangeIterVar(obj)
				fl.vars[obj] = i
				keyAliasIndex = true
			} else if obj := fl.info.Uses[id]; obj != nil {
				keyObj = obj
				fl.markAssigned(obj)
				fl.ensureRangeAssignCell(obj)
			}
			if !keyAliasIndex {
				v, defs, err := fl.defineLocal(id)
				if err != nil {
					return nil, err
				}
				out = append(out, defs...)
				keyVar = v
			}
		}
	}
	if needRune {
		if id, ok := s.Value.(*ast.Ident); ok && id.Name != "_" {
			if obj := fl.info.Defs[id]; obj != nil {
				valObj = obj
				v, defs, err := fl.defineLocal(id)
				if err != nil {
					return nil, err
				}
				out = append(out, defs...)
				valVar = v
				runeV = v
			} else if obj := fl.info.Uses[id]; obj != nil {
				valObj = obj
				v, defs, err := fl.defineLocal(id)
				if err != nil {
					return nil, err
				}
				out = append(out, defs...)
				valVar = v
			}
		}
		if runeV == nil {
			runeV = fl.newVar("rr", ir.TypeI32)
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
	bodySeq.Stmts = append(bodySeq.Stmts, &ir.Br{
		Label: breakL,
		Cond:  &ir.Instr{Op: ir.OpI32GeU, Args: []ir.Expr{fl.useVar(i), fl.useVar(lenV)}},
	})

	// Decode one UTF-8 rune at s[i]; size in sz, rune in runeV when needed.
	// Must match unicode/utf8.DecodeRuneInString (invalid starters are size 1).
	sz := fl.newVar("rsz", ir.TypeI32)
	b0 := fl.newVar("rb0", ir.TypeI32)
	bodySeq.Stmts = append(bodySeq.Stmts, fl.assignVar(b0, &ir.Instr{
		Op: ir.OpStringGet, Args: []ir.Expr{fl.useVar(sv), fl.useVar(i)},
	}))
	// Default: single byte (ASCII or invalid).
	bodySeq.Stmts = append(bodySeq.Stmts, fl.assignVar(sz, fl.i32const(1)))
	if runeV != nil {
		bodySeq.Stmts = append(bodySeq.Stmts, fl.assignVar(runeV, fl.useVar(b0)))
	}
	// Continuation bytes 0x80–0xBF and 0xF8+ are invalid starters → RuneError size 1.
	// 0xC0–0xDF → 2-byte; 0xE0–0xEF → 3-byte; 0xF0–0xF7 → 4-byte (validated in utf8Advance).
	runeErr := fl.i32const(0xFFFD)
	invalid := &ir.Seq{}
	if runeV != nil {
		invalid.Stmts = append(invalid.Stmts, fl.assignVar(runeV, runeErr))
	}
	bodySeq.Stmts = append(bodySeq.Stmts, &ir.If{
		Cond: &ir.Instr{Op: ir.OpI32LtU, Args: []ir.Expr{fl.useVar(b0), fl.i32const(0x80)}},
		Then: &ir.Seq{}, // ASCII already set
		Else: &ir.Seq{Stmts: []ir.Stmt{&ir.If{
			Cond: &ir.Instr{Op: ir.OpI32LtU, Args: []ir.Expr{fl.useVar(b0), fl.i32const(0xC0)}},
			Then: invalid, // continuation byte as starter
			Else: &ir.Seq{Stmts: []ir.Stmt{&ir.If{
				Cond: &ir.Instr{Op: ir.OpI32LtU, Args: []ir.Expr{fl.useVar(b0), fl.i32const(0xE0)}},
				Then: &ir.Seq{Stmts: fl.utf8Advance(sv, i, lenV, sz, runeV, 2)},
				Else: &ir.Seq{Stmts: []ir.Stmt{&ir.If{
					Cond: &ir.Instr{Op: ir.OpI32LtU, Args: []ir.Expr{fl.useVar(b0), fl.i32const(0xF0)}},
					Then: &ir.Seq{Stmts: fl.utf8Advance(sv, i, lenV, sz, runeV, 3)},
					Else: &ir.Seq{Stmts: []ir.Stmt{&ir.If{
						Cond: &ir.Instr{Op: ir.OpI32LtU, Args: []ir.Expr{fl.useVar(b0), fl.i32const(0xF8)}},
						Then: &ir.Seq{Stmts: fl.utf8Advance(sv, i, lenV, sz, runeV, 4)},
						Else: invalid, // 0xF8–0xFF
					}}},
				}}},
			}}},
		}}},
	})

	// Assign-form: copy iteration key/value into LHS (with parallel IndexExpr prep).
	if assignForm {
		needKey := s.Key != nil && !isBlankIdent(s.Key)
		needVal := needRune
		as, err := fl.emitRangeAssignForm(s, needKey, needVal, fl.useVar(i), func() ir.Expr {
			if runeV != nil {
				return fl.useVar(runeV)
			}
			return fl.i32const(0)
		}(), keyObj, keyVar, valObj, valVar)
		if err != nil {
			return nil, err
		}
		bodySeq.Stmts = append(bodySeq.Stmts, as...)
	}

	inner, err := fl.stmtBlock(s.Body)
	if err != nil {
		return nil, err
	}
	bodySeq.Stmts = append(bodySeq.Stmts, &ir.Block{Name: contL, Body: inner})
	bodySeq.Stmts = append(bodySeq.Stmts, fl.assignVar(i, &ir.Instr{
		Op: ir.OpI32Add, Args: []ir.Expr{fl.useVar(i), fl.useVar(sz)},
	}))
	bodySeq.Stmts = append(bodySeq.Stmts, &ir.Br{Label: loopL})
	loop := &ir.Loop{Name: loopL, Body: bodySeq}
	out = append(out, &ir.Block{Name: breakL, Body: &ir.Seq{Stmts: []ir.Stmt{loop}}})
	return out, nil
}

// utf8Advance validates and decodes an n-byte UTF-8 sequence at s[i].
// On short buffer, bad continuation, overlong, surrogate, or out-of-range:
// size 1 + RuneError (matches unicode/utf8.DecodeRuneInString).
func (fl *funcLower) utf8Advance(sv, i, lenV, sz, runeV *ir.Var, n int) []ir.Stmt {
	runeErr := fl.i32const(0xFFFD)
	failStmts := []ir.Stmt{fl.assignVar(sz, fl.i32const(1))}
	if runeV != nil {
		failStmts = append(failStmts, fl.assignVar(runeV, runeErr))
	}
	failSeq := &ir.Seq{Stmts: failStmts}

	byteAt := func(off int64) ir.Expr {
		idx := ir.Expr(fl.useVar(i))
		if off != 0 {
			idx = &ir.Instr{Op: ir.OpI32Add, Args: []ir.Expr{fl.useVar(i), fl.i32const(off)}}
		}
		return &ir.Instr{Op: ir.OpStringGet, Args: []ir.Expr{fl.useVar(sv), idx}}
	}
	contOK := func(off int64) ir.Expr {
		b := byteAt(off)
		return &ir.Instr{Op: ir.OpI32And, Args: []ir.Expr{
			&ir.Instr{Op: ir.OpI32GeU, Args: []ir.Expr{b, fl.i32const(0x80)}},
			&ir.Instr{Op: ir.OpI32LeU, Args: []ir.Expr{b, fl.i32const(0xBF)}},
		}}
	}

	var decode ir.Expr
	b0 := byteAt(0)
	switch n {
	case 2:
		b1 := byteAt(1)
		decode = &ir.Instr{Op: ir.OpI32Or, Args: []ir.Expr{
			&ir.Instr{Op: ir.OpI32Shl, Args: []ir.Expr{
				&ir.Instr{Op: ir.OpI32And, Args: []ir.Expr{b0, fl.i32const(0x1F)}},
				fl.i32const(6),
			}},
			&ir.Instr{Op: ir.OpI32And, Args: []ir.Expr{b1, fl.i32const(0x3F)}},
		}}
	case 3:
		b1, b2 := byteAt(1), byteAt(2)
		decode = &ir.Instr{Op: ir.OpI32Or, Args: []ir.Expr{
			&ir.Instr{Op: ir.OpI32Or, Args: []ir.Expr{
				&ir.Instr{Op: ir.OpI32Shl, Args: []ir.Expr{
					&ir.Instr{Op: ir.OpI32And, Args: []ir.Expr{b0, fl.i32const(0x0F)}},
					fl.i32const(12),
				}},
				&ir.Instr{Op: ir.OpI32Shl, Args: []ir.Expr{
					&ir.Instr{Op: ir.OpI32And, Args: []ir.Expr{b1, fl.i32const(0x3F)}},
					fl.i32const(6),
				}},
			}},
			&ir.Instr{Op: ir.OpI32And, Args: []ir.Expr{b2, fl.i32const(0x3F)}},
		}}
	case 4:
		b1, b2, b3 := byteAt(1), byteAt(2), byteAt(3)
		decode = &ir.Instr{Op: ir.OpI32Or, Args: []ir.Expr{
			&ir.Instr{Op: ir.OpI32Or, Args: []ir.Expr{
				&ir.Instr{Op: ir.OpI32Or, Args: []ir.Expr{
					&ir.Instr{Op: ir.OpI32Shl, Args: []ir.Expr{
						&ir.Instr{Op: ir.OpI32And, Args: []ir.Expr{b0, fl.i32const(0x07)}},
						fl.i32const(18),
					}},
					&ir.Instr{Op: ir.OpI32Shl, Args: []ir.Expr{
						&ir.Instr{Op: ir.OpI32And, Args: []ir.Expr{b1, fl.i32const(0x3F)}},
						fl.i32const(12),
					}},
				}},
				&ir.Instr{Op: ir.OpI32Shl, Args: []ir.Expr{
					&ir.Instr{Op: ir.OpI32And, Args: []ir.Expr{b2, fl.i32const(0x3F)}},
					fl.i32const(6),
				}},
			}},
			&ir.Instr{Op: ir.OpI32And, Args: []ir.Expr{b3, fl.i32const(0x3F)}},
		}}
	default:
		return failStmts
	}

	minRune := int64(0x80)
	if n == 3 {
		minRune = 0x800
	} else if n == 4 {
		minRune = 0x10000
	}
	rTmp := fl.newVar("rdec", ir.TypeI32)
	okStmts := []ir.Stmt{
		fl.assignVar(sz, fl.i32const(int64(n))),
		fl.assignVar(rTmp, decode),
	}
	if runeV != nil {
		okStmts = append(okStmts, fl.assignVar(runeV, fl.useVar(rTmp)))
	}
	okSeq := &ir.Seq{Stmts: okStmts}

	notSur := &ir.Instr{Op: ir.OpI32Or, Args: []ir.Expr{
		&ir.Instr{Op: ir.OpI32LtU, Args: []ir.Expr{fl.useVar(rTmp), fl.i32const(0xD800)}},
		&ir.Instr{Op: ir.OpI32GtU, Args: []ir.Expr{fl.useVar(rTmp), fl.i32const(0xDFFF)}},
	}}
	rangeOK := &ir.Instr{Op: ir.OpI32And, Args: []ir.Expr{
		&ir.Instr{Op: ir.OpI32And, Args: []ir.Expr{
			&ir.Instr{Op: ir.OpI32GeU, Args: []ir.Expr{fl.useVar(rTmp), fl.i32const(minRune)}},
			&ir.Instr{Op: ir.OpI32LeU, Args: []ir.Expr{fl.useVar(rTmp), fl.i32const(0x10FFFF)}},
		}},
		notSur,
	}}
	// Evaluate decode into rTmp before rangeOK reads it.
	pre := []ir.Stmt{fl.assignVar(rTmp, decode)}
	validated := &ir.If{Cond: rangeOK, Then: okSeq, Else: failSeq}

	var cont ir.Expr = fl.i32const(1)
	for off := int64(1); off < int64(n); off++ {
		cont = &ir.Instr{Op: ir.OpI32And, Args: []ir.Expr{cont, contOK(off)}}
	}
	afterCont := &ir.If{
		Cond: cont,
		Then: &ir.Seq{Stmts: append(pre, validated)},
		Else: failSeq,
	}
	short := &ir.Instr{Op: ir.OpI32GtU, Args: []ir.Expr{
		&ir.Instr{Op: ir.OpI32Add, Args: []ir.Expr{fl.useVar(i), fl.i32const(int64(n))}},
		fl.useVar(lenV),
	}}
	return []ir.Stmt{&ir.If{Cond: short, Then: failSeq, Else: &ir.Seq{Stmts: []ir.Stmt{afterCont}}}}
}

// rangeIntStmt lowers Go 1.22+ `for i := range n` (n integer) to 0..n-1.
func (fl *funcLower) rangeIntStmt(s *ast.RangeStmt, goLabel string) ([]ir.Stmt, error) {
	if s.Value != nil {
		return nil, fl.errorf(s, "range over int allows at most one iteration variable")
	}
	xIR, err := fl.mapType(fl.typeOf(s.X))
	if err != nil {
		return nil, fl.errorf(s.X, "%v", err)
	}
	nEx, xstmts, err := fl.expr(s.X, xIR)
	if err != nil {
		return nil, err
	}
	var out []ir.Stmt
	out = append(out, xstmts...)
	nV := fl.newVar("rn", ir.TypeI32)
	// Truncate/widen to i32 for the loop bound.
	if irNumWidth(xIR) == ir.I64 {
		out = append(out, fl.assignVar(nV, &ir.Instr{Op: ir.OpI32WrapI64, Args: []ir.Expr{nEx}}))
	} else {
		out = append(out, fl.assignVar(nV, nEx))
	}
	i := fl.newVar("ri", ir.TypeI32)
	out = append(out, fl.assignVar(i, fl.i32const(0)))
	if s.Key != nil {
		if id, ok := s.Key.(*ast.Ident); ok && id.Name != "_" {
			if obj := fl.info.Defs[id]; obj != nil {
				fl.markRangeIterVar(obj)
				fl.vars[obj] = i
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

	// Signed bounds: `range -1` must not run (Go spec). Unsigned keeps ge_u.
	geOp := ir.OpI32GeS
	if isUnsigned(fl.typeOf(s.X)) {
		geOp = ir.OpI32GeU
	}
	bodySeq := &ir.Seq{}
	bodySeq.Stmts = append(bodySeq.Stmts, &ir.Br{
		Label: breakL,
		Cond:  &ir.Instr{Op: geOp, Args: []ir.Expr{fl.useVar(i), fl.useVar(nV)}},
	})
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
			v, defs, err := fl.defineLocal(id)
			if err != nil {
				return nil, err
			}
			out = append(out, defs...)
			valVar = v
			if v != nil {
				valTy = v.Typ
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
		Cond:  &ir.Instr{Op: ir.OpI32Eqz, Args: []ir.Expr{fl.useTemp(ot)}},
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
		// Handled by switchStmt via body merging; stray fallthrough is invalid.
		return nil, fl.errorf(s, "fallthrough outside switch case")
	case token.GOTO:
		if s.Label == nil {
			return nil, fl.errorf(s, "goto without label")
		}
		lt, ok := fl.labels[s.Label.Name]
		if !ok || lt.gotoLabel == "" {
			return nil, fl.errorf(s, "goto %s: unknown or non-forward label", s.Label.Name)
		}
		if fl.gotoDispatch != nil && lt.gotoArm >= 0 {
			return []ir.Stmt{
				fl.assignVar(fl.gotoDispatch.State, fl.i32const(int64(lt.gotoArm))),
				&ir.Br{Label: fl.gotoDispatch.Loop},
			}, nil
		}
		return []ir.Stmt{&ir.Br{Label: lt.gotoLabel}}, nil
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
	// complex64/128 ++/--: add/sub 1+0i (Ref IR has no numeric width).
	if isComplex(ty) {
		name, elem := complexTypeShape(ty, want)
		fl.ensureComplexType(name, elem)
		cWant := &ir.Ref{Null: true, Named: name}
		lhs, stmts, err := fl.expr(s.X, cWant)
		if err != nil {
			return nil, err
		}
		var oneRe ir.Expr
		var add, sub ir.Op
		if elem == ir.TypeF32 {
			oneRe, add, sub = fl.f32const(1), ir.OpF32Add, ir.OpF32Sub
		} else {
			oneRe, add, sub = fl.f64const(1), ir.OpF64Add, ir.OpF64Sub
		}
		op := add
		if s.Tok != token.INC {
			op = sub
		}
		re := &ir.Instr{Op: ir.OpStructGet, Type: cWant, Field: "real", Args: []ir.Expr{lhs}}
		im := &ir.Instr{Op: ir.OpStructGet, Type: cWant, Field: "imag", Args: []ir.Expr{lhs}}
		rhs := &ir.Instr{
			Op: ir.OpStructNew, Type: cWant,
			Args: []ir.Expr{
				&ir.Instr{Op: op, Args: []ir.Expr{re, oneRe}},
				im,
			},
		}
		as, err := fl.assignTo(s.X, rhs)
		if err != nil {
			return nil, err
		}
		return append(stmts, as...), nil
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
	// *p++ / x[i]++ / m[k]++: evaluate destination once (bug196 f()[0]++).
	tok := token.ADD_ASSIGN
	if s.Tok != token.INC {
		tok = token.SUB_ASSIGN
	}
	if as, ok, err := fl.opAssignAddrOnceIR(s.X, tok, one, want); ok || err != nil {
		if err != nil {
			return nil, err
		}
		return as, nil
	}
	lhs, stmts, err := fl.expr(s.X, want)
	if err != nil {
		return nil, err
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
				// Box concrete case values into the tag's interface type
				// (e.g. strconv.toError switches on error vs internal/strconv.Error ints).
				ex, stmts, err := fl.coerceExpr(c, tagType, want)
				if err != nil {
					return nil, err
				}
				arm.pre = append(arm.pre, stmts...)
				arm.values = append(arm.values, ex)
			}
		}
		arms = append(arms, arm)
	}

	// Dense integer switch → IR Switch; else if-chain.
	// Fallthrough disables dense form (bodies are merged in the if-chain).
	if s.Tag != nil && isInteger(tagType) && canDenseSwitch(arms) && !anyFallthrough(arms) && !anyCasePre(arms) {
		sw, allTerm, err := fl.emitDenseSwitch(tag, arms, breakL)
		if err != nil {
			return nil, err
		}
		out = append(out, &ir.Block{Name: breakL, Body: &ir.Seq{Stmts: []ir.Stmt{sw}}})
		if len(fl.results) > 0 && allTerm {
			out = append(out, &ir.Unreachable{})
		}
		return out, nil
	}

	chain, allTerm, err := fl.emitSwitchIfChain(tag, s.Tag == nil, tagType, arms, breakL)
	if err != nil {
		return nil, err
	}
	out = append(out, &ir.Block{Name: breakL, Body: &ir.Seq{Stmts: chain}})
	// When every case returns/unreachable, fallthru after the break block
	// leaves no result on the stack; Wasm still expects the function result.
	if len(fl.results) > 0 && allTerm {
		out = append(out, &ir.Unreachable{})
	}
	return out, nil
}

type caseArm struct {
	values []ir.Expr  // for tagged switch
	pre    []ir.Stmt  // stmts from evaluating case values
	conds  []ast.Expr // for switch without tag
	body   *ast.BlockStmt
	def    bool
}

func caseHasFallthrough(body *ast.BlockStmt) bool {
	if body == nil || len(body.List) == 0 {
		return false
	}
	last := body.List[len(body.List)-1]
	br, ok := last.(*ast.BranchStmt)
	return ok && br.Tok == token.FALLTHROUGH
}

func stripFallthrough(body *ast.BlockStmt) *ast.BlockStmt {
	if !caseHasFallthrough(body) {
		return body
	}
	return &ast.BlockStmt{List: body.List[:len(body.List)-1]}
}

func anyFallthrough(arms []caseArm) bool {
	for _, a := range arms {
		if caseHasFallthrough(a.body) {
			return true
		}
	}
	return false
}

// allCasesReturn reports whether every case/default body ends with a return
// (no break out of the switch). Used to insert unreachable after the switch
// block in functions that return a value.
func allCasesReturn(arms []caseArm) bool {
	if len(arms) == 0 {
		return false
	}
	for _, a := range arms {
		if caseHasFallthrough(a.body) {
			return false
		}
		if !astBlockEndsWithReturn(a.body) {
			return false
		}
	}
	return true
}

func astBlockEndsWithReturn(b *ast.BlockStmt) bool {
	if b == nil || len(b.List) == 0 {
		return false
	}
	last := b.List[len(b.List)-1]
	if _, ok := last.(*ast.ReturnStmt); ok {
		return true
	}
	// panic(...) never returns — treat like a return for switch fallthru.
	es, ok := last.(*ast.ExprStmt)
	if !ok {
		return false
	}
	call, ok := es.X.(*ast.CallExpr)
	if !ok {
		return false
	}
	var id *ast.Ident
	switch f := call.Fun.(type) {
	case *ast.Ident:
		id = f
	case *ast.SelectorExpr:
		id = f.Sel
	}
	return id != nil && id.Name == "panic"
}

func anyCasePre(arms []caseArm) bool {
	for _, a := range arms {
		if len(a.pre) > 0 {
			return true
		}
	}
	return false
}

// mergeFallthroughBody concatenates this case with subsequent fallthrough
// targets (the next case's condition is not re-checked).
func mergeFallthroughBody(arms []caseArm, i int) *ast.BlockStmt {
	var list []ast.Stmt
	for j := i; j < len(arms); j++ {
		b := arms[j].body
		ft := caseHasFallthrough(b)
		b = stripFallthrough(b)
		if b != nil {
			list = append(list, b.List...)
		}
		if !ft {
			break
		}
	}
	return &ast.BlockStmt{List: list}
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

func (fl *funcLower) emitDenseSwitch(tag ir.Expr, arms []caseArm, breakL string) (ir.Stmt, bool, error) {
	sw := &ir.Switch{Tag: tag, Dense: true}
	allTerm := len(arms) > 0
	hasDefault := false
	for _, a := range arms {
		if a.def {
			body, err := fl.stmtBlock(a.body)
			if err != nil {
				return nil, false, err
			}
			beforeBr := body
			body = appendBr(body, breakL)
			if !regionTerminates(beforeBr) {
				allTerm = false
			}
			hasDefault = true
			sw.Default = body
			continue
		}
		// Multi-value cases (case A, B:) need a freshly lowered body per value.
		// Sharing one Region leaves temps defined in the first arm invisible to
		// later arms (IR verify: temp used outside its defining region).
		for _, v := range a.values {
			body, err := fl.stmtBlock(a.body)
			if err != nil {
				return nil, false, err
			}
			beforeBr := body
			body = appendBr(body, breakL)
			if !regionTerminates(beforeBr) {
				allTerm = false
			}
			c := v.(*ir.Const)
			sw.Cases = append(sw.Cases, ir.Case{Value: c.I64, Body: body})
		}
	}
	// Without a default, unmatched tags exit the switch normally — do not
	// emit unreachable after the break block (strings.Repeat count switch).
	if !hasDefault {
		allTerm = false
	}
	return sw, allTerm, nil
}

func appendBr(r ir.Region, label string) ir.Region {
	seq, ok := r.(*ir.Seq)
	if !ok {
		seq = &ir.Seq{Stmts: []ir.Stmt{r}}
	}
	// Skip trailing br after return/unreachable — dead and confuses Wasm
	// block typing (br after unreachable leaves the case arm ill-typed).
	if endsWithReturn(seq) || endsWithUnreachable(seq) {
		return seq
	}
	seq.Stmts = append(seq.Stmts, &ir.Br{Label: label})
	return seq
}

func endsWithUnreachable(s *ir.Seq) bool {
	if s == nil || len(s.Stmts) == 0 {
		return false
	}
	_, ok := s.Stmts[len(s.Stmts)-1].(*ir.Unreachable)
	return ok
}

func (fl *funcLower) emitSwitchIfChain(tag ir.Expr, untag bool, tagType types.Type, arms []caseArm, breakL string) ([]ir.Stmt, bool, error) {
	// Non-default cases form the if/else chain; default is the final else.
	// Fallthrough merges still walk source order in arms (incl. default).
	defIdx := -1
	var caseIdxs []int
	for i, a := range arms {
		if a.def {
			defIdx = i
		} else {
			caseIdxs = append(caseIdxs, i)
		}
	}
	allTerm := len(arms) > 0
	var build func(k int) (ir.Region, error)
	build = func(k int) (ir.Region, error) {
		if k >= len(caseIdxs) {
			if defIdx < 0 {
				// No default: falling off the switch is a valid exit.
				allTerm = false
				return &ir.Seq{}, nil
			}
			body, err := fl.stmtBlock(mergeFallthroughBody(arms, defIdx))
			if err != nil {
				return nil, err
			}
			if !regionTerminates(body) {
				allTerm = false
			}
			return appendBr(body, breakL), nil
		}
		i := caseIdxs[k]
		a := arms[i]
		body, err := fl.stmtBlock(mergeFallthroughBody(arms, i))
		if err != nil {
			return nil, err
		}
		if !regionTerminates(body) {
			allTerm = false
		}
		body = appendBr(body, breakL)
		elseR, err := build(k + 1)
		if err != nil {
			return nil, err
		}
		var cond ir.Expr
		var pre []ir.Stmt
		if untag {
			// OR of conditions (hoist side-effect stmts before the if).
			if len(a.conds) == 0 {
				return nil, fl.errorf(a.body, "empty case")
			}
			var cstmts []ir.Stmt
			cond, cstmts, err = fl.boolExpr(a.conds[0])
			if err != nil {
				return nil, err
			}
			pre = append(pre, cstmts...)
			for _, c := range a.conds[1:] {
				ex, st, err := fl.boolExpr(c)
				if err != nil {
					return nil, err
				}
				pre = append(pre, st...)
				cond = &ir.Instr{Op: ir.OpI32Or, Args: []ir.Expr{cond, ex}}
			}
		} else {
			pre = append(pre, a.pre...)
			want, _ := fl.mapType(tagType)
			// Interface / string / ref tags cannot use i32.eq (strconv.toError
			// switches on error values; string switches need runtime.stringeq).
			eq := func(v ir.Expr) ir.Expr {
				switch {
				case isIface(tagType):
					return &ir.Instr{Op: ir.OpConv, Type: ir.TypeI32, Str: "iface.eq", Args: []ir.Expr{tag, v}}
				case isString(tagType):
					// No Type on OpCall: Format omits it and Equal would fail round-trip.
					return &ir.Instr{Op: ir.OpCall, Func: "runtime.stringeq", Args: []ir.Expr{tag, v}}
				case isStruct(tagType):
					if st, ok := types.Unalias(tagType).Underlying().(*types.Struct); ok && !structHasComparableField(st) {
						return fl.i32const(1)
					}
					return fl.valueEqCall(tagType, tag, v)
				case isArray(tagType):
					if arr, ok := types.Unalias(tagType).Underlying().(*types.Array); ok && arr.Len() == 0 {
						return fl.i32const(1)
					}
					return fl.valueEqCall(tagType, tag, v)
				case irNumWidth(want) == ir.I64:
					return &ir.Instr{Op: ir.OpI64Eq, Args: []ir.Expr{tag, v}}
				case want != nil && isRefIR(want):
					return &ir.Instr{Op: ir.OpRefEq, Args: []ir.Expr{tag, v}}
				default:
					return &ir.Instr{Op: ir.OpI32Eq, Args: []ir.Expr{tag, v}}
				}
			}
			cond = eq(a.values[0])
			for _, v := range a.values[1:] {
				cond = &ir.Instr{Op: ir.OpI32Or, Args: []ir.Expr{cond, eq(v)}}
			}
		}
		seq := append(pre, &ir.If{Cond: cond, Then: body, Else: elseR})
		return &ir.Seq{Stmts: seq}, nil
	}
	r, err := build(0)
	if err != nil {
		return nil, false, err
	}
	if seq, ok := r.(*ir.Seq); ok {
		return seq.Stmts, allTerm, nil
	}
	return []ir.Stmt{r}, allTerm, nil
}

// regionTerminates reports whether every path through r ends in return or
// unreachable (no break out to a following statement).
func regionTerminates(r ir.Region) bool {
	if r == nil {
		return false
	}
	switch x := r.(type) {
	case *ir.Seq:
		if len(x.Stmts) == 0 {
			return false
		}
		return stmtTerminates(x.Stmts[len(x.Stmts)-1])
	case *ir.Block:
		return regionTerminates(x.Body)
	case *ir.Loop:
		return regionTerminates(x.Body)
	case *ir.If:
		if x.Else == nil {
			return false
		}
		return regionTerminates(x.Then) && regionTerminates(x.Else)
	case *ir.Switch:
		ok := true
		for _, c := range x.Cases {
			if !regionTerminates(c.Body) {
				ok = false
			}
		}
		if x.Default != nil {
			ok = ok && regionTerminates(x.Default)
		} else {
			ok = false
		}
		return ok
	default:
		return stmtTerminates(x)
	}
}

func stmtTerminates(s ir.Stmt) bool {
	switch x := s.(type) {
	case *ir.Return, *ir.Unreachable:
		return true
	case *ir.Block:
		return regionTerminates(x.Body)
	case *ir.Loop:
		return regionTerminates(x.Body)
	case *ir.If:
		if x.Else == nil {
			return false
		}
		return regionTerminates(x.Then) && regionTerminates(x.Else)
	case *ir.Switch:
		return regionTerminates(x)
	case *ir.Seq:
		return regionTerminates(x)
	default:
		return false
	}
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

	// If-else chain implements case exclusivity. Break block still needed for
	// explicit break; trailing br after return/unreachable is skipped (same as
	// value switches) so multi-result functions validate.
	breakL := fl.freshLabel("tsbreak")
	fl.breakStack = append(fl.breakStack, breakL)
	defer func() { fl.breakStack = fl.breakStack[:len(fl.breakStack)-1] }()

	allTerm := len(s.Body.List) > 0
	var build func(i int) (ir.Region, error)
	build = func(i int) (ir.Region, error) {
		if i >= len(s.Body.List) {
			// No default: falling off the type switch is a valid exit.
			allTerm = false
			return &ir.Seq{}, nil
		}
		cc := s.Body.List[i].(*ast.CaseClause)
		bodySeq := &ir.Seq{}
		// Bind variable for this case when present.
		if bind != nil && bind.Name != "_" {
			var caseTy types.Type
			nilCase := false
			if len(cc.List) == 1 {
				caseTy = fl.typeOf(cc.List[0])
				if caseTy == types.Typ[types.UntypedNil] || isNilIdent(cc.List[0]) {
					nilCase = true
					caseTy = fl.typeOf(tagExpr) // nil case binds as the interface
				}
			} else if len(cc.List) == 0 {
				caseTy = fl.typeOf(tagExpr) // default: interface type
			} else {
				// Multi-type case (case T1, T2:): bind as the switch interface.
				caseTy = fl.typeOf(tagExpr)
			}
			if caseTy != nil && fl.pkg != nil && len(fl.pkg.subst) > 0 {
				caseTy = fl.pkg.subst.Apply(caseTy)
			}
			if caseTy != nil && caseTy != types.Typ[types.UntypedNil] {
				irTy, err := fl.mapType(caseTy)
				if err != nil {
					return nil, err
				}
				v := &ir.Var{Name: bind.Name, Typ: irTy}
				// Shadow per-case — unique across all type switches in the
				// function (convert5 has many switch v := x.(type) in main).
				fl.varSeq++
				v.Name = bind.Name + "_" + itoa(fl.varSeq)
				fl.extraLocals = append(fl.extraLocals, v)
				// Per-case type-switch vars live in Implicits[CaseClause], not Defs[bind].
				obj := fl.info.Implicits[cc]
				if obj == nil {
					obj = fl.info.Defs[bind]
				}
				if obj != nil {
					fl.vars[obj] = v
				}
				if len(cc.List) == 1 && !nilCase {
					assertStr := "concrete,ok"
					if isIface(caseTy) {
						// Interface case: convert via itab rewrite, not concrete
						// type-identity (errors.Is switches on Unwrap interfaces).
						assertStr = "iface,ok"
					}
					assert := &ir.Instr{
						Op:     ir.OpIfaceAssert,
						Type:   irTy,
						Global: desc.TypeKey(caseTy),
						Str:    assertStr,
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
		if !regionTerminates(bodySeq) {
			allTerm = false
		}
		bodyR := appendBr(bodySeq, breakL)

		if len(cc.List) == 0 {
			// default
			return bodyR, nil
		}
		// Match: any of the listed types (or nil interface).
		var cond ir.Expr
		for j, texpr := range cc.List {
			var cmp ir.Expr
			if isNilIdent(texpr) || fl.typeOf(texpr) == types.Typ[types.UntypedNil] {
				cmp = &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{fl.useVar(ifaceV)}}
			} else {
				ct := fl.typeOf(texpr)
				if fl.pkg != nil && len(fl.pkg.subst) > 0 {
					ct = fl.pkg.subst.Apply(ct)
				}
				key := desc.TypeKey(ct)
				testStr := "test"
				if isIface(ct) {
					// Match when the dynamic concrete implements the case iface.
					testStr = "iface,test"
				}
				cmp = &ir.Instr{
					Op:     ir.OpIfaceAssert,
					Type:   ir.TypeI32,
					Global: key,
					Str:    testStr,
					Args:   []ir.Expr{fl.useVar(ifaceV)},
				}
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
		return &ir.Seq{Stmts: []ir.Stmt{&ir.If{Cond: cond, Then: bodyR, Else: elseR}}}, nil
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
	// When every case returns/unreachable, fallthru after the break block
	// leaves no result; Wasm still expects the function results.
	if len(fl.results) > 0 && allTerm {
		out = append(out, &ir.Unreachable{})
	}
	return out, nil
}
