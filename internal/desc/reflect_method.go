package desc

import (
	"go/token"
	"go/types"
	"strconv"
	"strings"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
)

type reflectMeth struct {
	name     string
	exprKey  string
	valKey   string
	exprWrap string
	valWrap  string
	closName string
	recvIR   ir.Type
	sel      *types.Selection
	fn       *types.Func
	exprRecv types.Type
	declRecv types.Type
}

type reflectMethTable struct {
	tr   *typeRec
	conc types.Type
	ms   []reflectMeth
}

// isReflectMethodAPI reports whether name is a reflect Method/MethodByName
// entry or an itab wrapper for one. An itab RefFunc to these must not keep
// every exported method live — only an actual OpCall does (cmd/link REFLECTMETHOD).
func isReflectMethodAPI(name string) bool {
	switch name {
	case "reflect.Value.Method", "reflect.Value.MethodByName",
		"reflect.(*rtype).Method", "reflect.(*rtype).MethodByName":
		return true
	}
	// itab wrappers: $go.wrap.reflect_Type.… .Method / .MethodByName
	if strings.Contains(name, "reflect") {
		if strings.HasSuffix(name, ".Method") || strings.HasSuffix(name, ".MethodByName") {
			return true
		}
	}
	return false
}

// needsReflectMethodBodies reports whether the program can reach
// reflect.Value/Type.Method or MethodByName via a real call (OpCall or
// call_ref of the ifacefn). Itab RefFunc edges alone do not count
// (fmt.Println imports reflect.Type whose itab lists Method).
func (g *gen) needsReflectMethodBodies() bool {
	live := g.reachableFuncsRefFuncSkipMethodAPI()
	for name := range live {
		if isReflectMethodAPI(name) {
			return true
		}
		f := g.mod.LookupFunc(name)
		if f == nil || f.Body == nil {
			continue
		}
		found := false
		walkRegion(f.Body, func(e ir.Expr) {
			in, ok := e.(*ir.Instr)
			if !ok {
				return
			}
			// desc.Populate runs before RuntimeOps expands OpIfaceCall → call_ref.
			// Match reflect.Type/Value and user ifaces that re-export Method
			// (reflectmethod2/3 MyType); itab RefFunc alone must not match.
			if in.Op == ir.OpIfaceCall && isReflectMethodSelector(in.Func) {
				found = true
			}
			if in.Op == ir.OpCallRef && callRefIsReflectMethod(in) {
				found = true
			}
		})
		if found {
			return true
		}
	}
	return false
}

func isReflectMethodSelector(name string) bool {
	return name == "Method" || name == "MethodByName"
}

// callRefIsReflectMethod reports a call_ref of reflect.Type/Value.Method
// or MethodByName (ifacefn type name encodes the selector).
func callRefIsReflectMethod(in *ir.Instr) bool {
	n := ifaceFnName(in.Type)
	if n == "" {
		return false
	}
	if !strings.Contains(n, "ifacefn.reflect_Type.") && !strings.Contains(n, "ifacefn.reflect_Value.") {
		return false
	}
	return strings.HasSuffix(n, ".Method") || strings.HasSuffix(n, ".MethodByName")
}

func ifaceFnName(t ir.Type) string {
	if x, ok := t.(*ir.Ref); ok {
		return x.Named
	}
	return ""
}

// reachableFuncsRefFuncSkipMethodAPI is like reachableFuncs, but RefFunc
// edges into Method/MethodByName APIs do not mark them live. OpCall still does.
func (g *gen) reachableFuncsRefFuncSkipMethodAPI() map[string]bool {
	live := map[string]bool{}
	var q []string
	mark := func(name string) {
		if name == "" || live[name] {
			return
		}
		if g.mod.LookupFunc(name) == nil {
			return
		}
		live[name] = true
		q = append(q, name)
	}
	for _, seed := range []string{"main.run", "main.main", "runtime.init", "main.init"} {
		mark(seed)
	}
	for _, f := range g.mod.Funcs {
		if strings.Contains(f.Name, ".init") || strings.HasSuffix(f.Name, ".init.0") {
			mark(f.Name)
		}
		if f.Export != "" {
			mark(f.Name)
		}
	}
	for len(q) > 0 {
		fn := q[0]
		q = q[1:]
		f := g.mod.LookupFunc(fn)
		if f == nil || f.Body == nil {
			continue
		}
		walkRegion(f.Body, func(e ir.Expr) {
			in, ok := e.(*ir.Instr)
			if !ok {
				return
			}
			if in.Op == ir.OpCall && in.Func != "" {
				mark(in.Func)
			}
			if in.Op == ir.OpRefFunc && in.Func != "" {
				if isReflectMethodAPI(in.Func) {
					return
				}
				mark(in.Func)
			}
		})
	}
	return live
}

