package runtime

// Shared open-addressed map (docs/design/03, 04). Per-type hash is computed
// here from the boxed key; equality uses Go's interface == (NaN, ±0, structs).
// Ctrl: 0 empty, 1 full, 2 tombstone. Probe is linear from hash&mask.
// Load factor 7/8 → grow, dropping tombstones. First insert allocates 8 slots.
//
// Concurrent map write detection: writing is set for the duration of
// assign/delete/clear. A nested or overlapping write throws. Same idea as
// upstream hashWriting; wasmgc is single-activation so this mainly catches
// re-entrant writes from within a range callback or after a park.

const (
	ctrlEmpty     uint8 = 0
	ctrlFull      uint8 = 1
	ctrlTombstone uint8 = 2

	mapMinCap    int32 = 8
	mapLoadNum   int32 = 7
	mapLoadDen   int32 = 8
	nanHashConst uint32 = 0x7ff80000
)

// hmap is $go.hmap (03). Keys and vals are boxed as any; typed arrays are a
// later size win. used is field 0 so len can be a struct.get.
type hmap struct {
	used    int32
	seed    uint32
	mask    int32 // cap-1; 0 when table is nil
	writing int32 // 1 while a write is in progress
	ctrl    []uint8
	keys    []any
	vals    []any
}

// mapIter holds a snapshot of the table arrays so growth during range is safe.
type mapIter struct {
	keys  []any
	vals  []any
	ctrl  []uint8
	start int32
	i     int32 // steps taken
	n     int32 // table length
}

// makemap allocates a map. hint 0 leaves the table nil until first insert.
func makemap(hint int32) *hmap {
	h := &hmap{seed: fastrand()}
	if hint > 0 {
		cap := mapCapForHint(hint)
		h.growTo(cap)
	}
	return h
}

func mapCapForHint(hint int32) int32 {
	// next power of two above hint/0.875
	need := (hint*mapLoadDen + mapLoadNum - 1) / mapLoadNum
	if need < mapMinCap {
		need = mapMinCap
	}
	cap := mapMinCap
	for cap < need {
		cap <<= 1
	}
	return cap
}

func (h *hmap) growTo(newCap int32) {
	oldCtrl := h.ctrl
	oldKeys := h.keys
	oldVals := h.vals
	oldMask := h.mask
	h.ctrl = make([]uint8, newCap)
	h.keys = make([]any, newCap)
	h.vals = make([]any, newCap)
	h.mask = newCap - 1
	h.used = 0
	if oldCtrl == nil {
		return
	}
	oldCap := oldMask + 1
	for i := int32(0); i < oldCap; i++ {
		if oldCtrl[i] != ctrlFull {
			continue
		}
		k := oldKeys[i]
		v := oldVals[i]
		hash := mapKeyHash(k, h.seed)
		h.insertNew(k, v, hash)
	}
}

func (h *hmap) insertNew(key, val any, hash uint32) {
	mask := uint32(h.mask)
	i := hash & mask
	n := h.mask + 1
	for step := int32(0); step < n; step++ {
		if h.ctrl[i] != ctrlFull {
			h.ctrl[i] = ctrlFull
			h.keys[i] = key
			h.vals[i] = val
			h.used++
			return
		}
		i = (i + 1) & mask
	}
}

func (h *hmap) maybeGrow() {
	cap := h.mask + 1
	if cap == 0 {
		h.growTo(mapMinCap)
		return
	}
	// Count full+tombstone against load; grow when full slots would exceed 7/8.
	if (h.used+1)*mapLoadDen > cap*mapLoadNum {
		h.growTo(cap << 1)
	}
}

// mapAccess looks up key. Nil map → (nil, false). Uncomparable keys panic
// even on nil/empty maps (same as gc): hash before the empty short-circuit.
func mapAccess(h *hmap, key any) (any, bool) {
	if h == nil {
		mapKeyHash(key, 0)
		return nil, false
	}
	mapCheckWrite(h)
	if h.ctrl == nil {
		mapKeyHash(key, h.seed)
		return nil, false
	}
	hash := mapKeyHash(key, h.seed)
	mask := uint32(h.mask)
	i := hash & mask
	n := h.mask + 1
	for step := int32(0); step < n; step++ {
		c := h.ctrl[i]
		if c == ctrlEmpty {
			return nil, false
		}
		if c == ctrlFull && h.keys[i] == key {
			return h.vals[i], true
		}
		i = (i + 1) & mask
	}
	return nil, false
}

func mapCheckWrite(h *hmap) {
	if h != nil && h.writing != 0 {
		throw("concurrent map read and map write")
	}
}

