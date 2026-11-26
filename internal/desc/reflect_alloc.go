package desc

import (
	"go/types"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
)

func (g *gen) fillReflectSetBoxI32() {
	g.fillReflectSetBox("runtime.ReflectSetBoxI32", "$go.box.i32", ir.TypeI32)
}
func (g *gen) fillReflectSetBoxI64() {
	g.fillReflectSetBox("runtime.ReflectSetBoxI64", "$go.box.i64", ir.TypeI64)
}
func (g *gen) fillReflectSetBoxF32() {
	g.fillReflectSetBox("runtime.ReflectSetBoxF32", "$go.box.f32", ir.TypeF32)
}
func (g *gen) fillReflectSetBoxF64() {
	g.fillReflectSetBox("runtime.ReflectSetBoxF64", "$go.box.f64", ir.TypeF64)
}
func (g *gen) fillReflectSetBoxString() {
	f := g.mod.LookupFunc("runtime.ReflectSetBoxString")
	if f == nil || !emptyBody(f) {
		return
	}
	g.ensureBoxString()
	g.fillReflectSetBox("runtime.ReflectSetBoxString", "$go.box.string", &ir.Ref{Named: "$go.string"})
}

func (g *gen) fillReflectSetBoxRef() {
	f := g.mod.LookupFunc("runtime.ReflectSetBoxRef")
	if f == nil || !emptyBody(f) {
		return
	}
	g.ensureBoxRef()
	anyT := &ir.Ref{Null: true, Heap: ir.HeapAny}
	g.fillReflectSetBox("runtime.ReflectSetBoxRef", "$go.box.ref", anyT)
}

func (g *gen) fillReflectNewRefBox() {
	name := "runtime.ReflectNewRefBox"
	f := g.mod.LookupFunc(name)
	if f == nil || !emptyBody(f) {
		return
	}
	g.ensureBoxRef()
	anyT := &ir.Ref{Null: true, Heap: ir.HeapAny}
	v := &ir.Var{Name: "v", Typ: anyT}
	boxTy := &ir.Ref{Named: "$go.box.ref"}
	body := &ir.Seq{Stmts: []ir.Stmt{
		&ir.Return{Results: []ir.Expr{&ir.Instr{
			Op:   ir.OpRefCast,
			Type: anyT,
			Args: []ir.Expr{&ir.Instr{
				Op:   ir.OpStructNew,
				Type: boxTy,
				Args: []ir.Expr{&ir.Name{Raw: "v", Var: v}},
			}},
		}}},
	}}
	g.replaceBody(name, []*ir.Var{v}, []ir.Type{anyT}, body)
}

func (g *gen) fillReflectUnboxRef() {
	name := "runtime.ReflectUnboxRef"
	f := g.mod.LookupFunc(name)
	if f == nil || !emptyBody(f) {
		return
	}
	g.ensureBoxRef()
	anyT := &ir.Ref{Null: true, Heap: ir.HeapAny}
	data := &ir.Var{Name: "data", Typ: anyT}
	boxTy := &ir.Ref{Named: "$go.box.ref"}
	b := &ir.Var{Name: "b", Typ: boxTy}
	body := &ir.Seq{Stmts: []ir.Stmt{
		&ir.VarDef{Var: b},
		&ir.If{
			Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{&ir.Name{Raw: "data", Var: data}}},
			Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{
				&ir.Instr{Op: ir.OpRefNull, Type: anyT},
			}}}},
			Else: &ir.Seq{Stmts: []ir.Stmt{
				&ir.Assign{Lhs: []ir.Dest{{Name: "b", Var: b}}, Rhs: &ir.Instr{
					Op: ir.OpRefCast, Type: boxTy, Args: []ir.Expr{&ir.Name{Raw: "data", Var: data}},
				}},
				&ir.Return{Results: []ir.Expr{&ir.Instr{
					Op: ir.OpStructGet, Type: boxTy, Field: "v",
					Args: []ir.Expr{&ir.Name{Raw: "b", Var: b}},
				}}},
			}},
		},
		&ir.Unreachable{},
	}}
	g.replaceBody(name, []*ir.Var{data}, []ir.Type{anyT}, body)
}

