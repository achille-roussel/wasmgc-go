package codegen

import (
	"fmt"
	"math"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
	"github.com/achille-roussel/wasmgc-go/internal/wasm"
)

type funcEmitter struct {
	g   *gen
	fn  *ir.Func
	enc *wasm.Encoder

	// local index by var/temp name
	localOf map[string]uint32
	locals  []wasm.Local // runs for the code section (non-param)

	// label stack for nested regions (innermost = depth 0)
	labels []string
}

func newFuncEmitter(g *gen, f *ir.Func) *funcEmitter {
	fe := &funcEmitter{
		g:       g,
		fn:      f,
		enc:     wasm.NewEncoder(),
		localOf: make(map[string]uint32),
	}
	// Params occupy the first local indices.
	for i, p := range f.Params {
		fe.localOf[p.Name] = uint32(i)
	}
	return fe
}

func (fe *funcEmitter) emit() error {
	// Collect var locals and temps → wasm locals after params.
	for _, v := range fe.fn.Locals {
		if err := fe.addLocal(v.Name, v.Typ); err != nil {
			return err
		}
	}
	collectTemps(fe.fn.Body, func(name string, typ ir.Type) {
		if _, ok := fe.localOf[name]; ok {
			return
		}
		_ = fe.addLocal(name, typ)
	})
	// Also collect VarDefs not listed in Locals (belt and suspenders).
	collectVarDefs(fe.fn.Body, func(v *ir.Var) {
		if _, ok := fe.localOf[v.Name]; ok {
			return
		}
		_ = fe.addLocal(v.Name, v.Typ)
	})

	if fe.fn.Body != nil {
		if err := fe.emitRegion(fe.fn.Body); err != nil {
			return err
		}
	}
	return nil
}

func (fe *funcEmitter) addLocal(name string, typ ir.Type) error {
	if typ == nil {
		typ = ir.TypeI32
	}
	vt, err := fe.g.valType(typ)
	if err != nil {
		// Temps may not have types filled if verifier skipped; default i32.
		vt = wasm.I32()
	}
	idx := uint32(len(fe.fn.Params)) + uint32(countLocals(fe.locals))
	fe.localOf[name] = idx
	// Coalesce into runs.
	if n := len(fe.locals); n > 0 {
		last := &fe.locals[n-1]
		if sameValType(last.Type, vt) {
			last.Count++
			return nil
		}
	}
	fe.locals = append(fe.locals, wasm.Local{Count: 1, Type: vt})
	return nil
}

func countLocals(ls []wasm.Local) int {
	n := 0
	for _, l := range ls {
		n += int(l.Count)
	}
	return n
}

func sameValType(a, b wasm.ValType) bool {
	return a.Equal(b)
}

func collectTemps(r ir.Region, fn func(name string, typ ir.Type)) {
	if r == nil {
		return
	}
	var walkStmt func(ir.Stmt)
	var walkRegion func(ir.Region)
	walkStmt = func(s ir.Stmt) {
		switch s := s.(type) {
		case ir.Region:
			walkRegion(s)
		case *ir.Assign:
			for _, d := range s.Lhs {
				if d.Temp != nil {
					fn(d.Temp.Name, d.Temp.Typ)
				} else if len(d.Name) > 0 && d.Name[0] == '%' {
					fn(d.Name, nil)
				}
			}
		}
	}
	walkRegion = func(r ir.Region) {
		switch r := r.(type) {
		case *ir.Seq:
			for _, s := range r.Stmts {
				walkStmt(s)
			}
		case *ir.Block:
			walkRegion(r.Body)
		case *ir.Loop:
			walkRegion(r.Body)
		case *ir.If:
			walkRegion(r.Then)
			if r.Else != nil {
				walkRegion(r.Else)
			}
		case *ir.Switch:
			for _, c := range r.Cases {
				walkRegion(c.Body)
			}
			if r.Default != nil {
				walkRegion(r.Default)
			}
		case *ir.Try:
			walkRegion(r.Body)
			walkRegion(r.Handler)
		case *ir.Dispatch:
			for _, a := range r.Arms {
				if a != nil {
					walkRegion(a)
				}
			}
		}
	}
	walkRegion(r)
}

