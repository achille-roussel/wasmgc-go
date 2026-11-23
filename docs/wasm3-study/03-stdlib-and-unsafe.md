# wasm3 standard library, unsafe, and the linear-memory boundary

HEAD `c8232bb660`. The compiler rejects the conversion that almost all low-level Go uses to walk memory:

```
wasm3: conversion between unsafe.Pointer and uintptr is not supported
(no scalar address for WasmGC refs); rewrite using runtime/wasm or
unsafe.SliceData/StringData
```

`src/cmd/compile/internal/ssagen/ssa.go:3520–3539`. It fires for both directions, and it runs before the normal pointer-shaped `OpConvert` so it is not optimized away (`ssa.go:3534–3536`). `unsafe.Pointer` is an `anyref`. `uintptr` is an `i64`. There is no instruction that turns one into the other.

That single fatal is why the stdlib changes exist. Anything that does `unsafe.Pointer(uintptr(p)+n)`, compares two pointers by casting them to `uintptr`, or loads a struct by overlaying it on a byte slice, does not compile. The branch's answer is a `*_wasm3.go` mirror with the arithmetic deleted, or a `//go:build !wasm3` tag on the original file.

A new compiler that uses `go/types` and its own runtime does not need these mirrors. It needs the list of language patterns that cannot be expressed, which is what this file is.

## What still has a linear address

`src/runtime/wasm/linearmem_wasm3.go:7–16`: linear memory is only the buffer a non-GC host can read. A pointer handed to the host is a `uint32` offset. The bump pointer is wasm global 0. The three intrinsics are `WriteLinearMemory`, `ReadLinearMemory`, `ResetLinearMemory`.

Everything else — strings, slices, structs, maps, interfaces — is a wasm GC ref. `runtime.mallocgc` throws if called (`src/runtime/mallocgc_wasm3.go:23–28`).

Consequences that showed up in real packages:

| Pattern | Why it breaks | What the branch did |
|---|---|---|
| `uintptr(unsafe.Pointer(p)) + n` | No scalar address | Compile error. Mirrors panic or use a typed accessor |
| `unsafe.Pointer(uintptr(p) ^ 0)` (`abi.NoEscape`) | Same conversion | Identity function, `//go:noinline` (`internal/abi/escape_wasm3.go:7–29`) |
| Two-word interface (`tab, data *uintptr`) | Interface is one `$go.iface` ref | `atomic.Value` is a plain `any` (`sync/atomic/value_wasm3.go:12–21`) |
| Swiss-table maps, control bytes, `groupReference` | Pointer plus index into a group | Package tagged `!wasm3`. Compiler two-array map instead (`internal/runtime/maps/maps_wasm3.go:7–18`) |
| `memmove` overlap test `uintptr(to) > uintptr(from)` | Same conversion | Byte copy through `unsafe.Slice`, no overlap compare (`runtime/memmove_wasm3.go:26–32`) |
| `reflect` walking `abi.Type` trailing bytes | Descriptor is an opaque `$go.object` global, not a byte struct | Helpers panic (`internal/abi/type_wasm3.go:12–16`, string at line 200) |
| Assembly that assumes a linear Go stack (`*_wasm.s`) | No stack frame, no SP | `.s` files deleted or replaced by intrinsics (floor, memclr, `math/big` arith, cgo) |
| cgo | No C stack, no pointer passing | `runtime/cgo_wasm3.go` nil function pointers. `runtime/cgo/asm_wasm3.s` deleted (`7d4a038aaa`) |
| `encoding/binary` overlay of a struct on `[]byte` | Needs the address of a struct and a size | Not rewritten. Only the little-endian build tag was widened (`encoding/binary/native_endian_little.go`). A program that does this will hit the uintptr fatal or a bad `unsafe.Slice` |
| `syscall/js` | Its wasmimport signatures use types the wasm3 import checker rejects | js/wasm3 does not import `syscall/js`. `syscall/fs_jswasm3.go:7–11` says so and returns `ENOSYS` except for stdio through `runtime.write1Bytes` |

`unsafe.String`, `unsafe.Slice`, `unsafe.StringData`, and `unsafe.SliceData` are the supported replacements, and only when the compiler intrinsifies them into `array` operations. `gostringnocopy` was rewritten to `unsafe.String` (`6f466710d3`). `rawbyteslice` / `gobytes` use `unsafe.Slice` (`fc66c00062`). `memequal` compares through `unsafe.Slice` (`4177b63818`). These are not a back door to pointer arithmetic: `SliceData` returns a ref to the backing array (or a fat pointer to element 0), not an integer.

