package ir

import (
	"fmt"
	"strings"
)

// resolveModule wires Name references to Var/Temp definitions and checks
// that referenced types/funcs/globals exist when mentioned.
func resolveModule(m *Module) error {
	typeByName := map[string]*TypeDef{}
	for _, t := range m.Types {
		if _, ok := typeByName[t.Name]; ok {
			return &Error{Pos: t.Pos, Msg: fmt.Sprintf("duplicate type %s", t.Name)}
		}
		typeByName[t.Name] = t
	}
	// Resolve type fields / elems that use named refs.
	for _, t := range m.Types {
		if err := resolveTypeDef(t, typeByName); err != nil {
			return err
		}
	}
	for _, g := range m.Globals {
		if err := resolveTypeRef(&g.Typ, typeByName); err != nil {
			return err
		}
		if g.Init != nil {
			if err := resolveExpr(g.Init, nil, nil); err != nil {
				return err
			}
		}
	}
	funcByName := map[string]*Func{}
	for _, f := range m.Funcs {
		if _, ok := funcByName[f.Name]; ok {
			return &Error{Pos: f.Pos, Msg: fmt.Sprintf("duplicate func %s", f.Name)}
		}
		funcByName[f.Name] = f
	}
	for _, f := range m.Funcs {
		if err := resolveFunc(f, typeByName); err != nil {
			return err
		}
	}
	return nil
}

func resolveTypeDef(t *TypeDef, types map[string]*TypeDef) error {
	if t.Super != "" {
		if _, ok := types[t.Super]; !ok {
			return &Error{Pos: t.Pos, Msg: fmt.Sprintf("unknown super type %s", t.Super)}
		}
	}
	for i := range t.Fields {
		if err := resolveTypeRef(&t.Fields[i].Type, types); err != nil {
			return err
		}
	}
	if t.Elem != nil {
		if err := resolveTypeRef(&t.Elem, types); err != nil {
			return err
		}
	}
	for i := range t.Params {
		if err := resolveTypeRef(&t.Params[i], types); err != nil {
			return err
		}
	}
	for i := range t.Results {
		if err := resolveTypeRef(&t.Results[i], types); err != nil {
			return err
		}
	}
	return nil
}

func resolveTypeRef(tp *Type, types map[string]*TypeDef) error {
	if tp == nil || *tp == nil {
		return nil
	}
	switch t := (*tp).(type) {
	case *Ref:
		if t.Named != "" {
			if td, ok := types[t.Named]; ok {
				_ = td // name is valid; keep Named string for printing
			}
			// Unknown names are allowed until verify (imports / prelude).
		}
	case *High:
		// leave as-is
	}
	return nil
}

func resolveFunc(f *Func, types map[string]*TypeDef) error {
	for _, p := range f.Params {
		if err := resolveTypeRef(&p.Typ, types); err != nil {
			return err
		}
	}
	for i := range f.Results {
		if err := resolveTypeRef(&f.Results[i], types); err != nil {
			return err
		}
	}
	vars := map[string]*Var{}
	temps := map[string]*Temp{}
	for _, p := range f.Params {
		vars[p.Name] = p
	}
	return resolveRegion(f.Body, vars, temps, types)
}

func resolveRegion(r Region, vars map[string]*Var, temps map[string]*Temp, types map[string]*TypeDef) error {
	if r == nil {
		return nil
	}
	switch r := r.(type) {
	case *Seq:
		// Temps defined in this Seq accumulate for later stmts and nested
		// regions (outer temps → function locals; #52). Clone so we do not
		// mutate the caller's map.
		localTemps := cloneTempMap(temps)
		for _, s := range r.Stmts {
			if err := resolveStmt(s, vars, localTemps, types); err != nil {
				return err
			}
		}
	case *Block:
		for _, p := range r.Params {
			vars[p.Name] = p
		}
		return resolveRegion(r.Body, vars, cloneTempMap(temps), types)
	case *Loop:
		for _, p := range r.Params {
			vars[p.Name] = p
		}
		return resolveRegion(r.Body, vars, cloneTempMap(temps), types)
	case *If:
		if err := resolveExpr(r.Cond, vars, temps); err != nil {
			return err
		}
		if err := resolveRegion(r.Then, vars, cloneTempMap(temps), types); err != nil {
			return err
		}
		if r.Else != nil {
			if err := resolveRegion(r.Else, vars, cloneTempMap(temps), types); err != nil {
				return err
			}
		}
	case *Switch:
		if err := resolveExpr(r.Tag, vars, temps); err != nil {
			return err
		}
		for i := range r.Cases {
			if err := resolveRegion(r.Cases[i].Body, vars, cloneTempMap(temps), types); err != nil {
				return err
			}
		}
		if r.Default != nil {
			if err := resolveRegion(r.Default, vars, cloneTempMap(temps), types); err != nil {
				return err
			}
		}
	case *Try:
		if err := resolveRegion(r.Body, vars, cloneTempMap(temps), types); err != nil {
			return err
		}
		hvars := cloneVarMap(vars)
		for _, p := range r.Params {
			hvars[p.Name] = p
		}
		return resolveRegion(r.Handler, hvars, cloneTempMap(temps), types)
	case *Dispatch:
		if r.State != nil {
			vars[r.State.Name] = r.State
		}
		for _, a := range r.Arms {
			if a == nil {
				continue
			}
			if err := resolveRegion(a, vars, cloneTempMap(temps), types); err != nil {
				return err
			}
		}
	}
	return nil
}