func collectVarDefs(r ir.Region, fn func(*ir.Var)) {
	if r == nil {
		return
	}
	var walkStmt func(ir.Stmt)
	var walkRegion func(ir.Region)
	walkStmt = func(s ir.Stmt) {
		switch s := s.(type) {
		case ir.Region:
			walkRegion(s)
		case *ir.VarDef:
			fn(s.Var)
		}
	}
	walkRegion = func(r ir.Region) {
		switch r := r.(type) {
		case *ir.Seq:
			for _, s := range r.Stmts {
				walkStmt(s)
			}
		case *ir.Block:
			walkRegion(r.Body)
		case *ir.Loop:
			walkRegion(r.Body)
		case *ir.If:
			walkRegion(r.Then)
			if r.Else != nil {
				walkRegion(r.Else)
			}
		case *ir.Switch:
			for _, c := range r.Cases {
				walkRegion(c.Body)
			}
			if r.Default != nil {
				walkRegion(r.Default)
			}
		case *ir.Try:
			walkRegion(r.Body)
			walkRegion(r.Handler)
		case *ir.Dispatch:
			for _, a := range r.Arms {
				if a != nil {
					walkRegion(a)
				}
			}
		}
	}
	walkRegion(r)
}

func (fe *funcEmitter) emitRegion(r ir.Region) error {
	switch r := r.(type) {
	case *ir.Seq:
		for _, s := range r.Stmts {
			if err := fe.emitStmt(s); err != nil {
				return err
			}
		}
		return nil
	case *ir.Block:
		fe.labels = append(fe.labels, r.Name)
		bt, err := fe.blockType(r.Results)
		if err != nil {
			return err
		}
		fe.enc.Block(bt)
		if r.Body != nil {
			if err := fe.emitRegion(r.Body); err != nil {
				return err
			}
		}
		fe.enc.End()
		fe.labels = fe.labels[:len(fe.labels)-1]
		return nil
	case *ir.Loop:
		fe.labels = append(fe.labels, r.Name)
		fe.enc.Loop(wasm.BlockEmpty())
		if r.Body != nil {
			if err := fe.emitRegion(r.Body); err != nil {
				return err
			}
		}
		fe.enc.End()
		fe.labels = fe.labels[:len(fe.labels)-1]
		return nil
	case *ir.If:
		if err := fe.emitExpr(r.Cond); err != nil {
			return err
		}
		// Wasm if creates a label; push a placeholder so br depths match.
		fe.labels = append(fe.labels, "")
		fe.enc.If(wasm.BlockEmpty())
		if r.Then != nil {
			if err := fe.emitRegion(r.Then); err != nil {
				return err
			}
		}
		if r.Else != nil {
			fe.enc.Else()
			if err := fe.emitRegion(r.Else); err != nil {
				return err
			}
		}
		fe.enc.End()
		fe.labels = fe.labels[:len(fe.labels)-1]
		return nil
	case *ir.Switch:
		return fe.emitSwitch(r)
	case *ir.Dispatch:
		return fe.emitDispatch(r)
	case *ir.Try:
		return fmt.Errorf("try regions not yet emitted in M1/M2 codegen")
	default:
		return fmt.Errorf("unknown region %T", r)
	}
}

func (fe *funcEmitter) blockType(results []ir.Type) (wasm.BlockType, error) {
	if len(results) == 0 {
		return wasm.BlockEmpty(), nil
	}
	if len(results) == 1 {
		vt, err := fe.g.valType(results[0])
		if err != nil {
			return wasm.BlockType{}, err
		}
		return wasm.BlockVal(vt), nil
	}
	params := []wasm.ValType{}
	res, err := fe.g.valTypes(results)
	if err != nil {
		return wasm.BlockType{}, err
	}
	idx := fe.g.internFuncType(params, res)
	return wasm.BlockTypeIndex(idx), nil
}

