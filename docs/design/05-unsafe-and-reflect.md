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

- `strconv`, `bytes`, `errors`, `sort`, `unicode`, `encoding/json`, and `encoding/binary` (non-test) do not convert `uintptr` to `unsafe.Pointer`. `strings` uses `unsafe.String`, `unsafe.SliceData`, and `abi.NoEscape(unsafe.Pointer(b))`, which are supported once `abi.NoEscape` is the identity function described in `06`.
- `fmt` does convert: `fmtPointer` in `print.go` does `uintptr(value.UnsafePointer())` for `%p` / sharp `%v`. The initial scan missed this because it searched for `uintptr(unsafe.Pointer` / `unsafe.Pointer(uintptr` text, not `uintptr` of a `reflect.Value.UnsafePointer()` result. That file is an overlay (`06`).
- `slices.overlaps` compares `uintptr(unsafe.Pointer(&a[0]))`. That file is an overlay.
- `sync.Cond`'s copy check and `sync.Pool.indexLocal` do the same. Those functions are overlays.
- `unique.clone` walks a value by `uintptr` offset. `unique` is not in the v1 library set.
- `reflect` and `internal/abi` walk `abi.Type` with `unsafe.Add`. They are rewrites, not overlays: the byte layout they walk does not exist.
- `hash/maphash` calls `runtime_memhash(unsafe.Pointer(&buf[0]), …)`. The runtime provides that function as a real hash of the bytes, which is the map hash. The call shape stays. No overlay for the conversion itself (`*T` to `unsafe.Pointer` is supported).

A package the scan missed fails `wasmgc-go std check` when the pin is materialized, because the checker runs on the vendored sources. The scan is the seed of the manifest, not the guarantee. The guarantee is the checker. Stream H's first `std check` run confirmed the `fmt` miss above.

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

### `Method` / `MethodByName` reachability (REFLECTMETHOD)

Exported methods stay live for reflection only when the program can **call** `reflect.Value.Method` / `MethodByName` or `reflect.Type.Method` / `MethodByName` — matching cmd/link's `REFLECTMETHOD`. An itab `ref.func` edge into those APIs (every program that imports `reflect.Type` for `fmt`) must **not** open the floodgate: `desc.fillReflectMethodHelpers` stubs `runtime.ReflectNumMethod` and friends when no real `OpCall` / `OpIfaceCall` / `call_ref` of Method is reachable, so DCE can prune unused methods. When Method is reachable, helpers emit method-expression and method-value closures with `ref.func` to the underlying methods. If every reachable use is `MethodByName` with a string literal, only those named methods are kept.

`MakeSlice`, `MakeMap`, `MakeChan`, `New`, `Zero`, `Append`, and `Copy` work for types in the universe below. `Select` uses the same `runtime.selectGo` as the language. `Call` calls a func value. `MakeSlice` of a type that is not in the universe does not happen: the constructor call that would have produced that type was a compile error.

### Dynamic type constructors

Wasm struct types are fixed when the module is built. `reflect.StructOf`, `FuncOf`, `ArrayOf`, `ChanOf`, `MapOf`, `SliceOf`, and `PointerTo` cannot mint a type the module does not already contain. Achille decided on 2026-11-22 that a reachable call whose result the compiler cannot prove is already in the module is a **compile error**, not a run-time panic.

The check runs after dead-code elimination (`01-architecture.md`). A call in a function that the reachability pass deleted is not an error. A call in a block the IR proves dead (a constant `if` whose condition folded) is not an error. Every other call in a live function is reachable, including a call on a rare path such as json's non-string map-key branch.

#### The universe

Let `B` be the set of Go types that appear in reachable code: named types, literals, instantiations, and the types of those types' fields and elements. This set is finite and is exactly the types the program wrote down.

The universe `U` is `B` plus one constructor application, not the transitive closure:

- `*T` for every `T` in `B`
- `[]T` for every `T` in `B`
- `map[T]struct{}` for every comparable `T` in `B`

