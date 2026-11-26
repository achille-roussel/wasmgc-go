package desc

import (
	"go/types"
	"strings"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
)

// fillReflectHelpers replaces empty bodies of runtime.Reflect* unpack helpers
// that reflect/reflectlite call (docs/design/05).
// namedDisplay reports whether n looks like "pkg.Type" / "main.T".
func namedDisplay(n string) bool {
	for i := 0; i < len(n); i++ {
		if n[i] == '.' {
			return true
		}
	}
	return false
}

func typeDisplayName(tr *typeRec) string {
	if tr == nil {
		return ""
	}
	if tr.goType != nil {
		if n, ok := types.Unalias(tr.goType).(*types.Named); ok {
			obj := n.Obj()
			// Instantiated generics: include type args (issue49547 %T).
			// go/types.String uses Pkg.Path(); soft-suite module path is
			// "gotestcase" while //.out expects package name "main".
			if targs := n.TypeArgs(); targs != nil && targs.Len() > 0 {
				s := n.String()
				if obj != nil && obj.Pkg() != nil && obj.Pkg().Name() == "main" {
					if path := obj.Pkg().Path(); path != "" && path != "main" {
						s = strings.ReplaceAll(s, path+".", "main.")
					}
				}
				return s
			}
			if obj == nil {
				return tr.goType.String()
			}
			if pkg := obj.Pkg(); pkg != nil {
				if pkg.Name() == "main" {
					return "main." + obj.Name()
				}
				if pkg.Name() != "" {
					return pkg.Name() + "." + obj.Name()
				}
			}
			return obj.Name()
		}
		return tr.goType.String()
	}
	return tr.name
}

func (g *gen) fillReflectHelpers() error {
	g.fillReflectType()
	g.fillReflectData()
	g.fillReflectTypeName()
	g.fillReflectExtra()
	g.fillReflectString()
	g.fillReflectIface()
	g.fillReflectIfaceFromData()
	g.fillReflectPackAny()
	g.fillReflectCallGetField()
	g.fillReflectCallSetField()
	g.fillReflectCallIndex()
	g.fillReflectCallSetIndex()
	g.fillReflectCallLen()
	g.fillReflectArrayLen()
	g.fillReflectStructNumField()
	g.fillReflectStructFieldType()
	g.fillReflectStructFieldName()
	g.fillReflectStructFieldTag()
	g.fillReflectSliceElem()
	g.fillReflectPtrElem()
	g.fillReflectMapKey()
	g.fillReflectMapElem()
	g.fillReflectUnboxI32()
	g.fillReflectUnboxI64()
	g.fillReflectUnboxF32()
	g.fillReflectUnboxF64()
	g.fillReflectComplex64()
	g.fillReflectComplex128()
	g.fillReflectSetComplex()
	g.fillReflectSetBoxI32()
	g.fillReflectSetBoxI64()
	g.fillReflectSetBoxF32()
	g.fillReflectSetBoxF64()
	g.fillReflectSetBoxString()
	g.fillReflectSetBoxRef()
	g.fillReflectNewRefBox()
	g.fillReflectUnboxRef()
	g.fillReflectFatPtrGet()
	g.fillReflectFatPtrSet()
	g.fillReflectSliceOf()
	g.fillReflectArrayOf()
	g.fillReflectPointerTo()
	g.fillReflectMapOfEmpty()
	g.fillReflectImplements()
	g.fillReflectNew()
	g.fillReflectMakeSlice()
	if err := g.fillReflectMethodHelpers(); err != nil {
		return err
	}
	if err := g.fillReflectMakeFuncHelpers(); err != nil {
		return err
	}
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
	id := &ir.Var{Name: "id", Typ: ir.TypeI32}

	// Descriptor .name is always null (global init cannot embed string data).
	// Dispatch on type id → String()-style name so %T / unhashable panics work.
	type idName struct {
		id   int32
		name string
	}
	var entries []idName
	for _, tr := range g.types {
		if tr == nil || tr.id < 0 {
			continue
		}
		// Named pkg.Type display names for fmt %#v / reflect.Type.String and
		// unhashable map-key panics (recover2).
		n := typeDisplayName(tr)
		if n == "" || !namedDisplay(n) {
			continue
		}
		entries = append(entries, idName{id: tr.id, name: n})
	}
	for i := 0; i < len(entries); i++ {
		for j := i + 1; j < len(entries); j++ {
			if entries[j].id < entries[i].id {
				entries[i], entries[j] = entries[j], entries[i]
			}
		}
	}
	cases := make([]ir.Stmt, 0, len(entries)+1)
	for _, e := range entries {
		e := e
		cases = append(cases, &ir.If{
			Cond: &ir.Instr{Op: ir.OpI32Eq, Args: []ir.Expr{
				&ir.Name{Raw: "id", Var: id},
				&ir.Const{Op: ir.OpI32Const, I64: int64(e.id)},
			}},
			Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{g.stringConstExpr(e.name)}}}},
			Else: &ir.Seq{},
		})
	}
	cases = append(cases, &ir.Return{Results: []ir.Expr{g.stringConstExpr("")}})

	body := &ir.Seq{Stmts: []ir.Stmt{
		&ir.VarDef{Var: id},
		&ir.If{
			Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{&ir.Name{Raw: "t", Var: t}}},
			Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{g.stringConstExpr("")}}}},
			Else: &ir.Seq{Stmts: []ir.Stmt{
				&ir.Assign{Lhs: []ir.Dest{{Name: "id", Var: id}}, Rhs: &ir.Instr{
					Op: ir.OpStructGet, Type: &ir.Ref{Named: "$go.type"}, Field: "id",
					Args: []ir.Expr{&ir.Name{Raw: "t", Var: t}},
				}},
				&ir.Seq{Stmts: cases},
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
	// New(string).Elem() stores $go.box.string; ValueOf(&s).Elem() stores $go.ptr.ref;
	// plain iface data may already be $go.string.
	g.ensureBoxString()
	g.ensureFatPtrRefTypes()
	anyT := &ir.Ref{Null: true, Heap: ir.HeapAny}
	strTy := &ir.Ref{Named: "$go.string"}
	d := &ir.Var{Name: "data", Typ: anyT}
	boxTy := &ir.Ref{Named: "$go.box.string"}
	ptrTy := &ir.Ref{Named: "$go.ptr.ref"}
	classTy := &ir.Ref{Named: "$go.class.ref"}
	bv := &ir.Var{Name: "b", Typ: boxTy}
	p := &ir.Var{Name: "p", Typ: ptrTy}
	cl := &ir.Var{Name: "cl", Typ: classTy}
	castStr := &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{&ir.Instr{
		Op: ir.OpRefCast, Type: strTy, Args: []ir.Expr{&ir.Name{Raw: "data", Var: d}},
	}}}}}
	ptrBranch := &ir.Seq{Stmts: []ir.Stmt{
		&ir.VarDef{Var: p},
		&ir.VarDef{Var: cl},
		&ir.If{
			Cond: &ir.Instr{Op: ir.OpRefTest, Type: ptrTy, Args: []ir.Expr{&ir.Name{Raw: "data", Var: d}}},
			Then: &ir.Seq{Stmts: []ir.Stmt{
				&ir.Assign{Lhs: []ir.Dest{{Name: "p", Var: p}}, Rhs: &ir.Instr{
					Op: ir.OpRefCast, Type: ptrTy, Args: []ir.Expr{&ir.Name{Raw: "data", Var: d}},
				}},
				&ir.Assign{Lhs: []ir.Dest{{Name: "cl", Var: cl}}, Rhs: &ir.Instr{
					Op: ir.OpStructGet, Type: ptrTy, Field: "class",
					Args: []ir.Expr{&ir.Name{Raw: "p", Var: p}},
				}},
				&ir.Return{Results: []ir.Expr{&ir.Instr{
					Op: ir.OpRefCast, Type: strTy,
					Args: []ir.Expr{&ir.Instr{
						Op: ir.OpCallRef, Type: &ir.Ref{Named: "$go.get.ref"},
						Args: []ir.Expr{
							&ir.Instr{Op: ir.OpStructGet, Type: ptrTy, Field: "base", Args: []ir.Expr{&ir.Name{Raw: "p", Var: p}}},
							&ir.Instr{Op: ir.OpStructGet, Type: ptrTy, Field: "index", Args: []ir.Expr{&ir.Name{Raw: "p", Var: p}}},
							&ir.Instr{Op: ir.OpStructGet, Type: classTy, Field: "get", Args: []ir.Expr{&ir.Name{Raw: "cl", Var: cl}}},
						},
					}},
				}}},
			}},
			Else: castStr,
		},
	}}
	elseBody := &ir.Seq{Stmts: []ir.Stmt{
		&ir.VarDef{Var: bv},
		&ir.If{
			Cond: &ir.Instr{Op: ir.OpRefTest, Type: boxTy, Args: []ir.Expr{&ir.Name{Raw: "data", Var: d}}},
			Then: &ir.Seq{Stmts: []ir.Stmt{
				&ir.Assign{Lhs: []ir.Dest{{Name: "b", Var: bv}}, Rhs: &ir.Instr{
					Op: ir.OpRefCast, Type: boxTy, Args: []ir.Expr{&ir.Name{Raw: "data", Var: d}},
				}},
				&ir.Return{Results: []ir.Expr{&ir.Instr{
					Op: ir.OpStructGet, Type: boxTy, Field: "v",
					Args: []ir.Expr{&ir.Name{Raw: "b", Var: bv}},
				}}},
			}},
			Else: ptrBranch,
		},
	}}
	body := &ir.Seq{Stmts: []ir.Stmt{
		&ir.If{
			Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{&ir.Name{Raw: "data", Var: d}}},
			Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{g.stringConstExpr("")}}}},
			Else: elseBody,
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
					// $go.itab.type is non-null; typ is refnull after the guard.
					Args: []ir.Expr{&ir.Instr{
						Op:   ir.OpRefAsNonNull,
						Args: []ir.Expr{&ir.Name{Raw: "typ", Var: typ}},
					}},
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

