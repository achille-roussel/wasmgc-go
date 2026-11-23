package ir

import (
	"fmt"
	"strconv"
	"strings"
)

// Error is a parse or verify diagnostic with a source position.
type Error struct {
	Pos Pos
	Msg string
}

func (e *Error) Error() string {
	if e.Pos.IsValid() {
		return fmt.Sprintf("%s: %s", e.Pos, e.Msg)
	}
	return e.Msg
}

// Parse parses IR text into a Module. file is used in diagnostics.
func Parse(file, src string) (*Module, error) {
	p := &parser{
		lex:  newLexer(file, src),
		file: file,
	}
	p.next()
	m, err := p.parseFile()
	if err != nil {
		return nil, err
	}
	if err := resolveModule(m); err != nil {
		return nil, err
	}
	return m, nil
}

type parser struct {
	lex  *lexer
	file string
	tok  token
}

func (p *parser) next() {
	p.tok = p.lex.lex()
}

func (p *parser) errorf(pos Pos, format string, args ...any) error {
	return &Error{Pos: pos, Msg: fmt.Sprintf(format, args...)}
}

func (p *parser) expect(kind tokenKind) error {
	if p.tok.kind != kind {
		return p.errorf(p.tok.pos, "expected %s, got %s", tokName(kind), p.tok)
	}
	p.next()
	return nil
}

func (p *parser) got(lit string) bool {
	switch lit {
	case "{":
		if p.tok.kind == tokLBrace {
			p.next()
			return true
		}
	case "}":
		if p.tok.kind == tokRBrace {
			p.next()
			return true
		}
	case "(":
		if p.tok.kind == tokLParen {
			p.next()
			return true
		}
	case ")":
		if p.tok.kind == tokRParen {
			p.next()
			return true
		}
	case ",":
		if p.tok.kind == tokComma {
			p.next()
			return true
		}
	case ":":
		if p.tok.kind == tokColon {
			p.next()
			return true
		}
	case "=":
		if p.tok.kind == tokAssign {
			p.next()
			return true
		}
	default:
		if p.tok.kind == tokIdent && p.tok.lit == lit {
			p.next()
			return true
		}
	}
	return false
}

func (p *parser) peekIdent(lit string) bool {
	return p.tok.kind == tokIdent && p.tok.lit == lit
}

func (p *parser) parseFile() (*Module, error) {
	m := &Module{Filename: p.file}
	for p.tok.kind != tokEOF {
		if p.tok.kind == tokError {
			return nil, p.errorf(p.tok.pos, "%s", p.tok.lit)
		}
		switch {
		case p.got("low"):
			m.Low = true
		case p.got("type"):
			t, err := p.parseTypeDecl()
			if err != nil {
				return nil, err
			}
			m.Types = append(m.Types, t)
		case p.got("global"):
			g, err := p.parseGlobalDecl()
			if err != nil {
				return nil, err
			}
			m.Globals = append(m.Globals, g)
		case p.got("data"):
			d, err := p.parseDataDecl()
			if err != nil {
				return nil, err
			}
			m.Data = append(m.Data, d)
		case p.got("func"):
			f, err := p.parseFuncDecl()
			if err != nil {
				return nil, err
			}
			m.Funcs = append(m.Funcs, f)
		default:
			return nil, p.errorf(p.tok.pos, "expected declaration, got %s", p.tok)
		}
	}
	return m, nil
}

