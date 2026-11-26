package lower

import (
	"go/ast"
	"go/types"
	"strings"

	"github.com/achille-roussel/wasmgc-go/internal/desc"
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
				{Name: "depth", Type: ir.TypeI32}, // $g.deferDepth at throw (defer recover)
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
			case "print", "println", "panic", "recover", "close", "delete", "copy":
				// Capture args; thunk re-emits the builtin.
				var printKinds []int
				var copyDstIR, copySrcIR ir.Type
				var deleteKeyStr string
				for i, a := range call.Args {
					src := fl.typeOf(a)
					var want ir.Type
					var ex ir.Expr
					var astmts []ir.Stmt
					var err error
					switch bname {
					case "panic":
						emptyIface := types.NewInterfaceType(nil, nil)
						want = typeIface
						ex, astmts, err = fl.convertToIface(a, src, emptyIface, typeIface)
					case "delete":
						if i == 0 {
							// map value
							want, err = fl.mapType(src)
							if err != nil {
								return nil, nil, err
							}
							ex, astmts, err = fl.expr(a, want)
							fl.markNeedsMaps()
							if keyT, _ := mapKeyElem(src); keyT != nil {
								deleteKeyStr = desc.TypeKey(keyT)
							}
						} else {
							// key boxed to iface at capture time (like delete itself)
							emptyIface := types.NewInterfaceType(nil, nil)
							want = typeIface
							ex, astmts, err = fl.convertToIface(a, src, emptyIface, typeIface)
						}
					case "close":
						fl.pkg.ensureChanBaseTypes()
						if fl.pkg.meta != nil {
							fl.pkg.meta.needsSched = true
						}
						want, err = fl.mapType(src)
						if err != nil {
							return nil, nil, err
						}
						ex, astmts, err = fl.expr(a, want)
					case "copy":
						want, err = fl.mapType(src)
						if err != nil {
							return nil, nil, err
						}
						ex, astmts, err = fl.expr(a, want)
						if i == 0 {
							copyDstIR = want
						} else {
							copySrcIR = want
						}
					default:
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
					if bname == "print" || bname == "println" {
						printKinds = append(printKinds, printArgKind(src))
					}
				}
				nargs := len(call.Args)
				kinds := printKinds
				dstIR, srcIR := copyDstIR, copySrcIR
				keyStr := deleteKeyStr
				callBuilder = func(child *funcLower, clos *ir.Var, fieldOf func(string) ir.Expr) ([]ir.Stmt, error) {
					if bname == "print" || bname == "println" {
						return child.deferredPrint(bname, nargs, kinds, fieldOf)
					}
					if bname == "copy" {
						return child.deferredCopy(dstIR, srcIR, fieldOf)
					}
					if bname == "close" {
						return child.deferredClose(fieldOf)
					}
					if bname == "delete" {
						return child.deferredDelete(keyStr, fieldOf)
					}
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
			nargs, err := fl.deferAddSigArgs(call, fsig, addCap, &stmts)
			if err != nil {
				return nil, nil, err
			}
			callBuilder = func(child *funcLower, clos *ir.Var, fieldOf func(string) ir.Expr) ([]ir.Stmt, error) {
				var args []ir.Expr
				for i := 0; i < nargs; i++ {
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
			nargs, err := fl.deferAddSigArgs(call, fsig, addCap, &stmts)
			if err != nil {
				return nil, nil, err
			}
			nCap := nargs
			callBuilder = func(child *funcLower, clos *ir.Var, fieldOf func(string) ir.Expr) ([]ir.Stmt, error) {
				return child.emitCallRefStmts(fieldOf("callee"), fsig, func(i int) ir.Expr { return fieldOf("a" + deferItoa(i)) }, nCap)
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
			exprTy := fl.typeOf(fun.X)
			landed := selectionRecvType(exprTy, sel)
			ifaceRecv := isIface(exprTy) || isIface(landed)
			var recvGo types.Type
			var recvWant ir.Type
			var err error
			if ifaceRecv {
				recvGo = landed
				if !isIface(recvGo) {
					recvGo = exprTy
				}
				recvWant = typeIface
			} else {
				if m := concreteMethod(exprTy, fn); m != nil {
					fn = m
					msig = fn.Type().(*types.Signature)
				}
				recvGo = msig.Recv().Type()
				recvWant, err = fl.mapType(recvGo)
				if err != nil {
					return nil, nil, err
				}
			}
			rex, astmts, err := fl.methodRecvSel(fun.X, sel, recvGo, recvWant)
			if err != nil {
				return nil, nil, err
			}
			stmts = append(stmts, astmts...)
			rt := fl.newTemp(recvWant)
			stmts = append(stmts, fl.assignTemp(rt, rex))
			// defer x.M() evaluates the method value at the defer site; a nil
			// interface receiver must panic here (issue15975), not when the
			// thunk later runs the call.
			if ifaceRecv {
				stmts = append(stmts, fl.panicIfNilRef(fl.useTemp(rt)))
			}
			addCap("recv", recvWant, fl.useTemp(rt))
			nargs, err := fl.deferAddSigArgs(call, msig, addCap, &stmts)
			if err != nil {
				return nil, nil, err
			}
			ifaceType := recvGo
			ifaceFn := fn
			useIface := ifaceRecv
			target := methodIRName(fn)
			nCap := nargs
			callBuilder = func(child *funcLower, clos *ir.Var, fieldOf func(string) ir.Expr) ([]ir.Stmt, error) {
				args := []ir.Expr{fieldOf("recv")}
				for i := 0; i < nCap; i++ {
					args = append(args, fieldOf("a"+deferItoa(i)))
				}
				var call ir.Expr
				if useIface {
					call = makeIfaceCall(ifaceFn, ifaceType, args, nil)
				} else {
					call = &ir.Instr{Op: ir.OpCall, Func: target, Args: args}
				}
				return []ir.Stmt{&ir.ExprStmt{X: call}, &ir.Return{}}, nil
			}
		} else {
			// package.Func
			name, _, err := fl.resolveCallee(call)
			if err != nil {
				return nil, nil, err
			}
			fsig := fl.info.TypeOf(call.Fun).Underlying().(*types.Signature)
			nargs, err := fl.deferAddSigArgs(call, fsig, addCap, &stmts)
			if err != nil {
				return nil, nil, err
			}
			nCap := nargs
			callBuilder = func(child *funcLower, clos *ir.Var, fieldOf func(string) ir.Expr) ([]ir.Stmt, error) {
				var args []ir.Expr
				for i := 0; i < nCap; i++ {
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
		nargs, err := fl.deferAddSigArgs(call, fsig, addCap, &stmts)
		if err != nil {
			return nil, nil, err
		}
		nCap := nargs
		callBuilder = func(child *funcLower, clos *ir.Var, fieldOf func(string) ir.Expr) ([]ir.Stmt, error) {
			return child.emitCallRefStmts(fieldOf("callee"), fsig, func(i int) ir.Expr { return fieldOf("a" + deferItoa(i)) }, nCap)
		}
	case *ast.CallExpr, *ast.ParenExpr, *ast.TypeAssertExpr:
		// defer f()() / defer (fn)() / defer x.(func())() — Fun is a func value.
		fsig := fl.funcValueSig(call.Fun)
		if fsig == nil {
			return nil, nil, fl.errorf(call, "unsupported defer call form %T", call.Fun)
		}
		closTy := &ir.Ref{Null: true, Named: closBaseName}
		ex, astmts, err := fl.expr(call.Fun, closTy)
		if err != nil {
			return nil, nil, err
		}
		stmts = append(stmts, astmts...)
		t := fl.newTemp(closTy)
		stmts = append(stmts, fl.assignTemp(t, ex))
		addCap("callee", closTy, fl.useTemp(t))
		nargs, err := fl.deferAddSigArgs(call, fsig, addCap, &stmts)
		if err != nil {
			return nil, nil, err
		}
		nCap := nargs
		callBuilder = func(child *funcLower, clos *ir.Var, fieldOf func(string) ir.Expr) ([]ir.Stmt, error) {
			return child.emitCallRefStmts(fieldOf("callee"), fsig, func(i int) ir.Expr { return fieldOf("a" + deferItoa(i)) }, nCap)
		}
	default:
		// Other func-value forms (IndexExpr instantiations, etc.).
		if fsig := fl.funcValueSig(call.Fun); fsig != nil {
			closTy := &ir.Ref{Null: true, Named: closBaseName}
			ex, astmts, err := fl.expr(call.Fun, closTy)
			if err != nil {
				return nil, nil, err
			}
			stmts = append(stmts, astmts...)
			t := fl.newTemp(closTy)
			stmts = append(stmts, fl.assignTemp(t, ex))
			addCap("callee", closTy, fl.useTemp(t))
			nargs, err := fl.deferAddSigArgs(call, fsig, addCap, &stmts)
			if err != nil {
				return nil, nil, err
			}
			nCap := nargs
			callBuilder = func(child *funcLower, clos *ir.Var, fieldOf func(string) ir.Expr) ([]ir.Stmt, error) {
				return child.emitCallRefStmts(fieldOf("callee"), fsig, func(i int) ir.Expr { return fieldOf("a" + deferItoa(i)) }, nCap)
			}
			break
		}
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
	// Locals allocated inside the thunk (e.g. deferred copy) need VarDefs +
	// Func.Locals — same preamble shape as lowerFunc / func lit bodies.
	var preamble []ir.Stmt
	for _, v := range child.extraLocals {
		preamble = append(preamble, &ir.VarDef{Var: v})
		if needsZeroInit(v.Typ) || isNamedStructRef(v.Typ) {
			if z, zerr := child.zeroValue(v.Typ); zerr == nil {
				preamble = append(preamble, child.assignVar(v, z))
			}
		}
	}
	body := &ir.Seq{Stmts: append(preamble, bodyStmts...)}
	fl.mod.Funcs = append(fl.mod.Funcs, &ir.Func{
		Name:    bodyName,
		Params:  child.params,
		Results: nil,
		Locals:  append([]*ir.Var(nil), child.extraLocals...),
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

// deferAddSigArgs lowers call args (including variadic packing) and registers
// them as defer-thunk captures a0..a{n-1}. Returns the capture count.
func (fl *funcLower) deferAddSigArgs(
	call *ast.CallExpr,
	fsig *types.Signature,
	addCap func(name string, ty ir.Type, ex ir.Expr),
	stmts *[]ir.Stmt,
) (int, error) {
	args, astmts, err := fl.lowerArgsWithSig(call, fsig)
	if err != nil {
		return 0, err
	}
	*stmts = append(*stmts, astmts...)
	nParams := 0
	if fsig != nil {
		nParams = fsig.Params().Len()
	}
	for i, ex := range args {
		var want ir.Type
		if i < nParams {
			want, err = fl.mapType(fsig.Params().At(i).Type())
			if err != nil {
				return 0, err
			}
		} else if ex != nil {
			// Should not happen: lowerArgsWithSig packs variadic into Params.
			want = typeIface
		}
		t := fl.newTemp(want)
		*stmts = append(*stmts, fl.assignTemp(t, ex))
		addCap("a"+deferItoa(i), want, fl.useTemp(t))
	}
	return len(args), nil
}

// printArgKind tags deferred print captures (bool and int share i32 IR).
const (
	printKindInt = iota
	printKindInt64
	printKindUint
	printKindUint64
	printKindBool
	printKindString
	printKindIface
	printKindSlice
	printKindPtr // pointer, map, chan, func
	printKindFloat32
	printKindFloat64
	printKindComplex64
	printKindComplex128
)

func printArgKind(ty types.Type) int {
	if ty == nil {
		return printKindInt
	}
	ty = types.Unalias(ty)
	switch {
	case isBoolean(ty):
		return printKindBool
	case isString(ty):
		return printKindString
	case isIface(ty):
		return printKindIface
	case isSlice(ty):
		return printKindSlice
	case isPointer(ty), isMap(ty), isChan(ty), isSignature(ty):
		return printKindPtr
	case isFloat(ty):
		if b, ok := ty.Underlying().(*types.Basic); ok && b.Kind() == types.Float32 {
			return printKindFloat32
		}
		return printKindFloat64
	case isComplex(ty):
		if b, ok := ty.Underlying().(*types.Basic); ok && b.Kind() == types.Complex64 {
			return printKindComplex64
		}
		return printKindComplex128
	case isInteger(ty):
		// Width from basic kind: int64/uint64 are always i64; int/uint are i32
		// on WasmGC. Named integer aliases share the underlying basic kind.
		if b, ok := ty.Underlying().(*types.Basic); ok {
			switch b.Kind() {
			case types.Int64:
				return printKindInt64
			case types.Uint64:
				return printKindUint64
			case types.Uint, types.Uint32, types.Uint16, types.Uint8, types.Uintptr:
				return printKindUint
			}
		}
		return printKindInt
	default:
		return printKindInt
	}
}

// deferredPrint emits print/println inside a defer thunk using kinds captured
// at the defer site (so bool vs int, both i32, stay distinct).
func (fl *funcLower) deferredPrint(name string, nargs int, kinds []int, fieldOf func(string) ir.Expr) ([]ir.Stmt, error) {
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
		kind := printKindInt
		if i < len(kinds) {
			kind = kinds[i]
		}
		out = append(out, fl.deferredPrintKind(arg, kind)...)
	}
	if name == "println" {
		out = append(out, &ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: "runtime.printnl"}})
	}
	out = append(out, &ir.Return{})
	return out, nil
}

func (fl *funcLower) deferredPrintKind(arg ir.Expr, kind int) []ir.Stmt {
	switch kind {
	case printKindBool:
		return []ir.Stmt{&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: "runtime.printbool", Args: []ir.Expr{arg}}}}
	case printKindString:
		return []ir.Stmt{&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: "runtime.printstring", Args: []ir.Expr{arg}}}}
	case printKindIface:
		isnull := &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{arg}}
		return []ir.Stmt{&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: "runtime.printiface", Args: []ir.Expr{isnull}}}}
	case printKindSlice:
		lenEx := &ir.Instr{Op: ir.OpLen, Type: ir.TypeI32, Args: []ir.Expr{arg}}
		capEx := &ir.Instr{Op: ir.OpCap, Type: ir.TypeI32, Args: []ir.Expr{arg}}
		isnull := &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{arg}}
		return []ir.Stmt{&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: "runtime.printslice", Args: []ir.Expr{lenEx, capEx, isnull}}}}
	case printKindPtr:
		isnull := &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{arg}}
		return []ir.Stmt{&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: "runtime.printpointer", Args: []ir.Expr{isnull}}}}
	case printKindFloat32:
		return []ir.Stmt{&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: "runtime.printfloat32", Args: []ir.Expr{arg}}}}
	case printKindFloat64:
		return []ir.Stmt{&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: "runtime.printfloat64", Args: []ir.Expr{arg}}}}
	case printKindComplex64:
		return []ir.Stmt{&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: "runtime.printcomplex64", Args: []ir.Expr{arg}}}}
	case printKindComplex128:
		return []ir.Stmt{&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: "runtime.printcomplex128", Args: []ir.Expr{arg}}}}
	case printKindInt64:
		return []ir.Stmt{&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: "runtime.printint64", Args: []ir.Expr{arg}}}}
	case printKindUint64:
		return []ir.Stmt{&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: "runtime.printuint64", Args: []ir.Expr{arg}}}}
	case printKindUint:
		return []ir.Stmt{&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: "runtime.printuint", Args: []ir.Expr{arg}}}}
	default:
		return []ir.Stmt{&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: "runtime.printint", Args: []ir.Expr{arg}}}}
	}
}

