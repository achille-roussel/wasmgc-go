// Package mono monomorphizes generic functions and methods into concrete
// IR instantiations (docs/design/01-architecture.md, decision 6 in 10).
//
// Every reachable instantiation becomes one IR function. Identical type-argument
// lists share one function. Generic methods are not itable entries: a call on a
// concrete receiver specializes; a call through an interface uses the ordinary
// (already concrete) method set.
//
// Dictionaries are not used. Naming uses '$' as the type-argument separator so
// IR identifiers stay lexer-safe: pkg.Max$int, pkg.Pair$int.Sum,
// pkg.(*Pair$int).Get.
package mono
