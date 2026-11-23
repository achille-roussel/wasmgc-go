package lower

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strconv"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
)

type labelTargets struct {
	breakLabel    string
	continueLabel string // empty if not a loop
}

type funcLower struct {
	pkg  *pkgLower
	fset *token.FileSet
	info *types.Info
	mod  *ir.Module
	obj  *types.Func
	sig  *types.Signature

	irName string

	params       []*ir.Var
	results      []ir.Type
	namedResults []*ir.Var
	extraLocals  []*ir.Var
	vars         map[types.Object]*ir.Var

	tempSeq int
	varSeq  int
	labSeq  int

	// Innermost-first control targets for unlabeled break/continue.
	breakStack    []string
	continueStack []string
	labels        map[string]*labelTargets
}

func (fl *funcLower) errorf(n ast.Node, format string, args ...any) error {
	return fl.pkg.errorf(n, format, args...)
}

func (fl *funcLower) newTemp(typ ir.Type) *ir.Temp {
	name := "%" + strconv.Itoa(fl.tempSeq)
	fl.tempSeq++
	return &ir.Temp{Name: name, Typ: typ}
}

func (fl *funcLower) newVar(name string, typ ir.Type) *ir.Var {
	if name == "" || name == "_" {
		name = "v" + strconv.Itoa(fl.varSeq)
		fl.varSeq++
	}
	base := name
	for i := 0; ; i++ {
		if !fl.nameTaken(name) {
			break
		}
		name = base + strconv.Itoa(i+1)
	}
	v := &ir.Var{Name: name, Typ: typ}
	fl.extraLocals = append(fl.extraLocals, v)
	return v
}

func (fl *funcLower) nameTaken(name string) bool {
	for _, p := range fl.params {
		if p.Name == name {
			return true
		}
	}
	for _, v := range fl.extraLocals {
		if v.Name == name {
			return true
		}
	}
	for _, v := range fl.namedResults {
		if v.Name == name {
			return true
		}
	}
	return false
}

func (fl *funcLower) useVar(v *ir.Var) *ir.Name {
	return &ir.Name{Raw: v.Name, Var: v}
}

func (fl *funcLower) useTemp(t *ir.Temp) *ir.Name {
	return &ir.Name{Raw: t.Name, Temp: t}
}

func (fl *funcLower) i32const(v int64) *ir.Const {
	return &ir.Const{Op: ir.OpI32Const, I64: v}
}

func (fl *funcLower) i64const(v int64) *ir.Const {
	return &ir.Const{Op: ir.OpI64Const, I64: v}
}

func (fl *funcLower) f32const(v float64) *ir.Const {
	return &ir.Const{Op: ir.OpF32Const, F64: v}
}

func (fl *funcLower) f64const(v float64) *ir.Const {
	return &ir.Const{Op: ir.OpF64Const, F64: v}
}

func (fl *funcLower) assignTemp(t *ir.Temp, rhs ir.Expr) *ir.Assign {
	a := &ir.Assign{
		Lhs: []ir.Dest{{Temp: t, Name: t.Name}},
		Rhs: rhs,
	}
	t.Def = a
	return a
}

func (fl *funcLower) assignVar(v *ir.Var, rhs ir.Expr) *ir.Assign {
	return &ir.Assign{
		Lhs: []ir.Dest{{Var: v, Name: v.Name}},
		Rhs: rhs,
	}
}

func (fl *funcLower) freshLabel(prefix string) string {
	fl.labSeq++
	return fmt.Sprintf("$%s.%d", prefix, fl.labSeq)
}

func (fl *funcLower) typeOf(e ast.Expr) types.Type {
	if t, ok := fl.info.Types[e]; ok && t.Type != nil {
		return t.Type
	}
	return nil
}

func (fl *funcLower) defineLocal(id *ast.Ident) (*ir.Var, []ir.Stmt, error) {
	if id.Name == "_" {
		return nil, nil, nil
	}
	obj := fl.info.Defs[id]
	if obj == nil {
		obj = fl.info.Uses[id]
	}
	if obj == nil {
		return nil, nil, fl.errorf(id, "no object for %s", id.Name)
	}
	if v, ok := fl.vars[obj]; ok {
		return v, nil, nil
	}
	ty, err := fl.mapType(obj.Type())
	if err != nil {
		return nil, nil, fl.errorf(id, "%v", err)
	}
	v := &ir.Var{Name: id.Name, Typ: ty}
	fl.vars[obj] = v
	fl.extraLocals = append(fl.extraLocals, v)
	// VarDef is emitted once in lowerFunc preamble.
	return v, nil, nil
}

func (fl *funcLower) lookupVar(id *ast.Ident) (*ir.Var, error) {
	if id.Name == "_" {
		return nil, nil
	}
	obj := fl.info.Uses[id]
	if obj == nil {
		obj = fl.info.Defs[id]
	}
	if obj == nil {
		return nil, fl.errorf(id, "unknown identifier %s", id.Name)
	}
	if v, ok := fl.vars[obj]; ok {
		return v, nil
	}
	return nil, fl.errorf(id, "variable %s has no IR local", id.Name)
}

func (fl *funcLower) lookupGlobal(id *ast.Ident) *ir.Global {
	obj := fl.info.Uses[id]
	if obj == nil {
		obj = fl.info.Defs[id]
	}
	if obj == nil {
		return nil
	}
	return fl.pkg.globals[obj]
}
