# 05 — unsafe and reflect

`unsafe.Pointer` is `anyref`. `uintptr` is `i32`. Nothing in the instruction set turns one into the other. wasm3 rejected the conversion in SSA, after lowering had started (`docs/wasm3-study/03-stdlib-and-unsafe.md`). The check here runs on the type-checked AST, and the error names the expression.

## The size fiction

`unsafe.Sizeof`, `unsafe.Alignof`, and `unsafe.Offsetof` are compile-time constants. They report the Go 32-bit layout:

| Type | Size | Align |
|---|---:|---:|
| `bool`, `int8`, `uint8` | 1 | 1 |
| `int16`, `uint16` | 2 | 2 |
| `int32`, `uint32`, `int`, `uint`, `uintptr` | 4 | 4 |
| `int64`, `uint64` | 8 | 4 |
| `float32` | 4 | 4 |
| `float64` | 8 | 4 |
| `complex64` | 8 | 4 |
| `complex128` | 16 | 4 |
| pointer, map, chan, func | 4 | 4 |
| string | 8 | 4 |
| slice | 12 | 4 |
| interface | 8 | 4 |
| struct | fields in order, aligned, trailing padding to the struct's align | max of the field aligns |
| array `[N]T` | `N * stride(T)` | align of `T` |

`go/types.StdSizes{WordSize: 4, MaxAlign: 4}` produces this, and it is the only size table (`01-architecture.md`).

These numbers are not the Wasm representation. A string is one reference, not 8 bytes. A struct field's Wasm index is not its `Offsetof`. The constants exist so that a program which branches on `Sizeof` or sizes a buffer without ever turning the number into an address keeps compiling. The checker rejects the conversion that would complete the usual pattern:

```go
p = unsafe.Pointer(uintptr(unsafe.Pointer(p)) + unsafe.Sizeof(*p))
```

`encoding/binary` of a struct does not use this pattern on the in-memory value; it walks fields with `reflect`. `binary.Size` of a type that contains a slice, string, or map is −1 in the standard library, so the fictional size of a string is not what `binary.Write` emits. Packages that do overlay a struct on a byte slice are overlays or rewrites (`06-stdlib-strategy.md`).

## Supported

| Operation | Behavior |
|---|---|
| `unsafe.Sizeof`, `Alignof`, `Offsetof` | the constants above |
| `unsafe.Pointer(p)` where `p` has type `*T` | the pointer value as `anyref` |
| `(*T)(u)` where `u` has type `unsafe.Pointer` | `ref.cast` to `*T`'s Wasm type. A wrong dynamic type traps. |
| `unsafe.StringData(s)` | a `*byte` fat pointer at index `off` of the string's backing array |
| `unsafe.SliceData(s)` | a `*T` fat pointer at index `off` of the slice's backing array. Nil slice returns nil. |
| `unsafe.String(p *byte, n)` | a string header over the same `i8` array, from the pointer's index, length `n`. The compiler accepts it when `p`'s static class is the `i32` element pointer of a `*byte` whose only possible base is `$go.bytes`. A `*byte` that addresses a struct field of type `byte` (stored as `i32`, not in an `i8` array) is a compile error. |
| `unsafe.Slice(p *T, n)` | a slice header over the array `p` points into, from that index, length and cap `n`. Accepted when `p` is an element pointer of an array of `T`. `n` past the array's length panics at run time. |
| `any` to `unsafe.Pointer` and back, through the interface data word | the data word is already `anyref` |

`unsafe.String` of a string's own `StringData` round-trips, including the offset. That is the operation `strings.Builder.String` uses in the pin: `src/strings/builder.go` calls `unsafe.String(unsafe.SliceData(b.buf), len(b.buf))`.

The nil check and the "length too large" check inside `unsafe.String` and `unsafe.Slice` do not use `uintptr(len) > -uintptr(ptr)`. There is no scalar address space to wrap in. The check is `n < 0` and, when the base is an array whose length the pointer carries, `index + n` past that length. wasm3 dropped the wrap check for the same reason (`docs/wasm3-study/03-stdlib-and-unsafe.md`).

## Rejected at compile time

The diagnostic format is fixed, so tests can match it:

```
foo.go:12:3: unsupported unsafe operation
    expression: uintptr(unsafe.Pointer(p)) + 4
    Wasm GC has no integer address for a GC object.
    supported: unsafe.Slice, unsafe.String, unsafe.SliceData, unsafe.StringData,
    and field or element access on a typed pointer.
```

Rejected expressions:

- A conversion between `uintptr` and `unsafe.Pointer`, either direction, including through a named type whose underlying type is one of those.
- `unsafe.Add`, `unsafe.Slice` of a pointer that is not an element pointer, and `unsafe.String` of a `*byte` that is not an element of an `i8` array.
- Pointer ordering (`<`, `>`, `<=`, `>=`) unless both operands are element pointers the checker can see share a base. Equality stays allowed.
- Subtracting two pointers.

`unsafe.Add` on an integer that did not come from a pointer is still a conversion the checker sees, because `unsafe.Add`'s first parameter is `unsafe.Pointer`. There is no legal call.

The checker does not try to be a points-to analysis. If the static type of the pointer is not specific enough to prove the operation, the operation is rejected. A cast through `unsafe.Pointer` to the right `*T` makes a following `SliceData` legal. That is the supported way to write the operation, and it is what the diagnostic says.

## What this does to the standard library

The packages that compile unchanged are the ones whose non-test files, after the build-tag re-filter of `01-architecture.md`, contain none of the rejected expressions. From a scan of the `go1.27.4` tree, excluding `cmd`, `runtime`, and `internal/runtime`:

