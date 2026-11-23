# wasm3 type mapping

How a Go type becomes a WebAssembly GC type on this branch, as the compiler actually collects it at HEAD `c8232bb660`. The collector is `src/cmd/compile/internal/wasm3/wasmtype.go`. The fixed prelude (types every module starts with) is `src/cmd/internal/wasmgc/type.go` `PreludeTypes`, indices `TypeGoObject` … `NumPreludeTypes` (lines 118–252). The call-ABI view of the same types is `flatPrimitiveFields` in `wasmabi.go` (lines 245–356) and `CalcSize` in `src/cmd/compile/internal/types/size.go`.

Two rules from `doc/wasm3-design.md` survived and one did not.

Survived, and the code repeats them:

- A wasm reference denotes a whole engine-allocated object. There is no interior reference. Field index and struct type are bytecode immediates (`struct.get $T n`). Only arrays take a dynamic index (`doc/wasm3-design.md` §4).
- A composite that must be one wasm value (a struct field, a slice element, a function parameter) is one `(ref null $SomeStruct)`, not a flattened tuple of scalars.

Abandoned as the default for fields:

- Design §6.2 rule 3 said composite values are flattened into the enclosing struct (a string field is three fields: backing, offset, length). `lowerFieldsImpl` now boxes them. The comment at `wasmtype.go:180–187` says a string field is one ref "not three flattened fields", because a byte offset into a flattened string cannot be resolved by the field-access ABI. Nested structs got the same treatment in "Rung 34" (`wasmtype.go:197–218`): flattening made the wasm field type (`i64`) disagree with the SSA local type (`anyref`) and validation failed with `local.set expected anyref, found struct.get of type i64`.

Several representations were started and not removed. Where two are both still declared, this document says which one codegen emits.

## Prelude that every module has

Built by `PreludeTypes` (`wasmgc/type.go:259–`). Indices are stable because program types are appended after `NumPreludeTypes`.

```wat
(type $go.object (sub (struct)))                          ;; TypeGoObject, open base

(type $go.bytes (array (mut i8)))                         ;; string and []byte backing

(type $go.string (sub $go.object (struct
  (field (ref $go.bytes))                                 ;; backing, non-null
  (field i32)                                             ;; offset
  (field i32))))                                          ;; length

(type $go.iface (sub $go.object (struct                   ;; LEGACY value shape, still what IfaceMake emits
  (field anyref)                                          ;; itab / type word
  (field anyref))))                                       ;; data

(type $go.iptr.i8  (sub $go.object (struct (field anyref) (field i32))))
;; same shape for i16, i32, i64, f32, f64, ref — container + offset

(type $go.ptr.i64 (sub $go.object (struct                 ;; LEGACY accessor-pair, still declared
  (field anyref) (field i32)
  (field (ref $go.getter.i64))
  (field (ref $go.setter.i64)))))
;; getter.i64 is (func (param anyref i32) (result i64))
;; also ptr.ref, ptr.f32, ptr.f64

(type $go.refclass.i64 (sub $go.object (struct            ;; vtable: shared get/set funcrefs
  (field (ref $go.getter.i64))
  (field (ref $go.setter.i64)))))
(type $go.ptr.poly.i64 (sub $go.object (struct            ;; CURRENT *scalar shape
  (field (ref $go.refclass.i64))                          ;; class
  (field anyref)                                          ;; base
  (field i32))))                                          ;; off
;; same family for ref, f32, f64

(type $go.chan (sub $go.object (struct                    ;; common chan state, 7 × i32
  (field (mut i32)) (field (mut i32)) (field (mut i32)) (field (mut i32))
  (field (mut i32))                                       ;; closed (i32, not i8 — bool lowers to i32)
  (field (mut i32))                                       ;; sendParkID
  (field (mut i32)))))                                    ;; recvParkID

(type $go.vtab (sub $go.object (struct)))                 ;; empty base; per-interface subtypes add funcrefs

(type $go.goroutine.entry (func))                         ;; void; was going to be (func (param anyref) (result anyref))
(type $go.cont (cont $go.goroutine.entry))                ;; downgraded to (func) at link if unused
```

