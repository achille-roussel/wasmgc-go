// Merge hand-written WAT with a compiler-stub module and run in Node.
//
// Requires on PATH:
//   wasm-tools   (tested 1.246.2) — parse + validate
//   wasm-merge   (Binaryen ≥113; tested 133) — GC-aware import/export fusion
//
// Node 22+ is enough (Wasm GC, no JSPI). Node 25+ matches wasmgc-go run.
import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const dir = dirname(fileURLToPath(import.meta.url));
const work = mkdtempSync(join(tmpdir(), "wasmgc-asm-"));

function which(bin) {
  try {
    return execFileSync("bash", ["-lc", `command -v ${bin}`], { encoding: "utf8" }).trim();
  } catch {
    return "";
  }
}

const wasmTools = which("wasm-tools");
const wasmMerge = which("wasm-merge");
if (!wasmTools) {
  console.error("wasm-tools not on PATH (see prototypes/README.md)");
  process.exit(1);
}
if (!wasmMerge) {
  console.error("wasm-merge not on PATH (Binaryen ≥113 with GC support)");
  process.exit(1);
}

/** Rewrite $go:Name → $go.Name (symbolic refs → compiler prelude names). */
function resolveSymbols(wat) {
  return wat.replace(/\$go:([A-Za-z_][\w.]*)/g, "$$go.$1");
}

function parseWat(srcPath, outName) {
  const raw = readFileSync(srcPath, "utf8");
  const resolved = resolveSymbols(raw);
  const watOut = join(work, outName + ".wat");
  const wasmOut = join(work, outName + ".wasm");
  writeFileSync(watOut, resolved);
  execFileSync(wasmTools, ["parse", watOut, "-o", wasmOut], { stdio: "inherit" });
  execFileSync(wasmTools, ["validate", wasmOut], { stdio: "inherit" });
  return wasmOut;
}

function merge(compilerWasm, compilerName, asmWasm, asmName, outName) {
  const out = join(work, outName);
  execFileSync(
    wasmMerge,
    [
      compilerWasm,
      compilerName,
      asmWasm,
      asmName,
      "-o",
      out,
      "--enable-gc",
      "--enable-reference-types",
      "--enable-bulk-memory",
      "--enable-multivalue",
    ],
    { stdio: "inherit" },
  );
  execFileSync(wasmTools, ["validate", out], { stdio: "inherit" });
  return out;
}

function check(name, got, want) {
  if (got !== want) {
    throw new Error(`${name}: got ${got}, want ${want}`);
  }
  console.log(`ok  ${name} = ${got}`);
}

async function runMerged(path, tests) {
  const { instance } = await WebAssembly.instantiate(readFileSync(path));
  for (const [name, fn, want] of tests) {
    check(name, instance.exports[fn](), want);
  }
}

// --- memequal: symbolic types + structural merge ---
const memAsm = parseWat(join(dir, "asm/memequal.wat"), "memequal");
const memComp = parseWat(join(dir, "compiler/stub.wat"), "stub");
const memMerged = merge(memComp, "compiler", memAsm, "goasm", "memequal.merged.wasm");
await runMerged(memMerged, [
  ["memequal equal strings", "run", 1],
  ["memequal unequal strings", "run_ne", 0],
  ["memequal substring headers share bytes", "run_sub", 1],
]);

// --- indexbyte: asm imports a compiler export (callback) ---
const idxAsm = parseWat(join(dir, "asm/indexbyte.wat"), "indexbyte");
const idxComp = parseWat(join(dir, "compiler/indexbyte_driver.wat"), "idxdrv");
const idxMerged = merge(idxComp, "compiler", idxAsm, "goasm", "indexbyte.merged.wasm");
await runMerged(idxMerged, [
  ["indexbyte finds e", "find_e", 1],
  ["indexbyte missing → -1", "find_missing", -1],
]);

// --- negative: divergent string shape must not fuse ---
const badWat = `
(module
  (type $go.string (struct (field $data i32) (field $len i32)))
  (func (export "memequal")
        (param $a (ref $go.string)) (param $b (ref $go.string)) (result i32)
    (i32.const 1))
)
`;
writeFileSync(join(work, "bad.wat"), badWat);
execFileSync(wasmTools, ["parse", join(work, "bad.wat"), "-o", join(work, "bad.wasm")], {
  stdio: "inherit",
});
let rejected = false;
try {
  execFileSync(
    wasmMerge,
    [
      memComp,
      "compiler",
      join(work, "bad.wasm"),
      "goasm",
      "-o",
      join(work, "bad.merged.wasm"),
      "--enable-gc",
      "--enable-reference-types",
      "--enable-multivalue",
    ],
    { stdio: ["ignore", "pipe", "pipe"] },
  );
} catch (e) {
  rejected = true;
  const msg = String(e.stderr || e.message);
  if (!/mismatch|Fatal/i.test(msg)) {
    console.log("(merge stderr)", msg.trim().split("\n").slice(0, 3).join(" | "));
  }
}
if (!rejected) {
  throw new Error("expected type-mismatched asm merge to fail");
}
console.log("ok  divergent $go.string shape rejected at merge");

rmSync(work, { recursive: true, force: true });
console.log(`node ${process.version}: wasm-asm prototype passed`);