// deferredBuiltin emits panic/recover inside a defer thunk,
// reading captured args via fieldOf("a0"), ...
func (fl *funcLower) deferredBuiltin(name string, nargs int, fieldOf func(string) ir.Expr) ([]ir.Stmt, error) {
	switch name {
	case "print", "println":
		// Backward-compatible path without kinds (treat as int/string heuristic).
		return fl.deferredPrint(name, nargs, nil, fieldOf)
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
		// defer recover() is a wrapper frame. Allow only when the current
		// panic was thrown at a shallower deferDepth than this deferRun's
		// entry (outer-frame panic). Same-frame panic → no-op (recover1
		// test7); outer panic seen from nested deferRun → recover (test5/6).
		fl.pkg.ensureDeferTypes()
		gTy := &ir.Ref{Named: "$go.g"}
		gGet := func() ir.Expr {
			return &ir.Instr{Op: ir.OpRefAsNonNull, Args: []ir.Expr{
				&ir.Instr{Op: ir.OpGlobalGet, Global: "$g"},
			}}
		}
		panicSlot := fl.newVar("drPanic", &ir.Ref{Null: true, Named: panicObjName})
		startDepth := fl.newVar("drStart", ir.TypeI32)
		ok := fl.newVar("drOk", ir.TypeI32)
		return []ir.Stmt{
			fl.assignVar(panicSlot, &ir.Instr{
				Op: ir.OpStructGet, Type: gTy, Field: "panic", Args: []ir.Expr{gGet()},
			}),
			fl.assignVar(startDepth, &ir.Instr{
				Op: ir.OpStructGet, Type: gTy, Field: "deferStartDepth", Args: []ir.Expr{gGet()},
			}),
			fl.assignVar(ok, fl.i32const(0)),
			&ir.If{
				Cond: &ir.Instr{Op: ir.OpI32Eqz, Args: []ir.Expr{
					&ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{fl.useVar(panicSlot)}},
				}},
				Then: &ir.Seq{Stmts: []ir.Stmt{
					fl.assignVar(ok, &ir.Instr{
						Op: ir.OpI32LtS,
						Args: []ir.Expr{
							&ir.Instr{
								Op: ir.OpStructGet, Type: &ir.Ref{Named: panicObjName}, Field: "depth",
								Args: []ir.Expr{&ir.Instr{Op: ir.OpRefAsNonNull, Args: []ir.Expr{fl.useVar(panicSlot)}}},
							},
							fl.useVar(startDepth),
						},
					}),
				}},
			},
			&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: "runtime.gorecover", Args: []ir.Expr{fl.useVar(ok)}}},
			&ir.Return{},
		}, nil
	default:
		return nil, fl.errorf(nil, "unsupported deferred builtin %s", name)
	}
}