func (g *gen) fillReflectPackAny() {
	name := "runtime.ReflectPackAny"
	f := g.mod.LookupFunc(name)
	if f == nil || !emptyBody(f) {
		return
	}
	ifaceTy := &ir.Ref{Null: true, Named: "$go.iface"}
	anyT := &ir.Ref{Null: true, Heap: ir.HeapAny}
	i := &ir.Var{Name: "i", Typ: ifaceTy}
	// any and unsafe.Pointer are both anyref; return the iface ref as-is.
	body := &ir.Seq{Stmts: []ir.Stmt{
		&ir.Return{Results: []ir.Expr{&ir.Instr{
			Op: ir.OpRefCast, Type: anyT, Args: []ir.Expr{&ir.Name{Raw: "i", Var: i}},
		}}},
	}}
	g.replaceBody(name, []*ir.Var{i}, []ir.Type{anyT}, body)
}

func (g *gen) fillReflectIfaceFromData() {
	name := "runtime.ReflectIfaceFromData"
	anyT := &ir.Ref{Null: true, Heap: ir.HeapAny}
	ifaceTy := &ir.Ref{Null: true, Named: "$go.iface"}
	data := &ir.Var{Name: "data", Typ: anyT}
	g.ensureBoxRef()
	ifaceRef := &ir.Ref{Named: "$go.iface"}
	boxTy := &ir.Ref{Named: "$go.box.ref"}
	iv := &ir.Var{Name: "iv", Typ: ifaceRef}
	bv := &ir.Var{Name: "bv", Typ: boxTy}
	nullIface := &ir.Instr{Op: ir.OpRefNull, Type: ifaceTy}
	// Soft paths: New(interface{}).Elem() may be $go.box.ref; plain fields are
	// $go.iface. Wrong shapes used to illegal-cast (json CompactBig / Elem).
	body := &ir.Seq{Stmts: []ir.Stmt{
		&ir.VarDef{Var: iv},
		&ir.VarDef{Var: bv},
		&ir.If{
			Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{&ir.Name{Raw: "data", Var: data}}},
			Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{nullIface}}}},
			Else: &ir.Seq{Stmts: []ir.Stmt{
				&ir.If{
					Cond: &ir.Instr{Op: ir.OpRefTest, Type: ifaceRef, Args: []ir.Expr{&ir.Name{Raw: "data", Var: data}}},
					Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{&ir.Instr{
						Op: ir.OpRefCast, Type: ifaceTy, Args: []ir.Expr{&ir.Name{Raw: "data", Var: data}},
					}}}}},
					Else: &ir.Seq{Stmts: []ir.Stmt{
						&ir.If{
							Cond: &ir.Instr{Op: ir.OpRefTest, Type: boxTy, Args: []ir.Expr{&ir.Name{Raw: "data", Var: data}}},
							Then: &ir.Seq{Stmts: []ir.Stmt{
								&ir.Assign{Lhs: []ir.Dest{{Name: "bv", Var: bv}}, Rhs: &ir.Instr{
									Op: ir.OpRefCast, Type: boxTy, Args: []ir.Expr{&ir.Name{Raw: "data", Var: data}},
								}},
								&ir.If{
									Cond: &ir.Instr{Op: ir.OpRefTest, Type: ifaceRef, Args: []ir.Expr{&ir.Instr{
										Op: ir.OpStructGet, Type: boxTy, Field: "v",
										Args: []ir.Expr{&ir.Name{Raw: "bv", Var: bv}},
									}}},
									Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{&ir.Instr{
										Op: ir.OpRefCast, Type: ifaceTy, Args: []ir.Expr{&ir.Instr{
											Op: ir.OpStructGet, Type: boxTy, Field: "v",
											Args: []ir.Expr{&ir.Name{Raw: "bv", Var: bv}},
										}},
									}}}}},
									Else: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{nullIface}}}},
								},
							}},
							Else: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{nullIface}}}},
						},
					}},
				},
			}},
		},
		&ir.Unreachable{},
	}}
	g.replaceBody(name, []*ir.Var{data}, []ir.Type{ifaceTy}, body)
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

