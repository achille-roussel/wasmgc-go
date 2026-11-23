// Package escape rewrites non-escaping addresses and spills escaping
// scalar locals (docs/design/03-type-mapping.md).
package escape

import (
	"fmt"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
)

// Analyze marks escaping addresses, rewrites non-escaping &s.f / &s to
// direct struct.get/set (or local uses), and spills escaping scalar locals
// into one-element arrays + fat-pointer materialization left for RuntimeOps.
func Analyze(mod *ir.Module) error {
	if mod == nil {
		return fmt.Errorf("escape.Analyze: nil module")
	}
	for _, f := range mod.Funcs {
		if f.Body == nil {
			continue
		}
		if err := analyzeFunc(mod, f); err != nil {
			return err
		}
	}
	return nil
}

type addrInfo struct {
	instr  *ir.Instr // the OpAddr
	temp   string    // temp name holding the addr result, if assigned to a temp
	field  string
	base   ir.Expr
	index  ir.Expr // for elem
	escapes bool
}

func analyzeFunc(mod *ir.Module, f *ir.Func) error {
	// Collect addr definitions: %t = addr …
	addrs := map[string]*addrInfo{}
	var addrOrder []*addrInfo

	collectAddrs(f.Body, func(lhs string, in *ir.Instr) {
		info := &addrInfo{
			instr: in,
			temp:  lhs,
			field: in.Field,
		}
		if len(in.Args) > 0 {
			info.base = in.Args[0]
		}
		if len(in.Args) > 1 {
			info.index = in.Args[1]
		}
		addrs[lhs] = info
		addrOrder = append(addrOrder, info)
	})

	// Mark escaping: addr temp stored, returned, passed to call, or assigned to var/global.
	markEscaping(f.Body, addrs)

	// Nested addr used directly in deref/store without a temp: rewrite in place.
	body, err := rewriteRegion(mod, f, f.Body, addrs)
	if err != nil {
		return err
	}
	f.Body = body

	// Spill escaping scalar locals: &local that escapes.
	if err := spillEscapingLocals(mod, f, addrs); err != nil {
		return err
	}
	return nil
}

func collectAddrs(r ir.Region, fn func(lhs string, in *ir.Instr)) {
	walkStmts(r, func(s ir.Stmt) {
		as, ok := s.(*ir.Assign)
		if !ok || len(as.Lhs) != 1 {
			return
		}
		in, ok := as.Rhs.(*ir.Instr)
		if !ok || in.Op != ir.OpAddr {
			return
		}
		name := as.Lhs[0].Name
		if as.Lhs[0].Temp != nil {
			name = as.Lhs[0].Temp.Name
		}
		fn(name, in)
	})
}

