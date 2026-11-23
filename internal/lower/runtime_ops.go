package lower

import (
	"fmt"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
)

// RuntimeOps rewrites high-level ops into the low form codegen accepts and
// sets mod.Low. For M1/M2 this expands string literals:
//
//	%t = string "hello"
//
// into:
//
//	data $litN "hello"
//	%d = array.new_data $go.bytes $litN
//	%t = struct.new $go.string %d, 0, len
//
// Branch depths are left unset; ir.Verify (called by codegen.Module) fills
// Br.Depth. This pass is owned by Stream D; codegen.LowerStrings is the same
// rewrite kept as a test bridge until drivers call RuntimeOps.
func RuntimeOps(mod *ir.Module) error {
	if mod == nil {
		return fmt.Errorf("lower.RuntimeOps: nil module")
	}
	// Idempotent: skip already-expanded string lits; n continues past existing data
	// so a second pass after rtlink can expand new literals without name clashes.
	n := len(mod.Data)
	for _, f := range mod.Funcs {
		if f.Body == nil {
			continue
		}
		body, err := lowerRegionStrings(mod, f.Body, &n)
		if err != nil {
			return err
		}
		f.Body = body
	}
	mod.Low = true
	return nil
}

func lowerRegionStrings(m *ir.Module, r ir.Region, n *int) (ir.Region, error) {
	switch r := r.(type) {
	case *ir.Seq:
		var out []ir.Stmt
		for _, s := range r.Stmts {
			stmts, err := lowerStmtStrings(m, s, n)
			if err != nil {
				return nil, err
			}
			out = append(out, stmts...)
		}
		return &ir.Seq{Stmts: out, Pos: r.Pos}, nil
	case *ir.Block:
		body, err := lowerRegionStrings(m, r.Body, n)
		if err != nil {
			return nil, err
		}
		rr := *r
		rr.Body = body
		return &rr, nil
	case *ir.Loop:
		body, err := lowerRegionStrings(m, r.Body, n)
		if err != nil {
			return nil, err
		}
		rr := *r
		rr.Body = body
		return &rr, nil
	case *ir.If:
		then, err := lowerRegionStrings(m, r.Then, n)
		if err != nil {
			return nil, err
		}
		var elseR ir.Region
		if r.Else != nil {
			elseR, err = lowerRegionStrings(m, r.Else, n)
			if err != nil {
				return nil, err
			}
		}
		cond, pre, err := rewriteExprStrings(m, r.Cond, n)
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
		tag, pre, err := rewriteExprStrings(m, r.Tag, n)
		if err != nil {
			return nil, err
		}
		rr.Tag = tag
		rr.Cases = append([]ir.Case(nil), r.Cases...)
		for i, c := range rr.Cases {
			body, err := lowerRegionStrings(m, c.Body, n)
			if err != nil {
				return nil, err
			}
			rr.Cases[i].Body = body
		}
		if r.Default != nil {
			d, err := lowerRegionStrings(m, r.Default, n)
			if err != nil {
				return nil, err
			}
			rr.Default = d
		}
		if len(pre) == 0 {
			return &rr, nil
		}
		return &ir.Seq{Stmts: append(pre, &rr)}, nil
	default:
		return r, nil
	}
}

