package ir

import "strings"

// PruneNoopCalls removes calls whose callee is a known no-op that discards
// its arguments. Today that is runtime.RegisterPoolCleanup (finalizer/GC-cycle
// pool cleanup is a no-op — decision 3), so the sync.poolCleanup closure and
// its body become unreachable for DCE.
func PruneNoopCalls(m *Module) {
	if m == nil {
		return
	}
	for _, f := range m.Funcs {
		if f.Body != nil {
			f.Body = pruneNoopRegion(f.Body)
		}
	}
}

func pruneNoopRegion(r Region) Region {
	if r == nil {
		return nil
	}
	switch r := r.(type) {
	case *Seq:
		out := make([]Stmt, 0, len(r.Stmts))
		for _, s := range r.Stmts {
			if es, ok := s.(*ExprStmt); ok && isNoopCall(es.X) {
				continue
			}
			if a, ok := s.(*Assign); ok && len(a.Lhs) == 0 && isNoopCall(a.Rhs) {
				continue
			}
			if reg, ok := s.(Region); ok {
				out = append(out, pruneNoopRegion(reg).(Stmt))
				continue
			}
			out = append(out, s)
		}
		r.Stmts = out
		return r
	case *Block:
		r.Body = pruneNoopRegion(r.Body)
		return r
	case *Loop:
		r.Body = pruneNoopRegion(r.Body)
		return r
	case *If:
		r.Then = pruneNoopRegion(r.Then)
		r.Else = pruneNoopRegion(r.Else)
		return r
	case *Switch:
		for i := range r.Cases {
			r.Cases[i].Body = pruneNoopRegion(r.Cases[i].Body)
		}
		r.Default = pruneNoopRegion(r.Default)
		return r
	case *Try:
		r.Body = pruneNoopRegion(r.Body)
		r.Handler = pruneNoopRegion(r.Handler)
		return r
	case *Dispatch:
		for i := range r.Arms {
			r.Arms[i] = pruneNoopRegion(r.Arms[i])
		}
		return r
	}
	return r
}

func isNoopCall(e Expr) bool {
	in, ok := e.(*Instr)
	if !ok || in.Op != OpCall {
		return false
	}
	switch in.Func {
	case "runtime.RegisterPoolCleanup", "runtime.registerPoolCleanup":
		return true
	}
	return false
}

// ScrubDeadGlobalStores deletes global.set to globals that are never read
// (global.get), then deletes pure assignments that only fed those stores.
// Call after reachability has marked read-globals, or use PruneDeadInits which
// computes the read set itself.
//
// Package inits eagerly build large const tables (strconv pow10Tab, math
// gamma coeffs, …). After fmt type-case pruning those tables are often
// write-only; scrubbing them is the main fmt-hello size win.
func ScrubDeadGlobalStores(m *Module) {
	if m == nil {
		return
	}
	read := map[string]bool{}
	for _, f := range m.Funcs {
		collectGlobalGets(f.Body, read)
	}
	for _, g := range m.Globals {
		collectGlobalGetsExpr(g.Init, read)
	}
	// Only scrub package init functions. Full-function dead-assign elimination
	// is subtle around non-defaultable locals (Wasm definite-assignment); inits
	// are flat store sequences where it is safe and where the size win lives.
	for _, f := range m.Funcs {
		if f.Body == nil {
			continue
		}
		if !isPackageInit(f.Name) {
			continue
		}
		f.Body = scrubRegion(f.Body, read)
		f.Locals = usedLocals(f)
	}
	// Drop globals that are neither read nor still written outside scrubbed inits.
	refs := map[string]bool{}
	for k := range read {
		refs[k] = true
	}
	for _, f := range m.Funcs {
		collectGlobalRefs(f.Body, refs) // get + set
	}
	gout := m.Globals[:0]
	for _, g := range m.Globals {
		if refs[g.Name] || read[g.Name] {
			gout = append(gout, g)
		}
	}
	m.Globals = gout
}

