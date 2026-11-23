#!/usr/bin/env node
// M7 channel/select + JSPI harness (docs/design/09). Skips when Suspending is
// missing so Node 22 CI stays green for non-parking modules.
import { readFileSync } from "node:fs";
import { spawnSync } from "node:child_process";
import { fileURLToPath, pathToFileURL } from "node:url";
import { dirname, join, resolve } from "node:path";

if (typeof WebAssembly.Suspending !== "function" || typeof WebAssembly.promising !== "function") {
  console.log(`m7: skip (node ${process.version} has no JSPI Suspending)`);
  process.exit(0);
}

const dir = dirname(fileURLToPath(import.meta.url));
const repo = resolve(dir, "..", "..", "..", "..");
const glue = await import(pathToFileURL(join(repo, "lib", "wasmgc.js")).href);

function check(name, got, want) {
  if (got !== want) {
    throw new Error(`${name}: got ${got}, want ${want}`);
  }
  console.log(`ok  ${name} = ${got}`);
}

async function load(watName) {
  const wat = join(dir, watName);
  const wasmPath = join(dir, watName.replace(/\.wat$/, ".wasm"));
  const parsed = spawnSync("wasm-tools", ["parse", wat, "-o", wasmPath], { encoding: "utf8" });
  if (parsed.status !== 0) {
    console.error(parsed.stderr || parsed.stdout);
    process.exit(1);
  }
  const module = await WebAssembly.compile(readFileSync(wasmPath));
  const opts = { programArgs: [] };
  const importObject = glue.buildImports(module, opts);
  const instance = await WebAssembly.instantiate(module, importObject);
  opts.attachExports?.(instance.exports);
  return instance;
}

async function call(instance, name) {
  return await WebAssembly.promising(instance.exports[name])();
}

const chan = await load("m7_chan.wat");
check("buffered_send_recv", await call(chan, "buffered_send_recv"), 7 | (1 << 8));
check("unbuffered_rendezvous", await call(chan, "unbuffered_rendezvous"), 42);
check("two_waiters", await call(chan, "two_waiters"), 30);
check("recv_closed", await call(chan, "recv_closed"), 0);

const sel = await load("m7_select.wat");
check("select_park", await call(sel, "select_park"), (1 << 8) | 99);

// Sleep via scheduleTimer + park (time.Sleep shape).
{
  const sched = glue.createScheduler({});
  const t0 = Date.now();
  const parked = sched.park(9); // Promise (not yet ready)
  sched.scheduleTimer(9, 8_000_000, 0); // 8ms
  await parked;
  const dt = Date.now() - t0;
  if (dt < 4) throw new Error(`sleep too fast: ${dt}ms`);
  console.log(`ok  scheduleTimer sleep ~${dt}ms`);
}

console.log("m7: ok");
