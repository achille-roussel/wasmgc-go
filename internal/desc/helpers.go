package desc

import (
	"github.com/achille-roussel/wasmgc-go/internal/ir"
)

// fillReflectHelpers replaces empty bodies of runtime.Reflect* unpack helpers
// that reflect/reflectlite call (docs/design/05).
func (g *gen) fillReflectHelpers() error {
	g.fillReflectType()
	g.fillReflectData()
	g.fillReflectTypeName()
	g.fillReflectExtra()
	g.fillReflectString()
	g.fillReflectIface()
	g.fillReflectCallGetField()
	g.fillReflectCallIndex()
	g.fillReflectCallLen()
	g.fillReflectStructNumField()
	g.fillReflectStructFieldType()
	g.fillReflectStructFieldName()
	g.fillReflectSliceElem()
	g.fillReflectPtrElem()
	g.fillReflectMapKey()
	g.fillReflectMapElem()
	g.fillReflectUnboxI32()
	g.fillReflectUnboxI64()
	g.fillReflectUnboxF32()
	g.fillReflectUnboxF64()
	return nil
}

func (g *gen) replaceBody(name string, params []*ir.Var, results []ir.Type, body ir.Region) {
	f := g.mod.LookupFunc(name)
	if f == nil {
		// Do not invent helpers into empty_main / println — only fill when
		// the rewrite runtime decl survived linking.
		return
	}
	if !emptyBody(f) {
		return
	}
	f.Params = params
	f.Results = results
	f.Body = body
}

func emptyBody(f *ir.Func) bool {
	if f == nil || f.Body == nil {
		return true
	}
	seq, ok := f.Body.(*ir.Seq)
	return ok && len(seq.Stmts) == 0
}

func (g *gen) fillReflectType() {
	name := "runtime.ReflectType"
	ifaceTy := &ir.Ref{Null: true, Named: "$go.iface"}
	itabTy := &ir.Ref{Named: "$go.itab"}
	i := &ir.Var{Name: "i", Typ: ifaceTy}
	itab := &ir.Var{Name: "itab", Typ: itabTy}
	body := &ir.Seq{Stmts: []ir.Stmt{
		&ir.VarDef{Var: itab},
		&ir.If{
			Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{&ir.Name{Raw: "i", Var: i}}},
			Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{
				&ir.Instr{Op: ir.OpRefNull, Type: &ir.Ref{Null: true, Named: "$go.type"}},
			}}}},
			Else: &ir.Seq{Stmts: []ir.Stmt{
				&ir.Assign{Lhs: []ir.Dest{{Name: "itab", Var: itab}}, Rhs: &ir.Instr{
					Op: ir.OpStructGet, Type: ifaceTy, Field: "itab",
					Args: []ir.Expr{&ir.Name{Raw: "i", Var: i}},
				}},
				&ir.Return{Results: []ir.Expr{&ir.Instr{
					Op: ir.OpStructGet, Type: itabTy, Field: "type",
					Args: []ir.Expr{&ir.Name{Raw: "itab", Var: itab}},
				}}},
			}},
		},
		&ir.Unreachable{},
	}}
	g.replaceBody(name, []*ir.Var{i}, []ir.Type{&ir.Ref{Null: true, Named: "$go.type"}}, body)
}

func (g *gen) fillReflectData() {
	name := "runtime.ReflectData"
	ifaceTy := &ir.Ref{Null: true, Named: "$go.iface"}
	anyT := &ir.Ref{Null: true, Heap: ir.HeapAny}
	i := &ir.Var{Name: "i", Typ: ifaceTy}
	body := &ir.Seq{Stmts: []ir.Stmt{
		&ir.If{
			Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{&ir.Name{Raw: "i", Var: i}}},
			Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{
				&ir.Instr{Op: ir.OpRefNull, Type: anyT},
			}}}},
			Else: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{&ir.Instr{
				Op: ir.OpStructGet, Type: ifaceTy, Field: "data",
				Args: []ir.Expr{&ir.Name{Raw: "i", Var: i}},
			}}}}},
		},
		&ir.Unreachable{},
	}}
	g.replaceBody(name, []*ir.Var{i}, []ir.Type{anyT}, body)
}

