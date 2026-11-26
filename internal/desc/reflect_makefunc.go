package desc

import (
	"go/types"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
)

const (
	makeFuncClosVoid = "$go.clos.makefunc.void"
	makeFuncWrapVoid = "$go.makefunc.void"
	makeFuncClosUF0  = "$go.clos.makefunc.ufunc0"
	makeFuncWrapUF0  = "$go.makefunc.ufunc0"
	makeFuncImplTy   = "$go.struct.reflect.makeFuncImpl"
	makeFuncValueSlice = "$go.slice.go.struct.reflect.Value"
	makeFuncValueArr   = "$go.array.go.struct.reflect.Value"
	makeFuncValueTy    = "$go.struct.reflect.Value"
	makeFuncCbFuncTy   = "$go.func.makefunc.cb" // (clos, []Value) []Value
)

// fillReflectMakeFuncHelpers implements runtime.ReflectMakeFuncClos so
// MakeFunc(...).Interface().(func…) yields a real $go.clos trampoline.
// Trampolines are raw IR (no clearRecoverOk) so deferred MakeFunc values
// preserve recoverOk for the user callback (recover.go test15).
func (g *gen) fillReflectMakeFuncHelpers() error {
	name := "runtime.ReflectMakeFuncClos"
	if g.mod.LookupFunc(name) == nil {
		return nil
	}
	if g.mod.LookupType(makeFuncImplTy) == nil {
		// reflect.MakeFunc not linked into this module.
		g.replaceBody(name, []*ir.Var{
			{Name: "t", Typ: &ir.Ref{Null: true, Named: "$go.type"}},
			{Name: "impl", Typ: &ir.Ref{Null: true, Heap: ir.HeapAny}},
		}, []ir.Type{&ir.Ref{Null: true, Heap: ir.HeapAny}}, &ir.Seq{Stmts: []ir.Stmt{
			&ir.Return{Results: []ir.Expr{&ir.Instr{Op: ir.OpRefNull, Type: &ir.Ref{Null: true, Heap: ir.HeapAny}}}},
		}})
		return nil
	}
	g.ensureClosBase()
	g.ensureMakeFuncCbFuncType()
	g.ensureMakeFuncVoidTrampoline()
	g.ensureMakeFuncUFunc0Trampoline()

	typeTy := &ir.Ref{Null: true, Named: "$go.type"}
	anyT := &ir.Ref{Null: true, Heap: ir.HeapAny}
	t := &ir.Var{Name: "t", Typ: typeTy}
	impl := &ir.Var{Name: "impl", Typ: anyT}
	idV := &ir.Var{Name: "id", Typ: ir.TypeI32}
	nullA := &ir.Instr{Op: ir.OpRefNull, Type: anyT}

	var voidIDs, uf0IDs []int32
	for _, tr := range g.types {
		if tr == nil || tr.id < 0 || tr.kind != KindFunc || tr.goType == nil {
			continue
		}
		sig, ok := tr.goType.Underlying().(*types.Signature)
		if !ok {
			continue
		}
		switch {
		case sig.Params().Len() == 0 && sig.Results().Len() == 0:
			voidIDs = append(voidIDs, tr.id)
		case sig.Params().Len() == 1 && sig.Results().Len() == 0:
			p0, ok := sig.Params().At(0).Type().Underlying().(*types.Signature)
			if ok && p0.Params().Len() == 0 && p0.Results().Len() == 0 {
				uf0IDs = append(uf0IDs, tr.id)
			}
		}
	}

	retClos := func(closName, wrap string) ir.Stmt {
		clos := &ir.Instr{
			Op:   ir.OpStructNew,
			Type: &ir.Ref{Named: closName},
			Args: []ir.Expr{
				&ir.Instr{Op: ir.OpRefFunc, Func: wrap},
				&ir.Instr{Op: ir.OpRefCast, Type: &ir.Ref{Null: true, Named: makeFuncImplTy}, Args: []ir.Expr{
					&ir.Name{Raw: "impl", Var: impl},
				}},
			},
		}
		cast := &ir.Instr{Op: ir.OpRefCast, Type: &ir.Ref{Named: "$go.clos"}, Args: []ir.Expr{clos}}
		return &ir.Return{Results: []ir.Expr{
			&ir.Instr{Op: ir.OpRefCast, Type: anyT, Args: []ir.Expr{cast}},
		}}
	}

	chain := ir.Stmt(&ir.Return{Results: []ir.Expr{nullA}})
	for i := len(uf0IDs) - 1; i >= 0; i-- {
		id := uf0IDs[i]
		chain = &ir.If{
			Cond: &ir.Instr{Op: ir.OpI32Eq, Args: []ir.Expr{
				&ir.Name{Raw: "id", Var: idV},
				&ir.Const{Op: ir.OpI32Const, I64: int64(id)},
			}},
			Then: &ir.Seq{Stmts: []ir.Stmt{retClos(makeFuncClosUF0, makeFuncWrapUF0)}},
			Else: &ir.Seq{Stmts: []ir.Stmt{chain}},
		}
	}
	for i := len(voidIDs) - 1; i >= 0; i-- {
		id := voidIDs[i]
		chain = &ir.If{
			Cond: &ir.Instr{Op: ir.OpI32Eq, Args: []ir.Expr{
				&ir.Name{Raw: "id", Var: idV},
				&ir.Const{Op: ir.OpI32Const, I64: int64(id)},
			}},
			Then: &ir.Seq{Stmts: []ir.Stmt{retClos(makeFuncClosVoid, makeFuncWrapVoid)}},
			Else: &ir.Seq{Stmts: []ir.Stmt{chain}},
		}
	}

	body := &ir.Seq{Stmts: []ir.Stmt{
		&ir.VarDef{Var: idV},
		&ir.If{
			Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{&ir.Name{Raw: "t", Var: t}}},
			Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{nullA}}}},
			Else: &ir.Seq{Stmts: []ir.Stmt{
				&ir.If{
					Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{&ir.Name{Raw: "impl", Var: impl}}},
					Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{nullA}}}},
					Else: &ir.Seq{Stmts: []ir.Stmt{
						&ir.Assign{Lhs: []ir.Dest{{Name: "id", Var: idV}}, Rhs: &ir.Instr{
							Op: ir.OpStructGet, Type: &ir.Ref{Named: "$go.type"}, Field: "id",
							Args: []ir.Expr{&ir.Name{Raw: "t", Var: t}},
						}},
						chain,
					}},
				},
			}},
		},
		&ir.Unreachable{},
	}}
	g.replaceBody(name, []*ir.Var{t, impl}, []ir.Type{anyT}, body)
	return nil
}