// ensureFatPtrKindTypes interns $go.ptr.<kind> / class / get / set so reflect
// can load/store through ValueOf(&local) fat pointers.
func (g *gen) ensureFatPtrKindTypes(kind string, val ir.Type) {
	anyT := &ir.Ref{Null: true, Heap: ir.HeapAny}
	getName := "$go.get." + kind
	setName := "$go.set." + kind
	className := "$go.class." + kind
	ptrName := "$go.ptr." + kind
	g.mod.InternType(&ir.TypeDef{
		Name: getName, Kind: ir.TypeFunc,
		Params: []ir.Type{anyT, ir.TypeI32}, Results: []ir.Type{val},
	})
	g.mod.InternType(&ir.TypeDef{
		Name: setName, Kind: ir.TypeFunc,
		Params: []ir.Type{anyT, ir.TypeI32, val},
	})
	g.mod.InternType(&ir.TypeDef{
		Name: className, Kind: ir.TypeStruct, Super: "$go.object",
		Fields: []ir.Field{
			{Name: "get", Type: &ir.Ref{Named: getName}},
			{Name: "set", Type: &ir.Ref{Named: setName}},
		},
	})
	g.mod.InternType(&ir.TypeDef{
		Name: ptrName, Kind: ir.TypeStruct, Super: "$go.object",
		Fields: []ir.Field{
			{Name: "class", Type: &ir.Ref{Named: className}},
			{Name: "base", Type: anyT},
			{Name: "index", Type: ir.TypeI32},
		},
	})
}

// ensureFatPtrRefTypes interns $go.ptr.ref / class / get / set so reflect can
// load/store through ValueOf(&mapVar) fat pointers (json Unmarshal).
func (g *gen) ensureFatPtrRefTypes() {
	g.ensureFatPtrKindTypes("ref", &ir.Ref{Null: true, Heap: ir.HeapAny})
}

func (g *gen) ensureFatPtrScalarTypes() {
	g.ensureFatPtrKindTypes("i32", ir.TypeI32)
	g.ensureFatPtrKindTypes("i64", ir.TypeI64)
	g.ensureFatPtrKindTypes("f32", ir.TypeF32)
	g.ensureFatPtrKindTypes("f64", ir.TypeF64)
	// Boxes used when wrapping scalar get results for ReflectUnbox*.
	for _, name := range []string{"$go.box.i32", "$go.box.i64", "$go.box.f32", "$go.box.f64"} {
		if g.mod.LookupType(name) != nil {
			continue
		}
		var vt ir.Type = ir.TypeI32
		switch name {
		case "$go.box.i64":
			vt = ir.TypeI64
		case "$go.box.f32":
			vt = ir.TypeF32
		case "$go.box.f64":
			vt = ir.TypeF64
		}
		g.mod.InternType(&ir.TypeDef{
			Name: name, Kind: ir.TypeStruct, Super: "$go.object",
			Fields: []ir.Field{{Name: "v", Type: vt, Mut: true}},
		})
	}
}

// fatPtrGetScalarBranch: ref.test $go.ptr.<kind> → class.get → box → return any.
func (g *gen) fatPtrGetScalarBranch(data *ir.Var, kind, box string, valTy ir.Type, elseBody ir.Region) ir.Region {
	anyT := &ir.Ref{Null: true, Heap: ir.HeapAny}
	ptrTy := &ir.Ref{Named: "$go.ptr." + kind}
	classTy := &ir.Ref{Named: "$go.class." + kind}
	boxTy := &ir.Ref{Named: box}
	p := &ir.Var{Name: "p_" + kind, Typ: ptrTy}
	cl := &ir.Var{Name: "cl_" + kind, Typ: classTy}
	loaded := &ir.Var{Name: "v_" + kind, Typ: valTy}
	return &ir.Seq{Stmts: []ir.Stmt{
		&ir.VarDef{Var: p},
		&ir.VarDef{Var: cl},
		&ir.VarDef{Var: loaded},
		&ir.If{
			Cond: &ir.Instr{Op: ir.OpRefTest, Type: ptrTy, Args: []ir.Expr{&ir.Name{Raw: "data", Var: data}}},
			Then: &ir.Seq{Stmts: []ir.Stmt{
				&ir.Assign{Lhs: []ir.Dest{{Name: p.Name, Var: p}}, Rhs: &ir.Instr{
					Op: ir.OpRefCast, Type: ptrTy, Args: []ir.Expr{&ir.Name{Raw: "data", Var: data}},
				}},
				&ir.Assign{Lhs: []ir.Dest{{Name: cl.Name, Var: cl}}, Rhs: &ir.Instr{
					Op: ir.OpStructGet, Type: ptrTy, Field: "class",
					Args: []ir.Expr{&ir.Name{Raw: p.Name, Var: p}},
				}},
				&ir.Assign{Lhs: []ir.Dest{{Name: loaded.Name, Var: loaded}}, Rhs: &ir.Instr{
					Op: ir.OpCallRef, Type: &ir.Ref{Named: "$go.get." + kind},
					Args: []ir.Expr{
						&ir.Instr{Op: ir.OpStructGet, Type: ptrTy, Field: "base", Args: []ir.Expr{&ir.Name{Raw: p.Name, Var: p}}},
						&ir.Instr{Op: ir.OpStructGet, Type: ptrTy, Field: "index", Args: []ir.Expr{&ir.Name{Raw: p.Name, Var: p}}},
						&ir.Instr{Op: ir.OpStructGet, Type: classTy, Field: "get", Args: []ir.Expr{&ir.Name{Raw: cl.Name, Var: cl}}},
					},
				}},
				&ir.Return{Results: []ir.Expr{&ir.Instr{
					Op: ir.OpRefCast, Type: anyT,
					Args: []ir.Expr{&ir.Instr{
						Op: ir.OpStructNew, Type: boxTy,
						Args: []ir.Expr{&ir.Name{Raw: loaded.Name, Var: loaded}},
					}},
				}}},
			}},
			Else: elseBody,
		},
	}}
}

