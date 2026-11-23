# Prototypes: SIMD over Wasm GC byte arrays

Hand-written (generated) WAT kernels that measure how expensive it is to run
`IndexByte`, `Equal`, and `Count` when the bytes live in a GC `(array i8)`,
versus strategies that reach Wasm's 128-bit SIMD.

This is the measurement behind [`docs/design/11-simd.md`](../../docs/design/11-simd.md).
It is not the compiler.

## Run

Needs [`wasm-tools`](https://github.com/bytecodealliance/wasm-tools) on `PATH`
(tested with 1.239.0) and Node.js 22+ with Wasm GC + SIMD (both ship in
Node 22.23.4 and 26.13.1).

```sh
export PATH="/path/to/wasm-tools:$PATH"
node prototypes/simd/gen.mjs
node prototypes/simd/run.mjs
```

`run.mjs` re-parses `kernels.wat` on each run. Optional env:

| Env | Default | Meaning |
|---|---|---|
| `SIZES` | `16,64,256,1024,4096,65536,1048576` | buffer sizes in bytes |
| `WARMUP` | `5` | untimed warmup calls |
| `TARGET_NS` | `80000000` | grow iterations until ~this many ns per cell |

## Strategies

| Name | What it does |
|---|---|
| `scalar` | loop of `array.get_u` on `(array i8)` — today's generic `bytealg` shape |
| `lanes` | 16× `array.get_u` + `i8x16.replace_lane`, then SIMD `eq` / `bitmask` |
| `bridge` | copy every byte into linear memory with `i32.store8`, then `v128.load` SIMD |
| `mem` | same SIMD as `bridge`, but the copy is **not** in the timed path (data already bridged) |
| `swar` | compose 8× `array.get_u` into an `i64`, then classic haszero SWAR (no multibyte load exists) |
| `v128arr` | `array.get` of `(array v128)` — oracle if bytes were stored as 16-byte chunks |
| `js` | host `Uint8Array.indexOf` / JS loop over already-bridged memory |

There is **no** instruction today that loads a `v128` from an `(array i8)` at a
byte offset. That is the Multibyte Array Access proposal (phase 2); see the
design doc.

## Results recorded 2026-11-23

Host: Node.js **v26.13.1**, linux/x64. Timer: `process.hrtime.bigint`.
`IndexByte` / `Equal` are full-buffer misses / identical compares; `Count`
fills every byte with the needle. Numbers are **ns/op** (wall / iterations).
Full JSON: [`results-node26.json`](results-node26.json). A subset on
Node **v22.23.4** is in [`results-node22.json`](results-node22.json); ratios
match within ~15%.

### IndexByte (miss, full scan), Node 26

| size | scalar | lanes | bridge | mem | swar | v128arr | js `indexOf` |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 16 | 17.5 | 17.1 | 20.1 | 6.0 | 19.2 | 16.2 | 22.8 |
| 1 KiB | 475 | 317 | 418 | 33 | 380 | 56 | 290 |
| 64 KiB | 28804 | 19625 | 26274 | 1797 | 22260 | 3189 | 16807 |
| 1 MiB | 461989 | 312581 | 418195 | 27784 | 358282 | 53013 | 266134 |

### Equal (identical buffers), Node 26

| size | scalar | lanes | bridge | mem | v128arr |
|---:|---:|---:|---:|---:|---:|
| 1 KiB | 586 | 640 | 588 | 40 | 83 |
| 1 MiB | 566625 | 629506 | 711738 | 47837 | 74882 |

### Count (every byte matches), Node 26

| size | scalar | lanes | bridge | mem | v128arr | js loop |
|---:|---:|---:|---:|---:|---:|---:|
| 1 KiB | 476 | 322 | 424 | 32 | 52 | 730 |
| 1 MiB | 481818 | 313300 | 418974 | 28240 | 47566 | 728358 |

### Copy overhead alone

`bridge_copy_only` for 1 MiB is ~357 µs — the same order as one scalar
`IndexByte` over that buffer. One-shot `bridge` therefore cannot beat scalar
for a single scan; `mem` and `v128arr` show what SIMD is worth once loads are
cheap.

## What this establishes

1. `(array v128)` and `v128` struct fields validate and run on Node 22 and 26.
2. Building a `v128` from sixteen packed `array.get_u`s is only a mild win
   (~1.5×) for IndexByte/Count and a loss for Equal.
3. True SIMD (`v128.load` or `array.get` of `v128`) is ~10–17× faster than
   scalar on large buffers.
4. Paying a byte-by-byte bridge copy to get there does not amortize on a
   single kernel call.
5. Composing `i64` from eight `array.get_u`s for SWAR is not a substitute for
   a multibyte load.