func (g *gen) ensureMakeFuncCbFuncType() {
	if g.mod.LookupType(makeFuncCbFuncTy) != nil {
		return
	}
	// Ensure []Value exists (reflect is linked if MakeFunc is).
	sliceTy := &ir.Ref{Null: true, Named: makeFuncValueSlice}
	g.mod.InternType(&ir.TypeDef{
		Name: makeFuncCbFuncTy,
		Kind: ir.TypeFunc,
		Params: []ir.Type{
			&ir.Ref{Named: "$go.clos"},
			sliceTy,
		},
		Results: []ir.Type{sliceTy},
	})
}

func (g *gen) ensureMakeFuncVoidTrampoline() {
	g.ensureClosBase()
	if g.mod.LookupType(makeFuncClosVoid) == nil {
		g.mod.InternType(&ir.TypeDef{
			Name:  makeFuncClosVoid,
			Kind:  ir.TypeStruct,
			Super: "$go.clos",
			Fields: []ir.Field{
				{Name: "fn", Type: &ir.Ref{Null: true, Heap: ir.HeapFunc}},
				{Name: "impl", Type: &ir.Ref{Null: true, Named: makeFuncImplTy}},
			},
		})
	}
	if g.mod.LookupFunc(makeFuncWrapVoid) != nil {
		return
	}
	closParam := &ir.Var{Name: "clos", Typ: &ir.Ref{Named: "$go.clos"}}
	body := g.makeFuncCallBody(closParam, nil)
	g.mod.Funcs = append(g.mod.Funcs, &ir.Func{
		Name:    makeFuncWrapVoid,
		Params:  []*ir.Var{closParam},
		Results: nil,
		Body:    body,
	})
}

