package rtlink

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/achille-roussel/wasmgc-go/internal/ir"
	"github.com/achille-roussel/wasmgc-go/internal/lower"
)

// EnsureSched links M7 scheduler / channel / select / sema / timer sources when
// the module uses go, channels, or parking imports. Optional pieces (select,
// timers, sema, rand) are linked only when the IR references them so a plain
// channel program does not pay for selectGo / timeSleep / mutex support.
func EnsureSched(mod *ir.Module, rewriteDir string) error {
	if mod == nil {
		return nil
	}
	need := scanSchedNeeds(mod)
	if !need.any {
		return nil
	}
	if mod.LookupFunc("runtime.go_") != nil && mod.LookupFunc("runtime.sched") != nil {
		fixSchedLink(mod)
		return nil
	}
	if rewriteDir == "" {
		return fmt.Errorf("rtlink: rewrite dir unset; cannot link runtime scheduler")
	}
	files := []string{
		"proc.go", "chan.go",
		// throw / fatal / panicObj; exit; print* for fatal.
		"fatal.go", "bridge.go", "print.go",
	}
	if need.select_ {
		files = append(files, "select.go", "rand.go")
	}
	if need.time {
		files = append(files, "time.go")
	}
	if need.sema {
		files = append(files, "sema.go")
	}
	var filenames []string
	var srcs []string
	for _, name := range files {
		path := filepath.Join(rewriteDir, "runtime", name)
		src, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("rtlink: read %s: %w", path, err)
		}
		filenames = append(filenames, path)
		srcs = append(srcs, string(src))
	}
	fset, astFiles, pkg, info, err := lower.TypeCheckFiles(filenames, srcs)
	if err != nil {
		return fmt.Errorf("rtlink: typecheck sched runtime: %w", err)
	}
	stripSchedFuncs(mod)
	stripPrintFuncs(mod) // re-link print with sched sources
	if err := lower.AppendFiles(mod, fset, astFiles, pkg, info); err != nil {
		return fmt.Errorf("rtlink: lower sched runtime: %w", err)
	}
	fixSchedLink(mod)
	return nil
}

func fixSchedLink(mod *ir.Module) {
	if f := mod.LookupFunc("runtime.sched"); f != nil {
		f.Export = "_sched"
	}
	renameGlobal(mod, "runtime.gp", "$g")
	// Prefer a single $g: if both existed, rename already folded uses.
	if g := mod.LookupGlobal("$g"); g != nil {
		g.Mut = true
		if g.Typ == nil {
			g.Typ = &ir.Ref{Null: true, Named: "$go.g"}
		}
	}
}

func renameGlobal(mod *ir.Module, from, to string) {
	var keep *ir.Global
	out := mod.Globals[:0]
	for _, g := range mod.Globals {
		if g.Name == from {
			g.Name = to
			if keep == nil {
				keep = g
				out = append(out, g)
			}
			continue
		}
		if g.Name == to {
			if keep == nil {
				keep = g
				out = append(out, g)
			}
			continue
		}
		out = append(out, g)
	}
	mod.Globals = out
	for _, f := range mod.Funcs {
		rewriteGlobalName(f.Body, from, to)
	}
}

func rewriteGlobalName(r ir.Region, from, to string) {
	if r == nil {
		return
	}
	walkExprs(r, func(e ir.Expr) {
		in, ok := e.(*ir.Instr)
		if !ok {
			return
		}
		if (in.Op == ir.OpGlobalGet || in.Op == ir.OpGlobalSet) && in.Global == from {
			in.Global = to
		}
	})
}

func walkExprs(r ir.Region, fn func(ir.Expr)) {
	if r == nil {
		return
	}
	switch r := r.(type) {
	case *ir.Seq:
		for _, s := range r.Stmts {
			walkStmtExprs(s, fn)
		}
	case *ir.Block:
		walkExprs(r.Body, fn)
	case *ir.Loop:
		walkExprs(r.Body, fn)
	case *ir.If:
		walkExprTree(r.Cond, fn)
		walkExprs(r.Then, fn)
		walkExprs(r.Else, fn)
	case *ir.Switch:
		walkExprTree(r.Tag, fn)
		for _, c := range r.Cases {
			walkExprs(c.Body, fn)
		}
		walkExprs(r.Default, fn)
	case *ir.Try:
		walkExprs(r.Body, fn)
		walkExprs(r.Handler, fn)
	}
}

