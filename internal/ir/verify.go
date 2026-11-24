package ir

import (
	"fmt"
	"strings"
)

// Verify checks that m is well-formed: types, region nesting, branch
// targets, temp scoping, and (when m.Low) absence of high-level ops.
func Verify(m *Module) error {
	v := &verifier{m: m}
	v.module()
	if len(v.errs) == 0 {
		return nil
	}
	return v.errs[0]
}

// VerifyAll returns every diagnostic, not just the first.
func VerifyAll(m *Module) []*Error {
	v := &verifier{m: m}
	v.module()
	return v.errs
}

type verifier struct {
	m    *Module
	errs []*Error
	fn   *Func

	// label stack: ancestor block/loop/try/dispatch names
	labels []labelInfo

	// temps in current straight-line seq
	temps map[string]*Temp
	vars  map[string]*Var

	hasDispatch bool
}

type labelInfo struct {
	name    string
	kind    labelKind
	results []Type
}

type labelKind int

const (
	labelBlock labelKind = iota
	labelLoop
	labelTry
	labelDispatch
)

func (v *verifier) errorf(pos Pos, format string, args ...any) {
	v.errs = append(v.errs, &Error{Pos: pos, Msg: fmt.Sprintf(format, args...)})
}

func (v *verifier) module() {
	names := map[string]bool{}
	for _, t := range v.m.Types {
		if names[t.Name] {
			v.errorf(t.Pos, "duplicate type %s", t.Name)
		}
		names[t.Name] = true
		v.checkTypeDef(t)
	}
	gnames := map[string]bool{}
	for _, g := range v.m.Globals {
		if gnames[g.Name] {
			v.errorf(g.Pos, "duplicate global %s", g.Name)
		}
		gnames[g.Name] = true
		if g.Typ == nil {
			v.errorf(g.Pos, "global %s has no type", g.Name)
		} else if !IsValueType(g.Typ) && !isNamedRef(g.Typ) {
			// High types OK before lowering
			if _, ok := g.Typ.(*High); !ok {
				v.errorf(g.Pos, "global %s has invalid value type %s", g.Name, g.Typ)
			}
		}
	}
	fnames := map[string]bool{}
	for _, f := range v.m.Funcs {
		if fnames[f.Name] {
			v.errorf(f.Pos, "duplicate func %s", f.Name)
		}
		fnames[f.Name] = true
		v.func_(f)
	}
}

func isNamedRef(t Type) bool {
	_, ok := t.(*Ref)
	return ok
}

func (v *verifier) checkTypeDef(t *TypeDef) {
	switch t.Kind {
	case TypeStruct:
		for _, f := range t.Fields {
			if f.Type == nil {
				v.errorf(t.Pos, "field %s of %s has no type", f.Name, t.Name)
			}
			if IsPacked(f.Type) {
				v.errorf(t.Pos, "packed type %s not allowed as struct field (use i32)", f.Type)
			}
		}
	case TypeArray:
		if t.Elem == nil {
			v.errorf(t.Pos, "array %s has no element type", t.Name)
		}
	case TypeFunc:
		for _, p := range t.Params {
			if IsPacked(p) {
				v.errorf(t.Pos, "packed type not allowed as func param")
			}
		}
		for _, r := range t.Results {
			if IsPacked(r) {
				v.errorf(t.Pos, "packed type not allowed as func result")
			}
		}
	}
}

func (v *verifier) func_(f *Func) {
	v.fn = f
	v.labels = nil
	v.hasDispatch = false
	v.vars = map[string]*Var{}
	v.temps = map[string]*Temp{}
	for _, p := range f.Params {
		if p.Typ == nil {
			v.errorf(p.Pos, "param %s has no type", p.Name)
		}
		if IsPacked(p.Typ) {
			v.errorf(p.Pos, "param %s has packed type %s", p.Name, p.Typ)
		}
		v.vars[p.Name] = p
	}
	for _, r := range f.Results {
		if IsPacked(r) {
			v.errorf(f.Pos, "result has packed type %s", r)
		}
	}
	v.region(f.Body)
	f.HasDispatch = v.hasDispatch
}

