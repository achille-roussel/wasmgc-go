// Run prototypes/jspi-sched/sched.wat.
// Requires Node.js 25+ (WebAssembly.Suspending / promising, no flag)
// and `wasm-tools` on PATH.
import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

if (typeof WebAssembly.Suspending !== "function" || typeof WebAssembly.promising !== "function") {
  console.error(
    `node ${process.version} has no WebAssembly.Suspending. ` +
      "JSPI is on by default in Node 25+; Node 24 needs --experimental-wasm-jspi; Node 22 only has the older Suspender.",
  );
  process.exit(1);
}

const dir = dirname(fileURLToPath(import.meta.url));
const out = join(tmpdir(), "wasmgc-jspi-sched.wasm");
execFileSync("wasm-tools", ["parse", join(dir, "sched.wat"), "-o", out], { stdio: "inherit" });
const wasm = readFileSync(out);

const waiters = new Map();
const alreadyReady = new Set();
const parkShape = [];
const log = [];

function park(id) {
  if (alreadyReady.delete(id)) {
    parkShape.push("value");
    return id;
  }
  if (id === 9) {
    parkShape.push("reject");
    return Promise.reject(new Error("host rejected"));
  }
  parkShape.push("promise");
  return new Promise((resolve) => {
    waiters.set(id, resolve);
  });
}

function ready(id) {
  const resolve = waiters.get(id);
  if (resolve) {
    waiters.delete(id);
    resolve(id);
    return;
  }
  alreadyReady.add(id);
}

let spawnKind = "child";
function spawn() {
  const kind = spawnKind;
  setTimeout(() => {
    log.push("macrotask:" + kind);
    if (kind === "child") {
      WebAssembly.promising(exports.child)();
    }
  }, 0);
}

function makeBox() {
  return exports.newBox();
}

function susExt() {
  return Promise.resolve({ tag: "from-js" });
}

function idExt(v) {
  return v;
}

const imports = {
  host: {
    park: new WebAssembly.Suspending(park),
    ready,
    spawn,
    log: (v) => log.push("wasm:" + v),
    makeBox,
    susExt: new WebAssembly.Suspending(susExt),
    idExt,
  },
};

const { instance } = await WebAssembly.instantiate(wasm, imports);
const exports = instance.exports;

function check(name, got, want) {
  if (got !== want) {
    throw new Error(`${name}: got ${JSON.stringify(got)}, want ${JSON.stringify(want)}`);
  }
  console.log(`ok  ${name} = ${JSON.stringify(got)}`);
}

// Calling a suspending import outside promising must fail.
let direct = "";
try {
  exports.local();
  direct = "returned";
} catch (err) {
  direct = err.constructor.name + ": " + err.message;
}
if (!/suspend/i.test(direct)) {
  throw new Error("direct call should fail to suspend, got " + direct);
}
console.log("ok  direct call without promising: " + direct);
parkShape.length = 0;
waiters.clear();

const localP = WebAssembly.promising(exports.local)();
ready(3);
check("i32 local survives suspend", await localP, 42);

const boxP = WebAssembly.promising(exports.boxy)();
ready(2);
check("struct ref local survives suspend", await boxP, 7);

const twiceP = WebAssembly.promising(exports.twice)();
ready(4);
ready(4);
check("local survives two suspends", await twiceP, 11);

spawnKind = "child";
log.length = 0;
const parentP = WebAssembly.promising(exports.parent)();
check("parent restores g after child clobber", await parentP, 1);
check("child ran and logged the clobber", log.includes("wasm:99"), true);
check("child was a macrotask", log.includes("macrotask:child"), true);

// ready-before-park returns a bare value, so the import does not suspend.
parkShape.length = 0;
check("ready-before-park result", await WebAssembly.promising(exports.early)(), 5);
check("ready-before-park did not return a Promise", parkShape[0], "value");

check("import can return a wasm struct ref", exports.fromHost(), 5);

const token = { tag: "same" };
check("externref round-trip", exports.idExt(token) === token, true);

const suspended = await WebAssembly.promising(exports.susExt)();
check("suspending import can return an externref", suspended.tag, "from-js");

let rejected = "";
try {
  await WebAssembly.promising(exports.reject)();
  rejected = "resolved";
} catch (err) {
  rejected = err.message;
}
check("rejected promise enters the wasm activation", /host rejected/.test(rejected), true);

// Main-return vs a queued spawn: the promise reaction is a microtask,
// the spawn is a macrotask, so the reaction runs first.
spawnKind = "exit";
log.length = 0;
const exitP = WebAssembly.promising(exports.exit)();
exitP.then(() => log.push("microtask:main-then"));
await exitP;
log.push("microtask:awaited");
await new Promise((resolve) => setTimeout(resolve, 0));
const microAt = log.indexOf("microtask:main-then");
const macroAt = log.indexOf("macrotask:exit");
if (!(microAt !== -1 && macroAt !== -1 && microAt < macroAt)) {
  throw new Error("expected the main reaction before the spawn macrotask, got " + log.join(","));
}
console.log("ok  main promise reaction runs before spawn macrotask: " + log.join(" "));

// Parked main runs a spawned child (already covered by parent/child above).
// Returning main does not: reinforce with an explicit "child must not have
// run before the main reaction" check on the exit case log ordering.
check("exit case did not run child export before main reaction", microAt < macroAt, true);

console.log(`node ${process.version}: JSPI prototype passed`);

// Real glue (lib/wasmgc.js) park/ready/spawn/_sched — M7 streams B+F.
const { spawnSync } = await import("node:child_process");
const glueRun = spawnSync(process.execPath, [join(dir, "run_glue.mjs")], {
  encoding: "utf8",
  stdio: ["ignore", "pipe", "pipe"],
});
if (glueRun.status !== 0) {
  console.error(glueRun.stdout);
  console.error(glueRun.stderr);
  process.exit(glueRun.status ?? 1);
}
process.stdout.write(glueRun.stdout);
