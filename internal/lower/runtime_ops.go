package lower

import (
	"fmt"
	"math"
	"strings"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
)

// RuntimeOps rewrites high-level ops into the low form codegen accepts and
// sets mod.Low.
//
// Bounds failures call runtime.panicIndex(x, y); growslice overflow calls
// runtime.panicSliceB. After M5 those throw recoverable panics via EH.
func RuntimeOps(mod *ir.Module) error {
	if mod == nil {
		return fmt.Errorf("lower.RuntimeOps: nil module")
	}
	// Idempotent: may run again after a later Ensure* AppendFiles introduces
	// new high ops (e.g. panicTypeAssert link after iface asserts expand).
	rt := &runtimeLower{mod: mod, classes: map[string]string{}, accessors: map[string]bool{}}
	n := 0
	for _, f := range mod.Funcs {
		if f.Body == nil {
			continue
		}
		body, err := rt.lowerRegion(f.Body, &n)
		if err != nil {
			return err
		}
		f.Body = body
	}
	mod.Low = true
	return nil
}

type runtimeLower struct {
	mod       *ir.Module
	classes   map[string]string // key → global name
	accessors map[string]bool   // func name declared
	tmp       int
}

func (rt *runtimeLower) fresh(prefix string) string {
	rt.tmp++
	return fmt.Sprintf("%%%s%d", prefix, rt.tmp)
}

func (rt *runtimeLower) freshVar(prefix string, typ ir.Type) *ir.Var {
	return &ir.Var{Name: rt.fresh(prefix), Typ: typ}
}

func (rt *runtimeLower) useVar(v *ir.Var) *ir.Name {
	return &ir.Name{Raw: v.Name, Var: v}
}

// panicIndex emits call runtime.panicIndex(idx, len) then unreachable.
func (rt *runtimeLower) panicIndex(idx, length ir.Expr) []ir.Stmt {
	return []ir.Stmt{
		&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: "runtime.panicIndex", Args: []ir.Expr{idx, length}}},
		&ir.Unreachable{},
	}
}

// panicSliceB emits call runtime.panicSliceB then unreachable.
func (rt *runtimeLower) panicSliceB() []ir.Stmt {
	return []ir.Stmt{
		&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: "runtime.panicSliceB"}},
		&ir.Unreachable{},
	}
}

// forceI32Expr wraps i64 make/append lengths down to i32 (WasmGC int width).
func forceI32Expr(ex ir.Expr) ir.Expr {
	switch x := ex.(type) {
	case *ir.Const:
		if x.Op == ir.OpI64Const {
			return &ir.Const{Op: ir.OpI32Const, I64: int64(int32(x.I64))}
		}
	case *ir.Name:
		var ty ir.Type
		if x.Temp != nil {
			ty = x.Temp.Typ
		} else if x.Var != nil {
			ty = x.Var.Typ
		}
		if ty == ir.TypeI64 {
			return &ir.Instr{Op: ir.OpI32WrapI64, Args: []ir.Expr{ex}}
		}
	case *ir.Instr:
		if x.Type == ir.TypeI64 || x.Op == ir.OpI64Const ||
			x.Op == ir.OpI64Add || x.Op == ir.OpI64Sub || x.Op == ir.OpI64Mul {
			return &ir.Instr{Op: ir.OpI32WrapI64, Args: []ir.Expr{ex}}
		}
	}
	return ex
}

// newVar allocates a mutable local name for values that cross region boundaries.
// Names are $-prefixed so they cannot collide with Go locals uniquified as
// h1/h2/… in nested blocks (decl.go).
func (rt *runtimeLower) newVar(prefix string, typ ir.Type) *ir.Var {
	rt.tmp++
	// Complex values are always (ref null $go.complex*); non-null temps reject
	// zero/nil assigns (ken/cplx5, gcgort).
	if r, ok := typ.(*ir.Ref); ok && !r.Null &&
		(r.Named == "$go.complex64" || r.Named == "$go.complex128") {
		typ = &ir.Ref{Null: true, Named: r.Named}
	}
	return &ir.Var{Name: fmt.Sprintf("$rt.%s%d", prefix, rt.tmp), Typ: typ}
}

func assignV(v *ir.Var, rhs ir.Expr) *ir.Assign {
	return &ir.Assign{Lhs: []ir.Dest{{Var: v, Name: v.Name}}, Rhs: rhs}
}

func useV(v *ir.Var) *ir.Name {
	return &ir.Name{Raw: v.Name, Var: v}
}

func (rt *runtimeLower) lowerRegion(r ir.Region, n *int) (ir.Region, error) {
	switch r := r.(type) {
	case *ir.Seq:
		var out []ir.Stmt
		for _, s := range r.Stmts {
			stmts, err := rt.lowerStmt(s, n)
			if err != nil {
				return nil, err
			}
			out = append(out, stmts...)
		}
		return &ir.Seq{Stmts: out, Pos: r.Pos}, nil
	case *ir.Block:
		body, err := rt.lowerRegion(r.Body, n)
		if err != nil {
			return nil, err
		}
		rr := *r
		rr.Body = body
		return &rr, nil
	case *ir.Loop:
		body, err := rt.lowerRegion(r.Body, n)
		if err != nil {
			return nil, err
		}
		rr := *r
		rr.Body = body
		return &rr, nil
	case *ir.If:
		// Cond before Then/Else so preamble stmts dominate the branch and any
		// shared expr trees are expanded once in order.
		cond, pre, err := rt.rewriteExpr(r.Cond, n)
		if err != nil {
			return nil, err
		}
		then, err := rt.lowerRegion(r.Then, n)
		if err != nil {
			return nil, err
		}
		var elseR ir.Region
		if r.Else != nil {
			elseR, err = rt.lowerRegion(r.Else, n)
			if err != nil {
				return nil, err
			}
		}
		iff := &ir.If{Cond: cond, Then: then, Else: elseR, Pos: r.Pos}
		if len(pre) == 0 {
			return iff, nil
		}
		return &ir.Seq{Stmts: append(pre, iff)}, nil
	case *ir.Switch:
		rr := *r
		tag, pre, err := rt.rewriteExpr(r.Tag, n)
		if err != nil {
			return nil, err
		}
		rr.Tag = tag
		rr.Cases = append([]ir.Case(nil), r.Cases...)
		for i, c := range rr.Cases {
			body, err := rt.lowerRegion(c.Body, n)
			if err != nil {
				return nil, err
			}
			rr.Cases[i].Body = body
		}
		if r.Default != nil {
			d, err := rt.lowerRegion(r.Default, n)
			if err != nil {
				return nil, err
			}
			rr.Default = d
		}
		if len(pre) == 0 {
			return &rr, nil
		}
		return &ir.Seq{Stmts: append(pre, &rr)}, nil
	case *ir.Try:
		body, err := rt.lowerRegion(r.Body, n)
		if err != nil {
			return nil, err
		}
		handler, err := rt.lowerRegion(r.Handler, n)
		if err != nil {
			return nil, err
		}
		rr := *r
		rr.Body = body
		rr.Handler = handler
		return &rr, nil
	case *ir.Dispatch:
		rr := *r
		rr.Arms = make([]ir.Region, len(r.Arms))
		for i, arm := range r.Arms {
			if arm == nil {
				continue
			}
			body, err := rt.lowerRegion(arm, n)
			if err != nil {
				return nil, err
			}
			rr.Arms[i] = body
		}
		return &rr, nil
	default:
		return r, nil
	}
}

func (rt *runtimeLower) lowerStmt(s ir.Stmt, n *int) ([]ir.Stmt, error) {
	switch s := s.(type) {
	case ir.Region:
		r, err := rt.lowerRegion(s, n)
		if err != nil {
			return nil, err
		}
		return []ir.Stmt{r}, nil
	case *ir.Assign:
		if c, ok := s.Rhs.(*ir.Const); ok && c.Op == ir.OpStringLit {
			return expandStringAssign(rt.mod, s, c.Str, n)
		}
		if in, ok := s.Rhs.(*ir.Instr); ok {
			if in.Op == ir.OpStringLit {
				return expandStringAssign(rt.mod, s, in.Str, n)
			}
			if in.Op.IsHigh() {
				return rt.expandHighAssign(s, in, n)
			}
		}
		rhs, pre, err := rt.rewriteExpr(s.Rhs, n)
		if err != nil {
			return nil, err
		}
		s.Rhs = rhs
		return append(pre, s), nil
	case *ir.ExprStmt:
		if in, ok := s.X.(*ir.Instr); ok && in.Op.IsHigh() {
			return rt.expandHighExpr(in, n)
		}
		x, pre, err := rt.rewriteExpr(s.X, n)
		if err != nil {
			return nil, err
		}
		s.X = x
		return append(pre, s), nil
	case *ir.Return:
		var pre []ir.Stmt
		for i, e := range s.Results {
			ex, p, err := rt.rewriteExpr(e, n)
			if err != nil {
				return nil, err
			}
			pre = append(pre, p...)
			s.Results[i] = ex
		}
		return append(pre, s), nil
	case *ir.Br:
		if s.Cond != nil {
			c, pre, err := rt.rewriteExpr(s.Cond, n)
			if err != nil {
				return nil, err
			}
			s.Cond = c
			return append(pre, s), nil
		}
		return []ir.Stmt{s}, nil
	default:
		return []ir.Stmt{s}, nil
	}
}

func (rt *runtimeLower) rewriteExpr(e ir.Expr, n *int) (ir.Expr, []ir.Stmt, error) {
	if e == nil {
		return nil, nil, nil
	}
	switch e := e.(type) {
	case *ir.Const:
		if e.Op == ir.OpStringLit {
			t := &ir.Temp{Typ: &ir.Ref{Named: "$go.string"}}
			t.Name = fmt.Sprintf("%%str_%p", t)
			*n++
			assign := &ir.Assign{Lhs: []ir.Dest{{Temp: t, Name: t.Name}}, Rhs: e}
			stmts, err := expandStringAssign(rt.mod, assign, e.Str, n)
			if err != nil {
				return nil, nil, err
			}
			return &ir.Name{Raw: t.Name, Temp: t}, stmts, nil
		}
		return e, nil, nil
	case *ir.Instr:
		if e.Op == ir.OpStringLit {
			t := &ir.Temp{Typ: &ir.Ref{Named: "$go.string"}}
			t.Name = fmt.Sprintf("%%str_%p", t)
			*n++
			assign := &ir.Assign{Lhs: []ir.Dest{{Temp: t, Name: t.Name}}, Rhs: e}
			stmts, err := expandStringAssign(rt.mod, assign, e.Str, n)
			if err != nil {
				return nil, nil, err
			}
			return &ir.Name{Raw: t.Name, Temp: t}, stmts, nil
		}
		if e.Op.IsHigh() {
			ty := e.Type
			if ty == nil {
				switch e.Op {
				case ir.OpAddr:
					ty = &ir.Ref{Null: true, Named: "$go.ptr.i32"}
				case ir.OpMakeSlice, ir.OpSliceAppend, ir.OpSliceSlice, ir.OpMapMake:
					ty = &ir.Ref{Null: true, Heap: ir.HeapAny}
				default:
					ty = ir.TypeI32
				}
			}
			// Use a Var, not a Temp: expanders (append, etc.) often assign the
			// result inside both sides of an if, and temps cannot cross regions.
			v := rt.newVar("h", ty)
			assign := &ir.Assign{Lhs: []ir.Dest{{Var: v, Name: v.Name}}, Rhs: e}
			stmts, err := rt.expandHighAssign(assign, e, n)
			if err != nil {
				return nil, nil, err
			}
			out := []ir.Stmt{&ir.VarDef{Var: v}}
			out = append(out, stmts...)
			return &ir.Name{Raw: v.Name, Var: v}, out, nil
		}
		var pre []ir.Stmt
		for i, a := range e.Args {
			ex, p, err := rt.rewriteExpr(a, n)
			if err != nil {
				return nil, nil, err
			}
			pre = append(pre, p...)
			e.Args[i] = ex
		}
		return e, pre, nil
	default:
		return e, nil, nil
	}
}

