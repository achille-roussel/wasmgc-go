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
	gotoArm       int    // dispatch arm index when gotoDispatch is active; -1 if unused
}

type gotoDispatchState struct {
	Loop  string
	State *ir.Var
	Arms  map[string]int // label → arm index
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

	// gotoDispatch is set while lowering a Dispatch region for irreducible
	// goto (docs/design/02). Goto stores the arm index in State and brs to Loop.
	gotoDispatch *gotoDispatchState

	// M5: closures, defer, recover.
	cells     map[types.Object]*ir.Var
	cellInits []cellInit
	// plainDirty marks locals written via the plain var (not the cell) since
	// the last cell sync. buildClosure syncs local→cell only when dirty so
	// loop-carried closure writes to the cell are not wiped by a stale local.
	plainDirty map[types.Object]bool
	hasDefer      bool
	deferUsesList bool
	deferInLoop   bool
	deferCount           int
	defersVar            *ir.Var
	mayPanic             bool
	needsRecoverPrologue bool
	allowVar             *ir.Var

	// M10: range-over-func (iterators). rangeFrames is innermost-last.
	// rangeYield is true when this funcLower is a synthesized yield body.
	rangeFrames []*rangeFrame
	rangeYield  bool

	// rangeIterVars are for/range key/value objects of this function. Closures
	// capture them by value (Go 1.22 per-iteration vars) so each closure sees
	// the iteration that created it, not a shared cell updated later.
	rangeIterVars map[types.Object]bool
}

// rangeFrame holds the state machine and early-exit cells for one
// range-over-func loop (docs/design/02-ir.md). Follow-up: defer inside the
// range body should attach to the outer frame (runtime.deferrangefunc).
type rangeFrame struct {
	stateObj   types.Object
	retObj     types.Object   // shared with outermost; 1 = outer return pending
	resultObjs []types.Object // staging cells for outer return values
	resultTys  []ir.Type
	resultGo   []types.Type
	breakL     string
	contL      string
}

func (fl *funcLower) errorf(n ast.Node, format string, args ...any) error {
	return fl.pkg.errorf(n, format, args...)
}

func (fl *funcLower) newTemp(typ ir.Type) *ir.Temp {
	name := "%" + strconv.Itoa(fl.tempSeq)
	fl.tempSeq++
	return &ir.Temp{Name: name, Typ: typ}
}

// goTypeOfLocal returns the Go type for an IR local registered via defineLocal.
func (fl *funcLower) goTypeOfLocal(v *ir.Var) types.Type {
	if fl == nil || v == nil {
		return nil
	}
	for obj, lv := range fl.vars {
		if lv == v && obj != nil {
			ty := obj.Type()
			if fl.pkg != nil && len(fl.pkg.subst) > 0 {
				ty = fl.pkg.subst.Apply(ty)
			}
			return ty
		}
	}
	return nil
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
	// Len elements so buf[:] / indexing is non-null (strconv.ftoa*). Prefer
	// goZeroValue so [N]string uses array.new of "" (elems are non-defaultable).
	var init []ir.Stmt
	if a, ok := types.Unalias(objTy).Underlying().(*types.Array); ok && a.Len() > 0 {
		if z, ok := fl.goZeroValue(objTy, ty); ok {
			init = []ir.Stmt{fl.assignVar(v, z)}
		} else {
			init = []ir.Stmt{fl.assignVar(v, &ir.Instr{
				Op:   ir.OpArrayNewDefault,
				Type: ty,
				Args: []ir.Expr{&ir.Const{Op: ir.OpI32Const, I64: a.Len()}},
			})}
		}
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
	// Never resolve a local/param via the globals map or name fallback.
	if fl.vars != nil {
		if _, ok := fl.vars[obj]; ok {
			return nil
		}
	}
	if fl.pkg.globals != nil {
		if g := fl.pkg.globals[obj]; g != nil {
			return g
		}
	}
	// Late mono emit (emitAllPendingMono) rebuilds pkgLower without the
	// origin package's globals map; resolve by qualified IR name.
	// Only package-level vars (parent == package scope) — not params named
	// like maps.Equal's m1/m2 when tests also declare package vars m1/m2.
	if v, ok := obj.(*types.Var); ok && v.Pkg() != nil {
		if v.Parent() != v.Pkg().Scope() {
			return nil
		}
		gname := pkgIRPrefix(v.Pkg()) + "." + v.Name()
		if g := fl.mod.LookupGlobal(gname); g != nil {
			return g
		}
	}
	return nil
}
