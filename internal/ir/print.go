package ir

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Format returns the canonical text form of m.
func Format(m *Module) string {
	var b bytes.Buffer
	f := &formatter{w: &b}
	f.module(m)
	return b.String()
}

// Fprint writes the canonical text form of m to w.
func Fprint(w io.Writer, m *Module) error {
	f := &formatter{w: w}
	f.module(m)
	return f.err
}

type formatter struct {
	w      io.Writer
	err    error
	indent int
}

func (f *formatter) write(s string) {
	if f.err != nil {
		return
	}
	_, f.err = io.WriteString(f.w, s)
}

func (f *formatter) printf(format string, args ...any) {
	if f.err != nil {
		return
	}
	_, f.err = fmt.Fprintf(f.w, format, args...)
}

func (f *formatter) newline() {
	f.write("\n")
	for i := 0; i < f.indent; i++ {
		f.write("  ")
	}
}

func (f *formatter) module(m *Module) {
	first := true
	emitBlank := func() {
		if !first {
			f.write("\n")
		}
		first = false
	}
	if m.Low {
		emitBlank()
		f.write("low\n")
	}
	for _, t := range m.Types {
		emitBlank()
		f.typeDef(t)
		f.write("\n")
	}
	for _, g := range m.Globals {
		emitBlank()
		f.global(g)
		f.write("\n")
	}
	for _, d := range m.Data {
		emitBlank()
		f.data(d)
		f.write("\n")
	}
	for _, fn := range m.Funcs {
		emitBlank()
		f.func_(fn)
		f.write("\n")
	}
}

func (f *formatter) typeDef(t *TypeDef) {
	f.printf("type %s", t.Name)
	if t.Super != "" {
		f.printf(" sub %s", t.Super)
	}
	switch t.Kind {
	case TypeStruct:
		f.write(" struct {")
		if len(t.Fields) == 0 {
			f.write("}")
			return
		}
		f.indent++
		for _, field := range t.Fields {
			f.newline()
			if field.Mut {
				f.write("mut ")
			}
			f.printf("%s %s", field.Name, TypeString(field.Type))
		}
		f.indent--
		f.newline()
		f.write("}")
	case TypeArray:
		f.write(" array ")
		if t.ElemMut {
			f.write("mut ")
		}
		f.write(TypeString(t.Elem))
	case TypeFunc:
		f.write(" func (")
		for i, p := range t.Params {
			if i > 0 {
				f.write(", ")
			}
			f.write(TypeString(p))
		}
		f.write(")")
		for i, r := range t.Results {
			if i == 0 {
				f.write(" ")
			} else {
				f.write(", ")
			}
			f.write(TypeString(r))
		}
	}
}

func (f *formatter) global(g *Global) {
	f.write("global ")
	if g.Mut {
		f.write("mut ")
	}
	f.printf("%s %s", g.Name, TypeString(g.Typ))
	if g.Init != nil {
		f.write(" = ")
		f.expr(g.Init)
	}
}

func (f *formatter) data(d *Data) {
	f.printf("data %s %s", d.Name, strconv.Quote(string(d.Bytes)))
}

func (f *formatter) func_(fn *Func) {
	f.write("func ")
	if fn.Export != "" {
		f.printf("export %s ", strconv.Quote(fn.Export))
	}
	f.printf("%s(", fn.Name)
	for i, p := range fn.Params {
		if i > 0 {
			f.write(", ")
		}
		f.printf("%s %s", p.Name, TypeString(p.Typ))
	}
	f.write(")")
	for i, r := range fn.Results {
		if i == 0 {
			f.write(" ")
		} else {
			f.write(", ")
		}
		f.write(TypeString(r))
	}
	f.write(" ")
	f.region(fn.Body)
}

