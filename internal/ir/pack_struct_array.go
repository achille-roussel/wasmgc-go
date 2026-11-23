package ir

import (
	"encoding/binary"
	"fmt"
	"math"
)

// Minimum element count before rewriting an unrolled const struct array into
// array.new_data + a fill loop. Below this, array.new_fixed is smaller.
const packStructArrayMin = 8

// PackConstStructArrays rewrites large constant arrays/slices of numeric
// structs (e.g. strconv pmHiLo{hi,lo uint64}) from unrolled
// struct.new + array.new_fixed into a passive data segment plus a tight
// fill loop. Call after RuntimeOps so literals are already expanded.
//
// When the table is later found write-only, PruneDeadInits removes the
// packed form too; this pass matters for programs that actually read the
// tables (fmt of floats).
func PackConstStructArrays(m *Module) {
	if m == nil {
		return
	}
	for _, f := range m.Funcs {
		if f.Body == nil {
			continue
		}
		f.Body = packRegion(m, f, f.Body)
	}
}

func packRegion(m *Module, f *Func, r Region) Region {
	if r == nil {
		return nil
	}
	switch r := r.(type) {
	case *Seq:
		r.Stmts = packSeq(m, f, r.Stmts)
		return r
	case *Block:
		r.Body = packRegion(m, f, r.Body)
		return r
	case *Loop:
		r.Body = packRegion(m, f, r.Body)
		return r
	case *If:
		r.Then = packRegion(m, f, r.Then)
		r.Else = packRegion(m, f, r.Else)
		return r
	case *Switch:
		for i := range r.Cases {
			r.Cases[i].Body = packRegion(m, f, r.Cases[i].Body)
		}
		r.Default = packRegion(m, f, r.Default)
		return r
	case *Try:
		r.Body = packRegion(m, f, r.Body)
		r.Handler = packRegion(m, f, r.Handler)
		return r
	case *Dispatch:
		for i := range r.Arms {
			r.Arms[i] = packRegion(m, f, r.Arms[i])
		}
		return r
	}
	return r
}

func packSeq(m *Module, f *Func, stmts []Stmt) []Stmt {
	// Index assigns: name → (index, struct.new instr) for pure numeric structs.
	type def struct {
		idx int
		in  *Instr
	}
	defs := map[string]def{}
	for i, s := range stmts {
		a, ok := s.(*Assign)
		if !ok || len(a.Lhs) != 1 {
			continue
		}
		in, ok := a.Rhs.(*Instr)
		if !ok || in.Op != OpStructNew {
			continue
		}
		if !numericConstStruct(in) {
			continue
		}
		defs[destKey(a.Lhs[0])] = def{idx: i, in: in}
	}

	// First pass: decide which array.new_fixed sites pack, and which
	// preceding struct.new assigns they consume.
	skip := make([]bool, len(stmts))
	replace := map[int][]Stmt{} // array.new_fixed index → packed stmts
	for i, s := range stmts {
		a, ok := s.(*Assign)
		if !ok {
			continue
		}
		in, ok := a.Rhs.(*Instr)
		if !ok || in.Op != OpArrayNewFixed || len(in.Args) < packStructArrayMin {
			continue
		}
		arrTy, ok := in.Type.(*Ref)
		if !ok || !hasPrefix(arrTy.Named, "$go.array.go.struct.") {
			continue
		}
		var elems []*Instr
		var elemIdxs []int
		okPack := true
		for _, arg := range in.Args {
			n, isName := arg.(*Name)
			if !isName {
				okPack = false
				break
			}
			d, found := defs[nameKey(n)]
			if !found || d.idx >= i || skip[d.idx] {
				okPack = false
				break
			}
			elems = append(elems, d.in)
			elemIdxs = append(elemIdxs, d.idx)
		}
		if !okPack || len(elems) == 0 {
			continue
		}
		stTy, ok := elems[0].Type.(*Ref)
		if !ok {
			continue
		}
		td := m.LookupType(stTy.Named)
		if td == nil || td.Kind != TypeStruct || len(td.Fields) == 0 {
			continue
		}
		nFields := len(elems[0].Args)
		for _, e := range elems[1:] {
			et, ok := e.Type.(*Ref)
			if !ok || et.Named != stTy.Named || len(e.Args) != nFields {
				okPack = false
				break
			}
		}
		if !okPack {
			continue
		}
		packed, err := buildPackedFill(m, f, arrTy, stTy, td, elems, a.Lhs)
		if err != nil {
			continue
		}
		for _, ei := range elemIdxs {
			skip[ei] = true
		}
		skip[i] = true
		replace[i] = packed
	}

	dropped := map[string]bool{}
	for i, s := range stmts {
		if !skip[i] {
			continue
		}
		if _, isReplace := replace[i]; isReplace {
			continue // the array.new_fixed site itself
		}
		// Packed-away struct.new assign.
		if a, ok := s.(*Assign); ok && len(a.Lhs) == 1 && a.Lhs[0].Var != nil {
			dropped[a.Lhs[0].Var.Name] = true
			dropLocal(f, a.Lhs[0].Var.Name)
		}
	}

	out := make([]Stmt, 0, len(stmts))
	for i, s := range stmts {
		if packed, ok := replace[i]; ok {
			out = append(out, packed...)
			continue
		}
		if skip[i] {
			continue
		}
		if vd, ok := s.(*VarDef); ok && vd.Var != nil && dropped[vd.Var.Name] {
			continue
		}
		out = append(out, s)
	}
	return out
}

