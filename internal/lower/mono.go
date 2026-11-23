package lower

import (
	"fmt"
	"go/ast"
	"go/types"
	"strconv"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
	"github.com/achille-roussel/wasmgc-go/internal/mono"
)

// emitMonoInstances lowers every collected specialization whose origin is
// declared in this package. Nested Ensure calls during lowering may add more;
// iterate until fixpoint.
func (l *pkgLower) emitMonoInstances() error {
	if l.mono == nil {
		return nil
	}
	for {
		var pending []*mono.Instance
		for _, inst := range l.mono.ByName {
			if inst == nil || inst.Origin == nil {
				continue
			}
			if l.mono.PkgFor[inst.Origin] != l.pkg {
				continue
			}
			if l.mod.LookupFunc(inst.IRName) != nil {
				continue
			}
			pending = append(pending, inst)
		}
		if len(pending) == 0 {
			return nil
		}
		for _, inst := range pending {
			fn, err := l.lowerInstance(inst)
			if err != nil {
				return err
			}
			l.mod.Funcs = append(l.mod.Funcs, fn)
		}
	}
}

func (l *pkgLower) lowerInstance(inst *mono.Instance) (*ir.Func, error) {
	fd := l.mono.Decl[inst.Origin]
	if fd == nil {
		return nil, fmt.Errorf("mono: no FuncDecl for %s", inst.Origin.FullName())
	}
	info := l.mono.InfoFor[inst.Origin]
	if info == nil {
		info = l.info
	}
	oldSubst, oldInfo := l.subst, l.info
	l.subst = inst.Subst
	l.info = info
	defer func() {
		l.subst = oldSubst
		l.info = oldInfo
	}()

	fl, err := l.newFuncLowerInst(fd, inst)
	if err != nil {
		return nil, err
	}
	return l.finishLowerFunc(fl, fd)
}

// finishLowerFunc shares body finishing with lowerFunc.
func (l *pkgLower) finishLowerFunc(fl *funcLower, fd *ast.FuncDecl) (*ir.Func, error) {
	if containsRecover(fd.Body, fl.info) {
		fl.needsRecoverPrologue = true
		fl.allowVar = fl.newVar("allow", ir.TypeI32)
	}
	if containsPanicCall(fd.Body, fl.info) {
		fl.mayPanic = true
	}

	body, err := fl.stmtBlock(fd.Body)
	if err != nil {
		return nil, err
	}
	seq, ok := body.(*ir.Seq)
	if !ok {
		seq = &ir.Seq{Stmts: []ir.Stmt{body}}
	}
	if len(fl.results) == 0 && !endsWithReturn(seq) {
		seq.Stmts = append(seq.Stmts, &ir.Return{})
	}

	originSig := fl.obj.Type().(*types.Signature)
	var clones []ir.Stmt
	for i := 0; i < originSig.Params().Len(); i++ {
		p := originSig.Params().At(i)
		pt := p.Type()
		if len(l.subst) > 0 {
			pt = l.subst.Apply(pt)
		}
		if isPointer(pt) || !isStruct(pt) {
			continue
		}
		if !fl.paramFieldAssigned(p, fd.Body) {
			continue
		}
		clone, err := fl.cloneStruct(fl.vars[p])
		if err != nil {
			return nil, err
		}
		clones = append(clones, clone...)
	}
	if recv := originSig.Recv(); recv != nil {
		rt := recv.Type()
		if len(l.subst) > 0 {
			rt = l.subst.Apply(rt)
		}
		if !isPointer(rt) && isStruct(rt) && fl.paramFieldAssigned(recv, fd.Body) {
			clone, err := fl.cloneStruct(fl.vars[recv])
			if err != nil {
				return nil, err
			}
			clones = append(clones, clone...)
		}
	}

	var preamble []ir.Stmt
	for _, v := range fl.namedResults {
		preamble = append(preamble, &ir.VarDef{Var: v})
	}
	for _, v := range fl.extraLocals {
		preamble = append(preamble, &ir.VarDef{Var: v})
	}
	for _, v := range append(fl.namedResults, fl.extraLocals...) {
		if !needsZeroInit(v.Typ) {
			continue
		}
		if r, ok := v.Typ.(*ir.Ref); ok && len(r.Named) >= 9 && r.Named[:9] == "$go.cell." {
			continue
		}
		z, err := fl.zeroValue(v.Typ)
		if err != nil {
			return nil, err
		}
		preamble = append(preamble, fl.assignVar(v, z))
	}
	for _, ci := range fl.cellInits {
		preamble = append(preamble, fl.assignVar(ci.cell, &ir.Instr{
			Op:   ir.OpStructNew,
			Type: &ir.Ref{Named: ci.cellTy},
			Args: []ir.Expr{fl.useVar(ci.orig)},
		}))
	}
	preamble = append(preamble, clones...)
	if fl.needsRecoverPrologue && !fl.hasDefer {
		preamble = append(preamble, fl.recoverPrologue()...)
	}
	if len(preamble) > 0 {
		seq.Stmts = append(preamble, seq.Stmts...)
	}
	if fl.hasDefer {
		seq, err = fl.applyDeferPass(seq)
		if err != nil {
			return nil, err
		}
		fl.pkg.meta.needsEH = true
	}
	if fl.mayPanic || fl.needsRecoverPrologue {
		fl.pkg.meta.needsEH = true
	}
	locals := append(fl.namedResults, fl.extraLocals...)
	if fl.allowVar != nil {
		locals = append(locals, fl.allowVar)
	}
	if fl.defersVar != nil {
		locals = append(locals, fl.defersVar)
	}
	return &ir.Func{
		Name:    fl.irName,
		Params:  fl.params,
		Results: fl.results,
		Body:    seq,
		Locals:  locals,
	}, nil
}

