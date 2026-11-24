package rtlink

import (
	"path/filepath"
	"testing"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
	"github.com/achille-roussel/wasmgc-go/internal/stdlib"
)

func TestEnsureUTF8LinksAppendRune(t *testing.T) {
	mod := &ir.Module{}
	mod.Funcs = append(mod.Funcs, &ir.Func{
		Name: "main.main",
		Body: &ir.Seq{Stmts: []ir.Stmt{
			&ir.ExprStmt{X: &ir.Instr{
				Op:   ir.OpCall,
				Func: "unicode.utf8.AppendRune",
				Args: nil,
			}},
			&ir.Return{},
		}},
	})
	if mod.LookupFunc("unicode.utf8.AppendRune") != nil {
		t.Fatal("expected no AppendRune before EnsureUTF8")
	}
	root, err := stdlib.CompilerModuleRoot()
	if err != nil {
		t.Fatal(err)
	}
	rewrite := filepath.Join(root, "std", "src")
	if err := EnsureUTF8(mod, rewrite); err != nil {
		t.Fatal(err)
	}
	if f := mod.LookupFunc("unicode.utf8.AppendRune"); f == nil {
		t.Fatal("expected unicode.utf8.AppendRune after EnsureUTF8")
	}
}
