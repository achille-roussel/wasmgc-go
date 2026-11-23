package codegen

import (
	"fmt"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
	"github.com/achille-roussel/wasmgc-go/internal/wasm"
)

// Options controls module emission.
type Options struct {
	// Names emits the Wasm name custom section when true.
	// Omitted for size builds and -names=false.
	Names bool

	// SynthPrintRuntime synthesizes runtime.print* when they are absent
	// from the IR (tests that skip rtlink). Prefer linking std/src/runtime
	// via internal/rtlink; body-less printstring is filled regardless.
	SynthPrintRuntime bool
}

// Module compiles a verified low IR module to Wasm bytes.
// m.Low must be true. ir.Verify is run to fill Br.Depth and Switch.Dense.
func Module(m *ir.Module, opts Options) ([]byte, error) {
	if m == nil {
		return nil, fmt.Errorf("codegen: nil module")
	}
	if !m.Low {
		return nil, fmt.Errorf("codegen: module is not low; runtime lowering required")
	}
	if err := ir.Verify(m); err != nil {
		return nil, fmt.Errorf("codegen: verify: %w", err)
	}
	g := newGen(m, opts)
	if err := g.build(); err != nil {
		return nil, err
	}
	return g.wm.Encode()
}

// TypeTable is the deterministic intern table of Wasm type indices.
// Stream G (descriptors) will consume the same indexing.
type TypeTable struct {
	// ByName maps IR type names (e.g. "$go.string") to Wasm type indices.
	ByName map[string]uint32
	// FuncSig maps a canonical signature key to a type index.
	FuncSig map[string]uint32
}

// PreludeIndices are fixed indices for the 03-type-mapping.md prelude.
// They are stable across modules so descriptors and codegen agree.
const (
	IdxGoObject   uint32 = 0
	IdxGoBytes    uint32 = 1
	IdxGoI16s     uint32 = 2
	IdxGoString   uint32 = 3
	IdxGoType     uint32 = 4
	IdxGoItab     uint32 = 5
	IdxGoIface    uint32 = 6
	IdxGoPanicObj uint32 = 7
)

func newGen(m *ir.Module, opts Options) *gen {
	return &gen{
		m:    m,
		opts: opts,
		wm:   wasm.NewModule(),
		types: &TypeTable{
			ByName:  make(map[string]uint32),
			FuncSig: make(map[string]uint32),
		},
		funcs:   make(map[string]uint32),
		globals: make(map[string]uint32),
		data:    make(map[string]uint32),
	}
}

type gen struct {
	m    *ir.Module
	opts Options
	wm   *wasm.Module
	types *TypeTable

	funcs   map[string]uint32 // IR func name → wasm func index
	globals map[string]uint32
	data    map[string]uint32

	// Host import function indices (filled before defined funcs).
	importWrite uint32
	hasWrite    bool
	hasMemory   bool
	memBumpIdx  uint32
	hasMemBump  bool

	// Synthesized print runtime function indices.
	printStringIdx uint32
	printIntIdx    uint32
	printNLIdx     uint32
	hasPrintStubs  bool

	needsStringTypes bool
	needsMemory      bool

	// Temporary print stubs when funcs are entirely missing (SynthPrintRuntime).
	stubNeedString bool
	stubNeedInt    bool
	stubNeedNL     bool
	// Fill empty IR bodies linked from std/src/runtime (stream F).
	fillPrintString bool
	fillPrintInt    bool
	fillPrintNL     bool
	// syscall.writeHost: []byte → linear bridge → go.runtime.write.
	fillWriteHost bool
	// runtime.getArgString: host getArg → linear memory → Go string.
	fillGetArgString bool
	// syscall/js string bridge fills (Go string ↔ JS string via linear memory).
	fillJSValueOfString bool
	fillJSValueToString bool

	// Slice/index panic stubs (legacy trap fill; M5 uses EH fills).
	panicIndexIdx       uint32
	panicSliceBIdx      uint32
	hasPanicStubs       bool
	stubNeedPanicIndex  bool
	stubNeedPanicSliceB bool
	fillPanicIndex      bool
	fillPanicSliceB     bool

	// M5 exception handling.
	hasEH            bool
	ehNeeded         bool
	panicTagIdx      uint32
	panicTagDeclared bool
	hasEHStubs       bool
	ehFill           map[string]bool
	hasExit          bool
	exitIdx          uint32

	// Host imports (park/ready/spawn/…): IR name → already imported.
	hostImported map[string]bool
}

func (g *gen) build() error {
	g.ehFill = map[string]bool{}
	g.hostImported = map[string]bool{}
	g.scanNeeds()
	g.scanEHNeeds() // before imports so exit can be imported
	g.emitPrelude()
	if err := g.emitProgramTypes(); err != nil {
		return err
	}
	if err := g.emitImports(); err != nil {
		return err
	}
	if g.needsMemory {
		g.emitMemory()
	}
	// Declare funcs before globals so class singletons can use ref.func in init.
	if err := g.declareFuncs(); err != nil {
		return err
	}
	// EH synth AddFunc's must land before print/panic stubs so code-section
	// order matches: emitFuncBodies (IR + EH fills) then print then panic.
	if err := g.ensureEH(); err != nil {
		return err
	}
	// Always consider print intrinsics: rtlink body-less decls, or synth when missing.
	if err := g.ensurePrintStubs(); err != nil {
		return err
	}
	g.ensurePanicStubs()
	g.addDeclarativeRefFuncs()
	if err := g.emitGlobals(); err != nil {
		return err
	}
	if err := g.emitData(); err != nil {
		return err
	}
	if err := g.emitFuncBodies(); err != nil {
		return err
	}
	if g.hasPrintStubs {
		if err := g.emitPrintStubBodies(); err != nil {
			return err
		}
	}
	if g.hasPanicStubs {
		g.emitPanicStubBodies()
	}
	g.emitExports()
	if g.opts.Names {
		g.emitNames()
	}
	return nil
}

