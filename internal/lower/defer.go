package lower

import (
	"go/ast"
	"go/types"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
)

// Defer / panic / recover lowering (docs/design/02, 04).
//
// Open-coded path: a fixed set of defers (none inside a loop) become direct
// call_ref / call sequences at every exit, with recoverOk around each call.
// Loop / dynamic path: push onto a $go.defer list and call runtime.deferRun.
// Functions that can observe a panic wrap the body in try $go.panic.

const deferTypeName = "$go.defer"
const panicObjName = "$go.panicObj"
const goPanicTag = "$go.panic"

func (l *pkgLower) ensureDeferTypes() {
	l.ensureClosBase()
	if l.mod.LookupType(panicObjName) == nil {
		l.mod.InternType(&ir.TypeDef{
			Name:  panicObjName,
			Kind:  ir.TypeStruct,
			Super: "$go.object",
			Fields: []ir.Field{
				{Name: "value", Type: typeIface},
				{Name: "recovered", Type: ir.TypeI32, Mut: true},
				{Name: "next", Type: &ir.Ref{Null: true, Named: panicObjName}},
			},
		})
	}
	if l.mod.LookupType(deferTypeName) == nil {
		// defer_ { fn: ref $go.clos, link: refnull $go.defer }
		l.mod.InternType(&ir.TypeDef{
			Name:  deferTypeName,
			Kind:  ir.TypeStruct,
			Super: "$go.object",
			Fields: []ir.Field{
				{Name: "fn", Type: &ir.Ref{Named: closBaseName}},
				{Name: "link", Type: &ir.Ref{Null: true, Named: deferTypeName}, Mut: true},
			},
		})
	}
	l.ensureGType()
	if l.mod.LookupGlobal("$g") == nil {
		l.mod.Globals = append(l.mod.Globals, &ir.Global{
			Name: "$g",
			Typ:  &ir.Ref{Null: true, Named: "$go.g"},
			Mut:  true,
			Init: &ir.Instr{Op: ir.OpRefNull, Type: &ir.Ref{Null: true, Named: "$go.g"}},
		})
	}
}

func (fl *funcLower) deferStmt(s *ast.DeferStmt) ([]ir.Stmt, error) {
	fl.pkg.ensureDeferTypes()
	fl.hasDefer = true
	if fl.deferInLoop {
		fl.deferUsesList = true
	}

	// Evaluate call arguments now; build a func() closure that performs the call.
	call := s.Call
	closEx, stmts, err := fl.deferClosure(call)
	if err != nil {
		return nil, err
	}
	if fl.defersVar == nil {
		fl.defersVar = fl.newVar("defers", &ir.Ref{Null: true, Named: deferTypeName})
	}
	// Push: defers = {fn: clos, link: defers}
	node := &ir.Instr{
		Op:   ir.OpStructNew,
		Type: &ir.Ref{Named: deferTypeName},
		Args: []ir.Expr{
			closEx,
			fl.useVar(fl.defersVar),
		},
	}
	stmts = append(stmts, fl.assignVar(fl.defersVar, node))
	fl.deferCount++
	return stmts, nil
}