func (rt *runtimeLower) expandHighAssign(s *ir.Assign, in *ir.Instr, n *int) ([]ir.Stmt, error) {
	switch in.Op {
	case ir.OpMakeSlice:
		return rt.expandMakeSlice(s, in, n)
	case ir.OpSliceAppend:
		return rt.expandAppend(s, in, n)
	case ir.OpSliceSlice:
		return rt.expandSliceSlice(s, in, n)
	case ir.OpSliceGet:
		return rt.expandSliceGet(s, in, n)
	case ir.OpLen, ir.OpCap:
		return rt.expandLenCap(s, in, n)
	case ir.OpLiteral:
		return rt.expandLiteral(s, in, n)
	case ir.OpAddr:
		return rt.expandAddr(s, in, n)
	case ir.OpDeref:
		return rt.expandDeref(s, in, n)
	case ir.OpStringGet:
		return rt.expandStringGet(s, in, n)
	case ir.OpStringSlice:
		return rt.expandStringSlice(s, in, n)
	case ir.OpIfaceMake:
		return rt.expandIfaceMake(s, in, n)
	case ir.OpIfaceCall:
		return rt.expandIfaceCall(s, in, n)
	case ir.OpIfaceAssert:
		return rt.expandIfaceAssert(s, in, n)
	case ir.OpConv:
		if in.Str == "iface.eq" {
			return rt.expandIfaceEq(s, in, n)
		}
		return nil, fmt.Errorf("lower.RuntimeOps: conv %q not lowered", in.Str)
	case ir.OpChanMake:
		return rt.expandChanMake(s, in, n)
	case ir.OpChanRecv:
		return rt.expandChanRecv(s, in, n)
	case ir.OpGo:
		return rt.expandGo(in, n)
	case ir.OpChanSend:
		return rt.expandChanSend(in, n)
	case ir.OpChanClose:
		return rt.expandChanClose(in, n)
	case ir.OpMapMake:
		return rt.expandMapMake(s, in, n)
	case ir.OpMapGet:
		return rt.expandMapGet(s, in, n)
	case ir.OpMapLen:
		return rt.expandMapLen(s, in, n)
	case ir.OpMapIter:
		return rt.expandMapIter(s, in, n)
	case ir.OpMapNext:
		return rt.expandMapNext(s, in, n)
	default:
		return nil, fmt.Errorf("lower.RuntimeOps: high-level op %s not lowered", in.Op)
	}
}

func (rt *runtimeLower) expandHighExpr(in *ir.Instr, n *int) ([]ir.Stmt, error) {
	switch in.Op {
	case ir.OpStore:
		return rt.expandStore(in, n)
	case ir.OpSliceSet:
		return rt.expandSliceSet(in, n)
	case ir.OpGo:
		return rt.expandGo(in, n)
	case ir.OpChanSend:
		return rt.expandChanSend(in, n)
	case ir.OpChanClose:
		return rt.expandChanClose(in, n)
	case ir.OpChanRecv:
		// Discard receive (ExprStmt / Drop): no unbox, no type globals.
		return rt.expandChanRecv(nil, in, n)
	case ir.OpMapSet:
		return rt.expandMapSet(in, n)
	case ir.OpMapDelete:
		return rt.expandMapDelete(in, n)
	default:
		// Expand as assign to dummy then drop — should not happen often.
		t := &ir.Temp{Name: rt.fresh("x"), Typ: ir.TypeI32}
		as := &ir.Assign{Lhs: []ir.Dest{{Temp: t, Name: t.Name}}, Rhs: in}
		return rt.expandHighAssign(as, in, n)
	}
}

func sliceParts(t ir.Type) (sliceName, arrName string) {
	r, ok := t.(*ir.Ref)
	if !ok {
		return "", ""
	}
	if !strings.HasPrefix(r.Named, "$go.slice.") {
		return "", ""
	}
	sliceName = r.Named
	suf := strings.TrimPrefix(sliceName, "$go.slice.")
	switch suf {
	case "i8":
		arrName = "$go.bytes"
	case "i16":
		arrName = "$go.i16s"
	default:
		arrName = "$go.array." + suf
	}
	return sliceName, arrName
}

// sliceTypeFrom recovers ($go.slice.*, $go.array.*) from the slice operand
// and, failing that, from the element type on OpSliceGet.
func (rt *runtimeLower) sliceTypeFrom(orig, rewritten ir.Expr, elem ir.Type) (sliceName, arrName string) {
	try := func(e ir.Expr) (string, string) {
		switch x := e.(type) {
		case *ir.Name:
			if x.Temp != nil {
				return sliceParts(x.Temp.Typ)
			}
			if x.Var != nil {
				return sliceParts(x.Var.Typ)
			}
		case *ir.Instr:
			// global.get has no Instr.Type; use the declared global type
			// (package-level []byte must stay $go.slice.i8, not fall back to i32).
			if x.Op == ir.OpGlobalGet && x.Global != "" && rt.mod != nil {
				if g := rt.mod.LookupGlobal(x.Global); g != nil {
					return sliceParts(g.Typ)
				}
			}
			// struct.get's Type is the struct; recover the field's slice type.
			if x.Op == ir.OpStructGet && x.Field != "" && x.Type != nil {
				if r, ok := x.Type.(*ir.Ref); ok && r.Named != "" {
					if td := rt.mod.LookupType(r.Named); td != nil {
						for _, f := range td.Fields {
							if f.Name == x.Field {
								return sliceParts(f.Type)
							}
						}
					}
				}
			}
			if x.Type != nil {
				if sn, an := sliceParts(x.Type); sn != "" {
					return sn, an
				}
			}
		}
		return "", ""
	}
	if sn, an := try(orig); sn != "" {
		return sn, an
	}
	if sn, an := try(rewritten); sn != "" {
		return sn, an
	}
	if elem == nil {
		return "", ""
	}
	// Build slice/array names from the element IR type (e.g. $go.iface →
	// $go.slice.go.iface / $go.array.go.iface).
	arrName = arrayTypeName(elem)
	if arrName == "" {
		return "", ""
	}
	return sliceTypeName(arrName), arrName
}

func (rt *runtimeLower) expandMakeSlice(s *ir.Assign, in *ir.Instr, n *int) ([]ir.Stmt, error) {
	sliceName, arrName := sliceParts(in.Type)
	if sliceName == "" {
		return nil, fmt.Errorf("make.slice: bad type %s", in.Type)
	}
	rt.ensureSliceTypes(sliceName, arrName)
	lenEx, pre, err := rt.rewriteExpr(in.Args[0], n)
	if err != nil {
		return nil, err
	}
	capEx := lenEx
	if len(in.Args) > 1 {
		var pre2 []ir.Stmt
		capEx, pre2, err = rt.rewriteExpr(in.Args[1], n)
		if err != nil {
			return nil, err
		}
		pre = append(pre, pre2...)
	}
	lenEx = forceI32Expr(lenEx)
	capEx = forceI32Expr(capEx)

	// Bound-check before array.new: negative / len>cap / too-large must
	// panicmakeslice* (recoverable), not Wasm "array too large" RuntimeError.
	// maxMakeElems is well below engine array limits but above any realistic
	// passing make; GOROOT makeslice tests use MaxInt32 / ^uint32(0).
	const maxMakeElems = 1 << 28
	lenV := rt.newVar("mlen", ir.TypeI32)
	capV := rt.newVar("mcap", ir.TypeI32)
	pre = append(pre, &ir.VarDef{Var: lenV}, &ir.VarDef{Var: capV})
	pre = append(pre, assignV(lenV, lenEx), assignV(capV, capEx))
	maxC := &ir.Const{Op: ir.OpI32Const, I64: maxMakeElems}
	// len < 0 || uint(len) > max → makeslice: len out of range
	pre = append(pre, &ir.If{
		Cond: &ir.Instr{Op: ir.OpI32Or, Args: []ir.Expr{
			&ir.Instr{Op: ir.OpI32LtS, Args: []ir.Expr{useV(lenV), &ir.Const{Op: ir.OpI32Const, I64: 0}}},
			&ir.Instr{Op: ir.OpI32GtU, Args: []ir.Expr{useV(lenV), maxC}},
		}},
		Then: &ir.Seq{Stmts: rt.panicMakeSliceLen()},
	})
	// cap < len || cap < 0 || uint(cap) > max → makeslice: cap out of range
	pre = append(pre, &ir.If{
		Cond: &ir.Instr{Op: ir.OpI32Or, Args: []ir.Expr{
			&ir.Instr{Op: ir.OpI32LtS, Args: []ir.Expr{useV(capV), useV(lenV)}},
			&ir.Instr{Op: ir.OpI32Or, Args: []ir.Expr{
				&ir.Instr{Op: ir.OpI32LtS, Args: []ir.Expr{useV(capV), &ir.Const{Op: ir.OpI32Const, I64: 0}}},
				&ir.Instr{Op: ir.OpI32GtU, Args: []ir.Expr{useV(capV), maxC}},
			}},
		}},
		Then: &ir.Seq{Stmts: rt.panicMakeSliceCap()},
	})

	dataT := &ir.Temp{Name: rt.fresh("data"), Typ: &ir.Ref{Named: arrName}}
	alloc, allocPre := rt.allocArray(arrName, useV(capV))
	pre = append(pre, allocPre...)
	pre = append(pre, &ir.Assign{
		Lhs: []ir.Dest{{Temp: dataT, Name: dataT.Name}},
		Rhs: alloc,
	})
	pre = append(pre, &ir.Assign{
		Lhs: s.Lhs,
		Rhs: &ir.Instr{
			Op:   ir.OpStructNew,
			Type: &ir.Ref{Named: sliceName},
			Args: []ir.Expr{
				&ir.Name{Temp: dataT, Raw: dataT.Name},
				&ir.Const{Op: ir.OpI32Const, I64: 0},
				useV(lenV),
				useV(capV),
			},
		},
	})
	return pre, nil
}

func (rt *runtimeLower) panicMakeSliceLen() []ir.Stmt {
	return []ir.Stmt{
		&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: "runtime.panicmakeslicelen"}},
		&ir.Unreachable{},
	}
}

func (rt *runtimeLower) panicMakeSliceCap() []ir.Stmt {
	return []ir.Stmt{
		&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: "runtime.panicmakeslicecap"}},
		&ir.Unreachable{},
	}
}

// allocArray allocates an array of arrName with the given length. Uses
// array.new with a zero init when elements are non-defaultable (e.g.
// (ref $go.string)); struct elems need a fill helper so each slot gets a
// fresh zero (array.new would alias one object; new_default leaves null).
func (rt *runtimeLower) allocArray(arrName string, size ir.Expr) (ir.Expr, []ir.Stmt) {
	if fn, ok := rt.ensureMakeZeroArrayFunc(arrName); ok {
		return &ir.Instr{
			Op:   ir.OpCall,
			Func: fn,
			Args: []ir.Expr{size},
			Type: &ir.Ref{Named: arrName},
		}, nil
	}
	if z, ok := rt.arrayElemZero(arrName); ok {
		return &ir.Instr{
			Op:   ir.OpArrayNew,
			Type: &ir.Ref{Named: arrName},
			Args: []ir.Expr{z, size},
		}, nil
	}
	return &ir.Instr{
		Op:   ir.OpArrayNewDefault,
		Type: &ir.Ref{Named: arrName},
		Args: []ir.Expr{size},
	}, nil
}

// ensureMakeZeroArrayFunc builds $go.zeroArray.* for make([]Struct) when
// array.new_default would leave null struct slots (refnull $go.struct.*).
// Pointer-elem arrays ($go.array.ptr.*) keep nulls. Zero-sized structs share
// one object via arrayElemZero so &a[i]==&a[j] (bug352).
func (rt *runtimeLower) ensureMakeZeroArrayFunc(arrName string) (string, bool) {
	if strings.HasPrefix(arrName, "$go.array.ptr.") {
		return "", false
	}
	td := rt.mod.LookupType(arrName)
	if td == nil || td.Kind != ir.TypeArray {
		return "", false
	}
	r, ok := td.Elem.(*ir.Ref)
	if !ok || r.Named == "" || !strings.HasPrefix(r.Named, "$go.struct.") {
		return "", false
	}
	ntd := rt.mod.LookupType(r.Named)
	if ntd == nil || ntd.Kind != ir.TypeStruct {
		return "", false
	}
	if len(ntd.Fields) == 0 {
		// Zero-sized: array.new of a shared empty (arrayElemZero).
		return "", false
	}
	name := "$go.zeroArray." + strings.TrimPrefix(arrName, "$")
	if rt.mod.LookupFunc(name) != nil {
		return name, true
	}
	aty := &ir.Ref{Named: arrName}
	elemTy := &ir.Ref{Null: true, Named: r.Named}
	nVar := &ir.Var{Name: "n", Typ: ir.TypeI32}
	aVar := &ir.Var{Name: "a", Typ: aty}
	iVar := &ir.Var{Name: "i", Typ: ir.TypeI32}
	mkZero := func() ir.Expr {
		z, err := rt.structZeroExpr(ntd, elemTy, map[string]bool{})
		if err != nil {
			return &ir.Instr{Op: ir.OpRefNull, Type: elemTy}
		}
		return z
	}
	loopL := rt.fresh("zeroArrL")
	breakL := rt.fresh("zeroArrB")
	body := &ir.Seq{Stmts: []ir.Stmt{
		&ir.VarDef{Var: aVar},
		&ir.VarDef{Var: iVar},
		&ir.Assign{Lhs: []ir.Dest{{Name: "a", Var: aVar}}, Rhs: &ir.Instr{
			Op:   ir.OpArrayNewDefault,
			Type: aty,
			Args: []ir.Expr{&ir.Name{Raw: "n", Var: nVar}},
		}},
		&ir.Assign{Lhs: []ir.Dest{{Name: "i", Var: iVar}}, Rhs: &ir.Const{Op: ir.OpI32Const, I64: 0}},
		&ir.Block{Name: breakL, Body: &ir.Loop{
			Name: loopL,
			Body: &ir.Seq{Stmts: []ir.Stmt{
				&ir.If{
					Cond: &ir.Instr{Op: ir.OpI32GeU, Args: []ir.Expr{
						&ir.Name{Raw: "i", Var: iVar},
						&ir.Name{Raw: "n", Var: nVar},
					}},
					Then: &ir.Seq{Stmts: []ir.Stmt{&ir.Br{Label: breakL}}},
				},
				&ir.ExprStmt{X: &ir.Instr{
					Op:   ir.OpArraySet,
					Type: aty,
					Args: []ir.Expr{
						&ir.Name{Raw: "a", Var: aVar},
						&ir.Name{Raw: "i", Var: iVar},
						mkZero(),
					},
				}},
				&ir.Assign{Lhs: []ir.Dest{{Name: "i", Var: iVar}}, Rhs: &ir.Instr{
					Op: ir.OpI32Add, Args: []ir.Expr{
						&ir.Name{Raw: "i", Var: iVar},
						&ir.Const{Op: ir.OpI32Const, I64: 1},
					},
				}},
				&ir.Br{Label: loopL},
			}},
		}},
		&ir.Return{Results: []ir.Expr{&ir.Name{Raw: "a", Var: aVar}}},
	}}
	rt.mod.Funcs = append(rt.mod.Funcs, &ir.Func{
		Name:    name,
		Params:  []*ir.Var{nVar},
		Results: []ir.Type{aty},
		Body:    body,
	})
	return name, true
}