// collectGlobalRefs marks globals mentioned by global.get or global.set.
func collectGlobalRefs(r Region, refs map[string]bool) {
	if r == nil {
		return
	}
	switch r := r.(type) {
	case *Seq:
		for _, s := range r.Stmts {
			collectGlobalRefsStmt(s, refs)
		}
	case *Block:
		collectGlobalRefs(r.Body, refs)
	case *Loop:
		collectGlobalRefs(r.Body, refs)
	case *If:
		collectGlobalRefsExprAll(r.Cond, refs)
		collectGlobalRefs(r.Then, refs)
		collectGlobalRefs(r.Else, refs)
	case *Switch:
		collectGlobalRefsExprAll(r.Tag, refs)
		for _, c := range r.Cases {
			collectGlobalRefs(c.Body, refs)
		}
		collectGlobalRefs(r.Default, refs)
	case *Try:
		collectGlobalRefs(r.Body, refs)
		collectGlobalRefs(r.Handler, refs)
	case *Dispatch:
		for _, arm := range r.Arms {
			collectGlobalRefs(arm, refs)
		}
	}
}

func collectGlobalRefsStmt(s Stmt, refs map[string]bool) {
	switch s := s.(type) {
	case *ExprStmt:
		collectGlobalRefsExprAll(s.X, refs)
	case *Assign:
		collectGlobalRefsExprAll(s.Rhs, refs)
	case *Return:
		for _, e := range s.Results {
			collectGlobalRefsExprAll(e, refs)
		}
	case *Br:
		collectGlobalRefsExprAll(s.Cond, refs)
		for _, e := range s.Args {
			collectGlobalRefsExprAll(e, refs)
		}
	case Region:
		collectGlobalRefs(s, refs)
	}
}

func collectGlobalRefsExprAll(e Expr, refs map[string]bool) {
	if e == nil {
		return
	}
	in, ok := e.(*Instr)
	if !ok {
		return
	}
	if (in.Op == OpGlobalGet || in.Op == OpGlobalSet) && in.Global != "" {
		refs[in.Global] = true
	}
	for _, a := range in.Args {
		collectGlobalRefsExprAll(a, refs)
	}
}

func isPackageInit(name string) bool {
	// pkg.init.N or pkg.init
	if strings.HasSuffix(name, ".init") {
		return true
	}
	return strings.Contains(name, ".init.")
}

func collectGlobalGets(r Region, read map[string]bool) {
	if r == nil {
		return
	}
	switch r := r.(type) {
	case *Seq:
		for _, s := range r.Stmts {
			collectGlobalGetsStmt(s, read)
		}
	case *Block:
		collectGlobalGets(r.Body, read)
	case *Loop:
		collectGlobalGets(r.Body, read)
	case *If:
		collectGlobalGetsExpr(r.Cond, read)
		collectGlobalGets(r.Then, read)
		collectGlobalGets(r.Else, read)
	case *Switch:
		collectGlobalGetsExpr(r.Tag, read)
		for _, c := range r.Cases {
			collectGlobalGets(c.Body, read)
		}
		collectGlobalGets(r.Default, read)
	case *Try:
		collectGlobalGets(r.Body, read)
		collectGlobalGets(r.Handler, read)
	case *Dispatch:
		for _, arm := range r.Arms {
			collectGlobalGets(arm, read)
		}
	}
}

func collectGlobalGetsStmt(s Stmt, read map[string]bool) {
	switch s := s.(type) {
	case *ExprStmt:
		collectGlobalGetsExpr(s.X, read)
	case *Assign:
		collectGlobalGetsExpr(s.Rhs, read)
	case *Return:
		for _, e := range s.Results {
			collectGlobalGetsExpr(e, read)
		}
	case *Br:
		collectGlobalGetsExpr(s.Cond, read)
		for _, e := range s.Args {
			collectGlobalGetsExpr(e, read)
		}
	case Region:
		collectGlobalGets(s, read)
	}
}

func collectGlobalGetsExpr(e Expr, read map[string]bool) {
	if e == nil {
		return
	}
	switch e := e.(type) {
	case *Instr:
		if e.Op == OpGlobalGet && e.Global != "" {
			read[e.Global] = true
		}
		// Deliberately ignore OpGlobalSet — a write does not keep the global.
		for _, a := range e.Args {
			collectGlobalGetsExpr(a, read)
		}
	}
}