func (g *gen) fillReflectCallSetField() {
	name := "runtime.ReflectCallSetField"
	anyT := &ir.Ref{Null: true, Heap: ir.HeapAny}
	extra := &ir.Var{Name: "extra", Typ: anyT}
	data := &ir.Var{Name: "data", Typ: anyT}
	i := &ir.Var{Name: "i", Typ: ir.TypeI32}
	val := &ir.Var{Name: "v", Typ: anyT}
	if g.mod.LookupType("$go.extra.struct") == nil || g.mod.LookupType("$go.fn.setField") == nil {
		g.replaceBody(name, []*ir.Var{extra, data, i, val}, nil, &ir.Seq{Stmts: []ir.Stmt{&ir.Return{}}})
		return
	}
	exTy := &ir.Ref{Named: "$go.extra.struct"}
	fnTy := &ir.Ref{Named: "$go.fn.setField"}
	ex := &ir.Var{Name: "ex", Typ: exTy}
	fn := &ir.Var{Name: "fn", Typ: fnTy}
	body := &ir.Seq{Stmts: []ir.Stmt{
		&ir.VarDef{Var: ex}, &ir.VarDef{Var: fn},
		&ir.If{
			Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{&ir.Name{Raw: "extra", Var: extra}}},
			Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{}}},
			Else: &ir.Seq{Stmts: []ir.Stmt{
				&ir.Assign{Lhs: []ir.Dest{{Name: "ex", Var: ex}}, Rhs: &ir.Instr{
					Op: ir.OpRefCast, Type: exTy, Args: []ir.Expr{&ir.Name{Raw: "extra", Var: extra}},
				}},
				&ir.Assign{Lhs: []ir.Dest{{Name: "fn", Var: fn}}, Rhs: &ir.Instr{
					Op: ir.OpStructGet, Type: exTy, Field: "setField",
					Args: []ir.Expr{&ir.Name{Raw: "ex", Var: ex}},
				}},
				&ir.ExprStmt{X: &ir.Instr{
					Op: ir.OpCallRef, Type: fnTy,
					Args: []ir.Expr{
						&ir.Name{Raw: "data", Var: data},
						&ir.Name{Raw: "i", Var: i},
						&ir.Name{Raw: "v", Var: val},
						&ir.Name{Raw: "fn", Var: fn},
					},
				}},
				&ir.Return{},
			}},
		},
	}}
	g.replaceBody(name, []*ir.Var{extra, data, i, val}, nil, body)
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

func (g *gen) fillReflectCallSetIndex() {
	name := "runtime.ReflectCallSetIndex"
	anyT := &ir.Ref{Null: true, Heap: ir.HeapAny}
	extra := &ir.Var{Name: "extra", Typ: anyT}
	data := &ir.Var{Name: "data", Typ: anyT}
	i := &ir.Var{Name: "i", Typ: ir.TypeI32}
	val := &ir.Var{Name: "v", Typ: anyT}
	if g.mod.LookupType("$go.extra.slice") == nil || g.mod.LookupType("$go.fn.setIndex") == nil {
		g.replaceBody(name, []*ir.Var{extra, data, i, val}, nil, &ir.Seq{Stmts: []ir.Stmt{&ir.Return{}}})
		return
	}
	exTy := &ir.Ref{Named: "$go.extra.slice"}
	fnTy := &ir.Ref{Named: "$go.fn.setIndex"}
	ex := &ir.Var{Name: "ex", Typ: exTy}
	fn := &ir.Var{Name: "fn", Typ: fnTy}
	body := &ir.Seq{Stmts: []ir.Stmt{
		&ir.VarDef{Var: ex}, &ir.VarDef{Var: fn},
		&ir.If{
			Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{&ir.Name{Raw: "extra", Var: extra}}},
			Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{}}},
			Else: &ir.Seq{Stmts: []ir.Stmt{
				&ir.Assign{Lhs: []ir.Dest{{Name: "ex", Var: ex}}, Rhs: &ir.Instr{
					Op: ir.OpRefCast, Type: exTy, Args: []ir.Expr{&ir.Name{Raw: "extra", Var: extra}},
				}},
				&ir.Assign{Lhs: []ir.Dest{{Name: "fn", Var: fn}}, Rhs: &ir.Instr{
					Op: ir.OpStructGet, Type: exTy, Field: "setIndex",
					Args: []ir.Expr{&ir.Name{Raw: "ex", Var: ex}},
				}},
				&ir.ExprStmt{X: &ir.Instr{
					Op: ir.OpCallRef, Type: fnTy,
					Args: []ir.Expr{
						&ir.Name{Raw: "data", Var: data},
						&ir.Name{Raw: "i", Var: i},
						&ir.Name{Raw: "v", Var: val},
						&ir.Name{Raw: "fn", Var: fn},
					},
				}},
				&ir.Return{},
			}},
		},
	}}
	g.replaceBody(name, []*ir.Var{extra, data, i, val}, nil, body)
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