// arrayElemZero returns a zero element for non-defaultable array elems that
// are safe to share across slots (immutable strings, zero-sized structs).
// Non-empty struct elems use ensureMakeZeroArrayFunc instead.
func (rt *runtimeLower) arrayElemZero(arrName string) (ir.Expr, bool) {
	if arrName == "$go.array.go.string" {
		return &ir.Instr{Op: ir.OpStringLit, Str: ""}, true
	}
	td := rt.mod.LookupType(arrName)
	if td == nil || td.Kind != ir.TypeArray {
		return nil, false
	}
	r, ok := td.Elem.(*ir.Ref)
	if !ok {
		return nil, false
	}
	if r.Named == "$go.string" {
		return &ir.Instr{Op: ir.OpStringLit, Str: ""}, true
	}
	// Zero-sized struct{}: one shared object so &slice[i]==&slice[j] (bug352).
	if strings.HasPrefix(r.Named, "$go.struct.") && !strings.HasPrefix(arrName, "$go.array.ptr.") {
		if ntd := rt.mod.LookupType(r.Named); ntd != nil && ntd.Kind == ir.TypeStruct && len(ntd.Fields) == 0 {
			z, err := rt.structZeroExpr(ntd, &ir.Ref{Null: true, Named: r.Named}, map[string]bool{})
			if err == nil {
				return z, true
			}
		}
	}
	return nil, false
}

// ensureZeroLenArrayGlobal returns a module-global canonical []T of length 0
// (runtime.zerobase semantics for new([0]T) and identical *[0]T identities).
func ensureZeroLenArrayGlobal(mod *ir.Module, arrName string) ir.Expr {
	gname := "$go.zerolen." + sanitize(arrName)
	if mod.LookupGlobal(gname) == nil {
		mod.Globals = append(mod.Globals, &ir.Global{
			Name: gname,
			Typ:  &ir.Ref{Named: arrName},
			Mut:  false,
			Init: &ir.Instr{
				Op:   ir.OpArrayNewDefault,
				Type: &ir.Ref{Named: arrName},
				Args: []ir.Expr{&ir.Const{Op: ir.OpI32Const, I64: 0}},
			},
		})
	}
	return &ir.Instr{Op: ir.OpGlobalGet, Global: gname}
}

func (rt *runtimeLower) ensureZeroLenArrayGlobal(arrName string) ir.Expr {
	return ensureZeroLenArrayGlobal(rt.mod, arrName)
}

func (rt *runtimeLower) ensureSliceTypes(sliceName, arrName string) {
	if arrName != "$go.bytes" && arrName != "$go.i16s" && rt.mod.LookupType(arrName) == nil {
		elem := elemOfArrayName(arrName)
		rt.mod.InternType(&ir.TypeDef{Name: arrName, Kind: ir.TypeArray, Elem: elem, ElemMut: true})
	}
	if rt.mod.LookupType(sliceName) == nil {
		rt.mod.InternType(&ir.TypeDef{
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
	}
}

func elemOfArrayName(arrName string) ir.Type {
	switch arrName {
	case "$go.bytes":
		return ir.TypeI8
	case "$go.i16s":
		return ir.TypeI16
	}
	suf := strings.TrimPrefix(arrName, "$go.array.")
	// $go.array.ptr.go.struct.T → same elem ref as $go.array.go.struct.T
	// (*Struct and Struct share IR; only the array name differs).
	if strings.HasPrefix(suf, "ptr.") {
		suf = strings.TrimPrefix(suf, "ptr.")
	}
	switch suf {
	case "i32":
		return ir.TypeI32
	case "i64":
		return ir.TypeI64
	case "f32":
		return ir.TypeF32
	case "f64":
		return ir.TypeF64
	case "i8":
		return ir.TypeI8
	case "i16":
		return ir.TypeI16
	case "go.iface", "iface":
		return &ir.Ref{Null: true, Named: "$go.iface"}
	case "go.string", "string":
		return &ir.Ref{Named: "$go.string"}
	default:
		// Reference element arrays ($go.array.go.slice.i8, …).
		if strings.HasSuffix(suf, ".iface") {
			return &ir.Ref{Null: true, Named: "$go.iface"}
		}
		if strings.HasPrefix(suf, "go.") {
			return &ir.Ref{Null: true, Named: "$" + suf}
		}
		return &ir.Ref{Null: true, Heap: ir.HeapAny}
	}
}

func (rt *runtimeLower) expandLiteral(s *ir.Assign, in *ir.Instr, n *int) ([]ir.Stmt, error) {
	if in.Str == "new" {
		return rt.expandNew(s, in, n)
	}
	tname := ""
	if r, ok := in.Type.(*ir.Ref); ok {
		tname = r.Named
	}
	if strings.HasPrefix(tname, "$go.slice.") {
		return rt.expandSliceLit(s, in, n)
	}
	if strings.HasPrefix(tname, "$go.struct.") {
		return rt.expandStructLit(s, in, n)
	}
	// Shared runtime layouts ($go.g, $go.chan, $go.waiter, …) are also structs.
	if td := rt.mod.LookupType(tname); td != nil && td.Kind == ir.TypeStruct {
		return rt.expandStructLit(s, in, n)
	}
	if strings.HasPrefix(tname, "$go.array.") || tname == "$go.bytes" || tname == "$go.i16s" {
		return rt.expandArrayLit(s, in, n)
	}
	if strings.HasPrefix(tname, "$go.ptr.") {
		return rt.expandNew(s, in, n)
	}
	return nil, fmt.Errorf("literal of type %s not supported", in.Type)
}

func (rt *runtimeLower) expandSliceLit(s *ir.Assign, in *ir.Instr, n *int) ([]ir.Stmt, error) {
	sliceName, arrName := sliceParts(in.Type)
	rt.ensureSliceTypes(sliceName, arrName)
	var pre []ir.Stmt
	var elems []ir.Expr
	for _, a := range in.Args {
		ex, p, err := rt.rewriteExpr(a, n)
		if err != nil {
			return nil, err
		}
		pre = append(pre, p...)
		elems = append(elems, ex)
	}
	length := len(elems)
	dataT := &ir.Temp{Name: rt.fresh("data"), Typ: &ir.Ref{Named: arrName}}
	arrTy := &ir.Ref{Named: arrName}
	if length == 0 {
		alloc, allocPre := rt.allocArray(arrName, &ir.Const{Op: ir.OpI32Const, I64: 0})
		pre = append(pre, allocPre...)
		pre = append(pre, &ir.Assign{
			Lhs: []ir.Dest{{Temp: dataT, Name: dataT.Name}},
			Rhs: alloc,
		})
	} else if rhs, ok := rt.constArrayNew(arrTy, elems, n); ok {
		pre = append(pre, &ir.Assign{
			Lhs: []ir.Dest{{Temp: dataT, Name: dataT.Name}},
			Rhs: rhs,
		})
	} else {
		pre = append(pre, &ir.Assign{
			Lhs: []ir.Dest{{Temp: dataT, Name: dataT.Name}},
			Rhs: &ir.Instr{Op: ir.OpArrayNewFixed, Type: arrTy, Args: elems},
		})
	}
	pre = append(pre, &ir.Assign{
		Lhs: s.Lhs,
		Rhs: &ir.Instr{
			Op:   ir.OpStructNew,
			Type: &ir.Ref{Named: sliceName},
			Args: []ir.Expr{
				&ir.Name{Temp: dataT, Raw: dataT.Name},
				&ir.Const{Op: ir.OpI32Const, I64: 0},
				&ir.Const{Op: ir.OpI32Const, I64: int64(length)},
				&ir.Const{Op: ir.OpI32Const, I64: int64(length)},
			},
		},
	})
	return pre, nil
}

func (rt *runtimeLower) expandStructLit(s *ir.Assign, in *ir.Instr, n *int) ([]ir.Stmt, error) {
	var pre []ir.Stmt
	var args []ir.Expr
	for _, a := range in.Args {
		ex, p, err := rt.rewriteExpr(a, n)
		if err != nil {
			return nil, err
		}
		pre = append(pre, p...)
		args = append(args, ex)
	}
	pre = append(pre, &ir.Assign{
		Lhs: s.Lhs,
		Rhs: &ir.Instr{Op: ir.OpStructNew, Type: in.Type, Args: args},
	})
	return pre, nil
}

func (rt *runtimeLower) expandArrayLit(s *ir.Assign, in *ir.Instr, n *int) ([]ir.Stmt, error) {
	var pre []ir.Stmt
	var args []ir.Expr
	for _, a := range in.Args {
		ex, p, err := rt.rewriteExpr(a, n)
		if err != nil {
			return nil, err
		}
		pre = append(pre, p...)
		args = append(args, ex)
	}
	rhs := ir.Expr(&ir.Instr{Op: ir.OpArrayNewFixed, Type: in.Type, Args: args})
	if packed, ok := rt.constArrayNew(in.Type, args, n); ok {
		rhs = packed
	}
	pre = append(pre, &ir.Assign{Lhs: s.Lhs, Rhs: rhs})
	return pre, nil
}

// constArrayNew packs a constant numeric array/slice backing into a passive
// data segment and returns array.new_data (03). Non-numeric or non-const
// elements fall back to array.new_fixed.
func (rt *runtimeLower) constArrayNew(arrTy ir.Type, elems []ir.Expr, n *int) (ir.Expr, bool) {
	if len(elems) == 0 {
		return nil, false
	}
	r, ok := arrTy.(*ir.Ref)
	if !ok {
		return nil, false
	}
	elemSize, pack := numericArrayPacker(r.Named)
	if pack == nil {
		return nil, false
	}
	buf := make([]byte, 0, len(elems)*elemSize)
	for _, e := range elems {
		c, ok := e.(*ir.Const)
		if !ok {
			return nil, false
		}
		b, ok := pack(c)
		if !ok {
			return nil, false
		}
		buf = append(buf, b...)
	}
	litName := fmt.Sprintf("$lit%d", len(rt.mod.Data))
	if n != nil {
		*n = len(rt.mod.Data) + 1
	}
	rt.mod.Data = append(rt.mod.Data, &ir.Data{Name: litName, Bytes: buf})
	return &ir.Instr{
		Op:   ir.OpArrayNewData,
		Type: arrTy,
		Data: litName,
	}, true
}

// numericArrayPacker returns the element byte width and a const→bytes packer
// for packed numeric Wasm array types ($go.bytes, $go.i16s, $go.array.i32, …).
func numericArrayPacker(typeName string) (elemSize int, pack func(*ir.Const) ([]byte, bool)) {
	putU16 := func(v uint16) []byte { return []byte{byte(v), byte(v >> 8)} }
	putU32 := func(v uint32) []byte {
		return []byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)}
	}
	putU64 := func(v uint64) []byte {
		b := make([]byte, 8)
		for i := 0; i < 8; i++ {
			b[i] = byte(v >> (8 * i))
		}
		return b
	}
	switch typeName {
	case "$go.bytes":
		return 1, func(c *ir.Const) ([]byte, bool) {
			if c.Op != ir.OpI32Const && c.Op != ir.OpI64Const {
				return nil, false
			}
			return []byte{byte(c.I64)}, true
		}
	case "$go.i16s":
		return 2, func(c *ir.Const) ([]byte, bool) {
			if c.Op != ir.OpI32Const && c.Op != ir.OpI64Const {
				return nil, false
			}
			return putU16(uint16(c.I64)), true
		}
	case "$go.array.i32", "$go.array.u32", "$go.array.int", "$go.array.uint":
		return 4, func(c *ir.Const) ([]byte, bool) {
			if c.Op != ir.OpI32Const && c.Op != ir.OpI64Const {
				return nil, false
			}
			return putU32(uint32(c.I64)), true
		}
	case "$go.array.i64", "$go.array.u64":
		return 8, func(c *ir.Const) ([]byte, bool) {
			if c.Op != ir.OpI32Const && c.Op != ir.OpI64Const {
				return nil, false
			}
			return putU64(uint64(c.I64)), true
		}
	case "$go.array.f32":
		return 4, func(c *ir.Const) ([]byte, bool) {
			if c.Op != ir.OpF32Const && c.Op != ir.OpF64Const {
				return nil, false
			}
			return putU32(math.Float32bits(float32(c.F64))), true
		}
	case "$go.array.f64":
		return 8, func(c *ir.Const) ([]byte, bool) {
			if c.Op != ir.OpF64Const && c.Op != ir.OpF32Const {
				return nil, false
			}
			return putU64(math.Float64bits(c.F64)), true
		}
	default:
		return 0, nil
	}
}

