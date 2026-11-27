package ir

import "strings"

// PruneUnreachable removes functions and globals that are not reachable from
// exports. Reachability is a fixpoint over:
//
//   - call / ref.func edges from live function bodies
//   - global.get from live function bodies → live globals
//   - ref.func / call in live global initializers (itabs, type extras)
//
// Starting from every global init (the previous approach) kept every itab's
// method wrappers alive even when no live code loaded that itab — bloating
// fmt.Println("hello") well past the M8 128 KiB budget.
//
// ref.func into reflect.Value.Call / CallSlice does not keep those methods
// live (cmd/link-style): every reflect.Value itab lists Call, but fmt only
// needs Interface/Kind/etc. An actual call still marks them reachable.
func PruneUnreachable(m *Module) {
	if m == nil || len(m.Funcs) == 0 {
		return
	}
	byName := make(map[string]*Func, len(m.Funcs))
	for _, f := range m.Funcs {
		byName[f.Name] = f
	}
	byGlobal := make(map[string]*Global, len(m.Globals))
	for _, g := range m.Globals {
		byGlobal[g.Name] = g
	}

	liveFn := map[string]bool{}
	liveGl := map[string]bool{}
	var fnQ, glQ []string

	markFn := func(name string) {
		if name == "" || liveFn[name] {
			return
		}
		if _, ok := byName[name]; !ok {
			return
		}
		liveFn[name] = true
		fnQ = append(fnQ, name)
	}
	markGl := func(name string) {
		if name == "" || liveGl[name] {
			return
		}
		if _, ok := byGlobal[name]; !ok {
			return
		}
		liveGl[name] = true
		glQ = append(glQ, name)
	}

	for _, f := range m.Funcs {
		if f.Export != "" {
			markFn(f.Name)
		}
	}

	// Keep the whole print cluster if any print helper is live so println's
	// size golden stays stable across EnsurePrint linking whole print.go.
	printCluster := []string{
		"runtime.printstring", "runtime.printint", "runtime.printnl", "runtime.printuint",
		// printuint64 is linked with print.go but kept only when referenced
		// (issue 21887); seeding it here bloated println.wasm.
	}
	seedPrint := false
	seedGetArg := false
	for _, n := range printCluster {
		if byName[n] != nil {
			// Only force the cluster once one member is already reached.
			// Seed check happens after the first wave below.
			_ = n
		}
	}

	for len(fnQ) > 0 || len(glQ) > 0 {
		for len(fnQ) > 0 {
			name := fnQ[0]
			fnQ = fnQ[1:]
			f := byName[name]
			if f == nil || f.Body == nil {
				continue
			}
			collectRefsRegion(f.Body, markFn, markGl)
		}
		for len(glQ) > 0 {
			name := glQ[0]
			glQ = glQ[1:]
			g := byGlobal[name]
			if g == nil {
				continue
			}
			collectRefsExpr(g.Init, markFn, markGl)
		}
		if !seedPrint {
			for _, n := range printCluster {
				if liveFn[n] {
					seedPrint = true
					break
				}
			}
			if seedPrint {
				for _, n := range printCluster {
					markFn(n)
				}
			}
		}
		// getArgString's body is filled by codegen and calls getArg; keep the
		// body-less host import alive when the fill target is reachable.
		if !seedGetArg && liveFn["runtime.getArgString"] {
			seedGetArg = true
			markFn("runtime.getArg")
		}
		if liveFn["runtime.getEnvString"] {
			markFn("runtime.getEnv")
		}
		// JS string bridge fills call host imports that empty IR bodies omit.
		if liveFn["syscall.js.valueOfString"] {
			markFn("syscall.js.jsValue")
		}
		if liveFn["syscall.js.valueToString"] {
			markFn("syscall.js.jsStringCopy")
		}
	}

	out := m.Funcs[:0]
	for _, f := range m.Funcs {
		if liveFn[f.Name] {
			out = append(out, f)
		}
	}
	m.Funcs = out

	gout := m.Globals[:0]
	for _, g := range m.Globals {
		if liveGl[g.Name] {
			gout = append(gout, g)
		}
	}
	m.Globals = gout
}

