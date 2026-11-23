package mono

import (
	"fmt"
	"go/types"
)

// Subst maps type parameters to concrete types for one instantiation.
type Subst map[*types.TypeParam]types.Type

// SubstForFunc builds a substitution for a generic function origin and args.
func SubstForFunc(origin *types.Func, args []types.Type) (Subst, error) {
	if origin == nil {
		return nil, fmt.Errorf("mono: nil origin func")
	}
	sig, ok := origin.Type().(*types.Signature)
	if !ok {
		return nil, fmt.Errorf("mono: %s is not a signature", origin.FullName())
	}
	tps := sig.TypeParams()
	if tps == nil || tps.Len() == 0 {
		return nil, fmt.Errorf("mono: %s is not generic", origin.FullName())
	}
	if len(args) != tps.Len() {
		return nil, fmt.Errorf("mono: %s: got %d type args, want %d", origin.FullName(), len(args), tps.Len())
	}
	s := make(Subst, tps.Len())
	for i := 0; i < tps.Len(); i++ {
		s[tps.At(i)] = args[i]
	}
	return s, nil
}

// SubstForMethod builds a substitution for a method of a generic named type.
// concreteRecv is the instantiated receiver type (e.g. Pair[int] or *Pair[int]).
func SubstForMethod(origin *types.Func, concreteRecv types.Type) (Subst, error) {
	if origin == nil {
		return nil, fmt.Errorf("mono: nil origin method")
	}
	sig, ok := origin.Type().(*types.Signature)
	if !ok || sig.Recv() == nil {
		return nil, fmt.Errorf("mono: %s is not a method", origin.FullName())
	}
	originRecv := stripPtr(sig.Recv().Type())
	originNamed, ok := types.Unalias(originRecv).(*types.Named)
	if !ok {
		return nil, fmt.Errorf("mono: %s: receiver is not named", origin.FullName())
	}
	conc := stripPtr(concreteRecv)
	concNamed, ok := types.Unalias(conc).(*types.Named)
	if !ok {
		return nil, fmt.Errorf("mono: concrete receiver is not named: %s", concreteRecv)
	}
	targs := concNamed.TypeArgs()
	if targs == nil || targs.Len() == 0 {
		return Subst{}, nil
	}
	s := make(Subst, targs.Len()*2)
	if otargs := originNamed.TypeArgs(); otargs != nil {
		for i := 0; i < otargs.Len() && i < targs.Len(); i++ {
			if tp, ok := otargs.At(i).(*types.TypeParam); ok {
				s[tp] = targs.At(i)
			}
		}
	}
	if tps := originNamed.Origin().TypeParams(); tps != nil {
		for i := 0; i < tps.Len() && i < targs.Len(); i++ {
			s[tps.At(i)] = targs.At(i)
		}
	}
	return s, nil
}

// Apply returns t with type parameters replaced.
func (s Subst) Apply(t types.Type) types.Type {
	if t == nil || len(s) == 0 {
		return t
	}
	return s.apply(t)
}

func (s Subst) apply(t types.Type) types.Type {
	t = types.Unalias(t)
	switch t := t.(type) {
	case *types.TypeParam:
		if r, ok := s[t]; ok {
			return r
		}
		return t
	case *types.Basic:
		return t
	case *types.Named:
		if targs := t.TypeArgs(); targs != nil && targs.Len() > 0 {
			newArgs := make([]types.Type, targs.Len())
			changed := false
			for i := 0; i < targs.Len(); i++ {
				newArgs[i] = s.apply(targs.At(i))
				if newArgs[i] != targs.At(i) {
					changed = true
				}
			}
			if changed {
				inst, err := types.Instantiate(nil, t.Origin(), newArgs, false)
				if err == nil {
					return inst
				}
			}
		}
		return t
	case *types.Pointer:
		elem := s.apply(t.Elem())
		if elem == t.Elem() {
			return t
		}
		return types.NewPointer(elem)
	case *types.Slice:
		elem := s.apply(t.Elem())
		if elem == t.Elem() {
			return t
		}
		return types.NewSlice(elem)
	case *types.Array:
		elem := s.apply(t.Elem())
		if elem == t.Elem() {
			return t
		}
		return types.NewArray(elem, t.Len())
	case *types.Map:
		k := s.apply(t.Key())
		v := s.apply(t.Elem())
		if k == t.Key() && v == t.Elem() {
			return t
		}
		return types.NewMap(k, v)
	case *types.Chan:
		elem := s.apply(t.Elem())
		if elem == t.Elem() {
			return t
		}
		return types.NewChan(t.Dir(), elem)
	case *types.Signature:
		return s.applySignature(t)
	case *types.Struct:
		return s.applyStruct(t)
	case *types.Interface:
		return t
	case *types.Tuple:
		return s.applyTuple(t)
	default:
		return t
	}
}