// structZeroExpr builds a non-null zero $go.struct.* with nested struct
// fields recursively allocated (same rules as funcLower.structZero).
func (rt *runtimeLower) structZeroExpr(td *ir.TypeDef, t ir.Type, seen map[string]bool) (ir.Expr, error) {
	if seen[td.Name] {
		// Cycle / self-pointer: null (avoid struct.new_default on strings).
		return &ir.Instr{Op: ir.OpRefNull, Type: t}, nil
	}
	seen[td.Name] = true
	args := make([]ir.Expr, len(td.Fields))
	for i, f := range td.Fields {
		// Pointer fields (Ptr) and same-type $go.struct → *T (null).
		if r, ok := f.Type.(*ir.Ref); ok && strings.HasPrefix(r.Named, "$go.struct.") {
			if f.Ptr || r.Named == td.Name {
				args[i] = &ir.Instr{Op: ir.OpRefNull, Type: r}
				continue
			}
			ntd := rt.mod.LookupType(r.Named)
			if ntd != nil && ntd.Kind == ir.TypeStruct {
				z, err := rt.structZeroExpr(ntd, r, seen)
				if err != nil {
					return nil, err
				}
				args[i] = z
				continue
			}
		}
		if f.Ptr {
			if r, ok := f.Type.(*ir.Ref); ok {
				args[i] = &ir.Instr{Op: ir.OpRefNull, Type: r}
				continue
			}
		}
		if r, ok := f.Type.(*ir.Ref); ok && r.Named == "$go.string" {
			args[i] = &ir.Instr{Op: ir.OpStringLit, Str: ""}
			continue
		}
		// Fixed Go array field: allocate Len elements. (ref $go.string) is
		// non-defaultable — use array.new of "" (StringLit expands later).
		if r, ok := f.Type.(*ir.Ref); ok && f.Len > 0 &&
			(r.Named == "$go.bytes" || r.Named == "$go.i16s" || strings.HasPrefix(r.Named, "$go.array.")) {
			if r.Named == "$go.array.go.string" {
				args[i] = &ir.Instr{
					Op:   ir.OpArrayNew,
					Type: &ir.Ref{Named: r.Named},
					Args: []ir.Expr{
						&ir.Instr{Op: ir.OpStringLit, Str: ""},
						&ir.Const{Op: ir.OpI32Const, I64: f.Len},
					},
				}
			} else {
				args[i] = &ir.Instr{
					Op:   ir.OpArrayNewDefault,
					Type: &ir.Ref{Named: r.Named},
					Args: []ir.Expr{&ir.Const{Op: ir.OpI32Const, I64: f.Len}},
				}
			}
			continue
		}
		switch f.Type {
		case ir.TypeI32:
			args[i] = &ir.Const{Op: ir.OpI32Const, I64: 0}
		case ir.TypeI64:
			args[i] = &ir.Const{Op: ir.OpI64Const, I64: 0}
		case ir.TypeF32:
			args[i] = &ir.Const{Op: ir.OpF32Const, F64: 0}
		case ir.TypeF64:
			args[i] = &ir.Const{Op: ir.OpF64Const, F64: 0}
		default:
			args[i] = &ir.Instr{Op: ir.OpRefNull, Type: f.Type}
		}
	}
	delete(seen, td.Name)
	return &ir.Instr{Op: ir.OpStructNew, Type: t, Args: args}, nil
}

func (rt *runtimeLower) expandNew(s *ir.Assign, in *ir.Instr, n *int) ([]ir.Stmt, error) {
	// new(T) / literal of ptr type: 1-elem array + fat pointer at index 0,
	// or struct.new_default for *Struct.
	tname := ""
	if r, ok := in.Type.(*ir.Ref); ok {
		tname = r.Named
	}
	if strings.HasPrefix(tname, "$go.struct.") {
		// Prefer a proper zero (nested $go.struct.* fields allocated, not
		// null) so new(pp) yields a usable pp.fmt, matching Go value zeros.
		var z ir.Expr
		if td := rt.mod.LookupType(tname); td != nil && td.Kind == ir.TypeStruct {
			if zz, err := rt.structZeroExpr(td, in.Type, map[string]bool{}); err == nil {
				z = zz
			}
		}
		if z == nil {
			z = &ir.Instr{Op: ir.OpStructNewDefault, Type: in.Type}
		}
		return []ir.Stmt{&ir.Assign{Lhs: s.Lhs, Rhs: z}}, nil
	}
	// *[N]T maps to the array ref itself ($go.array.*); allocate a zeroed array.
	// Length N is carried in in.I32 from builtinNew.
	if strings.HasPrefix(tname, "$go.array.") || tname == "$go.bytes" {
		nElems := int64(in.I32)
		if nElems < 0 {
			nElems = 0
		}
		if nElems == 0 {
			return []ir.Stmt{&ir.Assign{Lhs: s.Lhs, Rhs: rt.ensureZeroLenArrayGlobal(tname)}}, nil
		}
		alloc, pre := rt.allocArray(tname, &ir.Const{Op: ir.OpI32Const, I64: nElems})
		pre = append(pre, &ir.Assign{Lhs: s.Lhs, Rhs: alloc})
		return pre, nil
	}
	// Fat pointer to scalar: allocate [1]T and build ptr.
	kind := "i32"
	if strings.HasPrefix(tname, "$go.ptr.") {
		kind = strings.TrimPrefix(tname, "$go.ptr.")
	}
	arrName := "$go.array." + kind
	if kind == "i8" {
		arrName = "$go.bytes"
	}
	rt.ensureFatPtr(kind)
	if arrName != "$go.bytes" && rt.mod.LookupType(arrName) == nil {
		rt.mod.InternType(&ir.TypeDef{Name: arrName, Kind: ir.TypeArray, Elem: elemOfArrayName(arrName), ElemMut: true})
	}
	zero := zeroForKind(kind)
	dataT := &ir.Temp{Name: rt.fresh("box"), Typ: &ir.Ref{Named: arrName}}
	var pre []ir.Stmt
	pre = append(pre, &ir.Assign{
		Lhs: []ir.Dest{{Temp: dataT, Name: dataT.Name}},
		Rhs: &ir.Instr{Op: ir.OpArrayNewFixed, Type: &ir.Ref{Named: arrName}, Args: []ir.Expr{zero}},
	})
	classG := rt.classGlobal(arrName, "elem", kind)
	pre = append(pre, &ir.Assign{
		Lhs: s.Lhs,
		Rhs: &ir.Instr{
			Op:   ir.OpStructNew,
			Type: &ir.Ref{Named: "$go.ptr." + kind},
			Args: []ir.Expr{
				&ir.Instr{Op: ir.OpGlobalGet, Global: classG},
				&ir.Name{Temp: dataT, Raw: dataT.Name},
				&ir.Const{Op: ir.OpI32Const, I64: 0},
			},
		},
	})
	return pre, nil
}

func zeroForKind(kind string) ir.Expr {
	switch kind {
	case "i64":
		return &ir.Const{Op: ir.OpI64Const, I64: 0}
	case "f32":
		return &ir.Const{Op: ir.OpF32Const, F64: 0}
	case "f64":
		return &ir.Const{Op: ir.OpF64Const, F64: 0}
	case "ref":
		return &ir.Instr{Op: ir.OpRefNull, Type: &ir.Ref{Null: true, Heap: ir.HeapAny}}
	default:
		return &ir.Const{Op: ir.OpI32Const, I64: 0}
	}
}