Comments that justify the small choices:

- String offset exists "because substrings share a backing" (`type.go:280–283`).
- String/slice/chan/map length fields are `i32` "to match the int=i32 direction" and avoid `i32.wrap_i64` at every `len` (`type.go:281–283`, `wasmtype.go:457–463`). Go `int` itself is still `i64` in `scalarPrim` (next section). The "int=i32 direction" sentence is about these header fields, not about the Go `int` type.
- `$go.cont`'s body is a void `(func)` because "V8 (December 2025) does not yet implement resume against `(func (param anyref) (result anyref))`" (`type.go:459–465`). If no function body contains `cont.new`/`resume`, `writeTypeSec3` rewrites this type to a plain `(func)` so the module instantiates under JSPI alone (`type.go:475–480`).
- `closed` is `i32` because `scalarPrim(TBOOL) = I32`, so the Go struct `runtime.channel` can be the same type as `$go.chan` (`type.go:505–509`).

## Scalars

`scalarPrim` (`wasmtype.go:102–123`):

| Go | Wasm storage in a struct or array field | Wasm function param (`wasm3IntField`, `wasmabi.go` around 1289) |
|---|---|---|
| `bool`, `int8`/`uint8`, `int16`/`uint16`, `int32`/`uint32` | `i32` | `i32` |
| `int64`, `uint64`, `int`, `uint`, `uintptr` | `i64` | `i64` |
| `float32` | `f32` | `f32` |
| `float64` | `f64` | `f64` |

`int`/`uint`/`uintptr` are `i64` because `ArchWasm3.PtrSize` is 8 (`src/cmd/internal/sys/arch.go:281`). Sub-64-bit integers are `i32` in storage ("integers narrower than 64 bits use i32 storage", `wasmtype.go:103–106`).

Packed `i8`/`i16` is only for array elements, via `packedArrayPrim` (`wasmtype.go:86–99`). A struct field of type `byte` is `i32`. A `[N]byte` or `[]byte` backing is `(array (mut i8))`, read with `array.get_u`. The comment says this avoids "wasting 3 bytes per element on i32 storage".

SSA locals for integers are `i64` even when the wasm param is `i32`. `collectSliceStruct` (`wasmtype.go:460–463`) documents the tax: codegen does `i64.extend_i32_u` on `struct.get` and `i32.wrap_i64` on `struct.set`. That is a standing inconsistency. A new compiler can store lengths as `i64` and delete the extends, or it can make Go `int` 32-bit on this target. The branch did neither cleanly: the type checker says 64, the headers say 32, the SSA carrier says 64.

`complex64` is two `f32` fields, `complex128` is two `f64` (`wasmtype.go:153–162`). At a call boundary those are two wasm results, not a struct (`wasmabi.go:281–290`).

`bool` in a struct is `i32`, not packed `i8`. The chan `closed` field is the worked example (`type.go:505–509`).

## `string`

Value type:

```wat
(type $go.string (sub $go.object (struct
  (field (ref $go.bytes))   ;; (array (mut i8)), shared, non-null on a non-nil string
  (field i32)               ;; offset into the array
  (field i32))))            ;; length
```

- As a struct field, a parameter, a result, or a slice element: one `(ref null $go.string)` (`wasmtype.go:180–187`, `413–418`; `flatPrimitiveFields` `wasmabi.go:250–258`).
- A nil string is a null ref (`wasmtype.go:186`).
- A non-nil string's backing ref is non-null (`type.go:289`). Empty and non-empty strings share that rule; a zero-length string still has an array. (The slice header comment says the same thing: "a non-nil slice always has a real, possibly zero-length, backing array — mirroring go.string", `wasmtype.go:466–469`.)
- `s[i]` and `len(s)` are `struct.get` plus `array.get_u`, not pointer arithmetic. `OpWasm3StringLength` extends the `i32` field to the `i64` SSA local (`ssa.go:2140–2144`).
- Substrings share the backing array and change offset and length. That is why the offset field exists (`type.go:280`).

