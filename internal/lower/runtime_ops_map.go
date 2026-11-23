package lower

import (
	"fmt"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
)

func (rt *runtimeLower) expandMapMake(s *ir.Assign, in *ir.Instr, n *int) ([]ir.Stmt, error) {
	var pre []ir.Stmt
	hint := ir.Expr(&ir.Const{Op: ir.OpI32Const, I64: 0})
	if len(in.Args) > 0 {
		var p []ir.Stmt
		var err error
		hint, p, err = rt.rewriteExpr(in.Args[0], n)
		if err != nil {
			return nil, err
		}
		pre = append(pre, p...)
	}
	call := &ir.Instr{Op: ir.OpCall, Func: "runtime.makemap", Args: []ir.Expr{hint}}
	if s != nil && len(s.Lhs) > 0 {
		pre = append(pre, &ir.Assign{Lhs: s.Lhs, Rhs: call})
	} else {
		pre = append(pre, &ir.ExprStmt{X: call})
	}
	return pre, nil
}

func (rt *runtimeLower) expandMapGet(s *ir.Assign, in *ir.Instr, n *int) ([]ir.Stmt, error) {
	if len(in.Args) < 2 {
		return nil, fmt.Errorf("map.get: want map and key")
	}
	m, pre, err := rt.rewriteExpr(in.Args[0], n)
	if err != nil {
		return nil, err
	}
	key, p, err := rt.rewriteExpr(in.Args[1], n)
	if err != nil {
		return nil, err
	}
	pre = append(pre, p...)

	ifaceV := rt.newVar("mgv", typeIface)
	okV := rt.newVar("mgok", ir.TypeI32)
	pre = append(pre, &ir.VarDef{Var: ifaceV}, &ir.VarDef{Var: okV})
	call := &ir.Instr{
		Op:   ir.OpCall,
		Func: "runtime.mapAccess",
		Args: []ir.Expr{m, key},
	}
	pre = append(pre, &ir.Assign{
		Lhs: []ir.Dest{{Var: ifaceV, Name: ifaceV.Name}, {Var: okV, Name: okV.Name}},
		Rhs: call,
	})

	if s == nil || len(s.Lhs) == 0 {
		return pre, nil
	}

	commaOK := in.Str == "ok" || len(s.Lhs) == 2
	dstTy := in.Type
	if dstTy == nil {
		dstTy = typeIface
	}

	valT := rt.newVar("mval", dstTy)
	pre = append(pre, &ir.VarDef{Var: valT})
	zero, err := rt.zeroExpr(dstTy)
	if err != nil {
		return nil, err
	}
	pre = append(pre, assignV(valT, zero))

	var unbox []ir.Stmt
	if in.Field == "" || in.Field == "iface" || in.Field == "ref" && in.Global == "" {
		// Already iface or opaque ref stored as iface.
		unbox = []ir.Stmt{assignV(valT, useV(ifaceV))}
	} else if in.Field == "ref" {
		data := &ir.Instr{
			Op:    ir.OpStructGet,
			Type:  typeIface,
			Field: "data",
			Args:  []ir.Expr{useV(ifaceV)},
		}
		ubStmts, ub := rt.unboxTo(dstTy, data)
		unbox = append(ubStmts, assignV(valT, ub))
	} else {
		data := &ir.Instr{
			Op:    ir.OpStructGet,
			Type:  typeIface,
			Field: "data",
			Args:  []ir.Expr{useV(ifaceV)},
		}
		ubStmts, ub := rt.unboxTo(dstTy, data)
		unbox = append(ubStmts, assignV(valT, ub))
	}

	pre = append(pre, &ir.If{
		Cond: useV(okV),
		Then: &ir.Seq{Stmts: unbox},
		Else: &ir.Seq{},
	})

	if commaOK && len(s.Lhs) >= 2 {
		pre = append(pre, &ir.Assign{
			Lhs: []ir.Dest{s.Lhs[0], s.Lhs[1]},
			Rhs: &ir.Instr{Op: ir.OpCall, Func: "", Args: nil}, // placeholder replaced below
		})
		// Multi-value: assign val and ok separately.
		pre = pre[:len(pre)-1]
		pre = append(pre, &ir.Assign{Lhs: []ir.Dest{s.Lhs[0]}, Rhs: useV(valT)})
		pre = append(pre, &ir.Assign{Lhs: []ir.Dest{s.Lhs[1]}, Rhs: useV(okV)})
		return pre, nil
	}
	pre = append(pre, &ir.Assign{Lhs: s.Lhs, Rhs: useV(valT)})
	return pre, nil
}

