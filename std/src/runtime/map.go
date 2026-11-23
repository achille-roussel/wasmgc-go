package runtime

// Shared map path (docs/design/04). Per-type hash/eq stay in generated code;
// these bodies own ctrl/keys/vals manipulation. M6 exit is tested with a WAT
// driver (testdata/m6_map.wat) using i32 and string keys before the lowerer
// emits map ops.

// mapaccess looks up key in h. Returns the value slot as any, or nil if absent.
// Scaffold: always misses until the open-addressed table is implemented.
func mapaccess(h *hmap, key any, hash, eq uintptr) any {
	_ = h
	_ = key
	_ = hash
	_ = eq
	return nil
}

// mapassign inserts or updates key → val in h.
func mapassign(h *hmap, key, val any, hash, eq uintptr) {
	_ = h
	_ = key
	_ = val
	_ = hash
	_ = eq
	throw("runtime: mapassign scaffold")
}

// mapdelete removes key from h.
func mapdelete(h *hmap, key any, hash, eq uintptr) {
	_ = h
	_ = key
	_ = hash
	_ = eq
}

// hmap is the shared map header (03-type-mapping.md). Fields are placeholders
// matching the Swiss-table-inspired open-addressed layout in 04.
type hmap struct {
	count  int32
	flags  int32
	B      uint8
	hash0  uint32
	buckets any
}