// fatPtrSetScalarBranch: ref.test $go.ptr.<kind> → unbox v → class.set.
func (g *gen) fatPtrSetScalarBranch(data, v *ir.Var, kind, box string, valTy ir.Type, elseBody ir.Region) ir.Region {
	ptrTy := &ir.Ref{Named: "$go.ptr." + kind}
	classTy := &ir.Ref{Named: "$go.class." + kind}
	boxTy := &ir.Ref{Named: box}
	p := &ir.Var{Name: "sp_" + kind, Typ: ptrTy}
	cl := &ir.Var{Name: "scl_" + kind, Typ: classTy}
	bv := &ir.Var{Name: "sb_" + kind, Typ: boxTy}
	return &ir.Seq{Stmts: []ir.Stmt{
		&ir.VarDef{Var: p},
		&ir.VarDef{Var: cl},
		&ir.VarDef{Var: bv},
		&ir.If{
			Cond: &ir.Instr{Op: ir.OpRefTest, Type: ptrTy, Args: []ir.Expr{&ir.Name{Raw: "data", Var: data}}},
			Then: &ir.Seq{Stmts: []ir.Stmt{
				&ir.Assign{Lhs: []ir.Dest{{Name: p.Name, Var: p}}, Rhs: &ir.Instr{
					Op: ir.OpRefCast, Type: ptrTy, Args: []ir.Expr{&ir.Name{Raw: "data", Var: data}},
				}},
				&ir.Assign{Lhs: []ir.Dest{{Name: cl.Name, Var: cl}}, Rhs: &ir.Instr{
					Op: ir.OpStructGet, Type: ptrTy, Field: "class",
					Args: []ir.Expr{&ir.Name{Raw: p.Name, Var: p}},
				}},
				&ir.Assign{Lhs: []ir.Dest{{Name: bv.Name, Var: bv}}, Rhs: &ir.Instr{
					Op: ir.OpRefCast, Type: boxTy, Args: []ir.Expr{&ir.Name{Raw: "v", Var: v}},
				}},
				&ir.ExprStmt{X: &ir.Instr{
					Op: ir.OpCallRef, Type: &ir.Ref{Named: "$go.set." + kind},
					Args: []ir.Expr{
						&ir.Instr{Op: ir.OpStructGet, Type: ptrTy, Field: "base", Args: []ir.Expr{&ir.Name{Raw: p.Name, Var: p}}},
						&ir.Instr{Op: ir.OpStructGet, Type: ptrTy, Field: "index", Args: []ir.Expr{&ir.Name{Raw: p.Name, Var: p}}},
						&ir.Instr{Op: ir.OpStructGet, Type: boxTy, Field: "v", Args: []ir.Expr{&ir.Name{Raw: bv.Name, Var: bv}}},
						&ir.Instr{Op: ir.OpStructGet, Type: classTy, Field: "set", Args: []ir.Expr{&ir.Name{Raw: cl.Name, Var: cl}}},
					},
				}},
				&ir.Return{},
			}},
			Else: elseBody,
		},
	}}
}

