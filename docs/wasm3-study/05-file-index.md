# wasm3 file index

Every file in `git diff --name-status 7601c4bf..HEAD`. That commit is the upstream Go parent of the experiment. `origin/master` on this fork is an older Go 1.23-dev snapshot and is the wrong base. `A` = added, `M` = modified. 519 files, +80502 / −1477 lines.

## Key commits

| SHA | What it changed |
|---|---|
| `0ae47fcbc6` | GOARCH=wasm3 registered as a clone of wasm (M0). |
| `9d7bce37d9` | The wasm compiler backend forked into package wasm3. |
| `deef8bffca` | GC type model and recursion-group ordering. |
| `e9c6acbf19` | Go signatures lowered to a typed wasm function ABI. |
| `afc48ca883` | Hello World with print runs end to end. |
| `078b1ca922` | M2 size audit: empty main 1775 bytes. |
| `c975780b36` | Regalloc bypass: one local per SSA value, phi copies on CFG edges. |
| `0e73fe4570` | JSPI suspend and resume run on Node. |
| `098fc303ae` | wasm.Spawn starts a second JSPI stack. |
| `8804f9002a` | The go keyword calls newprocJSWasm3. |
| `6972d70bdc` | Spawn FIFO replaces the single global slot. |
| `65a5736298` | Unbuffered channels write the slot before parking. |
| `23ff200e8b` | Polymorphic fat pointer ($go.ptr.poly) becomes the scalar-pointer representation. |
| `38164cb190` | Defer runs on a normal return. |
| `27253aee23` | A recovered panic ends in exit(0). |
| `594566ff4f` | Interface method dispatch via a vtab funcref. The value is still $go.iface. |
| `0a737d78d7` | If no body uses cont.new, the linker rewrites $go.cont to a plain func so JSPI does not need wasmfx. |
| `4a164c573d` | Swiss-table maps tagged !wasm3. |
| `e7762ce701` | Last code commit, 2026-06-08. math/bits panics use an inline error string. |
| `c8232bb660` | HEAD, 2026-08-11. Adds the ssa.html dumps and nothing else. |

612 commits in total, all by Achille Roussel, no merges. The table is the spine; `git log --reverse 7601c4bf..HEAD` is the rest.

## Design notes and WAT spikes

| | Path | What it is |
|---|---|---|
| A | `doc/wasm3-array-valuecopy-derisk.wat` | WAT spike for copying composite array elements. |
| A | `doc/wasm3-chan-subtyping.md` | Chan as a $go.chan subtype. Trust the implementation-deltas section, not the first diagram. |
| A | `doc/wasm3-design.md` | Original design: host GC, no interior refs, stack switching, EH. Several §6 shapes were later revised. |
| A | `doc/wasm3-eh-defer-derisk.run.sh` | Runner for the exception-handling WAT spike. |
| A | `doc/wasm3-eh-defer-derisk.wat` | WAT spike for try_table defer. The compiler does not emit this. |
| A | `doc/wasm3-fat-pointer-derisk.wat` | WAT spike for the accessor-pair fat pointer. |
| A | `doc/wasm3-fat-pointers-design.md` | Interior-pointer design. The {container,i32} form was superseded by $go.ptr.poly for scalars. |
| A | `doc/wasm3-fat-ptr-class-prototype/README.md` | Prototype notes for the class-indirected fat pointer. |
| A | `doc/wasm3-fat-ptr-class-prototype/prototype.wasm` | Assembled fat-pointer prototype module. |
| A | `doc/wasm3-fat-ptr-class-prototype/prototype.wat` | WAT for $go.ptr.poly and the refclass vtable. |
| A | `doc/wasm3-fat-ptr-class-prototype/run.js` | Node runner for the fat-pointer prototype. |
| A | `doc/wasm3-iface-boxing-derisk.wat` | WAT spike for boxing an interface as one struct. |
| A | `doc/wasm3-iface-poly-derisk.run.sh` | Runner for the interface WAT spike. |
| A | `doc/wasm3-iface-poly-derisk.wat` | WAT spike for the polymorphic interface and vtab shape. |
| A | `doc/wasm3-js-chan-status.md` | Channel-on-JSPI status, including the generic-dictionary detour. |
| A | `doc/wasm3-js-go-keyword-status.md` | `go` routes to Spawn. The single-slot limitation in this doc is stale; the code is a FIFO. |
| A | `doc/wasm3-js-m4-complete.md` | JSPI canary: two activations and a hand-rolled channel. |
| A | `doc/wasm3-js-status.md` | Early JSPI status. The 'scheduler not wired' section is stale. |
| A | `doc/wasm3-m2-cutover-notes.md` | Working notes from the M2 compiler and linker cutover. |
| A | `doc/wasm3-m2-design.md` | M2 cutover design (typed ABI, early object model). |
| A | `doc/wasm3-m2-size-audit.md` | 2026-05-16 sizes: empty main 1775 bytes vs 1.8 MB. Historical, pre-JSPI. |
| A | `doc/wasm3-m2-spike.wat` | Hand-written WAT spike for the M2 object model. |
| A | `doc/wasm3-m3-captures-in-struct.md` | Closure captures stored as typed struct fields. |
| A | `doc/wasm3-m3-no-regalloc.md` | Why wasm3 skips regalloc: one SSA value, one wasm local. |
| A | `doc/wasm3-m3-notes.md` | Long M3 working notes (struct field limits, captures, blockers). |
| A | `doc/wasm3-m3-relooper-design.md` | Structured control flow via a relooper, with a br_table fallback. |
| A | `doc/wasm3-m3-stage-f-interfaces.md` | Interface lowering notes from M3. |
| A | `doc/wasm3-m4-status.md` | Wasmfx goroutines: toolchain emits them, engines do not resume. JSPI is the alternative. |
| A | `doc/wasm3-netpoll-design.md` | Planned JSPI fd bridge. TCP/UDP deferred. Not wired; wasip1 does not link. |
| A | `doc/wasm3-ref-element-derisk.wat` | WAT spike for ref-typed array elements. |
| A | `doc/wasm3-runtime-rung0-inventory.md` | Inventory of runtime pointer-arithmetic sites before the mirror wave. |
| A | `doc/wasm3-slice-boxing.md` | Boxed string/slice/interface ABI (one ref). Matches the code. |
| A | `doc/wasm3-stage-j-plan.md` | Plan to retire linear-memory shims. Two attempts were reverted. |
| A | `doc/wasm3-stage-j-progress.md` | Stage J paused. The status table in this file is stale. |
| A | `doc/wasm3-string-const-derisk.wat` | WAT spike for string literals via array.new_data. |
| A | `doc/wasm3-triage.md` | M0 inventory of every wasm arch check that had to be widened. Historical. |
| A | `src/ssa.html` | Second copy of the SSA HTML dump. |
| A | `ssa.html` | SSA HTML dump from the last commit. Not a design doc. |