// fillReflectMethodHelpers implements runtime.ReflectNumMethod / Method*
// when reflect.Type/Value.Method is reachable via a real call. Emits
// method-expression and method-value closures so Interface().(func…) works
// and DCE keeps the underlying methods (REFLECTMETHOD). When Method is only
// present on reflect.Type's itab (fmt.Println), stubs return empty/nil so
// exported methods stay pruneable.
func (g *gen) fillReflectMethodHelpers() error {
	if g.mod.LookupFunc("runtime.ReflectNumMethod") == nil {
		return nil
	}
	if !g.needsReflectMethodBodies() {
		g.fillReflectNumMethod(nil)
		g.fillReflectMethodName(nil)
		g.fillReflectMethodTypeOf(nil, true)
		g.fillReflectMethodTypeOf(nil, false)
		g.fillReflectMethodFunc(nil)
		_ = g.fillReflectMethodValue(nil)
		return nil
	}

	keepNames := g.reflectMethodByNameConstants() // nil ⇒ keep all exported

	var tables []reflectMethTable
	keys := make([]string, 0, len(g.types))
	for k := range g.types {
		keys = append(keys, k)
	}
	sortStrings(keys)

	for _, k := range keys {
		tr := g.types[k]
		if tr == nil || tr.id < 0 || tr.goType == nil {
			continue
		}
		goTy := types.Unalias(tr.goType)
		if _, isIface := goTy.Underlying().(*types.Interface); isIface {
			continue
		}
		ms := types.NewMethodSet(goTy)
		var infos []reflectMeth
		for i := 0; i < ms.Len(); i++ {
			sel := ms.At(i)
			fn, ok := sel.Obj().(*types.Func)
			if !ok || !fn.Exported() {
				continue
			}
			if keepNames != nil && !keepNames[fn.Name()] {
				continue
			}
			// Only methods lowered into this module. Stdlib descriptors may
			// list methods whose bodies were never linked (e.g. runtime.Func).
			if g.mod.LookupFunc(methodIRName(fn)) == nil {
				continue
			}
			msig := fn.Type().(*types.Signature)
			exprRecv := goTy
			exprSig := types.NewSignatureType(nil, nil, nil,
				tupleWithRecv(exprRecv, msig.Params()),
				msig.Results(), msig.Variadic())
			valSig := types.NewSignatureType(nil, nil, nil, msig.Params(), msig.Results(), msig.Variadic())
			exprKey := TypeKey(exprSig)
			valKey := TypeKey(valSig)
			g.ensureFuncTypeDesc(exprKey, exprSig)
			g.ensureFuncTypeDesc(valKey, valSig)

			declRecv := msig.Recv().Type()
			recvIR, err := mapTypeApprox(declRecv)
			if err != nil {
				recvIR = &ir.Ref{Null: true, Heap: ir.HeapAny}
			}
			exprWrap, err := g.ensureReflectMethodExpr(fn, exprRecv, declRecv)
			if err != nil {
				return err
			}
			closName, valWrap, err := g.ensureReflectMethodValue(fn, recvIR)
			if err != nil {
				return err
			}
			infos = append(infos, reflectMeth{
				name:     fn.Name(),
				exprKey:  exprKey,
				valKey:   valKey,
				exprWrap: exprWrap,
				valWrap:  valWrap,
				closName: closName,
				recvIR:   recvIR,
				sel:      sel,
				fn:       fn,
				exprRecv: exprRecv,
				declRecv: declRecv,
			})
		}
		if len(infos) == 0 {
			continue
		}
		tables = append(tables, reflectMethTable{tr: tr, conc: goTy, ms: infos})
	}

	g.fillReflectNumMethod(tables)
	g.fillReflectMethodName(tables)
	g.fillReflectMethodTypeOf(tables, true)
	g.fillReflectMethodTypeOf(tables, false)
	g.fillReflectMethodFunc(tables)
	return g.fillReflectMethodValue(tables)
}

