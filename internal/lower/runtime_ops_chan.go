package lower

import (
	"fmt"

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

	var valEx ir.Expr
	if r, ok := dstTy.(*ir.Ref); ok && r.Named == "$go.iface" {
		valEx = useV(ifaceV)
	} else if in.Global != "" {
		assert := &ir.Instr{
			Op:     ir.OpIfaceAssert,
			Type:   dstTy,
			Global: in.Global,
			Field:  in.Field,
			Str:    "concrete",
			Args:   []ir.Expr{useV(ifaceV)},
		}
		valT := rt.newVar("rv", dstTy)
		pre = append(pre, &ir.VarDef{Var: valT})
		as := &ir.Assign{Lhs: []ir.Dest{{Var: valT, Name: valT.Name}}, Rhs: assert}
		st, err := rt.expandIfaceAssert(as, assert, n)
		if err != nil {
			return nil, err
		}
		pre = append(pre, st...)
		valEx = useV(valT)
	} else {
		data := &ir.Instr{Op: ir.OpStructGet, Type: typeIface, Field: "data", Args: []ir.Expr{useV(ifaceV)}}
		ubStmts, ub := rt.unboxTo(dstTy, data)
		pre = append(pre, ubStmts...)
		valEx = ub
	}
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