func scrubRegion(r Region, read map[string]bool) Region {
	if r == nil {
		return nil
	}
	switch r := r.(type) {
	case *Seq:
		return scrubSeq(r, read)
	case *Block:
		r.Body = scrubRegion(r.Body, read)
		return r
	case *Loop:
		r.Body = scrubRegion(r.Body, read)
		return r
	case *If:
		r.Then = scrubRegion(r.Then, read)
		r.Else = scrubRegion(r.Else, read)
		return r
	case *Switch:
		for i := range r.Cases {
			r.Cases[i].Body = scrubRegion(r.Cases[i].Body, read)
		}
		r.Default = scrubRegion(r.Default, read)
		return r
	case *Try:
		r.Body = scrubRegion(r.Body, read)
		r.Handler = scrubRegion(r.Handler, read)
		return r
	case *Dispatch:
		for i := range r.Arms {
			r.Arms[i] = scrubRegion(r.Arms[i], read)
		}
		return r
	}
	return r
}

// scrubSeq removes dead global.set and pure assigns whose results are unused.
// Uses a backward liveness pass over the flat statement list, then recurses
// into nested regions.
func scrubSeq(seq *Seq, read map[string]bool) *Seq {
	// First scrub nested regions inside statements.
	for i, s := range seq.Stmts {
		switch s := s.(type) {
		case *Block:
			s.Body = scrubRegion(s.Body, read)
		case *Loop:
			s.Body = scrubRegion(s.Body, read)
		case *If:
			s.Then = scrubRegion(s.Then, read)
			s.Else = scrubRegion(s.Else, read)
		case *Switch:
			for j := range s.Cases {
				s.Cases[j].Body = scrubRegion(s.Cases[j].Body, read)
			}
			s.Default = scrubRegion(s.Default, read)
		case *Try:
			s.Body = scrubRegion(s.Body, read)
			s.Handler = scrubRegion(s.Handler, read)
		case *Dispatch:
			for j := range s.Arms {
				s.Arms[j] = scrubRegion(s.Arms[j], read)
			}
		}
		seq.Stmts[i] = s
	}

	live := map[string]bool{}
	out := make([]Stmt, len(seq.Stmts))
	n := 0
	for i := len(seq.Stmts) - 1; i >= 0; i-- {
		s := seq.Stmts[i]
		switch s := s.(type) {
		case *VarDef:
			// Keep all VarDefs here; usedLocals trims Func.Locals after.
			// Dropping a VarDef while an Assign to that var remains breaks codegen.
			out[n] = s
			n++
		case *Assign:
			if isDeadGlobalSet(s.Rhs, read) && len(s.Lhs) == 0 {
				// bare global.set as assign-without-lhs — drop
				continue
			}
			if in, ok := s.Rhs.(*Instr); ok && in.Op == OpGlobalSet && !read[in.Global] {
				// Drop the store; do not mark RHS names live.
				continue
			}
			keep := false
			if len(s.Lhs) == 0 {
				// Side-effecting statement (call, store, …).
				keep = true
			} else {
				for _, d := range s.Lhs {
					if name := destKey(d); name != "" && live[name] {
						keep = true
						break
					}
				}
				if !keep && exprHasSideEffects(s.Rhs) {
					keep = true
				}
			}
			if !keep {
				continue
			}
			for _, d := range s.Lhs {
				if name := destKey(d); name != "" {
					delete(live, name) // redefined; prior value not needed above
				}
			}
			markExprNames(s.Rhs, live)
			out[n] = s
			n++
		case *ExprStmt:
			if isDeadGlobalSet(s.X, read) {
				continue
			}
			if in, ok := s.X.(*Instr); ok && in.Op == OpGlobalSet && !read[in.Global] {
				continue
			}
			markExprNames(s.X, live)
			out[n] = s
			n++
		case *Return:
			for _, e := range s.Results {
				markExprNames(e, live)
			}
			out[n] = s
			n++
		case *Br:
			markExprNames(s.Cond, live)
			for _, e := range s.Args {
				markExprNames(e, live)
			}
			out[n] = s
			n++
		case *Unreachable:
			out[n] = s
			n++
		default:
			// Nested region or unknown: keep and mark conservatively.
			markStmtNames(s, live)
			out[n] = s
			n++
		}
	}
	// Reverse back to original order.
	stmts := make([]Stmt, n)
	for i := 0; i < n; i++ {
		stmts[i] = out[n-1-i]
	}
	seq.Stmts = stmts
	return seq
}

