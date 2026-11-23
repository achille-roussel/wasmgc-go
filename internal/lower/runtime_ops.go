package lower

import (
	"fmt"
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

// newVar allocates a mutable local name for values that cross region boundaries.
func (rt *runtimeLower) newVar(prefix string, typ ir.Type) *ir.Var {
	rt.tmp++
	return &ir.Var{Name: fmt.Sprintf("%s%d", prefix, rt.tmp), Typ: typ}
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
		cond, pre, err := rt.rewriteExpr(r.Cond, n)
		if err != nil {
			return nil, err
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
			t := &ir.Temp{Name: fmt.Sprintf("%%str%d", *n), Typ: &ir.Ref{Named: "$go.string"}}
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
			t := &ir.Temp{Name: fmt.Sprintf("%%str%d", *n), Typ: &ir.Ref{Named: "$go.string"}}
			*n++
			assign := &ir.Assign{Lhs: []ir.Dest{{Temp: t, Name: t.Name}}, Rhs: e}
			stmts, err := expandStringAssign(rt.mod, assign, e.Str, n)
			if err != nil {
				return nil, nil, err
			}
			return &ir.Name{Raw: t.Name, Temp: t}, stmts, nil
		}
		if e.Op.IsHigh() {
			tname := rt.fresh("h")
			ty := e.Type
			if ty == nil {
				ty = ir.TypeI32
			}
			t := &ir.Temp{Name: tname, Typ: ty}
			assign := &ir.Assign{Lhs: []ir.Dest{{Temp: t, Name: tname}}, Rhs: e}
			stmts, err := rt.expandHighAssign(assign, e, n)
			if err != nil {
				return nil, nil, err
			}
			return &ir.Name{Raw: tname, Temp: t}, stmts, nil
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
	dataT := &ir.Temp{Name: rt.fresh("data"), Typ: &ir.Ref{Named: arrName}}
	pre = append(pre, &ir.Assign{
		Lhs: []ir.Dest{{Temp: dataT, Name: dataT.Name}},
		Rhs: &ir.Instr{Op: ir.OpArrayNewDefault, Type: &ir.Ref{Named: arrName}, Args: []ir.Expr{capEx}},
	})
	pre = append(pre, &ir.Assign{
		Lhs: s.Lhs,
		Rhs: &ir.Instr{
			Op:   ir.OpStructNew,
			Type: &ir.Ref{Named: sliceName},
			Args: []ir.Expr{
				&ir.Name{Temp: dataT, Raw: dataT.Name},
				&ir.Const{Op: ir.OpI32Const, I64: 0},
				lenEx,
				capEx,
			},
		},
	})
	return pre, nil
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
	suf := strings.TrimPrefix(arrName, "$go.array.")
	switch suf {
	case "i32":
		return ir.TypeI32
	case "i64":
		return ir.TypeI64
	case "f32":
		return ir.TypeF32
	case "f64":
		return ir.TypeF64
	default:
		return ir.TypeI32
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
	if length == 0 {
		pre = append(pre, &ir.Assign{
			Lhs: []ir.Dest{{Temp: dataT, Name: dataT.Name}},
			Rhs: &ir.Instr{Op: ir.OpArrayNewDefault, Type: &ir.Ref{Named: arrName}, Args: []ir.Expr{&ir.Const{Op: ir.OpI32Const, I64: 0}}},
		})
	} else {
		pre = append(pre, &ir.Assign{
			Lhs: []ir.Dest{{Temp: dataT, Name: dataT.Name}},
			Rhs: &ir.Instr{Op: ir.OpArrayNewFixed, Type: &ir.Ref{Named: arrName}, Args: elems},
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
	pre = append(pre, &ir.Assign{
		Lhs: s.Lhs,
		Rhs: &ir.Instr{Op: ir.OpArrayNewFixed, Type: in.Type, Args: args},
	})
	return pre, nil
}

func (rt *runtimeLower) expandNew(s *ir.Assign, in *ir.Instr, n *int) ([]ir.Stmt, error) {
	// new(T) / literal of ptr type: 1-elem array + fat pointer at index 0,
	// or struct.new_default for *Struct.
	tname := ""
	if r, ok := in.Type.(*ir.Ref); ok {
		tname = r.Named
	}
	if strings.HasPrefix(tname, "$go.struct.") {
		return []ir.Stmt{&ir.Assign{
			Lhs: s.Lhs,
			Rhs: &ir.Instr{Op: ir.OpStructNewDefault, Type: in.Type},
		}}, nil
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
	nilSeq := &ir.Seq{Stmts: []ir.Stmt{
		assignV(lenV, &ir.Const{Op: ir.OpI32Const, I64: 0}),
		assignV(capV, &ir.Const{Op: ir.OpI32Const, I64: 0}),
		assignV(offV, &ir.Const{Op: ir.OpI32Const, I64: 0}),
		assignV(dataV, &ir.Instr{Op: ir.OpArrayNewDefault, Type: &ir.Ref{Named: arrName}, Args: []ir.Expr{&ir.Const{Op: ir.OpI32Const, I64: 0}}}),
	}}
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
	fitSeq.Stmts = append(fitSeq.Stmts, &ir.Assign{
		Lhs: s.Lhs,
		Rhs: &ir.Instr{Op: ir.OpStructNew, Type: st, Args: []ir.Expr{nnData(dataV), useV(offV), useV(nlenV), useV(capV)}},
	})

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
	growSeq.Stmts = append(growSeq.Stmts, assignV(ndataV, &ir.Instr{
		Op: ir.OpArrayNewDefault, Type: &ir.Ref{Named: arrName}, Args: []ir.Expr{useV(ncapV)},
	}))
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
	growSeq.Stmts = append(growSeq.Stmts, &ir.Assign{
		Lhs: s.Lhs,
		Rhs: &ir.Instr{Op: ir.OpStructNew, Type: st, Args: []ir.Expr{
			nnData(ndataV), &ir.Const{Op: ir.OpI32Const, I64: 0}, useV(nlenV), useV(ncapV),
		}},
	})

	pre = append(pre, &ir.If{Cond: fits, Then: fitSeq, Else: growSeq})
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
	offT := &ir.Temp{Name: rt.fresh("off"), Typ: ir.TypeI32}
	capT := &ir.Temp{Name: rt.fresh("cap"), Typ: ir.TypeI32}
	dataT := &ir.Temp{Name: rt.fresh("data"), Typ: &ir.Ref{Named: arrName}}
	pre = append(pre,
		&ir.Assign{Lhs: []ir.Dest{{Temp: offT, Name: offT.Name}}, Rhs: &ir.Instr{Op: ir.OpStructGet, Type: st, Field: "off", Args: []ir.Expr{sl}}},
		&ir.Assign{Lhs: []ir.Dest{{Temp: capT, Name: capT.Name}}, Rhs: &ir.Instr{Op: ir.OpStructGet, Type: st, Field: "cap", Args: []ir.Expr{sl}}},
		&ir.Assign{Lhs: []ir.Dest{{Temp: dataT, Name: dataT.Name}}, Rhs: &ir.Instr{Op: ir.OpStructGet, Type: st, Field: "data", Args: []ir.Expr{sl}}},
	)
	newOff := &ir.Instr{Op: ir.OpI32Add, Args: []ir.Expr{&ir.Name{Temp: offT, Raw: offT.Name}, lo}}
	newLen := &ir.Instr{Op: ir.OpI32Sub, Args: []ir.Expr{hi, lo}}
	var newCap ir.Expr
	if len(args) > 3 {
		newCap = &ir.Instr{Op: ir.OpI32Sub, Args: []ir.Expr{args[3], lo}}
	} else {
		newCap = &ir.Instr{Op: ir.OpI32Sub, Args: []ir.Expr{&ir.Name{Temp: capT, Raw: capT.Name}, lo}}
	}
	// Bounds: lo > hi → panicSliceB (trap until M5).
	pre = append(pre, &ir.If{
		Cond: &ir.Instr{Op: ir.OpI32GtU, Args: []ir.Expr{lo, hi}},
		Then: &ir.Seq{Stmts: rt.panicSliceB()},
	})
	pre = append(pre, &ir.Assign{
		Lhs: s.Lhs,
		Rhs: &ir.Instr{
			Op:   ir.OpStructNew,
			Type: st,
			Args: []ir.Expr{&ir.Name{Temp: dataT, Raw: dataT.Name}, newOff, newLen, newCap},
		},
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
	if arrName == "$go.bytes" {
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
	pre = append(pre, &ir.Assign{
		Lhs: s.Lhs,
		Rhs: &ir.Instr{Op: ir.OpStructGet, Type: ty, Field: field, Args: []ir.Expr{arg}},
	})
	return pre, nil
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
		if e.Type != nil {
			return e.Type
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
		if t := structTypeOf(base); t != "" {
			container = t
		}
		return container, field
	}
	if in.Field != "" {
		field = in.Field
		container = "$go.object"
		if t := structTypeOf(base); t != "" {
			container = t
		}
		return container, field
	}
	field = "elem"
	container = "$go.array.i32"
	if t := structTypeOf(base); t != "" {
		container = t
	}
	return container, field
}

func structTypeOf(base ir.Expr) string {
	switch b := base.(type) {
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
		if r, ok := b.Type.(*ir.Ref); ok {
			return r.Named
		}
	}
	return ""
}

func (rt *runtimeLower) expandDeref(s *ir.Assign, in *ir.Instr, n *int) ([]ir.Stmt, error) {
	ptr, pre, err := rt.rewriteExpr(in.Args[0], n)
	if err != nil {
		return nil, err
	}
	// Object pointer (*Struct): value is the ref itself for reads of the object;
	// scalar fat ptr: call_ref get.
	kind := "i32"
	ptrType := "$go.ptr.i32"
	if r, ok := in.Type.(*ir.Ref); ok && strings.HasPrefix(r.Named, "$go.struct.") {
		// Dereferencing *Struct yields the struct ref — ptr IS the value.
		pre = append(pre, &ir.Assign{Lhs: s.Lhs, Rhs: ptr})
		return pre, nil
	}
	if nm, ok := in.Args[0].(*ir.Name); ok {
		if nm.Var != nil {
			if r, ok := nm.Var.Typ.(*ir.Ref); ok {
				if strings.HasPrefix(r.Named, "$go.struct.") {
					pre = append(pre, &ir.Assign{Lhs: s.Lhs, Rhs: ptr})
					return pre, nil
				}
				if strings.HasPrefix(r.Named, "$go.ptr.") {
					kind = strings.TrimPrefix(r.Named, "$go.ptr.")
					ptrType = r.Named
				}
			}
		}
		if nm.Temp != nil {
			if r, ok := nm.Temp.Typ.(*ir.Ref); ok && strings.HasPrefix(r.Named, "$go.ptr.") {
				kind = strings.TrimPrefix(r.Named, "$go.ptr.")
				ptrType = r.Named
			}
		}
	}
	rt.ensureFatPtr(kind)
	pt := &ir.Ref{Named: ptrType}
	clT := &ir.Temp{Name: rt.fresh("cl"), Typ: &ir.Ref{Named: "$go.class." + kind}}
	pre = append(pre, &ir.Assign{
		Lhs: []ir.Dest{{Temp: clT, Name: clT.Name}},
		Rhs: &ir.Instr{Op: ir.OpStructGet, Type: pt, Field: "class", Args: []ir.Expr{&ir.Instr{Op: ir.OpRefAsNonNull, Args: []ir.Expr{ptr}}}},
	})
	getFn := &ir.Instr{Op: ir.OpStructGet, Type: &ir.Ref{Named: "$go.class." + kind}, Field: "get", Args: []ir.Expr{&ir.Name{Temp: clT, Raw: clT.Name}}}
	base := &ir.Instr{Op: ir.OpStructGet, Type: pt, Field: "base", Args: []ir.Expr{&ir.Instr{Op: ir.OpRefAsNonNull, Args: []ir.Expr{ptr}}}}
	index := &ir.Instr{Op: ir.OpStructGet, Type: pt, Field: "index", Args: []ir.Expr{&ir.Instr{Op: ir.OpRefAsNonNull, Args: []ir.Expr{ptr}}}}
	pre = append(pre, &ir.Assign{
		Lhs: s.Lhs,
		Rhs: &ir.Instr{
			Op:   ir.OpCallRef,
			Type: &ir.Ref{Named: "$go.get." + kind},
			Args: []ir.Expr{base, index, getFn},
		},
	})
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
	if nm, ok := in.Args[0].(*ir.Name); ok {
		typ := ir.Type(nil)
		if nm.Var != nil {
			typ = nm.Var.Typ
		}
		if nm.Temp != nil {
			typ = nm.Temp.Typ
		}
		if r, ok := typ.(*ir.Ref); ok {
			if strings.HasPrefix(r.Named, "$go.struct.") {
				// *Struct = val → fieldwise copy would be needed; for scalar field
				// stores we use fat ptr. Whole-struct assign through pointer:
				// copy fields — simplified: not in M3 exit criteria.
				return nil, fmt.Errorf("store through object pointer of whole struct not lowered")
			}
			if strings.HasPrefix(r.Named, "$go.ptr.") {
				kind = strings.TrimPrefix(r.Named, "$go.ptr.")
				ptrType = r.Named
			}
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
	pre = append(pre, &ir.ExprStmt{X: &ir.Instr{
		Op:   ir.OpCallRef,
		Type: &ir.Ref{Named: "$go.set." + kind},
		Args: []ir.Expr{base, index, val, setFn},
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
	isArrayElem := field == "elem" && (strings.HasPrefix(container, "$go.array.") ||
		container == "$go.bytes" || container == "$go.i16s")
	if isArrayElem {
		if isGet {
			getOp := ir.OpArrayGet
			if container == "$go.bytes" {
				getOp = ir.OpArrayGetU
			}
			body = &ir.Seq{Stmts: []ir.Stmt{
				&ir.Return{Results: []ir.Expr{&ir.Instr{
					Op: getOp, Type: &ir.Ref{Named: container},
					Args: []ir.Expr{cast, &ir.Name{Raw: "i", Var: idx}},
				}}},
			}}
		} else {
			v := &ir.Var{Name: "v", Typ: val}
			body = &ir.Seq{Stmts: []ir.Stmt{
				&ir.ExprStmt{X: &ir.Instr{
					Op: ir.OpArraySet, Type: &ir.Ref{Named: container},
					Args: []ir.Expr{cast, &ir.Name{Raw: "i", Var: idx}, &ir.Name{Raw: "v", Var: v}},
				}},
				&ir.Return{},
			}}
		}
	} else {
		if isGet {
			body = &ir.Seq{Stmts: []ir.Stmt{
				&ir.Return{Results: []ir.Expr{&ir.Instr{
					Op: ir.OpStructGet, Type: &ir.Ref{Named: container}, Field: field,
					Args: []ir.Expr{cast},
				}}},
			}}
		} else {
			v := &ir.Var{Name: "v", Typ: val}
			body = &ir.Seq{Stmts: []ir.Stmt{
				&ir.ExprStmt{X: &ir.Instr{
					Op: ir.OpStructSet, Type: &ir.Ref{Named: container}, Field: field,
					Args: []ir.Expr{cast, &ir.Name{Raw: "v", Var: v}},
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

func expandStringAssign(m *ir.Module, s *ir.Assign, str string, n *int) ([]ir.Stmt, error) {
	if len(s.Lhs) != 1 {
		return nil, fmt.Errorf("string literal assign wants 1 lhs")
	}
	// Use module-wide unique names: a second RuntimeOps pass (after Ensure*)
	// must not reuse $lit0… and clobber earlier string data (e.g. "main" →
	// "runtime error: …" printing as "runt").
	litName := fmt.Sprintf("$lit%d", len(m.Data))
	if n != nil {
		*n = len(m.Data) + 1
	}
	m.Data = append(m.Data, &ir.Data{Name: litName, Bytes: []byte(str)})

	dataTemp := fmt.Sprintf("%%strdata%d", *n)
	dataT := &ir.Temp{Name: dataTemp, Typ: &ir.Ref{Named: "$go.bytes"}}

	mkData := &ir.Assign{
		Lhs: []ir.Dest{{Temp: dataT, Name: dataTemp}},
		Rhs: &ir.Instr{
			Op:   ir.OpArrayNewData,
			Type: &ir.Ref{Named: "$go.bytes"},
			Data: litName,
		},
	}
	strNew := &ir.Instr{
		Op:   ir.OpStructNew,
		Type: &ir.Ref{Named: "$go.string"},
		Args: []ir.Expr{
			&ir.Name{Temp: dataT, Raw: dataTemp},
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
		strName := fmt.Sprintf("%%str%d", *n)
		*n++
		strT = &ir.Temp{Name: strName, Typ: &ir.Ref{Named: "$go.string"}}
	} else {
		strT.Typ = &ir.Ref{Named: "$go.string"}
	}
	mkStr := &ir.Assign{
		Lhs: []ir.Dest{{Temp: strT, Name: strT.Name}},
		Rhs: strNew,
	}
	return []ir.Stmt{mkData, mkStr}, nil
}