func (g *gen) fillReflectArrayLen() {
	name := "runtime.ReflectArrayLen"
	anyT := &ir.Ref{Null: true, Heap: ir.HeapAny}
	extra := &ir.Var{Name: "extra", Typ: anyT}
	if g.mod.LookupType("$go.extra.slice") == nil {
		g.replaceBody(name, []*ir.Var{extra}, []ir.Type{ir.TypeI32}, &ir.Seq{Stmts: []ir.Stmt{
			&ir.Return{Results: []ir.Expr{&ir.Const{Op: ir.OpI32Const, I64: 0}}},
		}})
		return
	}
	exTy := &ir.Ref{Named: "$go.extra.slice"}
	ex := &ir.Var{Name: "ex", Typ: exTy}
	body := &ir.Seq{Stmts: []ir.Stmt{
		&ir.VarDef{Var: ex},
		&ir.If{
			Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{&ir.Name{Raw: "extra", Var: extra}}},
			Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{
				&ir.Const{Op: ir.OpI32Const, I64: 0},
			}}}},
			Else: &ir.Seq{Stmts: []ir.Stmt{
				&ir.Assign{Lhs: []ir.Dest{{Name: "ex", Var: ex}}, Rhs: &ir.Instr{
					Op: ir.OpRefCast, Type: exTy, Args: []ir.Expr{&ir.Name{Raw: "extra", Var: extra}},
				}},
				&ir.Return{Results: []ir.Expr{&ir.Instr{
					Op: ir.OpStructGet, Type: exTy, Field: "n",
					Args: []ir.Expr{&ir.Name{Raw: "ex", Var: ex}},
				}}},
			}},
		},
		&ir.Unreachable{},
	}}
	g.replaceBody(name, []*ir.Var{extra}, []ir.Type{ir.TypeI32}, body)
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

func (g *gen) fillReflectStructFieldTag() {
	name := "runtime.ReflectStructFieldTag"
	if g.mod.LookupFunc(name) == nil || !emptyBody(g.mod.LookupFunc(name)) {
		return
	}
	anyT := &ir.Ref{Null: true, Heap: ir.HeapAny}
	extra := &ir.Var{Name: "extra", Typ: anyT}
	i := &ir.Var{Name: "i", Typ: ir.TypeI32}
	if g.mod.LookupType("$go.extra.struct") == nil || g.mod.LookupType("$go.fn.fieldTag") == nil {
		g.replaceBody(name, []*ir.Var{extra, i}, []ir.Type{&ir.Ref{Named: "$go.string"}}, &ir.Seq{Stmts: []ir.Stmt{
			&ir.Return{Results: []ir.Expr{g.stringConstExpr("")}},
		}})
		return
	}
	exTy := &ir.Ref{Named: "$go.extra.struct"}
	fnTy := &ir.Ref{Named: "$go.fn.fieldTag"}
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
					Op: ir.OpStructGet, Type: exTy, Field: "fieldTag",
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

// fillReflectSliceOf builds runtime.ReflectSliceOf(t) → descriptor for []T
// via a dense id dispatch over types present after closeUniverse.
func (g *gen) fillReflectSliceOf() {
	g.fillReflectCtorByElemID("runtime.ReflectSliceOf", func(elem *typeRec) *typeRec {
		if elem.goType == nil {
			return nil
		}
		want := TypeKey(types.NewSlice(elem.goType))
		return g.types[want]
	})
}

// fillReflectArrayOf builds runtime.ReflectArrayOf(n, t) → [n]T when present.
func (g *gen) fillReflectArrayOf() {
	name := "runtime.ReflectArrayOf"
	f := g.mod.LookupFunc(name)
	if f == nil || !emptyBody(f) {
		return
	}
	typeTy := &ir.Ref{Null: true, Named: "$go.type"}
	nV := &ir.Var{Name: "n", Typ: ir.TypeI32}
	t := &ir.Var{Name: "t", Typ: typeTy}
	nullT := &ir.Instr{Op: ir.OpRefNull, Type: typeTy}
	idV := &ir.Var{Name: "id", Typ: ir.TypeI32}

	type arm struct {
		id, n  int32
		global string
	}
	var arms []arm
	keys := make([]string, 0, len(g.types))
	for k := range g.types {
		keys = append(keys, k)
	}
	sortStrings(keys)
	for _, k := range keys {
		arr := g.types[k]
		if arr == nil || arr.goType == nil || arr.global == "" {
			continue
		}
		a, ok := types.Unalias(arr.goType).Underlying().(*types.Array)
		if !ok || a.Len() < 0 || a.Len() > 1<<28 {
			continue
		}
		elem := g.types[TypeKey(a.Elem())]
		if elem == nil || elem.id < 0 {
			continue
		}
		arms = append(arms, arm{id: elem.id, n: int32(a.Len()), global: arr.global})
	}

	var chain ir.Stmt = &ir.Return{Results: []ir.Expr{nullT}}
	for i := len(arms) - 1; i >= 0; i-- {
		a := arms[i]
		chain = &ir.If{
			Cond: &ir.Instr{Op: ir.OpI32And, Args: []ir.Expr{
				&ir.Instr{Op: ir.OpI32Eq, Args: []ir.Expr{
					&ir.Name{Raw: "id", Var: idV},
					&ir.Const{Op: ir.OpI32Const, I64: int64(a.id)},
				}},
				&ir.Instr{Op: ir.OpI32Eq, Args: []ir.Expr{
					&ir.Name{Raw: "n", Var: nV},
					&ir.Const{Op: ir.OpI32Const, I64: int64(a.n)},
				}},
			}},
			Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{
				&ir.Instr{Op: ir.OpGlobalGet, Global: a.global},
			}}}},
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
	g.replaceBody(name, []*ir.Var{nV, t}, []ir.Type{typeTy}, body)
}

func (g *gen) fillReflectPointerTo() {
	g.fillReflectCtorByElemID("runtime.ReflectPointerTo", func(elem *typeRec) *typeRec {
		if elem.goType == nil {
			return nil
		}
		want := TypeKey(types.NewPointer(elem.goType))
		return g.types[want]
	})
}