// deferClosure builds a func() closure that evaluates to calling the deferred
// expression with args already evaluated (captured).
func (fl *funcLower) deferClosure(call *ast.CallExpr) (ir.Expr, []ir.Stmt, error) {
	// Simple form: defer f() or defer x.M() — capture callee + args into a thunk.
	sig := types.NewSignatureType(nil, nil, nil, nil, nil, false) // func()

	// Materialize argument values into temps, then build a func lit-like thunk.
	var stmts []ir.Stmt
	var capExprs []ir.Expr
	var capTypes []ir.Type
	var capNames []string

	addCap := func(name string, ty ir.Type, ex ir.Expr) {
		capNames = append(capNames, name)
		capTypes = append(capTypes, ty)
		capExprs = append(capExprs, ex)
	}

	// Resolve callee.
	if fl.isBuiltinCall(call) && fl.builtinName(call) == "recover" {
		// defer recover() — still a thunk that calls recover (allow cleared).
	}

	var callBuilder func(child *funcLower, clos *ir.Var, fieldOf func(string) ir.Expr) ([]ir.Stmt, error)

	switch fun := call.Fun.(type) {
	case *ast.Ident:
		if fl.isBuiltinCall(call) {
			bname := fl.builtinName(call)
			switch bname {
			case "print", "println", "panic", "recover":
				// Capture args; thunk re-emits the builtin.
				for i, a := range call.Args {
					src := fl.typeOf(a)
					var want ir.Type
					var ex ir.Expr
					var astmts []ir.Stmt
					var err error
					if bname == "panic" {
						emptyIface := types.NewInterfaceType(nil, nil)
						want = typeIface
						ex, astmts, err = fl.convertToIface(a, src, emptyIface, typeIface)
					} else {
						want, err = fl.mapType(src)
						if err != nil {
							return nil, nil, err
						}
						ex, astmts, err = fl.expr(a, want)
					}
					if err != nil {
						return nil, nil, err
					}
					stmts = append(stmts, astmts...)
					t := fl.newTemp(want)
					stmts = append(stmts, fl.assignTemp(t, ex))
					addCap("a"+deferItoa(i), want, fl.useTemp(t))
				}
				nargs := len(call.Args)
				callBuilder = func(child *funcLower, clos *ir.Var, fieldOf func(string) ir.Expr) ([]ir.Stmt, error) {
					return child.deferredBuiltin(bname, nargs, fieldOf)
				}
			default:
				return nil, nil, fl.errorf(call, "unsupported deferred builtin %s", bname)
			}
			break
		}
		obj := fl.info.Uses[fun]
		if fn, ok := obj.(*types.Func); ok {
			// Known function: capture args only; call directly.
			target := fl.pkg.funcIRNameOf(fn)
			fsig := fn.Type().(*types.Signature)
			for i, a := range call.Args {
				want, err := fl.mapType(fsig.Params().At(i).Type())
				if err != nil {
					return nil, nil, err
				}
				ex, astmts, err := fl.coerceExpr(a, fl.typeOf(a), want)
				if err != nil {
					return nil, nil, err
				}
				stmts = append(stmts, astmts...)
				t := fl.newTemp(want)
				stmts = append(stmts, fl.assignTemp(t, ex))
				addCap("a"+deferItoa(i), want, fl.useTemp(t))
			}
			callBuilder = func(child *funcLower, clos *ir.Var, fieldOf func(string) ir.Expr) ([]ir.Stmt, error) {
				var args []ir.Expr
				for i := range call.Args {
					args = append(args, fieldOf("a"+deferItoa(i)))
				}
				return []ir.Stmt{&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: target, Args: args}}, &ir.Return{}}, nil
			}
		} else if _, ok := obj.(*types.Var); ok {
			// Func value variable.
			closTy := &ir.Ref{Null: true, Named: closBaseName}
			ex, astmts, err := fl.expr(fun, closTy)
			if err != nil {
				return nil, nil, err
			}
			stmts = append(stmts, astmts...)
			t := fl.newTemp(closTy)
			stmts = append(stmts, fl.assignTemp(t, ex))
			// Capture name must not be "fn" (reserved for the funcref field).
			addCap("callee", closTy, fl.useTemp(t))
			fsig, _ := obj.Type().Underlying().(*types.Signature)
			for i, a := range call.Args {
				want, err := fl.mapType(fsig.Params().At(i).Type())
				if err != nil {
					return nil, nil, err
				}
				ex, astmts, err := fl.coerceExpr(a, fl.typeOf(a), want)
				if err != nil {
					return nil, nil, err
				}
				stmts = append(stmts, astmts...)
				t := fl.newTemp(want)
				stmts = append(stmts, fl.assignTemp(t, ex))
				addCap("a"+deferItoa(i), want, fl.useTemp(t))
			}
			callBuilder = func(child *funcLower, clos *ir.Var, fieldOf func(string) ir.Expr) ([]ir.Stmt, error) {
				return child.emitCallRefStmts(fieldOf("callee"), fsig, func(i int) ir.Expr { return fieldOf("a" + deferItoa(i)) }, len(call.Args))
			}
		} else {
			return nil, nil, fl.errorf(call, "unsupported defer callee")
		}
	case *ast.SelectorExpr:
		if sel := fl.info.Selections[fun]; sel != nil && sel.Kind() == types.MethodVal {
			// defer x.M(args) — capture receiver + args, call method.
			// Use the method's declared receiver type (e.g. *objectNamespaceStack
			// fat ptr), not typeOf(x) (the slice value), and walk embedding.
			fn := sel.Obj().(*types.Func)
			msig := fn.Type().(*types.Signature)
			recvWant, err := fl.mapType(msig.Recv().Type())
			if err != nil {
				return nil, nil, err
			}
			rex, astmts, err := fl.methodRecvSel(fun.X, sel, msig.Recv().Type(), recvWant)
			if err != nil {
				return nil, nil, err
			}
			stmts = append(stmts, astmts...)
			rt := fl.newTemp(recvWant)
			stmts = append(stmts, fl.assignTemp(rt, rex))
			addCap("recv", recvWant, fl.useTemp(rt))
			for i, a := range call.Args {
				want, err := fl.mapType(msig.Params().At(i).Type())
				if err != nil {
					return nil, nil, err
				}
				ex, astmts, err := fl.coerceExpr(a, fl.typeOf(a), want)
				if err != nil {
					return nil, nil, err
				}
				stmts = append(stmts, astmts...)
				t := fl.newTemp(want)
				stmts = append(stmts, fl.assignTemp(t, ex))
				addCap("a"+deferItoa(i), want, fl.useTemp(t))
			}
			target := methodIRName(fn)
			callBuilder = func(child *funcLower, clos *ir.Var, fieldOf func(string) ir.Expr) ([]ir.Stmt, error) {
				args := []ir.Expr{fieldOf("recv")}
				for i := range call.Args {
					args = append(args, fieldOf("a"+deferItoa(i)))
				}
				return []ir.Stmt{&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: target, Args: args}}, &ir.Return{}}, nil
			}
		} else {
			// package.Func
			name, _, err := fl.resolveCallee(call)
			if err != nil {
				return nil, nil, err
			}
			fsig := fl.info.TypeOf(call.Fun).Underlying().(*types.Signature)
			for i, a := range call.Args {
				want, err := fl.mapType(fsig.Params().At(i).Type())
				if err != nil {
					return nil, nil, err
				}
				ex, astmts, err := fl.coerceExpr(a, fl.typeOf(a), want)
				if err != nil {
					return nil, nil, err
				}
				stmts = append(stmts, astmts...)
				t := fl.newTemp(want)
				stmts = append(stmts, fl.assignTemp(t, ex))
				addCap("a"+deferItoa(i), want, fl.useTemp(t))
			}
			callBuilder = func(child *funcLower, clos *ir.Var, fieldOf func(string) ir.Expr) ([]ir.Stmt, error) {
				var args []ir.Expr
				for i := range call.Args {
					args = append(args, fieldOf("a"+deferItoa(i)))
				}
				return []ir.Stmt{&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: name, Args: args}}, &ir.Return{}}, nil
			}
		}
	case *ast.FuncLit:
		// defer func(){ ... }() — lower the lit as a closure, then thunk that calls it.
		litClos, lstmts, err := fl.funcLit(fun, &ir.Ref{Null: true, Named: closBaseName})
		if err != nil {
			return nil, nil, err
		}
		stmts = append(stmts, lstmts...)
		t := fl.newTemp(&ir.Ref{Null: true, Named: closBaseName})
		stmts = append(stmts, fl.assignTemp(t, litClos))
		addCap("callee", &ir.Ref{Null: true, Named: closBaseName}, fl.useTemp(t))
		fsig := fl.typeOf(fun).Underlying().(*types.Signature)
		for i, a := range call.Args {
			want, err := fl.mapType(fsig.Params().At(i).Type())
			if err != nil {
				return nil, nil, err
			}
			ex, astmts, err := fl.coerceExpr(a, fl.typeOf(a), want)
			if err != nil {
				return nil, nil, err
			}
			stmts = append(stmts, astmts...)
			at := fl.newTemp(want)
			stmts = append(stmts, fl.assignTemp(at, ex))
			addCap("a"+deferItoa(i), want, fl.useTemp(at))
		}
		callBuilder = func(child *funcLower, clos *ir.Var, fieldOf func(string) ir.Expr) ([]ir.Stmt, error) {
			return child.emitCallRefStmts(fieldOf("callee"), fsig, func(i int) ir.Expr { return fieldOf("a" + deferItoa(i)) }, len(call.Args))
		}
	default:
		return nil, nil, fl.errorf(call, "unsupported defer call form %T", call.Fun)
	}

	// Build clos type for the thunk.
	var caps []captureInfo
	for i := range capNames {
		caps = append(caps, captureInfo{
			name:  capNames[i],
			typ:   capTypes[i],
			irTyp: capTypes[i],
		})
	}
	closName, err := fl.pkg.ensureClosType(caps)
	if err != nil {
		return nil, nil, err
	}
	// DeferRun call_refs with func (ref $go.clos); thunk params must match.
	funcTy, err := fl.pkg.ensureFuncType(sig, closBaseName)
	if err != nil {
		return nil, nil, err
	}
	bodyName := fl.pkg.freshClosName("defer")
	child := &funcLower{
		pkg:    fl.pkg,
		fset:   fl.fset,
		info:   fl.info,
		mod:    fl.mod,
		sig:    sig,
		irName: bodyName,
		vars:   map[types.Object]*ir.Var{},
		labels: map[string]*labelTargets{},
	}
	closParam := &ir.Var{Name: "clos", Typ: &ir.Ref{Named: closBaseName}}
	child.params = []*ir.Var{closParam}
	fieldOf := func(name string) ir.Expr {
		// Cast to the concrete clos subtype before reading capture fields.
		typed := ir.Expr(child.useVar(closParam))
		if closName != closBaseName {
			typed = &ir.Instr{
				Op:   ir.OpRefCast,
				Type: &ir.Ref{Named: closName},
				Args: []ir.Expr{typed},
			}
		}
		return &ir.Instr{
			Op:    ir.OpStructGet,
			Type:  &ir.Ref{Named: closName},
			Field: name,
			Args:  []ir.Expr{typed},
		}
	}
	// Trampoline only: do not install recoverPrologue here. deferRun sets
	// recoverOk around the trampoline call; the deferred body (e.g. func lit)
	// captures allow via its own prologue — nested clearing would zero it.
	bodyStmts, err := callBuilder(child, closParam, fieldOf)
	if err != nil {
		return nil, nil, err
	}
	body := &ir.Seq{Stmts: bodyStmts}
	fl.mod.Funcs = append(fl.mod.Funcs, &ir.Func{
		Name:    bodyName,
		Params:  child.params,
		Results: nil,
		Body:    body,
	})

	args := []ir.Expr{
		&ir.Instr{Op: ir.OpRefFunc, Func: bodyName, Type: &ir.Ref{Named: funcTy}},
	}
	args = append(args, capExprs...)
	ex := &ir.Instr{Op: ir.OpStructNew, Type: &ir.Ref{Named: closName}, Args: args}
	// Upcast to $go.clos for the defer list.
	if closName != closBaseName {
		ex2 := &ir.Instr{Op: ir.OpRefCast, Type: &ir.Ref{Named: closBaseName}, Args: []ir.Expr{ex}}
		return ex2, stmts, nil
	}
	return ex, stmts, nil
}