func collectRefsRegion(r Region, markFn, markGl func(string)) {
	if r == nil {
		return
	}
	switch r := r.(type) {
	case *Seq:
		for _, s := range r.Stmts {
			collectRefsStmt(s, markFn, markGl)
		}
	case *Block:
		collectRefsRegion(r.Body, markFn, markGl)
	case *Loop:
		collectRefsRegion(r.Body, markFn, markGl)
	case *If:
		collectRefsExpr(r.Cond, markFn, markGl)
		collectRefsRegion(r.Then, markFn, markGl)
		collectRefsRegion(r.Else, markFn, markGl)
	case *Switch:
		collectRefsExpr(r.Tag, markFn, markGl)
		for _, c := range r.Cases {
			collectRefsRegion(c.Body, markFn, markGl)
		}
		collectRefsRegion(r.Default, markFn, markGl)
	case *Try:
		collectRefsRegion(r.Body, markFn, markGl)
		collectRefsRegion(r.Handler, markFn, markGl)
	case *Dispatch:
		for _, arm := range r.Arms {
			collectRefsRegion(arm, markFn, markGl)
		}
	}
}

func collectRefsStmt(s Stmt, markFn, markGl func(string)) {
	switch s := s.(type) {
	case *ExprStmt:
		collectRefsExpr(s.X, markFn, markGl)
	case *Assign:
		collectRefsExpr(s.Rhs, markFn, markGl)
	case *Return:
		for _, e := range s.Results {
			collectRefsExpr(e, markFn, markGl)
		}
	case *VarDef:
		// no refs
	case *Br:
		collectRefsExpr(s.Cond, markFn, markGl)
	case Region:
		collectRefsRegion(s, markFn, markGl)
	}
}

func collectRefsExpr(e Expr, markFn, markGl func(string)) {
	if e == nil {
		return
	}
	switch e := e.(type) {
	case *Instr:
		switch e.Op {
		case OpCall:
			markFn(e.Func)
		case OpRefFunc:
			// Itab RefFunc alone must not keep Call alive — its concrete-func
			// type-switch pulls many signatures into fmt hello (issue19113).
			// Unused $go.extra.func / inType helpers are omitted at emit time
			// (needsFuncParamAPI) rather than skipped here — live type inits
			// that do embed ref.func $go.func.inType.* must keep them.
			if !isReflectCallAPI(e.Func) {
				markFn(e.Func)
			}
		case OpGlobalGet, OpGlobalSet:
			// ScrubDeadGlobalStores removes write-only package-init stores first;
			// remaining sets (e.g. runtime.runqTail in sched) must keep the global.
			markGl(e.Global)
		}
		for _, a := range e.Args {
			collectRefsExpr(a, markFn, markGl)
		}
	}
}

// isReflectCallAPI reports reflect.Value.Call / CallSlice and itab wrappers
// for those methods ($go.wrap.…reflect_Value….Call).
func isReflectCallAPI(name string) bool {
	switch name {
	case "reflect.Value.Call", "reflect.Value.CallSlice":
		return true
	}
	if !strings.Contains(name, "reflect_Value") && !strings.Contains(name, "reflect.Value") {
		return false
	}
	return strings.HasSuffix(name, ".Call") || strings.HasSuffix(name, ".CallSlice")
}

// Legacy helpers kept for any external callers / tests that used the old names.
func collectFuncRefsRegion(r Region, mark func(string)) {
	collectRefsRegion(r, mark, func(string) {})
}

func collectFuncRefsStmt(s Stmt, mark func(string)) {
	collectRefsStmt(s, mark, func(string) {})
}

func collectFuncRefsExpr(e Expr, mark func(string)) {
	collectRefsExpr(e, mark, func(string) {})
}