The old ABI was two `i64`s `(data, len)` or three slots `(backing, offset, length)`. `loweredSignature`'s comment at `wasmabi.go:69–75` still says "a string is three slots". `flatPrimitiveFields` and `topLevelFields` do not. The comment is stale. Call sites push one `anyref`.

Closure capture of a string is a separate, older shape. `captureClosureFields` is documented in `wasmabi.go` around the closure section as still carrying an `anyref` plus `i64` words in some paths. `collectClosureCtx` (`wasmabi.go:1662–1693`) stores field 1 as an `i64` "linear-memory captures-block pointer". See closures below.

## Slices

```wat
(type $go.array.<T> (array (mut <elem-storage>)))
(type $go.slice.<T> (sub $go.object (struct
  (field (mut (ref $go.array.<T>)))   ;; data, non-null when the slice ref is non-null
  (field (mut i32))                   ;; off
  (field (mut i32))                   ;; len
  (field (mut i32)))))                ;; cap
```

`collectSliceStruct` (`wasmtype.go:447–491`). A nil slice is a null `$go.slice` ref, not a struct with a null data field (`wasmtype.go:466–469`). `len`/`cap`/index widen `i32` to the `i64` SSA local.

Element storage, `collectBacking` (`wasmtype.go:378–444`):

| Element | Array element type |
|---|---|
| `bool`, integer narrower than 32 if `packedArrayPrim` says so (`byte`, `int16`, …) | packed `i8` or `i16` |
| other scalars (`int`, `int32`, `float64`, …) | that scalar's `scalarPrim` |
| `*U` | whatever `pointerStorage` says for `*U` |
| `unsafe.Pointer` | `(ref null $go.object)` (`wasmtype.go:408–412`) |
| `string` | `(ref null $go.string)` |
| slice, interface, chan, map, func | ref to that value's box |
| other composite (struct, array) | `(ref null $go.box.<T>)` — one allocation per element |

The composite-element case is design §6.3: "loses contiguous value layout — one allocation per element" (`wasmtype.go:430–433`). Accepted. There is no struct-of-arrays layout.

`[]byte` is the important special case of the packed rule: backing `(array (mut i8))`, header still `{data, off, len, cap}`.

A slice value is one ref at a call (`wasmabi.go:259–271`). The comment records the bug the 3-field ABI caused: "not enough arguments on the stack for call: need 3, got 1".

`wasm3FlatStride` (`wasmtype.go:637–659`) still returns 2 for `string` and interface and 3 for slice, describing an `(array i64)` layout "the wasm3 compiler emits today". That comment disagrees with `collectBacking`, which boxes those elements. Treat `wasm3FlatStride` as a leftover. A new compiler should not implement it. Confirm before deleting by searching callers (`ssa.go` still mentions it; the stride function is not the element type).

Register ABI: `CalcSize` sets `t.intRegs = 1` for slices, strings, and interfaces on wasm3 (`types/size.go:409–411`, `438–439`, `346–348`).

## Structs

Two wasm structs get created for a Go struct type `T`:

1. `$go.struct.T` via `collectStruct` (`wasmtype.go:339–376`), used when the value is addressed as `*T` and `T` is a struct (`pointerStorage`, lines 297–299). Fields are `lowerFields` of each Go field, so each composite field is itself one ref.
2. `$go.box.T` via `collectBox` (`wasmtype.go:680+`), used when a composite is stored by value inside something else (a nested struct field, a slice element, a pointer to a non-struct composite).

`runtime.channel` is not given its own struct. `collectStruct` aliases it to prelude `TypeGoChan` (`wasmtype.go:349–360`) so a `(ref $go.chan.<Elem>)` is a subtype of the pointer type the runtime helpers take.

Empty structs contribute zero fields (`wasmtype.go:220–231`). The reason is equality and field numbering: `struct { _ noCopy; v int32 }` must see `v` as wasm field 0, not field 1 behind an empty box. `CalcSize` leaves `intRegs = 0` for a zero-size struct and forces `intRegs = 1` for every other struct (`types/size.go:584–590`).