func (rt *runtimeLower) expandAppend(s *ir.Assign, in *ir.Instr, n *int) ([]ir.Stmt, error) {
	sliceName, arrName := sliceParts(in.Type)
	if sliceName == "" && len(in.Args) > 0 {
		if nm, ok := in.Args[0].(*ir.Name); ok {
			if nm.Temp != nil {
				sliceName, arrName = sliceParts(nm.Temp.Typ)
			}
			if nm.Var != nil {
				sliceName, arrName = sliceParts(nm.Var.Typ)
			}
		}
	}
	if in.Type != nil {
		sliceName, arrName = sliceParts(in.Type)
	}
	rt.ensureSliceTypes(sliceName, arrName)

	var pre []ir.Stmt
	sliceEx, p, err := rt.rewriteExpr(in.Args[0], n)
	if err != nil {
		return nil, err
	}
	pre = append(pre, p...)
	var elemEx []ir.Expr
	for _, a := range in.Args[1:] {
		ex, p, err := rt.rewriteExpr(a, n)
		if err != nil {
			return nil, err
		}
		pre = append(pre, p...)
		elemEx = append(elemEx, ex)
	}
	add := len(elemEx)
	st := &ir.Ref{Null: true, Named: sliceName}

	// append(s) with no elements must return s unchanged (nil stays nil).
	if add == 0 {
		pre = append(pre, &ir.Assign{Lhs: s.Lhs, Rhs: sliceEx})
		return pre, nil
	}

	// Array refs are nullable so locals are defaultable: grow-only ndata is
	// unset on the fit path, and Wasm rejects uninitialized non-defaultable locals.
	arrT := &ir.Ref{Null: true, Named: arrName}
	sl := rt.newVar("sl", st)
	lenV := rt.newVar("len", ir.TypeI32)
	capV := rt.newVar("cap", ir.TypeI32)
	offV := rt.newVar("off", ir.TypeI32)
	dataV := rt.newVar("data", arrT)
	nlenV := rt.newVar("nlen", ir.TypeI32)
	ncapV := rt.newVar("ncap", ir.TypeI32)
	twocapV := rt.newVar("twocap", ir.TypeI32)
	ndataV := rt.newVar("ndata", arrT)

	defs := []*ir.Var{sl, lenV, capV, offV, dataV, nlenV, ncapV, twocapV, ndataV}
	for _, v := range defs {
		pre = append(pre, &ir.VarDef{Var: v})
	}
	pre = append(pre, assignV(sl, sliceEx))
	// nil slice: treat as empty (len=0,cap=0,off=0, fresh zero-length array).
	nilAlloc, nilAllocPre := rt.allocArray(arrName, &ir.Const{Op: ir.OpI32Const, I64: 0})
	nilSeq := &ir.Seq{Stmts: append(append([]ir.Stmt{}, nilAllocPre...),
		assignV(lenV, &ir.Const{Op: ir.OpI32Const, I64: 0}),
		assignV(capV, &ir.Const{Op: ir.OpI32Const, I64: 0}),
		assignV(offV, &ir.Const{Op: ir.OpI32Const, I64: 0}),
		assignV(dataV, nilAlloc),
	)}
	nonNil := &ir.Seq{Stmts: []ir.Stmt{
		assignV(lenV, &ir.Instr{Op: ir.OpStructGet, Type: st, Field: "len", Args: []ir.Expr{useV(sl)}}),
		assignV(capV, &ir.Instr{Op: ir.OpStructGet, Type: st, Field: "cap", Args: []ir.Expr{useV(sl)}}),
		assignV(offV, &ir.Instr{Op: ir.OpStructGet, Type: st, Field: "off", Args: []ir.Expr{useV(sl)}}),
		assignV(dataV, &ir.Instr{Op: ir.OpStructGet, Type: st, Field: "data", Args: []ir.Expr{useV(sl)}}),
	}}
	pre = append(pre, &ir.If{
		Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{useV(sl)}},
		Then: nilSeq,
		Else: nonNil,
	})
	pre = append(pre,
		assignV(nlenV, &ir.Instr{Op: ir.OpI32Add, Args: []ir.Expr{useV(lenV), &ir.Const{Op: ir.OpI32Const, I64: int64(add)}}}),
	)

	fits := &ir.Instr{Op: ir.OpI32LeU, Args: []ir.Expr{useV(nlenV), useV(capV)}}
	nnData := func(v *ir.Var) ir.Expr {
		return &ir.Instr{Op: ir.OpRefAsNonNull, Args: []ir.Expr{useV(v)}}
	}

	// Result via a function-scoped var: assigning s.Lhs (often a Temp) inside
	// both if arms makes the temp unusable after the If (bug491 multi-assign).
	resV := rt.newVar("app", st)
	pre = append(pre, &ir.VarDef{Var: resV})

	fitSeq := &ir.Seq{}
	for i, el := range elemEx {
		idx := &ir.Instr{Op: ir.OpI32Add, Args: []ir.Expr{
			useV(offV),
			&ir.Instr{Op: ir.OpI32Add, Args: []ir.Expr{useV(lenV), &ir.Const{Op: ir.OpI32Const, I64: int64(i)}}},
		}}
		fitSeq.Stmts = append(fitSeq.Stmts, &ir.ExprStmt{X: &ir.Instr{
			Op: ir.OpArraySet, Type: &ir.Ref{Named: arrName},
			Args: []ir.Expr{nnData(dataV), idx, el},
		}})
	}
	fitSeq.Stmts = append(fitSeq.Stmts, assignV(resV, &ir.Instr{
		Op: ir.OpStructNew, Type: st, Args: []ir.Expr{nnData(dataV), useV(offV), useV(nlenV), useV(capV)},
	}))

	growSeq := &ir.Seq{}
	growSeq.Stmts = append(growSeq.Stmts, assignV(twocapV, &ir.Instr{Op: ir.OpI32Mul, Args: []ir.Expr{useV(capV), &ir.Const{Op: ir.OpI32Const, I64: 2}}}))
	growSeq.Stmts = append(growSeq.Stmts, &ir.If{
		Cond: &ir.Instr{Op: ir.OpI32GeU, Args: []ir.Expr{useV(twocapV), useV(nlenV)}},
		Then: &ir.Seq{Stmts: []ir.Stmt{assignV(ncapV, useV(twocapV))}},
		Else: &ir.Seq{Stmts: []ir.Stmt{assignV(ncapV, useV(nlenV))}},
	})
	growSeq.Stmts = append(growSeq.Stmts, &ir.If{
		Cond: &ir.Instr{Op: ir.OpI32LtU, Args: []ir.Expr{useV(ncapV), useV(nlenV)}},
		Then: &ir.Seq{Stmts: rt.panicSliceB()},
	})
	growAlloc, growAllocPre := rt.allocArray(arrName, useV(ncapV))
	growSeq.Stmts = append(growSeq.Stmts, growAllocPre...)
	growSeq.Stmts = append(growSeq.Stmts, assignV(ndataV, growAlloc))
	growSeq.Stmts = append(growSeq.Stmts, &ir.ExprStmt{X: &ir.Instr{
		Op: ir.OpArrayCopy, Type: &ir.Ref{Named: arrName}, Type2: &ir.Ref{Named: arrName},
		Args: []ir.Expr{nnData(ndataV), &ir.Const{Op: ir.OpI32Const, I64: 0}, nnData(dataV), useV(offV), useV(lenV)},
	}})
	for i, el := range elemEx {
		idx := &ir.Instr{Op: ir.OpI32Add, Args: []ir.Expr{useV(lenV), &ir.Const{Op: ir.OpI32Const, I64: int64(i)}}}
		growSeq.Stmts = append(growSeq.Stmts, &ir.ExprStmt{X: &ir.Instr{
			Op: ir.OpArraySet, Type: &ir.Ref{Named: arrName},
			Args: []ir.Expr{nnData(ndataV), idx, el},
		}})
	}
	growSeq.Stmts = append(growSeq.Stmts, assignV(resV, &ir.Instr{
		Op: ir.OpStructNew, Type: st, Args: []ir.Expr{
			nnData(ndataV), &ir.Const{Op: ir.OpI32Const, I64: 0}, useV(nlenV), useV(ncapV),
		},
	}))

	pre = append(pre, &ir.If{Cond: fits, Then: fitSeq, Else: growSeq})
	pre = append(pre, &ir.Assign{Lhs: s.Lhs, Rhs: useV(resV)})
	return pre, nil
}

func (rt *runtimeLower) expandSliceSlice(s *ir.Assign, in *ir.Instr, n *int) ([]ir.Stmt, error) {
	sliceName, arrName := sliceParts(in.Type)
	rt.ensureSliceTypes(sliceName, arrName)
	var pre []ir.Stmt
	args := make([]ir.Expr, len(in.Args))
	for i, a := range in.Args {
		ex, p, err := rt.rewriteExpr(a, n)
		if err != nil {
			return nil, err
		}
		pre = append(pre, p...)
		args[i] = ex
	}
	sl, lo, hi := args[0], args[1], args[2]
	st := &ir.Ref{Named: sliceName}
	newLen := &ir.Instr{Op: ir.OpI32Sub, Args: []ir.Expr{hi, lo}}
	// Bounds: lo > hi → panicSliceB.
	pre = append(pre, &ir.If{
		Cond: &ir.Instr{Op: ir.OpI32GtU, Args: []ir.Expr{lo, hi}},
		Then: &ir.Seq{Stmts: rt.panicSliceB()},
	})

	// Array → slice: base is a bare array ref; build a fresh header.
	baseTy := lenCapArgType(rt.mod, in.Args[0])
	if baseTy == nil {
		baseTy = lenCapArgType(rt.mod, sl)
	}
	if baseArr := arrayIRName(baseTy); baseArr != "" {
		if arrName == "" {
			arrName = baseArr
		}
		dataT := &ir.Temp{Name: rt.fresh("data"), Typ: &ir.Ref{Named: arrName}}
		// Slice headers require non-null data; array fields are refnull.
		nn := &ir.Instr{Op: ir.OpRefAsNonNull, Args: []ir.Expr{sl}}
		pre = append(pre, &ir.Assign{Lhs: []ir.Dest{{Temp: dataT, Name: dataT.Name}}, Rhs: nn})
		alen := &ir.Instr{Op: ir.OpArrayLen, Type: &ir.Ref{Named: arrName}, Args: []ir.Expr{&ir.Name{Temp: dataT, Raw: dataT.Name}}}
		alenT := &ir.Temp{Name: rt.fresh("alen"), Typ: ir.TypeI32}
		pre = append(pre, &ir.Assign{Lhs: []ir.Dest{{Temp: alenT, Name: alenT.Name}}, Rhs: alen})
		alenN := &ir.Name{Temp: alenT, Raw: alenT.Name}
		var newCap ir.Expr
		if len(args) > 3 {
			// s[lo:hi:max] on array: 0 <= lo <= hi <= max <= len(array)
			pre = append(pre, &ir.If{
				Cond: &ir.Instr{Op: ir.OpI32GtU, Args: []ir.Expr{args[3], alenN}},
				Then: &ir.Seq{Stmts: rt.panicSliceB()},
			})
			pre = append(pre, &ir.If{
				Cond: &ir.Instr{Op: ir.OpI32GtU, Args: []ir.Expr{hi, args[3]}},
				Then: &ir.Seq{Stmts: rt.panicSliceB()},
			})
			newCap = &ir.Instr{Op: ir.OpI32Sub, Args: []ir.Expr{args[3], lo}}
		} else {
			// s[lo:hi] on array: 0 <= lo <= hi <= len(array)
			pre = append(pre, &ir.If{
				Cond: &ir.Instr{Op: ir.OpI32GtU, Args: []ir.Expr{hi, alenN}},
				Then: &ir.Seq{Stmts: rt.panicSliceB()},
			})
			newCap = &ir.Instr{Op: ir.OpI32Sub, Args: []ir.Expr{alenN, lo}}
		}
		pre = append(pre, &ir.Assign{
			Lhs: s.Lhs,
			Rhs: &ir.Instr{
				Op:   ir.OpStructNew,
				Type: st,
				Args: []ir.Expr{&ir.Name{Temp: dataT, Raw: dataT.Name}, lo, newLen, newCap},
			},
		})
		return pre, nil
	}

	// nil[:0] is nil in Go (cap 0); nil with hi/max > 0 panics. Slice headers
	// use non-null data, so the nil slice is a null header ref — keep it null.
	nullSlice := &ir.Instr{Op: ir.OpRefNull, Type: st}
	// hi > 0 (or max > 0) on a nil slice → panic (high > cap).
	nilBad := ir.Expr(hi)
	if len(args) > 3 {
		nilBad = &ir.Instr{Op: ir.OpI32Or, Args: []ir.Expr{
			hi,
			args[3],
		}}
	}
	offV := rt.newVar("off", ir.TypeI32)
	capV := rt.newVar("cap", ir.TypeI32)
	// Nullable so the local is defaultable (only set on the non-null path).
	dataV := rt.newVar("data", &ir.Ref{Null: true, Named: arrName})
	resV := rt.newVar("sslice", &ir.Ref{Null: true, Named: sliceName})
	pre = append(pre,
		&ir.VarDef{Var: offV}, &ir.VarDef{Var: capV},
		&ir.VarDef{Var: dataV}, &ir.VarDef{Var: resV},
	)
	newOff := &ir.Instr{Op: ir.OpI32Add, Args: []ir.Expr{useV(offV), lo}}
	var newCap ir.Expr
	boundChecks := []ir.Stmt{
		assignV(offV, &ir.Instr{Op: ir.OpStructGet, Type: st, Field: "off", Args: []ir.Expr{sl}}),
		assignV(capV, &ir.Instr{Op: ir.OpStructGet, Type: st, Field: "cap", Args: []ir.Expr{sl}}),
		assignV(dataV, &ir.Instr{Op: ir.OpStructGet, Type: st, Field: "data", Args: []ir.Expr{sl}}),
	}
	if len(args) > 3 {
		// s[lo:hi:max]: 0 <= lo <= hi <= max <= cap(s)
		boundChecks = append(boundChecks,
			&ir.If{
				Cond: &ir.Instr{Op: ir.OpI32GtU, Args: []ir.Expr{args[3], useV(capV)}},
				Then: &ir.Seq{Stmts: rt.panicSliceB()},
			},
			&ir.If{
				Cond: &ir.Instr{Op: ir.OpI32GtU, Args: []ir.Expr{hi, args[3]}},
				Then: &ir.Seq{Stmts: rt.panicSliceB()},
			},
		)
		newCap = &ir.Instr{Op: ir.OpI32Sub, Args: []ir.Expr{args[3], lo}}
	} else {
		// s[lo:hi]: 0 <= lo <= hi <= cap(s)
		boundChecks = append(boundChecks, &ir.If{
			Cond: &ir.Instr{Op: ir.OpI32GtU, Args: []ir.Expr{hi, useV(capV)}},
			Then: &ir.Seq{Stmts: rt.panicSliceB()},
		})
		newCap = &ir.Instr{Op: ir.OpI32Sub, Args: []ir.Expr{useV(capV), lo}}
	}
	nonNullBody := append(boundChecks, assignV(resV, &ir.Instr{
		Op:   ir.OpStructNew,
		Type: st,
		// data field is non-null in the header; value comes from a live slice.
		Args: []ir.Expr{
			&ir.Instr{Op: ir.OpRefAsNonNull, Args: []ir.Expr{useV(dataV)}},
			newOff, newLen, newCap,
		},
	}))
	pre = append(pre, &ir.If{
		Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{sl}},
		Then: &ir.Seq{Stmts: []ir.Stmt{
			&ir.If{
				Cond: &ir.Instr{Op: ir.OpI32Ne, Args: []ir.Expr{nilBad, &ir.Const{Op: ir.OpI32Const, I64: 0}}},
				Then: &ir.Seq{Stmts: rt.panicSliceB()},
			},
			assignV(resV, nullSlice),
		}},
		Else: &ir.Seq{Stmts: nonNullBody},
	})
	pre = append(pre, &ir.Assign{
		Lhs: s.Lhs,
		Rhs: useV(resV),
	})
	return pre, nil
}