func walkStmtExprs(s ir.Stmt, fn func(ir.Expr)) {
	switch s := s.(type) {
	case *ir.ExprStmt:
		walkExprTree(s.X, fn)
	case *ir.Assign:
		walkExprTree(s.Rhs, fn)
	case *ir.Return:
		for _, e := range s.Results {
			walkExprTree(e, fn)
		}
	case *ir.Br:
		if s.Cond != nil {
			walkExprTree(s.Cond, fn)
		}
	case ir.Region:
		walkExprs(s, fn)
	}
}

func walkExprTree(e ir.Expr, fn func(ir.Expr)) {
	if e == nil {
		return
	}
	fn(e)
	if in, ok := e.(*ir.Instr); ok {
		for _, a := range in.Args {
			walkExprTree(a, fn)
		}
	}
}

var schedSyms = []string{
	"runtime.go_", "runtime.newproc", "runtime.sched", "runtime.gopark",
	"runtime.goready", "runtime.makechan", "runtime.chanSend", "runtime.chanRecv",
	"runtime.chanClose", "runtime.selectGo", "runtime.defaultChanOps",
	"runtime.chanRecvNonblock", "runtime.trySelectReady",
	"runtime.timeSleep", "runtime.TimeSleep", "runtime.semacquire", "runtime.semrelease",
	"runtime.runtime_Semacquire", "runtime.runtime_Semrelease",
	"runtime.runtime_SemacquireMutex", "runtime.runtime_SemacquireRWMutex",
	"runtime.runtime_SemacquireRWMutexR", "runtime.runtime_SemacquireWaitGroup",
	"runtime.Semacquire", "runtime.Semrelease",
	"runtime.park", "runtime.ready", "runtime.spawn", "runtime.goschedImport",
	"runtime.scheduleTimer", "runtime.clearTimer", "runtime.throw", "runtime.fatal",
	"runtime.Gosched", "runtime.getg", "runtime.mintParkID",
	"runtime.fastrand", "runtime.fastrandn",
	"runtime.walltime", "runtime.nanotime", "runtime.Walltime", "runtime.Nanotime",
}

// schedNeed is the granular set of runtime pieces a module requires.
type schedNeed struct {
	any     bool
	select_ bool
	time    bool
	sema    bool
}

func scanSchedNeeds(mod *ir.Module) schedNeed {
	var need schedNeed
	for _, f := range mod.Funcs {
		walkCalls(f.Body, func(name string) {
			switch name {
			case "runtime.go_", "runtime.newproc", "runtime.chanSend", "runtime.chanRecv",
				"runtime.chanClose", "runtime.makechan", "runtime.defaultChanOps",
				"runtime.gopark", "runtime.goready", "runtime.Gosched",
				"runtime.park", "runtime.ready", "runtime.spawn",
				"runtime.trySelectReady":
				need.any = true
			case "runtime.selectGo", "runtime.chanRecvNonblock",
				"runtime.fastrand", "runtime.fastrandn":
				need.any = true
				need.select_ = true
			case "runtime.timeSleep", "runtime.TimeSleep",
				"runtime.scheduleTimer", "runtime.clearTimer",
				"runtime.Walltime", "runtime.walltime",
				"runtime.Nanotime", "runtime.nanotime":
				need.any = true
				need.time = true
			case "runtime.semacquire", "runtime.semrelease",
				"runtime.runtime_Semacquire", "runtime.runtime_Semrelease",
				"runtime.runtime_SemacquireMutex", "runtime.runtime_SemacquireRWMutex",
				"runtime.runtime_SemacquireRWMutexR", "runtime.runtime_SemacquireWaitGroup",
				"runtime.Semacquire", "runtime.Semrelease":
				need.any = true
				need.sema = true
			}
		})
		walkHighOps(f.Body, func(op ir.Op) {
			switch op {
			case ir.OpGo, ir.OpChanMake, ir.OpChanSend, ir.OpChanRecv, ir.OpChanClose:
				need.any = true
			case ir.OpSelect:
				need.any = true
				need.select_ = true
			}
		})
	}
	return need
}

func stripSchedFuncs(mod *ir.Module) {
	names := map[string]bool{}
	for _, s := range schedSyms {
		names[s] = true
	}
	out := mod.Funcs[:0]
	for _, f := range mod.Funcs {
		if names[f.Name] {
			continue
		}
		out = append(out, f)
	}
	mod.Funcs = out
}
