package lower

import (
	"fmt"
	"go/types"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
)

// TypeStringRef is the IR type of a Go string value.
var typeString = &ir.Ref{Named: "$go.string"}

// mapType lowers a Go type to an IR value type (Wasm shape).
// int/uint/uintptr → i32 per StdSizes{4,4} and docs/design/03.
func mapType(t types.Type) (ir.Type, error) {
	if t == nil {
		return nil, fmt.Errorf("nil type")
	}
	t = types.Unalias(t)
	switch t := t.(type) {
	case *types.Basic:
		return mapBasic(t)
	case *types.Named:
		return mapType(t.Underlying())
	case *types.Alias:
		return mapType(t.Underlying())
	case *types.Signature:
		// Func values are not in M1/M2 as first-class values in params/results
		// beyond direct calls. Treat as unsupported if stored.
		return nil, fmt.Errorf("function type not supported as value in M1/M2: %s", t)
	case *types.Tuple:
		// multi-result handled at call site
		return nil, fmt.Errorf("tuple type not a single value: %s", t)
	default:
		return nil, fmt.Errorf("unsupported type in M1/M2: %s", t)
	}
}

func mapBasic(t *types.Basic) (ir.Type, error) {
	switch t.Kind() {
	case types.Bool,
		types.Int, types.Int8, types.Int16, types.Int32,
		types.Uint, types.Uint8, types.Uint16, types.Uint32, types.Uintptr:
		return ir.TypeI32, nil
	case types.Int64, types.Uint64:
		return ir.TypeI64, nil
	case types.Float32:
		return ir.TypeF32, nil
	case types.Float64:
		return ir.TypeF64, nil
	case types.String:
		return typeString, nil
	case types.UntypedBool:
		return ir.TypeI32, nil
	case types.UntypedInt, types.UntypedRune:
		// Default to i32; constant typing uses the context type.
		return ir.TypeI32, nil
	case types.UntypedFloat:
		return ir.TypeF64, nil
	case types.UntypedString:
		return typeString, nil
	case types.UntypedNil:
		return nil, fmt.Errorf("untyped nil not supported in M1/M2")
	case types.Complex64, types.Complex128, types.UntypedComplex:
		return nil, fmt.Errorf("complex numbers not supported in M1/M2")
	default:
		return nil, fmt.Errorf("unsupported basic type %s", t)
	}
}

func isUnsigned(t types.Type) bool {
	t = types.Unalias(t)
	if b, ok := t.Underlying().(*types.Basic); ok {
		switch b.Kind() {
		case types.Uint, types.Uint8, types.Uint16, types.Uint32, types.Uint64, types.Uintptr:
			return true
		}
	}
	return false
}

func isInteger(t types.Type) bool {
	t = types.Unalias(t)
	b, ok := t.Underlying().(*types.Basic)
	if !ok {
		return false
	}
	return b.Info()&types.IsInteger != 0
}

func isBoolean(t types.Type) bool {
	t = types.Unalias(t)
	b, ok := t.Underlying().(*types.Basic)
	if !ok {
		return false
	}
	return b.Info()&types.IsBoolean != 0
}

func isString(t types.Type) bool {
	t = types.Unalias(t)
	b, ok := t.Underlying().(*types.Basic)
	if !ok {
		return false
	}
	return b.Info()&types.IsString != 0
}

func isFloat(t types.Type) bool {
	t = types.Unalias(t)
	b, ok := t.Underlying().(*types.Basic)
	if !ok {
		return false
	}
	return b.Info()&types.IsFloat != 0
}

func basicKind(t types.Type) types.BasicKind {
	t = types.Unalias(t)
	if b, ok := t.Underlying().(*types.Basic); ok {
		return b.Kind()
	}
	return types.Invalid
}

func irNumWidth(t ir.Type) ir.NumKind {
	if n, ok := t.(*ir.NumType); ok {
		return n.Kind
	}
	return ir.I32
}