func (rt *runtimeLower) expandSliceGet(s *ir.Assign, in *ir.Instr, n *int) ([]ir.Stmt, error) {
	var pre []ir.Stmt
	sl, p, err := rt.rewriteExpr(in.Args[0], n)
	if err != nil {
		return nil, err
	}
	pre = append(pre, p...)
	idx, p, err := rt.rewriteExpr(in.Args[1], n)
	if err != nil {
		return nil, err
	}
	pre = append(pre, p...)
	sliceName, arrName := rt.sliceTypeFrom(in.Args[0], sl, in.Type)
	if sliceName == "" {
		sliceName = "$go.slice.i32"
		arrName = "$go.array.i32"
	}
	rt.ensureSliceTypes(sliceName, arrName)
	st := &ir.Ref{Named: sliceName}
	lenT := &ir.Temp{Name: rt.fresh("len"), Typ: ir.TypeI32}
	offT := &ir.Temp{Name: rt.fresh("off"), Typ: ir.TypeI32}
	dataT := &ir.Temp{Name: rt.fresh("data"), Typ: &ir.Ref{Named: arrName}}
	pre = append(pre,
		&ir.Assign{Lhs: []ir.Dest{{Temp: lenT, Name: lenT.Name}}, Rhs: &ir.Instr{Op: ir.OpStructGet, Type: st, Field: "len", Args: []ir.Expr{sl}}},
		&ir.Assign{Lhs: []ir.Dest{{Temp: offT, Name: offT.Name}}, Rhs: &ir.Instr{Op: ir.OpStructGet, Type: st, Field: "off", Args: []ir.Expr{sl}}},
		&ir.Assign{Lhs: []ir.Dest{{Temp: dataT, Name: dataT.Name}}, Rhs: &ir.Instr{Op: ir.OpStructGet, Type: st, Field: "data", Args: []ir.Expr{sl}}},
	)
	pre = append(pre, &ir.If{
		Cond: &ir.Instr{Op: ir.OpI32GeU, Args: []ir.Expr{idx, &ir.Name{Temp: lenT, Raw: lenT.Name}}},
		Then: &ir.Seq{Stmts: rt.panicIndex(idx, &ir.Name{Temp: lenT, Raw: lenT.Name})},
	})
	at := &ir.Instr{Op: ir.OpI32Add, Args: []ir.Expr{&ir.Name{Temp: offT, Raw: offT.Name}, idx}}
	getOp := ir.OpArrayGet
	switch arrName {
	case "$go.bytes", "$go.i16s":
		// Packed i8/i16 arrays require get_u (or get_s); plain get is invalid.
		getOp = ir.OpArrayGetU
	}
	pre = append(pre, &ir.Assign{
		Lhs: s.Lhs,
		Rhs: &ir.Instr{Op: getOp, Type: &ir.Ref{Named: arrName}, Args: []ir.Expr{&ir.Name{Temp: dataT, Raw: dataT.Name}, at}},
	})
	return pre, nil
}

func (rt *runtimeLower) expandSliceSet(in *ir.Instr, n *int) ([]ir.Stmt, error) {
	var pre []ir.Stmt
	sl, p, err := rt.rewriteExpr(in.Args[0], n)
	if err != nil {
		return nil, err
	}
	pre = append(pre, p...)
	idx, p, err := rt.rewriteExpr(in.Args[1], n)
	if err != nil {
		return nil, err
	}
	pre = append(pre, p...)
	val, p, err := rt.rewriteExpr(in.Args[2], n)
	if err != nil {
		return nil, err
	}
	pre = append(pre, p...)
	sliceName, arrName := rt.sliceTypeFrom(in.Args[0], sl, nil)
	if sliceName == "" {
		sliceName, arrName = "$go.slice.i32", "$go.array.i32"
	}
	rt.ensureSliceTypes(sliceName, arrName)
	st := &ir.Ref{Named: sliceName}
	lenT := &ir.Temp{Name: rt.fresh("len"), Typ: ir.TypeI32}
	offT := &ir.Temp{Name: rt.fresh("off"), Typ: ir.TypeI32}
	dataT := &ir.Temp{Name: rt.fresh("data"), Typ: &ir.Ref{Named: arrName}}
	pre = append(pre,
		&ir.Assign{Lhs: []ir.Dest{{Temp: lenT, Name: lenT.Name}}, Rhs: &ir.Instr{Op: ir.OpStructGet, Type: st, Field: "len", Args: []ir.Expr{sl}}},
		&ir.Assign{Lhs: []ir.Dest{{Temp: offT, Name: offT.Name}}, Rhs: &ir.Instr{Op: ir.OpStructGet, Type: st, Field: "off", Args: []ir.Expr{sl}}},
		&ir.Assign{Lhs: []ir.Dest{{Temp: dataT, Name: dataT.Name}}, Rhs: &ir.Instr{Op: ir.OpStructGet, Type: st, Field: "data", Args: []ir.Expr{sl}}},
		&ir.If{
			Cond: &ir.Instr{Op: ir.OpI32GeU, Args: []ir.Expr{idx, &ir.Name{Temp: lenT, Raw: lenT.Name}}},
			Then: &ir.Seq{Stmts: rt.panicIndex(idx, &ir.Name{Temp: lenT, Raw: lenT.Name})},
		},
		&ir.ExprStmt{X: &ir.Instr{
			Op:   ir.OpArraySet,
			Type: &ir.Ref{Named: arrName},
			Args: []ir.Expr{&ir.Name{Temp: dataT, Raw: dataT.Name}, &ir.Instr{Op: ir.OpI32Add, Args: []ir.Expr{&ir.Name{Temp: offT, Raw: offT.Name}, idx}}, val},
		}},
	)
	return pre, nil
}

func (rt *runtimeLower) expandLenCap(s *ir.Assign, in *ir.Instr, n *int) ([]ir.Stmt, error) {
	arg, pre, err := rt.rewriteExpr(in.Args[0], n)
	if err != nil {
		return nil, err
	}
	field := "len"
	if in.Op == ir.OpCap {
		field = "cap"
	}
	// Determine type from arg (var/temp/global — package []T uses global.get).
	ty := lenCapArgType(rt.mod, in.Args[0])
	if ty == nil {
		ty = lenCapArgType(rt.mod, arg)
	}
	if ty == nil {
		ty = &ir.Ref{Named: "$go.slice.i32"}
	}
	if r, ok := ty.(*ir.Ref); ok && r.Named == "$go.string" {
		if in.Op == ir.OpCap {
			return nil, fmt.Errorf("cap of string")
		}
		pre = append(pre, &ir.Assign{
			Lhs: s.Lhs,
			Rhs: &ir.Instr{Op: ir.OpStructGet, Type: &ir.Ref{Named: "$go.string"}, Field: "len", Args: []ir.Expr{arg}},
		})
		return pre, nil
	}
	// Channel: len → qcount, cap → dataqsiz; nil chan → 0.
	if r, ok := ty.(*ir.Ref); ok && r.Named == "$go.chan" {
		chanField := "qcount"
		if in.Op == ir.OpCap {
			chanField = "dataqsiz"
		}
		chV := rt.newVar("lch", ty)
		pre = append(pre, &ir.VarDef{Var: chV}, assignV(chV, arg))
		resV := rt.newVar("ln", ir.TypeI32)
		pre = append(pre, &ir.VarDef{Var: resV}, assignV(resV, &ir.Const{Op: ir.OpI32Const, I64: 0}))
		pre = append(pre, &ir.If{
			Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{useV(chV)}},
			Then: &ir.Seq{},
			Else: &ir.Seq{Stmts: []ir.Stmt{assignV(resV, &ir.Instr{
				Op: ir.OpStructGet, Type: &ir.Ref{Named: "$go.chan"}, Field: chanField,
				Args: []ir.Expr{useV(chV)},
			})}},
		})
		pre = append(pre, &ir.Assign{Lhs: s.Lhs, Rhs: useV(resV)})
		return pre, nil
	}
	// Map: len → used (or runtime.mapLen); cap of map is illegal in Go.
	if r, ok := ty.(*ir.Ref); ok && r.Named == "$go.hmap" {
		if in.Op == ir.OpCap {
			return nil, fmt.Errorf("cap of map")
		}
		call := &ir.Instr{Op: ir.OpCall, Func: "runtime.mapLen", Args: []ir.Expr{arg}}
		pre = append(pre, &ir.Assign{Lhs: s.Lhs, Rhs: call})
		return pre, nil
	}
	// Go arrays are bare Wasm array refs ($go.bytes / $go.array.*); len/cap = array.len.
	if arrName := arrayIRName(ty); arrName != "" {
		pre = append(pre, &ir.Assign{
			Lhs: s.Lhs,
			Rhs: &ir.Instr{Op: ir.OpArrayLen, Type: &ir.Ref{Named: arrName}, Args: []ir.Expr{arg}},
		})
		return pre, nil
	}
	// Slices (and slice-shaped headers): nil → len/cap 0.
	if r, ok := ty.(*ir.Ref); ok && (strings.HasPrefix(r.Named, "$go.slice.") || r.Named == "$go.bytes" || r.Named == "$go.i16s") {
		slV := rt.newVar("lsl", ty)
		pre = append(pre, &ir.VarDef{Var: slV}, assignV(slV, arg))
		resV := rt.newVar("ln", ir.TypeI32)
		pre = append(pre, &ir.VarDef{Var: resV}, assignV(resV, &ir.Const{Op: ir.OpI32Const, I64: 0}))
		pre = append(pre, &ir.If{
			Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{useV(slV)}},
			Then: &ir.Seq{},
			Else: &ir.Seq{Stmts: []ir.Stmt{assignV(resV, &ir.Instr{
				Op: ir.OpStructGet, Type: ty, Field: field,
				Args: []ir.Expr{useV(slV)},
			})}},
		})
		pre = append(pre, &ir.Assign{Lhs: s.Lhs, Rhs: useV(resV)})
		return pre, nil
	}
	pre = append(pre, &ir.Assign{
		Lhs: s.Lhs,
		Rhs: &ir.Instr{Op: ir.OpStructGet, Type: ty, Field: field, Args: []ir.Expr{arg}},
	})
	return pre, nil
}

// arrayIRName returns the Wasm array type name if t is a bare array ref
// (Go [N]T), else "".
func arrayIRName(t ir.Type) string {
	r, ok := t.(*ir.Ref)
	if !ok || r.Named == "" {
		return ""
	}
	switch {
	case r.Named == "$go.bytes", r.Named == "$go.i16s",
		strings.HasPrefix(r.Named, "$go.array."):
		return r.Named
	}
	return ""
}

func lenCapArgType(mod *ir.Module, e ir.Expr) ir.Type {
	switch e := e.(type) {
	case *ir.Name:
		if e.Var != nil && e.Var.Typ != nil {
			return e.Var.Typ
		}
		if e.Temp != nil && e.Temp.Typ != nil {
			return e.Temp.Typ
		}
	case *ir.Instr:
		if e.Op == ir.OpGlobalGet && mod != nil {
			if g := mod.LookupGlobal(e.Global); g != nil && g.Typ != nil {
				return g.Typ
			}
		}
		// struct.get's Type is the container; resolve the field's type.
		if e.Op == ir.OpStructGet && e.Field != "" && e.Type != nil && mod != nil {
			if r, ok := e.Type.(*ir.Ref); ok && r.Named != "" {
				if td := mod.LookupType(r.Named); td != nil {
					for _, f := range td.Fields {
						if f.Name == e.Field {
							return f.Type
						}
					}
				}
			}
		}
		// array.get's Type is the ARRAY (for codegen); len/cap need the element.
		// e.g. len(replacements[b]) where replacements is [256][]byte.
		if (e.Op == ir.OpArrayGet || e.Op == ir.OpArrayGetU || e.Op == ir.OpArrayGetS) &&
			e.Type != nil && mod != nil {
			if r, ok := e.Type.(*ir.Ref); ok && r.Named != "" {
				if td := mod.LookupType(r.Named); td != nil && td.Elem != nil {
					return td.Elem
				}
				// $go.bytes / $go.i16s → packed numeric, len of element is N/A
				// as a container; treat as i32 (caller won't slice-len it).
				if r.Named == "$go.bytes" || r.Named == "$go.i16s" {
					return ir.TypeI32
				}
			}
		}
		if e.Type != nil {
			return e.Type
		}
		// Call with no Type: use the callee's first result (concatstrings → string).
		if e.Op == ir.OpCall && e.Func != "" && mod != nil {
			if f := mod.LookupFunc(e.Func); f != nil && len(f.Results) > 0 {
				return f.Results[0]
			}
		}
	}
	return nil
}

func (rt *runtimeLower) expandStringGet(s *ir.Assign, in *ir.Instr, n *int) ([]ir.Stmt, error) {
	var pre []ir.Stmt
	str, p, err := rt.rewriteExpr(in.Args[0], n)
	if err != nil {
		return nil, err
	}
	pre = append(pre, p...)
	idx, p, err := rt.rewriteExpr(in.Args[1], n)
	if err != nil {
		return nil, err
	}
	pre = append(pre, p...)
	st := &ir.Ref{Named: "$go.string"}
	offT := &ir.Temp{Name: rt.fresh("off"), Typ: ir.TypeI32}
	dataT := &ir.Temp{Name: rt.fresh("data"), Typ: &ir.Ref{Named: "$go.bytes"}}
	pre = append(pre,
		&ir.Assign{Lhs: []ir.Dest{{Temp: offT, Name: offT.Name}}, Rhs: &ir.Instr{Op: ir.OpStructGet, Type: st, Field: "off", Args: []ir.Expr{str}}},
		&ir.Assign{Lhs: []ir.Dest{{Temp: dataT, Name: dataT.Name}}, Rhs: &ir.Instr{Op: ir.OpStructGet, Type: st, Field: "data", Args: []ir.Expr{str}}},
		&ir.Assign{
			Lhs: s.Lhs,
			Rhs: &ir.Instr{Op: ir.OpArrayGetU, Type: &ir.Ref{Named: "$go.bytes"}, Args: []ir.Expr{
				&ir.Name{Temp: dataT, Raw: dataT.Name},
				&ir.Instr{Op: ir.OpI32Add, Args: []ir.Expr{&ir.Name{Temp: offT, Raw: offT.Name}, idx}},
			}},
		},
	)
	return pre, nil
}

