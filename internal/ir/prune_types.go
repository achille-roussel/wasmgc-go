package ir

import "strings"

// PruneUnusedTypes drops unused $go.func.* / $go.itabtype.* / $go.ifacefn.*
// TypeDefs after DCE. Package lower of dead reflect.Value.Call still
// InternType's closed-universe call signatures; emitMissingItabTypes declares
// itabtypes for OpIfaceCall in bodies RuntimeOps expands before DCE. Without
// this prune those orphans stay in the Wasm type section (fmt hello size).
// Other TypeDefs (prelude, structs, clos subtypes) are left alone — a full
// type-graph prune would strip ABI shapes empty_main still dumps as goldens.
func PruneUnusedTypes(m *Module) {
	if m == nil || len(m.Types) == 0 {
		return
	}
	used := map[string]bool{}
	isPrunable := func(name string) bool {
		return strings.HasPrefix(name, "$go.func.") ||
			strings.HasPrefix(name, "$go.itabtype.") ||
			strings.HasPrefix(name, "$go.ifacefn.")
	}
	markType := func(t Type) {
		markNamedTypes(t, func(name string) {
			if isPrunable(name) {
				used[name] = true
			}
		})
	}

	for _, f := range m.Funcs {
		for _, p := range f.Params {
			if p != nil {
				markType(p.Typ)
			}
		}
		for _, r := range f.Results {
			markType(r)
		}
		for _, loc := range f.Locals {
			if loc != nil {
				markType(loc.Typ)
			}
		}
		markTypesRegion(f.Body, markType)
	}
	for _, g := range m.Globals {
		markType(g.Typ)
		markTypesExpr(g.Init, markType)
	}
	// Reachable prunable TypeDefs may mention other prunable names (itabtype
	// fields → ifacefn; func fields → func). Fixed-point over the type graph.
	changed := true
	for changed {
		changed = false
		for _, td := range m.Types {
			if td == nil || !isPrunable(td.Name) || !used[td.Name] {
				continue
			}
			before := len(used)
			for _, f := range td.Fields {
				markType(f.Type)
			}
			for _, p := range td.Params {
				markType(p)
			}
			for _, r := range td.Results {
				markType(r)
			}
			if len(used) > before {
				changed = true
			}
		}
	}

	out := m.Types[:0]
	for _, td := range m.Types {
		if td == nil {
			continue
		}
		if isPrunable(td.Name) && !used[td.Name] {
			continue
		}
		out = append(out, td)
	}
	m.Types = out
}

func markNamedTypes(t Type, mark func(string)) {
	if t == nil {
		return
	}
	switch t := t.(type) {
	case *Ref:
		if t.Named != "" {
			mark(t.Named)
		}
	case *TypeDef:
		if t.Name != "" {
			mark(t.Name)
		}
	}
}

func markTypesRegion(r Region, markType func(Type)) {
	if r == nil {
		return
	}
	switch r := r.(type) {
	case *Seq:
		for _, s := range r.Stmts {
			markTypesStmt(s, markType)
		}
	case *Block:
		markTypesRegion(r.Body, markType)
	case *Loop:
		markTypesRegion(r.Body, markType)
	case *If:
		markTypesExpr(r.Cond, markType)
		markTypesRegion(r.Then, markType)
		markTypesRegion(r.Else, markType)
	case *Switch:
		markTypesExpr(r.Tag, markType)
		for _, c := range r.Cases {
			markTypesRegion(c.Body, markType)
		}
		markTypesRegion(r.Default, markType)
	case *Try:
		markTypesRegion(r.Body, markType)
		markTypesRegion(r.Handler, markType)
	case *Dispatch:
		for _, arm := range r.Arms {
			markTypesRegion(arm, markType)
		}
	}
}

func markTypesStmt(s Stmt, markType func(Type)) {
	switch s := s.(type) {
	case *ExprStmt:
		markTypesExpr(s.X, markType)
	case *Assign:
		markTypesExpr(s.Rhs, markType)
	case *Return:
		for _, e := range s.Results {
			markTypesExpr(e, markType)
		}
	case *VarDef:
		if s.Var != nil {
			markType(s.Var.Typ)
		}
	case *Br:
		markTypesExpr(s.Cond, markType)
	case Region:
		markTypesRegion(s, markType)
	}
}

func markTypesExpr(e Expr, markType func(Type)) {
	if e == nil {
		return
	}
	switch e := e.(type) {
	case *Instr:
		markType(e.Type)
		markType(e.Type2)
		for _, a := range e.Args {
			markTypesExpr(a, markType)
		}
	case *Name:
		if e.Var != nil {
			markType(e.Var.Typ)
		}
		if e.Temp != nil {
			markType(e.Temp.Typ)
		}
	}
}