func (fe *funcEmitter) emitSwitch(r *ir.Switch) error {
	if !r.Dense {
		return fmt.Errorf("non-dense switch; lowerer should emit if-chain")
	}
	// Encode as: block for each case + default, then br_table.
	// Layout (wasm3 / spec style):
	//   block $default
	//     block $caseN
	//       ...
	//         block $case0
	//           br_table 0 1 ... N (tag)
	//         end ;; case0 body; br default
	//       end
	//     end
	//   end ;; default body
	min := r.Cases[0].Value
	max := r.Cases[0].Value
	for _, c := range r.Cases {
		if c.Value < min {
			min = c.Value
		}
		if c.Value > max {
			max = c.Value
		}
	}
	width := int(max - min + 1)
	caseByVal := map[int64]*ir.Case{}
	for i := range r.Cases {
		c := &r.Cases[i]
		caseByVal[c.Value] = c
	}

	// Blocks: one per table slot + default outermost.
	// Depths for br_table targets: innermost block = 0.
	fe.enc.Block(wasm.BlockEmpty()) // default / exit
	for i := 0; i < width; i++ {
		fe.enc.Block(wasm.BlockEmpty())
	}
	if err := fe.emitExpr(r.Tag); err != nil {
		return err
	}
	if min != 0 {
		fe.enc.I32Const(int32(min))
		fe.enc.I32Sub()
	}
	labels := make([]uint32, width)
	for i := 0; i < width; i++ {
		labels[i] = uint32(i)
	}
	fe.enc.BrTable(labels, uint32(width)) // default = outermost of the inner set = width

	for i := 0; i < width; i++ {
		fe.enc.End() // close case block i
		val := min + int64(i)
		if c, ok := caseByVal[val]; ok && c.Body != nil {
			if err := fe.emitRegion(c.Body); err != nil {
				return err
			}
		}
		fe.enc.Br(uint32(width - i)) // branch to exit (default outer)
	}
	fe.enc.End() // default block
	if r.Default != nil {
		if err := fe.emitRegion(r.Default); err != nil {
			return err
		}
	}
	return nil
}

func (fe *funcEmitter) emitDispatch(r *ir.Dispatch) error {
	fe.labels = append(fe.labels, r.Name)
	fe.enc.Loop(wasm.BlockEmpty())
	// Arms as blocks so each can br out or set state and br back.
	n := len(r.Arms)
	for i := 0; i < n; i++ {
		fe.enc.Block(wasm.BlockEmpty())
	}
	if r.State == nil {
		return fmt.Errorf("dispatch without state var")
	}
	idx, ok := fe.localOf[r.State.Name]
	if !ok {
		return fmt.Errorf("dispatch state %s not a local", r.State.Name)
	}
	fe.enc.LocalGet(idx)
	labels := make([]uint32, n)
	for i := 0; i < n; i++ {
		labels[i] = uint32(i)
	}
	fe.enc.BrTable(labels, uint32(n-1))
	for i := 0; i < n; i++ {
		fe.enc.End()
		if r.Arms[i] != nil {
			if err := fe.emitRegion(r.Arms[i]); err != nil {
				return err
			}
		}
	}
	fe.enc.End() // loop
	fe.labels = fe.labels[:len(fe.labels)-1]
	return nil
}

func (fe *funcEmitter) emitStmt(s ir.Stmt) error {
	switch s := s.(type) {
	case ir.Region:
		return fe.emitRegion(s)
	case *ir.VarDef:
		return nil // local already allocated
	case *ir.Assign:
		if err := fe.emitExpr(s.Rhs); err != nil {
			return err
		}
		// Results on stack; set destinations from last to first.
		for i := len(s.Lhs) - 1; i >= 0; i-- {
			d := s.Lhs[i]
			name := d.String()
			idx, ok := fe.localOf[name]
			if !ok {
				return fmt.Errorf("assign to unknown local %s", name)
			}
			fe.enc.LocalSet(idx)
		}
		// Side-effecting assign with no lhs: values must be dropped if any.
		if len(s.Lhs) == 0 {
			// call with no results leaves nothing; stores leave nothing.
			// If rhs produced values we don't know count — verifier ensures 0.
		}
		return nil
	case *ir.ExprStmt:
		if err := fe.emitExpr(s.X); err != nil {
			return err
		}
		// Drop any residual values — typically none for calls/stores.
		return nil
	case *ir.Return:
		for _, e := range s.Results {
			if err := fe.emitExpr(e); err != nil {
				return err
			}
		}
		fe.enc.Return()
		return nil
	case *ir.Br:
		for _, a := range s.Args {
			if err := fe.emitExpr(a); err != nil {
				return err
			}
		}
		// Always resolve by label when present — Depth from Verify may omit
		// if-labels that Wasm still counts.
		depth := s.Depth
		if s.Label != "" {
			d, ok := fe.findLabel(s.Label)
			if !ok {
				return fmt.Errorf("br target %s not in scope", s.Label)
			}
			depth = d
		} else if depth < 0 {
			return fmt.Errorf("br with no label and unset depth")
		}
		if s.Cond != nil {
			if err := fe.emitExpr(s.Cond); err != nil {
				return err
			}
			fe.enc.BrIf(uint32(depth))
		} else {
			fe.enc.Br(uint32(depth))
		}
		return nil
	case *ir.Unreachable:
		fe.enc.Unreachable()
		return nil
	default:
		return fmt.Errorf("unknown stmt %T", s)
	}
}

