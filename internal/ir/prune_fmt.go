package ir

import "strings"

// PruneDeadFmtTypeCases blanks unreachable fmt formatting arms so DCE can
// drop strconv float helpers, fmtsort, etc. (M8 fmt hello size budget).
//
//  1. Type-switch arms in printArg / handleMethods guarded by
//     ref.eq … $go.type.KEY when KEY has no $go.itab.any.@.KEY.
//  2. Kind-switch arms in printValue (if i32.eq tag, N) for kinds whose
//     concrete types are never boxed as any in this program.
func PruneDeadFmtTypeCases(m *Module) {
	if m == nil {
		return
	}
	boxed := map[string]bool{}
	for _, g := range m.Globals {
		const p = "$go.itab.any.@."
		if strings.HasPrefix(g.Name, p) {
			boxed[strings.TrimPrefix(g.Name, p)] = true
		}
	}
	// Always keep string/bool arms — Println paths and nil formatting use them.
	boxed["string"] = true
	boxed["bool"] = true
	// printValue walks struct fields via reflect without boxing them as any.
	// Mark field types of boxed structs so float/int arms stay live.
	markBoxedStructFieldTypes(m, boxed)

	liveKinds := kindsFromBoxed(boxed)

	for _, f := range m.Funcs {
		switch f.Name {
		case "fmt.(*pp).printArg", "fmt.(*pp).handleMethods":
			if f.Body != nil {
				pruneFmtRegion(f.Body, boxed, map[string]string{}, nil)
			}
		case "fmt.(*pp).printValue":
			if f.Body != nil {
				pruneFmtRegion(f.Body, boxed, map[string]string{}, liveKinds)
			}
		}
	}
}

// reflect.Kind values (must match reflect package).
const (
	kindBool       = 1
	kindInt        = 2
	kindInt8       = 3
	kindInt16      = 4
	kindInt32      = 5
	kindInt64      = 6
	kindUint       = 7
	kindUint8      = 8
	kindUint16     = 9
	kindUint32     = 10
	kindUint64     = 11
	kindUintptr    = 12
	kindFloat32    = 13
	kindFloat64    = 14
	kindComplex64  = 15
	kindComplex128 = 16
	kindArray      = 17
	kindChan       = 18
	kindFunc       = 19
	kindInterface  = 20
	kindMap        = 21
	kindPtr        = 22
	kindSlice      = 23
	kindString     = 24
	kindStruct     = 25
	kindUnsafePtr  = 26
)

// markBoxedStructFieldTypes adds Go type keys for fields of boxed structs so
// printValue's Kind arms (float64, int, …) are not blanked when those types
// only appear as struct fields (never boxed as any themselves).
func markBoxedStructFieldTypes(m *Module, boxed map[string]bool) {
	queue := make([]string, 0, len(boxed))
	for k := range boxed {
		queue = append(queue, k)
	}
	seen := map[string]bool{}
	for len(queue) > 0 {
		key := queue[0]
		queue = queue[1:]
		if seen[key] {
			continue
		}
		seen[key] = true
		td := m.LookupType("$go.struct." + key)
		if td == nil || td.Kind != TypeStruct {
			continue
		}
		for _, f := range td.Fields {
			markFmtFieldType(m, f.Type, boxed, &queue)
		}
	}
}