func dropLocal(f *Func, name string) {
	out := f.Locals[:0]
	for _, v := range f.Locals {
		if v != nil && v.Name == name {
			continue
		}
		out = append(out, v)
	}
	f.Locals = out
}

func hasPrefix(s, p string) bool {
	return len(s) >= len(p) && s[:len(p)] == p
}

func uniqueDataName(m *Module) string {
	n := len(m.Data)
	for {
		name := fmt.Sprintf("$pack%d", n)
		if m.LookupData(name) == nil {
			return name
		}
		n++
	}
}

func numericConstStruct(in *Instr) bool {
	if in == nil || in.Op != OpStructNew || len(in.Args) == 0 {
		return false
	}
	for _, a := range in.Args {
		if !isNumericConst(a) {
			return false
		}
	}
	return true
}

func isNumericConst(e Expr) bool {
	switch e := e.(type) {
	case *Const:
		switch e.Op {
		case OpI32Const, OpI64Const, OpF32Const, OpF64Const:
			return true
		}
	case *Instr:
		// f64.neg f64.const X
		if (e.Op == OpF64Neg || e.Op == OpF32Neg) && len(e.Args) == 1 {
			return isNumericConst(e.Args[0])
		}
	}
	return false
}

func buildPackedFill(m *Module, f *Func, arrTy, stTy *Ref, td *TypeDef, elems []*Instr, lhs []Dest) ([]Stmt, error) {
	n := len(elems)
	nFields := len(elems[0].Args)
	// Pack as interleaved little-endian field bytes into one i8 data segment,
	// then decode with typed array.gets into struct.new in a loop.
	// Simpler denser approach: one flat i64/i32 array per homogeneous field
	// type. For pmHiLo both fields are i64 → one flat i64 array of 2N.
	fieldKinds := make([]Op, nFields)
	for fi := 0; fi < nFields; fi++ {
		c := unwrapConst(elems[0].Args[fi])
		if c == nil {
			return nil, fmt.Errorf("non-const field")
		}
		fieldKinds[fi] = c.Op
		ft := td.Fields[fi].Type
		if !fieldMatchesConst(ft, c.Op) {
			return nil, fmt.Errorf("field type mismatch")
		}
	}

	// Emit a single flat numeric array: for each element, append each field.
	elemByte := 0
	for _, op := range fieldKinds {
		elemByte += constByteSize(op)
	}
	buf := make([]byte, 0, n*elemByte)
	for _, e := range elems {
		for fi := 0; fi < nFields; fi++ {
			c := unwrapConst(e.Args[fi])
			buf = append(buf, packConstBytes(c)...)
		}
	}
	litName := uniqueDataName(m)
	m.Data = append(m.Data, &Data{Name: litName, Bytes: buf})

	// Choose backing array type from dominant width (prefer i64 if any i64/f64).
	flatName, flatGet := flatArrayType(fieldKinds)
	ensureArrayType(m, flatName)

	// Locals (not temps): values must survive the fill loop region.
	flatV := &Var{Name: freshLocal(f, "pack"), Typ: &Ref{Named: flatName}}
	arrV := &Var{Name: freshLocal(f, "parr"), Typ: arrTy}
	iVar := &Var{Name: freshLocal(f, "pi"), Typ: TypeI32}
	stV := &Var{Name: freshLocal(f, "pst"), Typ: stTy}
	f.Locals = append(f.Locals, flatV, arrV, iVar, stV)

	width := constByteSize(fieldKinds[0])
	for _, op := range fieldKinds[1:] {
		if constByteSize(op) != width {
			return nil, fmt.Errorf("mixed field widths")
		}
	}

	var stmts []Stmt
	stmts = append(stmts, &Assign{
		Lhs: []Dest{{Var: flatV, Name: flatV.Name}},
		Rhs: &Instr{Op: OpArrayNewData, Type: &Ref{Named: flatName}, Data: litName},
	})
	stmts = append(stmts, &Assign{
		Lhs: []Dest{{Var: arrV, Name: arrV.Name}},
		Rhs: &Instr{
			Op:   OpArrayNewDefault,
			Type: arrTy,
			Args: []Expr{&Const{Op: OpI32Const, I64: int64(n)}},
		},
	})
	stmts = append(stmts, &Assign{
		Lhs: []Dest{{Var: iVar, Name: iVar.Name}},
		Rhs: &Const{Op: OpI32Const, I64: 0},
	})

	// loop {
	//   br_if break, i >= n
	//   struct = struct.new (array.get flat, i*nFields+0), ...
	//   array.set arr, i, struct
	//   i = i+1
	//   br loop
	// }
	breakL := freshLabel(f, "pack.br")
	loopL := freshLabel(f, "pack.lp")

	cond := &Instr{Op: OpI32GeU, Args: []Expr{
		&Name{Var: iVar, Raw: iVar.Name},
		&Const{Op: OpI32Const, I64: int64(n)},
	}}
	var fieldArgs []Expr
	for fi := 0; fi < nFields; fi++ {
		// index = i * nFields + fi
		idx := &Instr{Op: OpI32Add, Args: []Expr{
			&Instr{Op: OpI32Mul, Args: []Expr{
				&Name{Var: iVar, Raw: iVar.Name},
				&Const{Op: OpI32Const, I64: int64(nFields)},
			}},
			&Const{Op: OpI32Const, I64: int64(fi)},
		}}
		get := &Instr{
			Op:   flatGet,
			Type: &Ref{Named: flatName},
			Args: []Expr{&Name{Var: flatV, Raw: flatV.Name}, idx},
		}
		fieldArgs = append(fieldArgs, coerceFlatGet(get, fieldKinds[fi], flatName))
	}
	stNew := &Instr{Op: OpStructNew, Type: stTy, Args: fieldArgs}
	fillBody := &Seq{Stmts: []Stmt{
		&Br{Label: breakL, Cond: cond},
		&Assign{Lhs: []Dest{{Var: stV, Name: stV.Name}}, Rhs: stNew},
		&ExprStmt{X: &Instr{
			Op:   OpArraySet,
			Type: arrTy,
			Args: []Expr{
				&Name{Var: arrV, Raw: arrV.Name},
				&Name{Var: iVar, Raw: iVar.Name},
				&Name{Var: stV, Raw: stV.Name},
			},
		}},
		&Assign{
			Lhs: []Dest{{Var: iVar, Name: iVar.Name}},
			Rhs: &Instr{Op: OpI32Add, Args: []Expr{
				&Name{Var: iVar, Raw: iVar.Name},
				&Const{Op: OpI32Const, I64: 1},
			}},
		},
		&Br{Label: loopL},
	}}
	loop := &Block{Name: breakL, Body: &Loop{Name: loopL, Body: fillBody}}
	stmts = append(stmts, loop)
	stmts = append(stmts, &Assign{
		Lhs: lhs,
		Rhs: &Name{Var: arrV, Raw: arrV.Name},
	})
	return stmts, nil
}