// deferredBuiltin emits print/println/panic/recover inside a defer thunk,
// reading captured args via fieldOf("a0"), ...
func (fl *funcLower) deferredBuiltin(name string, nargs int, fieldOf func(string) ir.Expr) ([]ir.Stmt, error) {
	switch name {
	case "print", "println":
		var out []ir.Stmt
		for i := 0; i < nargs; i++ {
			if i > 0 {
				t := fl.newTemp(typeString)
				out = append(out, fl.assignTemp(t, &ir.Instr{Op: ir.OpStringLit, Str: " "}))
				out = append(out, &ir.ExprStmt{
					X: &ir.Instr{Op: ir.OpCall, Func: "runtime.printstring", Args: []ir.Expr{fl.useTemp(t)}},
				})
			}
			arg := fieldOf("a" + deferItoa(i))
			// Captured IR type drives the print form.
			switch typ := arg.(type) {
			default:
				_ = typ
			}
			// Heuristic: string-typed captures print as string; else printint.
			// Cap types are on the clos fields; fieldOf returns struct.get with no Typ.
			// Use printint for i32/i64 and printstring when the field is $go.string —
			// deferredBuiltin is only used with args we typed at capture time, so
			// look at the clos type field via the get's Type/Field.
			get, _ := arg.(*ir.Instr)
			var isStr bool
			if get != nil && get.Type != nil {
				if r, ok := get.Type.(*ir.Ref); ok && r.Named != "" {
					td := fl.mod.LookupType(r.Named)
					if td != nil {
						for _, f := range td.Fields {
							if f.Name == get.Field {
								if sr, ok := f.Type.(*ir.Ref); ok && sr.Named == "$go.string" {
									isStr = true
								}
								break
							}
						}
					}
				}
			}
			if isStr {
				out = append(out, &ir.ExprStmt{
					X: &ir.Instr{Op: ir.OpCall, Func: "runtime.printstring", Args: []ir.Expr{arg}},
				})
			} else {
				out = append(out, &ir.ExprStmt{
					X: &ir.Instr{Op: ir.OpCall, Func: "runtime.printint", Args: []ir.Expr{arg}},
				})
			}
		}
		if name == "println" {
			out = append(out, &ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: "runtime.printnl"}})
		}
		out = append(out, &ir.Return{})
		return out, nil
	case "panic":
		fl.mayPanic = true
		if nargs != 1 {
			return nil, fl.errorf(nil, "deferred panic wants 1 arg")
		}
		// Captured value may already be concrete; box to iface.
		v := fieldOf("a0")
		return []ir.Stmt{
			&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: "runtime.throwPanic", Args: []ir.Expr{v}}},
			&ir.Unreachable{},
		}, nil
	case "recover":
		ex, _, err := fl.builtinRecover(nil)
		if err != nil {
			return nil, err
		}
		return []ir.Stmt{&ir.ExprStmt{X: ex}, &ir.Return{}}, nil
	default:
		return nil, fl.errorf(nil, "unsupported deferred builtin %s", name)
	}
}