func (v *verifier) pushLabel(name string, kind labelKind, results []Type) {
	v.labels = append(v.labels, labelInfo{name: name, kind: kind, results: results})
}

func (v *verifier) popLabel() {
	v.labels = v.labels[:len(v.labels)-1]
}

func (v *verifier) findLabel(name string) (labelInfo, int, bool) {
	for i := len(v.labels) - 1; i >= 0; i-- {
		if v.labels[i].name == name {
			return v.labels[i], len(v.labels) - 1 - i, true
		}
	}
	return labelInfo{}, -1, false
}

func (v *verifier) region(r Region) {
	if r == nil {
		return
	}
	switch r := r.(type) {
	case *Seq:
		saved := v.temps
		v.temps = cloneTempMap(saved)
		for _, s := range r.Stmts {
			v.stmt(s)
		}
		v.temps = saved
	case *Block:
		v.pushLabel(r.Name, labelBlock, r.Results)
		saved := v.temps
		v.temps = map[string]*Temp{}
		v.region(r.Body)
		v.temps = saved
		v.popLabel()
	case *Loop:
		v.pushLabel(r.Name, labelLoop, nil)
		saved := v.temps
		v.temps = map[string]*Temp{}
		v.region(r.Body)
		v.temps = saved
		v.popLabel()
	case *If:
		ct := v.exprType(r.Cond)
		if ct != nil && !TypesEqual(ct, TypeI32) {
			if _, ok := ct.(*High); !ok {
				v.errorf(r.Cond.GetPos(), "if condition must be i32, got %s", ct)
			}
		}
		// Wasm if creates a label; count it so br depths match codegen.
		v.pushLabel("", labelBlock, nil)
		// Outer temps are readable in both arms (they lower to function locals).
		// Temps defined inside an arm do not escape: each arm gets a fresh clone.
		saved := v.temps
		v.temps = cloneTempMap(saved)
		v.region(r.Then)
		v.temps = cloneTempMap(saved)
		if r.Else != nil {
			v.region(r.Else)
		}
		v.temps = saved
		v.popLabel()
	case *Switch:
		_ = v.exprType(r.Tag)
		saved := v.temps
		for _, c := range r.Cases {
			v.temps = map[string]*Temp{}
			v.region(c.Body)
		}
		if r.Default != nil {
			v.temps = map[string]*Temp{}
			v.region(r.Default)
		}
		v.temps = saved
		r.Dense = isDenseSwitch(r)
	case *Try:
		if r.Name != "" {
			v.pushLabel(r.Name, labelTry, nil)
		}
		saved := v.temps
		v.temps = map[string]*Temp{}
		v.region(r.Body)
		hvars := cloneVarMap(v.vars)
		for _, p := range r.Params {
			hvars[p.Name] = p
		}
		oldVars := v.vars
		v.vars = hvars
		v.temps = map[string]*Temp{}
		v.region(r.Handler)
		v.vars = oldVars
		v.temps = saved
		if r.Name != "" {
			v.popLabel()
		}
	case *Dispatch:
		v.hasDispatch = true
		v.pushLabel(r.Name, labelDispatch, nil)
		if r.State != nil {
			v.vars[r.State.Name] = r.State
		}
		saved := v.temps
		for _, a := range r.Arms {
			v.temps = map[string]*Temp{}
			if a != nil {
				v.region(a)
			}
		}
		v.temps = saved
		v.popLabel()
	}
}

func isDenseSwitch(s *Switch) bool {
	if len(s.Cases) == 0 {
		return false
	}
	min, max := s.Cases[0].Value, s.Cases[0].Value
	seen := map[int64]bool{}
	for _, c := range s.Cases {
		if seen[c.Value] {
			return false
		}
		seen[c.Value] = true
		if c.Value < min {
			min = c.Value
		}
		if c.Value > max {
			max = c.Value
		}
	}
	width := max - min + 1
	return width <= 4*int64(len(s.Cases))
}

