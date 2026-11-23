package sync

// Map is a concurrent map[any]any. wasmgc rewrite uses a Mutex-backed map;
// upstream HashTrieMap lives in internal/sync which is out of v1.
type Map struct {
	mu Mutex
	m  map[any]any
}

func (m *Map) init() {
	if m.m == nil {
		m.m = make(map[any]any)
	}
}

// Load returns the value stored in the map for a key, or nil if no value is present.
func (m *Map) Load(key any) (value any, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	value, ok = m.m[key]
	return
}

// Store sets the value for a key.
func (m *Map) Store(key, value any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	m.m[key] = value
}

// Clear deletes all the entries.
func (m *Map) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.m = make(map[any]any)
}

// LoadOrStore returns the existing value for the key if present.
// Otherwise, it stores and returns the given value.
func (m *Map) LoadOrStore(key, value any) (actual any, loaded bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	if v, ok := m.m[key]; ok {
		return v, true
	}
	m.m[key] = value
	return value, false
}

// LoadAndDelete deletes the value for a key, returning the previous value if any.
func (m *Map) LoadAndDelete(key any) (value any, loaded bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	value, loaded = m.m[key]
	if loaded {
		delete(m.m, key)
	}
	return
}

// Delete deletes the value for a key.
func (m *Map) Delete(key any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	delete(m.m, key)
}

// Swap swaps the value for a key and returns the previous value if any.
func (m *Map) Swap(key, value any) (previous any, loaded bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	previous, loaded = m.m[key]
	m.m[key] = value
	return
}

// CompareAndSwap swaps old and new for key if the stored value equals old.
func (m *Map) CompareAndSwap(key, old, new any) (swapped bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	v, ok := m.m[key]
	if !ok || v != old {
		return false
	}
	m.m[key] = new
	return true
}

// CompareAndDelete deletes the entry for key if its value equals old.
func (m *Map) CompareAndDelete(key, old any) (deleted bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	v, ok := m.m[key]
	if !ok || v != old {
		return false
	}
	delete(m.m, key)
	return true
}

// Range calls f sequentially for each key and value present in the map.
func (m *Map) Range(f func(key, value any) bool) {
	m.mu.Lock()
	m.init()
	// Snapshot under lock so f can call back into Map.
	keys := make([]any, 0, len(m.m))
	vals := make([]any, 0, len(m.m))
	for k, v := range m.m {
		keys = append(keys, k)
		vals = append(vals, v)
	}
	m.mu.Unlock()
	for i := range keys {
		if !f(keys[i], vals[i]) {
			return
		}
	}
}