// deferredClose emits close(ch) inside a defer thunk.
func (fl *funcLower) deferredClose(fieldOf func(string) ir.Expr) ([]ir.Stmt, error) {
	fl.pkg.ensureChanBaseTypes()
	return []ir.Stmt{
		&ir.ExprStmt{X: &ir.Instr{Op: ir.OpChanClose, Args: []ir.Expr{fieldOf("a0")}}},
		&ir.Return{},
	}, nil
}

// deferredDelete emits delete(m, k) inside a defer thunk.
// keyStr is desc.TypeKey of the map key type (may be empty).
func (fl *funcLower) deferredDelete(keyStr string, fieldOf func(string) ir.Expr) ([]ir.Stmt, error) {
	fl.markNeedsMaps()
	in := &ir.Instr{Op: ir.OpMapDelete, Args: []ir.Expr{fieldOf("a0"), fieldOf("a1")}}
	if keyStr != "" {
		in.Str = keyStr
	}
	return []ir.Stmt{&ir.ExprStmt{X: in}, &ir.Return{}}, nil
}

// deferredCopy emits copy(dst, src) inside a defer thunk using captured slice/
// string values. Result is discarded (defer is a statement).
func (fl *funcLower) deferredCopy(dstIR, srcIR ir.Type, fieldOf func(string) ir.Expr) ([]ir.Stmt, error) {
	dstName, dstArr := sliceParts(dstIR)
	srcName, srcArr := sliceParts(srcIR)
	if dstName == "" || srcName == "" {
		if r, ok := srcIR.(*ir.Ref); ok && r.Named == "$go.string" {
			srcName, srcArr = "$go.string", "$go.bytes"
		}
		if r, ok := dstIR.(*ir.Ref); ok && len(r.Named) > 0 {
			dstName, dstArr = sliceParts(dstIR)
		}
	}
	if dstArr == "" {
		return nil, fl.errorf(nil, "deferred copy: unsupported dst type")
	}
	if srcArr == "" {
		srcArr = dstArr
	}

	dstV := fl.newVar("cdst", dstIR)
	srcV := fl.newVar("csrc", srcIR)
	nV := fl.newVar("cn", ir.TypeI32)
	dstLenV := fl.newVar("cdlen", ir.TypeI32)
	srcLenV := fl.newVar("cslen", ir.TypeI32)
	stmts := []ir.Stmt{
		fl.assignVar(dstV, fieldOf("a0")),
		fl.assignVar(srcV, fieldOf("a1")),
		fl.assignVar(dstLenV, fl.i32const(0)),
		fl.assignVar(srcLenV, fl.i32const(0)),
		fl.assignVar(nV, fl.i32const(0)),
	}

	srcIsString := false
	if r, ok := srcIR.(*ir.Ref); ok && r.Named == "$go.string" {
		srcIsString = true
	}
	if srcIsString {
		stmts = append(stmts, fl.assignVar(srcLenV, &ir.Instr{
			Op: ir.OpStructGet, Type: srcIR, Field: "len", Args: []ir.Expr{fl.useVar(srcV)},
		}))
	} else {
		stmts = append(stmts, &ir.If{
			Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{fl.useVar(srcV)}},
			Then: &ir.Seq{},
			Else: &ir.Seq{Stmts: []ir.Stmt{fl.assignVar(srcLenV, &ir.Instr{
				Op: ir.OpStructGet, Type: srcIR, Field: "len", Args: []ir.Expr{fl.useVar(srcV)},
			})}},
		})
	}
	stmts = append(stmts, &ir.If{
		Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{fl.useVar(dstV)}},
		Then: &ir.Seq{},
		Else: &ir.Seq{Stmts: []ir.Stmt{fl.assignVar(dstLenV, &ir.Instr{
			Op: ir.OpStructGet, Type: dstIR, Field: "len", Args: []ir.Expr{fl.useVar(dstV)},
		})}},
	})
	stmts = append(stmts, fl.assignVar(nV, fl.useVar(dstLenV)))
	stmts = append(stmts, &ir.If{
		Cond: &ir.Instr{Op: ir.OpI32LtU, Args: []ir.Expr{fl.useVar(srcLenV), fl.useVar(nV)}},
		Then: &ir.Seq{Stmts: []ir.Stmt{fl.assignVar(nV, fl.useVar(srcLenV))}},
		Else: &ir.Seq{},
	})
	copyBody := &ir.Seq{Stmts: []ir.Stmt{&ir.ExprStmt{X: &ir.Instr{
		Op: ir.OpArrayCopy, Type: &ir.Ref{Named: dstArr}, Type2: &ir.Ref{Named: srcArr},
		Args: []ir.Expr{
			&ir.Instr{Op: ir.OpStructGet, Type: dstIR, Field: "data", Args: []ir.Expr{fl.useVar(dstV)}},
			&ir.Instr{Op: ir.OpStructGet, Type: dstIR, Field: "off", Args: []ir.Expr{fl.useVar(dstV)}},
			&ir.Instr{Op: ir.OpStructGet, Type: srcIR, Field: "data", Args: []ir.Expr{fl.useVar(srcV)}},
			&ir.Instr{Op: ir.OpStructGet, Type: srcIR, Field: "off", Args: []ir.Expr{fl.useVar(srcV)}},
			fl.useVar(nV),
		},
	}}}}
	stmts = append(stmts, &ir.If{
		Cond: &ir.Instr{Op: ir.OpI32Eqz, Args: []ir.Expr{fl.useVar(nV)}},
		Then: &ir.Seq{},
		Else: copyBody,
	})
	_ = dstName
	_ = srcName
	stmts = append(stmts, &ir.Return{})
	return stmts, nil
}