func (f *formatter) region(r Region) {
	if r == nil {
		f.write("{}")
		return
	}
	switch r := r.(type) {
	case *Seq:
		f.write("{")
		if len(r.Stmts) == 0 {
			f.write("}")
			return
		}
		f.indent++
		for _, s := range r.Stmts {
			f.newline()
			f.stmt(s)
		}
		f.indent--
		f.newline()
		f.write("}")
	default:
		// Non-seq region as a statement body: wrap appearance via stmt.
		f.write("{")
		f.indent++
		f.newline()
		f.stmt(r)
		f.indent--
		f.newline()
		f.write("}")
	}
}

func (f *formatter) stmt(s Stmt) {
	switch s := s.(type) {
	case *Seq:
		f.region(s)
	case *VarDef:
		f.printf("var %s %s", s.Var.Name, TypeString(s.Var.Typ))
	case *Assign:
		for i, d := range s.Lhs {
			if i > 0 {
				f.write(", ")
			}
			f.write(d.String())
		}
		if len(s.Lhs) > 0 {
			f.write(" = ")
		}
		f.expr(s.Rhs)
	case *ExprStmt:
		f.expr(s.X)
	case *Return:
		f.write("return")
		for i, e := range s.Results {
			if i == 0 {
				f.write(" ")
			} else {
				f.write(", ")
			}
			f.expr(e)
		}
	case *Br:
		if s.Cond != nil {
			f.printf("br_if %s ", s.Label)
			f.expr(s.Cond)
		} else {
			f.printf("br %s", s.Label)
		}
		for i, a := range s.Args {
			if s.Cond == nil && i == 0 {
				f.write(" ")
			} else {
				f.write(", ")
			}
			f.expr(a)
		}
	case *Unreachable:
		f.write("unreachable")
	case *If:
		f.write("if ")
		f.expr(s.Cond)
		f.write(" ")
		f.region(s.Then)
		if s.Else != nil {
			f.write(" else ")
			f.region(s.Else)
		}
	case *Loop:
		f.printf("loop %s ", s.Name)
		f.region(s.Body)
	case *Block:
		f.printf("block %s ", s.Name)
		f.region(s.Body)
	case *Switch:
		f.write("switch ")
		f.expr(s.Tag)
		f.write(" {")
		f.indent++
		for _, c := range s.Cases {
			f.newline()
			f.printf("case %d: ", c.Value)
			f.region(c.Body)
		}
		if s.Default != nil {
			f.newline()
			f.write("default: ")
			f.region(s.Default)
		}
		f.indent--
		f.newline()
		f.write("}")
	case *Try:
		f.write("try")
		if s.Name != "" {
			f.printf(" %s", s.Name)
		}
		f.write(" ")
		f.region(s.Body)
		f.printf(" catch %s", s.Tag)
		if len(s.Params) > 0 {
			f.write(" (")
			for i, p := range s.Params {
				if i > 0 {
					f.write(", ")
				}
				f.printf("%s %s", p.Name, TypeString(p.Typ))
			}
			f.write(")")
		}
		f.write(" ")
		f.region(s.Handler)
	case *Dispatch:
		f.printf("dispatch %s", s.Name)
		if s.State != nil {
			f.printf(" (%s)", s.State.Name)
		}
		f.write(" {")
		f.indent++
		for i, arm := range s.Arms {
			if arm == nil {
				continue
			}
			f.newline()
			f.printf("arm %d: ", i)
			f.region(arm)
		}
		f.indent--
		f.newline()
		f.write("}")
	default:
		f.printf("/* unknown stmt %T */", s)
	}
}

func (f *formatter) expr(e Expr) {
	switch e := e.(type) {
	case *Const:
		switch e.Op {
		case OpI32Const, OpI64Const:
			// Bare integer; typed const ops print with opcode when not i32 default.
			if e.Op == OpI64Const {
				f.printf("i64.const %d", e.I64)
			} else {
				f.printf("%d", e.I64)
			}
		case OpF32Const:
			f.printf("f32.const %s", formatFloat(e.F64))
		case OpF64Const:
			f.printf("f64.const %s", formatFloat(e.F64))
		case OpStringLit:
			// High-level string lit uses the string op for clarity when
			// produced as Const from `string "…"`. Print as quoted if
			// standalone; callers wrap with string op when needed.
			f.write(strconv.Quote(e.Str))
		default:
			f.write("?")
		}
	case *Name:
		f.write(e.String())
	case *Instr:
		f.instr(e)
	default:
		f.printf("/* unknown expr %T */", e)
	}
}