## JS host glue

| | Path | What it is |
|---|---|---|
| A | `lib/wasm/go_js_wasm3_exec` | Shell launcher: node, the stack-switching flag, and the JS glue. |
| A | `lib/wasm/go_wasip1_wasm3_exec` | Shell launcher for wasip1/wasm3. That GOOS does not link at HEAD. |
| A | `lib/wasm/wasm_exec_wasm3.js` | Extended Node host: JSPI plus an fd registry for the unfinished poller. |
| A | `misc/wasm/wasm_exec_wasm3.js` | Minimal Node host. Uses WebAssembly.Suspending, absent on Node 22.23. |

## Compiler backend

| | Path | What it is |
|---|---|---|
| A | `src/cmd/compile/internal/ir/wasm3.go` | IR helpers used only by the wasm3 backend. |
| A | `src/cmd/compile/internal/reflectdata/wasm3_changen.go` | Per-element-type channel trampolines. |
| A | `src/cmd/compile/internal/reflectdata/wasm3_mapgen.go` | Per-type map trampolines. |
| A | `src/cmd/compile/internal/ssa/_gen/Wasm3.rules` | Rewrite rules from generic SSA ops to Wasm3 ops. |
| A | `src/cmd/compile/internal/ssa/_gen/Wasm3Ops.go` | SSA opcode list for the wasm3 backend. |
| A | `src/cmd/compile/internal/ssa/_gen/Wasm3latelower.rules` | Late Wasm3 rewrite rules. |
| A | `src/cmd/compile/internal/ssa/rewriteWasm3.go` | Generated from Wasm3.rules. |
| A | `src/cmd/compile/internal/ssa/rewriteWasm3latelower.go` | Generated from Wasm3latelower.rules. |
| A | `src/cmd/compile/internal/ssa/wasm3_relayout.go` | Post-pass layout fixups for wasm3 locals. |
| A | `src/cmd/compile/internal/ssa/wasm3place.go` | One wasm local per SSA value. regalloc returns immediately after this. |
| A | `src/cmd/compile/internal/wasm3/cfg.go` | Relooper from the SSA CFG to block/loop/br, with a dispatch fallback. |
| A | `src/cmd/compile/internal/wasm3/cfg_test.go` | Relooper tests. |
| A | `src/cmd/compile/internal/wasm3/ssa.go` | Genssa for every Wasm3 op: fat pointers, maps, chans, interfaces, calls. |
| A | `src/cmd/compile/internal/wasm3/wasmabi.go` | Signatures, closure types, interface vtabs, per-function type tables. |
| A | `src/cmd/compile/internal/wasm3/wasmabi_test.go` | ABI lowering tests. |
| A | `src/cmd/compile/internal/wasm3/wasmtype.go` | Go type to wasm GC type: strings, slices, structs, maps, chans, pointers. |
| A | `src/cmd/compile/internal/wasm3/wasmtype_test.go` | Type-collector tests. |

## Rest of cmd/compile