func (fl *funcLower) emitCallRefStmts(fnVal ir.Expr, fsig *types.Signature, argAt func(int) ir.Expr, nArgs int) ([]ir.Stmt, error) {
	fl.pkg.ensureClosBase()
	funcTy, err := fl.pkg.ensureFuncType(fsig, closBaseName)
	if err != nil {
		return nil, err
	}
	// Nil func value → panic (matches callFuncValue; needed for defer of nil).
	nilCheck := &ir.If{
		Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{fnVal}},
		Then: &ir.Seq{Stmts: []ir.Stmt{
			&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: "runtime.panicNil"}},
			&ir.Unreachable{},
		}},
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
		nilCheck,
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

// clearRecoverOk zeros $g.recoverOk at function entry. recover() is only
// valid when called directly by a deferred function (Go spec); deferRun sets
// recoverOk around the deferred trampoline, and that function's
// recoverPrologue captures it. Callees of a deferred frame must not see
// recoverOk still set (issue73916: defer F where F calls recover via helper).
// No-op when $g is still null (packages that never panic/defer).
func (fl *funcLower) clearRecoverOk() []ir.Stmt {
	// Do not ensureDeferTypes here — that would pull $g into packages that
	// never defer/panic (breaks empty-main IR goldens). Only clear when $g
	// already exists (some other function in the package used defer/panic).
	if fl.pkg.mod.LookupGlobal("$g") == nil {
		return nil
	}
	gTy := &ir.Ref{Named: "$go.g"}
	gGet := &ir.Instr{Op: ir.OpGlobalGet, Global: "$g"}
	return []ir.Stmt{
		&ir.If{
			Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{gGet}},
			Then: &ir.Seq{},
			Else: &ir.Seq{Stmts: []ir.Stmt{
				&ir.ExprStmt{X: &ir.Instr{
					Op:    ir.OpStructSet,
					Type:  gTy,
					Field: "recoverOk",
					Args: []ir.Expr{
						&ir.Instr{Op: ir.OpRefAsNonNull, Args: []ir.Expr{gGet}},
						fl.i32const(0),
					},
				}},
			}},
		},
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

// syncNamedResultCells copies cell-boxed named results into their plain locals
// when the cell is non-null. Used on the panic catch path after deferRun.
func (fl *funcLower) syncNamedResultCells() []ir.Stmt {
	if fl == nil || len(fl.namedResults) == 0 || fl.cells == nil || fl.sig == nil {
		return nil
	}
	var out []ir.Stmt
	results := fl.sig.Results()
	for i, v := range fl.namedResults {
		if i >= results.Len() {
			break
		}
		obj := results.At(i)
		cv, ok := fl.cells[obj]
		if !ok || cv == nil || v == nil || v == cv {
			continue
		}
		cellTy, _ := cv.Typ.(*ir.Ref)
		if cellTy == nil {
			continue
		}
		out = append(out, fl.assignVar(v, &ir.Instr{
			Op:    ir.OpStructGet,
			Type:  &ir.Ref{Named: cellTy.Named},
			Field: "v",
			Args:  []ir.Expr{fl.useVar(cv)},
		}))
	}
	return out
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
	// Peel leading VarDefs / zero-inits so they stay outside try. Catch handlers
	// return named results; Wasm requires those locals initialized on entry to
	// the function, not only on the try body path (abi/defer_recover_results).
	var peeled []ir.Stmt
	rest := body.Stmts
	for len(rest) > 0 {
		switch rest[0].(type) {
		case *ir.VarDef:
			peeled = append(peeled, rest[0])
			rest = rest[1:]
			continue
		}
		break
	}
	// Also peel simple zero-inits of named results / locals (assign of StringLit /
	// StructNewDefault / RefNull / Const to a var).
	for len(rest) > 0 {
		as, ok := rest[0].(*ir.Assign)
		if !ok || len(as.Lhs) != 1 || as.Lhs[0].Var == nil {
			break
		}
		if !isDeferPeelableInit(as.Rhs) {
			break
		}
		peeled = append(peeled, rest[0])
		rest = rest[1:]
	}
	// Hoist every $go.cell.* allocation out of the try body so catch can sync
	// cell→plain after recover (panics named-result helper). Leading peel alone
	// misses cells allocated after other setup (regexp.syntax.parse).
	var hoisted []ir.Stmt
	var kept []ir.Stmt
	for _, s := range rest {
		if as, ok := s.(*ir.Assign); ok && isCellAllocAssign(as) {
			hoisted = append(hoisted, s)
			continue
		}
		kept = append(kept, s)
	}
	body = &ir.Seq{Stmts: kept, Pos: body.Pos}

	// Initialize defers = null at entry. defersVar is VarDef'd via extraLocals.
	init := append(peeled, hoisted...)
	init = append(init, fl.assignVar(fl.defersVar, &ir.Instr{Op: ir.OpRefNull, Type: &ir.Ref{Null: true, Named: deferTypeName}}))
	if fl.needsRecoverPrologue {
		init = append(init, fl.recoverPrologue()...)
	}

	// Use runtime.deferRun always when we have a defer list (simpler; open-code
	// is the arg-eval + thunk construction above). Wrap in try when the body
	// may observe a panic or has recover.
	needTry := fl.mayPanic || fl.needsRecoverPrologue || fl.hasDefer

	// Snapshot the defer list, then null defersVar before deferRun. The try
	// body wraps this call: an unrecovered rethrow from deferRun is caught by
	// the same function's handler. If defersVar still pointed at the list, the
	// handler would walk it again (double-fire + panic chain) and then clear
	// $g.panic / return — swallowing the panic (breaks defernil / nested recover).
	// newVar puts deferSnap in extraLocals (single VarDef in the preamble).
	// Do not VarDef inside runDefers — rewriteReturns copies that block to
	// every return site and duplicate defs fail IR verify.
	// Empty name → newVar auto-uniques (nested wrapDeferBody / MakeFunc
	// paths must not emit two VarDefs named defersSnap in one IR func).
	deferSnap := fl.newVar("", &ir.Ref{Null: true, Named: deferTypeName})
	runDefers := []ir.Stmt{
		fl.assignVar(deferSnap, fl.useVar(fl.defersVar)),
		fl.assignVar(fl.defersVar, &ir.Instr{Op: ir.OpRefNull, Type: &ir.Ref{Null: true, Named: deferTypeName}}),
		&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: "runtime.deferRun", Args: []ir.Expr{fl.useVar(deferSnap)}}},
	}

	// Rewrite returns to run defers first. With named results, re-read them
	// after deferRun so mutations like `defer func(){ a++ }()` apply (bug293).
	newBody := fl.rewriteReturns(body, runDefers)

	if !needTry {
		return &ir.Seq{Stmts: append(init, newBody.Stmts...)}, nil
	}

	payload := &ir.Var{Name: "pobj", Typ: &ir.Ref{Named: panicObjName}}
	exn := &ir.Var{Name: "exn", Typ: ir.TypeExn}
	// Try params bind pobj/exn. deferRun walks remaining defers and rethrows
	// if the final panic is unrecovered; on return the panic was recovered.
	// Sync cell-boxed named results into plain locals after deferRun so a
	// recover defer that wrote `b = true` through the cell is visible
	// (panics() helper). Cell allocs are hoisted outside try above.
	//
	// Re-install the caught panic on $g before walking defers so recover()
	// sees it. Important: return-path runDefers lives inside this same try, so
	// an unrecovered panic from a nested defer (e.g. `defer func(){ defer
	// panic(5) }()`) is caught HERE rather than propagating. After deferRun,
	// rethrow if $g.panic is still unrecovered — unless it is the same panic
	// that was already pending at try entry (inherited outer panic after a
	// nested recover; recover.go test111). Clearing that would steal the
	// panic from the still-running deferred caller.
	gTy := &ir.Ref{Named: "$go.g"}
	gNonNull := &ir.Instr{Op: ir.OpRefAsNonNull, Args: []ir.Expr{
		&ir.Instr{Op: ir.OpGlobalGet, Global: "$g"},
	}}
	panicSlot := fl.newVar("catchPanic", &ir.Ref{Null: true, Named: panicObjName})
	entryPanic := fl.newVar("entryPanic", &ir.Ref{Null: true, Named: panicObjName})
	// Snapshot $g.panic before the try body so the handler can tell an
	// inherited outer panic from one thrown in this frame.
	init = append(init, fl.assignVar(entryPanic, &ir.Instr{
		Op: ir.OpStructGet, Type: gTy, Field: "panic",
		Args: []ir.Expr{gNonNull},
	}))
	// Fresh Seq per branch — shared Stmt nodes break IR verify (temps
	// appear in multiple regions; log.(*Logger).Prefix / goprint.go).
	leaveInherited := func() *ir.Seq {
		return &ir.Seq{Stmts: append(append([]ir.Stmt{}, fl.syncNamedResultCells()...), fl.resultReturn())}
	}
	clearAndReturn := func() *ir.Seq {
		s := &ir.Seq{Stmts: []ir.Stmt{
			&ir.ExprStmt{X: &ir.Instr{
				Op: ir.OpStructSet, Type: gTy, Field: "panic",
				Args: []ir.Expr{
					&ir.Instr{Op: ir.OpRefAsNonNull, Args: []ir.Expr{
						&ir.Instr{Op: ir.OpGlobalGet, Global: "$g"},
					}},
					&ir.Instr{Op: ir.OpRefNull, Type: &ir.Ref{Null: true, Named: panicObjName}},
				},
			}},
		}}
		s.Stmts = append(s.Stmts, fl.syncNamedResultCells()...)
		s.Stmts = append(s.Stmts, fl.resultReturn())
		return s
	}
	rethrow := func() *ir.Seq {
		return &ir.Seq{Stmts: []ir.Stmt{
			&ir.ExprStmt{X: &ir.Instr{
				Op:   ir.OpThrow,
				Args: []ir.Expr{&ir.Instr{Op: ir.OpRefAsNonNull, Args: []ir.Expr{fl.useVar(panicSlot)}}},
			}},
			&ir.Unreachable{},
		}}
	}
	handler := &ir.Seq{Stmts: []ir.Stmt{
		&ir.ExprStmt{X: &ir.Instr{Op: ir.OpDrop, Args: []ir.Expr{fl.useVar(exn)}}},
		&ir.ExprStmt{X: &ir.Instr{
			Op: ir.OpStructSet, Type: gTy, Field: "panic",
			Args: []ir.Expr{gNonNull, fl.useVar(payload)},
		}},
		&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: "runtime.deferRun", Args: []ir.Expr{fl.useVar(fl.defersVar)}}},
		// Read $g.panic after deferRun.
		fl.assignVar(panicSlot, &ir.Instr{
			Op: ir.OpStructGet, Type: gTy, Field: "panic",
			Args: []ir.Expr{&ir.Instr{Op: ir.OpRefAsNonNull, Args: []ir.Expr{
				&ir.Instr{Op: ir.OpGlobalGet, Global: "$g"},
			}}},
		}),
		&ir.If{
			Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{fl.useVar(panicSlot)}},
			Then: clearAndReturn(),
			Else: &ir.Seq{Stmts: []ir.Stmt{
				&ir.If{
					Cond: &ir.Instr{
						Op: ir.OpStructGet, Type: &ir.Ref{Named: panicObjName}, Field: "recovered",
						Args: []ir.Expr{&ir.Instr{Op: ir.OpRefAsNonNull, Args: []ir.Expr{fl.useVar(panicSlot)}}},
					},
					Then: clearAndReturn(),
					Else: &ir.Seq{Stmts: []ir.Stmt{
						&ir.If{
							Cond: &ir.Instr{Op: ir.OpI32Eqz, Args: []ir.Expr{
								&ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{fl.useVar(entryPanic)}},
							}},
							Then: &ir.Seq{Stmts: []ir.Stmt{
								&ir.If{
									Cond: &ir.Instr{Op: ir.OpRefEq, Args: []ir.Expr{
										fl.useVar(panicSlot), fl.useVar(entryPanic),
									}},
									Then: leaveInherited(),
									Else: rethrow(),
								},
							}},
							Else: rethrow(),
						},
					}},
				},
			}},
		},
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

