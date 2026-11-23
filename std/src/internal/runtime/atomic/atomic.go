// Package atomic is a wasmgc rewrite of internal/runtime/atomic (plain ops).
package atomic

import "unsafe"

func Load(ptr *uint32) uint32           { return *ptr }
func Loadp(ptr unsafe.Pointer) unsafe.Pointer {
	return *(*unsafe.Pointer)(ptr)
}
func Load8(ptr *uint8) uint8            { return *ptr }
func Load64(ptr *uint64) uint64         { return *ptr }
func LoadAcq(ptr *uint32) uint32        { return *ptr }
func LoadAcq64(ptr *uint64) uint64      { return *ptr }
func LoadAcquintptr(ptr *uintptr) uintptr { return *ptr }

func Store(ptr *uint32, val uint32) { *ptr = val }
func Store8(ptr *uint8, val uint8)  { *ptr = val }
func Store64(ptr *uint64, val uint64) { *ptr = val }
func StorepNoWB(ptr unsafe.Pointer, val unsafe.Pointer) {
	*(*unsafe.Pointer)(ptr) = val
}
func StoreRel(ptr *uint32, val uint32) { *ptr = val }
func StoreRel64(ptr *uint64, val uint64) { *ptr = val }
func StoreReluintptr(ptr *uintptr, val uintptr) { *ptr = val }

func Xadd(ptr *uint32, delta int32) uint32 {
	*ptr += uint32(delta)
	return *ptr
}
func Xadd64(ptr *uint64, delta int64) uint64 {
	*ptr += uint64(delta)
	return *ptr
}
func Xadduintptr(ptr *uintptr, delta uintptr) uintptr {
	*ptr += delta
	return *ptr
}

func Xchg(ptr *uint32, new uint32) uint32 {
	old := *ptr
	*ptr = new
	return old
}
func Xchg8(ptr *uint8, new uint8) uint8 {
	old := *ptr
	*ptr = new
	return old
}
func Xchg64(ptr *uint64, new uint64) uint64 {
	old := *ptr
	*ptr = new
	return old
}
func Xchguintptr(ptr *uintptr, new uintptr) uintptr {
	old := *ptr
	*ptr = new
	return old
}

func Cas(ptr *uint32, old, new uint32) bool {
	if *ptr == old {
		*ptr = new
		return true
	}
	return false
}
func Cas64(ptr *uint64, old, new uint64) bool {
	if *ptr == old {
		*ptr = new
		return true
	}
	return false
}
func CasRel(ptr *uint32, old, new uint32) bool { return Cas(ptr, old, new) }
func Casuintptr(ptr *uintptr, old, new uintptr) bool {
	if *ptr == old {
		*ptr = new
		return true
	}
	return false
}

func And8(ptr *uint8, val uint8)  { *ptr &= val }
func Or8(ptr *uint8, val uint8)   { *ptr |= val }
func And(ptr *uint32, val uint32) { *ptr &= val }
func Or(ptr *uint32, val uint32)  { *ptr |= val }

type Uint8 struct{ v uint8 }

func (u *Uint8) Load() uint8     { return u.v }
func (u *Uint8) Store(val uint8) { u.v = val }

type Int32 struct{ v int32 }

func (i *Int32) Load() int32     { return i.v }
func (i *Int32) Store(val int32) { i.v = val }
func (i *Int32) Add(delta int32) int32 { i.v += delta; return i.v }

type Uint32 struct{ v uint32 }

func (u *Uint32) Load() uint32           { return u.v }
func (u *Uint32) LoadAcquire() uint32    { return u.v }
func (u *Uint32) Store(val uint32)       { u.v = val }
func (u *Uint32) StoreRelease(val uint32) { u.v = val }
func (u *Uint32) CompareAndSwap(old, new uint32) bool {
	return Cas(&u.v, old, new)
}
func (u *Uint32) Swap(new uint32) uint32 { return Xchg(&u.v, new) }
func (u *Uint32) Add(delta int32) uint32 { return Xadd(&u.v, delta) }

type Uint64 struct{ v uint64 }

func (u *Uint64) Load() uint64        { return u.v }
func (u *Uint64) LoadAcquire() uint64 { return u.v }
func (u *Uint64) Store(val uint64)    { u.v = val }
func (u *Uint64) StoreRelease(val uint64) { u.v = val }
func (u *Uint64) CompareAndSwap(old, new uint64) bool {
	return Cas64(&u.v, old, new)
}
func (u *Uint64) Swap(new uint64) uint64 { return Xchg64(&u.v, new) }
func (u *Uint64) Add(delta int64) uint64 { return Xadd64(&u.v, delta) }

type Uintptr struct{ v uintptr }

func (u *Uintptr) Load() uintptr        { return u.v }
func (u *Uintptr) LoadAcquire() uintptr { return u.v }
func (u *Uintptr) Store(val uintptr)    { u.v = val }
func (u *Uintptr) StoreRelease(val uintptr) { u.v = val }
func (u *Uintptr) CompareAndSwap(old, new uintptr) bool {
	return Casuintptr(&u.v, old, new)
}
func (u *Uintptr) Swap(new uintptr) uintptr { return Xchguintptr(&u.v, new) }
func (u *Uintptr) Add(delta uintptr) uintptr {
	return Xadduintptr(&u.v, delta)
}

type Float64 struct{ v uint64 }

func (f *Float64) Load() float64 {
	return *(*float64)(unsafe.Pointer(&f.v))
}
func (f *Float64) Store(val float64) {
	f.v = *(*uint64)(unsafe.Pointer(&val))
}

type UnsafePointer struct{ v unsafe.Pointer }

func (p *UnsafePointer) Load() unsafe.Pointer { return p.v }
func (p *UnsafePointer) Store(val unsafe.Pointer) { p.v = val }
func (p *UnsafePointer) StoreNoWB(val unsafe.Pointer) { p.v = val }
