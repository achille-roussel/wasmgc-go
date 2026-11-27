package desc

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strconv"
	"strings"
	"sync"

	"github.com/achille-roussel/wasmgc-go/internal/mono"
)

// MonoOuter carries the enclosing generic instantiation while lowering a
// specialized function body. Nested function-local named types are implicitly
// parameterized by these args (typeparam/nested.go).
type MonoOuter struct {
	Args []types.Type
	// BodyPos/BodyEnd bound the FuncDecl body; only TypeNames declared inside
	// receive outer args (not locals from other functions used as type args).
	BodyPos token.Pos
	BodyEnd token.Pos
}

var (
	monoOuterMu sync.Mutex
	monoOuter   MonoOuter

	nestedDisplayMu sync.Mutex
	nestedDisplay   = map[string]string{} // TypeKey → reflect.Type.String form

	vargenMu sync.Mutex
	vargen   = map[*types.TypeName]int{}
)

// SetMonoOuter installs outer args for TypeKey during mono instance lowering.
// restore clears them.
func SetMonoOuter(o MonoOuter) (restore func()) {
	monoOuterMu.Lock()
	prev := monoOuter
	monoOuter = o
	monoOuterMu.Unlock()
	return func() {
		monoOuterMu.Lock()
		monoOuter = prev
		monoOuterMu.Unlock()
	}
}

// ClearNestedDisplay drops display registrations (start of a compile).
func ClearNestedDisplay() {
	nestedDisplayMu.Lock()
	nestedDisplay = map[string]string{}
	nestedDisplayMu.Unlock()
	vargenMu.Lock()
	vargen = map[*types.TypeName]int{}
	vargenMu.Unlock()
}

func currentMonoOuter() MonoOuter {
	monoOuterMu.Lock()
	defer monoOuterMu.Unlock()
	return monoOuter
}

func registerNestedDisplay(key, display string) {
	if key == "" || display == "" {
		return
	}
	nestedDisplayMu.Lock()
	nestedDisplay[key] = display
	nestedDisplayMu.Unlock()
}

func lookupNestedDisplay(key string) string {
	nestedDisplayMu.Lock()
	defer nestedDisplayMu.Unlock()
	return nestedDisplay[key]
}

// AssignVargen numbers function-local defined types in declaration order
// (gc unified IR ·N), matching typeparam/nested.out.
func AssignVargen(fset *token.FileSet, files []*ast.File, info *types.Info) {
	if info == nil {
		return
	}
	_ = fset
	vargenMu.Lock()
	defer vargenMu.Unlock()
	gen := 0
	for _, f := range files {
		if f == nil {
			continue
		}
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				if n == nil {
					return false
				}
				td, ok := n.(*ast.GenDecl)
				if !ok || td.Tok != token.TYPE {
					return true
				}
				for _, sp := range td.Specs {
					ts := sp.(*ast.TypeSpec)
					if ts.Assign != 0 {
						continue // alias
					}
					obj, _ := info.Defs[ts.Name].(*types.TypeName)
					if obj == nil {
						continue
					}
					gen++
					vargen[obj] = gen
				}
				return false
			})
		}
	}
}

func vargenOf(obj *types.TypeName) int {
	vargenMu.Lock()
	defer vargenMu.Unlock()
	return vargen[obj]
}

func declaredInMonoBody(obj *types.TypeName, o MonoOuter) bool {
	if obj == nil || !obj.Pos().IsValid() {
		return false
	}
	if !o.BodyPos.IsValid() || !o.BodyEnd.IsValid() {
		return false
	}
	p := obj.Pos()
	return o.BodyPos <= p && p < o.BodyEnd
}

