package lower

import (
	"go/ast"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
)

// goStmt lowers `go f(args)` to OpGo with a func() closure (same thunk shape
// as defer). Marks the module as needing the scheduler runtime.
func (fl *funcLower) goStmt(s *ast.GoStmt) ([]ir.Stmt, error) {
	fl.pkg.ensureChanBaseTypes()
	if fl.pkg.meta != nil {
		fl.pkg.meta.needsSched = true
	}
	closEx, stmts, err := fl.deferClosure(s.Call)
	if err != nil {
		return nil, err
	}
	stmts = append(stmts, &ir.ExprStmt{X: &ir.Instr{
		Op:   ir.OpGo,
		Args: []ir.Expr{closEx},
	}})
	return stmts, nil
}