| | Path | What it is |
|---|---|---|
| M | `src/cmd/compile/internal/abi/abiutils.go` | cmd/compile taught about wasm3, or a pass that emits or skips a wasm3-specific form. |
| M | `src/cmd/compile/internal/base/flag.go` | cmd/compile taught about wasm3, or a pass that emits or skips a wasm3-specific form. |
| M | `src/cmd/compile/internal/escape/call.go` | cmd/compile taught about wasm3, or a pass that emits or skips a wasm3-specific form. |
| M | `src/cmd/compile/internal/escape/escape.go` | cmd/compile taught about wasm3, or a pass that emits or skips a wasm3-specific form. |
| M | `src/cmd/compile/internal/ir/symtab.go` | cmd/compile taught about wasm3, or a pass that emits or skips a wasm3-specific form. |
| M | `src/cmd/compile/internal/liveness/plive.go` | cmd/compile taught about wasm3, or a pass that emits or skips a wasm3-specific form. |
| M | `src/cmd/compile/internal/noder/linker.go` | cmd/compile taught about wasm3, or a pass that emits or skips a wasm3-specific form. |
| M | `src/cmd/compile/internal/noder/noder.go` | cmd/compile taught about wasm3, or a pass that emits or skips a wasm3-specific form. |
| M | `src/cmd/compile/internal/noder/reader.go` | cmd/compile taught about wasm3, or a pass that emits or skips a wasm3-specific form. |
| M | `src/cmd/compile/internal/noder/writer.go` | cmd/compile taught about wasm3, or a pass that emits or skips a wasm3-specific form. |
| M | `src/cmd/compile/internal/reflectdata/alg.go` | cmd/compile taught about wasm3, or a pass that emits or skips a wasm3-specific form. |
| M | `src/cmd/compile/internal/reflectdata/reflect.go` | cmd/compile taught about wasm3, or a pass that emits or skips a wasm3-specific form. |
| M | `src/cmd/compile/internal/ssa/_gen/dec.rules` | cmd/compile taught about wasm3, or a pass that emits or skips a wasm3-specific form. |
| M | `src/cmd/compile/internal/ssa/_gen/generic.rules` | cmd/compile taught about wasm3, or a pass that emits or skips a wasm3-specific form. |
| M | `src/cmd/compile/internal/ssa/_gen/rulegen.go` | cmd/compile taught about wasm3, or a pass that emits or skips a wasm3-specific form. |
| M | `src/cmd/compile/internal/ssa/compile.go` | cmd/compile taught about wasm3, or a pass that emits or skips a wasm3-specific form. |
| M | `src/cmd/compile/internal/ssa/config.go` | cmd/compile taught about wasm3, or a pass that emits or skips a wasm3-specific form. |
| M | `src/cmd/compile/internal/ssa/deadstore.go` | cmd/compile taught about wasm3, or a pass that emits or skips a wasm3-specific form. |
| M | `src/cmd/compile/internal/ssa/decompose.go` | cmd/compile taught about wasm3, or a pass that emits or skips a wasm3-specific form. |
| M | `src/cmd/compile/internal/ssa/expand_calls.go` | cmd/compile taught about wasm3, or a pass that emits or skips a wasm3-specific form. |
| M | `src/cmd/compile/internal/ssa/func.go` | cmd/compile taught about wasm3, or a pass that emits or skips a wasm3-specific form. |
| M | `src/cmd/compile/internal/ssa/looprotate.go` | cmd/compile taught about wasm3, or a pass that emits or skips a wasm3-specific form. |
| M | `src/cmd/compile/internal/ssa/lower.go` | cmd/compile taught about wasm3, or a pass that emits or skips a wasm3-specific form. |
| M | `src/cmd/compile/internal/ssa/memcombine.go` | cmd/compile taught about wasm3, or a pass that emits or skips a wasm3-specific form. |
| M | `src/cmd/compile/internal/ssa/op.go` | cmd/compile taught about wasm3, or a pass that emits or skips a wasm3-specific form. |
| M | `src/cmd/compile/internal/ssa/opGen.go` | cmd/compile taught about wasm3, or a pass that emits or skips a wasm3-specific form. |
| M | `src/cmd/compile/internal/ssa/regalloc.go` | cmd/compile taught about wasm3, or a pass that emits or skips a wasm3-specific form. |
| M | `src/cmd/compile/internal/ssa/rewrite.go` | cmd/compile taught about wasm3, or a pass that emits or skips a wasm3-specific form. |
| M | `src/cmd/compile/internal/ssa/rewritedec.go` | cmd/compile taught about wasm3, or a pass that emits or skips a wasm3-specific form. |
| M | `src/cmd/compile/internal/ssa/rewritegeneric.go` | cmd/compile taught about wasm3, or a pass that emits or skips a wasm3-specific form. |
| M | `src/cmd/compile/internal/ssa/value.go` | cmd/compile taught about wasm3, or a pass that emits or skips a wasm3-specific form. |
| M | `src/cmd/compile/internal/ssagen/abi.go` | cmd/compile taught about wasm3, or a pass that emits or skips a wasm3-specific form. |
| M | `src/cmd/compile/internal/ssagen/arch.go` | cmd/compile taught about wasm3, or a pass that emits or skips a wasm3-specific form. |
| M | `src/cmd/compile/internal/ssagen/intrinsics.go` | cmd/compile taught about wasm3, or a pass that emits or skips a wasm3-specific form. |
| M | `src/cmd/compile/internal/ssagen/pgen.go` | cmd/compile taught about wasm3, or a pass that emits or skips a wasm3-specific form. |
| M | `src/cmd/compile/internal/ssagen/ssa.go` | cmd/compile taught about wasm3, or a pass that emits or skips a wasm3-specific form. |
| M | `src/cmd/compile/internal/staticinit/sched.go` | cmd/compile taught about wasm3, or a pass that emits or skips a wasm3-specific form. |
| M | `src/cmd/compile/internal/typecheck/_builtin/runtime.go` | cmd/compile taught about wasm3, or a pass that emits or skips a wasm3-specific form. |
| M | `src/cmd/compile/internal/typecheck/builtin.go` | cmd/compile taught about wasm3, or a pass that emits or skips a wasm3-specific form. |
| M | `src/cmd/compile/internal/types/size.go` | cmd/compile taught about wasm3, or a pass that emits or skips a wasm3-specific form. |
| M | `src/cmd/compile/internal/types/sym.go` | cmd/compile taught about wasm3, or a pass that emits or skips a wasm3-specific form. |
| M | `src/cmd/compile/internal/types2/gccgosizes.go` | cmd/compile taught about wasm3, or a pass that emits or skips a wasm3-specific form. |
| M | `src/cmd/compile/internal/types2/sizes.go` | cmd/compile taught about wasm3, or a pass that emits or skips a wasm3-specific form. |
| M | `src/cmd/compile/internal/walk/assign.go` | cmd/compile taught about wasm3, or a pass that emits or skips a wasm3-specific form. |
| M | `src/cmd/compile/internal/walk/builtin.go` | cmd/compile taught about wasm3, or a pass that emits or skips a wasm3-specific form. |
| M | `src/cmd/compile/internal/walk/closure.go` | cmd/compile taught about wasm3, or a pass that emits or skips a wasm3-specific form. |
| M | `src/cmd/compile/internal/walk/compare.go` | cmd/compile taught about wasm3, or a pass that emits or skips a wasm3-specific form. |
| M | `src/cmd/compile/internal/walk/complit.go` | cmd/compile taught about wasm3, or a pass that emits or skips a wasm3-specific form. |
| M | `src/cmd/compile/internal/walk/convert.go` | cmd/compile taught about wasm3, or a pass that emits or skips a wasm3-specific form. |
| M | `src/cmd/compile/internal/walk/expr.go` | cmd/compile taught about wasm3, or a pass that emits or skips a wasm3-specific form. |
| M | `src/cmd/compile/internal/walk/order.go` | cmd/compile taught about wasm3, or a pass that emits or skips a wasm3-specific form. |
| M | `src/cmd/compile/internal/walk/range.go` | cmd/compile taught about wasm3, or a pass that emits or skips a wasm3-specific form. |
| M | `src/cmd/compile/internal/walk/select.go` | cmd/compile taught about wasm3, or a pass that emits or skips a wasm3-specific form. |
| M | `src/cmd/compile/internal/walk/walk.go` | cmd/compile taught about wasm3, or a pass that emits or skips a wasm3-specific form. |
| M | `src/cmd/compile/main.go` | cmd/compile taught about wasm3, or a pass that emits or skips a wasm3-specific form. |

## Assembler, wasmgc, linker, dist