// reflectMethodByNameConstants returns the set of method names if every
// reachable Method/MethodByName use is MethodByName with a string literal
// (OpCall or OpIfaceCall). nil means keep all exported methods (e.g. when
// Method(index) is used, or any name is non-constant).
func (g *gen) reflectMethodByNameConstants() map[string]bool {
	live := g.reachableFuncsRefFuncSkipMethodAPI()
	byName := false
	index := false
	names := map[string]bool{}
	ok := true
	for fnName := range live {
		if isReflectMethodAPI(fnName) {
			if strings.HasSuffix(fnName, "MethodByName") {
				byName = true
			} else if strings.HasSuffix(fnName, ".Method") || fnName == "reflect.Value.Method" {
				index = true
			}
		}
		f := g.mod.LookupFunc(fnName)
		if f == nil || f.Body == nil {
			continue
		}
		walkRegion(f.Body, func(e ir.Expr) {
			in, okE := e.(*ir.Instr)
			if !okE {
				return
			}
			switch in.Op {
			case ir.OpCall:
				if !isReflectMethodAPI(in.Func) {
					return
				}
				if strings.HasSuffix(in.Func, "MethodByName") {
					byName = true
					if len(in.Args) < 1 {
						ok = false
						return
					}
					s, isLit := stringConstArg(in.Args[len(in.Args)-1])
					if !isLit {
						ok = false
						return
					}
					names[s] = true
					return
				}
				index = true
			case ir.OpIfaceCall:
				if !isReflectMethodSelector(in.Func) {
					return
				}
				if in.Func == "MethodByName" {
					byName = true
					if len(in.Args) < 1 {
						ok = false
						return
					}
					s, isLit := stringConstArg(in.Args[len(in.Args)-1])
					if !isLit {
						ok = false
						return
					}
					names[s] = true
					return
				}
				index = true
			}
		})
		if !ok {
			return nil
		}
	}
	if index || !byName || len(names) == 0 {
		return nil
	}
	return names
}

func stringConstArg(e ir.Expr) (string, bool) {
	if c, ok := e.(*ir.Const); ok && c.Op == ir.OpStringLit {
		return c.Str, true
	}
	return "", false
}

func (g *gen) ensureFuncTypeDesc(key string, sig *types.Signature) {
	g.needTypeKey(key)
	tr := g.types[key]
	if tr == nil {
		return
	}
	if tr.goType == nil {
		tr.goType = sig
	}
	tr.kind = KindFunc
	if tr.id < 0 {
		tr.id = g.nextID
		g.nextID++
	}
	_ = g.emitTypeGlobal(tr)
}

func tupleWithRecv(recv types.Type, params *types.Tuple) *types.Tuple {
	vars := make([]*types.Var, 0, 1+params.Len())
	vars = append(vars, types.NewVar(token.NoPos, nil, "", recv))
	for i := 0; i < params.Len(); i++ {
		vars = append(vars, params.At(i))
	}
	return types.NewTuple(vars...)
}

func (g *gen) ensureClosBase() {
	if g.mod.LookupType("$go.clos") != nil {
		return
	}
	g.mod.InternType(&ir.TypeDef{
		Name:  "$go.clos",
		Kind:  ir.TypeStruct,
		Super: "$go.object",
		Fields: []ir.Field{
			{Name: "fn", Type: &ir.Ref{Null: true, Heap: ir.HeapFunc}},
		},
	})
}

