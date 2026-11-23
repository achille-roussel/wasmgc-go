// Run prototypes/eh-panic/panic.wat. Requires `wasm-tools` on PATH.
import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const dir = dirname(fileURLToPath(import.meta.url));
const out = join(tmpdir(), "wasmgc-eh-panic.wasm");
execFileSync("wasm-tools", ["parse", join(dir, "panic.wat"), "-o", out], { stdio: "inherit" });
const wasm = readFileSync(out);
const { instance } = await WebAssembly.instantiate(wasm);
const e = instance.exports;

function check(name, got, want) {
  if (got !== want) {
    throw new Error(`${name}: got ${got}, want ${want}`);
  }
  console.log(`ok  ${name} = ${got}`);
}

e.reset();
check("recover7", e.recover7(), 8);
check("defer ran on recover", e.deferRuns(), 1);

e.reset();
check("propagate", e.propagate(), 8);
check("defer ran in both frames", e.deferRuns(), 2);

e.reset();
check("normalDefer", e.normalDefer(), 1);
check("defer ran on normal return", e.deferRuns(), 1);

console.log(`node ${process.version}: exception-handling prototype passed`);