func (p *parser) parseTypeDecl() (*TypeDef, error) {
	pos := p.tok.pos
	name, err := p.parseName()
	if err != nil {
		return nil, err
	}
	t := &TypeDef{Name: name, Pos: pos}
	if p.got("sub") {
		super, err := p.parseName()
		if err != nil {
			return nil, err
		}
		t.Super = super
	}
	switch {
	case p.got("struct"):
		t.Kind = TypeStruct
		if err := p.expect(tokLBrace); err != nil {
			return nil, err
		}
		for !p.got("}") && p.tok.kind != tokEOF {
			mut := p.got("mut")
			fname, err := p.parseName()
			if err != nil {
				return nil, err
			}
			ft, err := p.parseType()
			if err != nil {
				return nil, err
			}
			t.Fields = append(t.Fields, Field{Name: fname, Type: ft, Mut: mut})
		}
	case p.got("array"):
		t.Kind = TypeArray
		t.ElemMut = p.got("mut")
		elem, err := p.parseType()
		if err != nil {
			return nil, err
		}
		t.Elem = elem
	case p.got("func"):
		t.Kind = TypeFunc
		if err := p.expect(tokLParen); err != nil {
			return nil, err
		}
		if p.tok.kind != tokRParen {
			for {
				ty, err := p.parseType()
				if err != nil {
					return nil, err
				}
				t.Params = append(t.Params, ty)
				if !p.got(",") {
					break
				}
				// allow trailing sense: if next is ), stop — but we require another type
			}
		}
		if err := p.expect(tokRParen); err != nil {
			return nil, err
		}
		// optional results
		for p.tok.kind == tokIdent || p.tok.kind == tokTemp {
			// results look like types; stop if next decl keyword
			if isDeclKeyword(p.tok.lit) {
				break
			}
			// Also stop if it looks like start of next thing that isn't a type
			ty, err := p.parseType()
			if err != nil {
				return nil, err
			}
			t.Results = append(t.Results, ty)
			if !p.got(",") {
				break
			}
		}
	default:
		return nil, p.errorf(p.tok.pos, "expected struct, array, or func type body")
	}
	return t, nil
}

func isDeclKeyword(s string) bool {
	switch s {
	case "type", "func", "global", "data", "low":
		return true
	default:
		return false
	}
}

func (p *parser) parseGlobalDecl() (*Global, error) {
	pos := p.tok.pos
	mut := p.got("mut")
	name, err := p.parseName()
	if err != nil {
		return nil, err
	}
	ty, err := p.parseType()
	if err != nil {
		return nil, err
	}
	g := &Global{Name: name, Typ: ty, Mut: mut, Pos: pos}
	if p.got("=") {
		init, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		g.Init = init
	}
	return g, nil
}

func (p *parser) parseDataDecl() (*Data, error) {
	pos := p.tok.pos
	name, err := p.parseName()
	if err != nil {
		return nil, err
	}
	if p.tok.kind != tokString {
		return nil, p.errorf(p.tok.pos, "expected string literal for data segment")
	}
	d := &Data{Name: name, Bytes: []byte(p.tok.lit), Pos: pos}
	p.next()
	return d, nil
}

func (p *parser) parseFuncDecl() (*Func, error) {
	pos := p.tok.pos
	export := ""
	if p.got("export") {
		if p.tok.kind != tokString {
			return nil, p.errorf(p.tok.pos, "expected export name string")
		}
		export = p.tok.lit
		p.next()
	}
	name, err := p.parseName()
	if err != nil {
		return nil, err
	}
	if err := p.expect(tokLParen); err != nil {
		return nil, err
	}
	var params []*Var
	if p.tok.kind != tokRParen {
		for {
			pname, err := p.parseName()
			if err != nil {
				return nil, err
			}
			pty, err := p.parseType()
			if err != nil {
				return nil, err
			}
			params = append(params, &Var{Name: pname, Typ: pty, Pos: p.tok.pos})
			if !p.got(",") {
				break
			}
		}
	}
	if err := p.expect(tokRParen); err != nil {
		return nil, err
	}
	var results []Type
	// Results precede the body '{'.
	for p.tok.kind != tokLBrace && p.tok.kind != tokEOF {
		ty, err := p.parseType()
		if err != nil {
			return nil, err
		}
		results = append(results, ty)
		if !p.got(",") {
			break
		}
	}
	body, err := p.parseRegion()
	if err != nil {
		return nil, err
	}
	f := &Func{
		Name:    name,
		Export:  export,
		Params:  params,
		Results: results,
		Body:    body,
		Pos:     pos,
	}
	collectLocals(f)
	return f, nil
}

func collectLocals(f *Func) {
	seen := map[string]bool{}
	for _, p := range f.Params {
		seen[p.Name] = true
	}
	var walk func(Stmt)
	walk = func(s Stmt) {
		switch s := s.(type) {
		case *VarDef:
			if !seen[s.Var.Name] {
				f.Locals = append(f.Locals, s.Var)
				seen[s.Var.Name] = true
			}
		case *Seq:
			for _, st := range s.Stmts {
				walk(st)
			}
		case *Block:
			walk(s.Body)
		case *Loop:
			walk(s.Body)
		case *If:
			walk(s.Then)
			if s.Else != nil {
				walk(s.Else)
			}
		case *Switch:
			for _, c := range s.Cases {
				walk(c.Body)
			}
			if s.Default != nil {
				walk(s.Default)
			}
		case *Try:
			walk(s.Body)
			walk(s.Handler)
		case *Dispatch:
			for _, a := range s.Arms {
				walk(a)
			}
		}
	}
	if f.Body != nil {
		walk(f.Body)
	}
}

