package lower

import (
	"go/ast"
	"go/types"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
)

func (fl *funcLower) isBuiltinPrint(e *ast.CallExpr) bool {
	id, ok := e.Fun.(*ast.Ident)
	if !ok {
		return false
	}
	obj := fl.info.Uses[id]
	if obj == nil {
		return id.Name == "print" || id.Name == "println"
	}
	if b, ok := obj.(*types.Builtin); ok {
		return b.Name() == "print" || b.Name() == "println"
	}
	return false
}

func (fl *funcLower) builtinName(e *ast.CallExpr) string {
	id, ok := e.Fun.(*ast.Ident)
	if !ok {
		return ""
	}
	if obj := fl.info.Uses[id]; obj != nil {
		if b, ok := obj.(*types.Builtin); ok {
			return b.Name()
		}
	}
	return id.Name
}

func (fl *funcLower) isBuiltinCall(e *ast.CallExpr) bool {
	switch fl.builtinName(e) {
	case "make", "new", "append", "len", "cap", "print", "println", "panic", "recover", "close", "delete", "clear":
		return true
	}
	return false
}

func (fl *funcLower) printName(e *ast.CallExpr) string {
	return fl.builtinName(e)
}

// lowerPrint emits runtime.printstring / printint / printnl per 04-runtime-and-abi.md.
func (fl *funcLower) lowerPrint(e *ast.CallExpr) ([]ir.Stmt, error) {
	nl := fl.printName(e) == "println"
	var out []ir.Stmt
	for i, arg := range e.Args {
		if i > 0 {
			t := fl.newTemp(typeString)
			out = append(out, fl.assignTemp(t, &ir.Instr{Op: ir.OpStringLit, Str: " "}))
			out = append(out, &ir.ExprStmt{
				X: &ir.Instr{Op: ir.OpCall, Func: "runtime.printstring", Args: []ir.Expr{fl.useTemp(t)}},
			})
		}
		stmts, err := fl.printArg(arg)
		if err != nil {
			return nil, err
		}
		out = append(out, stmts...)
	}
	if nl {
		out = append(out, &ir.ExprStmt{
			X: &ir.Instr{Op: ir.OpCall, Func: "runtime.printnl"},
		})
	}
	return out, nil
}

func (fl *funcLower) printArg(arg ast.Expr) ([]ir.Stmt, error) {
	ty := fl.typeOf(arg)
	if ty == nil {
		return nil, fl.errorf(arg, "no type for print arg")
	}
	ty = types.Unalias(ty)
	if isString(ty) {
		ex, stmts, err := fl.expr(arg, typeString)
		if err != nil {
			return nil, err
		}
		t := fl.newTemp(typeString)
		stmts = append(stmts, fl.assignTemp(t, ex))
		stmts = append(stmts, &ir.ExprStmt{
			X: &ir.Instr{Op: ir.OpCall, Func: "runtime.printstring", Args: []ir.Expr{fl.useTemp(t)}},
		})
		return stmts, nil
	}
	if isBoolean(ty) {
		ex, stmts, err := fl.boolExpr(arg)
		if err != nil {
			return nil, err
		}
		stmts = append(stmts, &ir.ExprStmt{
			X: &ir.Instr{Op: ir.OpCall, Func: "runtime.printint", Args: []ir.Expr{ex}},
		})
		return stmts, nil
	}
	if isInteger(ty) {
		want, err := fl.mapType(ty)
		if err != nil {
			return nil, err
		}
		ex, stmts, err := fl.expr(arg, want)
		if err != nil {
			return nil, err
		}
		if irNumWidth(want) == ir.I64 {
			ex = &ir.Instr{Op: ir.OpI32WrapI64, Args: []ir.Expr{ex}}
		}
		stmts = append(stmts, &ir.ExprStmt{
			X: &ir.Instr{Op: ir.OpCall, Func: "runtime.printint", Args: []ir.Expr{ex}},
		})
		return stmts, nil
	}
	return nil, fl.errorf(arg, "print of type %s not supported in M3 (need string or int)", ty)
}

func (fl *funcLower) builtinAsValue(e *ast.CallExpr, want ir.Type) (ir.Expr, []ir.Stmt, error) {
	switch fl.builtinName(e) {
	case "len":
		return fl.builtinLenCap(e, ir.OpLen, want)
	case "cap":
		return fl.builtinLenCap(e, ir.OpCap, want)
	case "make":
		return fl.builtinMake(e, want)
	case "new":
		return fl.builtinNew(e, want)
	case "append":
		return fl.builtinAppend(e, want)
	case "recover":
		return fl.builtinRecover(e)
	case "close":
		st, err := fl.builtinClose(e)
		return nil, st, err
	case "delete":
		st, err := fl.builtinDelete(e)
		return nil, st, err
	case "clear":
		st, err := fl.builtinClear(e)
		return nil, st, err
	case "panic":
		return nil, nil, fl.errorf(e, "panic used as value")
	case "print", "println":
		return nil, nil, fl.errorf(e, "print used as value")
	default:
		return nil, nil, fl.errorf(e, "unknown builtin")
	}
}

