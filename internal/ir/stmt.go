package ir

// Stmt is a statement inside a region.
type Stmt interface {
	stmtNode()
	GetPos() Pos
}

// Assign binds one or more destinations to the results of Rhs.
// Destinations are vars (mutable) or temps (single-assignment).
// A bare call with no results is an Assign with empty Lhs.
type Assign struct {
	Lhs []Dest
	Rhs Expr
	Pos Pos
}

func (s *Assign) stmtNode()   {}
func (s *Assign) GetPos() Pos { return s.Pos }

// Dest is the left-hand side of an assignment: a var or a freshly
// defined temp. Field/index stores use Instr ops (struct.set, …) as Rhs
// of an Assign with empty Lhs, or as ExprStmt.
type Dest struct {
	Var  *Var
	Temp *Temp
	Name string // unresolved text; filled by parser before resolve
	Pos  Pos
}

func (d Dest) String() string {
	if d.Var != nil {
		return d.Var.Name
	}
	if d.Temp != nil {
		return d.Temp.Name
	}
	return d.Name
}

// ExprStmt is a statement that evaluates an expression for side effects
// (typically a call that returns nothing, or a store).
type ExprStmt struct {
	X   Expr
	Pos Pos
}

func (s *ExprStmt) stmtNode()   {}
func (s *ExprStmt) GetPos() Pos { return s.Pos }

// VarDef declares a mutable local in the enclosing function.
type VarDef struct {
	Var *Var
	Pos Pos
}

func (s *VarDef) stmtNode()   {}
func (s *VarDef) GetPos() Pos { return s.Pos }

// Return returns from the enclosing function with zero or more results.
type Return struct {
	Results []Expr
	Pos     Pos
}

func (s *Return) stmtNode()   {}
func (s *Return) GetPos() Pos { return s.Pos }

// Br branches to a named block or loop. Cond != nil means br_if.
// Depth is filled by codegen (or a pre-codegen pass); the verifier
// only checks that Label names an ancestor region.
type Br struct {
	Label string
	Cond  Expr   // nil ⇒ unconditional br
	Args  []Expr // block results, when the target block has results
	Depth int    // Wasm branch depth; -1 until computed
	Pos   Pos
}

func (s *Br) stmtNode()   {}
func (s *Br) GetPos() Pos { return s.Pos }

// Unreachable is the Wasm unreachable instruction.
type Unreachable struct {
	Pos Pos
}

func (s *Unreachable) stmtNode()   {}
func (s *Unreachable) GetPos() Pos { return s.Pos }