func (fl *funcLower) emitCallRefStmts(fnVal ir.Expr, fsig *types.Signature, argAt func(int) ir.Expr, nArgs int) ([]ir.Stmt, error) {
	fl.pkg.ensureClosBase()
	funcTy, err := fl.pkg.ensureFuncType(fsig, closBaseName)
	if err != nil {
		return nil, err
	}
	fnField := &ir.Instr{
		Op:    ir.OpStructGet,
		Type:  &ir.Ref{Named: closBaseName},
		Field: "fn",
		Args:  []ir.Expr{&ir.Instr{Op: ir.OpRefAsNonNull, Args: []ir.Expr{fnVal}}},
	}
	castFn := &ir.Instr{Op: ir.OpRefCast, Type: &ir.Ref{Named: funcTy}, Args: []ir.Expr{fnField}}
	callArgs := []ir.Expr{
		&ir.Instr{Op: ir.OpRefAsNonNull, Args: []ir.Expr{fnVal}},
	}
	for i := 0; i < nArgs; i++ {
		callArgs = append(callArgs, argAt(i))
	}
	callArgs = append(callArgs, castFn)
	return []ir.Stmt{
		&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCallRef, Type: &ir.Ref{Named: funcTy}, Args: callArgs}},
		&ir.Return{},
	}, nil
}

func deferItoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	n := len(b)
	neg := i < 0
	if neg {
		i = -i
	}
	for i > 0 {
		n--
		b[n] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		n--
		b[n] = '-'
	}
	return string(b[n:])
}

func (fl *funcLower) recoverPrologue() []ir.Stmt {
	fl.pkg.ensureDeferTypes()
	if fl.allowVar == nil {
		fl.allowVar = fl.newVar("allow", ir.TypeI32)
	}
	// allowVar is VarDef'd via extraLocals preamble (or explicitly by the
	// caller); do not emit a second VarDef here.
	gTy := &ir.Ref{Named: "$go.g"}
	return []ir.Stmt{
		fl.assignVar(fl.allowVar, &ir.Instr{
			Op:    ir.OpStructGet,
			Type:  gTy,
			Field: "recoverOk",
			Args: []ir.Expr{&ir.Instr{Op: ir.OpRefAsNonNull, Args: []ir.Expr{
				&ir.Instr{Op: ir.OpGlobalGet, Global: "$g"},
			}}},
		}),
		&ir.ExprStmt{X: &ir.Instr{
			Op:    ir.OpStructSet,
			Type:  gTy,
			Field: "recoverOk",
			Args: []ir.Expr{
				&ir.Instr{Op: ir.OpRefAsNonNull, Args: []ir.Expr{
					&ir.Instr{Op: ir.OpGlobalGet, Global: "$g"},
				}},
				fl.i32const(0),
			},
		}},
	}
}