// ensureReflectMethodExpr emits (clos, exprRecv, args…) → call method.
func (g *gen) ensureReflectMethodExpr(fn *types.Func, exprRecv, declRecv types.Type) (string, error) {
	g.ensureClosBase()
	wrapper := "$go.reflect.mexpr." + sanitize(TypeKey(exprRecv)) + "." + sanitize(methodIRName(fn))
	if g.mod.LookupFunc(wrapper) != nil {
		return wrapper, nil
	}
	msig := fn.Type().(*types.Signature)
	closParam := &ir.Var{Name: "clos", Typ: &ir.Ref{Named: "$go.clos"}}
	exprIR, _ := mapTypeApprox(exprRecv)
	declIR, _ := mapTypeApprox(declRecv)
	params := []*ir.Var{closParam}
	recvV := &ir.Var{Name: "recv", Typ: exprIR}
	params = append(params, recvV)
	recvExpr := ir.Expr(&ir.Name{Raw: recvV.Name, Var: recvV})
	var adapt []ir.Stmt

	needPtr := isPointer(declRecv)
	havePtr := isPointer(exprRecv)
	switch {
	case !needPtr && havePtr:
		elem := derefIfPtr(exprRecv)
		if isStruct(elem) || isArray(elem) {
			recvExpr = &ir.Instr{Op: ir.OpRefCast, Type: declIR, Args: []ir.Expr{recvExpr}}
		} else {
			t := &ir.Temp{Name: "%rm.deref", Typ: declIR}
			adapt = append(adapt, &ir.Assign{
				Lhs: []ir.Dest{{Temp: t, Name: t.Name}},
				Rhs: &ir.Instr{Op: ir.OpDeref, Type: declIR, Args: []ir.Expr{recvExpr}},
			})
			recvExpr = &ir.Name{Raw: t.Name, Temp: t}
		}
	case needPtr && !havePtr:
		recvExpr = &ir.Instr{Op: ir.OpRefCast, Type: declIR, Args: []ir.Expr{recvExpr}}
	default:
		if _, ok := declIR.(*ir.Ref); ok {
			recvExpr = &ir.Instr{Op: ir.OpRefCast, Type: declIR, Args: []ir.Expr{recvExpr}}
		}
	}

	callArgs := []ir.Expr{recvExpr}
	for i := 0; i < msig.Params().Len(); i++ {
		pt, _ := mapTypeApprox(msig.Params().At(i).Type())
		v := &ir.Var{Name: "a" + strconv.Itoa(i), Typ: pt}
		params = append(params, v)
		callArgs = append(callArgs, &ir.Name{Raw: v.Name, Var: v})
	}
	var results []ir.Type
	for i := 0; i < msig.Results().Len(); i++ {
		rt, _ := mapTypeApprox(msig.Results().At(i).Type())
		results = append(results, rt)
	}
	call := &ir.Instr{Op: ir.OpCall, Func: methodIRName(fn), Args: callArgs}
	body := reflectWrapperCallBody(call, results)
	if len(adapt) > 0 {
		seq := body.(*ir.Seq)
		body = &ir.Seq{Stmts: append(adapt, seq.Stmts...)}
	}
	g.mod.Funcs = append(g.mod.Funcs, &ir.Func{
		Name:    wrapper,
		Params:  params,
		Results: results,
		Body:    body,
	})
	return wrapper, nil
}

