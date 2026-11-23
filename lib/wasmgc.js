#!/usr/bin/env node
// Host glue for wasmgc-go modules. See docs/design/07-host-and-js.md.
//
// Instantiates a module, supplies only the imports the module declares,
// calls the `run` export, and handles exit codes. Does not reference
// WebAssembly.Suspending unless the module imports a suspending function
// (so Node 22 can run non-parking programs).
//
// Park / ready / spawn implement the JSPI scheduler in docs/design/04:
// spawn is a macrotask (setTimeout 0) calling promising(_sched); the main
// export's promise reaction exits as a microtask, so a returning main does
// not run queued goroutines, while a parked main does.

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { writeSync } from "node:fs";

const SUSPENDING_IMPORTS = new Set([
  "runtime.park",
  "runtime.gosched",
  // js.Await (later) is suspending; js.call is not wrapped by default (07).
]);

const __dirname = dirname(fileURLToPath(import.meta.url));

function parseArgs(argv) {
  // node lib/wasmgc.js [wasm-path] [-- args...]
  const args = argv.slice(2);
  let wasmPath = null;
  const programArgs = [];
  let sawDashDash = false;
  for (const a of args) {
    if (sawDashDash) {
      programArgs.push(a);
      continue;
    }
    if (a === "--") {
      sawDashDash = true;
      continue;
    }
    if (wasmPath === null && !a.startsWith("-")) {
      wasmPath = a;
      continue;
    }
    programArgs.push(a);
  }
  if (wasmPath === null) {
    // Default: sibling empty M0 module for smoke tests, else prog.wasm next to this file.
    const m0 = resolve(__dirname, "..", "testdata", "m0", "empty.wasm");
    const sibling = resolve(__dirname, "prog.wasm");
    try {
      readFileSync(m0);
      wasmPath = m0;
    } catch {
      wasmPath = sibling;
    }
  }
  return { wasmPath: resolve(wasmPath), programArgs };
}

function goExit(code) {
  const err = new Error(`GoExit:${code}`);
  err.name = "GoExit";
  err.code = code | 0;
  throw err;
}

/** Host-side park/ready/spawn/timer state for one instantiation. */
function createScheduler(opts) {
  const waiters = new Map(); // park id -> resolve
  const alreadyReady = new Set();
  const timers = new Map(); // park id -> timeout handle
  let exportsRef = null;
  let exited = false;

  function attachExports(exports) {
    exportsRef = exports;
  }

  function park(id) {
    id |= 0;
    if (alreadyReady.delete(id)) {
      return id;
    }
    return new Promise((resolve) => {
      waiters.set(id, resolve);
    });
  }

  function ready(id) {
    id |= 0;
    const resolve = waiters.get(id);
    if (resolve) {
      waiters.delete(id);
      resolve(id);
      return;
    }
    alreadyReady.add(id);
  }

  function spawn() {
    setTimeout(() => {
      if (exited) return;
      const exports = exportsRef;
      if (!exports || typeof exports._sched !== "function") {
        throw new Error("wasmgc.js: runtime.spawn requires export _sched");
      }
      if (typeof WebAssembly.promising !== "function") {
        throw new Error("wasmgc.js: WebAssembly.promising required for spawn");
      }
      const p = WebAssembly.promising(exports._sched)();
      if (p && typeof p.then === "function") {
        p.catch((err) => {
          if (err && err.name === "GoExit") {
            exited = true;
            if (typeof process !== "undefined" && process.exit) {
              process.exit(err.code | 0);
            }
            return;
          }
          console.error(err);
          if (typeof process !== "undefined" && process.exit) {
            process.exit(1);
          }
        });
      }
    }, 0);
  }

  function gosched() {
    return new Promise((resolve) => {
      setTimeout(resolve, 0);
    });
  }

  function scheduleTimer(id, lo, hi) {
    id |= 0;
    // ns is i64 split into two i32 limbs (07).
    const ns = BigInt.asIntN(
      64,
      BigInt(lo >>> 0) | (BigInt(hi | 0) << 32n),
    );
    let ms = Number(ns / 1000000n);
    if (ms < 0) ms = 0;
    if (timers.has(id)) {
      clearTimeout(timers.get(id));
      timers.delete(id);
    }
    const handle = setTimeout(() => {
      timers.delete(id);
      if (!exited) ready(id);
    }, ms);
    timers.set(id, handle);
  }

  function clearTimer(id) {
    id |= 0;
    const handle = timers.get(id);
    if (handle !== undefined) {
      clearTimeout(handle);
      timers.delete(id);
    }
  }

  function markExited() {
    exited = true;
    for (const handle of timers.values()) clearTimeout(handle);
    timers.clear();
  }

  return {
    park,
    ready,
    spawn,
    gosched,
    scheduleTimer,
    clearTimer,
    attachExports,
    markExited,
    // Test/introspection hooks (not imports).
    _waiters: waiters,
    _alreadyReady: alreadyReady,
  };
}