func markFmtFieldType(m *Module, t Type, boxed map[string]bool, queue *[]string) {
	switch t := t.(type) {
	case *NumType:
		switch t.Kind {
		case F32:
			boxed["float32"] = true
		case F64:
			boxed["float64"] = true
		case I64:
			// IR collapses int64/uint64 to i64; keep both Kind families.
			boxed["int64"] = true
			boxed["uint64"] = true
		case I32:
			boxed["int"] = true
			boxed["int32"] = true
			boxed["uint"] = true
			boxed["uint32"] = true
			boxed["uintptr"] = true
		case I8:
			boxed["int8"] = true
			boxed["uint8"] = true
			boxed["byte"] = true
		case I16:
			boxed["int16"] = true
			boxed["uint16"] = true
		}
	case *Ref:
		switch {
		case t.Named == "$go.string":
			boxed["string"] = true
		case strings.HasPrefix(t.Named, "$go.struct."):
			key := strings.TrimPrefix(t.Named, "$go.struct.")
			if !boxed[key] {
				boxed[key] = true
				*queue = append(*queue, key)
			}
		case strings.HasPrefix(t.Named, "$go.slice."):
			boxed["[]byte"] = true // ensures slice kind; elem handled below
			if td := m.LookupType(t.Named); td != nil {
				for _, f := range td.Fields {
					if f.Name == "data" {
						markFmtFieldType(m, f.Type, boxed, queue)
					}
				}
			}
		case strings.HasPrefix(t.Named, "$go.array.") || t.Named == "$go.bytes" || t.Named == "$go.i16s":
			boxed["array"] = true // kindsFromBoxed treats array via always-live kindArray
			if td := m.LookupType(t.Named); td != nil && td.Elem != nil {
				markFmtFieldType(m, td.Elem, boxed, queue)
			}
		case strings.HasPrefix(t.Named, "$go.box."):
			// ignore
		case t.Named == "$go.iface":
			boxed["interface"] = true
		case t.Named == "$go.hmap" || strings.HasPrefix(t.Named, "$go.hmap."):
			// Map fields erase to hmap refs; kindsFromBoxed keys on "map[".
			boxed["map["] = true
		case t.Named == "$go.chan" || strings.HasPrefix(t.Named, "$go.chan."):
			boxed["chan "] = true
		}
	}
}

func kindsFromBoxed(boxed map[string]bool) map[int64]bool {
	live := map[int64]bool{
		0:             true, // Invalid
		kindBool:      true,
		kindString:    true,
		kindPtr:       true, // *T boxing / catch-all
		kindSlice:     true,
		kindArray:     true,
		kindStruct:    true,
		kindInterface: true,
	}
	hasFloat := boxed["float32"] || boxed["float64"] ||
		boxed["*float32"] || boxed["*float64"]
	hasComplex := boxed["complex64"] || boxed["complex128"] ||
		boxed["*complex64"] || boxed["*complex128"]
	hasMap := false
	hasChan := false
	hasFunc := false
	hasInt := false
	hasUint := false
	for k := range boxed {
		base := strings.TrimPrefix(k, "*")
		switch {
		case strings.HasPrefix(base, "map["):
			hasMap = true
		case strings.HasPrefix(base, "chan ") || strings.HasPrefix(base, "<-chan") || strings.HasPrefix(base, "chan<-"):
			hasChan = true
		case strings.HasPrefix(base, "func"):
			hasFunc = true
		case base == "int" || base == "int8" || base == "int16" || base == "int32" || base == "int64":
			hasInt = true
		case base == "uint" || base == "uint8" || base == "uint16" || base == "uint32" || base == "uint64" || base == "uintptr" || base == "byte" || base == "rune":
			hasUint = true
		}
	}
	// *int from iter.init counts as int present.
	if boxed["*int"] || boxed["int"] {
		hasInt = true
	}
	if hasFloat {
		live[kindFloat32] = true
		live[kindFloat64] = true
	}
	if hasComplex {
		live[kindComplex64] = true
		live[kindComplex128] = true
	}
	if hasMap {
		live[kindMap] = true
	}
	if hasChan {
		live[kindChan] = true
	}
	if hasFunc {
		live[kindFunc] = true
	}
	if hasInt {
		live[kindInt] = true
		live[kindInt8] = true
		live[kindInt16] = true
		live[kindInt32] = true
		live[kindInt64] = true
	}
	if hasUint {
		live[kindUint] = true
		live[kindUint8] = true
		live[kindUint16] = true
		live[kindUint32] = true
		live[kindUint64] = true
		live[kindUintptr] = true
	}
	live[kindUnsafePtr] = boxed["unsafe.Pointer"]
	return live
}