func (g *gen) ensureReflectMethodValue(fn *types.Func, recvIR ir.Type) (closName, wrapper string, err error) {
	g.ensureClosBase()
	closName = "$go.clos.method." + sanitize(methodIRName(fn))
	if g.mod.LookupType(closName) == nil {
		g.mod.InternType(&ir.TypeDef{
			Name:  closName,
			Kind:  ir.TypeStruct,
			Super: "$go.clos",
			Fields: []ir.Field{
				{Name: "fn", Type: &ir.Ref{Null: true, Heap: ir.HeapFunc}},
				{Name: "recv", Type: recvIR},
			},
		})
	}
	wrapper = methodIRName(fn) + "$mval"
	if g.mod.LookupFunc(wrapper) != nil {
		return closName, wrapper, nil
	}
	msig := fn.Type().(*types.Signature)
	closParam := &ir.Var{Name: "clos", Typ: &ir.Ref{Named: "$go.clos"}}
	params := []*ir.Var{closParam}
	typed := ir.Expr(&ir.Instr{Op: ir.OpRefCast, Type: &ir.Ref{Named: closName}, Args: []ir.Expr{
		&ir.Name{Raw: closParam.Name, Var: closParam},
	}})
	recvGet := &ir.Instr{
		Op:    ir.OpStructGet,
		Type:  &ir.Ref{Named: closName},
		Field: "recv",
		Args:  []ir.Expr{typed},
	}
	callArgs := []ir.Expr{recvGet}
	for i := 0; i < msig.Params().Len(); i++ {
		pt, _ := mapTypeApprox(msig.Params().At(i).Type())
		v := &ir.Var{Name: "a" + strconv.Itoa(i), Typ: pt}
		params = append(params, v)
		callArgs = append(callArgs, &ir.Name{Raw: v.Name, Var: v})
	}
	var results []ir.Type
	for i := 0; i < msig.Results().Len(); i++ {
		rt, _ := mapTypeApprox(msig.Results().At(i).Type())
		results = append(results, rt)
	}
	call := &ir.Instr{Op: ir.OpCall, Func: methodIRName(fn), Args: callArgs}
	g.mod.Funcs = append(g.mod.Funcs, &ir.Func{
		Name:    wrapper,
		Params:  params,
		Results: results,
		Body:    reflectWrapperCallBody(call, results),
	})
	return closName, wrapper, nil
}

func reflectWrapperCallBody(call ir.Expr, results []ir.Type) ir.Region {
	switch len(results) {
	case 0:
		return &ir.Seq{Stmts: []ir.Stmt{&ir.ExprStmt{X: call}, &ir.Return{}}}
	case 1:
		return &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{call}}}}
	default:
		temps := make([]*ir.Temp, len(results))
		lhs := make([]ir.Dest, len(results))
		rets := make([]ir.Expr, len(results))
		for i, ty := range results {
			t := &ir.Temp{Name: "%" + strconv.Itoa(i), Typ: ty}
			temps[i] = t
			lhs[i] = ir.Dest{Temp: t, Name: t.Name}
			rets[i] = &ir.Name{Raw: t.Name, Temp: t}
		}
		return &ir.Seq{Stmts: []ir.Stmt{
			&ir.Assign{Lhs: lhs, Rhs: call},
			&ir.Return{Results: rets},
		}}
	}
}

func (g *gen) fillReflectNumMethod(tables []reflectMethTable) {
	name := "runtime.ReflectNumMethod"
	typeTy := &ir.Ref{Null: true, Named: "$go.type"}
	t := &ir.Var{Name: "t", Typ: typeTy}
	idV := &ir.Var{Name: "id", Typ: ir.TypeI32}
	zero := &ir.Const{Op: ir.OpI32Const, I64: 0}

	var chain ir.Stmt = &ir.Return{Results: []ir.Expr{zero}}
	for i := len(tables) - 1; i >= 0; i-- {
		tab := tables[i]
		chain = &ir.If{
			Cond: &ir.Instr{Op: ir.OpI32Eq, Args: []ir.Expr{
				&ir.Name{Raw: "id", Var: idV},
				&ir.Const{Op: ir.OpI32Const, I64: int64(tab.tr.id)},
			}},
			Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{
				&ir.Const{Op: ir.OpI32Const, I64: int64(len(tab.ms))},
			}}}},
			Else: &ir.Seq{Stmts: []ir.Stmt{chain}},
		}
	}
	body := &ir.Seq{Stmts: []ir.Stmt{
		&ir.VarDef{Var: idV},
		&ir.If{
			Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{&ir.Name{Raw: "t", Var: t}}},
			Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{zero}}}},
			Else: &ir.Seq{Stmts: []ir.Stmt{
				&ir.Assign{Lhs: []ir.Dest{{Name: "id", Var: idV}}, Rhs: &ir.Instr{
					Op: ir.OpStructGet, Type: &ir.Ref{Named: "$go.type"}, Field: "id",
					Args: []ir.Expr{&ir.Name{Raw: "t", Var: t}},
				}},
				chain,
			}},
		},
		&ir.Unreachable{},
	}}
	g.replaceBody(name, []*ir.Var{t}, []ir.Type{ir.TypeI32}, body)
}