func isDeadGlobalSet(e Expr, read map[string]bool) bool {
	in, ok := e.(*Instr)
	return ok && in.Op == OpGlobalSet && !read[in.Global]
}

func destKey(d Dest) string {
	if d.Var != nil {
		return d.Var.Name
	}
	if d.Temp != nil {
		return d.Temp.Name
	}
	return d.Name
}

func markExprNames(e Expr, live map[string]bool) {
	if e == nil {
		return
	}
	switch e := e.(type) {
	case *Name:
		if e.Var != nil {
			live[e.Var.Name] = true
		}
		if e.Temp != nil {
			live[e.Temp.Name] = true
		}
		if e.Raw != "" && e.Var == nil && e.Temp == nil {
			live[e.Raw] = true
		}
	case *Instr:
		for _, a := range e.Args {
			markExprNames(a, live)
		}
	}
}

func markStmtNames(s Stmt, live map[string]bool) {
	switch s := s.(type) {
	case *ExprStmt:
		markExprNames(s.X, live)
	case *Assign:
		markExprNames(s.Rhs, live)
	case *Return:
		for _, e := range s.Results {
			markExprNames(e, live)
		}
	case *Br:
		markExprNames(s.Cond, live)
		for _, e := range s.Args {
			markExprNames(e, live)
		}
	case *Block:
		markRegionNames(s.Body, live)
	case *Loop:
		markRegionNames(s.Body, live)
	case *If:
		markExprNames(s.Cond, live)
		markRegionNames(s.Then, live)
		markRegionNames(s.Else, live)
	case *Switch:
		markExprNames(s.Tag, live)
		for _, c := range s.Cases {
			markRegionNames(c.Body, live)
		}
		markRegionNames(s.Default, live)
	case *Try:
		markRegionNames(s.Body, live)
		markRegionNames(s.Handler, live)
	case *Dispatch:
		for _, arm := range s.Arms {
			markRegionNames(arm, live)
		}
	}
}

func markRegionNames(r Region, live map[string]bool) {
	if r == nil {
		return
	}
	if seq, ok := r.(*Seq); ok {
		for _, s := range seq.Stmts {
			markStmtNames(s, live)
		}
		return
	}
	markStmtNames(r, live)
}

// exprHasSideEffects reports whether e may observe or mutate state that
// scrubbing must preserve. Pure GC allocations and arithmetic are false.
func exprHasSideEffects(e Expr) bool {
	if e == nil {
		return false
	}
	in, ok := e.(*Instr)
	if !ok {
		return false
	}
	switch in.Op {
	case OpCall, OpCallRef, OpThrow, OpThrowRef,
		OpStructSet, OpArraySet, OpArrayCopy, OpArrayFill,
		OpGlobalSet, OpMemoryGrow, OpMemoryCopy, OpMemoryFill,
		OpPanic, OpDefer, OpGo:
		return true
	}
	for _, a := range in.Args {
		if exprHasSideEffects(a) {
			return true
		}
	}
	return false
}

func usedLocals(f *Func) []*Var {
	if f == nil || len(f.Locals) == 0 {
		return f.Locals
	}
	used := map[string]bool{}
	markRegionNames(f.Body, used)
	for _, p := range f.Params {
		if p != nil {
			used[p.Name] = true // params stay even if unread
		}
	}
	out := f.Locals[:0]
	for _, v := range f.Locals {
		if v != nil && used[v.Name] {
			out = append(out, v)
		}
	}
	return out
}

// PruneUnusedData drops passive data segments not referenced by array.new_data.
func PruneUnusedData(m *Module) {
	if m == nil || len(m.Data) == 0 {
		return
	}
	used := map[string]bool{}
	markData := func(e Expr) {
		walkDataRefs(e, used)
	}
	for _, f := range m.Funcs {
		walkDataRegion(f.Body, used)
	}
	for _, g := range m.Globals {
		markData(g.Init)
	}
	out := m.Data[:0]
	for _, d := range m.Data {
		if used[d.Name] {
			out = append(out, d)
		}
	}
	m.Data = out
}

