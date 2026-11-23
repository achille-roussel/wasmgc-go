// Package lower turns a type-checked Go program into wasmgc IR (internal/ir).
//
// Driver contract (internal/pipeline):
//
//	mod, err := lower.Package(prog.Packages, prog.Main)
//
// Packages must already be type-checked with
// go/types.StdSizes{WordSize: 4, MaxAlign: 4} and listed in dependency order.
// The result is Low IR: string literals expanded to data + array.new_data +
// struct.new, with export "run" main.run calling inits then main.main.
//
// M1/M2 coverage: empty or constant-return functions; scalars (int as i32);
// locals; assignments; if/else; for (all three forms) with break/continue and
// labels; integer switch; calls and multiple returns; string constants;
// print/println of strings and ints via runtime.printstring / printint / printnl.
//
// Compile / CompileHigh emit high IR for unit goldens. RuntimeOps is the
// high→low pass Package applies before returning. internal/mono and
// internal/escape are stubs until generics and pointers land.
package lower