func (g *gen) fillReflectFatPtrGet() {
	name := "runtime.ReflectFatPtrGet"
	f := g.mod.LookupFunc(name)
	if f == nil || !emptyBody(f) {
		return
	}
	g.ensureFatPtrRefTypes()
	g.ensureFatPtrScalarTypes()
	anyT := &ir.Ref{Null: true, Heap: ir.HeapAny}
	data := &ir.Var{Name: "data", Typ: anyT}
	ptrTy := &ir.Ref{Named: "$go.ptr.ref"}
	classTy := &ir.Ref{Named: "$go.class.ref"}
	p := &ir.Var{Name: "p", Typ: ptrTy}
	cl := &ir.Var{Name: "cl", Typ: classTy}
	// Fallback: not a known fat pointer — return data unchanged (fmt walk).
	fallthrough_ := &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{
		&ir.Name{Raw: "data", Var: data},
	}}}}
	// Chain: i32 → i64 → f32 → f64 → ref → fallthrough.
	refBranch := &ir.Seq{Stmts: []ir.Stmt{
		&ir.VarDef{Var: p},
		&ir.VarDef{Var: cl},
		&ir.If{
			Cond: &ir.Instr{Op: ir.OpRefTest, Type: ptrTy, Args: []ir.Expr{&ir.Name{Raw: "data", Var: data}}},
			Then: &ir.Seq{Stmts: []ir.Stmt{
				&ir.Assign{Lhs: []ir.Dest{{Name: "p", Var: p}}, Rhs: &ir.Instr{
					Op: ir.OpRefCast, Type: ptrTy, Args: []ir.Expr{&ir.Name{Raw: "data", Var: data}},
				}},
				&ir.Assign{Lhs: []ir.Dest{{Name: "cl", Var: cl}}, Rhs: &ir.Instr{
					Op: ir.OpStructGet, Type: ptrTy, Field: "class",
					Args: []ir.Expr{&ir.Name{Raw: "p", Var: p}},
				}},
				&ir.Return{Results: []ir.Expr{&ir.Instr{
					Op:   ir.OpCallRef,
					Type: &ir.Ref{Named: "$go.get.ref"},
					Args: []ir.Expr{
						&ir.Instr{Op: ir.OpStructGet, Type: ptrTy, Field: "base", Args: []ir.Expr{&ir.Name{Raw: "p", Var: p}}},
						&ir.Instr{Op: ir.OpStructGet, Type: ptrTy, Field: "index", Args: []ir.Expr{&ir.Name{Raw: "p", Var: p}}},
						&ir.Instr{Op: ir.OpStructGet, Type: classTy, Field: "get", Args: []ir.Expr{&ir.Name{Raw: "cl", Var: cl}}},
					},
				}}},
			}},
			Else: fallthrough_,
		},
	}}
	f64 := g.fatPtrGetScalarBranch(data, "f64", "$go.box.f64", ir.TypeF64, refBranch)
	f32 := g.fatPtrGetScalarBranch(data, "f32", "$go.box.f32", ir.TypeF32, f64)
	i64 := g.fatPtrGetScalarBranch(data, "i64", "$go.box.i64", ir.TypeI64, f32)
	i32 := g.fatPtrGetScalarBranch(data, "i32", "$go.box.i32", ir.TypeI32, i64)
	body := &ir.Seq{Stmts: []ir.Stmt{
		&ir.If{
			Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{&ir.Name{Raw: "data", Var: data}}},
			Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{
				&ir.Instr{Op: ir.OpRefNull, Type: anyT},
			}}}},
			Else: i32,
		},
		&ir.Unreachable{},
	}}
	g.replaceBody(name, []*ir.Var{data}, []ir.Type{anyT}, body)
}

func (g *gen) fillReflectFatPtrSet() {
	name := "runtime.ReflectFatPtrSet"
	f := g.mod.LookupFunc(name)
	if f == nil || !emptyBody(f) {
		return
	}
	g.ensureFatPtrRefTypes()
	g.ensureFatPtrScalarTypes()
	anyT := &ir.Ref{Null: true, Heap: ir.HeapAny}
	data := &ir.Var{Name: "data", Typ: anyT}
	v := &ir.Var{Name: "v", Typ: anyT}
	ptrTy := &ir.Ref{Named: "$go.ptr.ref"}
	classTy := &ir.Ref{Named: "$go.class.ref"}
	p := &ir.Var{Name: "p", Typ: ptrTy}
	cl := &ir.Var{Name: "cl", Typ: classTy}
	// Unknown shape: no-op (matches prior ref-cast-or-trap → soft fail).
	fallthrough_ := &ir.Seq{Stmts: []ir.Stmt{&ir.Return{}}}
	refBranch := &ir.Seq{Stmts: []ir.Stmt{
		&ir.VarDef{Var: p},
		&ir.VarDef{Var: cl},
		&ir.If{
			Cond: &ir.Instr{Op: ir.OpRefTest, Type: ptrTy, Args: []ir.Expr{&ir.Name{Raw: "data", Var: data}}},
			Then: &ir.Seq{Stmts: []ir.Stmt{
				&ir.Assign{Lhs: []ir.Dest{{Name: "p", Var: p}}, Rhs: &ir.Instr{
					Op: ir.OpRefCast, Type: ptrTy, Args: []ir.Expr{&ir.Name{Raw: "data", Var: data}},
				}},
				&ir.Assign{Lhs: []ir.Dest{{Name: "cl", Var: cl}}, Rhs: &ir.Instr{
					Op: ir.OpStructGet, Type: ptrTy, Field: "class",
					Args: []ir.Expr{&ir.Name{Raw: "p", Var: p}},
				}},
				&ir.ExprStmt{X: &ir.Instr{
					Op:   ir.OpCallRef,
					Type: &ir.Ref{Named: "$go.set.ref"},
					Args: []ir.Expr{
						&ir.Instr{Op: ir.OpStructGet, Type: ptrTy, Field: "base", Args: []ir.Expr{&ir.Name{Raw: "p", Var: p}}},
						&ir.Instr{Op: ir.OpStructGet, Type: ptrTy, Field: "index", Args: []ir.Expr{&ir.Name{Raw: "p", Var: p}}},
						&ir.Name{Raw: "v", Var: v},
						&ir.Instr{Op: ir.OpStructGet, Type: classTy, Field: "set", Args: []ir.Expr{&ir.Name{Raw: "cl", Var: cl}}},
					},
				}},
				&ir.Return{},
			}},
			Else: fallthrough_,
		},
	}}
	f64 := g.fatPtrSetScalarBranch(data, v, "f64", "$go.box.f64", ir.TypeF64, refBranch)
	f32 := g.fatPtrSetScalarBranch(data, v, "f32", "$go.box.f32", ir.TypeF32, f64)
	i64 := g.fatPtrSetScalarBranch(data, v, "i64", "$go.box.i64", ir.TypeI64, f32)
	i32 := g.fatPtrSetScalarBranch(data, v, "i32", "$go.box.i32", ir.TypeI32, i64)
	body := &ir.Seq{Stmts: []ir.Stmt{
		&ir.If{
			Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{&ir.Name{Raw: "data", Var: data}}},
			Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{}}},
			Else: i32,
		},
		&ir.Unreachable{},
	}}
	g.replaceBody(name, []*ir.Var{data, v}, nil, body)
}