func (fl *funcLower) builtinPanic(e *ast.CallExpr) ([]ir.Stmt, error) {
	fl.pkg.ensureDeferTypes()
	fl.mayPanic = true
	if len(e.Args) != 1 {
		return nil, fl.errorf(e, "panic wants 1 arg")
	}
	arg := e.Args[0]
	src := fl.typeOf(arg)
	emptyIface := types.NewInterfaceType(nil, nil)
	ex, stmts, err := fl.convertToIface(arg, src, emptyIface, typeIface)
	if err != nil {
		return nil, err
	}
	stmts = append(stmts,
		&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: "runtime.throwPanic", Args: []ir.Expr{ex}}},
		&ir.Unreachable{},
	)
	return stmts, nil
}

func (fl *funcLower) builtinRecover(e *ast.CallExpr) (ir.Expr, []ir.Stmt, error) {
	fl.pkg.ensureDeferTypes()
	fl.needsRecoverPrologue = true
	if fl.allowVar == nil {
		fl.allowVar = fl.newVar("allow", ir.TypeI32)
	}
	// recover() → runtime.gorecover(allow)
	return &ir.Instr{
		Op:   ir.OpCall,
		Func: "runtime.gorecover",
		Args: []ir.Expr{fl.useVar(fl.allowVar)},
	}, nil, nil
}

// resultReturn builds a return of named results, or zeros for result types,
// or a bare return for void functions.
func (fl *funcLower) resultReturn() *ir.Return {
	if len(fl.namedResults) > 0 {
		results := make([]ir.Expr, len(fl.namedResults))
		for i, v := range fl.namedResults {
			results[i] = fl.useVar(v)
		}
		return &ir.Return{Results: results}
	}
	if len(fl.results) == 0 {
		return &ir.Return{}
	}
	results := make([]ir.Expr, len(fl.results))
	for i, ty := range fl.results {
		z, err := fl.zeroValue(ty)
		if err != nil {
			results[i] = &ir.Const{Op: ir.OpI32Const, I64: 0}
			continue
		}
		results[i] = z
	}
	return &ir.Return{Results: results}
}