func (fe *funcEmitter) findLabel(name string) (int, bool) {
	for i := len(fe.labels) - 1; i >= 0; i-- {
		if fe.labels[i] == name {
			return len(fe.labels) - 1 - i, true
		}
	}
	return 0, false
}

func (fe *funcEmitter) emitExpr(e ir.Expr) error {
	if e == nil {
		return nil
	}
	switch e := e.(type) {
	case *ir.Const:
		return fe.emitConst(e)
	case *ir.Name:
		return fe.emitName(e)
	case *ir.Instr:
		return fe.emitInstr(e)
	default:
		return fmt.Errorf("unknown expr %T", e)
	}
}

func (fe *funcEmitter) emitConst(c *ir.Const) error {
	switch c.Op {
	case ir.OpI32Const:
		fe.enc.I32Const(int32(c.I64))
	case ir.OpI64Const:
		fe.enc.I64Const(c.I64)
	case ir.OpF32Const:
		fe.enc.F32Const(float32(c.F64))
	case ir.OpF64Const:
		fe.enc.F64Const(c.F64)
	case ir.OpStringLit:
		return fmt.Errorf("string literal remains in low module")
	default:
		return fmt.Errorf("bad const op %s", c.Op)
	}
	return nil
}

func (fe *funcEmitter) emitName(n *ir.Name) error {
	name := n.String()
	if idx, ok := fe.localOf[name]; ok {
		fe.enc.LocalGet(idx)
		return nil
	}
	if idx, ok := fe.g.globals[name]; ok {
		fe.enc.GlobalGet(idx)
		return nil
	}
	return fmt.Errorf("undefined name %s", name)
}