func (g *gen) ensureMakeFuncUFunc0Trampoline() {
	g.ensureClosBase()
	if g.mod.LookupType(makeFuncClosUF0) == nil {
		g.mod.InternType(&ir.TypeDef{
			Name:  makeFuncClosUF0,
			Kind:  ir.TypeStruct,
			Super: "$go.clos",
			Fields: []ir.Field{
				{Name: "fn", Type: &ir.Ref{Null: true, Heap: ir.HeapFunc}},
				{Name: "impl", Type: &ir.Ref{Null: true, Named: makeFuncImplTy}},
			},
		})
	}
	if g.mod.LookupFunc(makeFuncWrapUF0) != nil {
		return
	}
	closParam := &ir.Var{Name: "clos", Typ: &ir.Ref{Named: "$go.clos"}}
	argParam := &ir.Var{Name: "arg", Typ: &ir.Ref{Null: true, Named: "$go.clos"}}
	body := g.makeFuncCallBody(closParam, argParam)
	g.mod.Funcs = append(g.mod.Funcs, &ir.Func{
		Name:    makeFuncWrapUF0,
		Params:  []*ir.Var{closParam, argParam},
		Results: nil,
		Body:    body,
	})
}

// makeFuncCallBody loads impl.fn and call_refs it with []Value{arg?} (nil if arg is nil).
func (g *gen) makeFuncCallBody(closParam, argParam *ir.Var) ir.Region {
	implTy := &ir.Ref{Null: true, Named: makeFuncImplTy}
	sliceTy := &ir.Ref{Null: true, Named: makeFuncValueSlice}
	closBase := &ir.Ref{Named: "$go.clos"}
	typed := &ir.Var{Name: "typed", Typ: &ir.Ref{Named: makeFuncClosVoid}}
	// Use void clos layout for both (same fields); cast from $go.clos.
	implV := &ir.Var{Name: "impl", Typ: implTy}
	fnV := &ir.Var{Name: "fn", Typ: &ir.Ref{Null: true, Named: "$go.clos"}}
	argsV := &ir.Var{Name: "args", Typ: sliceTy}

	stmts := []ir.Stmt{
		&ir.VarDef{Var: typed},
		&ir.VarDef{Var: implV},
		&ir.VarDef{Var: fnV},
		&ir.VarDef{Var: argsV},
		&ir.Assign{Lhs: []ir.Dest{{Name: "typed", Var: typed}}, Rhs: &ir.Instr{
			Op: ir.OpRefCast, Type: &ir.Ref{Named: makeFuncClosVoid},
			Args: []ir.Expr{&ir.Name{Raw: closParam.Name, Var: closParam}},
		}},
		&ir.Assign{Lhs: []ir.Dest{{Name: "impl", Var: implV}}, Rhs: &ir.Instr{
			Op: ir.OpStructGet, Type: &ir.Ref{Named: makeFuncClosVoid}, Field: "impl",
			Args: []ir.Expr{&ir.Name{Raw: "typed", Var: typed}},
		}},
		&ir.Assign{Lhs: []ir.Dest{{Name: "fn", Var: fnV}}, Rhs: &ir.Instr{
			Op: ir.OpStructGet, Type: implTy, Field: "fn",
			Args: []ir.Expr{&ir.Instr{Op: ir.OpRefAsNonNull, Args: []ir.Expr{&ir.Name{Raw: "impl", Var: implV}}}},
		}},
	}

	// args = nil slice or one-element slice of Value{data: arg}.
	if argParam == nil {
		stmts = append(stmts, &ir.Assign{Lhs: []ir.Dest{{Name: "args", Var: argsV}}, Rhs: g.nilValueSlice()})
	} else {
		stmts = append(stmts, g.oneValueSlice(argParam, argsV)...)
	}

	fnNN := &ir.Instr{Op: ir.OpRefAsNonNull, Args: []ir.Expr{&ir.Name{Raw: "fn", Var: fnV}}}
	fnField := &ir.Instr{
		Op: ir.OpStructGet, Type: closBase, Field: "fn",
		Args: []ir.Expr{fnNN},
	}
	castFn := &ir.Instr{Op: ir.OpRefCast, Type: &ir.Ref{Named: makeFuncCbFuncTy}, Args: []ir.Expr{fnField}}
	call := &ir.Instr{
		Op:   ir.OpCallRef,
		Type: &ir.Ref{Named: makeFuncCbFuncTy},
		Args: []ir.Expr{
			fnNN,
			&ir.Name{Raw: "args", Var: argsV},
			castFn,
		},
	}
	stmts = append(stmts,
		&ir.ExprStmt{X: &ir.Instr{Op: ir.OpDrop, Args: []ir.Expr{call}}},
		&ir.Return{},
	)
	return &ir.Seq{Stmts: stmts}
}