// ensureBoxString interns the mutable string holder for reflect.New(string)
// / SetString. Kept out of the unconditional prelude so empty_main stays 208.
func (g *gen) ensureBoxString() {
	g.mod.InternType(&ir.TypeDef{
		Name:  "$go.box.string",
		Kind:  ir.TypeStruct,
		Super: "$go.object",
		Fields: []ir.Field{
			{Name: "v", Type: &ir.Ref{Named: "$go.string"}, Mut: true},
		},
	})
}

// ensureBoxRef interns a mutable anyref holder for reflect.New(map/slice/…).
func (g *gen) ensureBoxRef() {
	anyT := &ir.Ref{Null: true, Heap: ir.HeapAny}
	g.mod.InternType(&ir.TypeDef{
		Name:  "$go.box.ref",
		Kind:  ir.TypeStruct,
		Super: "$go.object",
		Fields: []ir.Field{
			{Name: "v", Type: anyT, Mut: true},
		},
	})
}

func (g *gen) fillReflectSetBox(name, box string, valT ir.Type) {
	f := g.mod.LookupFunc(name)
	if f == nil || !emptyBody(f) {
		return
	}
	anyT := &ir.Ref{Null: true, Heap: ir.HeapAny}
	data := &ir.Var{Name: "data", Typ: anyT}
	v := &ir.Var{Name: "v", Typ: valT}
	boxTy := &ir.Ref{Named: box}
	b := &ir.Var{Name: "b", Typ: boxTy}
	kind := "i32"
	switch box {
	case "$go.box.i64":
		kind = "i64"
	case "$go.box.f32":
		kind = "f32"
	case "$go.box.f64":
		kind = "f64"
	case "$go.box.string", "$go.box.ref":
		kind = "ref"
	}
	g.ensureFatPtrScalarTypes()
	g.ensureFatPtrRefTypes()
	ptrTy := &ir.Ref{Named: "$go.ptr." + kind}
	classTy := &ir.Ref{Named: "$go.class." + kind}
	p := &ir.Var{Name: "p", Typ: ptrTy}
	cl := &ir.Var{Name: "cl", Typ: classTy}
	storeVal := ir.Expr(&ir.Name{Raw: "v", Var: v})
	if kind == "ref" {
		storeVal = &ir.Instr{Op: ir.OpRefCast, Type: anyT, Args: []ir.Expr{&ir.Name{Raw: "v", Var: v}}}
	}
	// New().Elem() mutates $go.box.*; ValueOf(&local).Elem() stores through $go.ptr.*.
	body := &ir.Seq{Stmts: []ir.Stmt{
		&ir.VarDef{Var: b},
		&ir.VarDef{Var: p},
		&ir.VarDef{Var: cl},
		&ir.If{
			Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{&ir.Name{Raw: "data", Var: data}}},
			Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{}}},
			Else: &ir.Seq{Stmts: []ir.Stmt{
				&ir.If{
					Cond: &ir.Instr{Op: ir.OpRefTest, Type: boxTy, Args: []ir.Expr{&ir.Name{Raw: "data", Var: data}}},
					Then: &ir.Seq{Stmts: []ir.Stmt{
						&ir.Assign{Lhs: []ir.Dest{{Name: "b", Var: b}}, Rhs: &ir.Instr{
							Op: ir.OpRefCast, Type: boxTy, Args: []ir.Expr{&ir.Name{Raw: "data", Var: data}},
						}},
						&ir.ExprStmt{X: &ir.Instr{
							Op: ir.OpStructSet, Type: boxTy, Field: "v",
							Args: []ir.Expr{&ir.Name{Raw: "b", Var: b}, &ir.Name{Raw: "v", Var: v}},
						}},
						&ir.Return{},
					}},
					Else: &ir.Seq{Stmts: []ir.Stmt{
						&ir.If{
							Cond: &ir.Instr{Op: ir.OpRefTest, Type: ptrTy, Args: []ir.Expr{&ir.Name{Raw: "data", Var: data}}},
							Then: &ir.Seq{Stmts: []ir.Stmt{
								&ir.Assign{Lhs: []ir.Dest{{Name: "p", Var: p}}, Rhs: &ir.Instr{
									Op: ir.OpRefCast, Type: ptrTy, Args: []ir.Expr{&ir.Name{Raw: "data", Var: data}},
								}},
								&ir.Assign{Lhs: []ir.Dest{{Name: "cl", Var: cl}}, Rhs: &ir.Instr{
									Op: ir.OpStructGet, Type: ptrTy, Field: "class",
									Args: []ir.Expr{&ir.Name{Raw: "p", Var: p}},
								}},
								&ir.ExprStmt{X: &ir.Instr{
									Op: ir.OpCallRef, Type: &ir.Ref{Named: "$go.set." + kind},
									Args: []ir.Expr{
										&ir.Instr{Op: ir.OpStructGet, Type: ptrTy, Field: "base", Args: []ir.Expr{&ir.Name{Raw: "p", Var: p}}},
										&ir.Instr{Op: ir.OpStructGet, Type: ptrTy, Field: "index", Args: []ir.Expr{&ir.Name{Raw: "p", Var: p}}},
										storeVal,
										&ir.Instr{Op: ir.OpStructGet, Type: classTy, Field: "set", Args: []ir.Expr{&ir.Name{Raw: "cl", Var: cl}}},
									},
								}},
								&ir.Return{},
							}},
							Else: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{}}},
						},
					}},
				},
			}},
		},
	}}
	g.replaceBody(name, []*ir.Var{data, v}, nil, body)
}

