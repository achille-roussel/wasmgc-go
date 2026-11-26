package escape

import (
	"fmt"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
)

// Analyze rewrites non-escaping addresses to struct.get/set (or local
// loads/stores) and leaves escaping addr/deref/store for RuntimeOps.
func Analyze(mod *ir.Module) error {
	if mod == nil {
		return fmt.Errorf("escape.Analyze: nil module")
	}
	for _, f := range mod.Funcs {
		if f.Body == nil {
			continue
		}
		if err := analyzeFunc(f); err != nil {
			return err
		}
	}
	return nil
}

type addrInfo struct {
	instr   *ir.Instr
	name    string // temp or var holding the pointer
	escapes bool
}

func analyzeFunc(f *ir.Func) error {
	addrs := map[string]*addrInfo{}
	collectAddrs(f.Body, addrs)
	markEscaping(f.Body, addrs)
	body, err := rewriteRegion(f.Body, addrs)
	if err != nil {
		return err
	}
	f.Body = body
	return nil
}

func collectAddrs(r ir.Region, addrs map[string]*addrInfo) {
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
		} else if as.Lhs[0].Var != nil {
			name = as.Lhs[0].Var.Name
		}
		addrs[name] = &addrInfo{instr: in, name: name}
	})
}

func markEscaping(r ir.Region, addrs map[string]*addrInfo) {
	escapeName := func(e ir.Expr) {
		switch e := e.(type) {
		case *ir.Name:
			if info, ok := addrs[e.String()]; ok {
				info.escapes = true
			}
		case *ir.Instr:
			if e.Op == ir.OpAddr {
				e.I32 = 1
			}
		}
	}
	walkStmts(r, func(s ir.Stmt) {
		switch s := s.(type) {
		case *ir.Assign:
			// Copy of pointer to another name: if RHS is addr-holder, LHS becomes holder too
			// or marks escape if assigned in a way that leaves — treat copy to var as alias.
			if n, ok := s.Rhs.(*ir.Name); ok {
				if info, ok := addrs[n.String()]; ok {
					lhs := s.Lhs[0].Name
					if s.Lhs[0].Var != nil {
						lhs = s.Lhs[0].Var.Name
					}
					if s.Lhs[0].Temp != nil {
						lhs = s.Lhs[0].Temp.Name
					}
					// Share the same addrInfo so escape via a copy (e.g. fat-ptr
					// compare temps) keeps the original OpAddr materialization.
					addrs[lhs] = info
				}
			}
			markUses(s.Rhs, addrs, false) // assigning addr to local does not escape by itself
		case *ir.ExprStmt:
			if in, ok := s.X.(*ir.Instr); ok {
				switch in.Op {
				case ir.OpStore:
					if len(in.Args) > 0 {
						markUses(in.Args[0], addrs, false)
					}
					if len(in.Args) > 1 {
						markUses(in.Args[1], addrs, true) // stored value escapes if it's a pointer
						escapeName(in.Args[1])
					}
				case ir.OpGlobalSet:
					for _, a := range in.Args {
						escapeName(a)
						markUses(a, addrs, true)
					}
				case ir.OpCall, ir.OpCallRef:
					for _, a := range in.Args {
						escapeName(a)
						markUses(a, addrs, true)
					}
				default:
					markUses(in, addrs, false)
				}
			}
		case *ir.Return:
			for _, e := range s.Results {
				escapeName(e)
				markUses(e, addrs, true)
			}
		case *ir.Br:
			if s.Cond != nil {
				markUses(s.Cond, addrs, false)
			}
		case *ir.If:
			markUses(s.Cond, addrs, false)
		}
	})
}