func (g *gen) fillReflectMethodName(tables []reflectMethTable) {
	name := "runtime.ReflectMethodName"
	typeTy := &ir.Ref{Null: true, Named: "$go.type"}
	t := &ir.Var{Name: "t", Typ: typeTy}
	i := &ir.Var{Name: "i", Typ: ir.TypeI32}
	idV := &ir.Var{Name: "id", Typ: ir.TypeI32}
	empty := g.stringConstExpr("")

	var chain ir.Stmt = &ir.Return{Results: []ir.Expr{empty}}
	for ti := len(tables) - 1; ti >= 0; ti-- {
		tab := tables[ti]
		var ichain ir.Stmt = &ir.Return{Results: []ir.Expr{empty}}
		for mi := len(tab.ms) - 1; mi >= 0; mi-- {
			m := tab.ms[mi]
			ichain = &ir.If{
				Cond: &ir.Instr{Op: ir.OpI32Eq, Args: []ir.Expr{
					&ir.Name{Raw: "i", Var: i},
					&ir.Const{Op: ir.OpI32Const, I64: int64(mi)},
				}},
				Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{g.stringConstExpr(m.name)}}}},
				Else: &ir.Seq{Stmts: []ir.Stmt{ichain}},
			}
		}
		chain = &ir.If{
			Cond: &ir.Instr{Op: ir.OpI32Eq, Args: []ir.Expr{
				&ir.Name{Raw: "id", Var: idV},
				&ir.Const{Op: ir.OpI32Const, I64: int64(tab.tr.id)},
			}},
			Then: &ir.Seq{Stmts: []ir.Stmt{ichain}},
			Else: &ir.Seq{Stmts: []ir.Stmt{chain}},
		}
	}
	body := &ir.Seq{Stmts: []ir.Stmt{
		&ir.VarDef{Var: idV},
		&ir.If{
			Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{&ir.Name{Raw: "t", Var: t}}},
			Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{empty}}}},
			Else: &ir.Seq{Stmts: []ir.Stmt{
				&ir.Assign{Lhs: []ir.Dest{{Name: "id", Var: idV}}, Rhs: &ir.Instr{
					Op: ir.OpStructGet, Type: &ir.Ref{Named: "$go.type"}, Field: "id",
					Args: []ir.Expr{&ir.Name{Raw: "t", Var: t}},
				}},
				chain,
			}},
		},
		&ir.Unreachable{},
	}}
	g.replaceBody(name, []*ir.Var{t, i}, []ir.Type{&ir.Ref{Named: "$go.string"}}, body)
}

// fillReflectMethodTypeOf fills ReflectMethodType (expr=true) or
// ReflectMethodValueType (expr=false).
func (g *gen) fillReflectMethodTypeOf(tables []reflectMethTable, expr bool) {
	name := "runtime.ReflectMethodType"
	if !expr {
		name = "runtime.ReflectMethodValueType"
	}
	typeTy := &ir.Ref{Null: true, Named: "$go.type"}
	t := &ir.Var{Name: "t", Typ: typeTy}
	i := &ir.Var{Name: "i", Typ: ir.TypeI32}
	idV := &ir.Var{Name: "id", Typ: ir.TypeI32}
	nullT := &ir.Instr{Op: ir.OpRefNull, Type: typeTy}

	var chain ir.Stmt = &ir.Return{Results: []ir.Expr{nullT}}
	for ti := len(tables) - 1; ti >= 0; ti-- {
		tab := tables[ti]
		var ichain ir.Stmt = &ir.Return{Results: []ir.Expr{nullT}}
		for mi := len(tab.ms) - 1; mi >= 0; mi-- {
			m := tab.ms[mi]
			key := m.valKey
			if expr {
				key = m.exprKey
			}
			tr := g.types[key]
			var ret ir.Expr = nullT
			if tr != nil && tr.global != "" {
				ret = &ir.Instr{Op: ir.OpGlobalGet, Global: tr.global}
			}
			ichain = &ir.If{
				Cond: &ir.Instr{Op: ir.OpI32Eq, Args: []ir.Expr{
					&ir.Name{Raw: "i", Var: i},
					&ir.Const{Op: ir.OpI32Const, I64: int64(mi)},
				}},
				Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{ret}}}},
				Else: &ir.Seq{Stmts: []ir.Stmt{ichain}},
			}
		}
		chain = &ir.If{
			Cond: &ir.Instr{Op: ir.OpI32Eq, Args: []ir.Expr{
				&ir.Name{Raw: "id", Var: idV},
				&ir.Const{Op: ir.OpI32Const, I64: int64(tab.tr.id)},
			}},
			Then: &ir.Seq{Stmts: []ir.Stmt{ichain}},
			Else: &ir.Seq{Stmts: []ir.Stmt{chain}},
		}
	}
	body := &ir.Seq{Stmts: []ir.Stmt{
		&ir.VarDef{Var: idV},
		&ir.If{
			Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{&ir.Name{Raw: "t", Var: t}}},
			Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{nullT}}}},
			Else: &ir.Seq{Stmts: []ir.Stmt{
				&ir.Assign{Lhs: []ir.Dest{{Name: "id", Var: idV}}, Rhs: &ir.Instr{
					Op: ir.OpStructGet, Type: &ir.Ref{Named: "$go.type"}, Field: "id",
					Args: []ir.Expr{&ir.Name{Raw: "t", Var: t}},
				}},
				chain,
			}},
		},
		&ir.Unreachable{},
	}}
	g.replaceBody(name, []*ir.Var{t, i}, []ir.Type{typeTy}, body)
}