func (g *gen) fillReflectTypeName() {
	name := "runtime.ReflectTypeName"
	if g.mod.LookupFunc(name) == nil || !emptyBody(g.mod.LookupFunc(name)) {
		return
	}
	typeTy := &ir.Ref{Null: true, Named: "$go.type"}
	strTy := &ir.Ref{Named: "$go.string"}
	t := &ir.Var{Name: "t", Typ: typeTy}
	nameV := &ir.Var{Name: "n", Typ: &ir.Ref{Null: true, Named: "$go.string"}}
	body := &ir.Seq{Stmts: []ir.Stmt{
		&ir.VarDef{Var: nameV},
		&ir.If{
			Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{&ir.Name{Raw: "t", Var: t}}},
			Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{g.stringConstExpr("")}}}},
			Else: &ir.Seq{Stmts: []ir.Stmt{
				&ir.Assign{Lhs: []ir.Dest{{Name: "n", Var: nameV}}, Rhs: &ir.Instr{
					Op: ir.OpStructGet, Type: &ir.Ref{Named: "$go.type"}, Field: "name",
					Args: []ir.Expr{&ir.Name{Raw: "t", Var: t}},
				}},
				&ir.If{
					Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{&ir.Name{Raw: "n", Var: nameV}}},
					Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{g.stringConstExpr("")}}}},
					Else: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{&ir.Instr{
						Op: ir.OpRefCast, Type: strTy, Args: []ir.Expr{&ir.Name{Raw: "n", Var: nameV}},
					}}}}},
				},
			}},
		},
		&ir.Unreachable{},
	}}
	g.replaceBody(name, []*ir.Var{t}, []ir.Type{strTy}, body)
}

func (g *gen) fillReflectExtra() {
	name := "runtime.ReflectExtra"
	typeTy := &ir.Ref{Null: true, Named: "$go.type"}
	anyT := &ir.Ref{Null: true, Heap: ir.HeapAny}
	t := &ir.Var{Name: "t", Typ: typeTy}
	body := &ir.Seq{Stmts: []ir.Stmt{
		&ir.If{
			Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{&ir.Name{Raw: "t", Var: t}}},
			Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{
				&ir.Instr{Op: ir.OpRefNull, Type: anyT},
			}}}},
			Else: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{&ir.Instr{
				Op: ir.OpRefCast, Type: anyT,
				Args: []ir.Expr{&ir.Instr{
					Op: ir.OpStructGet, Type: &ir.Ref{Named: "$go.type"}, Field: "extra",
					Args: []ir.Expr{&ir.Name{Raw: "t", Var: t}},
				}},
			}}}}},
		},
		&ir.Unreachable{},
	}}
	g.replaceBody(name, []*ir.Var{t}, []ir.Type{anyT}, body)
}

func (g *gen) fillReflectString() {
	name := "runtime.ReflectString"
	if g.mod.LookupFunc(name) == nil || !emptyBody(g.mod.LookupFunc(name)) {
		return
	}
	anyT := &ir.Ref{Null: true, Heap: ir.HeapAny}
	strTy := &ir.Ref{Named: "$go.string"}
	d := &ir.Var{Name: "data", Typ: anyT}
	body := &ir.Seq{Stmts: []ir.Stmt{
		&ir.If{
			Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{&ir.Name{Raw: "data", Var: d}}},
			Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{g.stringConstExpr("")}}}},
			Else: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{&ir.Instr{
				Op: ir.OpRefCast, Type: strTy, Args: []ir.Expr{&ir.Name{Raw: "data", Var: d}},
			}}}}},
		},
		&ir.Unreachable{},
	}}
	g.replaceBody(name, []*ir.Var{d}, []ir.Type{strTy}, body)
}