func isCellAllocAssign(as *ir.Assign) bool {
	if as == nil || len(as.Lhs) != 1 || as.Lhs[0].Var == nil {
		return false
	}
	in, ok := as.Rhs.(*ir.Instr)
	if !ok || in.Op != ir.OpStructNew {
		return false
	}
	r, ok := in.Type.(*ir.Ref)
	return ok && strings.HasPrefix(r.Named, "$go.cell.")
}

func isDeferPeelableInit(rhs ir.Expr) bool {
	switch e := rhs.(type) {
	case *ir.Const:
		return true
	case *ir.Instr:
		switch e.Op {
		case ir.OpRefNull, ir.OpStringLit, ir.OpStructNewDefault, ir.OpI32Const, ir.OpI64Const,
			ir.OpF32Const, ir.OpF64Const:
			return true
		case ir.OpStructNew:
			// zero string: struct.new $go.string (empty bytes), 0, 0
			if r, ok := e.Type.(*ir.Ref); ok && r.Named == "$go.string" {
				return true
			}
			if r, ok := e.Type.(*ir.Ref); ok && (r.Named == "$go.complex64" || r.Named == "$go.complex128") {
				return true
			}
		case ir.OpArrayNew, ir.OpArrayNewDefault:
			return true
		}
	}
	return false
}