Top-level function parameters: `topLevelFields` (`wasmabi.go:93–100`) passes a struct as one `$go.struct.T` ref. `flatPrimitiveFields` (`wasmabi.go:291–300`) instead flattens a struct to its leaf wasm slots, and returns failure if a leaf is a slice or interface. `attachWasmType` tries the primitive path, then the collector path, then the flat path (`wasmabi.go` around 166–212). Which tier wins depends on the signature. The collector tier is the one that matches the "one ref" object model. The flat tier exists so call sites that still push one SSA register per leaf agree with the wasm type. A new compiler should pick one: one ref per struct value, including across calls. The branch could not, because Go's register ABI counts leaves (`intRegs`) and the wasm validator counts the function type's parameters.

Recursion: `collectStruct` reserves the table index before walking fields (`wasmtype.go:362–368`) so `type T struct { next *T }` terminates. `wasmgc.RecGroups` then puts the cycle in one wasm `rec` group. `collectBacking` does **not** reserve before recursing (`wasmtype.go:397–400`); the comment says a cycle through a slice (`struct X { ks []X }`) is "a future refinement". That is a known hole.

Wasm engines cap struct types at 10,000 fields. That is why arrays are always a single `(ref (array …))` and never unrolled (`wasmtype.go:234–248`, pointing at `doc/wasm3-m3-notes.md`).

## Arrays `[N]T`

In a struct field or as a heap value: one `(ref null (array <elem>))`, same element rules as slices (`wasmtype.go:234–248`).

At a call, `flatPrimitiveFields` explodes an array of 1..8 integers into N wasm params (`wasmabi.go:301–334`). Larger arrays and non-integer arrays fail the flat path and are supposed to fall through to a boxed ref. `CalcArraySize` sets `intRegs` to `N * elem.intRegs` when `N ≤ 8` and the total fits in 16 int / 32 float registers (`types/size.go:650–680`). The comment names the bug this fixed: `expected (ref $type) but nothing on stack` when the signature said one ref and the register ABI pushed N integers.

A new compiler that never uses Go's register ABI can pass every array as one ref and delete this split.

## Pointers

This is the most revised part of the design. Three mechanisms are all still in the prelude.

### Direct ref, when the pointee is already a heap object

`pointerStorage` (`wasmtype.go:292–317`):

- `*Struct` → `(ref null $go.struct.Struct)` (or `TypeGoChan` for `*runtime.channel`)
- `*slice`, `*string`, `*array`, and other composites → `(ref null $go.box.<pointee>)` (`collectBox`)
- `*scalar` → `(ref null $go.ptr.poly.<class>)`, not a box of the scalar

`unsafe.Pointer` as a value (not `*T`) is `anyref`, "the open ref valtype". A typed `(ref $go.object)` was rejected because "the wasm3 backend doesn't insert ref.cast automatically" at every call (`wasmtype.go:167–178`). Arithmetic on it is a compile error (`ssagen/ssa.go:3520–3539`):

```
wasm3: conversion between unsafe.Pointer and uintptr is not supported
(no scalar address for WasmGC refs)
```

The fatal runs before the ordinary `IsPtrShaped` convert, because `uintptr` is not pointer-shaped and would otherwise become `OpConvert` and hide the error (`ssa.go:3534–3536`).

### Polymorphic fat pointer, the path codegen emits for `*scalar` and `&struct.field`

```wat
(type $go.ptr.poly.i64 (sub $go.object (struct
  (field (ref $go.refclass.i64))  ;; {get, set} funcrefs, shared per (container, field)
  (field anyref)                  ;; base container
  (field i32))))                  ;; offset; 0 when the accessor has the field index baked in
```

Classes (`wasm3PtrClassFor`, `wasmtype.go:319–337`): integers and bool → `i64` (sub-word values widen in the accessor); `unsafe.Pointer` → `ref`; `float32` → `f32`; `float64` → `f64`.

`OpWasm3MakeFieldPtr` (`ssa.go:2272–2299`) builds one:

```wat
ref.func $getter          ;; compiler-generated per (container type, field offset)
ref.func $setter
struct.new $go.refclass.<X>
<container ref>
i32.const 0               ;; off unused; field index is immediate inside getter/setter
struct.new $go.ptr.poly.<X>
```