// fillReflectNew builds runtime.ReflectNew(t) → zero value of t (as anyref)
// via dense id dispatch. Pointers to structs are the struct itself; scalars
// are mutable $go.box.*; slices/maps may be null.
func (g *gen) fillReflectNew() {
	name := "runtime.ReflectNew"
	f := g.mod.LookupFunc(name)
	if f == nil || !emptyBody(f) {
		return
	}
	typeTy := &ir.Ref{Null: true, Named: "$go.type"}
	anyT := &ir.Ref{Null: true, Heap: ir.HeapAny}
	t := &ir.Var{Name: "t", Typ: typeTy}
	idV := &ir.Var{Name: "id", Typ: ir.TypeI32}
	nullA := &ir.Instr{Op: ir.OpRefNull, Type: anyT}

	type arm struct {
		id   int32
		zero ir.Expr
	}
	var arms []arm
	keys := make([]string, 0, len(g.types))
	for k := range g.types {
		keys = append(keys, k)
	}
	sortStrings(keys)
	for _, k := range keys {
		tr := g.types[k]
		if tr == nil || tr.id < 0 {
			continue
		}
		z := g.zeroValueExpr(tr)
		if z == nil {
			continue
		}
		arms = append(arms, arm{id: tr.id, zero: z})
	}

	var chain ir.Stmt = &ir.Return{Results: []ir.Expr{nullA}}
	for i := len(arms) - 1; i >= 0; i-- {
		a := arms[i]
		chain = &ir.If{
			Cond: &ir.Instr{Op: ir.OpI32Eq, Args: []ir.Expr{
				&ir.Name{Raw: "id", Var: idV},
				&ir.Const{Op: ir.OpI32Const, I64: int64(a.id)},
			}},
			Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{
				&ir.Instr{Op: ir.OpRefCast, Type: anyT, Args: []ir.Expr{a.zero}},
			}}}},
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
	g.replaceBody(name, []*ir.Var{t}, []ir.Type{anyT}, body)
}

