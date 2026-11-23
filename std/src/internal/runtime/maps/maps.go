// Package maps is a wasmgc rewrite stub. The compiler owns map representation.
package maps

// Map is a placeholder so dependents type-check. Bodies are not linked.
type Map struct {
	Used uint64
}

func NewEmpty() *Map { return &Map{} }