`runtime/unsafe_wasm3.go:33–36` drops the `uintptr(len) > -uintptr(ptr)` wrap check inside `unsafe.String`/`unsafe.Slice` "because anyref has no scalar address space to wrap past".

## Runtime

About 70 `*_wasm3.go` files. The pattern is: tag the upstream file `!wasm3`, add a mirror that compiles.

Deleted in substance (stubs in `src/runtime/gc_stubs_wasm3.go`, header lines 14–22): marker, sweeper, scavenger, heap, cache, page allocator, write barrier, finalizer. `SetFinalizer` and `AddCleanup` are no-ops. The comment says every stub "should not be reached"; a call means some file was not excluded.

Kept as real code, rewritten:

- `printnum_wasm3.go`, `printstring_wasm3.go`, `write1_jswasm3.go` — print into a wasm array, then copy to linear memory for the host.
- `string_wasm3.go`, `slice_wasm3.go`, `makeslice_wasm3.go` — headers and growth via intrinsics. `growslice` throws if the intrinsic does not fire.
- `memmove_wasm3.go`, `memequal_wasm3.go`, `mem_wasm3.go` — Go loops over slices.
- `chan_jswasm3.go`, `defer_wasm3.go`, `panic_wasm3.go`, `spawn_jswasm3.go`, `sched_jswasm3.go` — the JSPI runtime (`02`).
- `iface_wasm3.go` — `itabsinit` is empty; `convT*` for dynamic boxing is incomplete (several paths return nil or trap). Static conversions the compiler sees become `OpWasm3IfaceMake`.
- `type_wasm3.go` — type equality and a few algorithms that do not walk bytes. Name and method-list accessors are not here; those live in `internal/abi` and panic.
- `proc_wasm3.go` — a large mirror of `proc.go` so the package links, with `schedule` / `execute` / `goready` throwing on js because JSPI replaced them (`3431c8f7fb`).
- `stack_wasm3.go` — `stackalloc` throws.
- `float_wasm3.go` — bit casts are compiler intrinsics whose Go bodies are self-recursive so the frontend has a declaration and the body is never executed (`84ab00ea4a`, `d74b8f9988`). Same trick in `internal/runtime/atomic/types_wasm3.go` for `Float64`.
- `alg_wasm3.go`, `alg_string_wasm3.go`, `alg_iface_wasm3.go` — equality. Interface hashing is the stub in `maps_wasm3.go` that returns the seed.

`runtime/wasm` is the tiny intentional API: JSPI imports, linear-memory bridge, spawn, and the wasmfx intrinsics that panic if the compiler does not replace them.

`GOOS=wasip1 GOARCH=wasm3` does not build at HEAD. `src/runtime/netpoll_wasm3.go:413–418` and `:544` call `wasm.MintParkID`, `wasm.AwaitFdReadable`, `wasm.AwaitFdWritable`, and `wasm.WasmReady`, which are declared only in js-tagged files (`jspi_jswasm3.go`, `io_jswasm3.go`). This study's `make.bash` then `go build` of an empty `main` failed there. `GOOS=js` builds. The wasip1 launcher script in `lib/wasm` is from an earlier point in the branch.

## `internal/abi`

`type_wasm3.go` is a mirror of `type.go` except the pointer-walking helpers panic until "M6" (lines 7–16). The panic string (line 200):

```
internal/abi: pointer-arithmetic reflect helpers are not yet supported on GOARCH=wasm3 (M6 work)
```

`escape_wasm3.go`: `NoEscape` returns its argument and must not be inlined, because the usual `uintptr ^ 0` launder is what stops escape analysis. A wasm3-specific carve-out in `cmd/compile/internal/escape` drops the escape at the call. If someone inlines the identity, the argument escapes for real.

`abi_wasm3.go`: zero native register arguments. `IntRegArgAddr` no longer does pointer arithmetic. On this target the Go register ABI's "pass the rest in memory at SP+n" concept does not exist; wasm parameters are the registers (`02`).

## `internal/runtime/atomic` and `sync`

`atomic_wasm3.go:5–6`:

```
// TODO(neelance): implement with actual atomic operations as soon as threads are available
// See https://github.com/WebAssembly/design/issues/1073
```

Every `Load` / `Store` / `Cas` is a plain Go read or write. Correct only because the runtime assumes one wasm activation runs at a time (`spawn_jswasm3.go:43–46`). `Cas64`, `Xchg`, and friends are not atomic with respect to a host that re-enters, and they are not atomic with respect to wasm threads.

`xchg8_wasm3.go` cannot use the alignment trick the generic file uses, because that trick is pointer arithmetic.

`sync/atomic.Value` (`value_wasm3.go:12–21`) stores an `any`. `Load` and `Store` are field accesses. The comment says they are "not yet atomic" and points at M4. M4's JSPI model still runs one activation at a time, so the data race the comment worries about does not happen on one JS thread. The type is still not an atomic.

`sync/cond_check_wasm3.go` skips the "copied Cond" detector, which hashed `uintptr(unsafe.Pointer(c))`. `sync/pool_race_wasm3.go` stubs the race-detector address hash. `sync.Pool` itself is the upstream code plus those stubs; it works as a cache only to the extent the rest of `sync` does not park on a mutex implementation that needs `gopark`. Mutex parking goes through `sema` → `gopark` → `WasmPark` on js. That path exists and was not exercised in this study's samples.

## Maps

`internal/runtime/maps`: swiss table excluded. `maps_wasm3.go` keeps a `Map` struct `{used uint64; dirPtr unsafe.Pointer}` so `len` still loads offset 0, and panics from iterators. Hash functions return the seed (lines 67–79) and say this is correctness-breaking if anything uses them as a hash table.

`runtime_faststr_wasm3.go:5–33` is a linear scan of `map[string]V`. Keys are a WasmGC `[]string`. Values are still "a linear-memory bump-allocated region" (`values unsafe.Pointer`, lines 49–62). Other key classes "will trap if reached" (lines 30–33).

The compiler's `$go.map.K.V` (`01`) is a different type: `i32` used/cap and two wasm arrays, both on the GC heap. `map[int]int` in this study's sample ran and printed `2 20 30` after `delete`, so the compiler path for integer keys executes. `map[string]int` printed `1 2 2`. Which of the two string-map implementations produced that output was not separated; both are linked in spirit, and the compiler intrinsic is what a program hits when the map ops are lowered. The faststr file is what a call to `runtime.mapassign_faststr` hits. A new compiler should implement one of them. The two-array GC map is the one that does not need linear memory.

## `math`, `math/bits`, `math/big`, hashes

`math/floor_wasm3.go`: `archFloor` / `Ceil` / `Trunc` are wasm ops. The `.s` file was a linear-memory frame (`e57b30bb02`).

`math/bits/bits_wasm3.go`: `Div32` / `Div64` panic with an inline `errorString` instead of a package-level interface variable, because materializing that interface hit the polyptr gap (`e7762ce701`).

`math/big`: wasm3 is in the pure-Go arith build (`arith_decl_pure.go`). `arith_wasm3.s` was deleted (`4dcd623ded`).

`internal/chacha8rand`: the generic block, with the working array flattened from `[4][16]uint32` to a 1-D array because the 2-D array classifier mis-lowered (`395c7306f5`, `8854ac4154`). No `unsafe` reinterpret of the state.

`crypto/internal/fips140/alias/alias_wasm3.go`: overlap checks cannot use `uintptr(&x[0])`, so they return the conservative "may alias" answer.

`crypto/internal/sysrand`, `x509/root_wasm3.go`, FIPS entropy: js/wasm stubs copied across, no real OS randomness beyond what the js host could provide. The minimal glue does not import a random source. `crypto/rand` on this target is not something this study ran.

## `os`, `syscall`, `time`, `net`, `testing`

`syscall/syscall_jswasm3.go` and `tables_jswasm3.go` are a typed mirror of the js syscall layer so the package links. `fs_jswasm3.go` returns `ENOSYS` for filesystem calls. Stdio goes through `write1Bytes`. There is no `syscall/js` `Value` tree: that package's wasmimports pass `js.Value` and similar, which do not match the numeric wasmimport ABI.