func markUses(e ir.Expr, addrs map[string]*addrInfo, escapeContext bool) {
	if e == nil {
		return
	}
	switch e := e.(type) {
	case *ir.Name:
		if escapeContext {
			if info, ok := addrs[e.String()]; ok {
				info.escapes = true
			}
		}
	case *ir.Instr:
		switch e.Op {
		case ir.OpDeref:
			if len(e.Args) > 0 {
				markUses(e.Args[0], addrs, false)
			}
		case ir.OpStore:
			if len(e.Args) > 0 {
				markUses(e.Args[0], addrs, false)
			}
			if len(e.Args) > 1 {
				markUses(e.Args[1], addrs, escapeContext)
			}
		case ir.OpAddr:
			for _, a := range e.Args {
				markUses(a, addrs, false)
			}
			if escapeContext {
				e.I32 = 1
			}
		case ir.OpCall, ir.OpCallRef, ir.OpGlobalSet:
			for _, a := range e.Args {
				markUses(a, addrs, true)
			}
		case ir.OpRefIsNull, ir.OpRefEq, ir.OpRefCast, ir.OpStructGet, ir.OpStructSet:
			// Fat-pointer value uses (null check, base/index inspect) need the
			// addr materialized — not the load/store-through rewrite.
			for _, a := range e.Args {
				markUses(a, addrs, true)
			}
		default:
			for _, a := range e.Args {
				markUses(a, addrs, escapeContext)
			}
		}
	}
}