func (p *parser) parseName() (string, error) {
	if p.tok.kind != tokIdent && p.tok.kind != tokTemp {
		return "", p.errorf(p.tok.pos, "expected name, got %s", p.tok)
	}
	name := p.tok.lit
	p.next()
	return name, nil
}

func (p *parser) parseType() (Type, error) {
	pos := p.tok.pos
	if p.tok.kind != tokIdent {
		return nil, p.errorf(pos, "expected type, got %s", p.tok)
	}
	lit := p.tok.lit
	p.next()

	if k, ok := ParseNumKind(lit); ok {
		return Num(k), nil
	}
	switch lit {
	case "exn":
		return TypeExn, nil
	case "extern":
		return TypeExtern, nil
	case "ref", "refnull":
		null := lit == "refnull"
		if p.tok.kind != tokIdent {
			return nil, p.errorf(p.tok.pos, "expected heap type after %s", lit)
		}
		h := p.tok.lit
		p.next()
		r := &Ref{Null: null}
		switch h {
		case "any":
			r.Heap = HeapAny
		case "eq":
			r.Heap = HeapEq
		case "i31":
			r.Heap = HeapI31
		case "struct":
			r.Heap = HeapStruct
		case "array":
			r.Heap = HeapArray
		case "none":
			r.Heap = HeapNone
		case "nofunc":
			r.Heap = HeapNoFunc
		case "noextern":
			r.Heap = HeapNoExtern
		case "func":
			r.Heap = HeapFunc
		default:
			r.Named = h
		}
		return r, nil
	}
	if strings.HasPrefix(lit, "go.") {
		return &High{Name: lit}, nil
	}
	// Named module type used as a type name in signatures is not itself
	// a value type; callers write ref/refnull. Still allow the name for
	// immediates — but parseType is for value types. Treat bare names as
	// High if they look like go-ish, else as a named ref target wrapped?
	// Per the grammar, type = id | ref forms. Bare id of a defined type
	// is only valid in type immediates. For param/result slots we require
	// ref/refnull/num. Accept bare named types as High-like NamedRef proxy:
	return &High{Name: lit}, nil
}

// parseTypeImm parses a type immediate for ops (may be a bare type name).
func (p *parser) parseTypeImm() (Type, error) {
	pos := p.tok.pos
	if p.tok.kind != tokIdent {
		return nil, p.errorf(pos, "expected type immediate, got %s", p.tok)
	}
	lit := p.tok.lit
	// Prefer full parseType for ref/num/etc.
	if lit == "ref" || lit == "refnull" || lit == "exn" || lit == "extern" ||
		strings.HasPrefix(lit, "go.") {
		return p.parseType()
	}
	if _, ok := ParseNumKind(lit); ok {
		return p.parseType()
	}
	// Bare name: leave as a Ref to that named type (non-null), used as
	// heap-type immediate. Represent as High for unresolved, resolved later.
	p.next()
	return &Ref{Named: lit}, nil
}

func (p *parser) parseRegion() (Region, error) {
	pos := p.tok.pos
	if err := p.expect(tokLBrace); err != nil {
		return nil, err
	}
	var stmts []Stmt
	for p.tok.kind != tokRBrace && p.tok.kind != tokEOF {
		s, err := p.parseStmt()
		if err != nil {
			return nil, err
		}
		stmts = append(stmts, s)
	}
	if err := p.expect(tokRBrace); err != nil {
		return nil, err
	}
	return &Seq{Stmts: stmts, Pos: pos}, nil
}