// resultReturnAfterDefer is like resultReturn but always reads capture cells
// when present, so values mutated during deferRun are returned.
func (fl *funcLower) resultReturnAfterDefer() *ir.Return {
	if len(fl.namedResults) == 0 {
		return fl.resultReturn()
	}
	results := make([]ir.Expr, len(fl.namedResults))
	for i, v := range fl.namedResults {
		if fl.sig != nil && i < fl.sig.Results().Len() && fl.cells != nil {
			obj := fl.sig.Results().At(i)
			if _, ok := fl.cells[obj]; ok {
				if rx, err := fl.readVar(obj); err == nil {
					results[i] = rx
					continue
				}
			}
		}
		results[i] = fl.useVar(v)
	}
	return &ir.Return{Results: results}
}

func (fl *funcLower) rewriteReturns(r ir.Region, before []ir.Stmt) *ir.Seq {
	switch r := r.(type) {
	case *ir.Seq:
		var out []ir.Stmt
		for _, s := range r.Stmts {
			out = append(out, fl.rewriteReturnStmt(s, before)...)
		}
		return &ir.Seq{Stmts: out, Pos: r.Pos}
	default:
		return &ir.Seq{Stmts: fl.rewriteReturnStmt(r, before)}
	}
}

func (fl *funcLower) rewriteReturnStmt(s ir.Stmt, before []ir.Stmt) []ir.Stmt {
	switch s := s.(type) {
	case *ir.Return:
		out := append([]ir.Stmt{}, before...)
		// After defers run, named results (including cell-boxed ones) are the
		// source of truth — drop pre-computed Return operands.
		if fl != nil && len(fl.namedResults) > 0 {
			out = append(out, fl.resultReturnAfterDefer())
			return out
		}
		out = append(out, s)
		return out
	case *ir.Seq:
		return []ir.Stmt{fl.rewriteReturns(s, before)}
	case *ir.Block:
		return []ir.Stmt{&ir.Block{Name: s.Name, Params: s.Params, Results: s.Results, Body: fl.rewriteReturns(s.Body, before), Pos: s.Pos}}
	case *ir.Loop:
		return []ir.Stmt{&ir.Loop{Name: s.Name, Params: s.Params, Body: fl.rewriteReturns(s.Body, before), Pos: s.Pos}}
	case *ir.If:
		var elseR ir.Region
		if s.Else != nil {
			elseR = fl.rewriteReturns(s.Else, before)
		}
		return []ir.Stmt{&ir.If{Cond: s.Cond, Then: fl.rewriteReturns(s.Then, before), Else: elseR, Pos: s.Pos}}
	case *ir.Switch:
		ns := *s
		ns.Cases = append([]ir.Case(nil), s.Cases...)
		for i := range ns.Cases {
			ns.Cases[i].Body = fl.rewriteReturns(ns.Cases[i].Body, before)
		}
		if s.Default != nil {
			ns.Default = fl.rewriteReturns(s.Default, before)
		}
		return []ir.Stmt{&ns}
	case *ir.Try:
		return []ir.Stmt{&ir.Try{
			Name: s.Name, Body: fl.rewriteReturns(s.Body, before), Tag: s.Tag,
			Params: s.Params, Handler: fl.rewriteReturns(s.Handler, before), Pos: s.Pos,
		}}
	case *ir.Dispatch:
		ns := *s
		ns.Arms = make([]ir.Region, len(s.Arms))
		for i, arm := range s.Arms {
			if arm != nil {
				ns.Arms[i] = fl.rewriteReturns(arm, before)
			}
		}
		return []ir.Stmt{&ns}
	default:
		return []ir.Stmt{s}
	}
}
