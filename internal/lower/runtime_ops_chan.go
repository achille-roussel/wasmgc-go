package lower

import (
	"fmt"
	"strings"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
)

func (rt *runtimeLower) expandGo(in *ir.Instr, n *int) ([]ir.Stmt, error) {
	if len(in.Args) != 1 {
		return nil, fmt.Errorf("go: want 1 arg (closure)")
	}
	fn, pre, err := rt.rewriteExpr(in.Args[0], n)
	if err != nil {
		return nil, err
	}
	pre = append(pre, &ir.ExprStmt{X: &ir.Instr{
		Op:   ir.OpCall,
		Func: "runtime.go_",
		Args: []ir.Expr{fn},
	}})
	return pre, nil
}

func (rt *runtimeLower) expandChanMake(s *ir.Assign, in *ir.Instr, n *int) ([]ir.Stmt, error) {
	var pre []ir.Stmt
	capEx := ir.Expr(&ir.Const{Op: ir.OpI32Const, I64: 0})
	if len(in.Args) > 0 {
		var p []ir.Stmt
		var err error
		capEx, p, err = rt.rewriteExpr(in.Args[0], n)
		if err != nil {
			return nil, err
		}
		pre = append(pre, p...)
	}
	// runtime.makechan allocates []any when cap > 0.
	call := &ir.Instr{
		Op:   ir.OpCall,
		Func: "runtime.makechan",
		Args: []ir.Expr{capEx},
	}
	if s != nil && len(s.Lhs) > 0 {
		pre = append(pre, &ir.Assign{Lhs: s.Lhs, Rhs: call})
	} else {
		pre = append(pre, &ir.ExprStmt{X: call})
	}
	return pre, nil
}

func (rt *runtimeLower) expandChanSend(in *ir.Instr, n *int) ([]ir.Stmt, error) {
	if len(in.Args) < 2 {
		return nil, fmt.Errorf("chan.send: want chan and value")
	}
	ch, pre, err := rt.rewriteExpr(in.Args[0], n)
	if err != nil {
		return nil, err
	}
	val, p, err := rt.rewriteExpr(in.Args[1], n)
	if err != nil {
		return nil, err
	}
	pre = append(pre, p...)
	ops := &ir.Instr{Op: ir.OpCall, Func: "runtime.defaultChanOps"}
	opsT := rt.newVar("ops", &ir.Ref{Null: true, Named: "$go.chanOps"})
	pre = append(pre, &ir.VarDef{Var: opsT}, assignV(opsT, ops))
	call := &ir.Instr{
		Op:   ir.OpCall,
		Func: "runtime.chanSend",
		Args: []ir.Expr{ch, val, useV(opsT), &ir.Const{Op: ir.OpI32Const, I64: 1}},
	}
	pre = append(pre, &ir.ExprStmt{X: &ir.Instr{Op: ir.OpDrop, Args: []ir.Expr{call}}})
	return pre, nil
}

func (rt *runtimeLower) expandChanRecv(s *ir.Assign, in *ir.Instr, n *int) ([]ir.Stmt, error) {
	if len(in.Args) < 1 {
		return nil, fmt.Errorf("chan.recv: want chan")
	}
	ch, pre, err := rt.rewriteExpr(in.Args[0], n)
	if err != nil {
		return nil, err
	}
	ops := &ir.Instr{Op: ir.OpCall, Func: "runtime.defaultChanOps"}
	opsT := rt.newVar("ops", &ir.Ref{Null: true, Named: "$go.chanOps"})
	pre = append(pre, &ir.VarDef{Var: opsT}, assignV(opsT, ops))

	ifaceV := rt.newVar("rcv", typeIface)
	okV := rt.newVar("rok", ir.TypeI32)
	pre = append(pre, &ir.VarDef{Var: ifaceV}, &ir.VarDef{Var: okV})
	call := &ir.Instr{
		Op:   ir.OpCall,
		Func: "runtime.chanRecv",
		Args: []ir.Expr{ch, useV(opsT), &ir.Const{Op: ir.OpI32Const, I64: 1}},
	}
	pre = append(pre, &ir.Assign{
		Lhs: []ir.Dest{{Var: ifaceV, Name: ifaceV.Name}, {Var: okV, Name: okV.Name}},
		Rhs: call,
	})

	// Discard receive (`<-ch`): side effect only; skip unbox / type globals.
	if s == nil || len(s.Lhs) == 0 {
		return pre, nil
	}

	commaOK := in.Str == "ok" || len(s.Lhs) == 2
	dstTy := in.Type
	if dstTy == nil {
		dstTy = typeIface
	}

	// Closed/empty recv yields a nil iface; unbox only when ok is true,
	// otherwise produce the element zero value (needed for comma-ok and
	// for-range over chan).
	valT := rt.newVar("rv", dstTy)
	pre = append(pre, &ir.VarDef{Var: valT})
	zero, err := rt.zeroExpr(dstTy)
	if err != nil {
		return nil, err
	}
	pre = append(pre, assignV(valT, zero))

	var unbox []ir.Stmt
	if r, ok := dstTy.(*ir.Ref); ok && r.Named == "$go.iface" {
		unbox = []ir.Stmt{assignV(valT, useV(ifaceV))}
	} else if in.Global != "" {
		assert := &ir.Instr{
			Op:     ir.OpIfaceAssert,
			Type:   dstTy,
			Global: in.Global,
			Field:  in.Field,
			Str:    "concrete",
			Args:   []ir.Expr{useV(ifaceV)},
		}
		as := &ir.Assign{Lhs: []ir.Dest{{Var: valT, Name: valT.Name}}, Rhs: assert}
		st, err := rt.expandIfaceAssert(as, assert, n)
		if err != nil {
			return nil, err
		}
		unbox = st
	} else {
		data := &ir.Instr{Op: ir.OpStructGet, Type: typeIface, Field: "data", Args: []ir.Expr{useV(ifaceV)}}
		ubStmts, ub := rt.unboxTo(dstTy, data)
		unbox = append(ubStmts, assignV(valT, ub))
	}
	pre = append(pre, &ir.If{
		Cond: useV(okV),
		Then: &ir.Seq{Stmts: unbox},
		Else: &ir.Seq{},
	})
	valEx := useV(valT)
	if commaOK && len(s.Lhs) == 2 {
		pre = append(pre,
			&ir.Assign{Lhs: []ir.Dest{s.Lhs[0]}, Rhs: valEx},
			&ir.Assign{Lhs: []ir.Dest{s.Lhs[1]}, Rhs: useV(okV)},
		)
		return pre, nil
	}
	pre = append(pre, &ir.Assign{Lhs: s.Lhs, Rhs: valEx})
	return pre, nil
}

