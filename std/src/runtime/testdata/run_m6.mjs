#!/usr/bin/env node
import { readFileSync } from "node:fs";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const dir = dirname(fileURLToPath(import.meta.url));
const wat = join(dir, "m6_map.wat");
const wasmPath = join(dir, "m6_map.wasm");

const r = spawnSync("wasm-tools", ["parse", wat, "-o", wasmPath], { encoding: "utf8" });
if (r.status !== 0) {
  console.error(r.stderr || r.stdout);
  process.exit(1);
}
const { instance } = await WebAssembly.instantiate(readFileSync(wasmPath), {});
const v = instance.exports.i32_roundtrip();
if (v !== 42) {
  console.error(`i32_roundtrip: got ${v}, want 42`);
  process.exit(1);
}
const eq = instance.exports.string_eq_smoke();
if (eq !== 1) {
  console.error(`string_eq_smoke: got ${eq}, want 1`);
  process.exit(1);
}
console.log("m6_map: ok");