func (g *gen) fillReflectMethodFunc(tables []reflectMethTable) {
	name := "runtime.ReflectMethodFunc"
	typeTy := &ir.Ref{Null: true, Named: "$go.type"}
	anyT := &ir.Ref{Null: true, Heap: ir.HeapAny}
	t := &ir.Var{Name: "t", Typ: typeTy}
	i := &ir.Var{Name: "i", Typ: ir.TypeI32}
	idV := &ir.Var{Name: "id", Typ: ir.TypeI32}
	nullA := &ir.Instr{Op: ir.OpRefNull, Type: anyT}
	g.ensureClosBase()

	var chain ir.Stmt = &ir.Return{Results: []ir.Expr{nullA}}
	for ti := len(tables) - 1; ti >= 0; ti-- {
		tab := tables[ti]
		var ichain ir.Stmt = &ir.Return{Results: []ir.Expr{nullA}}
		for mi := len(tab.ms) - 1; mi >= 0; mi-- {
			m := tab.ms[mi]
			clos := &ir.Instr{
				Op:   ir.OpStructNew,
				Type: &ir.Ref{Named: "$go.clos"},
				Args: []ir.Expr{
					&ir.Instr{Op: ir.OpRefFunc, Func: m.exprWrap},
				},
			}
			ichain = &ir.If{
				Cond: &ir.Instr{Op: ir.OpI32Eq, Args: []ir.Expr{
					&ir.Name{Raw: "i", Var: i},
					&ir.Const{Op: ir.OpI32Const, I64: int64(mi)},
				}},
				Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{
					&ir.Instr{Op: ir.OpRefCast, Type: anyT, Args: []ir.Expr{clos}},
				}}}},
				Else: &ir.Seq{Stmts: []ir.Stmt{ichain}},
			}
		}
		chain = &ir.If{
			Cond: &ir.Instr{Op: ir.OpI32Eq, Args: []ir.Expr{
				&ir.Name{Raw: "id", Var: idV},
				&ir.Const{Op: ir.OpI32Const, I64: int64(tab.tr.id)},
			}},
			Then: &ir.Seq{Stmts: []ir.Stmt{ichain}},
			Else: &ir.Seq{Stmts: []ir.Stmt{chain}},
		}
	}
	body := &ir.Seq{Stmts: []ir.Stmt{
		&ir.VarDef{Var: idV},
		&ir.If{
			Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{&ir.Name{Raw: "t", Var: t}}},
			Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{nullA}}}},
			Else: &ir.Seq{Stmts: []ir.Stmt{
				&ir.Assign{Lhs: []ir.Dest{{Name: "id", Var: idV}}, Rhs: &ir.Instr{
					Op: ir.OpStructGet, Type: &ir.Ref{Named: "$go.type"}, Field: "id",
					Args: []ir.Expr{&ir.Name{Raw: "t", Var: t}},
				}},
				chain,
			}},
		},
		&ir.Unreachable{},
	}}
	g.replaceBody(name, []*ir.Var{t, i}, []ir.Type{anyT}, body)
}