func pruneFmtRegion(r Region, boxed map[string]bool, typeOfVar map[string]string, liveKinds map[int64]bool) {
	if r == nil {
		return
	}
	switch r := r.(type) {
	case *Seq:
		for _, s := range r.Stmts {
			pruneFmtStmt(s, boxed, typeOfVar, liveKinds)
		}
	case *Block:
		pruneFmtRegion(r.Body, boxed, typeOfVar, liveKinds)
	case *Loop:
		pruneFmtRegion(r.Body, boxed, typeOfVar, liveKinds)
	case *If:
		cleared := false
		if key, ok := condTypeKey(r.Cond, typeOfVar); ok && !boxed[key] {
			r.Then = &Seq{}
			cleared = true
		} else if liveKinds != nil {
			// Only blank arms whose tags are real reflect.Kind values
			// (1..kindUnsafePtr). Verb runes ('s'=115, 'q'=113, …) share the
			// same i32.eq / i32.or shape inside printValue; treating them as
			// kinds blanks the byte-array %s/%q/%x/%X path (TestSprintf).
			if kind, ok := condKindEq(r.Cond); ok && isReflectKind(kind) && !liveKinds[kind] {
				r.Then = &Seq{}
				cleared = true
			} else if allOrKindsDead(r.Cond, liveKinds) {
				r.Then = &Seq{}
				cleared = true
			}
		}
		if !cleared {
			pruneFmtRegion(r.Then, boxed, typeOfVar, liveKinds)
		}
		pruneFmtRegion(r.Else, boxed, typeOfVar, liveKinds)
	case *Switch:
		for i := range r.Cases {
			pruneFmtRegion(r.Cases[i].Body, boxed, typeOfVar, liveKinds)
		}
		pruneFmtRegion(r.Default, boxed, typeOfVar, liveKinds)
	case *Try:
		pruneFmtRegion(r.Body, boxed, typeOfVar, liveKinds)
		pruneFmtRegion(r.Handler, boxed, typeOfVar, liveKinds)
	case *Dispatch:
		for _, arm := range r.Arms {
			pruneFmtRegion(arm, boxed, typeOfVar, liveKinds)
		}
	}
}

func pruneFmtStmt(s Stmt, boxed map[string]bool, typeOfVar map[string]string, liveKinds map[int64]bool) {
	switch s := s.(type) {
	case *Assign:
		noteTypeEqAssign(s, typeOfVar)
		if r, ok := s.Rhs.(Region); ok {
			pruneFmtRegion(r, boxed, typeOfVar, liveKinds)
		}
	case Region:
		pruneFmtRegion(s, boxed, typeOfVar, liveKinds)
	}
}

func regionStmts(r Region) []Stmt {
	if r == nil {
		return nil
	}
	if seq, ok := r.(*Seq); ok {
		return seq.Stmts
	}
	return []Stmt{r}
}

func noteTypeEqAssign(s *Assign, typeOfVar map[string]string) {
	if len(s.Lhs) != 1 {
		return
	}
	dst := destName(s.Lhs[0])
	if dst == "" {
		return
	}
	if key, ok := typeKeyFromExpr(s.Rhs); ok {
		typeOfVar[dst] = key
		return
	}
	if n, ok := s.Rhs.(*Name); ok {
		src := nameKey(n)
		if key, ok := typeOfVar[src]; ok {
			typeOfVar[dst] = key
		}
	}
}

func destName(d Dest) string {
	if d.Var != nil {
		return d.Var.Name
	}
	if d.Temp != nil {
		return d.Temp.Name
	}
	return d.Name
}

func nameKey(n *Name) string {
	if n.Var != nil {
		return n.Var.Name
	}
	if n.Temp != nil {
		return n.Temp.Name
	}
	return n.Raw
}

func condTypeKey(cond Expr, typeOfVar map[string]string) (string, bool) {
	if n, ok := cond.(*Name); ok {
		if key, ok := typeOfVar[nameKey(n)]; ok {
			return key, true
		}
	}
	if key, ok := typeKeyFromExpr(cond); ok {
		return key, true
	}
	return "", false
}