// fillReflectImplements returns true when an itab was emitted for (concrete, iface).
func (g *gen) fillReflectImplements() {
	name := "runtime.ReflectImplements"
	f := g.mod.LookupFunc(name)
	if f == nil || !emptyBody(f) {
		return
	}
	typeTy := &ir.Ref{Null: true, Named: "$go.type"}
	t := &ir.Var{Name: "t", Typ: typeTy}
	u := &ir.Var{Name: "u", Typ: typeTy}
	tid := &ir.Var{Name: "tid", Typ: ir.TypeI32}
	uid := &ir.Var{Name: "uid", Typ: ir.TypeI32}
	falseC := &ir.Const{Op: ir.OpI32Const, I64: 0}
	trueC := &ir.Const{Op: ir.OpI32Const, I64: 1}

	type pair struct{ tid, uid int32 }
	seen := map[pair]bool{}
	var pairs []pair
	for _, it := range g.itabs {
		if it == nil {
			continue
		}
		ct := g.types[it.concKey]
		itIface := g.types[it.ifaceKey]
		if ct == nil || itIface == nil || ct.id < 0 || itIface.id < 0 {
			continue
		}
		p := pair{tid: ct.id, uid: itIface.id}
		if seen[p] {
			continue
		}
		seen[p] = true
		pairs = append(pairs, p)
	}
	// Stable order for deterministic IR.
	for i := 0; i < len(pairs); i++ {
		for j := i + 1; j < len(pairs); j++ {
			if pairs[j].tid < pairs[i].tid || (pairs[j].tid == pairs[i].tid && pairs[j].uid < pairs[i].uid) {
				pairs[i], pairs[j] = pairs[j], pairs[i]
			}
		}
	}

	checks := make([]ir.Stmt, 0, len(pairs)+1)
	for _, p := range pairs {
		p := p
		checks = append(checks, &ir.If{
			Cond: &ir.Instr{Op: ir.OpI32And, Args: []ir.Expr{
				&ir.Instr{Op: ir.OpI32Eq, Args: []ir.Expr{
					&ir.Name{Raw: "tid", Var: tid},
					&ir.Const{Op: ir.OpI32Const, I64: int64(p.tid)},
				}},
				&ir.Instr{Op: ir.OpI32Eq, Args: []ir.Expr{
					&ir.Name{Raw: "uid", Var: uid},
					&ir.Const{Op: ir.OpI32Const, I64: int64(p.uid)},
				}},
			}},
			Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{trueC}}}},
			Else: &ir.Seq{},
		})
	}
	checks = append(checks, &ir.Return{Results: []ir.Expr{falseC}})

	body := &ir.Seq{Stmts: []ir.Stmt{
		&ir.VarDef{Var: tid}, &ir.VarDef{Var: uid},
		&ir.If{
			Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{&ir.Name{Raw: "t", Var: t}}},
			Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{falseC}}}},
			Else: &ir.Seq{Stmts: []ir.Stmt{
				&ir.If{
					Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{&ir.Name{Raw: "u", Var: u}}},
					Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{falseC}}}},
					Else: &ir.Seq{Stmts: []ir.Stmt{
						&ir.Assign{Lhs: []ir.Dest{{Name: "tid", Var: tid}}, Rhs: &ir.Instr{
							Op: ir.OpStructGet, Type: &ir.Ref{Named: "$go.type"}, Field: "id",
							Args: []ir.Expr{&ir.Name{Raw: "t", Var: t}},
						}},
						&ir.Assign{Lhs: []ir.Dest{{Name: "uid", Var: uid}}, Rhs: &ir.Instr{
							Op: ir.OpStructGet, Type: &ir.Ref{Named: "$go.type"}, Field: "id",
							Args: []ir.Expr{&ir.Name{Raw: "u", Var: u}},
						}},
						&ir.Seq{Stmts: checks},
					}},
				},
			}},
		},
		&ir.Unreachable{},
	}}
	g.replaceBody(name, []*ir.Var{t, u}, []ir.Type{ir.TypeI32}, body)
}

func (g *gen) fillReflectMapOfEmpty() {
	empty := types.NewStruct(nil, nil)
	g.fillReflectCtorByElemID("runtime.ReflectMapOfEmpty", func(elem *typeRec) *typeRec {
		if elem.goType == nil || !types.Comparable(elem.goType) {
			return nil
		}
		want := TypeKey(types.NewMap(elem.goType, empty))
		return g.types[want]
	})
}

// fillReflectCtorByElemID replaces a body-less runtime helper that maps an
// element/key descriptor to a constructed type descriptor by dense id.
func (g *gen) fillReflectCtorByElemID(name string, lookup func(*typeRec) *typeRec) {
	f := g.mod.LookupFunc(name)
	if f == nil || !emptyBody(f) {
		return
	}
	typeTy := &ir.Ref{Null: true, Named: "$go.type"}
	t := &ir.Var{Name: "t", Typ: typeTy}
	nullT := &ir.Instr{Op: ir.OpRefNull, Type: typeTy}
	idV := &ir.Var{Name: "id", Typ: ir.TypeI32}

	// Build id → result global mapping from known descriptors.
	type arm struct {
		id     int32
		global string
	}
	var arms []arm
	keys := make([]string, 0, len(g.types))
	for k := range g.types {
		keys = append(keys, k)
	}
	sortStrings(keys)
	for _, k := range keys {
		elem := g.types[k]
		if elem == nil || elem.id < 0 {
			continue
		}
		res := lookup(elem)
		if res == nil || res.global == "" {
			continue
		}
		arms = append(arms, arm{id: elem.id, global: res.global})
	}

	var chain ir.Stmt = &ir.Return{Results: []ir.Expr{nullT}}
	for i := len(arms) - 1; i >= 0; i-- {
		a := arms[i]
		chain = &ir.If{
			Cond: &ir.Instr{Op: ir.OpI32Eq, Args: []ir.Expr{
				&ir.Name{Raw: "id", Var: idV},
				&ir.Const{Op: ir.OpI32Const, I64: int64(a.id)},
			}},
			Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{
				&ir.Instr{Op: ir.OpGlobalGet, Global: a.global},
			}}}},
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
	g.replaceBody(name, []*ir.Var{t}, []ir.Type{typeTy}, body)
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

