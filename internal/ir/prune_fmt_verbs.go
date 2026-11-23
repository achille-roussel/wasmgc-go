package ir

import "strings"

// PruneDeadFmtVerbs blanks fmt verb-switch arms and flag branches that this
// program cannot reach. Print/Println/Fprint/Fprintln only ever pass verb
// 'v' (118); when no Printf-family entry is live, other verbs and format
// flags (sharpV, plusV, …) are dead. That drops strconv.Quote /
// appendEscapedRune and the IsPrint unicode tables from fmt.Println("hello").
func PruneDeadFmtVerbs(m *Module) {
	if m == nil {
		return
	}
	liveVerb := map[int64]bool{}
	hasPrintf := false
	flagsEverSet := map[string]bool{} // field name → set somewhere

	for _, f := range m.Funcs {
		switch f.Name {
		case "fmt.Printf", "fmt.Sprintf", "fmt.Fprintf", "fmt.Appendf",
			"fmt.Errorf", "fmt.(*pp).doPrintf":
			hasPrintf = true
		}
		collectFmtFlagSets(f.Body, flagsEverSet)
		collectPrintArgVerbs(f.Body, liveVerb)
	}
	if hasPrintf {
		// Conservative: keep all verbs/flags when format strings exist.
		return
	}
	if len(liveVerb) == 0 {
		liveVerb[int64('v')] = true
	}
	// Println paths always use 'v'; keep 'T' only if observed.
	for _, f := range m.Funcs {
		switch f.Name {
		case "fmt.(*pp).fmtString", "fmt.(*pp).fmtBool", "fmt.(*pp).fmtInteger",
			"fmt.(*pp).fmtBytes", "fmt.(*pp).fmtPointer", "fmt.(*pp).printArg",
			"fmt.(*pp).printValue", "fmt.(*fmt).fmtInteger":
			if f.Body != nil {
				pruneVerbRegion(f.Body, liveVerb, flagsEverSet)
			}
		}
	}
}

func collectPrintArgVerbs(r Region, live map[int64]bool) {
	if r == nil {
		return
	}
	switch r := r.(type) {
	case *Seq:
		for _, s := range r.Stmts {
			collectPrintArgVerbsStmt(s, live)
		}
	case *Block:
		collectPrintArgVerbs(r.Body, live)
	case *Loop:
		collectPrintArgVerbs(r.Body, live)
	case *If:
		collectPrintArgVerbs(r.Then, live)
		collectPrintArgVerbs(r.Else, live)
	case *Switch:
		for _, c := range r.Cases {
			collectPrintArgVerbs(c.Body, live)
		}
		collectPrintArgVerbs(r.Default, live)
	case *Try:
		collectPrintArgVerbs(r.Body, live)
		collectPrintArgVerbs(r.Handler, live)
	case *Dispatch:
		for _, arm := range r.Arms {
			collectPrintArgVerbs(arm, live)
		}
	}
}

func collectPrintArgVerbsStmt(s Stmt, live map[int64]bool) {
	switch s := s.(type) {
	case *ExprStmt:
		notePrintArgVerb(s.X, live)
	case *Assign:
		notePrintArgVerb(s.Rhs, live)
	case Region:
		collectPrintArgVerbs(s, live)
	}
}

func notePrintArgVerb(e Expr, live map[int64]bool) {
	in, ok := e.(*Instr)
	if !ok {
		return
	}
	if in.Op == OpCall {
		switch in.Func {
		case "fmt.(*pp).printArg", "fmt.(*pp).printValue",
			"fmt.(*pp).fmtString", "fmt.(*pp).fmtBool",
			"fmt.(*pp).fmtInteger", "fmt.(*pp).fmtBytes",
			"fmt.(*pp).fmtPointer":
			if len(in.Args) >= 1 {
				// verb is last arg for these.
				if c, ok := constI32(in.Args[len(in.Args)-1]); ok {
					live[c] = true
				}
			}
		}
	}
	for _, a := range in.Args {
		notePrintArgVerb(a, live)
	}
}

func collectFmtFlagSets(r Region, set map[string]bool) {
	if r == nil {
		return
	}
	switch r := r.(type) {
	case *Seq:
		for _, s := range r.Stmts {
			collectFmtFlagSetsStmt(s, set)
		}
	case *Block:
		collectFmtFlagSets(r.Body, set)
	case *Loop:
		collectFmtFlagSets(r.Body, set)
	case *If:
		collectFmtFlagSets(r.Then, set)
		collectFmtFlagSets(r.Else, set)
	case *Switch:
		for _, c := range r.Cases {
			collectFmtFlagSets(c.Body, set)
		}
		collectFmtFlagSets(r.Default, set)
	case *Try:
		collectFmtFlagSets(r.Body, set)
		collectFmtFlagSets(r.Handler, set)
	case *Dispatch:
		for _, arm := range r.Arms {
			collectFmtFlagSets(arm, set)
		}
	}
}

func collectFmtFlagSetsStmt(s Stmt, set map[string]bool) {
	switch s := s.(type) {
	case *ExprStmt:
		noteFlagSet(s.X, set)
	case *Assign:
		noteFlagSet(s.Rhs, set)
	case Region:
		collectFmtFlagSets(s, set)
	}
}

