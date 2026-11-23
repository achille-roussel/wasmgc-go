#!/usr/bin/env node
// Host glue for wasmgc-go modules. See docs/design/07-host-and-js.md.
//
// Instantiates a module, supplies only the imports the module declares,
// calls the `run` export, and handles exit codes. Does not reference
// WebAssembly.Suspending unless the module imports a suspending function
// (so Node 22 can run non-parking programs).

import { readFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { writeSync } from "node:fs";

const SUSPENDING_IMPORTS = new Set([
  "runtime.park",
  "runtime.gosched",
  // js.call may suspend when awaiting; only wrap when the compiler marks it.
  // For now the compiler emits //go:wasmgc suspending on park/gosched only.
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
      case "runtime.gosched":
      case "runtime.ready":
      case "runtime.spawn":
      case "runtime.scheduleTimer":
      case "runtime.clearTimer":
      case "runtime.getArgc":
      case "runtime.getArg":
      case "runtime.getEnv":
        // Stubs sufficient for modules that declare but do not call them yet.
        fn = stubFor(name, opts);
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

function stubFor(name, opts) {
  switch (name) {
    case "runtime.park":
      return (id) => {
        throw new Error(`wasmgc.js: runtime.park(${id}) not wired in this glue build`);
      };
    case "runtime.gosched":
      return () => Promise.resolve();
    case "runtime.ready":
      return (_id) => {};
    case "runtime.spawn":
      return () => {};
    case "runtime.scheduleTimer":
      return () => {};
    case "runtime.clearTimer":
      return () => {};
    case "runtime.getArgc":
      return () => (opts.programArgs?.length ?? 0) | 0;
    case "runtime.getArg":
    case "runtime.getEnv":
      return () => 0;
    default:
      return () => {};
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

  if (opts.attachMemory) {
    const mem = instance.exports.memory;
    if (mem) opts.attachMemory(mem);
  }

  const run = instance.exports.run;
  if (typeof run !== "function") {
    throw new Error(`wasmgc.js: module ${wasmPath} has no exported function run`);
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
      await result;
    }
  } catch (e) {
    if (e && e.name === "GoExit") {
      process.exit(e.code | 0);
    }
    throw e;
  }
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

export { buildImports, main, SUSPENDING_IMPORTS };
