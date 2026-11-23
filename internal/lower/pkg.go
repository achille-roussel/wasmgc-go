package lower

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strconv"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
)

type pkgLower struct {
	fset  *token.FileSet
	files []*ast.File
	pkg   *types.Package
	info  *types.Info
	mod   *ir.Module
	meta  *meta

	initCount int
}

func (l *pkgLower) lower() error {
	// Collect top-level funcs in source order across files.
	type fnDecl struct {
		file *ast.File
		decl *ast.FuncDecl
	}
	var funcs []fnDecl
	var inits []fnDecl
	for _, f := range l.files {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			if fd.Recv != nil {
				return l.errorf(fd, "methods not supported in M1/M2")
			}
			if fd.Name.Name == "init" && (fd.Type.Params == nil || len(fd.Type.Params.List) == 0) {
				inits = append(inits, fnDecl{f, fd})
				continue
			}
			funcs = append(funcs, fnDecl{f, fd})
		}
	}
	for _, id := range inits {
		name := l.initName()
		fn, err := l.lowerFunc(id.decl, name)
		if err != nil {
			return err
		}
		l.mod.Funcs = append(l.mod.Funcs, fn)
		l.meta.initOrder = append(l.meta.initOrder, name)
	}
	for _, fd := range funcs {
		name := l.funcName(fd.decl.Name.Name)
		if name == "main.main" {
			l.meta.hasMain = true
		}
		fn, err := l.lowerFunc(fd.decl, name)
		if err != nil {
			return err
		}
		l.mod.Funcs = append(l.mod.Funcs, fn)
	}
	return nil
}

func (l *pkgLower) initName() string {
	n := l.initCount
	l.initCount++
	pkg := l.pkg.Name()
	if n == 0 {
		// Prefer main.init when only one; still unique with .N for many.
		return pkg + ".init." + strconv.Itoa(n)
	}
	return pkg + ".init." + strconv.Itoa(n)
}

func (l *pkgLower) funcName(name string) string {
	return l.pkg.Name() + "." + name
}

func (l *pkgLower) lowerFunc(fd *ast.FuncDecl, irName string) (*ir.Func, error) {
	obj, _ := l.info.Defs[fd.Name].(*types.Func)
	if obj == nil {
		return nil, l.errorf(fd, "no type for function %s", fd.Name.Name)
	}
	sig, _ := obj.Type().(*types.Signature)
	if sig == nil {
		return nil, l.errorf(fd, "no signature for %s", fd.Name.Name)
	}
	if sig.TypeParams().Len() > 0 {
		return nil, l.errorf(fd, "generic function %s not supported (see internal/mono)", irName)
	}

	fl := &funcLower{
		pkg:    l,
		fset:   l.fset,
		info:   l.info,
		mod:    l.mod,
		obj:    obj,
		sig:    sig,
		irName: irName,
		vars:   map[types.Object]*ir.Var{},
		labels: map[string]*labelTargets{},
	}

	for i := 0; i < sig.Params().Len(); i++ {
		p := sig.Params().At(i)
		ty, err := mapType(p.Type())
		if err != nil {
			return nil, l.errorf(fd, "param %s: %v", p.Name(), err)
		}
		name := p.Name()
		if name == "" {
			name = "_" + strconv.Itoa(i)
		}
		v := &ir.Var{Name: name, Typ: ty}
		fl.params = append(fl.params, v)
		fl.vars[p] = v
	}
	for i := 0; i < sig.Results().Len(); i++ {
		r := sig.Results().At(i)
		ty, err := mapType(r.Type())
		if err != nil {
			return nil, l.errorf(fd, "result: %v", err)
		}
		fl.results = append(fl.results, ty)
		if r.Name() != "" {
			// Named results become vars.
			v := &ir.Var{Name: r.Name(), Typ: ty}
			fl.namedResults = append(fl.namedResults, v)
			fl.vars[r] = v
		}
	}

	body, err := fl.stmtBlock(fd.Body)
	if err != nil {
		return nil, err
	}
	seq, ok := body.(*ir.Seq)
	if !ok {
		seq = &ir.Seq{Stmts: []ir.Stmt{body}}
	}
	// Ensure void functions end with return when falling off the end.
	if len(fl.results) == 0 && !endsWithReturn(seq) {
		seq.Stmts = append(seq.Stmts, &ir.Return{})
	}
	// Prepend named-result and local var defs.
	var preamble []ir.Stmt
	for _, v := range fl.namedResults {
		preamble = append(preamble, &ir.VarDef{Var: v})
	}
	for _, v := range fl.extraLocals {
		preamble = append(preamble, &ir.VarDef{Var: v})
	}
	if len(preamble) > 0 {
		seq.Stmts = append(preamble, seq.Stmts...)
	}

	return &ir.Func{
		Name:    irName,
		Params:  fl.params,
		Results: fl.results,
		Body:    seq,
		Locals:  append(fl.namedResults, fl.extraLocals...),
	}, nil
}

func endsWithReturn(s *ir.Seq) bool {
	if s == nil || len(s.Stmts) == 0 {
		return false
	}
	_, ok := s.Stmts[len(s.Stmts)-1].(*ir.Return)
	return ok
}

func (l *pkgLower) errorf(n ast.Node, format string, args ...any) error {
	msg := fmt.Sprintf(format, args...)
	if l.fset != nil && n != nil {
		return fmt.Errorf("%s: lower: %s", l.fset.Position(n.Pos()), msg)
	}
	path := "main"
	if l.pkg != nil {
		path = l.pkg.Path()
	}
	return fmt.Errorf("%s: lower: %s", path, msg)
}

func (l *pkgLower) errorPos(pos token.Pos, format string, args ...any) error {
	msg := fmt.Sprintf(format, args...)
	if l.fset != nil && pos.IsValid() {
		return fmt.Errorf("%s: lower: %s", l.fset.Position(pos), msg)
	}
	path := "main"
	if l.pkg != nil {
		path = l.pkg.Path()
	}
	return fmt.Errorf("%s: lower: %s", path, msg)
}