func (rt *runtimeLower) expandAddr(s *ir.Assign, in *ir.Instr, n *int) ([]ir.Stmt, error) {
	// Remaining addr → fat pointer struct.new(class, base, index)
	kind := "i32"
	if r, ok := in.Type.(*ir.Ref); ok && strings.HasPrefix(r.Named, "$go.ptr.") {
		kind = strings.TrimPrefix(r.Named, "$go.ptr.")
	}
	rt.ensureFatPtr(kind)

	var pre []ir.Stmt
	base, p, err := rt.rewriteExpr(in.Args[0], n)
	if err != nil {
		return nil, err
	}
	pre = append(pre, p...)
	index := ir.Expr(&ir.Const{Op: ir.OpI32Const, I64: 0})
	if len(in.Args) > 1 {
		index, p, err = rt.rewriteExpr(in.Args[1], n)
		if err != nil {
			return nil, err
		}
		pre = append(pre, p...)
	}

	// Slice / string headers are not array refs: unwrap to data + off+index
	// so the fat-pointer base is the backing array (docs/design/03, 05).
	if t := exprRefTypeMod(rt.mod, base); strings.HasPrefix(t, "$go.slice.") || t == "$go.string" {
		st := &ir.Ref{Named: t}
		arrName := "$go.bytes"
		if t != "$go.string" {
			arrName = arrayContainerOf(t)
		}
		dataT := &ir.Temp{Name: rt.fresh("ab"), Typ: &ir.Ref{Named: arrName}}
		offT := &ir.Temp{Name: rt.fresh("ao"), Typ: ir.TypeI32}
		pre = append(pre,
			&ir.Assign{Lhs: []ir.Dest{{Temp: dataT, Name: dataT.Name}}, Rhs: &ir.Instr{Op: ir.OpStructGet, Type: st, Field: "data", Args: []ir.Expr{base}}},
			&ir.Assign{Lhs: []ir.Dest{{Temp: offT, Name: offT.Name}}, Rhs: &ir.Instr{Op: ir.OpStructGet, Type: st, Field: "off", Args: []ir.Expr{base}}},
		)
		base = &ir.Name{Temp: dataT, Raw: dataT.Name}
		index = &ir.Instr{Op: ir.OpI32Add, Args: []ir.Expr{&ir.Name{Temp: offT, Raw: offT.Name}, index}}
	}

	container, fieldKey := rt.addrClassKey(in, base)
	classG := rt.classGlobal(container, fieldKey, kind)

	pre = append(pre, &ir.Assign{
		Lhs: s.Lhs,
		Rhs: &ir.Instr{
			Op:   ir.OpStructNew,
			Type: &ir.Ref{Named: "$go.ptr." + kind},
			Args: []ir.Expr{
				&ir.Instr{Op: ir.OpGlobalGet, Global: classG},
				base,
				index,
			},
		},
	})
	return pre, nil
}

func (rt *runtimeLower) addrClassKey(in *ir.Instr, base ir.Expr) (container, field string) {
	// IndexExpr addrOf sets Str:"elem"; a struct field may also be named "elem"
	// (e.g. waiter.elem), so only treat Str as the array-element marker.
	if in.Str == "elem" {
		field = "elem"
		container = "$go.array.i32"
		if t := exprRefTypeMod(rt.mod, base); t != "" {
			container = arrayContainerOf(t)
		}
		return container, field
	}
	if in.Field != "" {
		field = in.Field
		container = "$go.object"
		if t := exprRefTypeMod(rt.mod, base); t != "" {
			container = t
		}
		return container, field
	}
	field = "elem"
	container = "$go.array.i32"
	if t := exprRefTypeMod(rt.mod, base); t != "" {
		container = arrayContainerOf(t)
	}
	return container, field
}

// arrayContainerOf maps a slice/array/iface-array IR type name to the array
// type used for elem fat-pointer accessors.
func arrayContainerOf(t string) string {
	switch {
	case strings.HasPrefix(t, "$go.array."), t == "$go.bytes", t == "$go.i16s":
		return t
	case strings.HasPrefix(t, "$go.slice."):
		rest := strings.TrimPrefix(t, "$go.slice.")
		if rest == "i8" {
			return "$go.bytes"
		}
		if rest == "i16" {
			return "$go.i16s"
		}
		return "$go.array." + rest
	case t == "$go.iface" || strings.HasSuffix(t, ".iface"):
		return "$go.array.go.iface"
	case t == "$go.string":
		// []string / [N]string elem accessors use the string array type;
		// a bare $go.string here means the base type was resolved to the
		// element type — prefer the array of strings.
		return "$go.array.go.string"
	default:
		return t
	}
}

func structTypeOf(base ir.Expr) string {
	return exprRefType(base)
}

func exprRefType(e ir.Expr) string {
	return exprRefTypeMod(nil, e)
}

func exprRefTypeMod(mod *ir.Module, e ir.Expr) string {
	switch b := e.(type) {
	case *ir.Name:
		if b.Var != nil {
			if r, ok := b.Var.Typ.(*ir.Ref); ok {
				return r.Named
			}
		}
		if b.Temp != nil {
			if r, ok := b.Temp.Typ.(*ir.Ref); ok {
				return r.Named
			}
		}
	case *ir.Instr:
		// global.get has no Instr.Type; look up the global's declared type.
		if b.Op == ir.OpGlobalGet && mod != nil && b.Global != "" {
			if g := mod.LookupGlobal(b.Global); g != nil {
				if r, ok := g.Typ.(*ir.Ref); ok {
					return r.Named
				}
			}
		}
		// struct.get's Type is the container; resolve the field type when possible.
		if b.Op == ir.OpStructGet && b.Field != "" && b.Type != nil && mod != nil {
			if r, ok := b.Type.(*ir.Ref); ok && r.Named != "" {
				if td := mod.LookupType(r.Named); td != nil {
					for _, f := range td.Fields {
						if f.Name == b.Field {
							if fr, ok := f.Type.(*ir.Ref); ok {
								return fr.Named
							}
						}
					}
				}
			}
		}
		if r, ok := b.Type.(*ir.Ref); ok {
			return r.Named
		}
	}
	return ""
}

// isObjectPtrIR reports whether named is a bare object/array ref used for
// *Struct / *[N]T (not a fat $go.ptr.*). Includes runtime aliases that
// mapNamedStruct shares with the value type (*hmap, *g, …).
func isObjectPtrIR(named string) bool {
	switch named {
	case "$go.bytes", "$go.i16s",
		"$go.hmap", "$go.mapIter", "$go.chan", "$go.g", "$go.type",
		"$go.waiter", "$go.waitq", "$go.scase", "$go.select",
		"$go.runqnode", "$go.semaRoot", "$go.panicObj", "$go.chanOps":
		return true
	}
	return strings.HasPrefix(named, "$go.struct.") ||
		strings.HasPrefix(named, "$go.array.")
}

func (rt *runtimeLower) expandDeref(s *ir.Assign, in *ir.Instr, n *int) ([]ir.Stmt, error) {
	ptr, pre, err := rt.rewriteExpr(in.Args[0], n)
	if err != nil {
		return nil, err
	}
	// Object pointer (*Struct): the pointer value IS the struct ref.
	// Do not key off in.Type alone (result): fat-ptr deref to a struct element
	// also has result type $go.struct.* but must call_ref get.
	kind := "i32"
	ptrType := "$go.ptr.i32"
	ptrTyp := exprRefTypeMod(rt.mod, in.Args[0])
	if ptrTyp == "" {
		ptrTyp = exprRefTypeMod(rt.mod, ptr)
	}
	switch {
	case strings.HasPrefix(ptrTyp, "$go.ptr."):
		kind = strings.TrimPrefix(ptrTyp, "$go.ptr.")
		ptrType = ptrTyp
	case isObjectPtrIR(ptrTyp):
		// Object pointer: the ref is the value.
		pre = append(pre, &ir.Assign{Lhs: s.Lhs, Rhs: ptr})
		return pre, nil
	default:
		if r, ok := in.Type.(*ir.Ref); ok && r.Named != "" && !strings.HasPrefix(r.Named, "$go.ptr.") {
			kind = "ref"
			ptrType = "$go.ptr.ref"
		}
	}
	rt.ensureFatPtr(kind)
	pt := &ir.Ref{Named: ptrType}
	// Recoverable nil *T before ref.as_non_null (convert4: [0]byte(*nil)).
	pre = append(pre, &ir.If{
		Cond: &ir.Instr{Op: ir.OpRefIsNull, Args: []ir.Expr{ptr}},
		Then: &ir.Seq{Stmts: []ir.Stmt{
			&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: "runtime.panicNil"}},
			&ir.Unreachable{},
		}},
	})
	clT := &ir.Temp{Name: rt.fresh("cl"), Typ: &ir.Ref{Named: "$go.class." + kind}}
	pre = append(pre, &ir.Assign{
		Lhs: []ir.Dest{{Temp: clT, Name: clT.Name}},
		Rhs: &ir.Instr{Op: ir.OpStructGet, Type: pt, Field: "class", Args: []ir.Expr{&ir.Instr{Op: ir.OpRefAsNonNull, Args: []ir.Expr{ptr}}}},
	})
	getFn := &ir.Instr{Op: ir.OpStructGet, Type: &ir.Ref{Named: "$go.class." + kind}, Field: "get", Args: []ir.Expr{&ir.Name{Temp: clT, Raw: clT.Name}}}
	base := &ir.Instr{Op: ir.OpStructGet, Type: pt, Field: "base", Args: []ir.Expr{&ir.Instr{Op: ir.OpRefAsNonNull, Args: []ir.Expr{ptr}}}}
	index := &ir.Instr{Op: ir.OpStructGet, Type: pt, Field: "index", Args: []ir.Expr{&ir.Instr{Op: ir.OpRefAsNonNull, Args: []ir.Expr{ptr}}}}
	loaded := ir.Expr(&ir.Instr{
		Op:   ir.OpCallRef,
		Type: &ir.Ref{Named: "$go.get." + kind},
		Args: []ir.Expr{base, index, getFn},
	})
	// $go.get.ref returns anyref; cast to the concrete result type.
	if kind == "ref" && in.Type != nil {
		loaded = &ir.Instr{Op: ir.OpRefCast, Type: in.Type, Args: []ir.Expr{loaded}}
	}
	pre = append(pre, &ir.Assign{Lhs: s.Lhs, Rhs: loaded})
	return pre, nil
}

func (rt *runtimeLower) expandStore(in *ir.Instr, n *int) ([]ir.Stmt, error) {
	ptr, pre, err := rt.rewriteExpr(in.Args[0], n)
	if err != nil {
		return nil, err
	}
	val, p, err := rt.rewriteExpr(in.Args[1], n)
	if err != nil {
		return nil, err
	}
	pre = append(pre, p...)

	// Object pointer store of whole struct not handled here (field stores use fat/struct.set).
	kind := "i32"
	ptrType := "$go.ptr.i32"
	ptrTyp := exprRefTypeMod(rt.mod, in.Args[0])
	if ptrTyp == "" {
		ptrTyp = exprRefTypeMod(rt.mod, ptr)
	}
	switch {
	case strings.HasPrefix(ptrTyp, "$go.struct."):
		return nil, fmt.Errorf("store through object pointer of whole struct not lowered (ptr=%s val=%v)", ptrTyp, exprRefTypeMod(rt.mod, in.Args[1]))
	case strings.HasPrefix(ptrTyp, "$go.ptr."):
		kind = strings.TrimPrefix(ptrTyp, "$go.ptr.")
		ptrType = ptrTyp
	default:
		// Infer from the stored value when the pointer expr has no named type
		// (e.g. struct.get of a *[]byte field → $go.ptr.ref).
		valNamed := exprRefTypeMod(rt.mod, in.Args[1])
		if valNamed == "" {
			valNamed = exprRefTypeMod(rt.mod, val)
		}
		if valNamed != "" && !strings.HasPrefix(valNamed, "$go.ptr.") {
			kind = "ref"
			ptrType = "$go.ptr.ref"
		}
	}
	rt.ensureFatPtr(kind)
	pt := &ir.Ref{Named: ptrType}
	clT := &ir.Temp{Name: rt.fresh("cl"), Typ: &ir.Ref{Named: "$go.class." + kind}}
	nn := &ir.Instr{Op: ir.OpRefAsNonNull, Args: []ir.Expr{ptr}}
	pre = append(pre, &ir.Assign{
		Lhs: []ir.Dest{{Temp: clT, Name: clT.Name}},
		Rhs: &ir.Instr{Op: ir.OpStructGet, Type: pt, Field: "class", Args: []ir.Expr{nn}},
	})
	setFn := &ir.Instr{Op: ir.OpStructGet, Type: &ir.Ref{Named: "$go.class." + kind}, Field: "set", Args: []ir.Expr{&ir.Name{Temp: clT, Raw: clT.Name}}}
	base := &ir.Instr{Op: ir.OpStructGet, Type: pt, Field: "base", Args: []ir.Expr{&ir.Instr{Op: ir.OpRefAsNonNull, Args: []ir.Expr{ptr}}}}
	index := &ir.Instr{Op: ir.OpStructGet, Type: pt, Field: "index", Args: []ir.Expr{&ir.Instr{Op: ir.OpRefAsNonNull, Args: []ir.Expr{ptr}}}}
	storeVal := val
	if kind == "ref" {
		storeVal = &ir.Instr{Op: ir.OpRefCast, Type: &ir.Ref{Null: true, Heap: ir.HeapAny}, Args: []ir.Expr{val}}
	}
	pre = append(pre, &ir.ExprStmt{X: &ir.Instr{
		Op:   ir.OpCallRef,
		Type: &ir.Ref{Named: "$go.set." + kind},
		Args: []ir.Expr{base, index, storeVal, setFn},
	}})
	return pre, nil
}