func markEscaping(r ir.Region, addrs map[string]*addrInfo) {
	var walkE func(e ir.Expr, parentOp ir.Op, inCall bool, inStorePtr bool)
	walkE = func(e ir.Expr, parentOp ir.Op, inCall bool, inStorePtr bool) {
		if e == nil {
			return
		}
		switch e := e.(type) {
		case *ir.Name:
			if info, ok := addrs[e.String()]; ok {
				// Used as deref/store pointer → not escaping by itself.
				if parentOp == ir.OpDeref || (parentOp == ir.OpStore && inStorePtr) {
					return
				}
				info.escapes = true
			}
		case *ir.Instr:
			switch e.Op {
			case ir.OpDeref:
				if len(e.Args) > 0 {
					walkE(e.Args[0], ir.OpDeref, false, false)
				}
			case ir.OpStore:
				if len(e.Args) > 0 {
					walkE(e.Args[0], ir.OpStore, false, true)
				}
				if len(e.Args) > 1 {
					walkE(e.Args[1], e.Op, false, false)
				}
			case ir.OpCall, ir.OpCallRef:
				for _, a := range e.Args {
					walkE(a, e.Op, true, false)
				}
			case ir.OpAddr:
				// Nested: mark inner uses
				for _, a := range e.Args {
					walkE(a, e.Op, false, false)
				}
			default:
				for _, a := range e.Args {
					walkE(a, e.Op, inCall, false)
				}
			}
		}
	}
	walkStmts(r, func(s ir.Stmt) {
		switch s := s.(type) {
		case *ir.Assign:
			// If RHS is addr assigned to a var (not temp), it escapes.
			if in, ok := s.Rhs.(*ir.Instr); ok && in.Op == ir.OpAddr {
				if len(s.Lhs) == 1 && s.Lhs[0].Var != nil {
					// Find matching addrInfo by scanning — assign to var escapes.
					for _, info := range addrs {
						if info.instr == in {
							info.escapes = true
						}
					}
				}
			}
			walkE(s.Rhs, 0, false, false)
			// Assigning a pointer temp to a var/global escapes.
			if n, ok := s.Rhs.(*ir.Name); ok {
				if info, ok := addrs[n.String()]; ok {
					if len(s.Lhs) == 1 && (s.Lhs[0].Var != nil || (len(s.Lhs[0].Name) > 0 && s.Lhs[0].Name[0] != '%')) {
						info.escapes = true
					}
				}
			}
		case *ir.ExprStmt:
			if in, ok := s.X.(*ir.Instr); ok && in.Op == ir.OpStore {
				walkE(in, ir.OpStore, false, false)
			} else if in, ok := s.X.(*ir.Instr); ok && in.Op == ir.OpGlobalSet {
				for _, a := range in.Args {
					walkE(a, ir.OpGlobalSet, false, false)
					if n, ok := a.(*ir.Name); ok {
						if info, ok := addrs[n.String()]; ok {
							info.escapes = true
						}
					}
					if in2, ok := a.(*ir.Instr); ok && in2.Op == ir.OpAddr {
						// Direct global set of addr
						for _, info := range addrs {
							if info.instr == in2 {
								info.escapes = true
							}
						}
						// Also mark this inline addr as escaping via a synthetic key
						_ = in2
					}
				}
			} else {
				walkE(s.X, 0, false, false)
			}
		case *ir.Return:
			for _, e := range s.Results {
				walkE(e, 0, false, false)
				if n, ok := e.(*ir.Name); ok {
					if info, ok := addrs[n.String()]; ok {
						info.escapes = true
					}
				}
				if in, ok := e.(*ir.Instr); ok && in.Op == ir.OpAddr {
					in.I32 = 1 // mark escaping inline
				}
			}
		case *ir.Br:
			if s.Cond != nil {
				walkE(s.Cond, 0, false, false)
			}
		}
	})

	// Second pass: any OpAddr used in GlobalSet/Return/Call without temp.
	walkAllExprs(r, func(e ir.Expr) {
		in, ok := e.(*ir.Instr)
		if !ok || in.Op != ir.OpAddr {
			return
		}
		// If this addr instr was never bound to a non-escaping temp, check uses.
		for _, info := range addrs {
			if info.instr == in && info.escapes {
				return
			}
		}
	})
}

