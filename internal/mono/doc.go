// Package mono will monomorphize generic functions and methods into concrete
// IR instantiations (docs/design/01-architecture.md, decision in 10).
//
// For M1/M2 this package is a stub: programs without type parameters need no
// work, and Package rejects unsupported generics with a clear error.
package mono