func resolveStmt(s Stmt, vars map[string]*Var, temps map[string]*Temp, types map[string]*TypeDef) error {
	switch s := s.(type) {
	case Region:
		return resolveRegion(s, vars, temps, types)
	case *VarDef:
		if _, ok := vars[s.Var.Name]; ok {
			return &Error{Pos: s.Pos, Msg: fmt.Sprintf("duplicate var %s", s.Var.Name)}
		}
		if err := resolveTypeRef(&s.Var.Typ, types); err != nil {
			return err
		}
		vars[s.Var.Name] = s.Var
	case *Assign:
		if err := resolveExpr(s.Rhs, vars, temps); err != nil {
			return err
		}
		for i := range s.Lhs {
			d := &s.Lhs[i]
			if strings.HasPrefix(d.Name, "%") {
				if _, ok := temps[d.Name]; ok {
					return &Error{Pos: d.Pos, Msg: fmt.Sprintf("temp %s redefined", d.Name)}
				}
				if d.Temp == nil {
					d.Temp = &Temp{Name: d.Name, Def: s, Pos: d.Pos}
				}
				temps[d.Name] = d.Temp
			} else {
				v, ok := vars[d.Name]
				if !ok {
					return &Error{Pos: d.Pos, Msg: fmt.Sprintf("undefined var %s", d.Name)}
				}
				d.Var = v
			}
		}
	case *ExprStmt:
		return resolveExpr(s.X, vars, temps)
	case *Return:
		for _, e := range s.Results {
			if err := resolveExpr(e, vars, temps); err != nil {
				return err
			}
		}
	case *Br:
		if s.Cond != nil {
			if err := resolveExpr(s.Cond, vars, temps); err != nil {
				return err
			}
		}
		for _, e := range s.Args {
			if err := resolveExpr(e, vars, temps); err != nil {
				return err
			}
		}
	case *Unreachable:
		// ok
	default:
		return &Error{Pos: s.GetPos(), Msg: fmt.Sprintf("unknown stmt %T", s)}
	}
	return nil
}

func resolveExpr(e Expr, vars map[string]*Var, temps map[string]*Temp) error {
	switch e := e.(type) {
	case *Const:
		return nil
	case *Name:
		if strings.HasPrefix(e.Raw, "%") {
			t, ok := temps[e.Raw]
			if !ok {
				return &Error{Pos: e.Pos, Msg: fmt.Sprintf("undefined temp %s", e.Raw)}
			}
			e.Temp = t
			return nil
		}
		v, ok := vars[e.Raw]
		if !ok {
			return &Error{Pos: e.Pos, Msg: fmt.Sprintf("undefined name %s", e.Raw)}
		}
		e.Var = v
		return nil
	case *Instr:
		for _, a := range e.Args {
			if err := resolveExpr(a, vars, temps); err != nil {
				return err
			}
		}
		return nil
	default:
		return &Error{Pos: e.GetPos(), Msg: fmt.Sprintf("unknown expr %T", e)}
	}
}

func cloneVarMap(m map[string]*Var) map[string]*Var {
	out := make(map[string]*Var, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func cloneTempMap(m map[string]*Temp) map[string]*Temp {
	out := make(map[string]*Temp, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