`OpWasm3PtrLoad` (`ssa.go:2301–2319`) and `OpWasm3PtrStore` (`ssa.go:1026+`):

```wat
;; ptr is in a scratch local
struct.get $go.ptr.poly.<X> 1     ;; base
struct.get $go.ptr.poly.<X> 2     ;; off
struct.get $go.ptr.poly.<X> 0     ;; class
struct.get $go.refclass.<X> 0     ;; class.get
call_ref $go.getter.<X>           ;; (base, off) -> value
```

The getter `ref.cast`s `base` to the concrete container and does `struct.get` or `array.get` at a compile-time index. A callee that only has `*T` for a scalar `T` does not need the container's type. That is the whole point of the funcref (`type.go:343–351`).

`WasmGCFieldGetter` / `WasmGCFieldSetter` live in `cmd/compile/internal/reflectdata` and are generated per container type. The class instance is inline at each pointer materialization (`struct.new` of the two funcrefs), not a module global, despite the prelude comment talking about globals (`type.go:184–186`). The code in `MakeFieldPtr` is the authority.

### What is declared and not what codegen uses

- `$go.ptr.i64` `{base, off, get, set}` with the funcrefs on every pointer (`type.go:366–376`). `MakeFieldPtr`'s comment still says "Materialize `$go.ptr.i64`" (`ssa.go:2273`) and then emits `$go.ptr.poly`. The comment is stale. The poly shape shares the funcref pair instead of storing it on every pointer (`type.go:197–203`).
- `$go.iptr.<class>` `{container anyref, offset i32}` (`type.go:295–327`). Load/store were specified as `OpWasm3LoadInterior` / `StoreInterior`, which `ref.cast` the container and use the `i32` as an array index or a field index. Design write-up: `doc/wasm3-fat-pointers-design.md`. A 2026-05-20 note in that file says the per-access pattern (`for i { dst[i] = src[i] }`) worked, and a fat pointer through a phi or a function parameter failed validation (`expected i64, found anyref`) because a `*byte` is sometimes a linear address (WASI `nwritten *size`) and sometimes a GC ref. The poly pointer is the later attempt to make the representation a function of the type.
- `OpWasm3NewScalarPtr`, `OpWasm3PolyPtrLoad`, `OpWasm3PolyPtrStore` still `Fatalf` (`ssa.go:1005–1024`):

```
TODO(polyptr-step-2): emit the construction sequence
v.Fatalf("OpWasm3NewScalarPtr: codegen not yet implemented (polyptr migration step 2)")
```

`PtrLoad` / `PtrStore` / `MakeFieldPtr` are implemented. `NewScalarPtr` is not. `new(int)` is documented in `pointerStorage` (`wasmtype.go:301–311`) as going through `MakeFieldPtr`, not `NewScalarPtr`. If a lowering still emits `NewScalarPtr`, the compile dies.

`collectBoxedScalar` (`wasmtype.go:662–677`) builds a one-field `$go.box.<prim>` and is the design-doc §7 "selective boxing" of a scalar so a pointer can name it. `pointerStorage` does not call it for `*scalar`; it returns the poly pointer. The boxed-scalar type is leftover unless some other caller still registers it.

### Interior pointer, the practical rule for a new compiler

Carry one scheme, not three:

- If escape analysis can rewrite `&s.f` and `*p` into `struct.get`/`struct.set` on `s`, do that and allocate nothing. The design doc called this mandatory (`doc/wasm3-design.md` §7.1). The branch did it when the existing SSA forwarding happened to apply, and materialized a poly pointer otherwise.
- If the pointee is a whole heap object (`*Struct`, `*Slice` box), the pointer is that ref.
- If the pointee is a scalar slot inside a heap object that escapes, the pointer is `{class, base, offset}` plus a getter/setter closed over the static field index. One class instance per (container type, field), not per pointer.
- Do not also keep a linear `i64` meaning of `*T`. The branch's validation failures came from that aliasing (`doc/wasm3-fat-pointers-design.md` lines 230–241).