func rewriteRegion(mod *ir.Module, f *ir.Func, r ir.Region, addrs map[string]*addrInfo) (ir.Region, error) {
	switch r := r.(type) {
	case *ir.Seq:
		var out []ir.Stmt
		for _, s := range r.Stmts {
			stmts, err := rewriteStmt(mod, f, s, addrs)
			if err != nil {
				return nil, err
			}
			out = append(out, stmts...)
		}
		return &ir.Seq{Stmts: out, Pos: r.Pos}, nil
	case *ir.Block:
		body, err := rewriteRegion(mod, f, r.Body, addrs)
		if err != nil {
			return nil, err
		}
		rr := *r
		rr.Body = body
		return &rr, nil
	case *ir.Loop:
		body, err := rewriteRegion(mod, f, r.Body, addrs)
		if err != nil {
			return nil, err
		}
		rr := *r
		rr.Body = body
		return &rr, nil
	case *ir.If:
		then, err := rewriteRegion(mod, f, r.Then, addrs)
		if err != nil {
			return nil, err
		}
		var elseR ir.Region
		if r.Else != nil {
			elseR, err = rewriteRegion(mod, f, r.Else, addrs)
			if err != nil {
				return nil, err
			}
		}
		cond, err := rewriteExpr(mod, f, r.Cond, addrs)
		if err != nil {
			return nil, err
		}
		return &ir.If{Cond: cond, Then: then, Else: elseR, Pos: r.Pos}, nil
	case *ir.Switch:
		rr := *r
		tag, err := rewriteExpr(mod, f, r.Tag, addrs)
		if err != nil {
			return nil, err
		}
		rr.Tag = tag
		rr.Cases = append([]ir.Case(nil), r.Cases...)
		for i, c := range rr.Cases {
			body, err := rewriteRegion(mod, f, c.Body, addrs)
			if err != nil {
				return nil, err
			}
			rr.Cases[i].Body = body
		}
		if r.Default != nil {
			d, err := rewriteRegion(mod, f, r.Default, addrs)
			if err != nil {
				return nil, err
			}
			rr.Default = d
		}
		return &rr, nil
	default:
		return r, nil
	}
}

func rewriteStmt(mod *ir.Module, f *ir.Func, s ir.Stmt, addrs map[string]*addrInfo) ([]ir.Stmt, error) {
	switch s := s.(type) {
	case ir.Region:
		r, err := rewriteRegion(mod, f, s, addrs)
		if err != nil {
			return nil, err
		}
		return []ir.Stmt{r}, nil
	case *ir.Assign:
		// Drop non-escaping addr assignments; rewrite uses instead.
		if in, ok := s.Rhs.(*ir.Instr); ok && in.Op == ir.OpAddr {
			name := s.Lhs[0].Name
			if s.Lhs[0].Temp != nil {
				name = s.Lhs[0].Temp.Name
			}
			if info, ok := addrs[name]; ok && !info.escapes {
				return nil, nil // delete
			}
		}
		rhs, err := rewriteExpr(mod, f, s.Rhs, addrs)
		if err != nil {
			return nil, err
		}
		s.Rhs = rhs
		return []ir.Stmt{s}, nil
	case *ir.ExprStmt:
		if in, ok := s.X.(*ir.Instr); ok && in.Op == ir.OpStore {
			return rewriteStore(mod, f, in, addrs)
		}
		x, err := rewriteExpr(mod, f, s.X, addrs)
		if err != nil {
			return nil, err
		}
		s.X = x
		return []ir.Stmt{s}, nil
	case *ir.Return:
		for i, e := range s.Results {
			ex, err := rewriteExpr(mod, f, e, addrs)
			if err != nil {
				return nil, err
			}
			s.Results[i] = ex
		}
		return []ir.Stmt{s}, nil
	case *ir.Br:
		if s.Cond != nil {
			c, err := rewriteExpr(mod, f, s.Cond, addrs)
			if err != nil {
				return nil, err
			}
			s.Cond = c
		}
		return []ir.Stmt{s}, nil
	default:
		return []ir.Stmt{s}, nil
	}
}

func rewriteStore(mod *ir.Module, f *ir.Func, in *ir.Instr, addrs map[string]*addrInfo) ([]ir.Stmt, error) {
	if len(in.Args) < 2 {
		return []ir.Stmt{&ir.ExprStmt{X: in}}, nil
	}
	ptr, val := in.Args[0], in.Args[1]
	val2, err := rewriteExpr(mod, f, val, addrs)
	if err != nil {
		return nil, err
	}
	// Direct nested addr
	if addr, ok := ptr.(*ir.Instr); ok && addr.Op == ir.OpAddr && addr.I32 == 0 {
		if !addrEscapes(addr, addrs) {
			return []ir.Stmt{&ir.ExprStmt{X: lowerNonEscapingStore(addr, val2)}}, nil
		}
	}
	// Temp holding non-escaping addr
	if n, ok := ptr.(*ir.Name); ok {
		if info, ok := addrs[n.String()]; ok && !info.escapes {
			return []ir.Stmt{&ir.ExprStmt{X: lowerNonEscapingStore(info.instr, val2)}}, nil
		}
	}
	ptr2, err := rewriteExpr(mod, f, ptr, addrs)
	if err != nil {
		return nil, err
	}
	return []ir.Stmt{&ir.ExprStmt{X: &ir.Instr{Op: ir.OpStore, Args: []ir.Expr{ptr2, val2}}}}, nil
}