func (g *gen) fillReflectIface() {
	name := "runtime.ReflectIface"
	typeTy := &ir.Ref{Null: true, Named: "$go.type"}
	anyT := &ir.Ref{Null: true, Heap: ir.HeapAny}
	ifaceTy := &ir.Ref{Null: true, Named: "$go.iface"}
	itabTy := &ir.Ref{Named: "$go.itab"}
	typ := &ir.Var{Name: "typ", Typ: typeTy}
	data := &ir.Var{Name: "data", Typ: anyT}
	itab := &ir.Var{Name: "itab", Typ: itabTy}
	body := &ir.Seq{Stmts: []ir.Stmt{
		&ir.VarDef{Var: itab},
		&ir.If{
			Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{&ir.Name{Raw: "typ", Var: typ}}},
			Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{
				&ir.Instr{Op: ir.OpRefNull, Type: ifaceTy},
			}}}},
			Else: &ir.Seq{Stmts: []ir.Stmt{
				&ir.Assign{Lhs: []ir.Dest{{Name: "itab", Var: itab}}, Rhs: &ir.Instr{
					Op: ir.OpStructNew, Type: itabTy,
					Args: []ir.Expr{&ir.Name{Raw: "typ", Var: typ}},
				}},
				&ir.Return{Results: []ir.Expr{&ir.Instr{
					Op: ir.OpStructNew, Type: &ir.Ref{Named: "$go.iface"},
					Args: []ir.Expr{
						&ir.Name{Raw: "itab", Var: itab},
						&ir.Name{Raw: "data", Var: data},
					},
				}}},
			}},
		},
		&ir.Unreachable{},
	}}
	g.replaceBody(name, []*ir.Var{typ, data}, []ir.Type{ifaceTy}, body)
}

func (g *gen) fillReflectCallGetField() {
	name := "runtime.ReflectCallGetField"
	// Only useful when $go.extra.struct exists; otherwise return null.
	anyT := &ir.Ref{Null: true, Heap: ir.HeapAny}
	extra := &ir.Var{Name: "extra", Typ: anyT}
	data := &ir.Var{Name: "data", Typ: anyT}
	i := &ir.Var{Name: "i", Typ: ir.TypeI32}
	if g.mod.LookupType("$go.extra.struct") == nil {
		g.replaceBody(name, []*ir.Var{extra, data, i}, []ir.Type{anyT}, &ir.Seq{Stmts: []ir.Stmt{
			&ir.Return{Results: []ir.Expr{&ir.Instr{Op: ir.OpRefNull, Type: anyT}}},
		}})
		return
	}
	exTy := &ir.Ref{Named: "$go.extra.struct"}
	fnTy := &ir.Ref{Named: "$go.fn.getField"}
	ex := &ir.Var{Name: "ex", Typ: exTy}
	fn := &ir.Var{Name: "fn", Typ: fnTy}
	body := &ir.Seq{Stmts: []ir.Stmt{
		&ir.VarDef{Var: ex}, &ir.VarDef{Var: fn},
		&ir.If{
			Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{&ir.Name{Raw: "extra", Var: extra}}},
			Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{
				&ir.Instr{Op: ir.OpRefNull, Type: anyT},
			}}}},
			Else: &ir.Seq{Stmts: []ir.Stmt{
				&ir.Assign{Lhs: []ir.Dest{{Name: "ex", Var: ex}}, Rhs: &ir.Instr{
					Op: ir.OpRefCast, Type: exTy, Args: []ir.Expr{&ir.Name{Raw: "extra", Var: extra}},
				}},
				&ir.Assign{Lhs: []ir.Dest{{Name: "fn", Var: fn}}, Rhs: &ir.Instr{
					Op: ir.OpStructGet, Type: exTy, Field: "getField",
					Args: []ir.Expr{&ir.Name{Raw: "ex", Var: ex}},
				}},
				&ir.Return{Results: []ir.Expr{&ir.Instr{
					Op: ir.OpCallRef, Type: fnTy,
					Args: []ir.Expr{
						&ir.Name{Raw: "data", Var: data},
						&ir.Name{Raw: "i", Var: i},
						&ir.Name{Raw: "fn", Var: fn},
					},
				}}},
			}},
		},
		&ir.Unreachable{},
	}}
	g.replaceBody(name, []*ir.Var{extra, data, i}, []ir.Type{anyT}, body)
}