func (s Subst) applySignature(sig *types.Signature) types.Type {
	var recv *types.Var
	if r := sig.Recv(); r != nil {
		recv = types.NewVar(r.Pos(), r.Pkg(), r.Name(), s.apply(r.Type()))
	}
	params := s.applyTuple(sig.Params()).(*types.Tuple)
	results := s.applyTuple(sig.Results()).(*types.Tuple)
	return types.NewSignatureType(recv, nil, nil, params, results, sig.Variadic())
}

func (s Subst) applyStruct(st *types.Struct) types.Type {
	fields := make([]*types.Var, st.NumFields())
	tags := make([]string, st.NumFields())
	changed := false
	for i := 0; i < st.NumFields(); i++ {
		f := st.Field(i)
		ft := s.apply(f.Type())
		if ft != f.Type() {
			changed = true
			fields[i] = types.NewField(f.Pos(), f.Pkg(), f.Name(), ft, f.Embedded())
		} else {
			fields[i] = f
		}
		tags[i] = st.Tag(i)
	}
	if !changed {
		return st
	}
	return types.NewStruct(fields, tags)
}

func (s Subst) applyTuple(tup *types.Tuple) types.Type {
	if tup == nil {
		return (*types.Tuple)(nil)
	}
	vars := make([]*types.Var, tup.Len())
	changed := false
	for i := 0; i < tup.Len(); i++ {
		v := tup.At(i)
		vt := s.apply(v.Type())
		if vt != v.Type() {
			changed = true
			vars[i] = types.NewVar(v.Pos(), v.Pkg(), v.Name(), vt)
		} else {
			vars[i] = v
		}
	}
	if !changed {
		return tup
	}
	return types.NewTuple(vars...)
}

// ContainsTypeParam reports whether t still mentions a type parameter.
func ContainsTypeParam(t types.Type) bool {
	if t == nil {
		return false
	}
	found := false
	var walk func(types.Type)
	walk = func(t types.Type) {
		if found || t == nil {
			return
		}
		t = types.Unalias(t)
		switch t := t.(type) {
		case *types.TypeParam:
			found = true
		case *types.Named:
			if targs := t.TypeArgs(); targs != nil {
				for i := 0; i < targs.Len(); i++ {
					walk(targs.At(i))
				}
			}
		case *types.Pointer:
			walk(t.Elem())
		case *types.Slice:
			walk(t.Elem())
		case *types.Array:
			walk(t.Elem())
		case *types.Map:
			walk(t.Key())
			walk(t.Elem())
		case *types.Chan:
			walk(t.Elem())
		case *types.Signature:
			if r := t.Recv(); r != nil {
				walk(r.Type())
			}
			if p := t.Params(); p != nil {
				for i := 0; i < p.Len(); i++ {
					walk(p.At(i).Type())
				}
			}
			if r := t.Results(); r != nil {
				for i := 0; i < r.Len(); i++ {
					walk(r.At(i).Type())
				}
			}
		case *types.Struct:
			for i := 0; i < t.NumFields(); i++ {
				walk(t.Field(i).Type())
			}
		case *types.Tuple:
			for i := 0; i < t.Len(); i++ {
				walk(t.At(i).Type())
			}
		}
	}
	walk(t)
	return found
}

func stripPtr(t types.Type) types.Type {
	t = types.Unalias(t)
	if p, ok := t.(*types.Pointer); ok {
		return types.Unalias(p.Elem())
	}
	return t
}