func (g *gen) ensureComplexTypes() {
	g.mod.InternType(&ir.TypeDef{
		Name: "$go.complex64", Kind: ir.TypeStruct, Super: "$go.object",
		Fields: []ir.Field{
			{Name: "real", Type: ir.TypeF32, Mut: true},
			{Name: "imag", Type: ir.TypeF32, Mut: true},
		},
	})
	g.mod.InternType(&ir.TypeDef{
		Name: "$go.complex128", Kind: ir.TypeStruct, Super: "$go.object",
		Fields: []ir.Field{
			{Name: "real", Type: ir.TypeF64, Mut: true},
			{Name: "imag", Type: ir.TypeF64, Mut: true},
		},
	})
}

func (g *gen) fillReflectComplex64() {
	name := "runtime.ReflectComplex64"
	f := g.mod.LookupFunc(name)
	if f == nil || !emptyBody(f) {
		return
	}
	g.ensureComplexTypes()
	anyT := &ir.Ref{Null: true, Heap: ir.HeapAny}
	c64 := &ir.Ref{Named: "$go.complex64"}
	c128 := &ir.Ref{Null: true, Named: "$go.complex128"}
	data := &ir.Var{Name: "data", Typ: anyT}
	c := &ir.Var{Name: "c", Typ: c64}
	zero := &ir.Instr{Op: ir.OpStructNew, Type: c128, Args: []ir.Expr{
		&ir.Const{Op: ir.OpF64Const, F64: 0},
		&ir.Const{Op: ir.OpF64Const, F64: 0},
	}}
	body := &ir.Seq{Stmts: []ir.Stmt{
		&ir.VarDef{Var: c},
		&ir.If{
			Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{&ir.Name{Raw: "data", Var: data}}},
			Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{zero}}}},
			Else: &ir.Seq{Stmts: []ir.Stmt{
				&ir.If{
					Cond: &ir.Instr{Op: ir.OpRefTest, Type: c64, Args: []ir.Expr{&ir.Name{Raw: "data", Var: data}}},
					Then: &ir.Seq{Stmts: []ir.Stmt{
						&ir.Assign{Lhs: []ir.Dest{{Name: "c", Var: c}}, Rhs: &ir.Instr{
							Op: ir.OpRefCast, Type: c64, Args: []ir.Expr{&ir.Name{Raw: "data", Var: data}},
						}},
						&ir.Return{Results: []ir.Expr{&ir.Instr{
							Op: ir.OpStructNew, Type: c128,
							Args: []ir.Expr{
								&ir.Instr{Op: ir.OpF64PromoteF32, Args: []ir.Expr{&ir.Instr{
									Op: ir.OpStructGet, Type: c64, Field: "real",
									Args: []ir.Expr{&ir.Name{Raw: "c", Var: c}},
								}}},
								&ir.Instr{Op: ir.OpF64PromoteF32, Args: []ir.Expr{&ir.Instr{
									Op: ir.OpStructGet, Type: c64, Field: "imag",
									Args: []ir.Expr{&ir.Name{Raw: "c", Var: c}},
								}}},
							},
						}}},
					}},
					Else: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{zero}}}},
				},
			}},
		},
		&ir.Unreachable{},
	}}
	g.replaceBody(name, []*ir.Var{data}, []ir.Type{c128}, body)
}

func (g *gen) fillReflectComplex128() {
	name := "runtime.ReflectComplex128"
	f := g.mod.LookupFunc(name)
	if f == nil || !emptyBody(f) {
		return
	}
	g.ensureComplexTypes()
	anyT := &ir.Ref{Null: true, Heap: ir.HeapAny}
	c128 := &ir.Ref{Null: true, Named: "$go.complex128"}
	c64 := &ir.Ref{Named: "$go.complex64"}
	data := &ir.Var{Name: "data", Typ: anyT}
	c := &ir.Var{Name: "c", Typ: c64}
	zero := &ir.Instr{Op: ir.OpStructNew, Type: c128, Args: []ir.Expr{
		&ir.Const{Op: ir.OpF64Const, F64: 0},
		&ir.Const{Op: ir.OpF64Const, F64: 0},
	}}
	// Prefer ref.test: DeepEqual/Scan may pass complex64 data under a
	// Complex128 kind, or a non-complex leftover — never trap.
	body := &ir.Seq{Stmts: []ir.Stmt{
		&ir.VarDef{Var: c},
		&ir.If{
			Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{&ir.Name{Raw: "data", Var: data}}},
			Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{zero}}}},
			Else: &ir.Seq{Stmts: []ir.Stmt{
				&ir.If{
					Cond: &ir.Instr{Op: ir.OpRefTest, Type: c128, Args: []ir.Expr{&ir.Name{Raw: "data", Var: data}}},
					Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{&ir.Instr{
						Op: ir.OpRefCast, Type: c128, Args: []ir.Expr{&ir.Name{Raw: "data", Var: data}},
					}}}}},
					Else: &ir.Seq{Stmts: []ir.Stmt{
						&ir.If{
							Cond: &ir.Instr{Op: ir.OpRefTest, Type: c64, Args: []ir.Expr{&ir.Name{Raw: "data", Var: data}}},
							Then: &ir.Seq{Stmts: []ir.Stmt{
								&ir.Assign{Lhs: []ir.Dest{{Name: "c", Var: c}}, Rhs: &ir.Instr{
									Op: ir.OpRefCast, Type: c64, Args: []ir.Expr{&ir.Name{Raw: "data", Var: data}},
								}},
								&ir.Return{Results: []ir.Expr{&ir.Instr{
									Op: ir.OpStructNew, Type: c128,
									Args: []ir.Expr{
										&ir.Instr{Op: ir.OpF64PromoteF32, Args: []ir.Expr{&ir.Instr{
											Op: ir.OpStructGet, Type: c64, Field: "real",
											Args: []ir.Expr{&ir.Name{Raw: "c", Var: c}},
										}}},
										&ir.Instr{Op: ir.OpF64PromoteF32, Args: []ir.Expr{&ir.Instr{
											Op: ir.OpStructGet, Type: c64, Field: "imag",
											Args: []ir.Expr{&ir.Name{Raw: "c", Var: c}},
										}}},
									},
								}}},
							}},
							Else: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{zero}}}},
						},
					}},
				},
			}},
		},
		&ir.Unreachable{},
	}}
	g.replaceBody(name, []*ir.Var{data}, []ir.Type{c128}, body)
}