- `fmt`, `strconv`, `bytes`, `errors`, `sort`, `unicode`, `encoding/json`, and `encoding/binary` (non-test) do not convert `uintptr` to `unsafe.Pointer`. `strings` uses `unsafe.String`, `unsafe.SliceData`, and `abi.NoEscape(unsafe.Pointer(b))`, which are supported once `abi.NoEscape` is the identity function described in `06`.
- `slices.overlaps` compares `uintptr(unsafe.Pointer(&a[0]))`. That file is an overlay.
- `sync.Cond`'s copy check and `sync.Pool.indexLocal` do the same. Those functions are overlays.
- `unique.clone` walks a value by `uintptr` offset. `unique` is not in the v1 library set.
- `reflect` and `internal/abi` walk `abi.Type` with `unsafe.Add`. They are rewrites, not overlays: the byte layout they walk does not exist.
- `hash/maphash` calls `runtime_memhash(unsafe.Pointer(&buf[0]), …)`. The runtime provides that function as a real hash of the bytes, which is the map hash. The call shape stays.

A package the scan missed fails `wasmgc-go std check` when the pin is materialized, because the checker runs on the vendored sources. The scan is the seed of the manifest, not the guarantee. The guarantee is the checker.

## Reflect

`reflect` is a real package, written against type descriptors, because `fmt` and `encoding/json` need it. It is not a stub that panics on every call. wasm3 stubbed it and kept `errors` linking through a stubbed `reflectlite` (`docs/wasm3-study/02-runtime-and-abi.md`). That gets a program to link and then fail inside `fmt`.

### What a descriptor can answer

The descriptor in `03-type-mapping.md` carries kind, name, package path, size and alignment from the fiction above, and kind-specific extra:

- struct: field name, tag, type, Wasm field index, embedded flag, offset from the fiction
- interface: method names and types
- func: in and out tuples
- array, slice, pointer, chan: element
- map: key and element
- a funcref `equal`, a funcref `hash`, and the itable vector used by type assertions

`reflect.Type` methods that read those fields work: `Kind`, `Name`, `PkgPath`, `String`, `Size`, `Align`, `NumField`, `Field`, `Tag`, `NumMethod`, `Method`, `Elem`, `Key`, `In`, `Out`, `NumIn`, `NumOut`, `Implements`, `AssignableTo`, `ConvertibleTo`, `Comparable`, `Len` for arrays.

`reflect.Value` of a value the compiler boxed into an interface carries the descriptor and the data word. Field access, indexing, map iteration, `Len`, `IsNil`, `Interface`, `Set` on an addressable value, and method calls go through funcrefs the compiler generated for that concrete type: `getField(data, i) any`, `setField(data, i, v any)`, `index`, `setIndex`, `mapRange`. The same funcref idea is what wasm3's design note called M6 and never connected (`docs/wasm3-study/02-runtime-and-abi.md`). Generating them per reachable type is the implementation.

`MakeSlice`, `MakeMap`, `MakeChan`, `New`, `Zero`, `Append`, and `Copy` work for types in the universe below. `Select` uses the same `runtime.selectGo` as the language. `Call` calls a func value.

### The closed universe

Wasm types are fixed when the module is built. The compiler adds, for every type `T` that survives dead-code elimination:

- `*T`
- `[]T`
- `map[T]struct{}` when `T` is comparable

Those three closures are what `encoding/json` v2 actually asks `reflect` for. In `go1.27.4` `encoding/json/v2/arshal_default.go`, deterministic marshaling of a non-string map key does `reflect.SliceOf` on the key and the element, and duplicate-key detection does `reflect.MapOf(key, emptyStruct)`. Both results are in the closure. `fmt` does not call `SliceOf` or `StructOf` on the production path.

`reflect.SliceOf`, `MapOf`, `PointerTo`, and `ChanOf` look the result up in the descriptor table.

- A hit returns the existing descriptor.
- A miss panics: `reflect.SliceOf: []T was not part of this program`.

`reflect.ArrayOf` hits only when that exact array type already appears in the program. `reflect.StructOf` and `reflect.FuncOf` have no closure and panic with the same shape of message unless the exact type was written in source and survived elimination, in which case the lookup hits. A reachable call is not a compile error by default: the argument is a dynamic `reflect.Type`, and the production json path is reachable and legal. A call that the compiler can see is `StructOf` on a composite literal of fields still panics at run time. Question 5 is the stricter alternative of rejecting the package at compile time.

`reflect.Value.Pointer` and `reflect.Value.UnsafePointer` do not return an integer address. `UnsafePointer` returns the `unsafe.Pointer` (`anyref`) of the value when the value is a pointer, and panics otherwise. `Pointer` panics for every value: there is no integer to return. Callers that want a map key or an identity use `UnsafePointer` and `ref.eq`. This is the break with upstream `reflect` that cgo-style code will feel first, and the panic string says so.

## `internal/abi` and `internal/reflectlite`

`internal/abi.NoEscape` is the identity, and the escape pass treats a call to it as not a leak. wasm3 did the same after the `uintptr ^ 0` form stopped compiling (`docs/wasm3-study/03-stdlib-and-unsafe.md`). `internal/abi.Escape` is a call that counts as a leak.

`internal/reflectlite` is the subset of `reflect` that `errors` and `sort` use (`Type`, `Value`, `Elem`, `Interface`). It is implemented on the same descriptors, not as a panic stub. The upstream file walks `abi.Type` with pointer arithmetic and is not compiled.
