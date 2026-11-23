// Exercise lib/wasmgc.js park/ready/spawn against glue_sched.wat.
// Node 25+ (JSPI). Extends prototypes/jspi-sched for M7 stream B+F (09).
import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

if (typeof WebAssembly.Suspending !== "function" || typeof WebAssembly.promising !== "function") {
  console.error(`node ${process.version}: no WebAssembly.Suspending`);
  process.exit(1);
}

const dir = dirname(fileURLToPath(import.meta.url));
const repo = resolve(dir, "..", "..");
const out = join(tmpdir(), "wasmgc-glue-sched.wasm");
execFileSync("wasm-tools", ["parse", join(dir, "glue_sched.wat"), "-o", out], {
  stdio: "inherit",
});

const gluePath = join(repo, "lib", "wasmgc.js");
const glue = await import(pathToFileURL(gluePath).href);

function check(name, got, want) {
  if (got !== want) {
    throw new Error(`${name}: got ${JSON.stringify(got)}, want ${JSON.stringify(want)}`);
  }
  console.log(`ok  ${name} = ${JSON.stringify(got)}`);
}

// --- Parked main: spawn macrotask runs while main is suspended. ---
{
  const wasm = readFileSync(out);
  const module = await WebAssembly.compile(wasm);
  const opts = { programArgs: [] };
  const importObject = glue.buildImports(module, opts);
  const instance = await WebAssembly.instantiate(module, importObject);
  opts.attachExports(instance.exports);
  instance.exports.set_mode(1);

  const call = WebAssembly.promising(instance.exports.run);
  const p = call();
  // Do not use glue's process.exit path; await park resume ourselves.
  await p;
  check("parked main: child ran", instance.exports.child_ran(), 1);
  check("parked main: g restored", instance.exports.peek_g(), 1);
}

// --- Main returns without parking: spawn is after exit microtask. ---
{
  const wasm = readFileSync(out);
  const module = await WebAssembly.compile(wasm);
  const opts = { programArgs: [] };
  const importObject = glue.buildImports(module, opts);
  const instance = await WebAssembly.instantiate(module, importObject);
  opts.attachExports(instance.exports);
  instance.exports.set_mode(0);

  const log = [];
  const call = WebAssembly.promising(instance.exports.run);
  const p = call();
  p.then(() => {
    log.push("microtask:main-then");
    // Mark exited the way the glue does, so spawn's macrotask no-ops.
    opts.scheduler.markExited();
  });
  await p;
  log.push("microtask:awaited");
  await new Promise((r) => setTimeout(r, 20));
  check("returning main: child did not run", instance.exports.child_ran(), 0);
  check(
    "returning main: microtask before macrotask window",
    log.includes("microtask:main-then"),
    true,
  );
}

// --- ready-before-park via real glue ---
{
  const sched = glue.createScheduler({});
  sched.ready(7);
  const v = sched.park(7);
  check("ready-before-park is bare value", v, 7);
  check("ready-before-park not a Promise", typeof v.then, "undefined");
}

// --- scheduleTimer fires ready ---
{
  const sched = glue.createScheduler({});
  const p = Promise.resolve(sched.park(42)).then((id) => id);
  sched.scheduleTimer(42, 5_000_000, 0); // 5ms in ns, lo/hi split
  const id = await p;
  check("scheduleTimer readied park id", id, 42);
}

console.log(`node ${process.version}: glue JSPI scheduler passed`);