func (g *gen) fillReflectCallIndex() {
	name := "runtime.ReflectCallIndex"
	anyT := &ir.Ref{Null: true, Heap: ir.HeapAny}
	extra := &ir.Var{Name: "extra", Typ: anyT}
	data := &ir.Var{Name: "data", Typ: anyT}
	i := &ir.Var{Name: "i", Typ: ir.TypeI32}
	if g.mod.LookupType("$go.extra.slice") == nil {
		g.replaceBody(name, []*ir.Var{extra, data, i}, []ir.Type{anyT}, &ir.Seq{Stmts: []ir.Stmt{
			&ir.Return{Results: []ir.Expr{&ir.Instr{Op: ir.OpRefNull, Type: anyT}}},
		}})
		return
	}
	exTy := &ir.Ref{Named: "$go.extra.slice"}
	fnTy := &ir.Ref{Named: "$go.fn.index"}
	ex := &ir.Var{Name: "ex", Typ: exTy}
	fn := &ir.Var{Name: "fn", Typ: fnTy}
	body := &ir.Seq{Stmts: []ir.Stmt{
		&ir.VarDef{Var: ex}, &ir.VarDef{Var: fn},
		&ir.If{
			Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{&ir.Name{Raw: "extra", Var: extra}}},
			Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{
				&ir.Instr{Op: ir.OpRefNull, Type: anyT},
			}}}},
			Else: &ir.Seq{Stmts: []ir.Stmt{
				&ir.Assign{Lhs: []ir.Dest{{Name: "ex", Var: ex}}, Rhs: &ir.Instr{
					Op: ir.OpRefCast, Type: exTy, Args: []ir.Expr{&ir.Name{Raw: "extra", Var: extra}},
				}},
				&ir.Assign{Lhs: []ir.Dest{{Name: "fn", Var: fn}}, Rhs: &ir.Instr{
					Op: ir.OpStructGet, Type: exTy, Field: "index",
					Args: []ir.Expr{&ir.Name{Raw: "ex", Var: ex}},
				}},
				&ir.Return{Results: []ir.Expr{&ir.Instr{
					Op: ir.OpCallRef, Type: fnTy,
					Args: []ir.Expr{
						&ir.Name{Raw: "data", Var: data},
						&ir.Name{Raw: "i", Var: i},
						&ir.Name{Raw: "fn", Var: fn},
					},
				}}},
			}},
		},
		&ir.Unreachable{},
	}}
	g.replaceBody(name, []*ir.Var{extra, data, i}, []ir.Type{anyT}, body)
}

func (g *gen) fillReflectCallLen() {
	name := "runtime.ReflectCallLen"
	anyT := &ir.Ref{Null: true, Heap: ir.HeapAny}
	extra := &ir.Var{Name: "extra", Typ: anyT}
	data := &ir.Var{Name: "data", Typ: anyT}
	if g.mod.LookupType("$go.extra.slice") == nil {
		g.replaceBody(name, []*ir.Var{extra, data}, []ir.Type{ir.TypeI32}, &ir.Seq{Stmts: []ir.Stmt{
			&ir.Return{Results: []ir.Expr{&ir.Const{Op: ir.OpI32Const, I64: 0}}},
		}})
		return
	}
	exTy := &ir.Ref{Named: "$go.extra.slice"}
	fnTy := &ir.Ref{Named: "$go.fn.len"}
	ex := &ir.Var{Name: "ex", Typ: exTy}
	fn := &ir.Var{Name: "fn", Typ: fnTy}
	body := &ir.Seq{Stmts: []ir.Stmt{
		&ir.VarDef{Var: ex}, &ir.VarDef{Var: fn},
		&ir.If{
			Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{&ir.Name{Raw: "extra", Var: extra}}},
			Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{
				&ir.Const{Op: ir.OpI32Const, I64: 0},
			}}}},
			Else: &ir.Seq{Stmts: []ir.Stmt{
				&ir.Assign{Lhs: []ir.Dest{{Name: "ex", Var: ex}}, Rhs: &ir.Instr{
					Op: ir.OpRefCast, Type: exTy, Args: []ir.Expr{&ir.Name{Raw: "extra", Var: extra}},
				}},
				&ir.Assign{Lhs: []ir.Dest{{Name: "fn", Var: fn}}, Rhs: &ir.Instr{
					Op: ir.OpStructGet, Type: exTy, Field: "len",
					Args: []ir.Expr{&ir.Name{Raw: "ex", Var: ex}},
				}},
				&ir.Return{Results: []ir.Expr{&ir.Instr{
					Op: ir.OpCallRef, Type: fnTy,
					Args: []ir.Expr{
						&ir.Name{Raw: "data", Var: data},
						&ir.Name{Raw: "fn", Var: fn},
					},
				}}},
			}},
		},
		&ir.Unreachable{},
	}}
	g.replaceBody(name, []*ir.Var{extra, data}, []ir.Type{ir.TypeI32}, body)
}

