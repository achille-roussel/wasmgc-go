#!/usr/bin/env node
// Benchmarks GC byte-array kernels: scalar vs lane-build vs bridge vs (array v128).
//
//   export PATH="/path/to/wasm-tools:$PATH"
//   node gen.mjs && wasm-tools parse kernels.wat -o kernels.wasm
//   node run.mjs
//
// Optional: SIZES=16,64,1024,1048576 ITERS=… WARMUP=…

import { readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";

const dir = dirname(fileURLToPath(import.meta.url));
const wasmPath = join(dir, "kernels.wasm");
const watPath = join(dir, "kernels.wat");

function ensureWasm() {
  const parse = spawnSync("wasm-tools", ["parse", watPath, "-o", wasmPath], {
    encoding: "utf8",
  });
  if (parse.status !== 0) {
    console.error(parse.stderr || parse.stdout || "wasm-tools parse failed");
    process.exit(1);
  }
}

ensureWasm();

const bytes = readFileSync(wasmPath);
const { instance } = await WebAssembly.instantiate(bytes);
const ex = instance.exports;

const SIZES = (process.env.SIZES || "16,64,256,1024,4096,65536,1048576")
  .split(",")
  .map((s) => parseInt(s, 10));
const WARMUP = parseInt(process.env.WARMUP || "5", 10);
const TARGET_NS = parseInt(process.env.TARGET_NS || String(200e6), 10); // ~200ms per cell

function now() {
  return process.hrtime.bigint();
}

function bench(fn) {
  for (let i = 0; i < WARMUP; i++) fn();
  let iters = 1;
  let elapsed = 0n;
  // grow iters until we have enough time
  for (;;) {
    const t0 = now();
    for (let i = 0; i < iters; i++) fn();
    elapsed = now() - t0;
    if (elapsed >= BigInt(TARGET_NS) || iters >= 1e7) break;
    iters *= 2;
  }
  const nsPerOp = Number(elapsed) / iters;
  return { nsPerOp, iters, elapsedNs: Number(elapsed) };
}

function assertEq(name, got, want) {
  if (got !== want) {
    throw new Error(`${name}: got ${got}, want ${want}`);
  }
}

// Correctness checks on a small buffer.
{
  const n = 64;
  const needleAt = 37;
  const c = 0x5a;
  const a = ex.make_bytes(n, 0x11, needleAt, c);
  const a2 = ex.make_bytes(n, 0x11, needleAt, c);
  const v = ex.make_v128s(n, 0x11, needleAt, c);
  const v2 = ex.make_v128s(n, 0x11, needleAt, c);

  assertEq("indexbyte_scalar", ex.indexbyte_scalar(a, n, c), needleAt);
  assertEq("indexbyte_lanes", ex.indexbyte_lanes(a, n, c), needleAt);
  assertEq("indexbyte_bridge", ex.indexbyte_bridge(a, n, c), needleAt);
  assertEq("indexbyte_swar", ex.indexbyte_swar(a, n, c), needleAt);
  assertEq("indexbyte_v128arr", ex.indexbyte_v128arr(v, n, c), needleAt);
  ex.bridge_copy(a, n);
  assertEq("indexbyte_mem", ex.indexbyte_mem(n, c), needleAt);

  assertEq("equal_scalar", ex.equal_scalar(a, a2, n), 1);
  assertEq("equal_lanes", ex.equal_lanes(a, a2, n), 1);
  assertEq("equal_bridge", ex.equal_bridge(a, a2, n), 1);
  assertEq("equal_v128arr", ex.equal_v128arr(v, v2, n), 1);

  // count: fill with c at every 4th? make all fill=c for count==n
  const all = ex.make_bytes(n, c, 0, c);
  const allv = ex.make_v128s(n, c, 0, c);
  assertEq("count_scalar", ex.count_scalar(all, n, c), n);
  assertEq("count_lanes", ex.count_lanes(all, n, c), n);
  assertEq("count_bridge", ex.count_bridge(all, n, c), n);
  assertEq("count_v128arr", ex.count_v128arr(allv, n, c), n);
  console.log("ok  correctness checks passed");
}

const results = {
  host: {
    node: process.version,
    platform: process.platform,
    arch: process.arch,
  },
  methodology: {
    timer: "process.hrtime.bigint",
    warmup: WARMUP,
    targetNsPerCell: TARGET_NS,
    note: "ns/op is wall time / iterations. Data is GC (array i8) unless noted. Miss cases search the full buffer (needle past end). Equal compares two identical buffers. Count counts a buffer filled with the needle.",
  },
  sizes: {},
};

for (const n of SIZES) {
  // Miss case for IndexByte: needle not present (fill 0x11, needle 0x5a at n+1 → no poke)
  const a = ex.make_bytes(n, 0x11, n + 1, 0x5a);
  const a2 = ex.make_bytes(n, 0x11, n + 1, 0x5a);
  const v = ex.make_v128s(n, 0x11, n + 1, 0x5a);
  const v2 = ex.make_v128s(n, 0x11, n + 1, 0x5a);
  const all = ex.make_bytes(n, 0x5a, n + 1, 0);
  const allv = ex.make_v128s(n, 0x5a, n + 1, 0);
  const c = 0x5a;

  // Pre-copy for mem-only (amortized / already-bridged) measurements
  ex.bridge_copy(a, n);

  const cell = {};

  cell.indexbyte_scalar = bench(() => ex.indexbyte_scalar(a, n, c));
  cell.indexbyte_lanes = bench(() => ex.indexbyte_lanes(a, n, c));
  cell.indexbyte_bridge = bench(() => ex.indexbyte_bridge(a, n, c));
  cell.indexbyte_mem = bench(() => ex.indexbyte_mem(n, c)); // after one bridge_copy
  cell.indexbyte_swar = bench(() => ex.indexbyte_swar(a, n, c));
  if (n % 16 === 0) {
    cell.indexbyte_v128arr = bench(() => ex.indexbyte_v128arr(v, n, c));
  }

  cell.equal_scalar = bench(() => ex.equal_scalar(a, a2, n));
  cell.equal_lanes = bench(() => ex.equal_lanes(a, a2, n));
  cell.equal_bridge = bench(() => ex.equal_bridge(a, a2, n));
  ex.bridge_copy_pair(a, a2, n);
  cell.equal_mem = bench(() => ex.equal_mem(n));
  if (n % 16 === 0) {
    cell.equal_v128arr = bench(() => ex.equal_v128arr(v, v2, n));
  }

  cell.count_scalar = bench(() => ex.count_scalar(all, n, c));
  cell.count_lanes = bench(() => ex.count_lanes(all, n, c));
  cell.count_bridge = bench(() => ex.count_bridge(all, n, c));
  ex.bridge_copy(all, n);
  cell.count_mem = bench(() => ex.count_mem(n, c));
  if (n % 16 === 0) {
    cell.count_v128arr = bench(() => ex.count_v128arr(allv, n, c));
  }

  // Also measure copy-only cost
  cell.bridge_copy_only = bench(() => ex.bridge_copy(a, n));

  // Host JS kernel on already-bridged memory (engine TypedArray SIMD / builtins).
  ex.bridge_copy(a, n);
  const u8 = new Uint8Array(ex.memory.buffer, 0, n);
  cell.indexbyte_js_u8 = bench(() => u8.indexOf(c));
  ex.bridge_copy(all, n);
  const u8all = new Uint8Array(ex.memory.buffer, 0, n);
  cell.count_js_u8 = bench(() => {
    let cnt = 0;
    for (let i = 0; i < n; i++) if (u8all[i] === c) cnt++;
    return cnt;
  });

  results.sizes[n] = cell;

  const fmt = (r) => (r ? r.nsPerOp.toFixed(1) : "-");
  console.log(
    `size=${n}\n` +
      `  IndexByte  scalar=${fmt(cell.indexbyte_scalar)}  lanes=${fmt(cell.indexbyte_lanes)}  bridge=${fmt(cell.indexbyte_bridge)}  mem=${fmt(cell.indexbyte_mem)}  swar=${fmt(cell.indexbyte_swar)}  v128arr=${fmt(cell.indexbyte_v128arr)}  js=${fmt(cell.indexbyte_js_u8)}\n` +
      `  Equal      scalar=${fmt(cell.equal_scalar)}  lanes=${fmt(cell.equal_lanes)}  bridge=${fmt(cell.equal_bridge)}  mem=${fmt(cell.equal_mem)}  v128arr=${fmt(cell.equal_v128arr)}\n` +
      `  Count      scalar=${fmt(cell.count_scalar)}  lanes=${fmt(cell.count_lanes)}  bridge=${fmt(cell.count_bridge)}  mem=${fmt(cell.count_mem)}  v128arr=${fmt(cell.count_v128arr)}  js=${fmt(cell.count_js_u8)}\n` +
      `  bridge_copy_only=${fmt(cell.bridge_copy_only)}  (ns/op)`
  );
}

const outPath = join(dir, "results.json");
writeFileSync(outPath, JSON.stringify(results, null, 2));
console.log(`wrote ${outPath}`);