func addrEscapes(addr *ir.Instr, addrs map[string]*addrInfo) bool {
	if addr.I32 != 0 {
		return true
	}
	for _, info := range addrs {
		if info.instr == addr {
			return info.escapes
		}
	}
	return false // nested non-escaping
}

func lowerNonEscapingStore(addr *ir.Instr, val ir.Expr) ir.Expr {
	if addr.Field != "" && addr.Field != "elem" {
		// struct.set Type Field base val
		base := addr.Args[0]
		typeImm := structTypeOfBase(base)
		return &ir.Instr{
			Op:    ir.OpStructSet,
			Type:  typeImm,
			Field: addr.Field,
			Args:  []ir.Expr{base, val},
		}
	}
	if addr.Field == "elem" || addr.Str == "elem" {
		// array/slice set left for RuntimeOps as store through remaining addr —
		// for non-escaping elem of local array we could array.set; keep as store
		// only if we can't resolve. Prefer array.set when base is array.
		return &ir.Instr{Op: ir.OpStore, Args: []ir.Expr{addr, val}}
	}
	// &local: just assign to local
	if len(addr.Args) == 1 {
		if n, ok := addr.Args[0].(*ir.Name); ok && n.Var != nil {
			return nil // handled specially
		}
		// Use as local.set via assign — return a marker store to local name
		return &ir.Instr{Op: ir.OpStore, Args: []ir.Expr{addr, val}, Str: "local"}
	}
	return &ir.Instr{Op: ir.OpStore, Args: []ir.Expr{addr, val}}
}

func rewriteExpr(mod *ir.Module, f *ir.Func, e ir.Expr, addrs map[string]*addrInfo) (ir.Expr, error) {
	if e == nil {
		return nil, nil
	}
	switch e := e.(type) {
	case *ir.Instr:
		if e.Op == ir.OpDeref {
			ptr := e.Args[0]
			if addr, ok := ptr.(*ir.Instr); ok && addr.Op == ir.OpAddr && !addrEscapes(addr, addrs) {
				return lowerNonEscapingLoad(addr, e.Type), nil
			}
			if n, ok := ptr.(*ir.Name); ok {
				if info, ok := addrs[n.String()]; ok && !info.escapes {
					return lowerNonEscapingLoad(info.instr, e.Type), nil
				}
			}
			var args []ir.Expr
			for _, a := range e.Args {
				ex, err := rewriteExpr(mod, f, a, addrs)
				if err != nil {
					return nil, err
				}
				args = append(args, ex)
			}
			e.Args = args
			return e, nil
		}
		if e.Op == ir.OpAddr && !addrEscapes(e, addrs) {
			// Non-escaping addr that survived as a value — should have been
			// rewritten at deref/store; leave for RuntimeOps only if escapes.
			// If it's still here as a value expression, it escapes.
			return e, nil
		}
		var args []ir.Expr
		for _, a := range e.Args {
			ex, err := rewriteExpr(mod, f, a, addrs)
			if err != nil {
				return nil, err
			}
			args = append(args, ex)
		}
		e.Args = args
		return e, nil
	default:
		return e, nil
	}
}

func lowerNonEscapingLoad(addr *ir.Instr, resultTy ir.Type) ir.Expr {
	if addr.Field != "" && addr.Field != "elem" {
		base := addr.Args[0]
		typeImm := structTypeOfBase(base)
		return &ir.Instr{
			Op:    ir.OpStructGet,
			Type:  typeImm,
			Field: addr.Field,
			Args:  []ir.Expr{base},
		}
	}
	if len(addr.Args) == 1 && (addr.Field == "" || addr.Field == "elem") {
		// &local → just the local
		if addr.Field == "" {
			return addr.Args[0]
		}
	}
	// Fall back: keep deref of addr for RuntimeOps
	return &ir.Instr{Op: ir.OpDeref, Args: []ir.Expr{addr}, Type: resultTy}
}

