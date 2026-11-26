package ir

// Region is a structured control-flow node. A function body is a Region.
// Regions nest; there is no CFG of basic blocks.
type Region interface {
	Stmt
	regionNode()
}

// Seq is a straight-line sequence of statements. Temps defined in a Seq
// are visible to later statements in the same Seq and to nested regions
// entered after the definition (outer temps lower to function locals).
// Temps defined inside a nested region do not escape that region.
type Seq struct {
	Stmts []Stmt
	Pos   Pos
}

func (r *Seq) stmtNode()   {}
func (r *Seq) regionNode() {}
func (r *Seq) GetPos() Pos { return r.Pos }

// Block is a Wasm block. A br to its Name exits with Results types.
type Block struct {
	Name    string
	Params  []*Var // optional block params (Wasm block params)
	Results []Type
	Body    Region
	Pos     Pos
}

func (r *Block) stmtNode()   {}
func (r *Block) regionNode() {}
func (r *Block) GetPos() Pos { return r.Pos }

// Loop is a Wasm loop. A br to its Name restarts the loop.
type Loop struct {
	Name   string
	Params []*Var
	Body   Region
	Pos    Pos
}

func (r *Loop) stmtNode()   {}
func (r *Loop) regionNode() {}
func (r *Loop) GetPos() Pos { return r.Pos }

// If is a Wasm if/else. Cond must be i32.
type If struct {
	Cond Expr
	Then Region
	Else Region // may be nil (empty else)
	Pos  Pos
}

func (r *If) stmtNode()   {}
func (r *If) regionNode() {}
func (r *If) GetPos() Pos { return r.Pos }

// Case is one arm of a Switch.
type Case struct {
	Value int64
	Body  Region
	Pos   Pos
}

// Switch is a dense integer switch. Low form is br_table.
type Switch struct {
	Tag     Expr
	Cases   []Case
	Default Region // may be nil
	Dense   bool   // set by verifier / switch-build when br_table is OK
	Pos     Pos
}

func (r *Switch) stmtNode()   {}
func (r *Switch) regionNode() {}
func (r *Switch) GetPos() Pos { return r.Pos }

// Try is try_table with one catch_ref. Tag is the exception tag name
// (e.g. "$go.panic"). Handler params are the tag payload plus an exn.
type Try struct {
	Name    string // optional label for the try's body block
	Body    Region
	Tag     string
	Params  []*Var // handler params: payload…, exn
	Handler Region
	Pos     Pos
}

func (r *Try) stmtNode()   {}
func (r *Try) regionNode() {}
func (r *Try) GetPos() Pos { return r.Pos }

// Dispatch is the irreducible-goto fallback: a loop of br_table arms.
// State is the arm index var. Only the goto pass creates this; the
// verifier rejects mixing Dispatch with unclassified unstructured br.
type Dispatch struct {
	Name  string // loop name
	State *Var
	Arms  []Region
	Pos   Pos
}

func (r *Dispatch) stmtNode()   {}
func (r *Dispatch) regionNode() {}
func (r *Dispatch) GetPos() Pos { return r.Pos }