func formatFloat(v float64) string {
	s := strconv.FormatFloat(v, 'g', -1, 64)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return s
}

// immTypeString formats a type immediate (heap type / defined type name),
// not a value type. ref/refnull wrappers are stripped to the underlying name.
func immTypeString(t Type) string {
	if t == nil {
		return "<nil>"
	}
	switch t := t.(type) {
	case *Ref:
		if t.Named != "" {
			return t.Named
		}
		if t.Func != nil {
			return t.Func.Name
		}
		return t.Heap.String()
	case *TypeDef:
		return t.Name
	default:
		return TypeString(t)
	}
}

func (f *formatter) instr(e *Instr) {
	op := e.Op
	f.write(op.String())

	switch op {
	case OpCall:
		f.printf(" %s(", e.Func)
		for i, a := range e.Args {
			if i > 0 {
				f.write(", ")
			}
			f.expr(a)
		}
		f.write(")")
		return
	case OpCallRef:
		f.write(" ")
		f.write(immTypeString(e.Type))
		for _, a := range e.Args {
			f.write(" ")
			f.expr(a)
		}
		return
	case OpRefNull, OpRefCast, OpRefTest, OpStructNew, OpStructNewDefault,
		OpArrayNew, OpArrayNewDefault, OpArrayNewFixed, OpConv, OpMakeSlice,
		OpChanMake, OpIfaceMake, OpIfaceAssert, OpLiteral:
		f.write(" ")
		f.write(immTypeString(e.Type))
	case OpMapMake:
		f.write(" ")
		f.write(immTypeString(e.Type))
		f.write(" ")
		f.write(immTypeString(e.Type2))
	case OpStructGet, OpStructGetS, OpStructGetU, OpStructSet:
		f.write(" ")
		f.write(immTypeString(e.Type))
		f.printf(" %s", e.Field)
	case OpArrayGet, OpArrayGetS, OpArrayGetU, OpArraySet, OpArrayLen,
		OpArrayCopy, OpArrayFill:
		f.write(" ")
		f.write(immTypeString(e.Type))
	case OpArrayNewData:
		f.write(" ")
		f.write(immTypeString(e.Type))
		f.printf(" %s", e.Data)
	case OpRefFunc:
		f.printf(" %s", e.Func)
		return
	case OpGlobalGet:
		f.printf(" %s", e.Global)
		return
	case OpGlobalSet:
		f.printf(" %s", e.Global)
	case OpLocalGet:
		if len(e.Args) == 1 {
			f.write(" ")
			f.expr(e.Args[0])
		}
		return
	case OpLocalSet, OpLocalTee:
		if len(e.Args) >= 1 {
			f.write(" ")
			f.expr(e.Args[0])
			rest := e.Args[1:]
			if len(rest) > 0 {
				f.write(" ")
				for i, a := range rest {
					if i > 0 {
						f.write(", ")
					}
					f.expr(a)
				}
			}
		}
		return
	case OpThrow:
		f.printf(" %s", e.Func)
	case OpBrOnNull:
		f.printf(" %s", e.Label)
	case OpBrOnCast:
		f.printf(" %s %s", e.Label, immTypeString(e.Type))
	case OpStringLit:
		f.write(" ")
		f.write(strconv.Quote(e.Str))
		return
	}

	if len(e.Args) > 0 {
		f.write(" ")
		for i, a := range e.Args {
			if i > 0 {
				f.write(", ")
			}
			f.expr(a)
		}
	}
}