// fillReflectSetComplex mutates dst's real/imag from src. Both args are
// $go.complex64 or $go.complex128 refs (New().Elem() addressable slots).
func (g *gen) fillReflectSetComplex() {
	name := "runtime.ReflectSetComplex"
	f := g.mod.LookupFunc(name)
	if f == nil || !emptyBody(f) {
		return
	}
	g.ensureComplexTypes()
	anyT := &ir.Ref{Null: true, Heap: ir.HeapAny}
	c128 := &ir.Ref{Named: "$go.complex128"}
	c64 := &ir.Ref{Named: "$go.complex64"}
	dst := &ir.Var{Name: "dst", Typ: anyT}
	src := &ir.Var{Name: "src", Typ: anyT}
	d128 := &ir.Var{Name: "d128", Typ: c128}
	s128 := &ir.Var{Name: "s128", Typ: c128}
	d64 := &ir.Var{Name: "d64", Typ: c64}
	s64 := &ir.Var{Name: "s64", Typ: c64}
	body := &ir.Seq{Stmts: []ir.Stmt{
		&ir.VarDef{Var: d128},
		&ir.VarDef{Var: s128},
		&ir.VarDef{Var: d64},
		&ir.VarDef{Var: s64},
		&ir.If{
			Cond: &ir.Instr{Op: ir.OpI32Or, Args: []ir.Expr{
				&ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{&ir.Name{Raw: "dst", Var: dst}}},
				&ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{&ir.Name{Raw: "src", Var: src}}},
			}},
			Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{}}},
			Else: &ir.Seq{Stmts: []ir.Stmt{
				&ir.If{
					Cond: &ir.Instr{Op: ir.OpRefTest, Type: c128, Args: []ir.Expr{&ir.Name{Raw: "dst", Var: dst}}},
					Then: &ir.Seq{Stmts: []ir.Stmt{
						&ir.Assign{Lhs: []ir.Dest{{Name: "d128", Var: d128}}, Rhs: &ir.Instr{
							Op: ir.OpRefCast, Type: c128, Args: []ir.Expr{&ir.Name{Raw: "dst", Var: dst}},
						}},
						&ir.If{
							Cond: &ir.Instr{Op: ir.OpRefTest, Type: c128, Args: []ir.Expr{&ir.Name{Raw: "src", Var: src}}},
							Then: &ir.Seq{Stmts: []ir.Stmt{
								&ir.Assign{Lhs: []ir.Dest{{Name: "s128", Var: s128}}, Rhs: &ir.Instr{
									Op: ir.OpRefCast, Type: c128, Args: []ir.Expr{&ir.Name{Raw: "src", Var: src}},
								}},
								&ir.ExprStmt{X: &ir.Instr{
									Op: ir.OpStructSet, Type: c128, Field: "real",
									Args: []ir.Expr{
										&ir.Name{Raw: "d128", Var: d128},
										&ir.Instr{Op: ir.OpStructGet, Type: c128, Field: "real",
											Args: []ir.Expr{&ir.Name{Raw: "s128", Var: s128}}},
									},
								}},
								&ir.ExprStmt{X: &ir.Instr{
									Op: ir.OpStructSet, Type: c128, Field: "imag",
									Args: []ir.Expr{
										&ir.Name{Raw: "d128", Var: d128},
										&ir.Instr{Op: ir.OpStructGet, Type: c128, Field: "imag",
											Args: []ir.Expr{&ir.Name{Raw: "s128", Var: s128}}},
									},
								}},
								&ir.Return{},
							}},
							Else: &ir.Seq{Stmts: []ir.Stmt{
								&ir.If{
									Cond: &ir.Instr{Op: ir.OpRefTest, Type: c64, Args: []ir.Expr{&ir.Name{Raw: "src", Var: src}}},
									Then: &ir.Seq{Stmts: []ir.Stmt{
										&ir.Assign{Lhs: []ir.Dest{{Name: "s64", Var: s64}}, Rhs: &ir.Instr{
											Op: ir.OpRefCast, Type: c64, Args: []ir.Expr{&ir.Name{Raw: "src", Var: src}},
										}},
										&ir.ExprStmt{X: &ir.Instr{
											Op: ir.OpStructSet, Type: c128, Field: "real",
											Args: []ir.Expr{
												&ir.Name{Raw: "d128", Var: d128},
												&ir.Instr{Op: ir.OpF64PromoteF32, Args: []ir.Expr{&ir.Instr{
													Op: ir.OpStructGet, Type: c64, Field: "real",
													Args: []ir.Expr{&ir.Name{Raw: "s64", Var: s64}},
												}}},
											},
										}},
										&ir.ExprStmt{X: &ir.Instr{
											Op: ir.OpStructSet, Type: c128, Field: "imag",
											Args: []ir.Expr{
												&ir.Name{Raw: "d128", Var: d128},
												&ir.Instr{Op: ir.OpF64PromoteF32, Args: []ir.Expr{&ir.Instr{
													Op: ir.OpStructGet, Type: c64, Field: "imag",
													Args: []ir.Expr{&ir.Name{Raw: "s64", Var: s64}},
												}}},
											},
										}},
										&ir.Return{},
									}},
									Else: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{}}},
								},
							}},
						},
					}},
					Else: &ir.Seq{Stmts: []ir.Stmt{
						&ir.If{
							Cond: &ir.Instr{Op: ir.OpRefTest, Type: c64, Args: []ir.Expr{&ir.Name{Raw: "dst", Var: dst}}},
							Then: &ir.Seq{Stmts: []ir.Stmt{
								&ir.Assign{Lhs: []ir.Dest{{Name: "d64", Var: d64}}, Rhs: &ir.Instr{
									Op: ir.OpRefCast, Type: c64, Args: []ir.Expr{&ir.Name{Raw: "dst", Var: dst}},
								}},
								&ir.If{
									Cond: &ir.Instr{Op: ir.OpRefTest, Type: c64, Args: []ir.Expr{&ir.Name{Raw: "src", Var: src}}},
									Then: &ir.Seq{Stmts: []ir.Stmt{
										&ir.Assign{Lhs: []ir.Dest{{Name: "s64", Var: s64}}, Rhs: &ir.Instr{
											Op: ir.OpRefCast, Type: c64, Args: []ir.Expr{&ir.Name{Raw: "src", Var: src}},
										}},
										&ir.ExprStmt{X: &ir.Instr{
											Op: ir.OpStructSet, Type: c64, Field: "real",
											Args: []ir.Expr{
												&ir.Name{Raw: "d64", Var: d64},
												&ir.Instr{Op: ir.OpStructGet, Type: c64, Field: "real",
													Args: []ir.Expr{&ir.Name{Raw: "s64", Var: s64}}},
											},
										}},
										&ir.ExprStmt{X: &ir.Instr{
											Op: ir.OpStructSet, Type: c64, Field: "imag",
											Args: []ir.Expr{
												&ir.Name{Raw: "d64", Var: d64},
												&ir.Instr{Op: ir.OpStructGet, Type: c64, Field: "imag",
													Args: []ir.Expr{&ir.Name{Raw: "s64", Var: s64}}},
											},
										}},
										&ir.Return{},
									}},
									Else: &ir.Seq{Stmts: []ir.Stmt{
										&ir.If{
											Cond: &ir.Instr{Op: ir.OpRefTest, Type: c128, Args: []ir.Expr{&ir.Name{Raw: "src", Var: src}}},
											Then: &ir.Seq{Stmts: []ir.Stmt{
												&ir.Assign{Lhs: []ir.Dest{{Name: "s128", Var: s128}}, Rhs: &ir.Instr{
													Op: ir.OpRefCast, Type: c128, Args: []ir.Expr{&ir.Name{Raw: "src", Var: src}},
												}},
												&ir.ExprStmt{X: &ir.Instr{
													Op: ir.OpStructSet, Type: c64, Field: "real",
													Args: []ir.Expr{
														&ir.Name{Raw: "d64", Var: d64},
														&ir.Instr{Op: ir.OpF32DemoteF64, Args: []ir.Expr{&ir.Instr{
															Op: ir.OpStructGet, Type: c128, Field: "real",
															Args: []ir.Expr{&ir.Name{Raw: "s128", Var: s128}},
														}}},
													},
												}},
												&ir.ExprStmt{X: &ir.Instr{
													Op: ir.OpStructSet, Type: c64, Field: "imag",
													Args: []ir.Expr{
														&ir.Name{Raw: "d64", Var: d64},
														&ir.Instr{Op: ir.OpF32DemoteF64, Args: []ir.Expr{&ir.Instr{
															Op: ir.OpStructGet, Type: c128, Field: "imag",
															Args: []ir.Expr{&ir.Name{Raw: "s128", Var: s128}},
														}}},
													},
												}},
												&ir.Return{},
											}},
											Else: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{}}},
										},
									}},
								},
							}},
							Else: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{}}},
						},
					}},
				},
			}},
		},
		&ir.Return{},
	}}
	g.replaceBody(name, []*ir.Var{dst, src}, nil, body)
}