func (p *parser) parseStmt() (Stmt, error) {
	pos := p.tok.pos
	switch {
	case p.tok.kind == tokLBrace:
		return p.parseRegion()
	case p.got("var"):
		name, err := p.parseName()
		if err != nil {
			return nil, err
		}
		ty, err := p.parseType()
		if err != nil {
			return nil, err
		}
		return &VarDef{Var: &Var{Name: name, Typ: ty, Pos: pos}, Pos: pos}, nil
	case p.got("return"):
		r := &Return{Pos: pos}
		if p.tok.kind != tokRBrace && !isStmtStart(p.tok) {
			for {
				e, err := p.parseExpr()
				if err != nil {
					return nil, err
				}
				r.Results = append(r.Results, e)
				if !p.got(",") {
					break
				}
			}
		}
		return r, nil
	case p.got("br"):
		label, err := p.parseName()
		if err != nil {
			return nil, err
		}
		b := &Br{Label: label, Depth: -1, Pos: pos}
		for looksLikeExpr(p.tok) && !isStmtKeyword(p.tok) {
			e, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			b.Args = append(b.Args, e)
			if !p.got(",") {
				break
			}
		}
		return b, nil
	case p.got("br_if"):
		label, err := p.parseName()
		if err != nil {
			return nil, err
		}
		cond, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		b := &Br{Label: label, Cond: cond, Depth: -1, Pos: pos}
		for p.got(",") {
			e, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			b.Args = append(b.Args, e)
		}
		return b, nil
	case p.got("unreachable"):
		return &Unreachable{Pos: pos}, nil
	case p.got("if"):
		cond, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		then, err := p.parseRegion()
		if err != nil {
			return nil, err
		}
		var els Region
		if p.got("else") {
			els, err = p.parseRegion()
			if err != nil {
				return nil, err
			}
		}
		return &If{Cond: cond, Then: then, Else: els, Pos: pos}, nil
	case p.got("loop"):
		name, err := p.parseName()
		if err != nil {
			return nil, err
		}
		body, err := p.parseRegion()
		if err != nil {
			return nil, err
		}
		return &Loop{Name: name, Body: body, Pos: pos}, nil
	case p.got("block"):
		name, err := p.parseName()
		if err != nil {
			return nil, err
		}
		body, err := p.parseRegion()
		if err != nil {
			return nil, err
		}
		return &Block{Name: name, Body: body, Pos: pos}, nil
	case p.got("switch"):
		tag, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if err := p.expect(tokLBrace); err != nil {
			return nil, err
		}
		sw := &Switch{Tag: tag, Pos: pos}
		for !p.got("}") && p.tok.kind != tokEOF {
			switch {
			case p.got("case"):
				if p.tok.kind != tokInt {
					return nil, p.errorf(p.tok.pos, "expected case integer")
				}
				v, err := strconv.ParseInt(p.tok.lit, 0, 64)
				if err != nil {
					return nil, p.errorf(p.tok.pos, "bad case value: %v", err)
				}
				cpos := p.tok.pos
				p.next()
				if err := p.expect(tokColon); err != nil {
					return nil, err
				}
				body, err := p.parseRegion()
				if err != nil {
					return nil, err
				}
				sw.Cases = append(sw.Cases, Case{Value: v, Body: body, Pos: cpos})
			case p.got("default"):
				if err := p.expect(tokColon); err != nil {
					return nil, err
				}
				body, err := p.parseRegion()
				if err != nil {
					return nil, err
				}
				sw.Default = body
			default:
				return nil, p.errorf(p.tok.pos, "expected case or default")
			}
		}
		return sw, nil
	case p.got("try"):
		name := ""
		if p.tok.kind == tokIdent && p.tok.lit != "{" {
			// optional name before body
			if p.tok.lit != "catch" {
				n, err := p.parseName()
				if err != nil {
					return nil, err
				}
				name = n
			}
		}
		body, err := p.parseRegion()
		if err != nil {
			return nil, err
		}
		if !p.got("catch") {
			return nil, p.errorf(p.tok.pos, "expected catch")
		}
		tag, err := p.parseName()
		if err != nil {
			return nil, err
		}
		var params []*Var
		if p.tok.kind == tokLParen {
			p.next()
			for p.tok.kind != tokRParen {
				pname, err := p.parseName()
				if err != nil {
					return nil, err
				}
				pty, err := p.parseType()
				if err != nil {
					return nil, err
				}
				params = append(params, &Var{Name: pname, Typ: pty})
				if !p.got(",") {
					break
				}
			}
			if err := p.expect(tokRParen); err != nil {
				return nil, err
			}
		} else {
			// Legacy grammar: catch tag id id  → two bare names as param names with unknown types
			// Prefer modern form. If two idents follow, treat as (payload exn) of high types.
			if p.tok.kind == tokIdent && !p.peekIdent("{") {
				n1, _ := p.parseName()
				n2 := ""
				if p.tok.kind == tokIdent && !p.peekIdent("{") {
					n2, _ = p.parseName()
				}
				params = append(params, &Var{Name: n1, Typ: &High{Name: "catch.payload"}})
				if n2 != "" {
					params = append(params, &Var{Name: n2, Typ: TypeExn})
				}
			}
		}
		handler, err := p.parseRegion()
		if err != nil {
			return nil, err
		}
		return &Try{Name: name, Body: body, Tag: tag, Params: params, Handler: handler, Pos: pos}, nil
	case p.got("dispatch"):
		name, err := p.parseName()
		if err != nil {
			return nil, err
		}
		stateName := ""
		if p.got("(") {
			stateName, err = p.parseName()
			if err != nil {
				return nil, err
			}
			if err := p.expect(tokRParen); err != nil {
				return nil, err
			}
		}
		if err := p.expect(tokLBrace); err != nil {
			return nil, err
		}
		d := &Dispatch{Name: name, Pos: pos}
		if stateName != "" {
			d.State = &Var{Name: stateName, Typ: TypeI32}
		}
		for !p.got("}") && p.tok.kind != tokEOF {
			if !p.got("arm") {
				return nil, p.errorf(p.tok.pos, "expected arm")
			}
			if p.tok.kind != tokInt {
				return nil, p.errorf(p.tok.pos, "expected arm index")
			}
			idx, _ := strconv.Atoi(p.tok.lit)
			p.next()
			if err := p.expect(tokColon); err != nil {
				return nil, err
			}
			arm, err := p.parseRegion()
			if err != nil {
				return nil, err
			}
			for len(d.Arms) <= idx {
				d.Arms = append(d.Arms, nil)
			}
			d.Arms[idx] = arm
		}
		return d, nil
	default:
		// Assignment or expression statement.
		return p.parseAssignOrExprStmt(pos)
	}
}