func (fl *funcLower) builtinClose(e *ast.CallExpr) ([]ir.Stmt, error) {
	if len(e.Args) != 1 {
		return nil, fl.errorf(e, "close wants 1 arg")
	}
	fl.pkg.ensureChanBaseTypes()
	if fl.pkg.meta != nil {
		fl.pkg.meta.needsSched = true
	}
	ch, stmts, err := fl.expr(e.Args[0], nil)
	if err != nil {
		return nil, err
	}
	stmts = append(stmts, &ir.ExprStmt{X: &ir.Instr{Op: ir.OpChanClose, Args: []ir.Expr{ch}}})
	return stmts, nil
}

func (fl *funcLower) builtinLenCap(e *ast.CallExpr, op ir.Op, want ir.Type) (ir.Expr, []ir.Stmt, error) {
	if len(e.Args) != 1 {
		return nil, nil, fl.errorf(e, "len/cap wants 1 arg")
	}
	arg, stmts, err := fl.expr(e.Args[0], nil)
	if err != nil {
		return nil, nil, err
	}
	_ = want
	return &ir.Instr{Op: op, Args: []ir.Expr{arg}}, stmts, nil
}

func (fl *funcLower) builtinMake(e *ast.CallExpr, want ir.Type) (ir.Expr, []ir.Stmt, error) {
	resTy := fl.typeOf(e)
	irt, err := fl.mapType(resTy)
	if err != nil {
		return nil, nil, fl.errorf(e, "%v", err)
	}
	if want != nil {
		irt = want
	}
	if isChan(resTy) {
		fl.pkg.ensureChanBaseTypes()
		if fl.pkg.meta != nil {
			fl.pkg.meta.needsSched = true
		}
		var capEx ir.Expr = fl.i32const(0)
		var stmts []ir.Stmt
		if len(e.Args) >= 2 {
			var cstmts []ir.Stmt
			capEx, cstmts, err = fl.expr(e.Args[1], ir.TypeI32)
			if err != nil {
				return nil, nil, err
			}
			stmts = append(stmts, cstmts...)
		}
		return &ir.Instr{Op: ir.OpChanMake, Type: irt, Args: []ir.Expr{capEx}}, stmts, nil
	}
	if isMap(resTy) {
		fl.markNeedsMaps()
		var hint ir.Expr = fl.i32const(0)
		var stmts []ir.Stmt
		if len(e.Args) >= 2 {
			var hstmts []ir.Stmt
			hint, hstmts, err = fl.expr(e.Args[1], ir.TypeI32)
			if err != nil {
				return nil, nil, err
			}
			stmts = append(stmts, hstmts...)
		}
		return &ir.Instr{Op: ir.OpMapMake, Type: irt, Args: []ir.Expr{hint}}, stmts, nil
	}
	if len(e.Args) < 2 {
		return nil, nil, fl.errorf(e, "make needs type and length")
	}
	if !isSlice(resTy) {
		return nil, nil, fl.errorf(e, "make of non-slice/chan/map not supported")
	}
	lenEx, stmts, err := fl.expr(e.Args[1], ir.TypeI32)
	if err != nil {
		return nil, nil, err
	}
	var capEx ir.Expr
	if len(e.Args) >= 3 {
		var cstmts []ir.Stmt
		capEx, cstmts, err = fl.expr(e.Args[2], ir.TypeI32)
		if err != nil {
			return nil, nil, err
		}
		stmts = append(stmts, cstmts...)
	} else {
		capEx = lenEx
	}
	return &ir.Instr{Op: ir.OpMakeSlice, Type: irt, Args: []ir.Expr{lenEx, capEx}}, stmts, nil
}

func (fl *funcLower) builtinNew(e *ast.CallExpr, want ir.Type) (ir.Expr, []ir.Stmt, error) {
	if len(e.Args) != 1 {
		return nil, nil, fl.errorf(e, "new wants 1 type arg")
	}
	resTy := fl.typeOf(e)
	irt, err := fl.mapType(resTy)
	if err != nil {
		return nil, nil, fl.errorf(e, "%v", err)
	}
	if want != nil {
		irt = want
	}
	// literal … Str:"new" → RuntimeOps allocates (1-elem array + fat ptr, or struct).
	return &ir.Instr{Op: ir.OpLiteral, Type: irt, Str: "new"}, nil, nil
}

func (fl *funcLower) builtinAppend(e *ast.CallExpr, want ir.Type) (ir.Expr, []ir.Stmt, error) {
	if len(e.Args) < 1 {
		return nil, nil, fl.errorf(e, "append needs a slice")
	}
	slice, stmts, err := fl.expr(e.Args[0], nil)
	if err != nil {
		return nil, nil, err
	}
	args := []ir.Expr{slice}
	for _, a := range e.Args[1:] {
		ex, st, err := fl.expr(a, nil)
		if err != nil {
			return nil, nil, err
		}
		stmts = append(stmts, st...)
		args = append(args, ex)
	}
	ty := want
	if ty == nil {
		ty, _ = fl.mapType(fl.typeOf(e))
	}
	return &ir.Instr{Op: ir.OpSliceAppend, Type: ty, Args: args}, stmts, nil
}