func (g *gen) fillReflectUnbox(name, box string, res ir.Type) {
	anyT := &ir.Ref{Null: true, Heap: ir.HeapAny}
	data := &ir.Var{Name: "data", Typ: anyT}
	zero := ir.Expr(&ir.Const{Op: ir.OpI32Const, I64: 0})
	kind := "i32"
	switch res {
	case ir.TypeI64:
		zero = &ir.Const{Op: ir.OpI64Const, I64: 0}
		kind = "i64"
	case ir.TypeF32:
		zero = &ir.Const{Op: ir.OpF32Const, F64: 0}
		kind = "f32"
	case ir.TypeF64:
		zero = &ir.Const{Op: ir.OpF64Const, F64: 0}
		kind = "f64"
	}
	g.ensureFatPtrScalarTypes()
	boxTy := &ir.Ref{Named: box}
	ptrTy := &ir.Ref{Named: "$go.ptr." + kind}
	classTy := &ir.Ref{Named: "$go.class." + kind}
	b := &ir.Var{Name: "b", Typ: boxTy}
	p := &ir.Var{Name: "p", Typ: ptrTy}
	cl := &ir.Var{Name: "cl", Typ: classTy}
	// New().Elem() stores $go.box.*; ValueOf(&local).Elem() stores $go.ptr.*.
	body := &ir.Seq{Stmts: []ir.Stmt{
		&ir.VarDef{Var: b},
		&ir.VarDef{Var: p},
		&ir.VarDef{Var: cl},
		&ir.If{
			Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{&ir.Name{Raw: "data", Var: data}}},
			Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{zero}}}},
			Else: &ir.Seq{Stmts: []ir.Stmt{
				&ir.If{
					Cond: &ir.Instr{Op: ir.OpRefTest, Type: boxTy, Args: []ir.Expr{&ir.Name{Raw: "data", Var: data}}},
					Then: &ir.Seq{Stmts: []ir.Stmt{
						&ir.Assign{Lhs: []ir.Dest{{Name: "b", Var: b}}, Rhs: &ir.Instr{
							Op: ir.OpRefCast, Type: boxTy, Args: []ir.Expr{&ir.Name{Raw: "data", Var: data}},
						}},
						&ir.Return{Results: []ir.Expr{&ir.Instr{
							Op: ir.OpStructGet, Type: boxTy, Field: "v",
							Args: []ir.Expr{&ir.Name{Raw: "b", Var: b}},
						}}},
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
								&ir.Return{Results: []ir.Expr{&ir.Instr{
									Op: ir.OpCallRef, Type: &ir.Ref{Named: "$go.get." + kind},
									Args: []ir.Expr{
										&ir.Instr{Op: ir.OpStructGet, Type: ptrTy, Field: "base", Args: []ir.Expr{&ir.Name{Raw: "p", Var: p}}},
										&ir.Instr{Op: ir.OpStructGet, Type: ptrTy, Field: "index", Args: []ir.Expr{&ir.Name{Raw: "p", Var: p}}},
										&ir.Instr{Op: ir.OpStructGet, Type: classTy, Field: "get", Args: []ir.Expr{&ir.Name{Raw: "cl", Var: cl}}},
									},
								}}},
							}},
							Else: &ir.Seq{Stmts: []ir.Stmt{&ir.Return{Results: []ir.Expr{zero}}}},
						},
					}},
				},
			}},
		},
		&ir.Unreachable{},
	}}
	g.replaceBody(name, []*ir.Var{data}, []ir.Type{res}, body)
}