`os/pipe_wasm3.go`: `ENOSYS`. `os/executable_wasm3.go`, `os/exec/lp_wasm3.go`: no host path and no `exec`. `testing/run_example_wasm3.go` captures example output with a temp file because `os.Pipe` does not exist; that file's build tag is the js/wasip1 example runner, and a temp file will also fail if the filesystem is `ENOSYS`. Example tests are not a working feature.

`time/zoneinfo_jswasm3.go`: local zone is UTC. No `syscall/js` timezone lookup.

`net/http/transport_default_wasm3.go`: default dialer is nil, same as `GOARCH=wasm`. `internal/poll/fd_poll_jswasm3.go` is a stub `pollDesc`. `netpoll_wasm3.go` is where wasip1 currently fails to compile (js-only imports referenced from a file built for both OSes). `doc/wasm3-netpoll-design.md` describes a future `AwaitFd*` bridge and explicitly defers raw TCP and UDP.

## `slices`, `encoding`, coverage, cgo

`slices/overlaps_wasm3.go` always returns false. The comment (lines 9–20) says `array.copy` on a WasmGC backing handles overlap, so the overlap check that used pointer ranges is unnecessary. That is true for two slices of the same wasm array. It is false if a future representation puts different slices on different arrays that alias at the Go level; with the current one-array-per-backing model they do not.

`encoding/binary` was not ported. `binary.Read` of a struct uses `unsafe` to address fields. It will not compile, or it will compile and trap, depending on whether the particular function hits the uintptr fatal. This study did not compile `encoding/binary`'s tests.

`internal/coverage/rtcov/rtcov_wasm3.go`: `AddMeta` is a no-op. Coverage counters that live in a linear-memory struct were cleared rather than interpreted (`ccbfecd94b`).

`runtime/cgo_wasm3.go`: cgo is unsupported. The assembly file is gone.

## `reflect` and `go/types`

`src/reflect` has no wasm3 implementation. It type-checks only where it does not contain a uintptr cast that survives to SSA. `internal/reflectlite` is stubbed so `errors` and `os` can import it (`dbbe400893`).

`go/types` and `cmd/compile/internal/types2` gained a `wasm3` entry in the `Sizes` tables (`doc/wasm3-triage.md` section E) so `go vet` does not panic. Sizes follow `PtrSize == 8`. The `intRegs = 1` overrides for string, slice, interface, struct, and array are in `cmd/compile/internal/types/size.go` (see `01`), not in `go/types`. `go/types.StdSizes` still reports a string as two words. A tool using `go/types` sizes to lay out wasm memory will disagree with this compiler. A new compiler should define sizes in one place and not pretend a string is 16 bytes if it is one reference.

## Integer printing bug found while running

`printuint` (`src/runtime/printnum_wasm3.go:19–30`) fills a `[21]byte` from the end but slices `buf[i:]` after the loop. `i` starts at 20 and is decremented before the store, so the slice includes index 20, which was never written and is zero. This study ran `println(fib(20))` and the host observed the bytes `6765` plus a NUL, then a newline from `println`. `println(len(s), s[3])` for a 4-element slice was the two writes `"4\u0000"` and `"4\u0000"`. String `println("hello, world")` does not use this function and had no extra NUL. The off-by-one is visible in the source: the initial `i := size-1` plus a pre-decrement leaves one zero byte inside the slice.

## What a standalone compiler should treat as hard requirements

These are properties of the wasm GC heap, not of this fork:

1. No integer address of a GC object. `uintptr(unsafe.Pointer)` cannot exist. APIs that return `uintptr` for a pointer (`reflect.Value.Pointer`, `unsafe.Pointer` arithmetic, cgo handles) need a different design or a refusal.
2. `unsafe.Slice` / `unsafe.String` are expressible only as views of a wasm array plus offset and length, which is what the string and slice headers already are.
3. Structs are not byte-addressable. `encoding/binary`, `gob` type-punning, and `sync/atomic` word-tearing of an interface have to be special-cased or dropped.
4. Assembly files from other GOARCHes are meaningless. Intrinsics (`I64TruncSat`, `array.copy`, `memory.copy` for the bridge only) replace them.
5. The standard library's runtime is not portable to this model. The branch's mirrors are a catalog of every file that assumed a linear heap. A new runtime should be written against the wasm types, small enough that `mallocgc` does not exist.

The packages that compiled and ran in this study without those assumptions — arithmetic, slices of integers, maps of integers and strings, print, defer on one goroutine — did so because the compiler intrinsified them before they reached the mirrors.