// mapAssign inserts or updates. Nil map panics with the gc message.
func mapAssign(h *hmap, key, val any) {
	if h == nil {
		throw("assignment to entry in nil map")
	}
	if h.writing != 0 {
		throw("concurrent map writes")
	}
	h.writing = 1
	for attempt := 0; attempt < 2; attempt++ {
		h.maybeGrow()
		hash := mapKeyHash(key, h.seed)
		mask := uint32(h.mask)
		i := hash & mask
		var tomb int32 = -1
		n := h.mask + 1
		placed := false
		for step := int32(0); step < n; step++ {
			c := h.ctrl[i]
			if c == ctrlEmpty {
				if tomb >= 0 {
					i = uint32(tomb)
				}
				h.ctrl[i] = ctrlFull
				h.keys[i] = key
				h.vals[i] = val
				h.used++
				placed = true
				break
			}
			if c == ctrlTombstone {
				if tomb < 0 {
					tomb = int32(i)
				}
			} else if h.keys[i] == key {
				h.vals[i] = val
				placed = true
				break
			}
			i = (i + 1) & mask
		}
		if placed {
			break
		}
		// Table full of tombstones; grow and retry once (still under writing).
		h.growTo(n << 1)
	}
	h.writing = 0
}

// mapDelete removes key. Nil or missing is a no-op. Uncomparable keys panic
// even when the map is nil or empty (gc behavior).
func mapDelete(h *hmap, key any) {
	if h == nil {
		mapKeyHash(key, 0)
		return
	}
	if h.writing != 0 {
		throw("concurrent map writes")
	}
	h.writing = 1
	if h.ctrl == nil {
		mapKeyHash(key, h.seed)
	} else {
		hash := mapKeyHash(key, h.seed)
		mask := uint32(h.mask)
		i := hash & mask
		n := h.mask + 1
		for step := int32(0); step < n; step++ {
			c := h.ctrl[i]
			if c == ctrlEmpty {
				break
			}
			if c == ctrlFull && h.keys[i] == key {
				h.ctrl[i] = ctrlTombstone
				h.keys[i] = nil
				h.vals[i] = nil
				h.used--
				break
			}
			i = (i + 1) & mask
		}
	}
	h.writing = 0
}

// mapClear removes all entries. Nil is a no-op.
func mapClear(h *hmap) {
	if h == nil || h.ctrl == nil {
		return
	}
	if h.writing != 0 {
		throw("concurrent map writes")
	}
	h.writing = 1
	n := h.mask + 1
	for i := int32(0); i < n; i++ {
		h.ctrl[i] = ctrlEmpty
		h.keys[i] = nil
		h.vals[i] = nil
	}
	h.used = 0
	h.writing = 0
}

// mapLen returns used, or 0 for nil.
func mapLen(h *hmap) int32 {
	if h == nil {
		return 0
	}
	return h.used
}

// mapIterStart snapshots the table and picks a randomized start index.
func mapIterStart(h *hmap) *mapIter {
	mapCheckWrite(h)
	if h == nil || h.ctrl == nil || h.used == 0 {
		return &mapIter{}
	}
	n := h.mask + 1
	start := int32(0)
	if n > 0 {
		start = int32(fastrandn(uint32(n)))
	}
	return &mapIter{
		keys:  h.keys,
		vals:  h.vals,
		ctrl:  h.ctrl,
		start: start,
		n:     n,
	}
}

// mapIterNext returns the next full slot, skipping tombstones.
func mapIterNext(it *mapIter) (key, val any, ok bool) {
	if it == nil || it.n == 0 {
		return nil, nil, false
	}
	for it.i < it.n {
		idx := (it.start + it.i) % it.n
		it.i++
		if it.ctrl[idx] == ctrlFull {
			return it.keys[idx], it.vals[idx], true
		}
	}
	return nil, nil, false
}

// mapKeyHash hashes a boxed key mixed with seed. Uncomparable dynamic types
// panic via key==key. Per-type helpers keep non-defaultable assert locals
// scoped to one frame (codegen rejects uninitialized $go.string locals).
func mapKeyHash(key any, seed uint32) uint32 {
	if key == nil {
		return seed
	}
	if h, ok := hashBool(key, seed); ok {
		return h
	}
	if h, ok := hashInt(key, seed); ok {
		return h
	}
	if h, ok := hashInt64(key, seed); ok {
		return h
	}
	if h, ok := hashUint(key, seed); ok {
		return h
	}
	if h, ok := hashUint64(key, seed); ok {
		return h
	}
	if h, ok := hashFloat32(key, seed); ok {
		return h
	}
	if h, ok := hashFloat64(key, seed); ok {
		return h
	}
	if h, ok := hashComplex64(key, seed); ok {
		return h
	}
	if h, ok := hashComplex128(key, seed); ok {
		return h
	}
	if h, ok := hashString(key, seed); ok {
		return h
	}
	// Comparable-but-unspecialized keys: key==key succeeds → seed mix.
	// Uncomparable keys panic via ==; rewrite message for recover2.
	return mapKeyHashFallthrough(key, seed)
}

