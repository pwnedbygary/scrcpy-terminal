//   node web/pointer_test.mjs
//
// Drives the real pointer overlay out of web/pointer.js with synthetic pointer
// events against a small DOM shim, asserting what the user sees: the mouse
// ghost appears on hover, fills on press, morphs on drag, and touch gets
// ripples that exist only while a finger is down. The overlay is paint-only by
// contract, so the strongest assertion is also here: none of it may ever be
// mistaken for device input.
import { readFileSync } from "node:fs";
import vm from "node:vm";

const src = readFileSync(new URL("./pointer.js", import.meta.url), "utf8");

function el(tag) {
  const e = {
    tagName: tag, id: "", className: "", style: {}, children: [],
    classList: {
      _s: new Set(),
      add(...c) { c.forEach(x => this._s.add(x)); },
      remove(...c) { c.forEach(x => this._s.delete(x)); },
      contains(c) { return this._s.has(c); },
      toggle(c, on) { on ? this._s.add(c) : this._s.delete(c); },
    },
    appendChild(c) { this.children.push(c); return c; },
    remove() {},
  };
  return e;
}
const layer = el("div");
const body = { appendChild: (c) => { body.children.push(c); return c; }, children: [] };
const doc = {
  body,
  getElementById(id) { return id === "pointer-layer" ? layer : null; },
  createElement: (t) => el(t),
};
const listeners = {};
const win = { addEventListener: (t, fn) => { (listeners[t] ||= []).push(fn); } };

const ctx = { document: doc, window: win, performance: { now: () => Date.now() }, setTimeout: (fn) => 0, clearTimeout };
vm.createContext(ctx);
vm.runInContext(src + "\n;globalThis.__P = Pointer;", ctx);
const P = ctx.__P;
P.attach();

let pass = 0, fail = 0;
const check = (name, got, want) => {
  const g = JSON.stringify(got), w = JSON.stringify(want);
  if (g === w) { pass++; console.log("  ok  ", name); }
  else { fail++; console.log("  FAIL", name, "\n    got ", g, "\n    want", w); }
};

const fire = (t, ev) => (listeners[t] || []).forEach((fn) => fn(ev));
const mouse = (id, x, y, button = 0) => ({ pointerId: id, pointerType: "mouse", clientX: x, clientY: y, button });
const touch = (id, x, y, button = 0) => ({ pointerId: id, pointerType: "touch", clientX: x, clientY: y, button });

// ghost exists and tracks a mouse
check("ghost element created", P.ghost && P.ghost.id, "cursor-ghost");
check("ghost starts hidden", P.ghost.classList.contains("on"), false);
fire("pointermove", mouse(1, 100, 150));
check("mouse hover shows the ghost", P.ghost.classList.contains("on"), true);
check("ghost follows x", P.ghost.style.left, "100px");
check("ghost follows y", P.ghost.style.top, "150px");
fire("pointerdown", mouse(1, 101, 151));
check("press fills the ghost", P.ghost.classList.contains("down"), true);
check("press not marked as drag", P.ghost.classList.contains("drag"), false);
fire("pointerup", mouse(1, 101, 151));
check("release clears the press", P.ghost.classList.contains("down"), false);
check("release keeps the ghost", P.ghost.classList.contains("on"), true);
fire("pointerleave", mouse(1, 101, 151));
check("leaving hides the ghost", P.ghost.classList.contains("on"), false);

// right-button drag morphs to drag style and leaves trail nodes
const before = layer.children.length;
fire("pointermove", mouse(2, 200, 200));
fire("pointerdown", mouse(2, 200, 200, 2));
check("right press is a drag", P.ghost.classList.contains("drag"), true);
P.lastTrail = 0;
fire("pointermove", mouse(2, 210, 210, 2));
check("drag leaves a trail", layer.children.length > before, true);

// touch: ripple exists while down, marked out on release
const base = layer.children.length;
fire("pointerdown", touch(3, 50, 60));
check("touch creates a ripple", layer.children.length, base + 1);
const ripple = layer.children[layer.children.length - 1];
check("ripple is a ripple", ripple.className, "ripple");
fire("pointermove", touch(3, 60, 70));
fire("pointerup", touch(3, 60, 70));
check("release fades the ripple", ripple.classList.contains("out"), true);
check("release forgets the finger", P.down[3], undefined);
// mouse hover must not create ripples
const base2 = layer.children.length;
fire("pointermove", mouse(4, 90, 90));
check("mouse hover creates no ripple", layer.children.length, base2);

console.log(`\n${pass} passed, ${fail} failed`);
process.exit(fail ? 1 : 0);