func (g *gen) fillReflectStructNumField() {
	name := "runtime.ReflectStructNumField"
	anyT := &ir.Ref{Null: true, Heap: ir.HeapAny}
	extra := &ir.Var{Name: "extra", Typ: anyT}
	if g.mod.LookupType("$go.extra.struct") == nil {
		g.replaceBody(name, []*ir.Var{extra}, []ir.Type{ir.TypeI32}, &ir.Seq{Stmts: []ir.Stmt{
			&ir.Return{Results: []ir.Expr{&ir.Const{Op: ir.OpI32Const, I64: 0}}},
		}})
		return
	}
	exTy := &ir.Ref{Named: "$go.extra.struct"}
	ex := &ir.Var{Name: "ex", Typ: exTy}
	body := &ir.Seq{Stmts: []ir.Stmt{
		&ir.VarDef{Var: ex},
		&ir.If{
			Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{&ir.Name{Raw: "extra", Var: extra}}},
			Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{&ir.Const{Op: ir.OpI32Const, I64: 0}}}}},
			Else: &ir.Seq{Stmts: []ir.Stmt{
				&ir.Assign{Lhs: []ir.Dest{{Name: "ex", Var: ex}}, Rhs: &ir.Instr{
					Op: ir.OpRefCast, Type: exTy, Args: []ir.Expr{&ir.Name{Raw: "extra", Var: extra}},
				}},
				&ir.Return{Results: []ir.Expr{&ir.Instr{
					Op: ir.OpStructGet, Type: exTy, Field: "numField",
					Args: []ir.Expr{&ir.Name{Raw: "ex", Var: ex}},
				}}},
			}},
		},
		&ir.Unreachable{},
	}}
	g.replaceBody(name, []*ir.Var{extra}, []ir.Type{ir.TypeI32}, body)
}

func (g *gen) fillReflectStructFieldType() {
	name := "runtime.ReflectStructFieldType"
	anyT := &ir.Ref{Null: true, Heap: ir.HeapAny}
	extra := &ir.Var{Name: "extra", Typ: anyT}
	i := &ir.Var{Name: "i", Typ: ir.TypeI32}
	nullT := &ir.Instr{Op: ir.OpRefNull, Type: &ir.Ref{Null: true, Named: "$go.type"}}
	if g.mod.LookupType("$go.extra.struct") == nil {
		g.replaceBody(name, []*ir.Var{extra, i}, []ir.Type{&ir.Ref{Null: true, Named: "$go.type"}}, &ir.Seq{Stmts: []ir.Stmt{
			&ir.Return{Results: []ir.Expr{nullT}},
		}})
		return
	}
	exTy := &ir.Ref{Named: "$go.extra.struct"}
	fnTy := &ir.Ref{Named: "$go.fn.fieldType"}
	ex := &ir.Var{Name: "ex", Typ: exTy}
	fn := &ir.Var{Name: "fn", Typ: fnTy}
	body := &ir.Seq{Stmts: []ir.Stmt{
		&ir.VarDef{Var: ex}, &ir.VarDef{Var: fn},
		&ir.If{
			Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{&ir.Name{Raw: "extra", Var: extra}}},
			Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{nullT}}}},
			Else: &ir.Seq{Stmts: []ir.Stmt{
				&ir.Assign{Lhs: []ir.Dest{{Name: "ex", Var: ex}}, Rhs: &ir.Instr{
					Op: ir.OpRefCast, Type: exTy, Args: []ir.Expr{&ir.Name{Raw: "extra", Var: extra}},
				}},
				&ir.Assign{Lhs: []ir.Dest{{Name: "fn", Var: fn}}, Rhs: &ir.Instr{
					Op: ir.OpStructGet, Type: exTy, Field: "fieldType",
					Args: []ir.Expr{&ir.Name{Raw: "ex", Var: ex}},
				}},
				&ir.Return{Results: []ir.Expr{&ir.Instr{
					Op: ir.OpCallRef, Type: fnTy,
					Args: []ir.Expr{&ir.Name{Raw: "i", Var: i}, &ir.Name{Raw: "fn", Var: fn}},
				}}},
			}},
		},
		&ir.Unreachable{},
	}}
	g.replaceBody(name, []*ir.Var{extra, i}, []ir.Type{&ir.Ref{Null: true, Named: "$go.type"}}, body)
}