// namedTypeKey is TypeKey for *types.Named, including mono outer args for
// locals declared in the active generic body. Keeps pkgIRPrefix so keys match
// historical TypeKey for imported packages.
func namedTypeKey(t *types.Named) string {
	pkg := ""
	if t.Obj().Pkg() != nil {
		pkg = pkgIRPrefix(t.Obj().Pkg()) + "."
	}
	o := currentMonoOuter()
	var outer []types.Type
	if len(o.Args) > 0 && mono.IsFunctionLocal(t.Obj()) && declaredInMonoBody(t.Obj(), o) {
		outer = o.Args
	}
	name := pkg + t.Obj().Name()
	if len(outer) > 0 {
		name += "$x" + strconv.Itoa(len(outer)) + "$"
		for i, a := range outer {
			if i > 0 {
				name += "$"
			}
			name += TypeKey(a)
		}
	}
	suffix := ""
	if targs := t.TypeArgs(); targs != nil && targs.Len() > 0 {
		var b strings.Builder
		ok := true
		for i := 0; i < targs.Len(); i++ {
			a := targs.At(i)
			if _, isTP := types.Unalias(a).(*types.TypeParam); isTP {
				ok = false
				break
			}
			if i > 0 {
				b.WriteByte('$')
			}
			b.WriteString(TypeKey(a))
		}
		if ok {
			suffix = "$" + b.String()
		}
	}
	key := name + suffix + mono.LocalTypeSuffix(t.Obj())
	if len(outer) > 0 {
		registerNestedDisplay(key, formatNestedDisplay(t, outer, false))
	}
	return key
}

func formatNestedDisplay(t *types.Named, outer []types.Type, asArg bool) string {
	if t == nil {
		return ""
	}
	pkg := "main"
	if t.Obj().Pkg() != nil && t.Obj().Pkg().Name() != "" {
		pkg = t.Obj().Pkg().Name()
	}
	var b strings.Builder
	b.WriteString(pkg)
	b.WriteByte('.')
	b.WriteString(t.Obj().Name())

	o := currentMonoOuter()
	useOuter := len(outer) > 0 && mono.IsFunctionLocal(t.Obj()) && !monoContainsTypeParam(outer)
	targs := namedTypeArgs(t)
	useInner := len(targs) > 0 && !monoContainsTypeParam(targs)
	if useOuter || useInner {
		b.WriteByte('[')
		n := 0
		write := func(a types.Type) {
			if n > 0 {
				b.WriteByte(';')
			}
			b.WriteString(formatDisplayArg(a, outer, o))
			n++
		}
		if useOuter {
			for _, a := range outer {
				write(a)
			}
		}
		if useInner {
			for _, a := range targs {
				write(a)
			}
		}
		b.WriteByte(']')
	}
	if asArg {
		if g := vargenOf(t.Obj()); g > 0 {
			fmt.Fprintf(&b, "·%d", g)
		}
	}
	return b.String()
}

func formatDisplayArg(a types.Type, outer []types.Type, o MonoOuter) string {
	a = types.Unalias(a)
	switch a := a.(type) {
	case *types.Named:
		asArg := mono.IsFunctionLocal(a.Obj())
		argOuter := []types.Type(nil)
		// Only locals declared in the active generic body inherit outer.
		if asArg && declaredInMonoBody(a.Obj(), o) {
			argOuter = outer
		}
		return formatNestedDisplay(a, argOuter, asArg)
	case *types.Basic:
		switch a.Kind() {
		case types.Byte:
			return "uint8"
		case types.Rune:
			return "int32"
		}
		return a.Name()
	default:
		s := a.String()
		if strings.HasPrefix(s, "gotestcase.") {
			return "main." + strings.TrimPrefix(s, "gotestcase.")
		}
		return s
	}
}

func namedTypeArgs(t *types.Named) []types.Type {
	if t == nil {
		return nil
	}
	ta := t.TypeArgs()
	if ta == nil || ta.Len() == 0 {
		return nil
	}
	out := make([]types.Type, ta.Len())
	for i := 0; i < ta.Len(); i++ {
		out[i] = ta.At(i)
	}
	return out
}

func monoContainsTypeParam(args []types.Type) bool {
	for _, a := range args {
		if _, ok := types.Unalias(a).(*types.TypeParam); ok {
			return true
		}
	}
	return false
}