function buildImports(module, opts) {
  const imports = {};
  const declared = WebAssembly.Module.imports(module);
  const needed = declared.filter((i) => i.module === "go");
  if (needed.length === 0) {
    return imports;
  }

  const go = {};
  imports.go = go;

  let memory = null;
  const getMemory = () => {
    if (memory) return memory;
    throw new Error("wasmgc.js: memory used before instantiation completed");
  };
  // Filled after instantiate.
  opts.attachMemory = (m) => {
    memory = m;
  };

  const sched = createScheduler(opts);
  opts.scheduler = sched;
  opts.attachExports = (exports) => {
    sched.attachExports(exports);
  };

  const hasSuspendingCtor = typeof WebAssembly.Suspending === "function";
  let needsPromising = false;

  for (const imp of needed) {
    const name = imp.name;
    let fn;
    switch (name) {
      case "runtime.write":
        fn = (fd, ptr, n) => {
          const mem = getMemory();
          const bytes = new Uint8Array(mem.buffer, ptr >>> 0, n >>> 0);
          if (typeof process !== "undefined" && process.stdout) {
            writeSync(fd === 2 ? 2 : 1, bytes);
          } else {
            const text = new TextDecoder().decode(bytes);
            if (fd === 2) console.error(text);
            else console.log(text);
          }
          return n | 0;
        };
        break;
      case "runtime.exit":
        fn = (code) => {
          sched.markExited();
          if (typeof process !== "undefined" && process.exit) {
            process.exit(code | 0);
          }
          goExit(code | 0);
        };
        break;
      case "runtime.walltime":
        fn = () => {
          const ms = Date.now();
          const sec = BigInt(Math.floor(ms / 1000));
          const nsec = ((ms % 1000) * 1e6) | 0;
          return [sec, nsec];
        };
        break;
      case "runtime.nanotime":
        fn = () => {
          if (typeof process !== "undefined" && process.hrtime?.bigint) {
            return process.hrtime.bigint();
          }
          return BigInt(Math.floor(performance.now() * 1e6));
        };
        break;
      case "runtime.rand":
        fn = (ptr, n) => {
          const mem = getMemory();
          const view = new Uint8Array(mem.buffer, ptr >>> 0, n >>> 0);
          crypto.getRandomValues(view);
        };
        break;
      case "runtime.park":
        fn = (id) => sched.park(id);
        break;
      case "runtime.ready":
        fn = (id) => sched.ready(id);
        break;
      case "runtime.spawn":
        fn = () => sched.spawn();
        break;
      case "runtime.gosched":
        fn = () => sched.gosched();
        break;
      case "runtime.scheduleTimer":
        fn = (id, lo, hi) => sched.scheduleTimer(id, lo, hi);
        break;
      case "runtime.clearTimer":
        fn = (id) => sched.clearTimer(id);
        break;
      case "runtime.getArgc":
        fn = () => (opts.programArgs?.length ?? 0) | 0;
        break;
      case "runtime.getArg":
      case "runtime.getEnv":
        // Linear-memory copies land with os.Args (M9). Length-only stubs for now.
        fn = () => 0;
        break;
      default:
        if (name.startsWith("js.")) {
          fn = () => {
            throw new Error(`wasmgc.js: unimplemented import go.${name}`);
          };
        } else {
          throw new Error(`wasmgc.js: unknown import go.${name}`);
        }
    }

    if (SUSPENDING_IMPORTS.has(name)) {
      if (!hasSuspendingCtor) {
        throw new Error(
          `wasmgc.js: module imports suspending go.${name}, but WebAssembly.Suspending is missing. ` +
            `Need Node.js 25+ (or 24 with --experimental-wasm-jspi). See docs/design/07-host-and-js.md.`,
        );
      }
      go[name] = new WebAssembly.Suspending(fn);
      needsPromising = true;
    } else {
      go[name] = fn;
    }
  }

  opts.needsPromising = needsPromising;
  return imports;
}

async function runInstance(instance, opts) {
  if (opts.attachMemory) {
    const mem = instance.exports.memory;
    if (mem) opts.attachMemory(mem);
  }
  if (opts.attachExports) {
    opts.attachExports(instance.exports);
  }

  const run = instance.exports.run;
  if (typeof run !== "function") {
    throw new Error("wasmgc.js: module has no exported function run");
  }

  let call = run;
  if (opts.needsPromising) {
    if (typeof WebAssembly.promising !== "function") {
      throw new Error("wasmgc.js: WebAssembly.promising required for suspending imports");
    }
    call = WebAssembly.promising(run);
  }

  try {
    const result = call();
    if (result && typeof result.then === "function") {
      // Microtask reaction: exit before any spawn macrotask (04, 07).
      await result.then(() => {
        if (opts.scheduler) opts.scheduler.markExited();
        if (typeof process !== "undefined" && process.exit) {
          process.exit(0);
        }
      });
    } else if (opts.scheduler) {
      // Non-suspending module: nothing parks; exit when run returns.
      opts.scheduler.markExited();
    }
  } catch (e) {
    if (e && e.name === "GoExit") {
      if (opts.scheduler) opts.scheduler.markExited();
      process.exit(e.code | 0);
    }
    throw e;
  }
}

async function main() {
  const { wasmPath, programArgs } = parseArgs(process.argv);
  const wasmBytes = readFileSync(wasmPath);
  const module = await WebAssembly.compile(wasmBytes);

  const opts = { programArgs };
  const importObject = buildImports(module, opts);

  // instantiate(Module) resolves to an Instance; instantiate(bytes) resolves
  // to { module, instance }. We already compiled, so take the Instance.
  const instance = await WebAssembly.instantiate(module, importObject);
  await runInstance(instance, opts);
}

const isMain =
  process.argv[1] &&
  fileURLToPath(import.meta.url) === resolve(process.argv[1]);

if (isMain) {
  main().catch((err) => {
    console.error(err);
    process.exit(1);
  });
}

export {
  buildImports,
  createScheduler,
  main,
  runInstance,
  SUSPENDING_IMPORTS,
};