func (v *verifier) stmt(s Stmt) {
	switch s := s.(type) {
	case Region:
		v.region(s)
	case *VarDef:
		if _, ok := v.vars[s.Var.Name]; ok {
			fn := "?"
			if v.fn != nil {
				fn = v.fn.Name
			}
			v.errorf(s.Pos, "duplicate var %s in %s", s.Var.Name, fn)
			return
		}
		if IsPacked(s.Var.Typ) {
			v.errorf(s.Pos, "var %s has packed type %s", s.Var.Name, s.Var.Typ)
		}
		v.vars[s.Var.Name] = s.Var
	case *Assign:
		rts := v.exprTypes(s.Rhs)
		if len(s.Lhs) == 0 {
			// side-effecting expr; OK
			return
		}
		if len(rts) > 0 && len(rts) != len(s.Lhs) {
			fn := ""
			if v.fn != nil {
				fn = v.fn.Name
			}
			v.errorf(s.Pos, "assignment has %d destinations but rhs produces %d values in %s", len(s.Lhs), len(rts), fn)
		}
		for i, d := range s.Lhs {
			var got Type
			if i < len(rts) {
				got = rts[i]
			}
			if d.Temp != nil {
				if _, ok := v.temps[d.Temp.Name]; ok {
					v.errorf(d.Pos, "temp %s redefined in region", d.Temp.Name)
				}
				// Prefer inferred rhs type; keep an existing typ when inference
				// returns nothing (e.g. incomplete call_ref metadata).
				if got != nil {
					d.Temp.Typ = got
				}
				v.temps[d.Temp.Name] = d.Temp
			} else if d.Var != nil {
				if want := d.Var.Typ; got != nil && want != nil && !typesCompatible(want, got) {
					fn := "?"
					if v.fn != nil {
						fn = v.fn.Name
					}
					v.errorf(d.Pos, "cannot assign %s to var %s of type %s in %s", got, d.Var.Name, want, fn)
				}
			} else if strings.HasPrefix(d.Name, "%") {
				t := &Temp{Name: d.Name, Typ: got, Def: s, Pos: d.Pos}
				s.Lhs[i].Temp = t
				v.temps[t.Name] = t
			} else {
				vr, ok := v.vars[d.Name]
				if !ok {
					v.errorf(d.Pos, "undefined var %s", d.Name)
					continue
				}
				s.Lhs[i].Var = vr
				if got != nil && vr.Typ != nil && !typesCompatible(vr.Typ, got) {
					v.errorf(d.Pos, "cannot assign %s to var %s of type %s", got, vr.Name, vr.Typ)
				}
			}
		}
		if v.m.Low {
			v.checkLowExpr(s.Rhs)
		}
	case *ExprStmt:
		_ = v.exprTypes(s.X)
		if v.m.Low {
			v.checkLowExpr(s.X)
		}
	case *Return:
		if len(s.Results) != len(v.fn.Results) {
			fn := "?"
			if v.fn != nil {
				fn = v.fn.Name
			}
			v.errorf(s.Pos, "return has %d values, function wants %d in %s", len(s.Results), len(v.fn.Results), fn)
		}
		for i, e := range s.Results {
			got := v.exprType(e)
			if i < len(v.fn.Results) && got != nil && v.fn.Results[i] != nil &&
				!typesCompatible(v.fn.Results[i], got) {
				fn := "?"
				if v.fn != nil {
					fn = v.fn.Name
				}
				v.errorf(e.GetPos(), "return value %d: have %s, want %s in %s", i, got, v.fn.Results[i], fn)
			}
		}
	case *Br:
		info, depth, ok := v.findLabel(s.Label)
		if !ok {
			v.errorf(s.Pos, "br target %s not in scope", s.Label)
		} else {
			s.Depth = depth
			if len(s.Args) != len(info.results) {
				// loops have no results; blocks may
				if info.kind == labelBlock && len(info.results) > 0 {
					v.errorf(s.Pos, "br %s: got %d args, block wants %d", s.Label, len(s.Args), len(info.results))
				}
			}
		}
		if s.Cond != nil {
			ct := v.exprType(s.Cond)
			if ct != nil && !TypesEqual(ct, TypeI32) {
				if _, ok := ct.(*High); !ok {
					v.errorf(s.Cond.GetPos(), "br_if condition must be i32, got %s", ct)
				}
			}
		}
		for _, a := range s.Args {
			_ = v.exprType(a)
		}
	case *Unreachable:
		// ok
	default:
		v.errorf(s.GetPos(), "unknown statement %T", s)
	}
}