func (rt *runtimeLower) expandMapSet(in *ir.Instr, n *int) ([]ir.Stmt, error) {
	if len(in.Args) < 3 {
		return nil, fmt.Errorf("map.set: want map, key, val")
	}
	m, pre, err := rt.rewriteExpr(in.Args[0], n)
	if err != nil {
		return nil, err
	}
	key, p, err := rt.rewriteExpr(in.Args[1], n)
	if err != nil {
		return nil, err
	}
	pre = append(pre, p...)
	val, p, err := rt.rewriteExpr(in.Args[2], n)
	if err != nil {
		return nil, err
	}
	pre = append(pre, p...)
	pre = append(pre, &ir.ExprStmt{X: &ir.Instr{
		Op:   ir.OpCall,
		Func: "runtime.mapAssign",
		Args: []ir.Expr{m, key, val},
	}})
	return pre, nil
}

func (rt *runtimeLower) expandMapDelete(in *ir.Instr, n *int) ([]ir.Stmt, error) {
	if in.Str == "clear" {
		if len(in.Args) < 1 {
			return nil, fmt.Errorf("map.clear: want map")
		}
		m, pre, err := rt.rewriteExpr(in.Args[0], n)
		if err != nil {
			return nil, err
		}
		pre = append(pre, &ir.ExprStmt{X: &ir.Instr{
			Op:   ir.OpCall,
			Func: "runtime.mapClear",
			Args: []ir.Expr{m},
		}})
		return pre, nil
	}
	if len(in.Args) < 2 {
		return nil, fmt.Errorf("map.delete: want map and key")
	}
	m, pre, err := rt.rewriteExpr(in.Args[0], n)
	if err != nil {
		return nil, err
	}
	key, p, err := rt.rewriteExpr(in.Args[1], n)
	if err != nil {
		return nil, err
	}
	pre = append(pre, p...)
	pre = append(pre, &ir.ExprStmt{X: &ir.Instr{
		Op:   ir.OpCall,
		Func: "runtime.mapDelete",
		Args: []ir.Expr{m, key},
	}})
	return pre, nil
}

func (rt *runtimeLower) expandMapLen(s *ir.Assign, in *ir.Instr, n *int) ([]ir.Stmt, error) {
	if len(in.Args) < 1 {
		return nil, fmt.Errorf("map.len: want map")
	}
	m, pre, err := rt.rewriteExpr(in.Args[0], n)
	if err != nil {
		return nil, err
	}
	call := &ir.Instr{Op: ir.OpCall, Func: "runtime.mapLen", Args: []ir.Expr{m}}
	if s != nil && len(s.Lhs) > 0 {
		pre = append(pre, &ir.Assign{Lhs: s.Lhs, Rhs: call})
	}
	return pre, nil
}

func (rt *runtimeLower) expandMapIter(s *ir.Assign, in *ir.Instr, n *int) ([]ir.Stmt, error) {
	if len(in.Args) < 1 {
		return nil, fmt.Errorf("map.iter: want map")
	}
	m, pre, err := rt.rewriteExpr(in.Args[0], n)
	if err != nil {
		return nil, err
	}
	call := &ir.Instr{Op: ir.OpCall, Func: "runtime.mapIterStart", Args: []ir.Expr{m}}
	if s != nil && len(s.Lhs) > 0 {
		pre = append(pre, &ir.Assign{Lhs: s.Lhs, Rhs: call})
	}
	return pre, nil
}

func (rt *runtimeLower) expandMapNext(s *ir.Assign, in *ir.Instr, n *int) ([]ir.Stmt, error) {
	if len(in.Args) < 1 {
		return nil, fmt.Errorf("map.next: want iter")
	}
	it, pre, err := rt.rewriteExpr(in.Args[0], n)
	if err != nil {
		return nil, err
	}
	call := &ir.Instr{Op: ir.OpCall, Func: "runtime.mapIterNext", Args: []ir.Expr{it}}
	if s != nil && len(s.Lhs) > 0 {
		pre = append(pre, &ir.Assign{Lhs: s.Lhs, Rhs: call})
	}
	return pre, nil
}