func (g *gen) fillReflectMethodValue(tables []reflectMethTable) error {
	name := "runtime.ReflectMethodValue"
	typeTy := &ir.Ref{Null: true, Named: "$go.type"}
	anyT := &ir.Ref{Null: true, Heap: ir.HeapAny}
	t := &ir.Var{Name: "t", Typ: typeTy}
	i := &ir.Var{Name: "i", Typ: ir.TypeI32}
	// Named "data" so loadRecv's Raw:"data" Name nodes resolve.
	recv := &ir.Var{Name: "data", Typ: anyT}
	idV := &ir.Var{Name: "id", Typ: ir.TypeI32}
	nullA := &ir.Instr{Op: ir.OpRefNull, Type: anyT}
	g.ensureClosBase()

	var chain ir.Stmt = &ir.Return{Results: []ir.Expr{nullA}}
	for ti := len(tables) - 1; ti >= 0; ti-- {
		tab := tables[ti]
		var ichain ir.Stmt = &ir.Return{Results: []ir.Expr{nullA}}
		for mi := len(tab.ms) - 1; mi >= 0; mi-- {
			m := tab.ms[mi]
			// Convert Value data word → declared receiver via loadRecv.
			stmts, recvExpr, err := g.loadRecv(tab.conc, m.sel.Index(), m.declRecv, m.recvIR, recv)
			if err != nil {
				return err
			}
			clos := &ir.Instr{
				Op:   ir.OpStructNew,
				Type: &ir.Ref{Named: m.closName},
				Args: []ir.Expr{
					&ir.Instr{Op: ir.OpRefFunc, Func: m.valWrap},
					recvExpr,
				},
			}
			castClos := &ir.Instr{Op: ir.OpRefCast, Type: &ir.Ref{Named: "$go.clos"}, Args: []ir.Expr{clos}}
			retStmts := append(stmts, &ir.Return{Results: []ir.Expr{
				&ir.Instr{Op: ir.OpRefCast, Type: anyT, Args: []ir.Expr{castClos}},
			}})
			ichain = &ir.If{
				Cond: &ir.Instr{Op: ir.OpI32Eq, Args: []ir.Expr{
					&ir.Name{Raw: "i", Var: i},
					&ir.Const{Op: ir.OpI32Const, I64: int64(mi)},
				}},
				Then: &ir.Seq{Stmts: retStmts},
				Else: &ir.Seq{Stmts: []ir.Stmt{ichain}},
			}
		}
		chain = &ir.If{
			Cond: &ir.Instr{Op: ir.OpI32Eq, Args: []ir.Expr{
				&ir.Name{Raw: "id", Var: idV},
				&ir.Const{Op: ir.OpI32Const, I64: int64(tab.tr.id)},
			}},
			Then: &ir.Seq{Stmts: []ir.Stmt{ichain}},
			Else: &ir.Seq{Stmts: []ir.Stmt{chain}},
		}
	}
	body := &ir.Seq{Stmts: []ir.Stmt{
		&ir.VarDef{Var: idV},
		&ir.If{
			Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{&ir.Name{Raw: "t", Var: t}}},
			Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{nullA}}}},
			Else: &ir.Seq{Stmts: []ir.Stmt{
				&ir.Assign{Lhs: []ir.Dest{{Name: "id", Var: idV}}, Rhs: &ir.Instr{
					Op: ir.OpStructGet, Type: &ir.Ref{Named: "$go.type"}, Field: "id",
					Args: []ir.Expr{&ir.Name{Raw: "t", Var: t}},
				}},
				chain,
			}},
		},
		&ir.Unreachable{},
	}}
	g.replaceBody(name, []*ir.Var{t, i, recv}, []ir.Type{anyT}, body)
	return nil
}