func unwrapConst(e Expr) *Const {
	switch e := e.(type) {
	case *Const:
		return e
	case *Instr:
		if (e.Op == OpF64Neg || e.Op == OpF32Neg) && len(e.Args) == 1 {
			c := unwrapConst(e.Args[0])
			if c == nil {
				return nil
			}
			neg := *c
			if e.Op == OpF64Neg {
				neg.F64 = -neg.F64
				neg.Op = OpF64Const
			} else {
				neg.F64 = -neg.F64
				neg.Op = OpF32Const
			}
			return &neg
		}
	}
	return nil
}

func fieldMatchesConst(ft Type, op Op) bool {
	nt, ok := ft.(*NumType)
	if !ok {
		return false
	}
	switch nt.Kind {
	case I32:
		return op == OpI32Const
	case I64:
		return op == OpI64Const
	case F32:
		return op == OpF32Const
	case F64:
		return op == OpF64Const
	}
	return false
}

func constByteSize(op Op) int {
	switch op {
	case OpI32Const, OpF32Const:
		return 4
	case OpI64Const, OpF64Const:
		return 8
	}
	return 0
}

func packConstBytes(c *Const) []byte {
	switch c.Op {
	case OpI32Const:
		b := make([]byte, 4)
		binary.LittleEndian.PutUint32(b, uint32(c.I64))
		return b
	case OpI64Const:
		b := make([]byte, 8)
		binary.LittleEndian.PutUint64(b, uint64(c.I64))
		return b
	case OpF32Const:
		b := make([]byte, 4)
		binary.LittleEndian.PutUint32(b, math.Float32bits(float32(c.F64)))
		return b
	case OpF64Const:
		b := make([]byte, 8)
		binary.LittleEndian.PutUint64(b, math.Float64bits(c.F64))
		return b
	}
	return nil
}