`&local` of a scalar that escapes has nowhere to point: wasm locals are not addressable. The branch's answer is to spill the local into a one-element array or a box and point at that. `OpWasm3NewScalarPtr`'s unimplemented sequence (`ssa.go:1006–1013`) is exactly that spill: `array.new_default` of one element, then a poly pointer at index 0. It was not finished. A new compiler has to finish it or refuse escaping `&local`.

## Maps

Compiler representation, `collectMapStruct` (`wasmtype.go:494–552`):

```wat
(type $go.map.<K>.<V> (sub $go.object (struct
  (field (mut i32))                          ;; used  — field 0 so len(m) is struct.get 0
  (field (mut i32))                          ;; cap
  (field (mut (ref null $go.array.<K>)))     ;; keys
  (field (mut (ref null $go.array.<V>))))))  ;; values
```

Keys and values use `collectBacking`, so `map[string]int` has keys `(array (ref null $go.string))` and values `(array i64)` (int is `i64`). Empty map: `used=0`, `cap=0`, both arrays null (`wasmtype.go:521–524`). First insert allocates with `array.new_default`.

This is a flat open-address-free table: two parallel arrays, scanned or indexed by the compiler's own map ops (`OpWasm3Map*`), not the swiss table. `internal/runtime/maps` swiss-table sources are build-tagged `!wasm3` because they do `unsafe.Pointer(uintptr(p)+n)` (`maps_wasm3.go:7–18`).

There is a second implementation. `internal/runtime/maps/runtime_faststr_wasm3.go` is a linear scan for `map[string]V` whose **values** still live in a linear-memory bump region (`wasm3MapData.values unsafe.Pointer`, lines 42–62). Its header (lines 5–33) calls itself "the bring-up rung, not the long-term home" and says other key classes (`runtime_fast32/64`) trap. `len` on that runtime `Map` reads `used` at Go offset 0, which is why both structs put `used` first — but the runtime `Map.used` is a `uint64` (`maps_wasm3.go:39–41`) while `$go.map` stores `i32`. They are not the same type. The compiler path is `$go.map`. The faststr path is what you get if execution falls into the runtime map functions. A new compiler should keep the two-array WasmGC map and not port faststr.

No hash seed, no overflow buckets, no deletion tombstones in the type itself. Hash functions `MemHash` / `StrHash` in `maps_wasm3.go:69–80` return the seed unchanged, with the comment that this is "correctness-breaking for hash-table use". Interface-keyed maps and `hash/maphash` are not done.

## Channels

```wat
(type $go.chan.<T> (sub $go.chan (struct
  ;; fields 0..6 repeat $go.chan exactly (subtyping prefix rule)
  (field (mut i32)) ... (field (mut i32))    ;; qcount, dataqsiz, sendx, recvx, closed, sendParkID, recvParkID
  (field (mut anyref)))))                    ;; slots — NOT (ref (array T))
```

`collectChanStruct` (`wasmtype.go:583–634`). The slots field is `anyref` on purpose: if it were `(ref null (array T))`, `mergeTable` across packages failed to dedup `$go.chan.<T>` because the array's local type index remapped differently per function (`wasmtype.go:618–630`). The per-T array type is still allocated (`collectBacking` at line 603) and the trampoline `ref.cast`s the `anyref` back to `(ref (array T))` before `array.get`/`array.set`.

The comment block at `wasmtype.go:563–573` still shows `i64` counters, a typed array, and `i8 closed`. The code below it is `i32` and `anyref`. Trust the code. `doc/wasm3-chan-subtyping.md` has the same split: an older diagram and a later "implementation deltas" section. The deltas match the code.

`sendParkID` / `recvParkID` are a single waiting goroutine per side (`wasmtype.go:576–582`). A second sender has nowhere to queue. That is a hard limit of this representation, not an accident.

`qcount` is field 0 so the existing `len(ch)` lowering (load at offset 0) is a `struct.get` (`wasmtype.go:575`).

A chan value is one ref (`wasmabi.go:344–353`). Nil chan is a null ref.

## Functions and closures