func (v *verifier) checkLowExpr(e Expr) {
	switch e := e.(type) {
	case *Instr:
		if e.Op.IsHigh() {
			v.errorf(e.Pos, "high-level op %s remains in low module", e.Op)
		}
		for _, a := range e.Args {
			v.checkLowExpr(a)
		}
	case *Const:
		if e.Op == OpStringLit {
			v.errorf(e.Pos, "high-level string literal remains in low module")
		}
	}
}

func typesCompatible(want, got Type) bool {
	if TypesEqual(want, got) {
		return true
	}
	// High types are opaque before lowering.
	if _, ok := want.(*High); ok {
		return true
	}
	if _, ok := got.(*High); ok {
		return true
	}
	// ref T assignable from refnull T is not; refnull from ref is OK in Wasm
	// with implicit... actually Wasm is strict. Keep strict equality for low types.
	wr, wok := want.(*Ref)
	gr, gok := got.(*Ref)
	if wok && gok && wr.Named == gr.Named && wr.Heap == gr.Heap && (wr.Func == nil) == (gr.Func == nil) {
		// allow ref → refnull? No. allow refnull destination from ref source: yes in practice via local.set
		if wr.Null && !gr.Null {
			return true
		}
	}
	return false
}

func (v *verifier) exprType(e Expr) Type {
	ts := v.exprTypes(e)
	if len(ts) == 0 {
		return nil
	}
	return ts[0]
}

func (v *verifier) exprTypes(e Expr) []Type {
	if e == nil {
		return nil
	}
	switch e := e.(type) {
	case *Const:
		switch e.Op {
		case OpI32Const:
			return []Type{TypeI32}
		case OpI64Const:
			return []Type{TypeI64}
		case OpF32Const:
			return []Type{TypeF32}
		case OpF64Const:
			return []Type{TypeF64}
		case OpStringLit:
			return []Type{&Ref{Named: "$go.string"}}
		}
	case *Name:
		if e.Temp != nil {
			if _, ok := v.temps[e.Temp.Name]; !ok {
				fn := "?"
				if v.fn != nil {
					fn = v.fn.Name
				}
				v.errorf(e.Pos, "temp %s used outside its defining region in %s", e.Temp.Name, fn)
			}
			return []Type{e.Temp.Typ}
		}
		if e.Var != nil {
			return []Type{e.Var.Typ}
		}
		if strings.HasPrefix(e.Raw, "%") {
			t, ok := v.temps[e.Raw]
			if !ok {
				v.errorf(e.Pos, "undefined temp %s", e.Raw)
				return nil
			}
			return []Type{t.Typ}
		}
		vr, ok := v.vars[e.Raw]
		if !ok {
			v.errorf(e.Pos, "undefined name %s", e.Raw)
			return nil
		}
		return []Type{vr.Typ}
	case *Instr:
		return v.instrTypes(e)
	}
	return nil
}

