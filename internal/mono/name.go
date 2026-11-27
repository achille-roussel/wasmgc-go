package mono

import (
	"fmt"
	"go/types"
	"strings"
)

// TypeArgKey returns a stable, IR-lexer-safe spelling of t for specialized names.
func TypeArgKey(t types.Type) string {
	t = types.Unalias(t)
	switch t := t.(type) {
	case *types.Basic:
		// Match desc.TypeKey so itab wrappers and mono method names agree
		// for byte/rune aliases (typeparam/issue48049: fooer2$uint8.Foo).
		switch t.Kind() {
		case types.Byte:
			return "uint8"
		case types.Rune:
			return "int32"
		}
		return t.Name()
	case *types.TypeParam:
		return t.Obj().Name()
	case *types.Named:
		pkg := ""
		if t.Obj().Pkg() != nil {
			pkg = t.Obj().Pkg().Name() + "."
		}
		name := pkg + t.Obj().Name()
		if targs := t.TypeArgs(); targs != nil && targs.Len() > 0 {
			name += "$" + typeArgsKey(typeListSlice(targs))
		}
		// Distinguish function-local named types (F[local Int] ≠ F[pkg Int]
		// in typeparam/nested.go).
		name += LocalTypeSuffix(t.Obj())
		return name
	case *types.Pointer:
		return "*" + TypeArgKey(t.Elem())
	case *types.Slice:
		return "slice." + TypeArgKey(t.Elem())
	case *types.Array:
		return fmt.Sprintf("array.%d.%s", t.Len(), TypeArgKey(t.Elem()))
	case *types.Map:
		return "map." + TypeArgKey(t.Key()) + "." + TypeArgKey(t.Elem())
	case *types.Chan:
		return "chan." + TypeArgKey(t.Elem())
	case *types.Interface:
		if t.Empty() {
			return "any"
		}
		return "iface"
	case *types.Signature:
		return "func"
	default:
		s := strings.ReplaceAll(t.String(), " ", "_")
		s = strings.ReplaceAll(s, "[", "$")
		s = strings.ReplaceAll(s, "]", "")
		s = strings.ReplaceAll(s, ",", "$")
		return s
	}
}

func typeArgsKey(args []types.Type) string {
	var b strings.Builder
	for i, a := range args {
		if i > 0 {
			b.WriteByte('$')
		}
		b.WriteString(TypeArgKey(a))
	}
	return b.String()
}

// FuncIRName returns the IR name for a specialized function or method.
//
// Type-arg suffixes attach to the generic named receiver (List$int.Len) when
// the receiver type is generic. For methods that themselves have type
// parameters on a non-generic receiver ((*TypeMap).Set[T]), the suffix
// attaches to the method name (main.(*TypeMap).Set$main.res) so a type whose
// name ends with the method name (HashSet) cannot be misparsed (issue80976).
func FuncIRName(origin *types.Func, args []types.Type) string {
	sig := origin.Type().(*types.Signature)
	pkg := "main"
	if origin.Pkg() != nil {
		pkg = origin.Pkg().Name()
	}
	suffix := ""
	if len(args) > 0 {
		suffix = "$" + typeArgsKey(args)
	}
	if recv := sig.Recv(); recv != nil {
		rt := recv.Type()
		ptr := false
		if p, ok := types.Unalias(rt).(*types.Pointer); ok {
			ptr = true
			rt = p.Elem()
		}
		named, _ := types.Unalias(rt).(*types.Named)
		recvGeneric := named != nil && named.Origin().TypeParams().Len() > 0
		tname := "T"
		if named != nil {
			tname = named.Obj().Name()
			if recvGeneric {
				tname += suffix
			}
			tname += LocalTypeSuffix(named.Obj())
		} else if recvGeneric {
			tname += suffix
		}
		methodSuffix := ""
		if !recvGeneric {
			methodSuffix = suffix
		}
		if ptr {
			return pkg + ".(*" + tname + ")." + origin.Name() + methodSuffix
		}
		return pkg + "." + tname + "." + origin.Name() + methodSuffix
	}
	return pkg + "." + origin.Name() + suffix
}

// NamedIRSuffix returns the "$T1$T2" suffix for an instantiated named type.
func NamedIRSuffix(t *types.Named) string {
	if t == nil {
		return ""
	}
	args := typeListSlice(t.TypeArgs())
	if len(args) == 0 || argsContainTypeParam(args) {
		return ""
	}
	return "$" + typeArgsKey(args)
}