// applyDeferPass rewrites a function body that contains defer.
func (fl *funcLower) applyDeferPass(body *ir.Seq) (*ir.Seq, error) {
	fl.pkg.ensureDeferTypes()
	// Initialize defers = null at entry. defersVar is VarDef'd via extraLocals.
	init := []ir.Stmt{
		fl.assignVar(fl.defersVar, &ir.Instr{Op: ir.OpRefNull, Type: &ir.Ref{Null: true, Named: deferTypeName}}),
	}
	if fl.needsRecoverPrologue {
		init = append(init, fl.recoverPrologue()...)
	}

	// Use runtime.deferRun always when we have a defer list (simpler; open-code
	// is the arg-eval + thunk construction above). Wrap in try when the body
	// may observe a panic or has recover.
	needTry := fl.mayPanic || fl.needsRecoverPrologue || fl.hasDefer

	runDefers := []ir.Stmt{
		&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: "runtime.deferRun", Args: []ir.Expr{fl.useVar(fl.defersVar)}}},
	}

	// Rewrite returns to run defers first.
	newBody := rewriteReturns(body, runDefers)

	if !needTry {
		return &ir.Seq{Stmts: append(init, newBody.Stmts...)}, nil
	}

	payload := &ir.Var{Name: "pobj", Typ: &ir.Ref{Named: panicObjName}}
	exn := &ir.Var{Name: "exn", Typ: ir.TypeExn}
	// Try params bind pobj/exn. deferRun walks remaining defers and rethrows
	// if the final panic is unrecovered; on return the panic was recovered.
	handler := &ir.Seq{Stmts: []ir.Stmt{
		&ir.ExprStmt{X: &ir.Instr{Op: ir.OpDrop, Args: []ir.Expr{fl.useVar(payload)}}},
		&ir.ExprStmt{X: &ir.Instr{Op: ir.OpDrop, Args: []ir.Expr{fl.useVar(exn)}}},
		&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: "runtime.deferRun", Args: []ir.Expr{fl.useVar(fl.defersVar)}}},
		fl.resultReturn(),
	}}

	// Normal edge: body already runs defers at returns; add fallthrough run+return.
	if !endsWithReturn(newBody) {
		newBody.Stmts = append(newBody.Stmts, runDefers...)
		newBody.Stmts = append(newBody.Stmts, fl.resultReturn())
	}

	try := &ir.Try{
		Body:    newBody,
		Tag:     goPanicTag,
		Params:  []*ir.Var{payload, exn},
		Handler: handler,
	}
	return &ir.Seq{Stmts: append(init, try)}, nil
}

func rewriteReturns(r ir.Region, before []ir.Stmt) *ir.Seq {
	switch r := r.(type) {
	case *ir.Seq:
		var out []ir.Stmt
		for _, s := range r.Stmts {
			out = append(out, rewriteReturnStmt(s, before)...)
		}
		return &ir.Seq{Stmts: out, Pos: r.Pos}
	default:
		return &ir.Seq{Stmts: rewriteReturnStmt(r, before)}
	}
}

func rewriteReturnStmt(s ir.Stmt, before []ir.Stmt) []ir.Stmt {
	switch s := s.(type) {
	case *ir.Return:
		out := append([]ir.Stmt{}, before...)
		out = append(out, s)
		return out
	case *ir.Seq:
		return []ir.Stmt{rewriteReturns(s, before)}
	case *ir.Block:
		return []ir.Stmt{&ir.Block{Name: s.Name, Params: s.Params, Results: s.Results, Body: rewriteReturns(s.Body, before), Pos: s.Pos}}
	case *ir.Loop:
		return []ir.Stmt{&ir.Loop{Name: s.Name, Params: s.Params, Body: rewriteReturns(s.Body, before), Pos: s.Pos}}
	case *ir.If:
		var elseR ir.Region
		if s.Else != nil {
			elseR = rewriteReturns(s.Else, before)
		}
		return []ir.Stmt{&ir.If{Cond: s.Cond, Then: rewriteReturns(s.Then, before), Else: elseR, Pos: s.Pos}}
	case *ir.Switch:
		ns := *s
		ns.Cases = append([]ir.Case(nil), s.Cases...)
		for i := range ns.Cases {
			ns.Cases[i].Body = rewriteReturns(ns.Cases[i].Body, before)
		}
		if s.Default != nil {
			ns.Default = rewriteReturns(s.Default, before)
		}
		return []ir.Stmt{&ns}
	case *ir.Try:
		return []ir.Stmt{&ir.Try{
			Name: s.Name, Body: rewriteReturns(s.Body, before), Tag: s.Tag,
			Params: s.Params, Handler: rewriteReturns(s.Handler, before), Pos: s.Pos,
		}}
	default:
		return []ir.Stmt{s}
	}
}