func (l *pkgLower) newFuncLowerInst(fd *ast.FuncDecl, inst *mono.Instance) (*funcLower, error) {
	origin := inst.Origin
	originSig := origin.Type().(*types.Signature)
	sig := inst.Sig
	if sig == nil {
		return nil, l.errorf(fd, "mono: nil concrete signature for %s", inst.IRName)
	}

	fl := &funcLower{
		pkg:    l,
		fset:   l.fset,
		info:   l.info,
		mod:    l.mod,
		obj:    origin,
		sig:    sig,
		irName: inst.IRName,
		vars:   map[types.Object]*ir.Var{},
		labels: map[string]*labelTargets{},
	}

	if recv := originSig.Recv(); recv != nil {
		ty, err := l.mapType(recv.Type())
		if err != nil {
			return nil, l.errorf(fd, "recv: %v", err)
		}
		name := recv.Name()
		if name == "" || name == "_" {
			name = "recv"
		}
		v := &ir.Var{Name: name, Typ: ty}
		fl.params = append(fl.params, v)
		fl.vars[recv] = v
	}
	for i := 0; i < originSig.Params().Len(); i++ {
		p := originSig.Params().At(i)
		ty, err := l.mapType(p.Type())
		if err != nil {
			return nil, l.errorf(fd, "param %s: %v", p.Name(), err)
		}
		name := p.Name()
		if name == "" {
			name = "_" + strconv.Itoa(i)
		}
		v := &ir.Var{Name: name, Typ: ty}
		fl.params = append(fl.params, v)
		fl.vars[p] = v
	}
	for i := 0; i < sig.Results().Len(); i++ {
		rty, err := l.mapType(sig.Results().At(i).Type())
		if err != nil {
			return nil, l.errorf(fd, "result: %v", err)
		}
		fl.results = append(fl.results, rty)
		if i < originSig.Results().Len() {
			rOrig := originSig.Results().At(i)
			if rOrig.Name() != "" {
				v := &ir.Var{Name: rOrig.Name(), Typ: rty}
				fl.namedResults = append(fl.namedResults, v)
				fl.vars[rOrig] = v
			}
		}
	}
	return fl, nil
}

// resolveGenericCallee returns the specialized IR name when e calls a generic
// function or a method of a generic type.
func (fl *funcLower) resolveGenericCallee(e *ast.CallExpr) (name string, results []ir.Type, ok bool, err error) {
	if fl.pkg.mono == nil {
		return "", nil, false, nil
	}
	fun := e.Fun
	switch x := fun.(type) {
	case *ast.IndexExpr:
		fun = x.X
	case *ast.IndexListExpr:
		fun = x.X
	}
	switch fun := fun.(type) {
	case *ast.Ident:
		return fl.resolveGenericIdent(fun)
	case *ast.SelectorExpr:
		if id, isID := fun.X.(*ast.Ident); isID {
			if _, isPkg := fl.info.Uses[id].(*types.PkgName); isPkg {
				return fl.resolveGenericIdent(fun.Sel)
			}
		}
		return fl.resolveGenericMethod(fun)
	}
	return "", nil, false, nil
}