func (rt *runtimeLower) ensureFatPtr(kind string) {
	getName := "$go.get." + kind
	setName := "$go.set." + kind
	className := "$go.class." + kind
	ptrName := "$go.ptr." + kind
	var val ir.Type = ir.TypeI32
	switch kind {
	case "i64":
		val = ir.TypeI64
	case "f32":
		val = ir.TypeF32
	case "f64":
		val = ir.TypeF64
	case "ref":
		val = &ir.Ref{Null: true, Heap: ir.HeapAny}
	}
	rt.mod.InternType(&ir.TypeDef{Name: getName, Kind: ir.TypeFunc, Params: []ir.Type{&ir.Ref{Null: true, Heap: ir.HeapAny}, ir.TypeI32}, Results: []ir.Type{val}})
	rt.mod.InternType(&ir.TypeDef{Name: setName, Kind: ir.TypeFunc, Params: []ir.Type{&ir.Ref{Null: true, Heap: ir.HeapAny}, ir.TypeI32, val}})
	rt.mod.InternType(&ir.TypeDef{
		Name: className, Kind: ir.TypeStruct, Super: "$go.object",
		Fields: []ir.Field{
			{Name: "get", Type: &ir.Ref{Named: getName}},
			{Name: "set", Type: &ir.Ref{Named: setName}},
		},
	})
	rt.mod.InternType(&ir.TypeDef{
		Name: ptrName, Kind: ir.TypeStruct, Super: "$go.object",
		Fields: []ir.Field{
			{Name: "class", Type: &ir.Ref{Named: className}},
			{Name: "base", Type: &ir.Ref{Null: true, Heap: ir.HeapAny}},
			{Name: "index", Type: ir.TypeI32},
		},
	})
}

func (rt *runtimeLower) classGlobal(container, field, kind string) string {
	key := container + "." + field + "." + kind
	if g, ok := rt.classes[key]; ok {
		return g
	}
	gname := "$go.class." + sanitize(container) + "." + field
	rt.classes[key] = gname
	rt.ensureFatPtr(kind)

	getFn := rt.ensureAccessor(container, field, kind, true)
	setFn := rt.ensureAccessor(container, field, kind, false)

	// Idempotent across multiple RuntimeOps passes (pipeline runs twice).
	if rt.mod.LookupGlobal(gname) != nil {
		return gname
	}
	init := &ir.Instr{
		Op:   ir.OpStructNew,
		Type: &ir.Ref{Named: "$go.class." + kind},
		Args: []ir.Expr{
			&ir.Instr{Op: ir.OpRefFunc, Func: getFn},
			&ir.Instr{Op: ir.OpRefFunc, Func: setFn},
		},
	}
	rt.mod.Globals = append(rt.mod.Globals, &ir.Global{
		Name: gname,
		Typ:  &ir.Ref{Named: "$go.class." + kind},
		Mut:  false,
		Init: init,
	})
	return gname
}

func sanitize(s string) string {
	s = strings.TrimPrefix(s, "$")
	return strings.ReplaceAll(s, ".", "_")
}

func (rt *runtimeLower) ensureAccessor(container, field, kind string, isGet bool) string {
	op := "get"
	if !isGet {
		op = "set"
	}
	name := "$go.acc." + sanitize(container) + "." + field + "." + op
	if rt.accessors[name] {
		return name
	}
	rt.accessors[name] = true
	// Already emitted by a prior RuntimeOps pass.
	if rt.mod.LookupFunc(name) != nil {
		return name
	}
	rt.ensureFatPtr(kind)

	var val ir.Type = ir.TypeI32
	switch kind {
	case "i64":
		val = ir.TypeI64
	case "f32":
		val = ir.TypeF32
	case "f64":
		val = ir.TypeF64
	case "ref":
		val = &ir.Ref{Null: true, Heap: ir.HeapAny}
	}

	base := &ir.Var{Name: "b", Typ: &ir.Ref{Null: true, Heap: ir.HeapAny}}
	idx := &ir.Var{Name: "i", Typ: ir.TypeI32}
	cast := &ir.Instr{Op: ir.OpRefCast, Type: &ir.Ref{Named: container}, Args: []ir.Expr{&ir.Name{Raw: "b", Var: base}}}

	var body ir.Region
	// "elem" is the synthetic field name for array/slice index addresses.
	// A real struct field may also be named elem (waiter.elem); only use the
	// array path when the container is an array type.
	// Normalize mistaken non-array containers for elem accessors.
	if field == "elem" {
		container = arrayContainerOf(container)
	}
	isArrayElem := field == "elem" && (strings.HasPrefix(container, "$go.array.") ||
		container == "$go.bytes" || container == "$go.i16s")
	castToVal := func(ex ir.Expr) ir.Expr {
		if kind == "ref" {
			return &ir.Instr{Op: ir.OpRefCast, Type: val, Args: []ir.Expr{ex}}
		}
		return ex
	}
	if isArrayElem {
		if isGet {
			getOp := ir.OpArrayGet
			if container == "$go.bytes" || container == "$go.i16s" {
				getOp = ir.OpArrayGetU
			}
			body = &ir.Seq{Stmts: []ir.Stmt{
				&ir.Return{Results: []ir.Expr{castToVal(&ir.Instr{
					Op: getOp, Type: &ir.Ref{Named: container},
					Args: []ir.Expr{cast, &ir.Name{Raw: "i", Var: idx}},
				})}},
			}}
		} else {
			v := &ir.Var{Name: "v", Typ: val}
			setVal := ir.Expr(&ir.Name{Raw: "v", Var: v})
			if kind == "ref" {
				// Caller passes anyref; cast to element type for array.set.
				elemTy := elemOfArrayName(container)
				if elemTy != nil {
					setVal = &ir.Instr{Op: ir.OpRefCast, Type: elemTy, Args: []ir.Expr{setVal}}
				}
			}
			body = &ir.Seq{Stmts: []ir.Stmt{
				&ir.ExprStmt{X: &ir.Instr{
					Op: ir.OpArraySet, Type: &ir.Ref{Named: container},
					Args: []ir.Expr{cast, &ir.Name{Raw: "i", Var: idx}, setVal},
				}},
				&ir.Return{},
			}}
		}
	} else {
		if isGet {
			body = &ir.Seq{Stmts: []ir.Stmt{
				&ir.Return{Results: []ir.Expr{castToVal(&ir.Instr{
					Op: ir.OpStructGet, Type: &ir.Ref{Named: container}, Field: field,
					Args: []ir.Expr{cast},
				})}},
			}}
		} else {
			v := &ir.Var{Name: "v", Typ: val}
			setVal := ir.Expr(&ir.Name{Raw: "v", Var: v})
			if kind == "ref" {
				// Caller passes anyref; cast to the struct field's concrete type.
				if td := rt.mod.LookupType(container); td != nil {
					for _, f := range td.Fields {
						if f.Name == field {
							setVal = &ir.Instr{Op: ir.OpRefCast, Type: f.Type, Args: []ir.Expr{setVal}}
							break
						}
					}
				}
			}
			body = &ir.Seq{Stmts: []ir.Stmt{
				&ir.ExprStmt{X: &ir.Instr{
					Op: ir.OpStructSet, Type: &ir.Ref{Named: container}, Field: field,
					Args: []ir.Expr{cast, setVal},
				}},
				&ir.Return{},
			}}
		}
	}

	params := []*ir.Var{base, idx}
	results := []ir.Type{}
	if isGet {
		results = []ir.Type{val}
	} else {
		params = append(params, &ir.Var{Name: "v", Typ: val})
	}
	rt.mod.Funcs = append(rt.mod.Funcs, &ir.Func{
		Name:    name,
		Params:  params,
		Results: results,
		Body:    body,
	})
	return name
}

func ensureEmptyBytesGlobal(m *ir.Module) {
	const gname = "$go.emptyBytes"
	if m.LookupGlobal(gname) != nil {
		return
	}
	m.Globals = append(m.Globals, &ir.Global{
		Name: gname,
		Typ:  &ir.Ref{Named: "$go.bytes"},
		Mut:  false,
		Init: &ir.Instr{
			Op:   ir.OpArrayNewDefault,
			Type: &ir.Ref{Named: "$go.bytes"},
			Args: []ir.Expr{&ir.Const{Op: ir.OpI32Const, I64: 0}},
		},
	})
}

func expandStringAssign(m *ir.Module, s *ir.Assign, str string, n *int) ([]ir.Stmt, error) {
	if len(s.Lhs) != 1 {
		return nil, fmt.Errorf("string literal assign wants 1 lhs")
	}
	// Unique temp names via pointer identity (empty strings do not append Data,
	// so len(m.Data)-based counters would collide).
	dataT := &ir.Temp{Typ: &ir.Ref{Named: "$go.bytes"}}
	dataT.Name = fmt.Sprintf("%%strdata_%p", dataT)

	var mkData ir.Stmt
	if str == "" {
		// Canonical empty backing so unsafe.StringData of every empty string
		// compares equal (strings.TestClone).
		ensureEmptyBytesGlobal(m)
		mkData = &ir.Assign{
			Lhs: []ir.Dest{{Temp: dataT, Name: dataT.Name}},
			Rhs: &ir.Instr{Op: ir.OpGlobalGet, Global: "$go.emptyBytes"},
		}
	} else {
		// Use module-wide unique names: a second RuntimeOps pass (after Ensure*)
		// must not reuse $lit0… and clobber earlier string data (e.g. "main" →
		// "runtime error: …" printing as "runt").
		litName := fmt.Sprintf("$lit%d", len(m.Data))
		m.Data = append(m.Data, &ir.Data{Name: litName, Bytes: []byte(str)})
		if n != nil && *n < len(m.Data)+1 {
			*n = len(m.Data) + 1
		}
		mkData = &ir.Assign{
			Lhs: []ir.Dest{{Temp: dataT, Name: dataT.Name}},
			Rhs: &ir.Instr{
				Op:   ir.OpArrayNewData,
				Type: &ir.Ref{Named: "$go.bytes"},
				Data: litName,
			},
		}
	}
	strNew := &ir.Instr{
		Op:   ir.OpStructNew,
		Type: &ir.Ref{Named: "$go.string"},
		Args: []ir.Expr{
			&ir.Name{Temp: dataT, Raw: dataT.Name},
			&ir.Const{Op: ir.OpI32Const, I64: 0},
			&ir.Const{Op: ir.OpI32Const, I64: int64(len(str))},
		},
	}
	// Preserve var destinations; only synthesize a temp when the lhs is already a temp
	// or a bare name that is not a module var (call-arg materialization).
	lhs := s.Lhs[0]
	if lhs.Var != nil {
		return []ir.Stmt{mkData, &ir.Assign{Lhs: []ir.Dest{lhs}, Rhs: strNew}}, nil
	}
	strT := lhs.Temp
	if strT == nil {
		strT = &ir.Temp{Typ: &ir.Ref{Named: "$go.string"}}
		strT.Name = fmt.Sprintf("%%str_%p", strT)
		if n != nil {
			*n++
		}
	} else {
		strT.Typ = &ir.Ref{Named: "$go.string"}
	}
	mkStr := &ir.Assign{
		Lhs: []ir.Dest{{Temp: strT, Name: strT.Name}},
		Rhs: strNew,
	}
	return []ir.Stmt{mkData, mkStr}, nil
}