It is one step on purpose. Closing under both `*` and `[]` without a bound is infinite (`*[]*[]…`). A second constructor applied to a type that is in `U` but not in `B` is checked against `U` as it stands. `PointerTo(SliceOf(t))` is legal only when `*[]T` is already in `U`, which it is when `[]T` is in `B` (the program wrote a slice and we added one pointer step) and not merely because we added `[]T` ourselves.

`chan T`, `[n]T` for an `n` the program did not write, `map[K]V` for an arbitrary `V`, function types, and struct types are not added by the closure. They are in `U` only when they are in `B`.

#### What the compiler knows about an argument

Each IR value of type `reflect.Type` carries an abstract set:

| Origin | Set |
|---|---|
| `reflect.TypeOf(v)` of a concrete `v`, `reflect.TypeFor[T]()` | `{T}` |
| `reflect.TypeOf(v)` of an interface, or `Value.Type()` of a value that came from an interface | the concrete types that flow to that interface, computed whole-program. If the set cannot be bounded, `B` itself (every type the program contains). |
| `.Elem()`, `.Key()`, `.In(i)`, `.Out(i)`, `.Field(i).Type` of a type in `B` | the corresponding component, which is in `B` |
| `SliceOf`, `PointerTo`, `MapOf`, or any other constructor | the result type, marked constructed. It is in `U` and not thereby in `B`. |

`B` as the unknown set is sound because every `reflect.Type` that exists at run time came from a type in `B` or from a constructor we already accepted into `U`. A constructor argument marked constructed is not widened to `B`. The analysis does not pretend a synthesized type is one the user wrote.

#### When the call is legal

| Call | Legal when |
|---|---|
| `PointerTo(t)` | every possible `t` is in `B`. The result `*t` is the one pointer step. `PointerTo` of a constructed type is legal only when that specific `*U` is already in `U`. |
| `SliceOf(t)` | every possible `t` is in `B`. The result `[]t` is the one slice step. Same rule for a constructed argument. |
| `MapOf(k, v)` | every pair is in `U`. In particular, `v` exactly `struct{}` and `k` in `B` is legal, because `map[K]struct{}` was added for every comparable `K`. An incomparable `K` is upstream `MapOf`'s own panic, and we leave that panic in place. Any other unknown `v` is not legal. |
| `ChanOf(dir, t)` | the exact chan type, direction included, is in `B`. `t` drawn from all of `B` is not enough. We do not close under `chan`. |
| `ArrayOf(n, t)` | `n` is a constant on every reaching path and `[n]T` is in `B`. A non-constant `n` is not legal. |
| `StructOf(fields)` | the field slice is a constant the compiler can read (names, types, tags, embedding) and an existing struct in `B` has that shape. A loop that appends fields, which is how the json tests build a struct, is not a constant. |
| `FuncOf(in, out, variadic)` | the exact signature is in `B`. A non-constant slice of parameter types is not legal. |

A call that passes is lowered to a load of the descriptor global. It cannot miss at run time. There is no run-time fallback and no panic for "type not in the module".

#### The diagnostic

```
encoding/json/v2/fold_test.go:119:14: reflect.StructOf result is not a Wasm type in this program
    reflect.StructOf(fields)
    the field list is not a constant struct type this program already declares.
    Wasm GC cannot mint a struct type at run time.
    this program's universe is the types it writes, plus one step of *T, []T,
    and map[T]struct{} for a comparable T.
```

The compiler prints one error per call site, with the call expression and which argument it could not pin down.

#### Standard-library call sites in go1.27.4

The materializer builds `encoding/json` with `goexperiment.jsonv2` on, which is the Go 1.27 default. `encode.go` and `decode.go` are tagged `!goexperiment.jsonv2` and are not compiled. The live production calls are:

| Call | File | Verdict |
|---|---|---|
| `reflect.PointerTo(t)` inside `implements`, used to test whether `*T` has `Marshaler` / `Unmarshaler` | `encoding/json/v2/arshal_methods.go` | Legal. `t` is the type being marshaled, which is in `B`. `*t` is the one pointer step. |
| `reflect.PointerTo(from)` | `encoding/json/v2/arshal_funcs.go` | Legal, same reason. |
| `reflect.PointerTo(sf.Type)` when a struct field's `IsZero` method might be on the pointer | `encoding/json/v2/fields.go` | Legal. The field type is in `B`. |
| `reflect.SliceOf(t.Key())` and `reflect.SliceOf(t.Elem())`, building scratch slices for deterministic marshaling of a non-string map key | `encoding/json/v2/arshal_default.go` lines 913–914 | Legal. `t` is the map type flowing into `Marshal`, so the key and element are in `B`, and `[]K` / `[]V` are the one slice step. No overlay. |
| `reflect.MapOf(k.Type(), emptyStructType)` where `emptyStructType = reflect.TypeFor[struct{}]()` | `encoding/json/v2/arshal_default.go` line 1015 | Legal. The value type is exactly `struct{}` and the key is in `B`, so `map[K]struct{}` is in `U`. No overlay. |

`fmt` has no call to any of these seven functions outside tests. It compiles with no overlay and no special case.

These production calls are also legal under the same rule, in packages that are not on the v1 list. They do not need overlays if those packages are vendored later:

| Call | File | Verdict |
|---|---|---|
| `reflect.PointerTo` | `encoding/gob/type.go`, `encode.go`, `decode.go` | Legal. |
| `reflect.PointerTo(s.typ)` | `net/rpc/server.go` | Legal. |
| `reflect.PointerTo(value.Type())` | `text/template/exec.go` | Legal. |

These calls are reachable from tests and are compile errors. They are not patched. `wasmgc-go test` records them in `skip.txt` (`08-testing.md`):

| Call | Where | Why it fails |
|---|---|---|
| `reflect.StructOf(fs)` in a loop that renames the field | `encoding/json/bench_test.go`, `BenchmarkTypeFieldsCache` | The field list is not a constant struct in `B`. |
| `reflect.StructOf(fields)` with `X0`…`X99` | `encoding/json/v2/arshal_test.go`, the `Structs/OmitEmpty/PathologicalBreadth` case | Same. |
| `reflect.StructOf(fields)` with `Name0`… | `encoding/json/v2/fold_test.go`, `TestBenchmarkUnmarshalUnknown` | Same. |
| `reflect.StructOf` / `reflect.ArrayOf` building a nested type that is not in the program | `encoding/gob/gobencdec_test.go` | `ArrayOf(1, typ)` only works when `[1]T` is already in `B`. The test's `typ` is not. |

No overlay deletes these calls from the library. The library sources do not contain them. The test files do, and the skip list is how a test binary that would otherwise be a compile error is set aside.

`reflect.Value.Pointer` and `reflect.Value.UnsafePointer` do not return an integer address. `UnsafePointer` returns the `unsafe.Pointer` (`anyref`) of the value when the value is a pointer, and panics otherwise. `Pointer` panics for every value: there is no integer to return. Callers that want a map key or an identity use `UnsafePointer` and `ref.eq`. This is the break with upstream `reflect` that cgo-style code will feel first, and the panic string says so.

## `internal/abi` and `internal/reflectlite`

`internal/abi.NoEscape` is the identity, and the escape pass treats a call to it as not a leak. wasm3 did the same after the `uintptr ^ 0` form stopped compiling (`docs/wasm3-study/03-stdlib-and-unsafe.md`). `internal/abi.Escape` is a call that counts as a leak.

`internal/reflectlite` is the subset of `reflect` that `errors` and `sort` use (`Type`, `Value`, `Elem`, `Interface`). It is implemented on the same descriptors, not as a panic stub. The upstream file walks `abi.Type` with pointer arithmetic and is not compiled.