func (g *gen) nilValueSlice() ir.Expr {
	sliceTy := &ir.Ref{Null: true, Named: makeFuncValueSlice}
	arrTy := &ir.Ref{Named: makeFuncValueArr}
	// Ensure array type exists.
	if g.mod.LookupType(makeFuncValueArr) == nil {
		g.mod.InternType(&ir.TypeDef{
			Name: makeFuncValueArr,
			Kind: ir.TypeArray,
			Elem: &ir.Ref{Null: true, Named: makeFuncValueTy},
		})
	}
	if g.mod.LookupType(makeFuncValueSlice) == nil {
		g.mod.InternType(&ir.TypeDef{
			Name: makeFuncValueSlice,
			Kind: ir.TypeStruct,
			Super: "$go.object",
			Fields: []ir.Field{
				{Name: "data", Type: &ir.Ref{Named: makeFuncValueArr}, Mut: true},
				{Name: "off", Type: ir.TypeI32, Mut: true},
				{Name: "len", Type: ir.TypeI32, Mut: true},
				{Name: "cap", Type: ir.TypeI32, Mut: true},
			},
		})
	}
	empty := &ir.Instr{
		Op: ir.OpRefAsNonNull,
		Args: []ir.Expr{
			&ir.Instr{Op: ir.OpArrayNewDefault, Type: arrTy, Args: []ir.Expr{
				&ir.Const{Op: ir.OpI32Const, I64: 0},
			}},
		},
	}
	return &ir.Instr{
		Op: ir.OpStructNew, Type: sliceTy,
		Args: []ir.Expr{
			empty,
			&ir.Const{Op: ir.OpI32Const, I64: 0},
			&ir.Const{Op: ir.OpI32Const, I64: 0},
			&ir.Const{Op: ir.OpI32Const, I64: 0},
		},
	}
}