func isStmtStart(t token) bool {
	if t.kind == tokLBrace {
		return true
	}
	return t.kind == tokIdent && isStmtKeyword(t)
}

func isStmtKeyword(t token) bool {
	if t.kind != tokIdent {
		return false
	}
	switch t.lit {
	case "var", "return", "br", "br_if", "unreachable", "if", "else",
		"loop", "block", "switch", "case", "default", "try", "catch",
		"dispatch", "arm", "type", "func", "global", "data", "low":
		return true
	default:
		return false
	}
}

func looksLikeExpr(t token) bool {
	switch t.kind {
	case tokIdent, tokTemp, tokInt, tokFloat, tokString, tokLParen:
		return true
	default:
		return false
	}
}

func (p *parser) parseAssignOrExprStmt(pos Pos) (Stmt, error) {
	// Collect one or more destinations or a leading expression.
	// Forms:
	//   name = expr
	//   %0, %1 = expr
	//   call foo ()
	//   i32.add a, b          (as expr stmt — rare)
	//   struct.set T f x v    (as expr stmt)

	// Lookahead: if we see name/temp (comma name/temp)* '=', it's an assign.
	if p.isAssignStart() {
		var lhs []Dest
		for {
			d, err := p.parseDest()
			if err != nil {
				return nil, err
			}
			lhs = append(lhs, d)
			if !p.got(",") {
				break
			}
		}
		if err := p.expect(tokAssign); err != nil {
			return nil, err
		}
		rhs, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		a := &Assign{Lhs: lhs, Rhs: rhs, Pos: pos}
		for i := range a.Lhs {
			if strings.HasPrefix(a.Lhs[i].Name, "%") {
				t := &Temp{Name: a.Lhs[i].Name, Def: a, Pos: a.Lhs[i].Pos}
				a.Lhs[i].Temp = t
			}
		}
		return a, nil
	}
	e, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	return &ExprStmt{X: e, Pos: pos}, nil
}

func (p *parser) isAssignStart() bool {
	// Peek without consuming: current must be ident or temp, then either
	// '=' or ',' … '='. We only have one token of lookahead, so scan
	// manually in the source… Simpler: if tok is temp, it's assign-like
	// when followed by '=' or ','. For idents, '=' means assign; if the
	// ident is an opcode/call, it's an expr.
	if p.tok.kind == tokTemp {
		return true
	}
	if p.tok.kind != tokIdent {
		return false
	}
	lit := p.tok.lit
	if lit == "call" || lit == "call_ref" {
		return false
	}
	if LookupOp(lit) != OpInvalid {
		return false
	}
	// Could be a var name. Check if next non-space is '=' or ','.
	return p.lookaheadAssign()
}

