#!/usr/bin/env node
// Harness for m5_defer_panic.wat — stream F M5 exit checks (docs/design/09).
import { readFileSync, existsSync } from "node:fs";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const dir = dirname(fileURLToPath(import.meta.url));
const wat = join(dir, "m5_defer_panic.wat");
const wasmPath = join(dir, "m5_defer_panic.wasm");

function parseWat() {
  const r = spawnSync("wasm-tools", ["parse", wat, "-o", wasmPath], {
    encoding: "utf8",
  });
  if (r.status !== 0) {
    console.error(r.stderr || r.stdout);
    process.exit(1);
  }
}

parseWat();
const bytes = readFileSync(wasmPath);
const { instance } = await WebAssembly.instantiate(bytes, {});
const { defer_lifo, unrecovered, recover_ok } = instance.exports;

const lifo = defer_lifo();
if (lifo !== 11) {
  console.error(`defer_lifo: got ${lifo}, want 11 (A=1 then B=10)`);
  process.exit(1);
}

const code = unrecovered();
if (code !== 2) {
  console.error(`unrecovered: exit code ${code}, want 2`);
  process.exit(1);
}

const ok = recover_ok();
if (ok !== 1) {
  console.error(`recover_ok: got ${ok}, want 1 (recoverOk set by deferRun)`);
  process.exit(1);
}

console.log("m5_defer_panic: ok");