func (rt *runtimeLower) expandChanClose(in *ir.Instr, n *int) ([]ir.Stmt, error) {
	if len(in.Args) < 1 {
		return nil, fmt.Errorf("chan.close: want chan")
	}
	ch, pre, err := rt.rewriteExpr(in.Args[0], n)
	if err != nil {
		return nil, err
	}
	pre = append(pre, &ir.ExprStmt{X: &ir.Instr{
		Op:   ir.OpCall,
		Func: "runtime.chanClose",
		Args: []ir.Expr{ch},
	}})
	return pre, nil
}

// zeroExpr is the IR zero for dstTy (scalars, string, null refs, empty structs).
func (rt *runtimeLower) zeroExpr(t ir.Type) (ir.Expr, error) {
	switch t := t.(type) {
	case *ir.NumType:
		switch t.Kind {
		case ir.I64:
			return &ir.Const{Op: ir.OpI64Const, I64: 0}, nil
		case ir.F32:
			return &ir.Const{Op: ir.OpF32Const, F64: 0}, nil
		case ir.F64:
			return &ir.Const{Op: ir.OpF64Const, F64: 0}, nil
		default:
			return &ir.Const{Op: ir.OpI32Const, I64: 0}, nil
		}
	case *ir.Ref:
		if t.Named == "$go.string" {
			return &ir.Instr{Op: ir.OpStringLit, Str: ""}, nil
		}
		if strings.HasPrefix(t.Named, "$go.struct.") {
			if td := rt.mod.LookupType(t.Named); td != nil && td.Kind == ir.TypeStruct {
				return rt.structZero(td, t)
			}
		}
		return &ir.Instr{Op: ir.OpRefNull, Type: t}, nil
	default:
		return nil, fmt.Errorf("no zero value for %s", t)
	}
}

func (rt *runtimeLower) structZero(td *ir.TypeDef, t ir.Type) (ir.Expr, error) {
	return rt.structZeroSeen(td, t, map[string]bool{})
}

func (rt *runtimeLower) structZeroSeen(td *ir.TypeDef, t ir.Type, seen map[string]bool) (ir.Expr, error) {
	if seen[td.Name] {
		return &ir.Instr{Op: ir.OpStructNewDefault, Type: t}, nil
	}
	seen[td.Name] = true
	args := make([]ir.Expr, len(td.Fields))
	for i, f := range td.Fields {
		if r, ok := f.Type.(*ir.Ref); ok && strings.HasPrefix(r.Named, "$go.struct.") {
			ntd := rt.mod.LookupType(r.Named)
			if ntd != nil && ntd.Kind == ir.TypeStruct {
				z, err := rt.structZeroSeen(ntd, r, seen)
				if err != nil {
					return nil, err
				}
				args[i] = z
				continue
			}
		}
		z, err := rt.zeroExpr(f.Type)
		if err != nil {
			return nil, err
		}
		args[i] = z
	}
	delete(seen, td.Name)
	return &ir.Instr{Op: ir.OpStructNew, Type: t, Args: args}, nil
}
