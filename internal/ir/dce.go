package ir

// PruneUnreachable removes functions that are not reachable from exports or
// from ref.func / call sites in live functions and global initializers.
// Used so a GOROOT import of rewrite package runtime (via sync, etc.) does not
// keep every runtime file that PackageHigh lowered — same end state as
// EnsureSched's selective AppendFiles.
func PruneUnreachable(m *Module) {
	if m == nil || len(m.Funcs) == 0 {
		return
	}
	byName := make(map[string]*Func, len(m.Funcs))
	for _, f := range m.Funcs {
		byName[f.Name] = f
	}
	live := map[string]bool{}
	var queue []string
	mark := func(name string) {
		if name == "" || live[name] {
			return
		}
		if _, ok := byName[name]; !ok {
			return
		}
		live[name] = true
		queue = append(queue, name)
	}
	for _, f := range m.Funcs {
		if f.Export != "" {
			mark(f.Name)
		}
	}
	for _, g := range m.Globals {
		collectFuncRefsExpr(g.Init, mark)
	}
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		f := byName[name]
		if f == nil || f.Body == nil {
			continue
		}
		collectFuncRefsRegion(f.Body, mark)
	}
	// Keep the whole print cluster if any print helper is live so println's
	// size golden (970) stays stable across EnsurePrint linking whole print.go.
	printCluster := []string{
		"runtime.printstring", "runtime.printint", "runtime.printnl", "runtime.printuint",
	}
	keepPrint := false
	for _, n := range printCluster {
		if live[n] {
			keepPrint = true
			break
		}
	}
	if keepPrint {
		for _, n := range printCluster {
			mark(n)
		}
		for len(queue) > 0 {
			name := queue[0]
			queue = queue[1:]
			f := byName[name]
			if f == nil || f.Body == nil {
				continue
			}
			collectFuncRefsRegion(f.Body, mark)
		}
	}
	out := m.Funcs[:0]
	for _, f := range m.Funcs {
		if live[f.Name] {
			out = append(out, f)
		}
	}
	m.Funcs = out
}

func collectFuncRefsRegion(r Region, mark func(string)) {
	if r == nil {
		return
	}
	switch r := r.(type) {
	case *Seq:
		for _, s := range r.Stmts {
			collectFuncRefsStmt(s, mark)
		}
	case *Block:
		collectFuncRefsRegion(r.Body, mark)
	case *Loop:
		collectFuncRefsRegion(r.Body, mark)
	case *If:
		collectFuncRefsExpr(r.Cond, mark)
		collectFuncRefsRegion(r.Then, mark)
		collectFuncRefsRegion(r.Else, mark)
	case *Switch:
		collectFuncRefsExpr(r.Tag, mark)
		for _, c := range r.Cases {
			collectFuncRefsRegion(c.Body, mark)
		}
		collectFuncRefsRegion(r.Default, mark)
	case *Try:
		collectFuncRefsRegion(r.Body, mark)
		collectFuncRefsRegion(r.Handler, mark)
	case *Dispatch:
		for _, arm := range r.Arms {
			collectFuncRefsRegion(arm, mark)
		}
	}
}

func collectFuncRefsStmt(s Stmt, mark func(string)) {
	switch s := s.(type) {
	case *ExprStmt:
		collectFuncRefsExpr(s.X, mark)
	case *Assign:
		collectFuncRefsExpr(s.Rhs, mark)
	case *Return:
		for _, e := range s.Results {
			collectFuncRefsExpr(e, mark)
		}
	case *VarDef:
		// no refs
	case *Br:
		collectFuncRefsExpr(s.Cond, mark)
	case Region:
		collectFuncRefsRegion(s, mark)
	}
}

func collectFuncRefsExpr(e Expr, mark func(string)) {
	if e == nil {
		return
	}
	switch e := e.(type) {
	case *Instr:
		switch e.Op {
		case OpCall, OpRefFunc:
			mark(e.Func)
		}
		for _, a := range e.Args {
			collectFuncRefsExpr(a, mark)
		}
	}
}
