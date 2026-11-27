package ir

import "testing"

// RuntimeOps wraps If cond string-lit expansion as a nested *Seq stmt inside
// package inits. markStmtNames must walk those Seqs or ScrubDeadGlobalStores
// drops switch-tag assigns (uninitialized non-defaultable local at validate).
func TestScrubDeadGlobalStores_keepsSwitchTagAssignInNestedSeq(t *testing.T) {
	tag := &Var{Name: "tag", Typ: &Ref{Named: "$go.string"}}
	v := &Var{Name: "v", Typ: &Ref{Named: "$go.string"}}
	strTmp := &Temp{Name: "%str_on", Typ: &Ref{Named: "$go.string"}}
	body := &Seq{Stmts: []Stmt{
		&VarDef{Var: v},
		&VarDef{Var: tag},
		&Assign{Lhs: []Dest{{Var: v, Name: "v"}}, Rhs: &Instr{Op: OpCall, Func: "Value", Type: &Ref{Named: "$go.string"}}},
		&Assign{Lhs: []Dest{{Var: tag, Name: "tag"}}, Rhs: &Name{Raw: "v", Var: v}},
		&Block{Name: "$sw.1", Body: &Seq{Stmts: []Stmt{
			// Nested Seq as Stmt — the RuntimeOps shape for If+string lit pre.
			&Seq{Stmts: []Stmt{
				&Assign{Lhs: []Dest{{Temp: strTmp, Name: strTmp.Name}}, Rhs: &Instr{
					Op: OpStructNew, Type: &Ref{Named: "$go.string"},
				}},
				&If{
					Cond: &Instr{Op: OpCall, Func: "runtime.stringeq", Args: []Expr{
						&Name{Raw: "tag", Var: tag},
						&Name{Raw: strTmp.Name, Temp: strTmp},
					}},
					Then: &Seq{Stmts: []Stmt{&Br{Label: "$sw.1"}}},
					Else: &Seq{Stmts: []Stmt{&Br{Label: "$sw.1"}}},
				},
			}},
		}}},
		&Return{},
	}}
	f := &Func{Name: "main.init.0", Locals: []*Var{v, tag}, Body: body}
	m := &Module{Funcs: []*Func{f}}
	ScrubDeadGlobalStores(m)

	found := false
	if seq, ok := f.Body.(*Seq); ok {
		for _, s := range seq.Stmts {
			a, ok := s.(*Assign)
			if !ok || len(a.Lhs) != 1 {
				continue
			}
			if a.Lhs[0].Var == tag {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("tag = v was scrubbed; nested Seq hid the switch use from init DCE")
	}
}