func (v *verifier) instrTypes(e *Instr) []Type {
	for _, a := range e.Args {
		_ = v.exprType(a)
	}
	switch e.Op {
	case OpI32Const, OpI32Add, OpI32Sub, OpI32Mul, OpI32DivS, OpI32DivU, OpI32RemS, OpI32RemU,
		OpI32And, OpI32Or, OpI32Xor, OpI32Shl, OpI32ShrS, OpI32ShrU, OpI32Rotl, OpI32Rotr,
		OpI32Clz, OpI32Ctz, OpI32Popcnt, OpI32Eqz,
		OpI32Eq, OpI32Ne, OpI32LtS, OpI32LtU, OpI32GtS, OpI32GtU, OpI32LeS, OpI32LeU, OpI32GeS, OpI32GeU,
		OpI32WrapI64, OpI32Extend8S, OpI32Extend16S,
		OpI32TruncF32S, OpI32TruncF32U, OpI32TruncF64S, OpI32TruncF64U,
		OpI32TruncSatF32S, OpI32TruncSatF32U, OpI32TruncSatF64S, OpI32TruncSatF64U,
		OpI32ReinterpretF32,
		OpF32Eq, OpF32Ne, OpF32Lt, OpF32Gt, OpF32Le, OpF32Ge,
		OpF64Eq, OpF64Ne, OpF64Lt, OpF64Gt, OpF64Le, OpF64Ge,
		OpI64Eq, OpI64Ne, OpI64LtS, OpI64LtU, OpI64GtS, OpI64GtU, OpI64LeS, OpI64LeU, OpI64GeS, OpI64GeU, OpI64Eqz,
		OpRefIsNull, OpRefEq, OpRefTest, OpArrayLen, OpMemorySize, OpMemoryGrow:
		return []Type{TypeI32}
	case OpI64Const, OpI64Add, OpI64Sub, OpI64Mul, OpI64DivS, OpI64DivU, OpI64RemS, OpI64RemU,
		OpI64And, OpI64Or, OpI64Xor, OpI64Shl, OpI64ShrS, OpI64ShrU, OpI64Rotl, OpI64Rotr,
		OpI64Clz, OpI64Ctz, OpI64Popcnt,
		OpI64ExtendI32S, OpI64ExtendI32U, OpI64Extend8S, OpI64Extend16S, OpI64Extend32S,
		OpI64TruncF32S, OpI64TruncF32U, OpI64TruncF64S, OpI64TruncF64U,
		OpI64TruncSatF32S, OpI64TruncSatF32U, OpI64TruncSatF64S, OpI64TruncSatF64U,
		OpI64ReinterpretF64:
		return []Type{TypeI64}
	case OpF32Const, OpF32Add, OpF32Sub, OpF32Mul, OpF32Div, OpF32Min, OpF32Max,
		OpF32Abs, OpF32Neg, OpF32Sqrt, OpF32Ceil, OpF32Floor, OpF32Trunc, OpF32Nearest, OpF32Copysign,
		OpF32ConvertI32S, OpF32ConvertI32U, OpF32ConvertI64S, OpF32ConvertI64U, OpF32DemoteF64, OpF32ReinterpretI32:
		return []Type{TypeF32}
	case OpF64Const, OpF64Add, OpF64Sub, OpF64Mul, OpF64Div, OpF64Min, OpF64Max,
		OpF64Abs, OpF64Neg, OpF64Sqrt, OpF64Ceil, OpF64Floor, OpF64Trunc, OpF64Nearest, OpF64Copysign,
		OpF64ConvertI32S, OpF64ConvertI32U, OpF64ConvertI64S, OpF64ConvertI64U, OpF64PromoteF32, OpF64ReinterpretI64:
		return []Type{TypeF64}
	case OpLocalGet:
		if len(e.Args) == 1 {
			return v.exprTypes(e.Args[0])
		}
	case OpLocalTee:
		if len(e.Args) >= 1 {
			if n, ok := e.Args[0].(*Name); ok && n.Var != nil {
				return []Type{n.Var.Typ}
			}
		}
	case OpLocalSet, OpGlobalSet, OpDrop, OpStructSet, OpArraySet, OpArrayCopy, OpArrayFill,
		OpMemoryCopy, OpMemoryFill, OpThrow, OpThrowRef, OpPanic, OpDefer, OpGo,
		OpChanSend, OpChanClose, OpStore, OpSliceSet, OpMapSet, OpMapDelete:
		return nil
	case OpGlobalGet:
		if g := v.m.LookupGlobal(e.Global); g != nil {
			return []Type{g.Typ}
		}
	case OpRefNull:
		if e.Type != nil {
			if _, ok := e.Type.(*Extern); ok {
				return []Type{TypeExtern}
			}
			if r, ok := e.Type.(*Ref); ok {
				return []Type{&Ref{Null: true, Named: r.Named, Heap: r.Heap, Func: r.Func}}
			}
			// type immediate is heap name
			return []Type{&Ref{Null: true, Named: TypeString(e.Type)}}
		}
	case OpRefFunc:
		return []Type{&Ref{Heap: HeapFunc}}
	case OpRefCast, OpRefAsNonNull:
		if e.Type != nil {
			return []Type{e.Type}
		}
	case OpStructNew, OpStructNewDefault:
		if e.Type != nil {
			return []Type{&Ref{Named: typeNameOf(e.Type)}}
		}
	case OpStructGet, OpStructGetS, OpStructGetU:
		// field type unknown without looking up; use i32 as safe default for packed gets
		if e.Type != nil {
			if td := v.m.LookupType(typeNameOf(e.Type)); td != nil {
				for _, f := range td.Fields {
					if f.Name == e.Field {
						if IsPacked(f.Type) {
							return []Type{TypeI32}
						}
						return []Type{f.Type}
					}
				}
			}
		}
		return []Type{TypeI32}
	case OpArrayNew, OpArrayNewDefault, OpArrayNewFixed, OpArrayNewData:
		if e.Type != nil {
			return []Type{&Ref{Named: typeNameOf(e.Type)}}
		}
	case OpArrayGet:
		if e.Type != nil {
			if td := v.m.LookupType(typeNameOf(e.Type)); td != nil && td.Elem != nil {
				return []Type{td.Elem}
			}
		}
	case OpArrayGetS, OpArrayGetU:
		return []Type{TypeI32}
	case OpCall:
		if fn := v.m.LookupFunc(e.Func); fn != nil {
			return append([]Type(nil), fn.Results...)
		}
		// external / not yet defined — unknown
		return nil
	case OpCallRef:
		// Type immediate is the funcref signature ($go.ifacefn… / $go.get…).
		if e.Type != nil {
			if td := v.m.LookupType(typeNameOf(e.Type)); td != nil && td.Kind == TypeFunc {
				return append([]Type(nil), td.Results...)
			}
			if r, ok := e.Type.(*Ref); ok && r.Named != "" {
				if td := v.m.LookupType(r.Named); td != nil && td.Kind == TypeFunc {
					return append([]Type(nil), td.Results...)
				}
			}
		}
		return nil
	case OpStringLit:
		return []Type{&Ref{Named: "$go.string"}}
	case OpStringLen, OpMapLen, OpLen, OpCap, OpTypeSwitch:
		return []Type{TypeI32}
	case OpStringGet:
		return []Type{TypeI32}
	case OpMakeSlice, OpSliceAppend, OpSliceSlice, OpStringSlice,
		OpMapMake, OpMapIter, OpChanMake, OpIfaceMake, OpRecover, OpAddr, OpDeref,
		OpConv, OpLiteral:
		if e.Type != nil {
			return []Type{e.Type}
		}
		return []Type{&High{Name: e.Op.String()}}
	case OpSliceGet, OpMapGet, OpChanRecv, OpIfaceAssert, OpIfaceCall, OpSelect, OpMapNext:
		return nil // multi-value / unknown without signatures
	case OpReal, OpImag:
		return []Type{TypeF64}
	case OpComplex:
		return []Type{&High{Name: "complex"}}
	}
	return nil
}

func typeNameOf(t Type) string {
	switch t := t.(type) {
	case *TypeDef:
		return t.Name
	case *Ref:
		if t.Named != "" {
			return t.Named
		}
		return t.String()
	case *High:
		return t.String()
	default:
		return TypeString(t)
	}
}