func flatArrayType(kinds []Op) (typeName string, getOp Op) {
	hasI64, hasF64, hasF32 := false, false, false
	for _, op := range kinds {
		switch op {
		case OpI64Const:
			hasI64 = true
		case OpF64Const:
			hasF64 = true
		case OpF32Const:
			hasF32 = true
		}
	}
	switch {
	case hasF64:
		return "$go.array.f64", OpArrayGet
	case hasI64:
		return "$go.array.i64", OpArrayGet
	case hasF32:
		return "$go.array.f32", OpArrayGet
	default:
		return "$go.array.i32", OpArrayGet
	}
}

func coerceFlatGet(get Expr, want Op, flatName string) Expr {
	// Flat i64 holding i64, etc. — no coerce when matching.
	if want == OpI64Const && flatName == "$go.array.i64" {
		return get
	}
	if want == OpI32Const && flatName == "$go.array.i32" {
		return get
	}
	if want == OpF64Const && flatName == "$go.array.f64" {
		return get
	}
	if want == OpF32Const && flatName == "$go.array.f32" {
		return get
	}
	if want == OpI32Const && flatName == "$go.array.i64" {
		return &Instr{Op: OpI32WrapI64, Args: []Expr{get}}
	}
	return get
}

func ensureArrayType(m *Module, name string) {
	if m.LookupType(name) != nil {
		return
	}
	var elem Type
	switch name {
	case "$go.array.i32":
		elem = TypeI32
	case "$go.array.i64":
		elem = TypeI64
	case "$go.array.f32":
		elem = TypeF32
	case "$go.array.f64":
		elem = TypeF64
	default:
		return
	}
	m.Types = append(m.Types, &TypeDef{Name: name, Kind: TypeArray, Elem: elem, ElemMut: true})
}

func freshLocal(f *Func, stem string) string {
	n := 0
	for {
		name := fmt.Sprintf("%s%d", stem, n)
		n++
		conflict := false
		for _, v := range f.Locals {
			if v != nil && v.Name == name {
				conflict = true
				break
			}
		}
		for _, v := range f.Params {
			if v != nil && v.Name == name {
				conflict = true
				break
			}
		}
		if !conflict {
			return name
		}
	}
}

func freshLabel(f *Func, stem string) string {
	// Unique enough within one pack site; multiple packs in one func get
	// distinct stems from call sites (pack.br / pack.lp) plus a counter.
	n := 0
	for _, v := range f.Locals {
		if v != nil {
			n++
		}
	}
	return fmt.Sprintf("%s%d", stem, n)
}