func noteFlagSet(e Expr, set map[string]bool) {
	in, ok := e.(*Instr)
	if !ok {
		return
	}
	if in.Op == OpStructSet && len(in.Args) >= 2 {
		// struct.set $go.struct.fmt.fmtFlags FIELD obj, val
		if t, ok := in.Type.(*Ref); ok && strings.Contains(t.Named, "fmtFlags") {
			if in.Field != "" {
				set[in.Field] = true
			}
		}
		// Some emitters put field name in a string immediate.
		if in.Str != "" && strings.Contains(typeName(in.Type), "fmtFlags") {
			set[in.Str] = true
		}
	}
	for _, a := range in.Args {
		noteFlagSet(a, set)
	}
}

func typeName(t Type) string {
	if t == nil {
		return ""
	}
	if r, ok := t.(*Ref); ok {
		return r.Named
	}
	return t.String()
}

func pruneVerbRegion(r Region, liveVerb map[int64]bool, flags map[string]bool) {
	if r == nil {
		return
	}
	switch r := r.(type) {
	case *Seq:
		out := make([]Stmt, 0, len(r.Stmts))
		for _, s := range r.Stmts {
			if iff, ok := s.(*If); ok {
				out = append(out, foldVerbIf(iff, liveVerb, flags)...)
				continue
			}
			if reg, ok := s.(Region); ok {
				pruneVerbRegion(reg, liveVerb, flags)
			}
			out = append(out, s)
		}
		r.Stmts = out
	case *Block:
		pruneVerbRegion(r.Body, liveVerb, flags)
	case *Loop:
		pruneVerbRegion(r.Body, liveVerb, flags)
	case *If:
		// Not in a Seq: force else-only when dead.
		if verbOrFlagDead(r.Cond, liveVerb, flags) {
			pruneVerbRegion(r.Else, liveVerb, flags)
			r.Cond = &Const{Op: OpI32Const, I64: 1}
			r.Then = r.Else
			r.Else = &Seq{}
			return
		}
		pruneVerbRegion(r.Then, liveVerb, flags)
		pruneVerbRegion(r.Else, liveVerb, flags)
	case *Switch:
		for i := range r.Cases {
			pruneVerbRegion(r.Cases[i].Body, liveVerb, flags)
		}
		pruneVerbRegion(r.Default, liveVerb, flags)
	case *Try:
		pruneVerbRegion(r.Body, liveVerb, flags)
		pruneVerbRegion(r.Handler, liveVerb, flags)
	case *Dispatch:
		for _, arm := range r.Arms {
			pruneVerbRegion(arm, liveVerb, flags)
		}
	}
}

func foldVerbIf(iff *If, liveVerb map[int64]bool, flags map[string]bool) []Stmt {
	// Verb-code arms: if i32.eq tag, 'q' → keep else cascade.
	if kind, ok := condKindEq(iff.Cond); ok && kind > 32 && !liveVerb[kind] {
		pruneVerbRegion(iff.Else, liveVerb, flags)
		return regionStmts(iff.Else)
	}
	// Direct flag reads: if struct.get … sharpV { A } else { B }.
	// Only when the flag field is never written. Do not touch `if scL2`
	// name-conds that assign sc2 = plusV/sharpV — those must stay.
	if field, ok := condFlagGet(iff.Cond); ok && !flags[field] {
		pruneVerbRegion(iff.Else, liveVerb, flags)
		return regionStmts(iff.Else)
	}
	pruneVerbRegion(iff.Then, liveVerb, flags)
	pruneVerbRegion(iff.Else, liveVerb, flags)
	return []Stmt{iff}
}

func verbOrFlagDead(cond Expr, liveVerb map[int64]bool, flags map[string]bool) bool {
	if kind, ok := condKindEq(cond); ok && kind > 32 && !liveVerb[kind] {
		return true
	}
	if field, ok := condFlagGet(cond); ok && !flags[field] {
		return true
	}
	return false
}

// condFlagGet matches `struct.get $go.struct.fmt.fmtFlags FIELD …` used as if cond.
func condFlagGet(cond Expr) (string, bool) {
	in, ok := cond.(*Instr)
	if !ok {
		return "", false
	}
	if in.Op == OpStructGet {
		tn := typeName(in.Type)
		if strings.Contains(tn, "fmtFlags") || strings.Contains(tn, "fmt.fmt") {
			if in.Field != "" {
				return in.Field, true
			}
			if in.Str != "" {
				return in.Str, true
			}
		}
		// Nested: struct.get fmtFlags (struct.get fmt (struct.get pp fmt p))
	}
	// Walk: if cond is struct.get of a flags field through nested gets.
	return flagFieldFromGet(cond)
}

func flagFieldFromGet(e Expr) (string, bool) {
	in, ok := e.(*Instr)
	if !ok || in.Op != OpStructGet {
		return "", false
	}
	field := in.Field
	if field == "" {
		field = in.Str
	}
	switch field {
	case "sharpV", "plusV", "sharp", "plus", "minus", "space", "zero",
		"widthPresent", "precPresent", "plusSafe":
		return field, true
	}
	return "", false
}