| | Path | What it is |
|---|---|---|
| M | `src/cmd/asm/internal/arch/arch.go` | Toolchain driver (dist, asm, compile, or link) taught the wasm3 GOARCH name. |
| M | `src/cmd/dist/build.go` | Toolchain driver (dist, asm, compile, or link) taught the wasm3 GOARCH name. |
| M | `src/cmd/dist/buildtool.go` | Toolchain driver (dist, asm, compile, or link) taught the wasm3 GOARCH name. |
| M | `src/cmd/internal/goobj/objfile.go` | Shared internal: arch record, object file, or relocation used by wasm3. |
| M | `src/cmd/internal/obj/fips140.go` | Shared internal: arch record, object file, or relocation used by wasm3. |
| M | `src/cmd/internal/obj/link.go` | Shared internal: arch record, object file, or relocation used by wasm3. |
| M | `src/cmd/internal/obj/objfile.go` | Shared internal: arch record, object file, or relocation used by wasm3. |
| M | `src/cmd/internal/obj/sym.go` | Shared internal: arch record, object file, or relocation used by wasm3. |
| M | `src/cmd/internal/obj/wasm/a.out.go` | Wasm assembler: new opcodes, relocations, or a wasm3 path through the object writer. |
| M | `src/cmd/internal/obj/wasm/anames.go` | Wasm assembler: new opcodes, relocations, or a wasm3 path through the object writer. |
| A | `src/cmd/internal/obj/wasm/wasm3_test.go` | Assembler tests for wasm3 opcodes. |
| A | `src/cmd/internal/obj/wasm/wasm3check.go` | Optional check that local.set and local.tee types match. |
| A | `src/cmd/internal/obj/wasm/wasm3obj.go` | Encodes wasm3 instructions, locals, calls, and GC ops. |
| M | `src/cmd/internal/obj/wasm/wasmobj.go` | Wasm assembler: new opcodes, relocations, or a wasm3 path through the object writer. |
| M | `src/cmd/internal/objabi/reloctype.go` | Shared internal: arch record, object file, or relocation used by wasm3. |
| M | `src/cmd/internal/objabi/reloctype_string.go` | Shared internal: arch record, object file, or relocation used by wasm3. |
| M | `src/cmd/internal/sys/arch.go` | Shared internal: arch record, object file, or relocation used by wasm3. |
| A | `src/cmd/internal/wasmgc/serial.go` | Serialize the type table from the compiler to the linker. |
| A | `src/cmd/internal/wasmgc/serial_test.go` | Type-table serialization tests. |
| A | `src/cmd/internal/wasmgc/type.go` | Shared GC type model and the prelude ($go.string, $go.chan, $go.ptr.poly, …). |
| A | `src/cmd/internal/wasmgc/type_test.go` | Tests for the GC type model. |
| A | `src/cmd/internal/wasmgc/typesec.go` | Binary encoding of the wasm type section. |
| A | `src/cmd/internal/wasmgc/typesec_test.go` | Type-section encoding tests. |
| M | `src/cmd/link/internal/ld/config.go` | Linker support for selecting the wasm3 module writer. |
| M | `src/cmd/link/internal/ld/lib.go` | Linker support for selecting the wasm3 module writer. |
| M | `src/cmd/link/internal/ld/pcln.go` | Linker support for selecting the wasm3 module writer. |
| M | `src/cmd/link/internal/loader/loader.go` | Linker support for selecting the wasm3 module writer. |
| M | `src/cmd/link/internal/wasm/asm.go` | Linker support for selecting the wasm3 module writer. |
| A | `src/cmd/link/internal/wasm/asm3.go` | Writes the wasm3 module. The file header still describes the M2 status. |
| M | `src/cmd/link/internal/wasm/obj.go` | Linker support for selecting the wasm3 module writer. |
| M | `src/cmd/link/main.go` | Linker support for selecting the wasm3 module writer. |

## runtime

| | Path | What it is |
|---|---|---|
| M | `src/runtime/alg.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/alg_hash_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| A | `src/runtime/alg_iface_default.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/alg_iface_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| A | `src/runtime/alg_string_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| A | `src/runtime/alg_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| M | `src/runtime/arena.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/asm_wasm3.s` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| M | `src/runtime/atomic_pointer.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/atomic_pointer_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| M | `src/runtime/cgo.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/cgo_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| M | `src/runtime/cgocall.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/cgocheck.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/chan.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/chan_jswasm3.go` | Channel state machine. One park id per side. select's yield is SleepMs(1). |
| A | `src/runtime/chan_wasm3.go` | Stub hchan so other files have a type. Not the channel implementation. |
| M | `src/runtime/checkptr.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/checkptr_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| M | `src/runtime/coro.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/covercounter.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/covercounter_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| M | `src/runtime/cpuprof.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/cputicks.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/debug.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/debuglog.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/debuglog_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| A | `src/runtime/defer_wasm3.go` | Defer list in one package global. recover calls exit(0); unrecovered panic overflows the stack. |
| M | `src/runtime/env_posix.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/env_posix_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| M | `src/runtime/error.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/error_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| M | `src/runtime/float.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/float_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| A | `src/runtime/gc_stubs_wasm3.go` | No-op stand-ins for the deleted GC, finalizers, and cleanup. |
| A | `src/runtime/gwrite.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/gwrite_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| M | `src/runtime/heapdump.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/hexdump.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/hexdump_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| M | `src/runtime/histogram.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/iface.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/iface_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| A | `src/runtime/interiorptr_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| M | `src/runtime/lfstack.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/lfstack_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| M | `src/runtime/list_manual.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/list_manual_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| M | `src/runtime/lock_js.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/lock_jswasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| M | `src/runtime/lock_spinbit.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/makeslice_default.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/makeslice_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| M | `src/runtime/malloc.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/malloc_generated.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/malloc_stubs.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/malloc_tables_generated.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/mallocgc_default.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/mallocgc_wasm3.go` | mallocgc throws. A call means an allocation was not intrinsified. |
| M | `src/runtime/map.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/mbarrier.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/mbarrier_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| M | `src/runtime/mbitmap.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/mcache.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/mcentral.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/mcheckmark.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/mcleanup.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/mem.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/mem_js.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/mem_jswasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| M | `src/runtime/mem_nonsbrk.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/mem_sbrk.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/mem_sbrk_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| A | `src/runtime/mem_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| A | `src/runtime/memequal_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| A | `src/runtime/memmove_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| M | `src/runtime/metrics.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/mfinal.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/mfixalloc.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/mgc.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/mgclimit.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/mgcmark.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/mgcmark_greenteagc.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/mgcmark_nogreenteagc.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/mgcpacer.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/mgcscavenge.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/mgcstack.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/mgcsweep.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/mgcwork.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/mheap.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/minmax.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/minmax_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| M | `src/runtime/mpagealloc.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/mpagealloc_32bit.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/mpagealloc_64bit.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/mpagecache.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/mpallocbits.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/mprof.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/mranges.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/msize.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/mspanset.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/mstats.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/mstats_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| M | `src/runtime/mwbbuf.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/netpoll.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/netpoll_fake.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/netpoll_jswasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| A | `src/runtime/netpoll_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| A | `src/runtime/newobject.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/newobject_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| M | `src/runtime/os_js.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/os_jswasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| M | `src/runtime/os_wasip1.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/os_wasip1_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| A | `src/runtime/os_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| M | `src/runtime/panic.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/panic_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| M | `src/runtime/pinner.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/plugin.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/preempt.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/preempt_wasm3.s` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| M | `src/runtime/print.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/print_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| A | `src/runtime/printfloat.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/printfloat_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| A | `src/runtime/printlock.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/printlock_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| A | `src/runtime/printnum.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/printnum_wasm3.go` | Prints an integer from a wasm byte array. The slice includes one trailing zero byte. |
| A | `src/runtime/printstring.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/printstring_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| M | `src/runtime/proc.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/proc_sched_wasm3.go` | Wasmfx shims. gogo_wasm3 throws because resume is not implemented. |
| A | `src/runtime/proc_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| A | `src/runtime/prof_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| M | `src/runtime/profbuf.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/rand.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/rand_default.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/rand_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| A | `src/runtime/rt0_jswasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| A | `src/runtime/rt0_wasip1_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| M | `src/runtime/runtime.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/runtime1.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/runtime1_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| M | `src/runtime/runtime2.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/runtime2_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| A | `src/runtime/runtime_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| A | `src/runtime/sched_jswasm3.go` | gopark and goready on js/wasm3 call WasmPark and WasmReady. |
| A | `src/runtime/sched_notjswasm3.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/scheduler_intptr.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/scheduler_intptr_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| M | `src/runtime/select.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/sema.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/sema_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| M | `src/runtime/slice.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/slice_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| A | `src/runtime/slicecopy_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| A | `src/runtime/spawn_jswasm3.go` | newprocJSWasm3, the runtime entry the compiler emits for the go keyword. |
| A | `src/runtime/spawn_notjswasm3.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/stack.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/stack_wasm3.go` | stackalloc throws. There is no linear Go stack. |
| M | `src/runtime/stkframe.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/string.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/string_bytes_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| A | `src/runtime/string_concat_default.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/string_concat_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| A | `src/runtime/string_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| M | `src/runtime/stubs.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/stubs_memequal_default.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/stubs_memmove_default.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/stubs_nonwasm.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/stubs_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| M | `src/runtime/symtab.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/symtab_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| M | `src/runtime/synctest.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/sys_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| A | `src/runtime/sys_wasm3.s` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| M | `src/runtime/tagptr_64bit.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/tagptr_64bit_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| A | `src/runtime/throw_default.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/throw_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| M | `src/runtime/trace.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/trace_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| M | `src/runtime/traceallocfree.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/traceback.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/traceback_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| M | `src/runtime/tracebuf.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/tracecpu.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/traceevent.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/tracemap.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/traceregion.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/traceruntime.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/tracestack.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/tracestatus.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/tracestring.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/tracetime.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/tracetype.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| M | `src/runtime/type.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/type_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| M | `src/runtime/unsafe.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/unsafe_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| A | `src/runtime/wasm/io_jswasm3.go` | runtime/wasm: JSPI imports, the linear-memory bridge, or wasmfx intrinsics. |
| A | `src/runtime/wasm/jspi_jswasm3.go` | wasmimport declarations for SleepMs, WasmPark, and WasmReady. |
| A | `src/runtime/wasm/linearmem_stub.go` | runtime/wasm: JSPI imports, the linear-memory bridge, or wasmfx intrinsics. |
| A | `src/runtime/wasm/linearmem_wasm3.go` | runtime/wasm: JSPI imports, the linear-memory bridge, or wasmfx intrinsics. |
| A | `src/runtime/wasm/sched_wasm3.go` | runtime/wasm: JSPI imports, the linear-memory bridge, or wasmfx intrinsics. |
| A | `src/runtime/wasm/spawn_jswasm3.go` | FIFO of goroutines and the _run_wasm3_js export. |
| A | `src/runtime/wasm3map.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/wrapclosure_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| A | `src/runtime/write1_jswasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |
| A | `src/runtime/write1_wasip1.go` | Runtime file whose build tag now excludes wasm3, or a small wasm3 branch inside it. |
| A | `src/runtime/write1_wasip1_wasm3.go` | wasm3 runtime mirror or stub. The upstream file is tagged !wasm3. |