func walkDataRegion(r Region, used map[string]bool) {
	if r == nil {
		return
	}
	switch r := r.(type) {
	case *Seq:
		for _, s := range r.Stmts {
			walkDataStmt(s, used)
		}
	case *Block:
		walkDataRegion(r.Body, used)
	case *Loop:
		walkDataRegion(r.Body, used)
	case *If:
		walkDataRefs(r.Cond, used)
		walkDataRegion(r.Then, used)
		walkDataRegion(r.Else, used)
	case *Switch:
		walkDataRefs(r.Tag, used)
		for _, c := range r.Cases {
			walkDataRegion(c.Body, used)
		}
		walkDataRegion(r.Default, used)
	case *Try:
		walkDataRegion(r.Body, used)
		walkDataRegion(r.Handler, used)
	case *Dispatch:
		for _, arm := range r.Arms {
			walkDataRegion(arm, used)
		}
	}
}

func walkDataStmt(s Stmt, used map[string]bool) {
	switch s := s.(type) {
	case *ExprStmt:
		walkDataRefs(s.X, used)
	case *Assign:
		walkDataRefs(s.Rhs, used)
	case *Return:
		for _, e := range s.Results {
			walkDataRefs(e, used)
		}
	case *Br:
		walkDataRefs(s.Cond, used)
		for _, e := range s.Args {
			walkDataRefs(e, used)
		}
	case Region:
		walkDataRegion(s, used)
	}
}

func walkDataRefs(e Expr, used map[string]bool) {
	if e == nil {
		return
	}
	in, ok := e.(*Instr)
	if !ok {
		return
	}
	if in.Op == OpArrayNewData && in.Data != "" {
		used[in.Data] = true
	}
	for _, a := range in.Args {
		walkDataRefs(a, used)
	}
}

// PruneEmptyInits removes call sites in main.run for init functions whose
// body is only a return (after scrubbing). The init funcs themselves are
// dropped by a subsequent PruneUnreachable.
func PruneEmptyInits(m *Module) {
	if m == nil {
		return
	}
	empty := map[string]bool{}
	for _, f := range m.Funcs {
		if strings.Contains(f.Name, ".init.") && isEmptyInit(f) {
			empty[f.Name] = true
		}
	}
	if len(empty) == 0 {
		return
	}
	run := m.LookupFunc("main.run")
	if run == nil || run.Body == nil {
		return
	}
	run.Body = dropEmptyInitCalls(run.Body, empty)
}

func isEmptyInit(f *Func) bool {
	if f == nil || f.Body == nil {
		return false
	}
	seq, ok := f.Body.(*Seq)
	if !ok {
		return false
	}
	for _, s := range seq.Stmts {
		switch s.(type) {
		case *Return:
			continue
		case *VarDef:
			continue
		default:
			return false
		}
	}
	return true
}

func dropEmptyInitCalls(r Region, empty map[string]bool) Region {
	seq, ok := r.(*Seq)
	if !ok {
		// EH-wrapped run: walk into try body.
		switch r := r.(type) {
		case *Try:
			r.Body = dropEmptyInitCalls(r.Body, empty)
			return r
		case *Block:
			r.Body = dropEmptyInitCalls(r.Body, empty)
			return r
		}
		return r
	}
	out := seq.Stmts[:0]
	for _, s := range seq.Stmts {
		if es, ok := s.(*ExprStmt); ok {
			if in, ok := es.X.(*Instr); ok && in.Op == OpCall && empty[in.Func] {
				continue
			}
		}
		out = append(out, s)
	}
	seq.Stmts = out
	return seq
}

// PruneDeadInits is the combined post-DCE cleanup: scrub write-only globals,
// drop unused data, drop empty package inits from main.run. Call after
// PruneUnreachable (or after PruneDeadFmtTypeCases + a reachability pass).
func PruneDeadInits(m *Module) {
	ScrubDeadGlobalStores(m)
	PruneUnusedData(m)
	PruneEmptyInits(m)
}