func (fl *funcLower) resolveGenericIdent(id *ast.Ident) (name string, results []ir.Type, ok bool, err error) {
	obj := fl.info.Uses[id]
	fn, isFn := obj.(*types.Func)
	if !isFn {
		return "", nil, false, nil
	}
	origin := fn.Origin()
	if origin == nil {
		origin = fn
	}
	sig, _ := origin.Type().(*types.Signature)
	if sig == nil || sig.TypeParams().Len() == 0 {
		return "", nil, false, nil
	}
	instRec, has := fl.info.Instances[id]
	if !has {
		return "", nil, false, fl.errorf(id, "generic call %s missing Instance", id.Name)
	}
	args := fl.concreteArgs(instRec.TypeArgs)
	if args == nil {
		return "", nil, false, fl.errorf(id, "generic call %s has unsubstituted type args", id.Name)
	}
	inst, err := fl.pkg.mono.EnsureFunc(origin, args)
	if err != nil {
		return "", nil, false, fl.errorf(id, "%v", err)
	}
	results, err = fl.resultTypesOf(inst.Sig)
	if err != nil {
		return "", nil, false, err
	}
	return inst.IRName, results, true, nil
}

func (fl *funcLower) resolveGenericMethod(fun *ast.SelectorExpr) (name string, results []ir.Type, ok bool, err error) {
	sel := fl.info.Selections[fun]
	if sel == nil || sel.Kind() != types.MethodVal {
		return "", nil, false, nil
	}
	fn, isFn := sel.Obj().(*types.Func)
	if !isFn {
		return "", nil, false, nil
	}
	origin := fn.Origin()
	if origin == nil {
		return "", nil, false, nil
	}
	recv := fn.Type().(*types.Signature).Recv().Type()
	if len(fl.pkg.subst) > 0 {
		recv = fl.pkg.subst.Apply(recv)
	}
	named := namedTypeOf(recv)
	if named == nil {
		return "", nil, false, nil
	}
	args := make([]types.Type, 0)
	if targs := named.TypeArgs(); targs != nil {
		for i := 0; i < targs.Len(); i++ {
			a := targs.At(i)
			if mono.ContainsTypeParam(a) {
				return "", nil, false, nil
			}
			args = append(args, a)
		}
	}
	if len(args) == 0 {
		return "", nil, false, nil
	}
	if !methodNeedsMono(origin) {
		return "", nil, false, nil
	}
	inst, err := fl.pkg.mono.EnsureMethod(origin, recv)
	if err != nil {
		return "", nil, false, err
	}
	results, err = fl.resultTypesOf(inst.Sig)
	if err != nil {
		return "", nil, false, err
	}
	return inst.IRName, results, true, nil
}

func methodNeedsMono(origin *types.Func) bool {
	origSig := origin.Type().(*types.Signature)
	if origSig.TypeParams() != nil && origSig.TypeParams().Len() > 0 {
		return true
	}
	if r := origSig.Recv(); r != nil {
		if on := namedTypeOf(r.Type()); on != nil && on.Origin().TypeParams().Len() > 0 {
			return true
		}
	}
	return false
}

func (fl *funcLower) concreteArgs(targs *types.TypeList) []types.Type {
	if targs == nil {
		return nil
	}
	args := make([]types.Type, targs.Len())
	for i := 0; i < targs.Len(); i++ {
		a := targs.At(i)
		if len(fl.pkg.subst) > 0 {
			a = fl.pkg.subst.Apply(a)
		}
		if mono.ContainsTypeParam(a) {
			return nil
		}
		args[i] = a
	}
	return args
}

func (fl *funcLower) resultTypesOf(sig *types.Signature) ([]ir.Type, error) {
	var results []ir.Type
	for i := 0; i < sig.Results().Len(); i++ {
		ty, err := fl.mapType(sig.Results().At(i).Type())
		if err != nil {
			return nil, err
		}
		results = append(results, ty)
	}
	return results, nil
}

func namedTypeOf(t types.Type) *types.Named {
	t = types.Unalias(t)
	if p, ok := t.(*types.Pointer); ok {
		t = types.Unalias(p.Elem())
	}
	n, _ := t.(*types.Named)
	return n
}

// isGenericOrigin reports whether fn is a generic function or a method of a
// generic named type (origins are skipped; only concrete instances are lowered).
func isGenericOrigin(fn *types.Func) bool {
	if fn == nil {
		return false
	}
	sig, _ := fn.Type().(*types.Signature)
	if sig == nil {
		return false
	}
	if sig.TypeParams().Len() > 0 {
		return true
	}
	if r := sig.Recv(); r != nil {
		if n := namedTypeOf(r.Type()); n != nil {
			// Origin form: TypeParams set, or TypeArgs still type parameters.
			if n.Origin().TypeParams().Len() > 0 {
				if targs := n.TypeArgs(); targs != nil && targs.Len() > 0 {
					for i := 0; i < targs.Len(); i++ {
						if mono.ContainsTypeParam(targs.At(i)) {
							return true
						}
					}
					return false // concrete instantiation already
				}
				// Declared generic type with no args yet (rare on methods).
				if n.TypeParams().Len() > 0 {
					return true
				}
			}
		}
	}
	return false
}