func (g *gen) fillReflectStructFieldName() {
	name := "runtime.ReflectStructFieldName"
	if g.mod.LookupFunc(name) == nil || !emptyBody(g.mod.LookupFunc(name)) {
		return
	}
	anyT := &ir.Ref{Null: true, Heap: ir.HeapAny}
	extra := &ir.Var{Name: "extra", Typ: anyT}
	i := &ir.Var{Name: "i", Typ: ir.TypeI32}
	if g.mod.LookupType("$go.extra.struct") == nil {
		g.replaceBody(name, []*ir.Var{extra, i}, []ir.Type{&ir.Ref{Named: "$go.string"}}, &ir.Seq{Stmts: []ir.Stmt{
			&ir.Return{Results: []ir.Expr{g.stringConstExpr("")}},
		}})
		return
	}
	exTy := &ir.Ref{Named: "$go.extra.struct"}
	fnTy := &ir.Ref{Named: "$go.fn.fieldName"}
	ex := &ir.Var{Name: "ex", Typ: exTy}
	fn := &ir.Var{Name: "fn", Typ: fnTy}
	body := &ir.Seq{Stmts: []ir.Stmt{
		&ir.VarDef{Var: ex}, &ir.VarDef{Var: fn},
		&ir.If{
			Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{&ir.Name{Raw: "extra", Var: extra}}},
			Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{g.stringConstExpr("")}}}},
			Else: &ir.Seq{Stmts: []ir.Stmt{
				&ir.Assign{Lhs: []ir.Dest{{Name: "ex", Var: ex}}, Rhs: &ir.Instr{
					Op: ir.OpRefCast, Type: exTy, Args: []ir.Expr{&ir.Name{Raw: "extra", Var: extra}},
				}},
				&ir.Assign{Lhs: []ir.Dest{{Name: "fn", Var: fn}}, Rhs: &ir.Instr{
					Op: ir.OpStructGet, Type: exTy, Field: "fieldName",
					Args: []ir.Expr{&ir.Name{Raw: "ex", Var: ex}},
				}},
				&ir.Return{Results: []ir.Expr{&ir.Instr{
					Op: ir.OpCallRef, Type: fnTy,
					Args: []ir.Expr{&ir.Name{Raw: "i", Var: i}, &ir.Name{Raw: "fn", Var: fn}},
				}}},
			}},
		},
		&ir.Unreachable{},
	}}
	g.replaceBody(name, []*ir.Var{extra, i}, []ir.Type{&ir.Ref{Named: "$go.string"}}, body)
}

func (g *gen) fillReflectSliceElem() {
	g.fillReflectElemField("runtime.ReflectSliceElem", "$go.extra.slice")
}

func (g *gen) fillReflectPtrElem() {
	g.fillReflectElemField("runtime.ReflectPtrElem", "$go.extra.ptr")
}

func (g *gen) fillReflectElemField(name, extraType string) {
	anyT := &ir.Ref{Null: true, Heap: ir.HeapAny}
	extra := &ir.Var{Name: "extra", Typ: anyT}
	nullT := &ir.Instr{Op: ir.OpRefNull, Type: &ir.Ref{Null: true, Named: "$go.type"}}
	if g.mod.LookupType(extraType) == nil {
		g.replaceBody(name, []*ir.Var{extra}, []ir.Type{&ir.Ref{Null: true, Named: "$go.type"}}, &ir.Seq{Stmts: []ir.Stmt{
			&ir.Return{Results: []ir.Expr{nullT}},
		}})
		return
	}
	exTy := &ir.Ref{Named: extraType}
	ex := &ir.Var{Name: "ex", Typ: exTy}
	body := &ir.Seq{Stmts: []ir.Stmt{
		&ir.VarDef{Var: ex},
		&ir.If{
			Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{&ir.Name{Raw: "extra", Var: extra}}},
			Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{nullT}}}},
			Else: &ir.Seq{Stmts: []ir.Stmt{
				&ir.Assign{Lhs: []ir.Dest{{Name: "ex", Var: ex}}, Rhs: &ir.Instr{
					Op: ir.OpRefCast, Type: exTy, Args: []ir.Expr{&ir.Name{Raw: "extra", Var: extra}},
				}},
				&ir.Return{Results: []ir.Expr{&ir.Instr{
					Op: ir.OpStructGet, Type: exTy, Field: "elem",
					Args: []ir.Expr{&ir.Name{Raw: "ex", Var: ex}},
				}}},
			}},
		},
		&ir.Unreachable{},
	}}
	g.replaceBody(name, []*ir.Var{extra}, []ir.Type{&ir.Ref{Null: true, Named: "$go.type"}}, body)
}