## src/internal

| | Path | What it is |
|---|---|---|
| M | `src/internal/abi/abi.go` | Internal package: build tag or mirror so wasm3 does not compile pointer arithmetic. |
| A | `src/internal/abi/abi_wasm3.go` | No native register-argument area. IntRegArgAddr does no pointer arithmetic. |
| M | `src/internal/abi/escape.go` | Internal package: build tag or mirror so wasm3 does not compile pointer arithmetic. |
| A | `src/internal/abi/escape_wasm3.go` | NoEscape is an identity. The uintptr xor launder does not compile. |
| M | `src/internal/abi/type.go` | Internal package: build tag or mirror so wasm3 does not compile pointer arithmetic. |
| A | `src/internal/abi/type_wasm3.go` | abi.Type mirror. Methods and name accessors panic until M6. |
| M | `src/internal/buildcfg/cfg.go` | Internal package: build tag or mirror so wasm3 does not compile pointer arithmetic. |
| M | `src/internal/buildcfg/exp.go` | Internal package: build tag or mirror so wasm3 does not compile pointer arithmetic. |
| M | `src/internal/bytealg/compare_generic.go` | Internal package: build tag or mirror so wasm3 does not compile pointer arithmetic. |
| A | `src/internal/bytealg/equal_wasm3.s` | wasm3-only file so this package links without uintptr(unsafe.Pointer) or a linear stack. |
| A | `src/internal/chacha8rand/block_asm.go` | Internal package: build tag or mirror so wasm3 does not compile pointer arithmetic. |
| A | `src/internal/chacha8rand/block_wasm3.go` | wasm3-only file so this package links without uintptr(unsafe.Pointer) or a linear stack. |
| M | `src/internal/chacha8rand/chacha8.go` | Internal package: build tag or mirror so wasm3 does not compile pointer arithmetic. |
| M | `src/internal/chacha8rand/chacha8_generic.go` | Internal package: build tag or mirror so wasm3 does not compile pointer arithmetic. |
| A | `src/internal/chacha8rand/chacha8_generic_wasm3.go` | wasm3-only file so this package links without uintptr(unsafe.Pointer) or a linear stack. |
| M | `src/internal/chacha8rand/chacha8_stub.s` | Internal package: build tag or mirror so wasm3 does not compile pointer arithmetic. |
| M | `src/internal/coverage/rtcov/rtcov.go` | Internal package: build tag or mirror so wasm3 does not compile pointer arithmetic. |
| A | `src/internal/coverage/rtcov/rtcov_wasm3.go` | wasm3-only file so this package links without uintptr(unsafe.Pointer) or a linear stack. |
| A | `src/internal/cpu/cpu_wasm3.go` | wasm3-only file so this package links without uintptr(unsafe.Pointer) or a linear stack. |
| M | `src/internal/filepathlite/path_unix.go` | Internal package: build tag or mirror so wasm3 does not compile pointer arithmetic. |
| M | `src/internal/goarch/goarch.go` | GOARCH identity for wasm3, or the generated file that records it. |
| A | `src/internal/goarch/goarch_wasm3.go` | wasm3-only file so this package links without uintptr(unsafe.Pointer) or a linear stack. |
| A | `src/internal/goarch/iswasm.go` | GOARCH identity for wasm3, or the generated file that records it. |
| M | `src/internal/goarch/zgoarch_386.go` | Generated GOARCH constants. wasm3 was added and the other arch files were regenerated. |
| M | `src/internal/goarch/zgoarch_amd64.go` | Generated GOARCH constants. wasm3 was added and the other arch files were regenerated. |
| M | `src/internal/goarch/zgoarch_arm.go` | Generated GOARCH constants. wasm3 was added and the other arch files were regenerated. |
| M | `src/internal/goarch/zgoarch_arm64.go` | Generated GOARCH constants. wasm3 was added and the other arch files were regenerated. |
| M | `src/internal/goarch/zgoarch_arm64be.go` | Generated GOARCH constants. wasm3 was added and the other arch files were regenerated. |
| M | `src/internal/goarch/zgoarch_armbe.go` | Generated GOARCH constants. wasm3 was added and the other arch files were regenerated. |
| M | `src/internal/goarch/zgoarch_loong64.go` | Generated GOARCH constants. wasm3 was added and the other arch files were regenerated. |
| M | `src/internal/goarch/zgoarch_mips.go` | Generated GOARCH constants. wasm3 was added and the other arch files were regenerated. |
| M | `src/internal/goarch/zgoarch_mips64.go` | Generated GOARCH constants. wasm3 was added and the other arch files were regenerated. |
| M | `src/internal/goarch/zgoarch_mips64le.go` | Generated GOARCH constants. wasm3 was added and the other arch files were regenerated. |
| M | `src/internal/goarch/zgoarch_mips64p32.go` | Generated GOARCH constants. wasm3 was added and the other arch files were regenerated. |
| M | `src/internal/goarch/zgoarch_mips64p32le.go` | Generated GOARCH constants. wasm3 was added and the other arch files were regenerated. |
| M | `src/internal/goarch/zgoarch_mipsle.go` | Generated GOARCH constants. wasm3 was added and the other arch files were regenerated. |
| M | `src/internal/goarch/zgoarch_ppc.go` | Generated GOARCH constants. wasm3 was added and the other arch files were regenerated. |
| M | `src/internal/goarch/zgoarch_ppc64.go` | Generated GOARCH constants. wasm3 was added and the other arch files were regenerated. |
| M | `src/internal/goarch/zgoarch_ppc64le.go` | Generated GOARCH constants. wasm3 was added and the other arch files were regenerated. |
| M | `src/internal/goarch/zgoarch_riscv.go` | Generated GOARCH constants. wasm3 was added and the other arch files were regenerated. |
| M | `src/internal/goarch/zgoarch_riscv64.go` | Generated GOARCH constants. wasm3 was added and the other arch files were regenerated. |
| M | `src/internal/goarch/zgoarch_s390.go` | Generated GOARCH constants. wasm3 was added and the other arch files were regenerated. |
| M | `src/internal/goarch/zgoarch_s390x.go` | Generated GOARCH constants. wasm3 was added and the other arch files were regenerated. |
| M | `src/internal/goarch/zgoarch_sparc.go` | Generated GOARCH constants. wasm3 was added and the other arch files were regenerated. |
| M | `src/internal/goarch/zgoarch_sparc64.go` | Generated GOARCH constants. wasm3 was added and the other arch files were regenerated. |
| M | `src/internal/goarch/zgoarch_wasm.go` | Generated GOARCH constants. wasm3 was added and the other arch files were regenerated. |
| A | `src/internal/goarch/zgoarch_wasm3.go` | Generated GOARCH constants. wasm3 was added and the other arch files were regenerated. |
| M | `src/internal/platform/zosarch.go` | Internal package: build tag or mirror so wasm3 does not compile pointer arithmetic. |
| M | `src/internal/poll/fd_fsync_posix.go` | Internal package: build tag or mirror so wasm3 does not compile pointer arithmetic. |
| A | `src/internal/poll/fd_poll_jswasm3.go` | wasm3-only file so this package links without uintptr(unsafe.Pointer) or a linear stack. |
| M | `src/internal/poll/fd_posix.go` | Internal package: build tag or mirror so wasm3 does not compile pointer arithmetic. |
| M | `src/internal/poll/fd_unix.go` | Internal package: build tag or mirror so wasm3 does not compile pointer arithmetic. |
| M | `src/internal/poll/fd_unixjs.go` | Internal package: build tag or mirror so wasm3 does not compile pointer arithmetic. |
| M | `src/internal/poll/hook_unix.go` | Internal package: build tag or mirror so wasm3 does not compile pointer arithmetic. |
| M | `src/internal/poll/sys_cloexec.go` | Internal package: build tag or mirror so wasm3 does not compile pointer arithmetic. |
| A | `src/internal/reflectlite/reflectlite_wasm3.go` | wasm3-only file so this package links without uintptr(unsafe.Pointer) or a linear stack. |
| M | `src/internal/reflectlite/swapper.go` | Internal package: build tag or mirror so wasm3 does not compile pointer arithmetic. |
| M | `src/internal/reflectlite/type.go` | Internal package: build tag or mirror so wasm3 does not compile pointer arithmetic. |
| M | `src/internal/reflectlite/value.go` | Internal package: build tag or mirror so wasm3 does not compile pointer arithmetic. |
| M | `src/internal/runtime/atomic/atomic_andor_generic.go` | Internal package: build tag or mirror so wasm3 does not compile pointer arithmetic. |
| A | `src/internal/runtime/atomic/atomic_wasm3.go` | Atomics are plain loads and stores. |
| A | `src/internal/runtime/atomic/atomic_wasm3.s` | wasm3-only file so this package links without uintptr(unsafe.Pointer) or a linear stack. |
| M | `src/internal/runtime/atomic/stubs.go` | Internal package: build tag or mirror so wasm3 does not compile pointer arithmetic. |
| M | `src/internal/runtime/atomic/types.go` | Internal package: build tag or mirror so wasm3 does not compile pointer arithmetic. |
| M | `src/internal/runtime/atomic/types_64bit.go` | Internal package: build tag or mirror so wasm3 does not compile pointer arithmetic. |
| A | `src/internal/runtime/atomic/types_wasm3.go` | wasm3-only file so this package links without uintptr(unsafe.Pointer) or a linear stack. |
| M | `src/internal/runtime/atomic/xchg8.go` | Internal package: build tag or mirror so wasm3 does not compile pointer arithmetic. |
| A | `src/internal/runtime/atomic/xchg8_wasm3.go` | wasm3-only file so this package links without uintptr(unsafe.Pointer) or a linear stack. |
| M | `src/internal/runtime/maps/group.go` | Internal package: build tag or mirror so wasm3 does not compile pointer arithmetic. |
| M | `src/internal/runtime/maps/map.go` | Internal package: build tag or mirror so wasm3 does not compile pointer arithmetic. |
| A | `src/internal/runtime/maps/maps_wasm3.go` | Swiss table excluded. MemHash and StrHash return the seed. |
| M | `src/internal/runtime/maps/memhash_aes.go` | Internal package: build tag or mirror so wasm3 does not compile pointer arithmetic. |
| M | `src/internal/runtime/maps/memhash_noaes.go` | Internal package: build tag or mirror so wasm3 does not compile pointer arithmetic. |
| M | `src/internal/runtime/maps/runtime.go` | Internal package: build tag or mirror so wasm3 does not compile pointer arithmetic. |
| M | `src/internal/runtime/maps/runtime_alg.go` | Internal package: build tag or mirror so wasm3 does not compile pointer arithmetic. |
| M | `src/internal/runtime/maps/runtime_fast32.go` | Internal package: build tag or mirror so wasm3 does not compile pointer arithmetic. |
| M | `src/internal/runtime/maps/runtime_fast64.go` | Internal package: build tag or mirror so wasm3 does not compile pointer arithmetic. |
| M | `src/internal/runtime/maps/runtime_faststr.go` | Internal package: build tag or mirror so wasm3 does not compile pointer arithmetic. |
| A | `src/internal/runtime/maps/runtime_faststr_wasm3.go` | Linear map[string]V. Values are still described as a linear-memory blob. |
| M | `src/internal/runtime/maps/table.go` | Internal package: build tag or mirror so wasm3 does not compile pointer arithmetic. |
| M | `src/internal/runtime/maps/table_debug.go` | Internal package: build tag or mirror so wasm3 does not compile pointer arithmetic. |
| M | `src/internal/strconv/deps.go` | Internal package: build tag or mirror so wasm3 does not compile pointer arithmetic. |
| A | `src/internal/strconv/deps_wasm3.go` | wasm3-only file so this package links without uintptr(unsafe.Pointer) or a linear stack. |
| A | `src/internal/syscall/unix/fcntl_jswasm3.go` | wasm3-only file so this package links without uintptr(unsafe.Pointer) or a linear stack. |
| A | `src/internal/syscall/unix/nonblocking_jswasm3.go` | wasm3-only file so this package links without uintptr(unsafe.Pointer) or a linear stack. |
| M | `src/internal/syslist/syslist.go` | Internal package: build tag or mirror so wasm3 does not compile pointer arithmetic. |
| M | `src/internal/testenv/testenv_notunix.go` | Internal package: build tag or mirror so wasm3 does not compile pointer arithmetic. |

