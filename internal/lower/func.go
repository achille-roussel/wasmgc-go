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
	gotoLabel     string // forward goto target block (docs/design/02)
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

	// M5: closures, defer, recover.
	cells                map[types.Object]*ir.Var
	cellInits            []cellInit
	hasDefer             bool
	deferUsesList        bool
	deferInLoop          bool
	deferCount           int
	defersVar            *ir.Var
	mayPanic             bool
	needsRecoverPrologue bool
	allowVar             *ir.Var
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
		ty := t.Type
		if len(fl.pkg.subst) > 0 {
			ty = fl.pkg.subst.Apply(ty)
		}
		return ty
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
	objTy := obj.Type()
	if len(fl.pkg.subst) > 0 {
		objTy = fl.pkg.subst.Apply(objTy)
	}
	ty, err := fl.mapType(objTy)
	if err != nil {
		return nil, nil, fl.errorf(id, "%v", err)
	}
	// Uniquify IR names when the same Go identifier is redefined in nested
	// blocks (e.g. two `for w := …` loops in one function).
	name := id.Name
	base := name
	for i := 0; fl.nameTaken(name); i++ {
		name = base + strconv.Itoa(i+1)
	}
	v := &ir.Var{Name: name, Typ: ty}
	fl.vars[obj] = v
	fl.extraLocals = append(fl.extraLocals, v)
	// Non-defaultable refs (e.g. $go.string) must be initialized; Wasm GC
	// rejects an uninitialized (ref T) local even if every use is dominated
	// by an assignment (comma-ok type assert is the usual trigger).
	// Local [N]T arrays map to nullable array refs with length erased; allocate
	// array.new_default of Len so buf[:] / indexing is non-null (strconv.ftoa*).
	var init []ir.Stmt
	if a, ok := types.Unalias(objTy).Underlying().(*types.Array); ok && a.Len() > 0 {
		init = []ir.Stmt{fl.assignVar(v, &ir.Instr{
			Op:   ir.OpArrayNewDefault,
			Type: ty,
			Args: []ir.Expr{&ir.Const{Op: ir.OpI32Const, I64: a.Len()}},
		})}
	} else if needsZeroInit(ty) {
		z, err := fl.zeroValue(ty)
		if err == nil {
			init = []ir.Stmt{fl.assignVar(v, z)}
		}
	}
	return v, init, nil
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
	fn := fl.irName
	if fn == "" && fl.obj != nil {
		fn = fl.obj.Name()
	}
	return nil, fl.errorf(id, "variable %s has no IR local (in %s)", id.Name, fn)
}

func (fl *funcLower) lookupGlobal(id *ast.Ident) *ir.Global {
	obj := fl.info.Uses[id]
	if obj == nil {
		obj = fl.info.Defs[id]
	}
	if obj == nil {
		return nil
	}
	if fl.pkg.globals != nil {
		if g := fl.pkg.globals[obj]; g != nil {
			return g
		}
	}
	// Late mono emit (emitAllPendingMono) rebuilds pkgLower without the
	// origin package's globals map; resolve by qualified IR name.
	if v, ok := obj.(*types.Var); ok && v.Pkg() != nil {
		gname := pkgIRPrefix(v.Pkg()) + "." + v.Name()
		if g := fl.mod.LookupGlobal(gname); g != nil {
			return g
		}
	}
	return nil
}