func (g *gen) scanNeeds() {
	for _, f := range g.m.Funcs {
		walkExprs(f.Body, func(e ir.Expr) {
			switch e := e.(type) {
			case *ir.Instr:
				switch e.Op {
				case ir.OpArrayNewData, ir.OpStructNew, ir.OpStructGet, ir.OpStructGetU, ir.OpStructGetS,
					ir.OpArrayGetU, ir.OpArrayGet, ir.OpArrayGetS, ir.OpArrayLen, ir.OpArrayCopy,
					ir.OpArraySet, ir.OpArrayNew, ir.OpArrayNewDefault, ir.OpArrayNewFixed,
					ir.OpStructSet, ir.OpStructNewDefault, ir.OpRefNull, ir.OpRefCast:
					g.needsStringTypes = true
				case ir.OpMemoryCopy, ir.OpMemoryFill, ir.OpMemorySize, ir.OpMemoryGrow:
					g.needsMemory = true
				case ir.OpCall:
					switch e.Func {
					case "runtime.printstring", "runtime.printint", "runtime.printnl",
						"go.runtime.write", "syscall.writeHost", "runtime.getArgString",
						"runtime.getArg",
						"syscall.js.valueOfString", "syscall.js.valueToString",
						"syscall.js.jsValue", "syscall.js.jsStringCopy":
						g.needsMemory = true
						g.needsStringTypes = true
					}
				}
			case *ir.Const:
				if e.Op == ir.OpStringLit {
					g.needsStringTypes = true
					g.needsMemory = true
				}
			}
		})
		for _, t := range f.Results {
			if refNamed(t, "$go.string") {
				g.needsStringTypes = true
			}
		}
		for _, p := range f.Params {
			if refNamed(p.Typ, "$go.string") {
				g.needsStringTypes = true
			}
		}
	}
	for _, t := range g.m.Types {
		if t.Name == "$go.string" || t.Name == "$go.bytes" {
			g.needsStringTypes = true
		}
	}
	for _, d := range g.m.Data {
		if len(d.Bytes) > 0 {
			g.needsStringTypes = true
		}
	}
	// Body-less print/write/arg decls still need memory + string prelude.
	for _, name := range []string{
		"runtime.printstring", "runtime.printint", "runtime.printnl",
		"syscall.writeHost", "runtime.getArgString",
		"syscall.js.valueOfString", "syscall.js.valueToString",
	} {
		if f := g.m.LookupFunc(name); f != nil && emptyPrintBody(f) {
			g.needsMemory = true
			g.needsStringTypes = true
		}
	}
}

func refNamed(t ir.Type, name string) bool {
	r, ok := t.(*ir.Ref)
	return ok && r.Named == name
}

func walkExprs(r ir.Region, fn func(ir.Expr)) {
	if r == nil {
		return
	}
	var walkStmt func(ir.Stmt)
	var walkExpr func(ir.Expr)
	var walkRegion func(ir.Region)
	walkExpr = func(e ir.Expr) {
		if e == nil {
			return
		}
		fn(e)
		switch e := e.(type) {
		case *ir.Instr:
			for _, a := range e.Args {
				walkExpr(a)
			}
		}
	}
	walkStmt = func(s ir.Stmt) {
		switch s := s.(type) {
		case ir.Region:
			walkRegion(s)
		case *ir.Assign:
			walkExpr(s.Rhs)
		case *ir.ExprStmt:
			walkExpr(s.X)
		case *ir.Return:
			for _, e := range s.Results {
				walkExpr(e)
			}
		case *ir.Br:
			if s.Cond != nil {
				walkExpr(s.Cond)
			}
			for _, a := range s.Args {
				walkExpr(a)
			}
		}
	}
	walkRegion = func(r ir.Region) {
		switch r := r.(type) {
		case *ir.Seq:
			for _, s := range r.Stmts {
				walkStmt(s)
			}
		case *ir.Block:
			walkRegion(r.Body)
		case *ir.Loop:
			walkRegion(r.Body)
		case *ir.If:
			walkExpr(r.Cond)
			walkRegion(r.Then)
			if r.Else != nil {
				walkRegion(r.Else)
			}
		case *ir.Switch:
			walkExpr(r.Tag)
			for _, c := range r.Cases {
				walkRegion(c.Body)
			}
			if r.Default != nil {
				walkRegion(r.Default)
			}
		case *ir.Try:
			walkRegion(r.Body)
			walkRegion(r.Handler)
		case *ir.Dispatch:
			for _, a := range r.Arms {
				if a != nil {
					walkRegion(a)
				}
			}
		}
	}
	walkRegion(r)
}
