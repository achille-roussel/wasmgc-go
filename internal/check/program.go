package check

import (
	"go/types"

	"github.com/achille-roussel/wasmgc-go/internal/load"
)

// Options controls program-level checks beyond go/types.
type Options struct {
	// VetFail promotes int64/uint64 → int/uint conversion warnings to errors.
	VetFail bool
	// CheckStd also runs diagnostics on standard-library packages in the program.
	// Default false: std is covered by `wasmgc-go std check`.
	CheckStd bool
}

// Check walks a loaded program and returns unsupported-unsafe errors plus
// int-conversion warnings. Unsafe findings are always SeverityError.
// Conversion findings are SeverityWarning unless VetFail is set.
func Check(prog *load.Program, opts Options) ([]Diagnostic, error) {
	if prog == nil {
		return nil, nil
	}
	var diags []Diagnostic
	for _, pkg := range prog.Packages {
		if pkg.Standard && !opts.CheckStd {
			continue
		}
		if pkg.Info == nil || len(pkg.Syntax) == 0 {
			continue
		}
		for _, d := range CheckUnsafe(prog.Fset, pkg.Info, pkg.Syntax) {
			d.Severity = SeverityError
			if d.Kind == "" {
				d.Kind = KindUnsafe
			}
			diags = append(diags, d)
		}
		for _, d := range CheckIntConvert(prog.Fset, pkg.Info, pkg.Syntax) {
			if opts.VetFail {
				d.Severity = SeverityError
			}
			diags = append(diags, d)
		}
		reach := ReachableFuncs([]*types.Package{pkg.Types}, pkg.Info, pkg.Syntax)
		for _, d := range CheckReflectConstructors(prog.Fset, pkg.Info, pkg.Syntax, reach) {
			d.Severity = SeverityError
			diags = append(diags, d)
		}
	}
	return diags, nil
}
