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

func (fl *funcLower) printName(e *ast.CallExpr) string {
	id := e.Fun.(*ast.Ident)
	if obj := fl.info.Uses[id]; obj != nil {
		if b, ok := obj.(*types.Builtin); ok {
			return b.Name()
		}
	}
	return id.Name
}

// lowerPrint emits runtime.printstring / printint / printnl per 04-runtime-and-abi.md.
func (fl *funcLower) lowerPrint(e *ast.CallExpr) ([]ir.Stmt, error) {
	nl := fl.printName(e) == "println"
	var out []ir.Stmt
	for i, arg := range e.Args {
		if i > 0 {
			// Space between args (Go println/print).
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
		// print bool as 0/1 via printint
		stmts = append(stmts, &ir.ExprStmt{
			X: &ir.Instr{Op: ir.OpCall, Func: "runtime.printint", Args: []ir.Expr{ex}},
		})
		return stmts, nil
	}
	if isInteger(ty) {
		want, err := mapType(ty)
		if err != nil {
			return nil, err
		}
		ex, stmts, err := fl.expr(arg, want)
		if err != nil {
			return nil, err
		}
		if irNumWidth(want) == ir.I64 {
			// 04 lists only printint (i32). Truncate for M2; document gap.
			ex = &ir.Instr{Op: ir.OpI32WrapI64, Args: []ir.Expr{ex}}
		}
		stmts = append(stmts, &ir.ExprStmt{
			X: &ir.Instr{Op: ir.OpCall, Func: "runtime.printint", Args: []ir.Expr{ex}},
		})
		return stmts, nil
	}
	return nil, fl.errorf(arg, "print of type %s not supported in M1/M2 (need string or int)", ty)
}