func (p *parser) lookaheadAssign() bool {
	// Save lexer state
	off, line, col := p.lex.off, p.lex.line, p.lex.col
	tok := p.tok
	p.next()
	ok := false
	switch p.tok.kind {
	case tokAssign:
		ok = true
	case tokComma:
		ok = true
	}
	// restore
	p.lex.off, p.lex.line, p.lex.col = off, line, col
	p.tok = tok
	return ok
}

func (p *parser) parseDest() (Dest, error) {
	pos := p.tok.pos
	switch p.tok.kind {
	case tokTemp, tokIdent:
		name := p.tok.lit
		p.next()
		return Dest{Name: name, Pos: pos}, nil
	default:
		return Dest{}, p.errorf(pos, "expected assignment destination")
	}
}

func (p *parser) parseExpr() (Expr, error) {
	pos := p.tok.pos
	switch p.tok.kind {
	case tokInt:
		v, err := strconv.ParseInt(p.tok.lit, 0, 64)
		if err != nil {
			return nil, p.errorf(pos, "bad integer: %v", err)
		}
		p.next()
		return &Const{Op: OpI32Const, I64: v, Pos: pos}, nil
	case tokFloat:
		v, err := strconv.ParseFloat(p.tok.lit, 64)
		if err != nil {
			return nil, p.errorf(pos, "bad float: %v", err)
		}
		p.next()
		return &Const{Op: OpF64Const, F64: v, Pos: pos}, nil
	case tokString:
		s := p.tok.lit
		p.next()
		return &Const{Op: OpStringLit, Str: s, Pos: pos}, nil
	case tokTemp:
		name := p.tok.lit
		p.next()
		return &Name{Raw: name, Pos: pos}, nil
	case tokIdent:
		lit := p.tok.lit
		if lit == "call" {
			return p.parseCall(pos)
		}
		if lit == "call_ref" {
			return p.parseCallRef(pos)
		}
		if op := LookupOp(lit); op != OpInvalid {
			return p.parseOpExpr(pos, op, lit)
		}
		// bare name
		p.next()
		return &Name{Raw: lit, Pos: pos}, nil
	default:
		return nil, p.errorf(pos, "expected expression, got %s", p.tok)
	}
}

func (p *parser) parseCall(pos Pos) (Expr, error) {
	p.next() // call
	name, err := p.parseName()
	if err != nil {
		return nil, err
	}
	if err := p.expect(tokLParen); err != nil {
		return nil, err
	}
	inst := &Instr{Op: OpCall, Func: name, Pos: pos}
	if p.tok.kind != tokRParen {
		for {
			e, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			inst.Args = append(inst.Args, e)
			if !p.got(",") {
				break
			}
		}
	}
	if err := p.expect(tokRParen); err != nil {
		return nil, err
	}
	return inst, nil
}

func (p *parser) parseCallRef(pos Pos) (Expr, error) {
	p.next() // call_ref
	ty, err := p.parseTypeImm()
	if err != nil {
		return nil, err
	}
	callees, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	inst := &Instr{Op: OpCallRef, Type: ty, Args: []Expr{callees}, Pos: pos}
	for looksLikeExpr(p.tok) && !isStmtKeyword(p.tok) {
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		inst.Args = append(inst.Args, e)
		if !p.got(",") {
			// call_ref args may be space-separated per informal grammar
			if !looksLikeExpr(p.tok) || isStmtKeyword(p.tok) {
				break
			}
		}
	}
	return inst, nil
}

