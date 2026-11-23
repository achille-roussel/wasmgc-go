package ir

// Var is a function-scoped mutable slot. It becomes one Wasm local.
// Parameters are vars. A var's type does not change.
type Var struct {
	Name string
	Typ  Type
	Pos  Pos
}

func (v *Var) String() string {
	if v == nil {
		return "<nil-var>"
	}
	return v.Name
}

// Temp is a single-assignment name produced by an instruction (%0, %1, …).
// The verifier requires every use to be inside the same straight-line
// region that defined it. A temp does not survive a region boundary.
type Temp struct {
	Name string // including leading '%', e.g. "%0"
	Typ  Type   // set by verifier or explicit assignment
	Def  *Assign
	Pos  Pos
}

func (t *Temp) String() string {
	if t == nil {
		return "<nil-temp>"
	}
	return t.Name
}

// Name is a resolved reference to a Var or Temp in an expression.
// Exactly one of Var or Temp is non-nil after name resolution.
type Name struct {
	Raw  string // text as written
	Var  *Var
	Temp *Temp
	Pos  Pos
}

func (n *Name) exprNode()   {}
func (n *Name) GetPos() Pos { return n.Pos }
func (n *Name) String() string {
	if n.Var != nil {
		return n.Var.Name
	}
	if n.Temp != nil {
		return n.Temp.Name
	}
	return n.Raw
}