func (fe *funcEmitter) emitInstr(in *ir.Instr) error {
	// Emit args first (stack order).
	for _, a := range in.Args {
		if err := fe.emitExpr(a); err != nil {
			return err
		}
	}
	switch in.Op {
	case ir.OpI32Const:
		fe.enc.I32Const(in.I32)
	case ir.OpI64Const:
		if len(in.Args) == 0 {
			fe.enc.I64Const(int64(in.I32))
		}
	case ir.OpF32Const:
		fe.enc.F32Const(math.Float32frombits(uint32(in.I32)))
	case ir.OpF64Const:
		fe.enc.F64Const(math.Float64frombits(uint64(in.I32)))
	case ir.OpRefNull:
		ht, err := fe.g.heapTypeOf(in.Type)
		if err != nil {
			return err
		}
		fe.enc.RefNull(ht)
	case ir.OpRefFunc:
		idx, ok := fe.g.funcs[in.Func]
		if !ok {
			return fmt.Errorf("ref.func unknown %s", in.Func)
		}
		fe.enc.RefFunc(idx)

	case ir.OpLocalGet:
		// Arg already emitted if it was a name... local.get usually takes a name immediate.
		// In our IR, local.get may wrap a Name in Args, already emitted — but that would
		// double-get. Handle: if Args empty, use nothing; if Args is Name, we already
		// emitted local.get via emitName. So OpLocalGet with Name arg is redundant.
		// Prefer: Args[0] is Name — we emitted get already; do nothing more.
		// If no args, error.
		if len(in.Args) == 0 {
			return fmt.Errorf("local.get without name")
		}
		// Already emitted as value; OpLocalGet as wrapper is a no-op after arg emit.
		// But wait: emitName already did local.get. So we're done.
	case ir.OpLocalSet:
		if len(in.Args) < 1 {
			return fmt.Errorf("local.set needs name")
		}
		// Args were emitted: for local.set x, v the IR might be local.set with args [x,v]
		// or Assign. Check arity: typically Assign handles sets.
		// If Instr form: last arg is value (emitted), first is name (also emitted as get — wrong).
		return fmt.Errorf("local.set as instr not supported; use assignment")
	case ir.OpLocalTee:
		return fmt.Errorf("local.tee as instr not supported yet")

	case ir.OpGlobalGet:
		idx, ok := fe.g.globals[in.Global]
		if !ok {
			return fmt.Errorf("unknown global %s", in.Global)
		}
		fe.enc.GlobalGet(idx)
	case ir.OpGlobalSet:
		idx, ok := fe.g.globals[in.Global]
		if !ok {
			return fmt.Errorf("unknown global %s", in.Global)
		}
		// Value is already on the stack from Args.
		fe.enc.GlobalSet(idx)

	case ir.OpCall:
		idx, ok := fe.g.funcs[in.Func]
		if !ok {
			return fmt.Errorf("call unknown function %s", in.Func)
		}
		fe.enc.Call(idx)
	case ir.OpCallRef:
		ty, err := fe.g.typeIndexOf(in.Type)
		if err != nil {
			return err
		}
		fe.enc.CallRef(ty)
	case ir.OpDrop:
		fe.enc.Drop()

	case ir.OpI32Add:
		fe.enc.I32Add()
	case ir.OpI32Sub:
		fe.enc.I32Sub()
	case ir.OpI32Mul:
		fe.enc.I32Mul()
	case ir.OpI32DivS:
		fe.enc.I32DivS()
	case ir.OpI32DivU:
		fe.enc.I32DivU()
	case ir.OpI32RemS:
		fe.enc.I32RemS()
	case ir.OpI32RemU:
		fe.enc.I32RemU()
	case ir.OpI32And:
		fe.enc.I32And()
	case ir.OpI32Or:
		fe.enc.I32Or()
	case ir.OpI32Xor:
		fe.enc.I32Xor()
	case ir.OpI32Shl:
		fe.enc.I32Shl()
	case ir.OpI32ShrS:
		fe.enc.I32ShrS()
	case ir.OpI32ShrU:
		fe.enc.I32ShrU()
	case ir.OpI32Eqz:
		fe.enc.I32Eqz()
	case ir.OpI32Eq:
		fe.enc.I32Eq()
	case ir.OpI32Ne:
		fe.enc.I32Ne()
	case ir.OpI32LtS:
		fe.enc.I32LtS()
	case ir.OpI32LtU:
		fe.enc.I32LtU()
	case ir.OpI32GtS:
		fe.enc.I32GtS()
	case ir.OpI32GtU:
		fe.enc.I32GtU()
	case ir.OpI32LeS:
		fe.enc.I32LeS()
	case ir.OpI32LeU:
		fe.enc.I32LeU()
	case ir.OpI32GeS:
		fe.enc.I32GeS()
	case ir.OpI32GeU:
		fe.enc.I32GeU()
	case ir.OpI32WrapI64:
		fe.enc.I32WrapI64()

	case ir.OpI64Add:
		fe.enc.I64Add()
	case ir.OpI64Sub:
		fe.enc.I64Sub()
	case ir.OpI64Mul:
		fe.enc.I64Mul()
	case ir.OpI64And:
		fe.enc.I64And()
	case ir.OpI64Or:
		fe.enc.I64Or()
	case ir.OpI64Xor:
		fe.enc.I64Xor()
	case ir.OpI64Shl:
		fe.enc.I64Shl()
	case ir.OpI64ShrS:
		fe.enc.I64ShrS()
	case ir.OpI64ShrU:
		fe.enc.I64ShrU()
	case ir.OpI64ExtendI32S:
		fe.enc.I64ExtendI32S()
	case ir.OpI64ExtendI32U:
		fe.enc.I64ExtendI32U()

	case ir.OpF32Add:
		fe.enc.Raw(0x92)
	case ir.OpF32Sub:
		fe.enc.Raw(0x93)
	case ir.OpF32Mul:
		fe.enc.Raw(0x94)
	case ir.OpF32Div:
		fe.enc.Raw(0x95)
	case ir.OpF64Add:
		fe.enc.Raw(0xA0)
	case ir.OpF64Sub:
		fe.enc.Raw(0xA1)
	case ir.OpF64Mul:
		fe.enc.Raw(0xA2)
	case ir.OpF64Div:
		fe.enc.Raw(0xA3)

	case ir.OpStructNew:
		idx, err := fe.g.typeIndexOf(in.Type)
		if err != nil {
			return err
		}
		fe.enc.StructNew(idx)
	case ir.OpStructNewDefault:
		idx, err := fe.g.typeIndexOf(in.Type)
		if err != nil {
			return err
		}
		fe.enc.StructNewDefault(idx)
	case ir.OpStructGet, ir.OpStructGetS, ir.OpStructGetU:
		tname := typeName(in.Type)
		fi, err := fe.g.fieldIndex(tname, in.Field)
		if err != nil {
			return err
		}
		ti, err := fe.g.typeIndexOf(in.Type)
		if err != nil {
			return err
		}
		switch in.Op {
		case ir.OpStructGet:
			fe.enc.StructGet(ti, fi)
		case ir.OpStructGetS:
			fe.enc.StructGetS(ti, fi)
		case ir.OpStructGetU:
			fe.enc.StructGetU(ti, fi)
		}
	case ir.OpStructSet:
		tname := typeName(in.Type)
		fi, err := fe.g.fieldIndex(tname, in.Field)
		if err != nil {
			return err
		}
		ti, err := fe.g.typeIndexOf(in.Type)
		if err != nil {
			return err
		}
		fe.enc.StructSet(ti, fi)

	case ir.OpArrayNew:
		idx, err := fe.g.typeIndexOf(in.Type)
		if err != nil {
			return err
		}
		fe.enc.ArrayNew(idx)
	case ir.OpArrayNewDefault:
		idx, err := fe.g.typeIndexOf(in.Type)
		if err != nil {
			return err
		}
		fe.enc.ArrayNewDefault(idx)
	case ir.OpArrayNewFixed:
		idx, err := fe.g.typeIndexOf(in.Type)
		if err != nil {
			return err
		}
		n := uint32(len(in.Args))
		if in.I32 > 0 {
			n = uint32(in.I32)
		}
		fe.enc.ArrayNewFixed(idx, n)
	case ir.OpArrayNewData:
		idx, err := fe.g.typeIndexOf(in.Type)
		if err != nil {
			return err
		}
		di, ok := fe.g.data[in.Data]
		if !ok {
			return fmt.Errorf("array.new_data unknown data %s", in.Data)
		}
		// Spec: stack operands are (offset, size) into the passive segment.
		// When IR omits them, use the whole segment.
		if len(in.Args) == 0 {
			seg := fe.g.m.LookupData(in.Data)
			fe.enc.I32Const(0)
			if seg != nil {
				fe.enc.I32Const(int32(len(seg.Bytes)))
			} else {
				fe.enc.I32Const(0)
			}
		}
		fe.enc.ArrayNewData(idx, di)
	case ir.OpArrayGet:
		idx, err := fe.g.typeIndexOf(in.Type)
		if err != nil {
			return err
		}
		fe.enc.ArrayGet(idx)
	case ir.OpArrayGetS:
		idx, err := fe.g.typeIndexOf(in.Type)
		if err != nil {
			return err
		}
		fe.enc.ArrayGetS(idx)
	case ir.OpArrayGetU:
		idx, err := fe.g.typeIndexOf(in.Type)
		if err != nil {
			return err
		}
		fe.enc.ArrayGetU(idx)
	case ir.OpArraySet:
		idx, err := fe.g.typeIndexOf(in.Type)
		if err != nil {
			return err
		}
		fe.enc.ArraySet(idx)
	case ir.OpArrayLen:
		fe.enc.ArrayLen()
	case ir.OpArrayCopy:
		dst, err := fe.g.typeIndexOf(in.Type)
		if err != nil {
			return err
		}
		src, err := fe.g.typeIndexOf(in.Type2)
		if err != nil {
			return err
		}
		fe.enc.ArrayCopy(dst, src)
	case ir.OpArrayFill:
		idx, err := fe.g.typeIndexOf(in.Type)
		if err != nil {
			return err
		}
		fe.enc.ArrayFill(idx)

	case ir.OpRefCast:
		ht, err := fe.g.heapTypeOf(in.Type)
		if err != nil {
			return err
		}
		fe.enc.RefCast(ht)
	case ir.OpRefTest:
		ht, err := fe.g.heapTypeOf(in.Type)
		if err != nil {
			return err
		}
		fe.enc.RefTest(ht)
	case ir.OpRefEq:
		fe.enc.RefEq()
	case ir.OpRefIsNull:
		fe.enc.RefIsNull()
	case ir.OpRefAsNonNull:
		fe.enc.RefAsNonNull()

	case ir.OpMemorySize:
		fe.enc.MemorySize()
	case ir.OpMemoryGrow:
		fe.enc.MemoryGrow()
	case ir.OpMemoryCopy:
		fe.enc.MemoryCopy()
	case ir.OpMemoryFill:
		fe.enc.MemoryFill()

	default:
		if in.Op.IsHigh() {
			return fmt.Errorf("high-level op %s in low module", in.Op)
		}
		return fmt.Errorf("unimplemented op %s", in.Op)
	}
	return nil
}