func (g *gen) oneValueSlice(arg *ir.Var, dst *ir.Var) []ir.Stmt {
	_ = g.nilValueSlice() // ensure types
	sliceTy := &ir.Ref{Null: true, Named: makeFuncValueSlice}
	arrTy := &ir.Ref{Named: makeFuncValueArr}
	valTy := &ir.Ref{Null: true, Named: makeFuncValueTy}
	rtypeTy := &ir.Ref{Null: true, Named: "$go.struct.reflect.rtype"}
	arrV := &ir.Var{Name: "arr", Typ: arrTy}
	valV := &ir.Var{Name: "val", Typ: valTy}
	rtV := &ir.Var{Name: "rt", Typ: rtypeTy}
	goTyName := "$go.type.func()"
	var goTy ir.Expr = &ir.Instr{Op: ir.OpRefNull, Type: &ir.Ref{Null: true, Named: "$go.type"}}
	if g.mod.LookupGlobal(goTyName) != nil {
		goTy = &ir.Instr{Op: ir.OpGlobalGet, Global: goTyName}
	}
	rtypeNew := &ir.Instr{
		Op: ir.OpStructNew, Type: rtypeTy,
		Args: []ir.Expr{
			goTy,
			&ir.Const{Op: ir.OpI32Const, I64: 19}, // reflect.Func
			g.emptyString(),
		},
	}
	dataAny := &ir.Instr{Op: ir.OpRefCast, Type: &ir.Ref{Null: true, Heap: ir.HeapAny}, Args: []ir.Expr{
		&ir.Name{Raw: arg.Name, Var: arg},
	}}
	valFields := g.makeFuncValueFields(&ir.Name{Raw: "rt", Var: rtV}, dataAny)
	return []ir.Stmt{
		&ir.VarDef{Var: arrV},
		&ir.VarDef{Var: valV},
		&ir.VarDef{Var: rtV},
		&ir.Assign{Lhs: []ir.Dest{{Name: "rt", Var: rtV}}, Rhs: rtypeNew},
		&ir.Assign{Lhs: []ir.Dest{{Name: "arr", Var: arrV}}, Rhs: &ir.Instr{
			Op: ir.OpArrayNewDefault, Type: arrTy, Args: []ir.Expr{&ir.Const{Op: ir.OpI32Const, I64: 1}},
		}},
		&ir.Assign{Lhs: []ir.Dest{{Name: "val", Var: valV}}, Rhs: &ir.Instr{
			Op: ir.OpStructNew, Type: valTy, Args: valFields,
		}},
		&ir.ExprStmt{X: &ir.Instr{
			Op: ir.OpArraySet, Type: arrTy,
			Args: []ir.Expr{
				&ir.Name{Raw: "arr", Var: arrV},
				&ir.Const{Op: ir.OpI32Const, I64: 0},
				&ir.Name{Raw: "val", Var: valV},
			},
		}},
		&ir.Assign{Lhs: []ir.Dest{{Name: dst.Name, Var: dst}}, Rhs: &ir.Instr{
			Op: ir.OpStructNew, Type: sliceTy,
			Args: []ir.Expr{
				&ir.Instr{Op: ir.OpRefAsNonNull, Args: []ir.Expr{&ir.Name{Raw: "arr", Var: arrV}}},
				&ir.Const{Op: ir.OpI32Const, I64: 0},
				&ir.Const{Op: ir.OpI32Const, I64: 1},
				&ir.Const{Op: ir.OpI32Const, I64: 1},
			},
		}},
	}
}

func (g *gen) emptyString() ir.Expr {
	strTy := &ir.Ref{Named: "$go.string"}
	bytesTy := &ir.Ref{Named: "$go.bytes"}
	return &ir.Instr{
		Op: ir.OpStructNew, Type: strTy,
		Args: []ir.Expr{
			&ir.Instr{Op: ir.OpRefAsNonNull, Args: []ir.Expr{
				&ir.Instr{Op: ir.OpArrayNewDefault, Type: bytesTy, Args: []ir.Expr{
					&ir.Const{Op: ir.OpI32Const, I64: 0},
				}},
			}},
			&ir.Const{Op: ir.OpI32Const, I64: 0},
			&ir.Const{Op: ir.OpI32Const, I64: 0},
		},
	}
}

func (g *gen) makeFuncValueFields(typ, data ir.Expr) []ir.Expr {
	td := g.mod.LookupType(makeFuncValueTy)
	zeroI := &ir.Const{Op: ir.OpI32Const, I64: 0}
	if td == nil || len(td.Fields) == 0 {
		// typ, ptr-ish data, flag — common shape.
		return []ir.Expr{typ, data, zeroI}
	}
	args := make([]ir.Expr, len(td.Fields))
	for i, f := range td.Fields {
		switch f.Name {
		case "typ", "Type":
			args[i] = typ
		case "data", "ptr":
			args[i] = data
		default:
			args[i] = zeroOfIR(f.Type)
		}
	}
	return args
}

func zeroOfIR(t ir.Type) ir.Expr {
	if r, ok := t.(*ir.Ref); ok {
		return &ir.Instr{Op: ir.OpRefNull, Type: r}
	}
	if n, ok := t.(*ir.NumType); ok {
		switch n.Kind {
		case ir.I64:
			return &ir.Const{Op: ir.OpI64Const, I64: 0}
		case ir.F32:
			return &ir.Const{Op: ir.OpF32Const, F64: 0}
		case ir.F64:
			return &ir.Const{Op: ir.OpF64Const, F64: 0}
		}
	}
	return &ir.Const{Op: ir.OpI32Const, I64: 0}
}