## Other standard library

| | Path | What it is |
|---|---|---|
| M | `src/crypto/internal/fips140/alias/alias.go` | Stdlib file: build tag extended to wasm3, or a one-line wasm3 special case. |
| A | `src/crypto/internal/fips140/alias/alias_wasm3.go` | wasm3-only file so this package links without uintptr(unsafe.Pointer) or a linear stack. |
| M | `src/crypto/internal/fips140/bigmod/nat_noasm.go` | Stdlib file: build tag extended to wasm3, or a one-line wasm3 special case. |
| A | `src/crypto/internal/fips140/bigmod/nat_wasm3.go` | wasm3-only file so this package links without uintptr(unsafe.Pointer) or a linear stack. |
| M | `src/crypto/internal/fips140/check/checktest/asm.s` | Stdlib file: build tag extended to wasm3, or a one-line wasm3 special case. |
| M | `src/crypto/internal/fips140/drbg/entropy_fips140.go` | Stdlib file: build tag extended to wasm3, or a one-line wasm3 special case. |
| A | `src/crypto/internal/fips140/drbg/entropy_wasm3.go` | wasm3-only file so this package links without uintptr(unsafe.Pointer) or a linear stack. |
| M | `src/crypto/internal/sysrand/rand_js.go` | Stdlib file: build tag extended to wasm3, or a one-line wasm3 special case. |
| A | `src/crypto/internal/sysrand/rand_jswasm3.go` | wasm3-only file so this package links without uintptr(unsafe.Pointer) or a linear stack. |
| M | `src/crypto/x509/root_unix.go` | Stdlib file: build tag extended to wasm3, or a one-line wasm3 special case. |
| A | `src/crypto/x509/root_wasm3.go` | wasm3-only file so this package links without uintptr(unsafe.Pointer) or a linear stack. |
| M | `src/encoding/binary/native_endian_little.go` | Stdlib file: build tag extended to wasm3, or a one-line wasm3 special case. |
| M | `src/go/types/gccgosizes.go` | Stdlib file: build tag extended to wasm3, or a one-line wasm3 special case. |
| M | `src/go/types/sizes.go` | Stdlib file: build tag extended to wasm3, or a one-line wasm3 special case. |
| M | `src/math/big/arith_decl.go` | Stdlib file: build tag extended to wasm3, or a one-line wasm3 special case. |
| M | `src/math/big/arith_decl_pure.go` | Stdlib file: build tag extended to wasm3, or a one-line wasm3 special case. |
| M | `src/math/bits/bits.go` | Stdlib file: build tag extended to wasm3, or a one-line wasm3 special case. |
| A | `src/math/bits/bits_wasm3.go` | wasm3-only file so this package links without uintptr(unsafe.Pointer) or a linear stack. |
| M | `src/math/floor_noasm.go` | Stdlib file: build tag extended to wasm3, or a one-line wasm3 special case. |
| A | `src/math/floor_wasm3.go` | wasm3-only file so this package links without uintptr(unsafe.Pointer) or a linear stack. |
| M | `src/mime/type_unix.go` | Stdlib file: build tag extended to wasm3, or a one-line wasm3 special case. |
| M | `src/net/http/transport_default_other.go` | Stdlib file: build tag extended to wasm3, or a one-line wasm3 special case. |
| A | `src/net/http/transport_default_wasm3.go` | wasm3-only file so this package links without uintptr(unsafe.Pointer) or a linear stack. |
| M | `src/net/internal/socktest/switch_unix.go` | Stdlib file: build tag extended to wasm3, or a one-line wasm3 special case. |
| M | `src/net/internal/socktest/sys_unix.go` | Stdlib file: build tag extended to wasm3, or a one-line wasm3 special case. |
| M | `src/os/dir_unix.go` | Stdlib file: build tag extended to wasm3, or a one-line wasm3 special case. |
| M | `src/os/exec/internal/fdtest/exists_unix.go` | Stdlib file: build tag extended to wasm3, or a one-line wasm3 special case. |
| A | `src/os/exec/lp_wasm3.go` | wasm3-only file so this package links without uintptr(unsafe.Pointer) or a linear stack. |
| M | `src/os/exec_posix.go` | Stdlib file: build tag extended to wasm3, or a one-line wasm3 special case. |
| M | `src/os/exec_unix.go` | Stdlib file: build tag extended to wasm3, or a one-line wasm3 special case. |
| A | `src/os/executable_wasm3.go` | wasm3-only file so this package links without uintptr(unsafe.Pointer) or a linear stack. |
| M | `src/os/file_open_unix.go` | Stdlib file: build tag extended to wasm3, or a one-line wasm3 special case. |
| M | `src/os/file_posix.go` | Stdlib file: build tag extended to wasm3, or a one-line wasm3 special case. |
| M | `src/os/file_unix.go` | Stdlib file: build tag extended to wasm3, or a one-line wasm3 special case. |
| M | `src/os/path_unix.go` | Stdlib file: build tag extended to wasm3, or a one-line wasm3 special case. |
| M | `src/os/pidfd_other.go` | Stdlib file: build tag extended to wasm3, or a one-line wasm3 special case. |
| A | `src/os/pipe_wasm3.go` | wasm3-only file so this package links without uintptr(unsafe.Pointer) or a linear stack. |
| M | `src/os/removeall_noat.go` | Stdlib file: build tag extended to wasm3, or a one-line wasm3 special case. |
| A | `src/os/root_jswasm3.go` | wasm3-only file so this package links without uintptr(unsafe.Pointer) or a linear stack. |
| M | `src/os/root_noopenat.go` | Stdlib file: build tag extended to wasm3, or a one-line wasm3 special case. |
| M | `src/os/signal/signal_unix.go` | Stdlib file: build tag extended to wasm3, or a one-line wasm3 special case. |
| A | `src/os/stat_jswasm3.go` | wasm3-only file so this package links without uintptr(unsafe.Pointer) or a linear stack. |
| M | `src/os/stat_unix.go` | Stdlib file: build tag extended to wasm3, or a one-line wasm3 special case. |
| M | `src/os/statat_other.go` | Stdlib file: build tag extended to wasm3, or a one-line wasm3 special case. |
| M | `src/os/sticky_bsd.go` | Stdlib file: build tag extended to wasm3, or a one-line wasm3 special case. |
| M | `src/os/sys_bsd.go` | Stdlib file: build tag extended to wasm3, or a one-line wasm3 special case. |
| A | `src/os/sys_jswasm3.go` | wasm3-only file so this package links without uintptr(unsafe.Pointer) or a linear stack. |
| M | `src/os/user/listgroups_unix.go` | Stdlib file: build tag extended to wasm3, or a one-line wasm3 special case. |
| M | `src/os/user/lookup_unix.go` | Stdlib file: build tag extended to wasm3, or a one-line wasm3 special case. |
| M | `src/os/wait_unimp.go` | Stdlib file: build tag extended to wasm3, or a one-line wasm3 special case. |
| M | `src/path/filepath/path_unix.go` | Stdlib file: build tag extended to wasm3, or a one-line wasm3 special case. |
| A | `src/reflect/asm_wasm3.s` | wasm3-only file so this package links without uintptr(unsafe.Pointer) or a linear stack. |
| A | `src/slices/overlaps.go` | Stdlib file: build tag extended to wasm3, or a one-line wasm3 special case. |
| A | `src/slices/overlaps_wasm3.go` | wasm3-only file so this package links without uintptr(unsafe.Pointer) or a linear stack. |
| M | `src/slices/slices.go` | Stdlib file: build tag extended to wasm3, or a one-line wasm3 special case. |
| M | `src/sync/atomic/value.go` | Stdlib file: build tag extended to wasm3, or a one-line wasm3 special case. |
| A | `src/sync/atomic/value_wasm3.go` | atomic.Value is a single any, not two uintptr words. |
| M | `src/sync/cond.go` | Stdlib file: build tag extended to wasm3, or a one-line wasm3 special case. |
| A | `src/sync/cond_check.go` | Stdlib file: build tag extended to wasm3, or a one-line wasm3 special case. |
| A | `src/sync/cond_check_wasm3.go` | wasm3-only file so this package links without uintptr(unsafe.Pointer) or a linear stack. |
| M | `src/sync/pool.go` | Stdlib file: build tag extended to wasm3, or a one-line wasm3 special case. |
| A | `src/sync/pool_race.go` | Stdlib file: build tag extended to wasm3, or a one-line wasm3 special case. |
| A | `src/sync/pool_race_wasm3.go` | wasm3-only file so this package links without uintptr(unsafe.Pointer) or a linear stack. |
| M | `src/syscall/dirent.go` | Stdlib file: build tag extended to wasm3, or a one-line wasm3 special case. |
| M | `src/syscall/env_unix.go` | Stdlib file: build tag extended to wasm3, or a one-line wasm3 special case. |
| A | `src/syscall/fs_jswasm3.go` | wasm3-only file so this package links without uintptr(unsafe.Pointer) or a linear stack. |
| A | `src/syscall/net_jswasm3.go` | wasm3-only file so this package links without uintptr(unsafe.Pointer) or a linear stack. |
| A | `src/syscall/syscall_jswasm3.go` | wasm3-only file so this package links without uintptr(unsafe.Pointer) or a linear stack. |
| A | `src/syscall/tables_jswasm3.go` | wasm3-only file so this package links without uintptr(unsafe.Pointer) or a linear stack. |
| M | `src/syscall/timestruct.go` | Stdlib file: build tag extended to wasm3, or a one-line wasm3 special case. |
| A | `src/testing/run_example_wasm3.go` | wasm3-only file so this package links without uintptr(unsafe.Pointer) or a linear stack. |
| M | `src/time/sys_unix.go` | Stdlib file: build tag extended to wasm3, or a one-line wasm3 special case. |
| A | `src/time/zoneinfo_jswasm3.go` | wasm3-only file so this package links without uintptr(unsafe.Pointer) or a linear stack. |
| A | `src/vendor/golang.org/x/sys/cpu/cpu_wasm3.go` | wasm3-only file so this package links without uintptr(unsafe.Pointer) or a linear stack. |
| M | `src/vendor/golang.org/x/sys/cpu/endian_little.go` | Stdlib file: build tag extended to wasm3, or a one-line wasm3 special case. |


Listed 519 of 519 files.