func lowerStmtStrings(m *ir.Module, s ir.Stmt, n *int) ([]ir.Stmt, error) {
	switch s := s.(type) {
	case ir.Region:
		r, err := lowerRegionStrings(m, s, n)
		if err != nil {
			return nil, err
		}
		return []ir.Stmt{r}, nil
	case *ir.Assign:
		if c, ok := s.Rhs.(*ir.Const); ok && c.Op == ir.OpStringLit {
			return expandStringAssign(m, s, c.Str, n)
		}
		if in, ok := s.Rhs.(*ir.Instr); ok && in.Op == ir.OpStringLit {
			return expandStringAssign(m, s, in.Str, n)
		}
		rhs, pre, err := rewriteExprStrings(m, s.Rhs, n)
		if err != nil {
			return nil, err
		}
		s.Rhs = rhs
		return append(pre, s), nil
	case *ir.ExprStmt:
		x, pre, err := rewriteExprStrings(m, s.X, n)
		if err != nil {
			return nil, err
		}
		s.X = x
		return append(pre, s), nil
	case *ir.Return:
		var pre []ir.Stmt
		for i, e := range s.Results {
			ex, p, err := rewriteExprStrings(m, e, n)
			if err != nil {
				return nil, err
			}
			pre = append(pre, p...)
			s.Results[i] = ex
		}
		return append(pre, s), nil
	case *ir.Br:
		if s.Cond != nil {
			c, pre, err := rewriteExprStrings(m, s.Cond, n)
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

// rewriteExprStrings expands nested string lits (e.g. call args) into temps.
func rewriteExprStrings(m *ir.Module, e ir.Expr, n *int) (ir.Expr, []ir.Stmt, error) {
	if e == nil {
		return nil, nil, nil
	}
	switch e := e.(type) {
	case *ir.Const:
		if e.Op == ir.OpStringLit {
			t := &ir.Temp{Name: fmt.Sprintf("%%str%d", *n), Typ: &ir.Ref{Named: "$go.string"}}
			*n++
			assign := &ir.Assign{Lhs: []ir.Dest{{Temp: t, Name: t.Name}}, Rhs: e}
			stmts, err := expandStringAssign(m, assign, e.Str, n)
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
			stmts, err := expandStringAssign(m, assign, e.Str, n)
			if err != nil {
				return nil, nil, err
			}
			return &ir.Name{Raw: t.Name, Temp: t}, stmts, nil
		}
		if e.Op.IsHigh() {
			return nil, nil, fmt.Errorf("lower.RuntimeOps: high-level op %s not lowered in M1/M2", e.Op)
		}
		var pre []ir.Stmt
		for i, a := range e.Args {
			ex, p, err := rewriteExprStrings(m, a, n)
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

func expandStringAssign(m *ir.Module, s *ir.Assign, str string, n *int) ([]ir.Stmt, error) {
	if len(s.Lhs) != 1 {
		return nil, fmt.Errorf("string literal assign wants 1 lhs")
	}
	litName := fmt.Sprintf("$lit%d", *n)
	*n++
	m.Data = append(m.Data, &ir.Data{Name: litName, Bytes: []byte(str)})

	dataTemp := fmt.Sprintf("%%strdata%d", *n)
	dataT := &ir.Temp{Name: dataTemp, Typ: &ir.Ref{Named: "$go.bytes"}}
	strT := s.Lhs[0].Temp
	if strT == nil {
		strT = &ir.Temp{Name: s.Lhs[0].Name, Typ: &ir.Ref{Named: "$go.string"}}
		s.Lhs[0].Temp = strT
	} else {
		strT.Typ = &ir.Ref{Named: "$go.string"}
	}

	mkData := &ir.Assign{
		Lhs: []ir.Dest{{Temp: dataT, Name: dataTemp}},
		Rhs: &ir.Instr{
			Op:   ir.OpArrayNewData,
			Type: &ir.Ref{Named: "$go.bytes"},
			Data: litName,
		},
	}
	mkStr := &ir.Assign{
		Lhs: []ir.Dest{{Temp: strT, Name: strT.Name}},
		Rhs: &ir.Instr{
			Op:   ir.OpStructNew,
			Type: &ir.Ref{Named: "$go.string"},
			Args: []ir.Expr{
				&ir.Name{Temp: dataT, Raw: dataTemp},
				&ir.Const{Op: ir.OpI32Const, I64: 0},
				&ir.Const{Op: ir.OpI32Const, I64: int64(len(str))},
			},
		},
	}
	return []ir.Stmt{mkData, mkStr}, nil
}