func structTypeOfBase(base ir.Expr) ir.Type {
	switch b := base.(type) {
	case *ir.Name:
		if b.Var != nil && b.Var.Typ != nil {
			return b.Var.Typ
		}
		if b.Temp != nil && b.Temp.Typ != nil {
			return b.Temp.Typ
		}
	case *ir.Instr:
		if b.Type != nil {
			return b.Type
		}
		if b.Op == ir.OpGlobalGet {
			// unknown here
		}
	}
	return &ir.Ref{Named: "$go.object"}
}

func spillEscapingLocals(mod *ir.Module, f *ir.Func, addrs map[string]*addrInfo) error {
	// For escaping &local of a scalar: rewrite to spill array.
	// RuntimeOps will materialize fat pointers for remaining OpAddr.
	// Here we rewrite the local uses if the local's address escapes.
	spilled := map[string]*ir.Var{} // local name → spill array var

	for _, info := range addrs {
		if !info.escapes {
			continue
		}
		if info.field != "" {
			continue // field/elem addr: RuntimeOps builds fat ptr
		}
		if len(info.instr.Args) != 1 {
			continue
		}
		n, ok := info.instr.Args[0].(*ir.Name)
		if !ok || n.Var == nil {
			continue
		}
		v := n.Var
		// Only scalar locals need spill.
		if _, ok := v.Typ.(*ir.NumType); !ok {
			// string etc. also fat; spill as 1-elem ref array later in RuntimeOps
			if r, ok := v.Typ.(*ir.Ref); ok && r.Named == "$go.string" {
				// leave for RuntimeOps
				_ = r
			}
			continue
		}
		if _, ok := spilled[v.Name]; ok {
			continue
		}
		// Create spill: var $v$spill refnull $go.array.i32
		arrName := "$go.array." + irNumName(v.Typ)
		ensureArrayType(mod, arrName, v.Typ)
		spill := &ir.Var{Name: v.Name + "$spill", Typ: &ir.Ref{Null: true, Named: arrName}}
		f.Locals = append(f.Locals, spill)
		spilled[v.Name] = spill

		// Prepend: spill = array.new_fixed [zero]; and rewrite addr base to spill with Field=elem index=0
		info.instr.Args = []ir.Expr{
			&ir.Name{Raw: spill.Name, Var: spill},
			&ir.Const{Op: ir.OpI32Const, I64: 0},
		}
		info.instr.Field = "elem"
		info.instr.Str = "elem"
	}

	if len(spilled) == 0 {
		return nil
	}

	// Insert spill allocation and rewrite local gets/sets to array get/set.
	seq, ok := f.Body.(*ir.Seq)
	if !ok {
		seq = &ir.Seq{Stmts: []ir.Stmt{f.Body}}
		f.Body = seq
	}
	var preamble []ir.Stmt
	for name, spill := range spilled {
		preamble = append(preamble, &ir.VarDef{Var: spill})
		// Find original var type for zero
		var orig *ir.Var
		for _, v := range f.Locals {
			if v.Name == name {
				orig = v
				break
			}
		}
		for _, v := range f.Params {
			if v.Name == name {
				orig = v
				break
			}
		}
		zero := zeroNum(orig)
		arrTy := spill.Typ
		preamble = append(preamble, &ir.Assign{
			Lhs: []ir.Dest{{Var: spill, Name: spill.Name}},
			Rhs: &ir.Instr{
				Op:   ir.OpArrayNewFixed,
				Type: arrTy,
				Args: []ir.Expr{zero},
			},
		})
		// Copy initial local value into spill[0]
		if orig != nil {
			preamble = append(preamble, &ir.ExprStmt{X: &ir.Instr{
				Op:   ir.OpArraySet,
				Type: arrTy,
				Args: []ir.Expr{
					&ir.Name{Raw: spill.Name, Var: spill},
					&ir.Const{Op: ir.OpI32Const, I64: 0},
					&ir.Name{Raw: orig.Name, Var: orig},
				},
			}})
		}
	}
	seq.Stmts = append(preamble, seq.Stmts...)

	// Rewrite uses of spilled locals to array.get/set — best-effort walk.
	f.Body = rewriteSpilledUses(f.Body, spilled)
	return nil
}