```wat
(type $go.func.<sig> (func <lowered params> <lowered results>))
(type $go.closure.<sig> (sub $go.object (struct
  (field (ref $go.func.<sig>))    ;; funcref for call_ref
  (field i64))))                  ;; captures pointer; 0 for a top-level func
```

`collectClosureCtx` (`wasmabi.go:1662–1693`). The `i64` is described as "a linear-memory captures-block pointer" copied into a wasm global `CTXT` so the body's existing CTXT-relative loads work. `wasm3obj.go` also has `CTXT_REF` (an `anyref` global) for the captures-in-struct path. The i64 global is "targeted for retirement" in the obj backend comments (see `02-runtime-and-abi.md`).

Per-closure struct, `collectPerClosureCtx` (`wasmabi.go:1637–1659`): subtypes `$go.closure.<sig>`, repeats fields 0 and 1, then appends one field group per captured variable via `captureClosureFields`. Method values (`-fm` wrappers) use the same struct, keyed by the body `LSym` (`wasmabi.go:1444–1448`).

A func value at a call or in a struct is one `anyref` (`wasmabi.go:335–343`, `wasmtype.go:271–279`). The indirect call `ref.cast`s to the closure struct, `struct.get`s the funcref, and `call_ref`s.

Top-level functions are ordinary wasm functions with real parameter and result types. There is no `(i32)->(i32)` block-id ABI. See `02`.

## Interfaces

**What a value is today.** `lowerFieldsImpl` for `TINTER` (`wasmtype.go:281–286`) and `OpWasm3IfaceMake` (`ssa.go:2163–2270`) build:

```wat
struct.new $go.iface     ;; (itab anyref, data anyref)
```

Field 0 is the itab or type descriptor, field 1 is the data (`ssa.go:2146–2161`). Both are `anyref`. A nil interface is a null ref, not a struct with two nulls (`wasmtype.go:284–285`). A nil word inside a non-nil interface is `ref.null any`: the lowering special-cases `I64Const 0`, drops the `i64`, and emits `ref.null` (`ssa.go:2194–2198`).

Type descriptors and itabs are not linear-memory `abi.Type` blobs. A symbol named `type:T` or `go:itab.I,T` is read with `global.get` and relocation `R_WASMDESCRIPTOR`, and the global's initializer is `struct.new_default $go.object` (`ssa.go:2206–2229`). The pointer's identity is the wasm object identity. There is no size, hash, or method list stored in that object yet. `internal/abi/type_wasm3.go:7–16` says the helpers that walk a real type descriptor panic until "M6".

**What phase D added beside that.** For a non-empty interface, `IfaceMake` also registers `$go.vtab.<I>` and `$go.iface.<I>` (`ssa.go:2182–2185`) so the linker emits them. The intended shape, written on the prelude (`type.go:213–249`):

```wat
(type $go.vtab.<I> (sub $go.vtab (struct
  (field (ref null $go.object))          ;; type descriptor, field 0 — code still has this
  (field (ref $functype_method0))
  (field (ref $functype_method1)) ...)))

(type $go.iface.<I> (sub $go.object (struct
  (field anyref)                         ;; data
  (field (ref $go.vtab.<I>)))))          ;; vtab; nil vtab means nil interface
```

The prelude comment says type identity **is** the vtab pointer, and a type assert is `ref.eq` on that pointer (`type.go:233–237`). The vtab-construction comment in `collectIfaceVtabType` still puts a type-descriptor ref at field 0 (`wasmabi.go` around 1498–1500). Those two sentences disagree. Dispatch that did land (`594566ff4f`) is `struct.get` of a method funcref and `call_ref`. Construction of the interface value did not switch to `$go.iface.<I>`; it still `struct.new`s `TypeGoIface` (`ssa.go:2269–2270`). The prelude says so: "TypeGoIface (legacy) is preserved through phase C; phase D switches call sites… Once all sites migrate TypeGoIface can be removed" (`type.go:245–249`). Phase D started (vtabs exist, dispatch exists) and the value representation did not move.

