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
		tname := "T"
		if named != nil {
			tname = named.Obj().Name() + suffix + LocalTypeSuffix(named.Obj())
		} else {
			tname = tname + suffix
		}
		if ptr {
			return pkg + ".(*" + tname + ")." + origin.Name()
		}
		return pkg + "." + tname + "." + origin.Name()
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
	pkg := ""
	if t.Obj().Pkg() != nil {
		pkg = t.Obj().Pkg().Name() + "."
	}
	return pkg + t.Obj().Name() + NamedIRSuffix(t) + LocalTypeSuffix(t.Obj())
}