func mapKeyHashFallthrough(key any, seed uint32) (h uint32) {
	defer func() {
		if recover() != nil {
			name := ReflectTypeName(ReflectType(key))
			if name == "" {
				name = "<unknown>"
			}
			throwPanic(panicErr("runtime error: hash of unhashable type " + name))
		}
	}()
	_ = key == key
	return seed ^ 0x9e3779b9
}

func hashBool(key any, seed uint32) (uint32, bool) {
	x, ok := key.(bool)
	if !ok {
		return 0, false
	}
	if x {
		return seed ^ 1, true
	}
	return seed, true
}

func hashInt(key any, seed uint32) (uint32, bool) {
	if x, ok := key.(int); ok {
		return seed ^ uint32(x), true
	}
	if x, ok := key.(int8); ok {
		return seed ^ uint32(uint8(x)), true
	}
	if x, ok := key.(int16); ok {
		return seed ^ uint32(uint16(x)), true
	}
	if x, ok := key.(int32); ok {
		return seed ^ uint32(x), true
	}
	return 0, false
}

func hashInt64(key any, seed uint32) (uint32, bool) {
	x, ok := key.(int64)
	if !ok {
		return 0, false
	}
	return seed ^ uint32(x) ^ uint32(uint64(x)>>32), true
}

func hashUint(key any, seed uint32) (uint32, bool) {
	if x, ok := key.(uint); ok {
		return seed ^ uint32(x), true
	}
	if x, ok := key.(uint8); ok {
		return seed ^ uint32(x), true
	}
	if x, ok := key.(uint16); ok {
		return seed ^ uint32(x), true
	}
	if x, ok := key.(uint32); ok {
		return seed ^ x, true
	}
	if x, ok := key.(uintptr); ok {
		return seed ^ uint32(x), true
	}
	return 0, false
}

func hashUint64(key any, seed uint32) (uint32, bool) {
	x, ok := key.(uint64)
	if !ok {
		return 0, false
	}
	return seed ^ uint32(x) ^ uint32(x>>32), true
}

func hashFloat32(key any, seed uint32) (uint32, bool) {
	x, ok := key.(float32)
	if !ok {
		return 0, false
	}
	return seed ^ f32hash(x), true
}

func hashFloat64(key any, seed uint32) (uint32, bool) {
	x, ok := key.(float64)
	if !ok {
		return 0, false
	}
	return seed ^ f64hash(x), true
}

func hashString(key any, seed uint32) (uint32, bool) {
	x, ok := key.(string)
	if !ok {
		return 0, false
	}
	return strhash(x, seed), true
}

func hashComplex64(key any, seed uint32) (uint32, bool) {
	x, ok := key.(complex64)
	if !ok {
		return 0, false
	}
	return seed ^ f32hash(real(x)) ^ (f32hash(imag(x)) * 0x01000193), true
}

func hashComplex128(key any, seed uint32) (uint32, bool) {
	x, ok := key.(complex128)
	if !ok {
		return 0, false
	}
	return seed ^ f64hash(real(x)) ^ (f64hash(imag(x)) * 0x01000193), true
}

func f32hash(f float32) uint32 {
	u := f32bits(f)
	// NaN: exp all 1s, mantissa nonzero. ±0 → 0. Avoids f32.eq/ne (codegen).
	abs := u & 0x7fffffff
	if abs > 0x7f800000 {
		return nanHashConst
	}
	if abs == 0 {
		return 0
	}
	return u
}

func f64hash(f float64) uint32 {
	u := f64bits(f)
	abs := u &^ (uint64(1) << 63)
	if abs > 0x7ff0000000000000 {
		return nanHashConst
	}
	if abs == 0 {
		return 0
	}
	return uint32(u) ^ uint32(u>>32)
}

// strhash hashes the string bytes (len from the header).
func strhash(s string, seed uint32) uint32 {
	h := seed
	n := len(s)
	for i := 0; i < n; i++ {
		h ^= uint32(s[i])
		h *= 0x01000193 // FNV-1a prime
	}
	h ^= uint32(n)
	return h
}

// f32bits / f64bits are body-less; codegen fills reinterpret (design 12 nosuspend).
//
//go:wasmgc nosuspend
func f32bits(f float32) uint32

//go:wasmgc nosuspend
func f64bits(f float64) uint64
