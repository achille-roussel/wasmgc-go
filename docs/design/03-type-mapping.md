# 03 — Type mapping

A Go value lowers to one Wasm value. A composite value is one reference, not a flattened list of scalars. wasm3 tried flattening a string field into the enclosing struct and backed out: a later pass had an `anyref` local where `struct.get` had produced an `i64`, and validation failed (`docs/wasm3-study/01-type-mapping.md`). Nested structs stay one reference each for the same reason. Empty structs contribute no fields, so `struct { _ noCopy; v int32 }` still has `v` at Wasm field 0, which is the wasm3 rule and the right one.

There is no interior reference. `struct.get` takes a type and a field index as immediates. Only arrays take a dynamic index. A pointer at a field or an element is the fat pointer below, or it is rewritten away by escape analysis.

`int`, `uint`, and `uintptr` are `i32`. Headers store lengths as `i32`. This is the decision in `00-overview.md`. wasm3 stored the same headers as `i32` and the Go `int` as `i64`.

Wasm struct types are limited by the engine. The core spec leaves the number of fields implementation-defined ([implementation limits](https://webassembly.github.io/spec/core/appendix/implementation.html), WebAssembly 3.0, 2026-11-16). V8's limit is 10,000 (`kV8MaxWasmStructFields` in `src/wasm/wasm-limits.h`). Arrays are always one array reference, never a struct with N fields. A Go struct with more than 10,000 fields is a compile error that names the type. That program is not a real one; the limit is there so the failure is ours.

## Prelude

Every module starts with these types. Program types are appended and interned. Recursive types (`type T struct { next *T }`, `struct { xs []X }` where `X` mentions the struct) are one Wasm `rec` group. The collector reserves the type index before walking fields, which wasm3 did for structs and missed for slices (`docs/wasm3-study/01-type-mapping.md`, `collectBacking`).

```wat
(type $go.object (sub (struct)))

(type $go.bytes (array (mut i8)))
(type $go.i16s  (array (mut i16)))

(type $go.string (sub $go.object (struct
  (field $data (ref $go.bytes))
  (field $off  i32)
  (field $len  i32))))

(type $go.type (sub $go.object (struct
  (field $id    i32)
  (field $kind  i32)
  (field $name  (ref null $go.string))
  (field $extra (ref null $go.object)))))

(type $go.itab (sub $go.object (struct
  (field $type (ref $go.type)))))

(type $go.iface (sub $go.object (struct
  (field $itab (ref $go.itab))
  (field $data (ref null any)))))

(type $go.panicObj (sub $go.object (struct
  (field $value     (ref null $go.iface))
  (field $recovered (mut i32))
  (field $next      (ref null $go.panicObj)))))
```

`$go.object` is the open struct base for descriptors and for values that are themselves structs. It is not the type of an array. Fat-pointer bases use `anyref`, below, because an array is not a subtype of `struct`. The prototype `prototypes/fat-pointer/pointer.wat` uses that split and ran on Node 22 and Node 26.

`$go.bytes` stays `(array (mut i8))`. Wasm allows `(array v128)`, and the SIMD study (`11-simd.md`) measured it, but changing the byte backing to vector chunks would break byte-oriented slicing and fat pointers. `v128` appears only as a temporary in SIMD code, not as a heap representation of `[]byte`.

## Scalars

| Go | In a struct field or a non-packed array | As a value, a parameter, or a local |
|---|---|---|
| `bool`, `int8`, `uint8`, `int16`, `uint16`, `int32`, `uint32`, `int`, `uint`, `uintptr` | `i32`, except the packed-array rows below | `i32` |
| `int64`, `uint64` | `i64` | `i64` |
| `float32` | `f32` | `f32` |
| `float64` | `f64` | `f64` |
| `complex64` | two `f32` fields, or a struct of two `f32` when the complex is itself a value | two `f32` results at a call, one struct ref when stored |
| `complex128` | two `f64` | same pattern |

Packed array elements, and only array elements:

| Element | Storage | Load |
|---|---|---|
| `int8`, `uint8`, `bool` | `(array (mut i8))` | `array.get_s` or `array.get_u` |
| `int16`, `uint16` | `(array (mut i16))` | `array.get_s` or `array.get_u` |

A struct field of type `byte` is `i32`. wasm3 did this, and a packed struct field is not worth a special access path when the field index is static (`docs/wasm3-study/01-type-mapping.md`). `[]byte` and `[N]byte` use the packed array. That is the representation that has to be fast.

`bool` stored in a struct is `i32`. A `bool` array is packed `i8`.

## Strings

```wat
(type $go.string (sub $go.object (struct
  (field $data (ref $go.bytes))   ;; non-null when the string ref is non-null
  (field $off  i32)
  (field $len  i32))))
```

A string value is `(ref $go.string)`, non-null. The zero string is one shared header, `$go.emptyString`, whose length is 0 and whose backing is a shared zero-length array. Go has no nil string, so a string field, a string local, and a string global are never null. `struct.new_default` is the wrong initializer for a struct that contains a string: the zero value of that field is `$go.emptyString`, written with `struct.new`. wasm3 used a null ref as the nil string (`docs/wasm3-study/01-type-mapping.md`). That matches a slice and does not match a string. The only null string reference in the module is the data word of an interface that holds no string.

Fields are immutable. A substring `s[i:j]` is a new header with the same `$data`, a shifted `$off`, and a new `$len`. Concatenation allocates a new `(array i8)` and `array.copy`s. Bytes are not mutated after the header is published.

Literals are `array.new_data` from a passive data segment, then `data.drop` of that segment at the end of init. The bytes occupy the file once.

`s[i]` is `array.get_u` at `off+i`, after an unsigned compare of `i` against `len`. `len(s)` is `struct.get $len`. No extend.

`string([]byte)` allocates and copies. `[]byte(string)` allocates and copies. The compiler elides the copy when the source is the last use and the result is not mutated, which is the same license `cmd/compile` takes. `unsafe.String` and `unsafe.StringData` are the explicit share (`05-unsafe-and-reflect.md`).

Comparison is length, then bytes from `off` for `len`. The backing past `len` is not read.

## Slices

```wat
(type $go.array.i32 (array (mut i32)))          ;; one per element storage
(type $go.slice.i32 (sub $go.object (struct
  (field $data (ref $go.array.i32))
  (field $off  i32)
  (field $len  i32)
  (field $cap  i32))))
```

A slice value is `(ref null $go.slice.T)`. A nil slice is a null ref. A non-nil slice has a non-null `$data`, even when `len` and `cap` are 0. wasm3 used the same rule (`docs/wasm3-study/01-type-mapping.md`).

**Headers are immutable.** `s2 := s1` copies the reference. `s2 = s2[1:]` allocates a new header and leaves `s1` alone. `s2[0] = x` writes the shared backing array, so `s1[0]` changes. That is the Go model. wasm3 declared the header fields `(mut …)`. A mutable header aliased by assignment would make `s2 = s2[1:]` change `s1`. Immutable headers make the aliasing impossible. The cost is one `struct.new` per reslice or `append` that changes the header, and no copy of the backing when `cap` is sufficient.

`append` that fits in `cap` writes the new elements with `array.set` and allocates a header with the new length. `append` that does not fit allocates a new array of `max(2*cap, needed, hint)`, `array.copy`s, then writes. Growth is the usual doubling, capped at the `i32` maximum; an overflow panics with the Go `growslice` message.

Element storage:

| Element | Array element |
|---|---|
| packed scalars, from the table above | packed `i8` or `i16` |
| other scalars | that scalar |
| string, slice, map, chan, func, interface, pointer | `(ref null …)` of that value |
| struct or array | `(ref null $go.struct.T)` or `(ref null $go.array.E)`, one object per element |

One object per composite element is the wasm3 rule and is accepted for the same reason: Wasm has no unboxed struct-of-arrays for mixed refs (`docs/wasm3-study/01-type-mapping.md`). `[]byte`, `[]int`, and `[]float64` stay packed. Those are the ones that matter for size and speed.

Three-index slice `s[i:j:k]` checks `0 ≤ i ≤ j ≤ k ≤ cap` and builds a header with length `j-i` and cap `k-i`.

## Structs

```wat
(type $go.struct.Point (sub $go.object (struct
  (field $X (mut i32))
  (field $Y (mut i32)))))
```

A struct value that is stored (a field, an element, a global, an escaped local) is `(ref null $go.struct.T)`, and the pointer `*T` is that same reference. Assignment `*p = v` copies fields into the existing object. Assignment `b = a` of struct values allocates a new object and copies fields. Nested struct fields are references and are cloned; pointer, slice, map, and chan fields are copied as references. That is a deep copy of values and a shallow copy of reference types, which is what Go does.

A struct parameter is one reference. The caller passes its reference. The callee that writes a field of a by-value parameter must not be visible to the caller, so the lowerer clones at the start of the callee when any field of the parameter is assigned, and skips the clone when the parameter is not mutated. The analysis is the set of assignments in that function, not a heuristic.

A local struct that does not escape and is not addressed is not allocated. Its fields are vars. Escape of the struct, or `&` of the struct, allocates it at the point of escape.

An embedded field is a real field. Promotion is a load of that field followed by the promoted operation. The Wasm field index is assigned in source order, skipping fields of size zero (`struct{}` and arrays of it). Padding does not exist: Wasm fields are not laid out in bytes. `unsafe.Offsetof` is the fictional byte offset of `05-unsafe-and-reflect.md` and is a different number from the field index.

## Arrays

`[N]T` stored in memory is `(ref null (array elem))` of length N, one type per element type, shared with slices of that element. The length N is not part of the Wasm array type. A Go array and a Go slice of the same element share the backing type and differ in the header. Bounds checks use the constant N for the array and the header length for the slice.

`*[N]T` is the array reference. `*p = v` copies N elements with `array.copy` into the existing array. It does not replace the reference. A local array that does not escape is N vars when N ≤ 4 and the element is a scalar, and one array allocation otherwise. The threshold is a code-size choice.

wasm3 exploded integer arrays of length 1..8 into parameters because the Go register ABI did (`docs/wasm3-study/01-type-mapping.md`). A call here passes one reference.

## Pointers

Three representations, and a pointer value is exactly one of them.

### Object pointer

`*Struct` and `*[N]T` are `(ref null $go.struct.T)` and `(ref null $go.array.elem)`. Nil is null. Equality is `ref.eq`. These are the pointees whose assignment writes through the reference instead of reseating a slot.

### Fat pointer

Everything else whose address is taken: a scalar field, a scalar element, a string, a slice header, a map, a chan, a func, an interface, another pointer. Also `*T` for a scalar `T` from `new(T)`.

```wat
(type $go.get.i32 (func (param anyref i32) (result i32)))
(type $go.set.i32 (func (param anyref i32 i32)))
(type $go.class.i32 (sub $go.object (struct
  (field $get (ref $go.get.i32))
  (field $set (ref $go.set.i32)))))
(type $go.ptr.i32 (sub $go.object (struct
  (field $class (ref $go.class.i32))
  (field $base  anyref)
  (field $index i32))))
```

The same shape exists for `i64`, `f32`, `f64`, and `ref`. The class is a singleton global per `(container Wasm type, field index or "element")`, not a `struct.new` at each `&`. wasm3 inlined a new class object at every materialization even though the prelude comment said it was shared (`docs/wasm3-study/01-type-mapping.md`). Sharing the class is the size win on a program that takes many addresses.

`load` is `call_ref` of `class.get` with `(base, index)`. `store` is `class.set`. The getter `ref.cast`s `base` to the container. For a struct field the index immediate is baked into the getter and the pointer's `i32` is 0. For an array element the `i32` is the index. `prototypes/fat-pointer/pointer.wat` does both and checks that a store through the pointer is visible as a field load.

Classes by Go type:

| Pointee | Class |
|---|---|
| `bool`, integer narrower than 64, `int`, `uint`, `uintptr` | `i32` |
| `int64`, `uint64` | `i64` |
| `float32` | `f32` |
| `float64` | `f64` |
| string, slice, map, chan, func, interface, pointer, struct-or-array stored as a ref | `ref` |

Nil of any fat-pointer type is a null `(ref null $go.ptr.i32)`, not a pointer object with a null base. One representation keeps `== nil` a `ref.is_null`. Equality of two non-nil fat pointers is `ref.eq` on the bases, equal indexes, and `ref.eq` on the classes. Two pointers to the same field compare equal. Two pointers to different elements compare unequal.

`unsafe.Pointer` is `anyref`. Converting `*T` to `unsafe.Pointer` is a subtyping upcast when `*T` is an object pointer, and a pass-through of the fat-pointer reference when it is a fat pointer (the fat pointer is a subtype of `$go.object`, which is a subtype of `any`). Converting back is `ref.cast` to the destination. A cast to the wrong type traps. That is the defined behavior of a lie the language allows.

### Spill of an escaping scalar

Wasm locals are not addressable. `&x` of a scalar local that escapes allocates a one-element array of the scalar's storage and a fat pointer at index 0. The local is gone; uses of `x` go through the pointer, unless the escape pass can split the uses. This is the sequence wasm3 named `OpWasm3NewScalarPtr` and left as a compile-time fatal (`docs/wasm3-study/04-open-problems.md`). It is part of v1, not a follow-up. `new(int)` is the same sequence.

### Escape

The always-spill lowering is correct for every address-taken scalar. The escape pass deletes spills when the address does not leave the function and is not stored. A loop variable whose address escapes is spilled once per iteration, per the Go 1.22 loop semantics. A field address that does not escape is a direct `struct.get` / `struct.set`, and no pointer is built.

Whole-program escape, where a pointer passed to a callee is known not to be stored, is a later pass. v1 does not need it for correctness.

wasm3 carried three pointer representations at once (`$go.iptr`, `$go.ptr.i64`, `$go.ptr.poly`) and codegen used one of them (`docs/wasm3-study/01-type-mapping.md`). This compiler has the object pointer and the fat pointer. There is no linear `i64` meaning of `*T`.

## Maps

```wat
(type $go.map.K.V (sub $go.object (struct
  (field $used   (mut i32))
  (field $seed   i32)
  (field $mask   (mut i32))                 ;; cap - 1, cap is a power of two
  (field $ctrl   (mut (ref null $go.bytes)))
  (field $keys   (mut (ref null $go.array.K)))
  (field $vals   (mut (ref null $go.array.V))))))
```

A nil map is a null ref. `len` of nil is 0. A read of nil returns the zero value. A write of nil panics.

This is an open-addressed table, not the swiss table and not wasm3's flat unscanned pair of arrays. The swiss table in `internal/runtime/maps` is built on `unsafe.Pointer(uintptr(p)+n)` and does not compile here (`docs/wasm3-study/03-stdlib-and-unsafe.md`). wasm3's compiler map had no seed that changed the hash: `MemHash` returned the seed (`docs/wasm3-study/01-type-mapping.md`). The hash here is a real function.

`ctrl` bytes: `0` empty, `1` full, `2` tombstone. The probe is linear from `hash & mask`. Load factor 7/8, then double and rehash, dropping tombstones. Delete writes a tombstone.

The hash of a key is a generated function per key type, mixed with the per-map seed by xor. The seed is a per-map `i32` from `runtime.fastrand`. Strings hash the `len` bytes from `off`. Structs and arrays hash fields in order. Interfaces hash the type id mixed with the dynamic value's hash. Floats hash the bits, and all NaN bit patterns hash as one constant; equality uses `!=` semantics, so a NaN lookup finds nothing and each inserted NaN is a new entry. That is the Go spec.

`range` captures the `keys`, `vals`, and `ctrl` references and a start index derived from the seed, then walks. A growth allocates new arrays, so the iterator keeps walking the old ones and does not crash. Delete of the current key is a tombstone the iterator skips. This is a defined choice where Go leaves the iteration unspecified.

Comparison of maps is a compile error except against nil, which is `ref.is_null`.

## Channels

```wat
(type $go.waitq (sub $go.object (struct
  (field $head (mut (ref null $go.waiter)))
  (field $tail (mut (ref null $go.waiter))))))

(type $go.waiter (sub $go.object (struct
  (field $park  i32)
  (field $sel   (ref null $go.object))     ;; nil, or the select this waiter belongs to
  (field $case  i32)
  (field $next  (mut (ref null $go.waiter))))))

(type $go.chan (sub $go.object (struct
  (field $qcount (mut i32))
  (field $cap    i32)
  (field $sendx  (mut i32))
  (field $recvx  (mut i32))
  (field $closed (mut i32))
  (field $sendq  (mut (ref null $go.waitq)))
  (field $recvq  (mut (ref null $go.waitq))))))

(type $go.chan.T (sub $go.chan (struct
  ;; the seven fields above, repeated, because subtyping is a prefix
  (field $qcount (mut i32))
  (field $cap    i32)
  (field $sendx  (mut i32))
  (field $recvx  (mut i32))
  (field $closed (mut i32))
  (field $sendq  (mut (ref null $go.waitq)))
  (field $recvq  (mut (ref null $go.waitq)))
  (field $buf    (ref null (array (mut T)))))))
```

The buffer is a typed array. wasm3 stored it as `anyref` and cast on every access because each function had its own type table and structural dedup failed (`docs/wasm3-study/01-type-mapping.md`). One module-wide intern table makes the typed field validate. `$go.chan.T` is a subtype of `$go.chan`, so the runtime's queue manipulation takes `ref null $go.chan` and the generated send/recv `ref.cast`s back to touch `$buf`.

wasm3 stored one `sendParkID` and one `recvParkID`. A second waiter was dropped (`docs/wasm3-study/02-runtime-and-abi.md`). The queues here are lists. The send and receive protocol is in `04-runtime-and-abi.md`, including the write-before-ready order from that study.

A nil channel is a null ref. Send or receive on nil parks forever, which with deadlock detection is the Go panic "all goroutines are asleep". `len` and `cap` of nil are 0.

## Functions and closures

```wat
(type $go.func.sig (func (param $clos (ref $go.clos.sig)) (param …) (result …)))
(type $go.clos.sig (sub $go.object (struct
  (field $fn (ref $go.func.sig))
  ;; one field per captured variable, in source order
  )))
```

A func value is `(ref null $go.clos.sig)`. Nil is null. A top-level function has a closure struct whose capture list is empty; the compiler still uses one so a func value has one representation. `call_ref` passes the closure as the first argument. There is no `CTXT` global and no `i64` capture pointer. wasm3 had both, plus a per-closure struct (`docs/wasm3-study/01-type-mapping.md`). The global is shared by every JSPI activation, so a context global would be wrong the moment two goroutines run. The closure reference rides on the stack, which JSPI preserves (`prototypes/jspi-sched`).

A method value `x.M` is a closure that captures `x` and whose code loads the receiver and calls `M`. A method value of an interface captures the interface value and `call_ref`s through its itable.

Direct calls of known functions do not go through the closure.

## Interfaces

```wat
(type $go.itab.I (sub $go.itab (struct
  (field $type (ref $go.type))
  (field $m0   (ref $func.m0))
  (field $m1   (ref $func.m1)))))

(type $go.iface (sub $go.object (struct
  (field $itab (ref $go.itab))
  (field $data (ref null any)))))
```

One shape for empty and non-empty interfaces. A nil interface is a null `$go.iface`, not a struct with a null itable. An interface holding a nil pointer is a non-null iface, a non-null itable, and a null data word. wasm3 used the null-ref nil and a `{itab, data}` struct, and then started a second `{data, vtab}` representation without switching construction (`docs/wasm3-study/01-type-mapping.md`). Construction and dispatch here use the same struct.

The itable is one global per `(interface, concrete type)` pair the program actually converts or asserts. Field 0 is the concrete type descriptor, always. The remaining fields are the method funcrefs, in method-set order. An empty interface's itable has only field 0. Type identity is the descriptor, not the itable: one concrete type has a different itable per interface, so `ref.eq` on itables cannot answer a type assertion to a different interface.

A method call loads the itable, loads the funcref, and `call_ref`s with the data word as the receiver. A call whose concrete type is known is a direct `call` and builds no interface value.

A type assertion to a concrete type loads field 0 and `ref.eq`s it to the destination descriptor. A type assertion to an interface `J` loads the descriptor and indexes that descriptor's itable vector (an array of `(ref null $go.itab)`, one slot per interface that is the target of some assertion or conversion in the program). A null slot means the concrete type does not implement `J`. The failing one-result form panics with a `TypeAssertionError`. The two-result form returns the zero value and false.

A type switch is the same test, lowered to a `br_table` on the descriptor's dense `i32` id when the case set is known, which it is.

Embedding: the itable entry for a promoted method points at a wrapper that loads the embedded field and calls. The wrapper is generated with the itable.

Comparison: both nil, or the descriptors are `ref.eq` and the values are equal under the descriptor's equal function. If the dynamic type is not comparable, the comparison panics. The equal function is a funcref on the descriptor.

Assignment of an interface copies the reference. The itable and the data are immutable fields, so the copy cannot tear, which matters for `sync/atomic.Value` (`06-stdlib-strategy.md`).

## Named types, type descriptors, generics

A named type does not get its own Wasm type. `type ID int32` is `i32`. `type Name string` is `$go.string`. The descriptor is a different global, and `ref.eq` on descriptors is the type-identity test. The descriptor's `i32` id is dense over the types that survive dead-code elimination.

The descriptor fields beyond `id`, `kind`, and `name` live in `$extra`, whose concrete subtype depends on the kind: a struct type points at a field list (name, tag, type descriptor, Wasm field index), an array or slice or chan or pointer points at the element descriptor, a map points at key and element, a func points at the signature. `reflect` reads these (`05-unsafe-and-reflect.md`). They are real fields, not an `abi.Type` byte blob walked with pointer arithmetic. wasm3's descriptor was an empty object whose identity mattered and whose fields did not (`docs/wasm3-study/01-type-mapping.md`).

### Type-identity keys (`TypeKey`)

Descriptor identity follows `internal/desc.TypeKey`, not raw `types.Type.String()`:

- **`byte` / `rune`** are the specified aliases of `uint8` / `int32`: one descriptor each, so type switches and asserts treat them as identical.
- **Function-/block-local named types** append `LocalTypeSuffix` (`_pN` from the declaration position) so a local `type B struct{}` does not collide with a package-level `B` (#52). The suffix avoids `$`, which would split IR/Wasm name tokens.
- **Struct keys** use field names plus `TypeKey` of each field type (and tags), so `struct{Int}` (alias) differs from `struct{int}`, and two block-local aliases of the same name with different underlyings do not collapse.
- **`[]T` vs `[]*T`** (and arrays) keep the pointer in the key (`[]*pkg.T` ≠ `[]pkg.T`); `Sanitize` also distinguishes `[]T` from `[N]T` so itab/type names do not collide.
- **`complex64` / `complex128`** are distinct kinds (`KindComplex64` / `KindComplex128`) with `$go.complex64` / `$go.complex128` structs (mutable when addressable for `reflect.SetComplex`).

Generics are monomorphized (`01-architecture.md`). A generic struct `Pair[T, U]` becomes one Wasm struct per instantiation that is allocated. The descriptor of `Pair[int, string]` is not the descriptor of `Pair[string, int]`.

### Generic methods and parameterized interfaces

When a concrete generic type is converted to an interface, the itab funcrefs must name **instantiated** method bodies (`mono.EnsureMethod`), not the generic origin (#53). Instantiation also covers constraint method calls and method values/expressions. **Parameterized interface** methods (`Abs[T].Abs`, `A2[T].m2`) have no bodies: `methodNeedsMono` skips them and dispatch stays ordinary itab `call_ref` (#54).

## Nil, in one place

| Go nil | Wasm |
|---|---|
| nil pointer, slice, map, chan, func, interface | null ref of that value's type |
| nil `unsafe.Pointer` | `ref.null any` |
| nil data inside a non-nil interface | `ref.null any` in field `$data` |
| zero string | non-null empty header, not null |
| zero struct, zero array | the zero object, not null, when the value is on the heap; zeros in vars when it is not |

## What this mapping refuses from wasm3

| wasm3 kept | This mapping |
|---|---|
| `$go.iptr`, `$go.ptr.i64`, and `$go.ptr.poly` together | one fat pointer, class shared, base `anyref` |
| interface value `{itab, data}` and a parallel `{data, vtab}` type | `{itab, data}` only, itable field 0 is the type |
| `i64` Go `int` and `i32` headers | `i32` for both |
| mutable slice headers | immutable headers, new header on update |
| chan buffer as `anyref` | typed array, because there is one type table |
| closure field 1 as an `i64` captures pointer, plus `CTXT` | captures are typed fields, context is the first parameter |
| map hash that returns the seed | per-type hash mixed with a per-map seed |
| one park id per channel side | waiter queues |