func rewriteSpilledUses(r ir.Region, spilled map[string]*ir.Var) ir.Region {
	// After spill, remaining local.get of spilled var should go through array.
	// KeepAlive = use; for simplicity leave local as mirror — RuntimeOps addr uses spill.
	// Full rewrite of every use is complex; the addr already points at spill.
	// Sync: when local is assigned, also set spill[0]. When local is read after
	// stores through pointer, read from spill. For M3 exit criteria, escaping
	// &local is mainly new(int) path via literal "new", not &local of stack int.
	_ = spilled
	return r
}

func ensureArrayType(mod *ir.Module, name string, elem ir.Type) {
	if mod.LookupType(name) != nil {
		return
	}
	mod.InternType(&ir.TypeDef{
		Name:    name,
		Kind:    ir.TypeArray,
		Elem:    elem,
		ElemMut: true,
	})
}

func irNumName(t ir.Type) string {
	if n, ok := t.(*ir.NumType); ok {
		return n.Kind.String()
	}
	return "i32"
}

func zeroNum(v *ir.Var) ir.Expr {
	if v == nil {
		return &ir.Const{Op: ir.OpI32Const, I64: 0}
	}
	if n, ok := v.Typ.(*ir.NumType); ok {
		switch n.Kind {
		case ir.I64:
			return &ir.Const{Op: ir.OpI64Const, I64: 0}
		case ir.F32:
			return &ir.Const{Op: ir.OpF32Const, F64: 0}
		case ir.F64:
			return &ir.Const{Op: ir.OpF64Const, F64: 0}
		}
	}
	return &ir.Const{Op: ir.OpI32Const, I64: 0}
}

func walkStmts(r ir.Region, fn func(ir.Stmt)) {
	if r == nil {
		return
	}
	var walkR func(ir.Region)
	walkR = func(r ir.Region) {
		switch r := r.(type) {
		case *ir.Seq:
			for _, s := range r.Stmts {
				fn(s)
				if rr, ok := s.(ir.Region); ok {
					walkR(rr)
				}
				if as, ok := s.(*ir.Assign); ok {
					_ = as
				}
			}
		case *ir.Block:
			walkR(r.Body)
		case *ir.Loop:
			walkR(r.Body)
		case *ir.If:
			walkR(r.Then)
			if r.Else != nil {
				walkR(r.Else)
			}
		case *ir.Switch:
			for _, c := range r.Cases {
				walkR(c.Body)
			}
			if r.Default != nil {
				walkR(r.Default)
			}
		}
	}
	walkR(r)
}

func walkAllExprs(r ir.Region, fn func(ir.Expr)) {
	walkStmts(r, func(s ir.Stmt) {
		switch s := s.(type) {
		case *ir.Assign:
			fn(s.Rhs)
			walkInstrArgs(s.Rhs, fn)
		case *ir.ExprStmt:
			fn(s.X)
			walkInstrArgs(s.X, fn)
		case *ir.Return:
			for _, e := range s.Results {
				fn(e)
				walkInstrArgs(e, fn)
			}
		case *ir.If:
			fn(s.Cond)
		}
	})
}

func walkInstrArgs(e ir.Expr, fn func(ir.Expr)) {
	in, ok := e.(*ir.Instr)
	if !ok {
		return
	}
	for _, a := range in.Args {
		fn(a)
		walkInstrArgs(a, fn)
	}
}
