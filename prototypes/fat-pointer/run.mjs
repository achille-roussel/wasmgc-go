// Run prototypes/fat-pointer/pointer.wat. Requires `wasm-tools` on PATH.
import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const dir = dirname(fileURLToPath(import.meta.url));
const out = join(tmpdir(), "wasmgc-fat-pointer.wasm");
execFileSync("wasm-tools", ["parse", join(dir, "pointer.wat"), "-o", out], { stdio: "inherit" });
const wasm = readFileSync(out);
const { instance } = await WebAssembly.instantiate(wasm);
const e = instance.exports;

function check(name, got, want) {
  if (got !== want) {
    throw new Error(`${name}: got ${got}, want ${want}`);
  }
  console.log(`ok  ${name} = ${got}`);
}

check("struct field store through call_ref", e.field(), 11);
check("array element load through call_ref", e.elem(), 9);
check("nil pointer is a null ref", e.nil(), 1);
console.log(`node ${process.version}: fat-pointer prototype passed`);