func typeKeyFromExpr(e Expr) (string, bool) {
	in, ok := e.(*Instr)
	if !ok {
		return "", false
	}
	if in.Op == OpRefEq {
		for _, a := range in.Args {
			if k, ok := typeGlobalKey(a); ok {
				return k, true
			}
		}
	}
	if in.Op == OpI32Eqz || in.Op == OpI32Eq || in.Op == OpI32Ne {
		for _, a := range in.Args {
			if k, ok := typeKeyFromExpr(a); ok {
				return k, true
			}
		}
	}
	return "", false
}

func typeGlobalKey(e Expr) (string, bool) {
	switch e := e.(type) {
	case *Instr:
		if e.Op == OpGlobalGet && strings.HasPrefix(e.Global, "$go.type.") {
			return strings.TrimPrefix(e.Global, "$go.type."), true
		}
		for _, a := range e.Args {
			if k, ok := typeGlobalKey(a); ok {
				return k, true
			}
		}
	}
	return "", false
}

// condKindEq matches `i32.eq tag, N` or `i32.or (i32.eq tag, A) (i32.eq tag, B)…`
// used by printValue's kind cascade. Returns a single kind when Cond is a
// plain equality; for or-chains returns the first kind (caller checks each
// via recursive prune on nested ifs — or-chains are one if for int family).
func condKindEq(cond Expr) (int64, bool) {
	in, ok := cond.(*Instr)
	if !ok {
		return 0, false
	}
	if in.Op == OpI32Eq && len(in.Args) == 2 {
		if c, ok := constI32(in.Args[1]); ok {
			return c, true
		}
		if c, ok := constI32(in.Args[0]); ok {
			return c, true
		}
	}
	// i32.or of several i32.eq tag, K — if ANY kind in the or is live we
	// must keep the arm. Return ok=false so we don't clear; a dedicated
	// helper clears only when ALL or'd kinds are dead.
	if in.Op == OpI32Or {
		kinds := collectOrKinds(in)
		if len(kinds) == 0 {
			return 0, false
		}
		// Signal "or-chain" by returning a sentinel; handled in prune via
		// allOrKindsDead.
		return -1, false
	}
	return 0, false
}

func allOrKindsDead(cond Expr, liveKinds map[int64]bool) bool {
	in, ok := cond.(*Instr)
	if !ok || in.Op != OpI32Or {
		return false
	}
	kinds := collectOrKinds(in)
	if len(kinds) == 0 {
		return false
	}
	// Verb/int switches use the same i32.or(eq…) shape. If any tag is
	// outside the reflect.Kind range, this is not a kind cascade — keep it.
	for _, k := range kinds {
		if !isReflectKind(k) {
			return false
		}
	}
	for _, k := range kinds {
		if liveKinds[k] {
			return false
		}
	}
	return true
}

// isReflectKind reports whether n is a reflect.Kind discriminant (Invalid=0
// through UnsafePointer=26). Fmt verb runes and other int switches use larger
// constants and must not be pruned as dead kinds.
func isReflectKind(n int64) bool {
	return n >= 0 && n <= kindUnsafePtr
}

func collectOrKinds(in *Instr) []int64 {
	var out []int64
	var walk func(Expr)
	walk = func(e Expr) {
		in, ok := e.(*Instr)
		if !ok {
			return
		}
		if in.Op == OpI32Or {
			for _, a := range in.Args {
				walk(a)
			}
			return
		}
		if in.Op == OpI32Eq && len(in.Args) == 2 {
			if c, ok := constI32(in.Args[1]); ok {
				out = append(out, c)
				return
			}
			if c, ok := constI32(in.Args[0]); ok {
				out = append(out, c)
			}
		}
	}
	walk(in)
	return out
}

func constI32(e Expr) (int64, bool) {
	switch e := e.(type) {
	case *Const:
		if e.Op == OpI32Const {
			return e.I64, true
		}
	}
	return 0, false
}
