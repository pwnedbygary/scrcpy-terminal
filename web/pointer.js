// scterm pointer overlay: a visible answer to "where am I about to click?".
//
// The native cursor is hidden over the canvas (player.css), and the device
// cursor is not something the protocol can move -- so without this the window
// is a picture with no pointer. This module paints a purely LOCAL indication
// from the very same events the input path uses. It sends nothing, ever: it is
// an overlay, not a remote control.
//
// Mouse/pen hover gets a ghost ring that fills on press and drags a small
// trail. Touch has no hover to show, so it gets a ripple per finger that lives
// only while the finger is down. That asymmetry is the point: a mouse is
// always somewhere, a finger is only somewhere while it is touching.
"use strict";

const Pointer = {
  ghost: null,
  layer: null,
  down: {},
  seen: false,
  lastTrail: 0,

  attach() {
    this.layer = document.getElementById("pointer-layer") ||
      document.body.appendChild(document.createElement("div"));
    this.layer.id = "pointer-layer";
    this.ghost = document.createElement("div");
    this.ghost.id = "cursor-ghost";
    this.layer.appendChild(this.ghost);
    // Capture phase: the canvas handler calls preventDefault, and these must
    // see every event regardless of who stops it later.
    for (const t of ["pointermove", "pointerdown", "pointerup", "pointercancel", "pointerleave"]) {
      window.addEventListener(t, (ev) => this.event(t, ev), true);
    }
  },

  event(type, ev) {
    const kind = ev.pointerType === "touch" ? "touch" : "mouse";
    if (kind === "mouse") {
      if (type === "pointerleave") {
        this.ghost.classList.remove("on", "down", "drag");
        delete this.down[ev.pointerId];
        return;
      }
      this.ghost.classList.add("on");
      this.ghost.style.left = ev.clientX + "px";
      this.ghost.style.top = ev.clientY + "px";
      if (type === "pointerdown") {
        this.down[ev.pointerId] = {
          el: this.ghost,
          cls: ev.button === 2 ? "drag" : "down",
        };
        this.ghost.classList.toggle("drag", ev.button === 2);
        this.ghost.classList.toggle("down", ev.button !== 2);
      } else if (type === "pointermove") {
        const d = this.down[ev.pointerId];
        if (d) this.trail(ev.clientX, ev.clientY, ev.button === 2);
      } else {
        this.release(ev);
      }
      return;
    }

    // Touch: ripple per finger, removed when that finger lifts.
    if (type === "pointerdown") {
      const el = document.createElement("div");
      el.className = "ripple";
      el.style.left = ev.clientX + "px";
      el.style.top = ev.clientY + "px";
      this.layer.appendChild(el);
      this.down[ev.pointerId] = { el, cls: "" };
    } else if (type === "pointermove") {
      const d = this.down[ev.pointerId];
      if (d) this.trail(ev.clientX, ev.clientY, false, d.el);
    } else {
      this.release(ev);
    }
  },

  // release removes the touch ripple (or ends the mouse press state).
  release(ev) {
    const d = this.down[ev.pointerId];
    if (!d) return;
    delete this.down[ev.pointerId];
    if (ev.pointerType === "touch" && d.el) {
      const el = d.el;
      el.classList.add("out");
      setTimeout(() => el.remove(), 380);
    } else {
      this.ghost.classList.remove("down", "drag");
    }
  },

  // trail drops a fading dot behind a moving press. Throttled by time rather
  // than distance so a fast flick does not paint hundreds of nodes.
  trail(x, y, rightButton, touchEl) {
    const now = performance.now();
    if (now - this.lastTrail < 26) return;
    this.lastTrail = now;
    const el = document.createElement("div");
    el.className = "trail";
    el.style.left = x + "px";
    el.style.top = y + "px";
    if (rightButton) el.style.background = "rgba(120,170,255,.55)";
    if (touchEl) el.style.background = "rgba(140,200,255,.5)";
    this.layer.appendChild(el);
    setTimeout(() => el.remove(), 520);
  },
};