// fillReflectMakeSlice builds runtime.ReflectMakeSlice(t, len, cap) for slice
// types present in the closed universe.
func (g *gen) fillReflectMakeSlice() {
	name := "runtime.ReflectMakeSlice"
	f := g.mod.LookupFunc(name)
	if f == nil || !emptyBody(f) {
		return
	}
	typeTy := &ir.Ref{Null: true, Named: "$go.type"}
	anyT := &ir.Ref{Null: true, Heap: ir.HeapAny}
	t := &ir.Var{Name: "t", Typ: typeTy}
	ln := &ir.Var{Name: "len", Typ: ir.TypeI32}
	cp := &ir.Var{Name: "cap", Typ: ir.TypeI32}
	idV := &ir.Var{Name: "id", Typ: ir.TypeI32}
	nullA := &ir.Instr{Op: ir.OpRefNull, Type: anyT}

	type arm struct {
		id        int32
		sliceName string
	}
	var arms []arm
	keys := make([]string, 0, len(g.types))
	for k := range g.types {
		keys = append(keys, k)
	}
	sortStrings(keys)
	for _, k := range keys {
		tr := g.types[k]
		if tr == nil || tr.id < 0 || tr.kind != KindSlice {
			continue
		}
		_, sliceName := g.sliceElemIR(tr)
		if sliceName == "" {
			continue
		}
		arms = append(arms, arm{id: tr.id, sliceName: sliceName})
	}

	var chain ir.Stmt = &ir.Return{Results: []ir.Expr{nullA}}
	for i := len(arms) - 1; i >= 0; i-- {
		a := arms[i]
		sty := &ir.Ref{Named: a.sliceName}
		mk := &ir.Instr{
			Op:   ir.OpMakeSlice,
			Type: sty,
			Args: []ir.Expr{
				&ir.Name{Raw: "len", Var: ln},
				&ir.Name{Raw: "cap", Var: cp},
			},
		}
		chain = &ir.If{
			Cond: &ir.Instr{Op: ir.OpI32Eq, Args: []ir.Expr{
				&ir.Name{Raw: "id", Var: idV},
				&ir.Const{Op: ir.OpI32Const, I64: int64(a.id)},
			}},
			Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{
				&ir.Instr{Op: ir.OpRefCast, Type: anyT, Args: []ir.Expr{mk}},
			}}}},
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
	g.replaceBody(name, []*ir.Var{t, ln, cp}, []ir.Type{anyT}, body)
}

// zeroValueExpr returns an IR expression for the zero value of tr suitable as
// the data word of reflect.New(t) / New().Elem().
func (g *gen) zeroValueExpr(tr *typeRec) ir.Expr {
	if tr == nil {
		return nil
	}
	anyT := &ir.Ref{Null: true, Heap: ir.HeapAny}
	switch tr.kind {
	case KindBool, KindInt, KindInt8, KindInt16, KindInt32,
		KindUint, KindUint8, KindUint16, KindUint32, KindUintptr:
		return &ir.Instr{
			Op: ir.OpStructNew, Type: &ir.Ref{Named: "$go.box.i32"},
			Args: []ir.Expr{&ir.Const{Op: ir.OpI32Const, I64: 0}},
		}
	case KindInt64, KindUint64:
		return &ir.Instr{
			Op: ir.OpStructNew, Type: &ir.Ref{Named: "$go.box.i64"},
			Args: []ir.Expr{&ir.Const{Op: ir.OpI64Const, I64: 0}},
		}
	case KindFloat32:
		return &ir.Instr{
			Op: ir.OpStructNew, Type: &ir.Ref{Named: "$go.box.f32"},
			Args: []ir.Expr{&ir.Const{Op: ir.OpF32Const, F64: 0}},
		}
	case KindFloat64:
		return &ir.Instr{
			Op: ir.OpStructNew, Type: &ir.Ref{Named: "$go.box.f64"},
			Args: []ir.Expr{&ir.Const{Op: ir.OpF64Const, F64: 0}},
		}
	case KindComplex64:
		// Mutable $go.complex64 so New(complex64).Elem().SetComplex works.
		g.ensureComplexTypes()
		return &ir.Instr{
			Op: ir.OpStructNew, Type: &ir.Ref{Named: "$go.complex64"},
			Args: []ir.Expr{
				&ir.Const{Op: ir.OpF32Const, F64: 0},
				&ir.Const{Op: ir.OpF32Const, F64: 0},
			},
		}
	case KindComplex128:
		g.ensureComplexTypes()
		return &ir.Instr{
			Op: ir.OpStructNew, Type: &ir.Ref{Named: "$go.complex128"},
			Args: []ir.Expr{
				&ir.Const{Op: ir.OpF64Const, F64: 0},
				&ir.Const{Op: ir.OpF64Const, F64: 0},
			},
		}
	case KindString:
		// Mutable holder so New(string).Elem().SetString works.
		g.ensureBoxString()
		return &ir.Instr{
			Op: ir.OpStructNew, Type: &ir.Ref{Named: "$go.box.string"},
			Args: []ir.Expr{g.stringConstExpr("")},
		}
	case KindStruct:
		st, wasmName := g.structShape(tr)
		if st == nil || wasmName == "" {
			return nil
		}
		g.ensureStructWasmType(wasmName, st)
		td := g.mod.LookupType(wasmName)
		if td == nil {
			return &ir.Instr{Op: ir.OpStructNewDefault, Type: &ir.Ref{Named: wasmName}}
		}
		return g.structZeroIR(td, &ir.Ref{Named: wasmName}, map[string]bool{})
	case KindArray:
		// Arrays are never nil in Go; SetZero/json-null must allocate a real
		// zero array (MergeNull / ReflectNew), not leave a null ref.
		arrName := g.arrayIRName(tr)
		if arrName == "" || tr.goType == nil {
			return nil
		}
		a, ok := tr.goType.Underlying().(*types.Array)
		if !ok {
			return nil
		}
		n := a.Len()
		if n < 0 {
			n = 0
		}
		aty := &ir.Ref{Named: arrName}
		if arrName == "$go.array.go.string" {
			return &ir.Instr{
				Op:   ir.OpArrayNew,
				Type: aty,
				Args: []ir.Expr{
					g.stringConstExpr(""),
					&ir.Const{Op: ir.OpI32Const, I64: n},
				},
			}
		}
		return &ir.Instr{
			Op:   ir.OpArrayNewDefault,
			Type: aty,
			Args: []ir.Expr{&ir.Const{Op: ir.OpI32Const, I64: n}},
		}
	case KindSlice:
		// Nil slice.
		_, sliceName := g.sliceElemIR(tr)
		if sliceName == "" {
			return &ir.Instr{Op: ir.OpRefNull, Type: anyT}
		}
		return &ir.Instr{Op: ir.OpRefNull, Type: &ir.Ref{Null: true, Named: sliceName}}
	case KindMap:
		return &ir.Instr{Op: ir.OpRefNull, Type: anyT}
	case KindPointer:
		return &ir.Instr{Op: ir.OpRefNull, Type: anyT}
	case KindInterface:
		return &ir.Instr{Op: ir.OpRefNull, Type: &ir.Ref{Null: true, Named: "$go.iface"}}
	}
	if tr.goType != nil {
		if _, ok := tr.goType.Underlying().(*types.Basic); ok {
			return nil
		}
	}
	return nil
}