func (p *parser) parseOpExpr(pos Pos, op Op, lit string) (Expr, error) {
	p.next() // consume op name
	inst := &Instr{Op: op, Pos: pos}

	// Ops with leading type / field immediates.
	switch op {
	case OpRefNull, OpRefCast, OpRefTest, OpRefAsNonNull,
		OpStructNew, OpStructNewDefault, OpArrayNew, OpArrayNewDefault,
		OpArrayNewFixed, OpConv, OpMakeSlice, OpMapMake, OpChanMake,
		OpIfaceMake, OpIfaceAssert, OpLiteral:
		ty, err := p.parseTypeImm()
		if err != nil {
			return nil, err
		}
		inst.Type = ty
		if op == OpMapMake {
			ty2, err := p.parseTypeImm()
			if err != nil {
				return nil, err
			}
			inst.Type2 = ty2
		}
	case OpStructGet, OpStructGetS, OpStructGetU, OpStructSet:
		ty, err := p.parseTypeImm()
		if err != nil {
			return nil, err
		}
		inst.Type = ty
		field, err := p.parseName()
		if err != nil {
			return nil, err
		}
		inst.Field = field
	case OpArrayGet, OpArrayGetS, OpArrayGetU, OpArraySet, OpArrayLen,
		OpArrayCopy, OpArrayFill, OpArrayNewData:
		ty, err := p.parseTypeImm()
		if err != nil {
			return nil, err
		}
		inst.Type = ty
		if op == OpArrayNewData {
			data, err := p.parseName()
			if err != nil {
				return nil, err
			}
			inst.Data = data
		}
	case OpRefFunc:
		name, err := p.parseName()
		if err != nil {
			return nil, err
		}
		inst.Func = name
		return inst, nil
	case OpGlobalGet, OpGlobalSet:
		name, err := p.parseName()
		if err != nil {
			return nil, err
		}
		inst.Global = name
		if op == OpGlobalGet {
			return inst, nil
		}
	case OpLocalGet, OpLocalSet, OpLocalTee:
		name, err := p.parseName()
		if err != nil {
			return nil, err
		}
		inst.Args = []Expr{&Name{Raw: name, Pos: pos}}
		if op == OpLocalGet {
			return inst, nil
		}
		// set/tee take a value
	case OpThrow:
		tag, err := p.parseName()
		if err != nil {
			return nil, err
		}
		inst.Func = tag // reuse Func field for tag name
	case OpBrOnNull, OpBrOnCast:
		label, err := p.parseName()
		if err != nil {
			return nil, err
		}
		inst.Label = label
		if op == OpBrOnCast {
			ty, err := p.parseTypeImm()
			if err != nil {
				return nil, err
			}
			inst.Type = ty
		}
	case OpStringLit:
		if p.tok.kind != tokString {
			return nil, p.errorf(p.tok.pos, "expected string literal")
		}
		inst.Str = p.tok.lit
		p.next()
		return inst, nil
	case OpAddr:
		// Optional field/elem immediate: "addr X s" or "addr elem a, i" or "addr s".
		if p.tok.kind == tokIdent {
			field := p.tok.lit
			p.next()
			if looksLikeExpr(p.tok) && !isStmtKeyword(p.tok) {
				inst.Field = field
				if field == "elem" {
					inst.Str = "elem"
				}
			} else {
				// Rewind: the ident was the sole argument.
				inst.Args = []Expr{&Name{Raw: field, Pos: pos}}
				return inst, nil
			}
		}
	case OpI32Const, OpI64Const:
		if p.tok.kind != tokInt && p.tok.kind != tokFloat {
			return nil, p.errorf(p.tok.pos, "expected integer immediate")
		}
		v, err := strconv.ParseInt(p.tok.lit, 0, 64)
		if err != nil {
			return nil, p.errorf(p.tok.pos, "bad integer: %v", err)
		}
		p.next()
		return &Const{Op: op, I64: v, Pos: pos}, nil
	case OpF32Const, OpF64Const:
		if p.tok.kind != tokFloat && p.tok.kind != tokInt {
			return nil, p.errorf(p.tok.pos, "expected float immediate")
		}
		v, err := strconv.ParseFloat(p.tok.lit, 64)
		if err != nil {
			return nil, p.errorf(p.tok.pos, "bad float: %v", err)
		}
		p.next()
		return &Const{Op: op, F64: v, Pos: pos}, nil
	}

	// Value operands: comma-separated exprs.
	// Fixed-arity ops stop after enough args; variable-arity (-1) reads
	// one comma-separated list and stops at the first non-expr.
	// Arity 0 (e.g. array.new_data) must not consume the next statement's
	// leading temp as an operand.
	arity := OpArity(op)
	if arity == 0 {
		return inst, nil
	}
	n := 0
	for {
		if arity >= 0 && n >= arity {
			break
		}
		if n > 0 {
			if p.tok.kind != tokComma {
				break
			}
			p.next()
		}
		if !looksLikeExpr(p.tok) || isStmtKeyword(p.tok) {
			if n > 0 {
				return nil, p.errorf(p.tok.pos, "expected expression after ','")
			}
			break
		}
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		inst.Args = append(inst.Args, e)
		n++
	}
	return inst, nil
}