// LocalTypeSuffix disambiguates function-/block-local named types that would
// otherwise collide with a package-level type of the same name (e.g. fmt_test's
// package B vs TestNilDoesNotBecomeTyped's local empty B).
func LocalTypeSuffix(obj *types.TypeName) string {
	if obj == nil || obj.Pkg() == nil {
		return ""
	}
	parent := obj.Parent()
	if parent == nil || parent == obj.Pkg().Scope() {
		return ""
	}
	pos := obj.Pos()
	if !pos.IsValid() {
		return "_local"
	}
	// Use _pN (not $pN): '$' starts a new IR/Wasm name token, so
	// main.T$p1 would lex as main.T + $p1 and break eq/itab lookup.
	return fmt.Sprintf("_p%d", pos)
}

// NamedTypeKey returns a descriptor/Wasm type key distinguishing instantiations.
func NamedTypeKey(t *types.Named) string {
	return NamedTypeKeyOuter(t, nil)
}

// NamedTypeKeyOuter is NamedTypeKey plus optional outer (enclosing generic
// function) type arguments. Nested locals inside F[A] are implicitly
// parameterized by A (typeparam/nested.go): F[int].T[int] ≠ F[Int].T[int].
// outer is ignored when t is not function-local.
func NamedTypeKeyOuter(t *types.Named, outer []types.Type) string {
	pkg := ""
	if t.Obj().Pkg() != nil {
		pkg = t.Obj().Pkg().Name() + "."
	}
	name := pkg + t.Obj().Name()
	if len(outer) > 0 && LocalTypeSuffix(t.Obj()) != "" && !argsContainTypeParam(outer) {
		// $xN$ marks N outer args so keys stay parseable for display.
		name += fmt.Sprintf("$x%d$", len(outer)) + typeArgsKey(outer)
	}
	return name + NamedIRSuffix(t) + LocalTypeSuffix(t.Obj())
}

// NestedDisplayName formats a nested generic local like gc reflect.Type.String:
// main.T[int;int] with outer args before ';' and ·N on locals used as args.
// asArg includes the ·N vargen suffix on this type's own name.
//
// Local types declared in the same generic function share outer. When a local
// named appears as a type argument, sameBody (via NestedDisplayArg) decides
// whether that arg inherits outer.
func NestedDisplayName(t *types.Named, outer []types.Type, asArg bool, vargen func(*types.TypeName) int) string {
	return nestedDisplayName(t, outer, asArg, true, vargen)
}

func nestedDisplayName(t *types.Named, outer []types.Type, asArg, argsSameBody bool, vargen func(*types.TypeName) int) string {
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

	useOuter := len(outer) > 0 && LocalTypeSuffix(t.Obj()) != "" && !argsContainTypeParam(outer)
	targs := typeListSlice(t.TypeArgs())
	useInner := len(targs) > 0 && !argsContainTypeParam(targs)
	if useOuter || useInner {
		b.WriteByte('[')
		args := make([]types.Type, 0, len(outer)+len(targs))
		if useOuter {
			args = append(args, outer...)
		}
		if useInner {
			args = append(args, targs...)
		}
		for i, a := range args {
			if i > 0 {
				b.WriteByte(';')
			}
			b.WriteString(NestedDisplayArg(a, outer, argsSameBody, vargen))
		}
		b.WriteByte(']')
	}
	if asArg && vargen != nil {
		if g := vargen(t.Obj()); g > 0 {
			fmt.Fprintf(&b, "·%d", g)
		}
	}
	return b.String()
}

// NestedDisplayArg formats a type argument. sameBody reports whether a
// function-local named type was declared in the same generic body as the
// parent (shares outer implicits). Locals from other functions only get ·N.
func NestedDisplayArg(a types.Type, outer []types.Type, sameBody bool, vargen func(*types.TypeName) int) string {
	a = types.Unalias(a)
	switch a := a.(type) {
	case *types.Named:
		asArg := LocalTypeSuffix(a.Obj()) != ""
		argOuter := []types.Type(nil)
		if asArg && sameBody {
			argOuter = outer
		}
		return NestedDisplayName(a, argOuter, asArg, vargen)
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

// IsFunctionLocal reports whether obj is declared outside package scope.
func IsFunctionLocal(obj *types.TypeName) bool {
	return LocalTypeSuffix(obj) != ""
}