func rewriteRegion(r ir.Region, addrs map[string]*addrInfo) (ir.Region, error) {
	switch r := r.(type) {
	case *ir.Seq:
		var out []ir.Stmt
		for _, s := range r.Stmts {
			ss, err := rewriteStmt(s, addrs)
			if err != nil {
				return nil, err
			}
			out = append(out, ss...)
		}
		return &ir.Seq{Stmts: out, Pos: r.Pos}, nil
	case *ir.Block:
		body, err := rewriteRegion(r.Body, addrs)
		if err != nil {
			return nil, err
		}
		rr := *r
		rr.Body = body
		return &rr, nil
	case *ir.Loop:
		body, err := rewriteRegion(r.Body, addrs)
		if err != nil {
			return nil, err
		}
		rr := *r
		rr.Body = body
		return &rr, nil
	case *ir.If:
		then, err := rewriteRegion(r.Then, addrs)
		if err != nil {
			return nil, err
		}
		var elseR ir.Region
		if r.Else != nil {
			elseR, err = rewriteRegion(r.Else, addrs)
			if err != nil {
				return nil, err
			}
		}
		cond, err := rewriteExpr(r.Cond, addrs)
		if err != nil {
			return nil, err
		}
		return &ir.If{Cond: cond, Then: then, Else: elseR, Pos: r.Pos}, nil
	case *ir.Switch:
		rr := *r
		tag, err := rewriteExpr(r.Tag, addrs)
		if err != nil {
			return nil, err
		}
		rr.Tag = tag
		rr.Cases = append([]ir.Case(nil), r.Cases...)
		for i, c := range rr.Cases {
			body, err := rewriteRegion(c.Body, addrs)
			if err != nil {
				return nil, err
			}
			rr.Cases[i].Body = body
		}
		if r.Default != nil {
			d, err := rewriteRegion(r.Default, addrs)
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

func rewriteStmt(s ir.Stmt, addrs map[string]*addrInfo) ([]ir.Stmt, error) {
	switch s := s.(type) {
	case ir.Region:
		r, err := rewriteRegion(s, addrs)
		if err != nil {
			return nil, err
		}
		return []ir.Stmt{r}, nil
	case *ir.Assign:
		if in, ok := s.Rhs.(*ir.Instr); ok && in.Op == ir.OpAddr {
			name := s.Lhs[0].Name
			if s.Lhs[0].Temp != nil {
				name = s.Lhs[0].Temp.Name
			} else if s.Lhs[0].Var != nil {
				name = s.Lhs[0].Var.Name
			}
			if info, ok := addrs[name]; ok && !info.escapes {
				return nil, nil // drop non-escaping addr materialization
			}
		}
		rhs, err := rewriteExpr(s.Rhs, addrs)
		if err != nil {
			return nil, err
		}
		s.Rhs = rhs
		return []ir.Stmt{s}, nil
	case *ir.ExprStmt:
		if in, ok := s.X.(*ir.Instr); ok && in.Op == ir.OpStore {
			return rewriteStore(in, addrs)
		}
		x, err := rewriteExpr(s.X, addrs)
		if err != nil {
			return nil, err
		}
		s.X = x
		return []ir.Stmt{s}, nil
	case *ir.Return:
		for i, e := range s.Results {
			ex, err := rewriteExpr(e, addrs)
			if err != nil {
				return nil, err
			}
			s.Results[i] = ex
		}
		return []ir.Stmt{s}, nil
	case *ir.Br:
		if s.Cond != nil {
			c, err := rewriteExpr(s.Cond, addrs)
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

func rewriteStore(in *ir.Instr, addrs map[string]*addrInfo) ([]ir.Stmt, error) {
	if len(in.Args) < 2 {
		return []ir.Stmt{&ir.ExprStmt{X: in}}, nil
	}
	ptr, val := in.Args[0], in.Args[1]
	val2, err := rewriteExpr(val, addrs)
	if err != nil {
		return nil, err
	}
	if addr, ok := nonEscapingAddr(ptr, addrs); ok {
		return storeThrough(addr, val2), nil
	}
	ptr2, err := rewriteExpr(ptr, addrs)
	if err != nil {
		return nil, err
	}
	return []ir.Stmt{&ir.ExprStmt{X: &ir.Instr{Op: ir.OpStore, Args: []ir.Expr{ptr2, val2}}}}, nil
}

func rewriteExpr(e ir.Expr, addrs map[string]*addrInfo) (ir.Expr, error) {
	if e == nil {
		return nil, nil
	}
	in, ok := e.(*ir.Instr)
	if !ok {
		return e, nil
	}
	if in.Op == ir.OpDeref && len(in.Args) > 0 {
		if addr, ok := nonEscapingAddr(in.Args[0], addrs); ok {
			return loadThrough(addr, in.Type), nil
		}
	}
	// Nested: call runtime.printint(deref p) — rewrite args
	for i, a := range in.Args {
		ex, err := rewriteExpr(a, addrs)
		if err != nil {
			return nil, err
		}
		in.Args[i] = ex
	}
	return in, nil
}

func nonEscapingAddr(ptr ir.Expr, addrs map[string]*addrInfo) (*ir.Instr, bool) {
	if addr, ok := ptr.(*ir.Instr); ok && addr.Op == ir.OpAddr && addr.I32 == 0 {
		return addr, true
	}
	if n, ok := ptr.(*ir.Name); ok {
		if info, ok := addrs[n.String()]; ok && !info.escapes {
			return info.instr, true
		}
	}
	return nil, false
}

func loadThrough(addr *ir.Instr, resultTy ir.Type) ir.Expr {
	if addr.Field != "" && addr.Field != "elem" {
		return &ir.Instr{
			Op:    ir.OpStructGet,
			Type:  structTypeForFieldAddr(addr),
			Field: addr.Field,
			Args:  []ir.Expr{addr.Args[0]},
		}
	}
	if addr.Field == "" && len(addr.Args) == 1 {
		return addr.Args[0]
	}
	return &ir.Instr{Op: ir.OpDeref, Args: []ir.Expr{addr}, Type: resultTy}
}

func storeThrough(addr *ir.Instr, val ir.Expr) []ir.Stmt {
	if addr.Field != "" && addr.Field != "elem" {
		return []ir.Stmt{&ir.ExprStmt{X: &ir.Instr{
			Op:    ir.OpStructSet,
			Type:  structTypeForFieldAddr(addr),
			Field: addr.Field,
			Args:  []ir.Expr{addr.Args[0], val},
		}}}
	}
	if addr.Field == "" && len(addr.Args) == 1 {
		if n, ok := addr.Args[0].(*ir.Name); ok {
			return []ir.Stmt{&ir.Assign{
				Lhs: []ir.Dest{{Var: n.Var, Temp: n.Temp, Name: n.String()}},
				Rhs: val,
			}}
		}
	}
	return []ir.Stmt{&ir.ExprStmt{X: &ir.Instr{Op: ir.OpStore, Args: []ir.Expr{addr, val}}}}
}

// structTypeForFieldAddr returns the struct type that owns addr.Field.
// Prefer Addr.Str (set by lower for promoted/embedded fields). Falling back
// to Args[0].Type is wrong when Args[0] is struct.get of an embed: that
// Type names the outer struct, not the embedded field's type.
func structTypeForFieldAddr(addr *ir.Instr) ir.Type {
	if addr.Str != "" && addr.Str != "elem" {
		return &ir.Ref{Named: addr.Str}
	}
	return structTypeOfBase(addr.Args[0])
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
	}
	return &ir.Ref{Named: "$go.object"}
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
				if sub, ok := s.(ir.Region); ok {
					walkR(sub)
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