func (g *gen) structZeroIR(td *ir.TypeDef, t ir.Type, seen map[string]bool) ir.Expr {
	if td == nil {
		// Prefer null over struct.new_default: many structs have non-defaultable
		// (ref $go.string) fields that Wasm rejects for new_default.
		return &ir.Instr{Op: ir.OpRefNull, Type: t}
	}
	if seen[td.Name] {
		// Cycle / self-pointer: null (same as lower.structZeroExpr).
		return &ir.Instr{Op: ir.OpRefNull, Type: t}
	}
	seen[td.Name] = true
	args := make([]ir.Expr, len(td.Fields))
	for i, f := range td.Fields {
		switch ft := f.Type.(type) {
		case *ir.NumType:
			switch ft {
			case ir.TypeI32:
				args[i] = &ir.Const{Op: ir.OpI32Const, I64: 0}
			case ir.TypeI64:
				args[i] = &ir.Const{Op: ir.OpI64Const, I64: 0}
			case ir.TypeF32:
				args[i] = &ir.Const{Op: ir.OpF32Const, F64: 0}
			case ir.TypeF64:
				args[i] = &ir.Const{Op: ir.OpF64Const, F64: 0}
			default:
				args[i] = &ir.Const{Op: ir.OpI32Const, I64: 0}
			}
		case *ir.Ref:
			if ft.Named == "$go.string" {
				args[i] = g.stringConstExpr("")
				continue
			}
			// Pointer fields and same-type fields are *T (null).
			if f.Ptr || (ft.Named != "" && ft.Named == td.Name) {
				args[i] = &ir.Instr{Op: ir.OpRefNull, Type: ft}
				continue
			}
			// Nested program structs only (not $go.string / boxes / runtime).
			if ntd := g.mod.LookupType(ft.Named); ntd != nil && ntd.Kind == ir.TypeStruct &&
				len(ft.Named) >= 11 && ft.Named[:11] == "$go.struct." {
				args[i] = g.structZeroIR(ntd, ft, seen)
				continue
			}
			// Fixed arrays ($go.bytes / $go.array.*): allocate empty/zero when Len set.
			if f.Len > 0 && (ft.Named == "$go.bytes" || ft.Named == "$go.i16s" ||
				(len(ft.Named) > 10 && ft.Named[:10] == "$go.array.")) {
				if ft.Named == "$go.array.go.string" {
					args[i] = &ir.Instr{
						Op:   ir.OpArrayNew,
						Type: &ir.Ref{Named: ft.Named},
						Args: []ir.Expr{
							g.stringConstExpr(""),
							&ir.Const{Op: ir.OpI32Const, I64: f.Len},
						},
					}
				} else {
					args[i] = &ir.Instr{
						Op:   ir.OpArrayNewDefault,
						Type: &ir.Ref{Named: ft.Named},
						Args: []ir.Expr{&ir.Const{Op: ir.OpI32Const, I64: f.Len}},
					}
				}
				continue
			}
			args[i] = &ir.Instr{Op: ir.OpRefNull, Type: ft}
		default:
			args[i] = &ir.Instr{Op: ir.OpRefNull, Type: f.Type}
		}
	}
	delete(seen, td.Name)
	return &ir.Instr{Op: ir.OpStructNew, Type: t, Args: args}
}
