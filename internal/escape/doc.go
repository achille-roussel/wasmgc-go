// Package escape rewrites non-escaping addresses and spills escaping
// scalar locals (docs/design/01-architecture.md, 03-type-mapping.md).
//
// Correctness does not depend on this pass alone: RuntimeOps can materialize
// a fat pointer for every remaining addr. Escape deletes spills when the
// address does not leave the function.
package escape
