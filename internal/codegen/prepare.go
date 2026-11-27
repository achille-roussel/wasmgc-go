package codegen

import (
	"fmt"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
)

// LowerStrings rewrites high-level string literals into low IR:
//
//	%t = string "hello"
//
// becomes a data segment plus:
//
//	%data = array.new_data $go.bytes $litN
//	%t = struct.new $go.string %data, 0, len
//
// Intended for tests and for a thin bridge until the lowerer (stream D)
// emits this form. The module is not marked Low; the caller must set it.
func LowerStrings(m *ir.Module) error {
	n := 0
	for _, f := range m.Funcs {
		if f.Body == nil {
			continue
		}
		var err error
		f.Body, err = lowerRegionStrings(m, f.Body, &n)
		if err != nil {
			return err
		}
	}
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
		return &ir.If{Cond: r.Cond, Then: then, Else: elseR, Pos: r.Pos}, nil
	case *ir.Switch:
		rr := *r
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
		return &rr, nil
	case *ir.Try:
		body, err := lowerRegionStrings(m, r.Body, n)
		if err != nil {
			return nil, err
		}
		handler, err := lowerRegionStrings(m, r.Handler, n)
		if err != nil {
			return nil, err
		}
		rr := *r
		rr.Body = body
		rr.Handler = handler
		return &rr, nil
	case *ir.Dispatch:
		rr := *r
		rr.Arms = append([]ir.Region(nil), r.Arms...)
		for i, a := range rr.Arms {
			if a == nil {
				continue
			}
			body, err := lowerRegionStrings(m, a, n)
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
		return []ir.Stmt{s}, nil
	default:
		return []ir.Stmt{s}, nil
	}
}

func expandStringAssign(m *ir.Module, s *ir.Assign, str string, n *int) ([]ir.Stmt, error) {
	if len(s.Lhs) != 1 {
		return nil, fmt.Errorf("string literal assign wants 1 lhs")
	}
	litName := fmt.Sprintf("$lit%d", len(m.Data))
	if n != nil {
		*n = len(m.Data) + 1
	}
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

// EnsureRunExport adds `func export "run" main.run() { call main.main(); return }`
// when the module has main.main but no run export. Used by execution tests.
// If the program defines func run (IR main.run), the wrapper is $go.run.
func EnsureRunExport(m *ir.Module) {
	if m.LookupExport("run") != nil {
		return
	}
	if m.LookupFunc("main.main") == nil {
		return
	}
	entry := ir.RunEntryName
	if m.LookupFunc(entry) != nil {
		entry = ir.AltRunEntryName
	}
	m.Funcs = append([]*ir.Func{{
		Name:   entry,
		Export: "run",
		Body: &ir.Seq{Stmts: []ir.Stmt{
			&ir.ExprStmt{X: &ir.Instr{Op: ir.OpCall, Func: "main.main"}},
			&ir.Return{},
		}},
	}}, m.Funcs...)
}

// PrepareForCodegen lowers strings, ensures a run export, and sets Low.
// It does not add print stubs; pass Options.SynthPrintRuntime for those.
func PrepareForCodegen(m *ir.Module) error {
	if err := LowerStrings(m); err != nil {
		return err
	}
	EnsureRunExport(m)
	m.Low = true
	return nil
}
