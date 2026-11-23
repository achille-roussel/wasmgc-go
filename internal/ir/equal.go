package ir

import (
	"bytes"
)

// Equal reports whether a and b are structurally identical for the
// round-trip guarantee parse(print(x)) == x. Positions and verifier
// annotations (Depth, Dense, HasDispatch) are ignored.
func Equal(a, b *Module) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Low != b.Low {
		return false
	}
	if len(a.Types) != len(b.Types) || len(a.Globals) != len(b.Globals) ||
		len(a.Data) != len(b.Data) || len(a.Funcs) != len(b.Funcs) {
		return false
	}
	for i := range a.Types {
		if !equalTypeDef(a.Types[i], b.Types[i]) {
			return false
		}
	}
	for i := range a.Globals {
		if !equalGlobal(a.Globals[i], b.Globals[i]) {
			return false
		}
	}
	for i := range a.Data {
		if a.Data[i].Name != b.Data[i].Name || !bytes.Equal(a.Data[i].Bytes, b.Data[i].Bytes) {
			return false
		}
	}
	for i := range a.Funcs {
		if !equalFunc(a.Funcs[i], b.Funcs[i]) {
			return false
		}
	}
	return true
}

func equalTypeDef(a, b *TypeDef) bool {
	if a.Name != b.Name || a.Kind != b.Kind || a.Super != b.Super || a.ElemMut != b.ElemMut {
		return false
	}
	if !TypesEqual(a.Elem, b.Elem) || !TypeListEqual(a.Params, b.Params) || !TypeListEqual(a.Results, b.Results) {
		return false
	}
	if len(a.Fields) != len(b.Fields) {
		return false
	}
	for i := range a.Fields {
		if a.Fields[i].Name != b.Fields[i].Name || a.Fields[i].Mut != b.Fields[i].Mut ||
			!TypesEqual(a.Fields[i].Type, b.Fields[i].Type) {
			return false
		}
	}
	return true
}

func equalGlobal(a, b *Global) bool {
	return a.Name == b.Name && a.Mut == b.Mut && TypesEqual(a.Typ, b.Typ) && equalExpr(a.Init, b.Init)
}

func equalFunc(a, b *Func) bool {
	if a.Name != b.Name || a.Export != b.Export {
		return false
	}
	if len(a.Params) != len(b.Params) || !TypeListEqual(a.Results, b.Results) {
		return false
	}
	for i := range a.Params {
		if a.Params[i].Name != b.Params[i].Name || !TypesEqual(a.Params[i].Typ, b.Params[i].Typ) {
			return false
		}
	}
	return equalRegion(a.Body, b.Body)
}

func equalRegion(a, b Region) bool {
	if a == nil || b == nil {
		return a == b
	}
	switch a := a.(type) {
	case *Seq:
		b, ok := b.(*Seq)
		if !ok || len(a.Stmts) != len(b.Stmts) {
			return false
		}
		for i := range a.Stmts {
			if !equalStmt(a.Stmts[i], b.Stmts[i]) {
				return false
			}
		}
		return true
	case *Block:
		b, ok := b.(*Block)
		return ok && a.Name == b.Name && TypeListEqual(a.Results, b.Results) && equalRegion(a.Body, b.Body)
	case *Loop:
		b, ok := b.(*Loop)
		return ok && a.Name == b.Name && equalRegion(a.Body, b.Body)
	case *If:
		b, ok := b.(*If)
		return ok && equalExpr(a.Cond, b.Cond) && equalRegion(a.Then, b.Then) && equalRegion(a.Else, b.Else)
	case *Switch:
		b, ok := b.(*Switch)
		if !ok || !equalExpr(a.Tag, b.Tag) || len(a.Cases) != len(b.Cases) || !equalRegion(a.Default, b.Default) {
			return false
		}
		for i := range a.Cases {
			if a.Cases[i].Value != b.Cases[i].Value || !equalRegion(a.Cases[i].Body, b.Cases[i].Body) {
				return false
			}
		}
		return true
	case *Try:
		b, ok := b.(*Try)
		if !ok || a.Name != b.Name || a.Tag != b.Tag || len(a.Params) != len(b.Params) {
			return false
		}
		for i := range a.Params {
			if a.Params[i].Name != b.Params[i].Name || !TypesEqual(a.Params[i].Typ, b.Params[i].Typ) {
				return false
			}
		}
		return equalRegion(a.Body, b.Body) && equalRegion(a.Handler, b.Handler)
	case *Dispatch:
		b, ok := b.(*Dispatch)
		if !ok || a.Name != b.Name || len(a.Arms) != len(b.Arms) {
			return false
		}
		if (a.State == nil) != (b.State == nil) {
			return false
		}
		if a.State != nil && a.State.Name != b.State.Name {
			return false
		}
		for i := range a.Arms {
			if !equalRegion(a.Arms[i], b.Arms[i]) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func equalStmt(a, b Stmt) bool {
	if a == nil || b == nil {
		return a == b
	}
	if ar, ok := a.(Region); ok {
		br, ok := b.(Region)
		return ok && equalRegion(ar, br)
	}
	switch a := a.(type) {
	case *VarDef:
		b, ok := b.(*VarDef)
		return ok && a.Var.Name == b.Var.Name && TypesEqual(a.Var.Typ, b.Var.Typ)
	case *Assign:
		b, ok := b.(*Assign)
		if !ok || len(a.Lhs) != len(b.Lhs) || !equalExpr(a.Rhs, b.Rhs) {
			return false
		}
		for i := range a.Lhs {
			if a.Lhs[i].String() != b.Lhs[i].String() {
				return false
			}
		}
		return true
	case *ExprStmt:
		b, ok := b.(*ExprStmt)
		return ok && equalExpr(a.X, b.X)
	case *Return:
		b, ok := b.(*Return)
		if !ok || len(a.Results) != len(b.Results) {
			return false
		}
		for i := range a.Results {
			if !equalExpr(a.Results[i], b.Results[i]) {
				return false
			}
		}
		return true
	case *Br:
		b, ok := b.(*Br)
		if !ok || a.Label != b.Label || !equalExpr(a.Cond, b.Cond) || len(a.Args) != len(b.Args) {
			return false
		}
		for i := range a.Args {
			if !equalExpr(a.Args[i], b.Args[i]) {
				return false
			}
		}
		return true
	case *Unreachable:
		_, ok := b.(*Unreachable)
		return ok
	default:
		return false
	}
}

func equalExpr(a, b Expr) bool {
	if a == nil || b == nil {
		return a == b
	}
	switch a := a.(type) {
	case *Const:
		b, ok := b.(*Const)
		return ok && a.Op == b.Op && a.I64 == b.I64 && a.F64 == b.F64 && a.Str == b.Str
	case *Name:
		b, ok := b.(*Name)
		return ok && a.String() == b.String()
	case *Instr:
		b, ok := b.(*Instr)
		if !ok || a.Op != b.Op || a.Field != b.Field || a.Func != b.Func ||
			a.Global != b.Global || a.Label != b.Label || a.Data != b.Data ||
			a.Str != b.Str || a.I32 != b.I32 ||
			!TypesEqual(a.Type, b.Type) || !TypesEqual(a.Type2, b.Type2) ||
			len(a.Args) != len(b.Args) {
			return false
		}
		for i := range a.Args {
			if !equalExpr(a.Args[i], b.Args[i]) {
				return false
			}
		}
		return true
	default:
		return false
	}
}