func (g *gen) fillReflectMapKey() {
	g.fillReflectMapField("runtime.ReflectMapKey", "key")
}

func (g *gen) fillReflectMapElem() {
	g.fillReflectMapField("runtime.ReflectMapElem", "elem")
}

func (g *gen) fillReflectMapField(name, field string) {
	anyT := &ir.Ref{Null: true, Heap: ir.HeapAny}
	extra := &ir.Var{Name: "extra", Typ: anyT}
	nullT := &ir.Instr{Op: ir.OpRefNull, Type: &ir.Ref{Null: true, Named: "$go.type"}}
	if g.mod.LookupType("$go.extra.map") == nil {
		g.replaceBody(name, []*ir.Var{extra}, []ir.Type{&ir.Ref{Null: true, Named: "$go.type"}}, &ir.Seq{Stmts: []ir.Stmt{
			&ir.Return{Results: []ir.Expr{nullT}},
		}})
		return
	}
	exTy := &ir.Ref{Named: "$go.extra.map"}
	ex := &ir.Var{Name: "ex", Typ: exTy}
	body := &ir.Seq{Stmts: []ir.Stmt{
		&ir.VarDef{Var: ex},
		&ir.If{
			Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{&ir.Name{Raw: "extra", Var: extra}}},
			Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{nullT}}}},
			Else: &ir.Seq{Stmts: []ir.Stmt{
				&ir.Assign{Lhs: []ir.Dest{{Name: "ex", Var: ex}}, Rhs: &ir.Instr{
					Op: ir.OpRefCast, Type: exTy, Args: []ir.Expr{&ir.Name{Raw: "extra", Var: extra}},
				}},
				&ir.Return{Results: []ir.Expr{&ir.Instr{
					Op: ir.OpStructGet, Type: exTy, Field: field,
					Args: []ir.Expr{&ir.Name{Raw: "ex", Var: ex}},
				}}},
			}},
		},
		&ir.Unreachable{},
	}}
	g.replaceBody(name, []*ir.Var{extra}, []ir.Type{&ir.Ref{Null: true, Named: "$go.type"}}, body)
}

func (g *gen) fillReflectUnboxI32() {
	g.fillReflectUnbox("runtime.ReflectUnboxI32", "$go.box.i32", ir.TypeI32)
}
func (g *gen) fillReflectUnboxI64() {
	g.fillReflectUnbox("runtime.ReflectUnboxI64", "$go.box.i64", ir.TypeI64)
}
func (g *gen) fillReflectUnboxF32() {
	g.fillReflectUnbox("runtime.ReflectUnboxF32", "$go.box.f32", ir.TypeF32)
}
func (g *gen) fillReflectUnboxF64() {
	g.fillReflectUnbox("runtime.ReflectUnboxF64", "$go.box.f64", ir.TypeF64)
}

func (g *gen) fillReflectUnbox(name, box string, res ir.Type) {
	anyT := &ir.Ref{Null: true, Heap: ir.HeapAny}
	data := &ir.Var{Name: "data", Typ: anyT}
	zero := ir.Expr(&ir.Const{Op: ir.OpI32Const, I64: 0})
	switch res {
	case ir.TypeI64:
		zero = &ir.Const{Op: ir.OpI64Const, I64: 0}
	case ir.TypeF32:
		zero = &ir.Const{Op: ir.OpF32Const, F64: 0}
	case ir.TypeF64:
		zero = &ir.Const{Op: ir.OpF64Const, F64: 0}
	}
	boxTy := &ir.Ref{Named: box}
	b := &ir.Var{Name: "b", Typ: boxTy}
	body := &ir.Seq{Stmts: []ir.Stmt{
		&ir.VarDef{Var: b},
		&ir.If{
			Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{&ir.Name{Raw: "data", Var: data}}},
			Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{zero}}}},
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
	g.replaceBody(name, []*ir.Var{data}, []ir.Type{res}, body)
}