`runtime/iface.go` is tagged `!wasm3` because its type-assert cache stores `uintptr(unsafe.Pointer(typ))` (`iface_wasm3.go:11–18`). `itabsinit` is a no-op (`iface_wasm3.go:29–33`). Assertions are supposed to be SSA ops, not `runtime.typeAssert`.

`convT16` / `convT32` / `convT64` / `convTstring` / `convTslice` are the usual runtime boxing entries. The wasm3 mirror's dynamic-boxing path is incomplete (several return nil or trap). Static conversions the compiler can see become `IfaceMake` directly. A conversion it cannot see falls into those stubs. This is the main way "put this `int` in an `any`" fails if the compiler does not intrinsify that exact call.

Empty interface and non-empty interface are the same `$go.iface` shape. The itab word of an empty interface is the type descriptor global; of a non-empty interface it is the itab global (`ssa.go:2206–2210`).

## Named types, type descriptors, nil

Named types do not get their own wasm type. The collector keys on the compiler's canonical `*types.Type` (`wasmtype.go:28–29`). `type Name string` lowers as `string`. `typeName` (`wasmtype.go:718–725`) is a debug label only.

There is no wasm struct that mirrors `abi.Type` / `rtype`. The descriptor is an opaque `$go.object` global whose identity matters and whose fields do not. Anything in `reflect` or `runtime` that reads `typ.size`, method slices, or uncommon-type bytes via `uintptr(p)+n` panics or does not compile (`internal/abi/type_wasm3.go:12–16`, string `wasm3PtrArithUnsupported` at line 200).

Nil:

| Go nil | Wasm |
|---|---|
| nil pointer, slice, string, map, chan, func, interface | null ref of that value's type |
| nil `unsafe.Pointer` | `ref.null any` |
| nil interface word stored inside a present interface struct | `ref.null any` in that field (`ssa.go:2194–2198`) |

## Half-migrated representations (do not copy all of them)

| Still declared | What codegen actually emits | Where |
|---|---|---|
| `$go.ptr.<class>` with get/set on the pointer | `$go.ptr.poly.<class>` `{class, base, off}` | `type.go:160–211` vs `ssa.go:2286–2299` |
| `$go.iptr.<class>` `{container, offset}` | poly pointer, plus `LoadInterior` rules where a rewrite still matches an `OpWasm3InteriorPtr` syntactically | `type.go:295–327`, `doc/wasm3-fat-pointers-design.md` |
| `$go.iface.<I>` `{data, vtab}` | `struct.new $go.iface` `{itab, data}` | `type.go:245–249`, `ssa.go:2269–2270` |
| `$go.box.scalar` / `collectBoxedScalar` | `*scalar` is a poly pointer | `wasmtype.go:301–311` vs `662–677` |
| `loweredSignature` comment: string = 3 slots, struct flattened | one ref for string/slice/iface; struct either one ref (collector) or flattened leaves (flat tier) | `wasmabi.go:69–75` vs `93–100` and `250–280` |
| `wasm3FlatStride` `(array i64)` for `[]string` | `collectBacking` boxes the element | `wasmtype.go:637–659` vs `413–433` |
| `collectChanStruct` comment: `i64` + typed array + `i8` | `i32` prefix + `anyref` slots | `wasmtype.go:563–573` vs `610–631` |
| Closure field 1 = `i64` linear captures | also per-closure typed fields and `CTXT_REF` | `wasmabi.go:1668–1673` |
| `OpWasm3NewScalarPtr` / `PolyPtrLoad` / `PolyPtrStore` | `Fatalf` at compile time | `ssa.go:1005–1024` |
| runtime `maps.Map` + linear value bytes | compiler `$go.map.K.V` two wasm arrays | `runtime_faststr_wasm3.go` vs `wasmtype.go:530–552` |

The object model a new compiler should start from is the column "what codegen emits", minus the fatals: scalars as wasm primitives, one GC ref per composite value, strings and slices as header structs over wasm arrays, maps as two parallel arrays, chans as a small state struct plus a slots array, `*scalar` as `{class, base, off}` with shared accessors, interfaces as either the legacy pair or the vtab pair but not both, and no `uintptr` interpretation of any of those refs.
