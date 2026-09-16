// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.
//
// Real thrown-physics dice, replacing dice3d.js's per-bubble contained-spin
// system (see `git log dice3d.js` for that earlier generation, and this
// project's docs/design.md and master/web/README.md for the switch's own
// reasoning). One shared `DiceBox` instance (vendor/dice-box-threejs.es.js
// — see vendor/README.md) owns a single physics arena overlaying the chat
// log (`#dice-arena`, sized to `.log-frame`'s visible box — see index.html
// and style.css's `.dice-arena` rule), instead of a small isolated canvas
// per die. Every roll in this app is revealed one die at a time — a player
// clicks their own die, or a spectator's die fills in the instant the
// roller reveals it (see app.js's "client.roll* interactive dice" section)
// — so this module's public throw API is intentionally per-die
// (`throwDie(sides, result)`), not the batch `throwDice(diceSpecs)` shape
// floated while planning this rework: nothing in this app ever actually
// has more than one die's reveal to kick off at once, and a per-die API
// matches that exactly rather than adding batching machinery no call site
// would use. Independent per-die throws (two different bubbles' dice
// clicked close together, or a spectator reveal landing while the local
// roller is also mid-throw) still can't step on each other — see "Why a
// serial queue" below — so the *effect* the batch design was reaching for
// (concurrent reveals don't cross-wire or corrupt each other) is achieved
// anyway, just by queuing independent calls rather than grouping them.
//
// --- Why a serial queue (a real, confirmed library bug, not caution for
// its own sake) ---
//
// The natural design once `DiceBox.add()` is in the picture would be "call
// add() for every reveal, whenever it happens, and trust the library to
// let a new throw land alongside whatever's already mid-air" — that's
// literally what `add()`'s own implementation looks like it's for
// (`if (!this.diceList.length) return this.roll(e)`, i.e. "safe to call
// even from empty"). That trust turned out to be misplaced for the
// *concurrent* case specifically, confirmed by reading `startClickThrow`/
// `clearDice` in the vendored source and then reproducing it in a real
// headless-browser run (not just from reading the code): `add()`/`roll()`
// both route through `startClickThrow`, which does
// `this.rolling && (this.clearDice(), this.rolling = !1)` — and
// `this.rolling` stays `true` for this *entire* library's whole physics
// animation, not just its initial spawn instant. Calling `add()` again
// while an earlier throw is still mid-air does not "throw in alongside"
// it the way the plan (reasonably, given the library's own framing)
// assumed — it calls `clearDice()`, which rips every current die
// (including the still-animating one) out of the scene and physics world.
// Worse than a visual glitch: the *first* throw's own `animateThrow` loop
// keeps a closed-over copy of `this.running` from when it started, checks
// `this.running == e` every frame before continuing or resolving, and
// `clearDice()`/the second `startClickThrow()` call stomps `this.running`
// to a new value — so the first throw's loop condition goes false forever
// and its promise *never resolves*. Reproduced directly: firing a second
// `add()` ~200ms into a first throw's roughly one-second animation left
// the first call's promise permanently unresolved (15+ second wait,
// no resolution) and the second call resolved with an empty `[]` result
// array instead of the forced value it asked for. A caller `await`-ing
// that first promise (which is exactly what this module needs to do, to
// know when a die has settled and can fly home) would hang forever.
//
// The fix here is not a library patch (out of scope — see the plan this
// module implements) but never letting the collision happen in the first
// place: every call that touches the shared `DiceBox` instance
// (`add`/`remove`/`setDimensions`) is funneled through `runQueued`, a
// plain promise chain that only starts a task once the previous one has
// fully settled. Two rolls "close together" in this app now animate one
// after another rather than physically overlapping in the tray — a real,
// visible difference from the plan's original "land together like a real
// tabletop" framing, honestly a downgrade from that ambition, but the
// alternative (trusting `add()`'s own concurrency claim) is a confirmed
// hang, not a cosmetic risk. See master/web/README.md's changelog entry
// for this rework for the same finding in narrative form.

// --- Palette ---
//
// A custom colorset (ivory body, dark ink glyphs, brass edge) rather than
// one of the library's built-ins, matching this app's existing parchment/
// brass theme (style.css's --parchment/--parchment-ink/--brass — see
// dice3d.js's own BASE_COLOR comment for the same "warm ivory sits
// naturally in this app's theme" reasoning, carried forward here).
// `texture: "none"` deliberately — the library's optional surface-pattern
// webp skins (theme_texture) aren't vendored (see vendor/README.md), so a
// flat color is the only option that doesn't 404 a missing asset.
const DIE_COLORSET = {
  foreground: "#2c2013", // --parchment-ink
  background: "#efe1c4", // --parchment
  outline: "#cc9a54", // --brass
  texture: "none",
};

// ARENA_SELECTOR must match the element index.html adds inside .log-frame
// (see that file's own comment on #dice-arena) — DiceBox reads this via
// `document.querySelector` in its own constructor (confirmed by reading
// the vendored source: `this.container = document.querySelector(e)`, a
// selector *string*, not an element reference), so this has to stay a
// selector, not a DOM node handed over some other way.
const ARENA_SELECTOR = "#dice-arena";

// RESIZE_DEBOUNCE_MS bounds how often a live window resize drag reissues
// DiceBox.setDimensions() (see handleResize below) — cheap enough on its
// own (it's a synchronous rebuild of the physics walls + camera, not a
// network/theme reload) that this is about avoiding needless work mid-drag
// more than correctness, but there's no reason to run it every single
// resize event when the user is still actively dragging.
const RESIZE_DEBOUNCE_MS = 150;

// CROP_PADDING_CSS_PX pads the cropped region around a settled die's own
// projected bounding box (see boundsForMesh) so the fly-home image isn't
// razor-cropped right up against the die's silhouette — a little breathing
// room (and its drop shadow) reads better than a hard edge.
const CROP_PADDING_CSS_PX = 6;

let boxPromise = null; // set once createBox() has been kicked off — see ensureBox.
let queue = Promise.resolve(); // see runQueued and the "Why a serial queue" doc block above.

// runQueued appends `task` to the shared serial queue and returns a
// promise for *its own* outcome (resolving or rejecting exactly as `task`
// does) without letting one task's failure block whatever queues up after
// it — the internal chain (`queue`) always continues on a resolved state,
// but each caller still observes their own task's real result via the
// promise this function returns. See the "Why a serial queue" doc block
// for why every DiceBox call in this module goes through here rather than
// being called directly.
function runQueued(task) {
  const outcome = queue.then(task, task);
  queue = outcome.then(
    () => undefined,
    () => undefined,
  );
  return outcome;
}

// ensureBox lazily creates and initializes the one shared DiceBox instance
// this whole app uses, the first time any die is actually thrown — not at
// module load / page load. Dynamic `import()` (not a static top-of-file
// import) is deliberate: a static import would still make the browser
// fetch and parse the vendored ~700KB module as soon as app.js pulls this
// file in, even though `new DiceBox()`/`.initialize()` were already
// deferred — the whole point (per the plan this implements) is that a
// session where nobody ever rolls dice pays nothing for this module at
// all. Every call this module makes into the resulting DiceBox instance
// still goes through runQueued (see above) — including this initial
// construction — so a throw that arrives before initialization finishes
// simply waits its turn rather than racing it.
function ensureBox() {
  if (!boxPromise) boxPromise = createBox();
  return boxPromise;
}

async function createBox() {
  const { default: DiceBox } = await import("./vendor/dice-box-threejs.es.js");
  const box = new DiceBox(ARENA_SELECTOR, {
    sounds: false, // no vendored audio assets (see vendor/README.md) — avoid a 404 per die.
    theme_customColorset: DIE_COLORSET,
    theme_material: "plastic", // opaque matte finish — "glass" (the library default) softened number contrast at this render size.
  });
  await box.initialize();
  window.addEventListener("resize", handleResize);
  return box;
}

let resizeTimer = null;
// handleResize keeps the arena's physics walls and top-down camera sized
// to #dice-arena's *current* box after the page's own layout changes size
// (see .dice-arena's CSS — it always fills .log-frame, so its size tracks
// the window). Without this, a roll made after a resize would still
// bounce against walls built for whatever size the arena was when the
// DiceBox was first constructed (see ensureBox — that's lazy, so it could
// be long after page load), which could visibly let dice cross past the
// *current* .log-frame edge. `setDimensions()` (no argument) is the
// correct call for this, verified against the library's own source and a
// real resize-then-throw run in a headless browser — NOT `updateConfig()`,
// which is a same-named-sounding but unrelated *theme* reloader (colorset/
// texture/material) that never touches physics bounds or the camera at
// all; calling it here would silently do nothing useful. `setDimensions`
// itself reads `this.container.clientWidth/Height` fresh every call, so
// no argument is needed to pick up the live size.
function handleResize() {
  if (!boxPromise) return; // nothing constructed yet — the next ensureBox() will read the live size anyway.
  window.clearTimeout(resizeTimer);
  resizeTimer = window.setTimeout(() => {
    runQueued(async () => {
      const box = await boxPromise;
      box.setDimensions();
    });
  }, RESIZE_DEBOUNCE_MS);
}

// normalizeAddResult flattens DiceBox.add()'s two different resolved
// shapes into one common `[{id, sides, value}, ...]` list. Confirmed by
// direct testing (not assumed from the library's own docs): when the
// shared tray is empty, `add()` delegates to `roll()` internally (see this
// module's own top-of-file doc block) and resolves with `roll()`'s
// *aggregate* shape (`{notation, sets: [{rolls: [...]}], modifier,
// total}`); once the tray already holds at least one die, later `add()`
// calls instead resolve with a flat per-die array
// (`[{type, sides, id, value, label, reason}, ...]`). Since this module's
// serial queue means every throw in this app is exactly one die, `raw`
// here only ever has one entry either way — but which *shape* wraps that
// one entry depends on whether this happens to be the very first roll of
// the session, so both have to be handled rather than assuming the array
// form.
function normalizeAddResult(raw) {
  if (Array.isArray(raw)) return raw;
  const flat = [];
  for (const set of raw.sets) flat.push(...set.rolls);
  return flat;
}

// --- Fly-home projection math ---
//
// See this module's top-of-file doc block's sibling concern (the serial
// queue) for the *other* high-risk piece of this rework; this is the
// first one — turning a settled die's 3D position into the on-screen
// pixel region a 2D crop/fly-home animation can use. Verified against
// real rendered screenshots in a headless browser during development, not
// just checked as math on paper (see master/web/README.md's changelog
// entry for this rework for the specifics of what was actually rendered
// and inspected) — the exact same discipline the project's earlier
// face-orientation bug (see dice3d.js's own doc comments) was caught
// missing until an independent pixel-level check happened.
//
// mulMat4Vec4 multiplies a column-major 4x4 matrix (the `.elements` layout
// every three.js `Matrix4` uses, including `camera.projectionMatrix` and
// `camera.matrixWorldInverse` below) by a homogeneous 4-vector. This
// module never imports three.js itself (see ensureBox's doc comment on
// why this file only ever dynamically imports the vendored DiceBox
// bundle, and even then not eagerly) — `dice-box-threejs.es.js` has no
// export besides the `DiceBox` class itself, so there's no `THREE.Vector3`
// to borrow. Reimplementing this one multiply by hand, against the
// well-documented standard `Matrix4.elements` layout, is simpler and more
// honest than trying to smuggle in a second copy of three.js just for
// this.
function mulMat4Vec4(m, x, y, z, w) {
  return [
    m[0] * x + m[4] * y + m[8] * z + m[12] * w,
    m[1] * x + m[5] * y + m[9] * z + m[13] * w,
    m[2] * x + m[6] * y + m[10] * z + m[14] * w,
    m[3] * x + m[7] * y + m[11] * z + m[15] * w,
  ];
}

// projectToNDC runs one world-space point through the camera's view and
// projection matrices (the same two-matrix pipeline `Object3D.project`
// uses internally in three.js itself) and returns normalized device
// coordinates (each axis in [-1, 1] once a point is in view). `camera` is
// DiceBox's own live `box.camera` — a real three.js PerspectiveCamera
// instance, positioned top-down looking at the tray's center (confirmed
// by reading `setDimensions`: the camera only ever gets a `position.z`
// assignment plus `lookAt(0, 0, 0)`, and the physics world's gravity is
// likewise along Z — this arena is Z-up, not the Y-up convention three.js
// scenes more commonly use) — so `matrixWorldInverse` is already current
// as of the frame DiceBox itself most recently rendered, which for a
// just-settled die is the frame `add()`'s own promise resolved on.
function projectToNDC(camera, worldX, worldY, worldZ) {
  const view = camera.matrixWorldInverse.elements;
  const [vx, vy, vz, vw] = mulMat4Vec4(view, worldX, worldY, worldZ, 1);
  const proj = camera.projectionMatrix.elements;
  const [px, py, , pw] = mulMat4Vec4(proj, vx, vy, vz, vw);
  return [px / pw, py / pw];
}

// boundsForMesh projects a settled die's own geometry bounding box (its 8
// local-space corners, transformed into world space and then through the
// camera — not just its center point, which would give a location but no
// size) into a CSS-pixel rectangle relative to the arena container's own
// top-left corner. Using the real per-die geometry bounding box (computed
// once and cached by three.js on first call, via `computeBoundingBox`)
// rather than a fixed guessed radius means this works unmodified for every
// die shape this app supports — a d4's bounding box is a different size
// and aspect than a d20's, and this never has to know that by name.
function boundsForMesh(box, mesh) {
  mesh.updateMatrixWorld(true);
  box.camera.updateMatrixWorld(true);
  if (!mesh.geometry.boundingBox) mesh.geometry.computeBoundingBox();
  const bb = mesh.geometry.boundingBox;
  const mw = mesh.matrixWorld.elements;

  const containerWidth = box.container.clientWidth;
  const containerHeight = box.container.clientHeight;

  let minX = Infinity;
  let minY = Infinity;
  let maxX = -Infinity;
  let maxY = -Infinity;
  for (const lx of [bb.min.x, bb.max.x]) {
    for (const ly of [bb.min.y, bb.max.y]) {
      for (const lz of [bb.min.z, bb.max.z]) {
        const [wx, wy, wz, ww] = mulMat4Vec4(mw, lx, ly, lz, 1);
        const [ndcX, ndcY] = projectToNDC(box.camera, wx / ww, wy / ww, wz / ww);
        // NDC -> CSS pixels relative to the container's own box. Y flips
        // (NDC's +1 is screen-up; CSS pixels grow downward).
        const px = ((ndcX + 1) / 2) * containerWidth;
        const py = ((1 - ndcY) / 2) * containerHeight;
        minX = Math.min(minX, px);
        minY = Math.min(minY, py);
        maxX = Math.max(maxX, px);
        maxY = Math.max(maxY, py);
      }
    }
  }

  return {
    left: minX - CROP_PADDING_CSS_PX,
    top: minY - CROP_PADDING_CSS_PX,
    width: maxX - minX + CROP_PADDING_CSS_PX * 2,
    height: maxY - minY + CROP_PADDING_CSS_PX * 2,
  };
}

// cropDieImage crops the shared arena canvas down to just the settled
// die's own on-screen region (see boundsForMesh) and returns it as a data
// URL — the same toDataURL()-a-canvas-region technique dice3d.js used for
// its own per-die freeze frames (see that file's top-of-file doc block),
// just cropping a *sub-region* of one shared canvas here instead of
// reading a whole dedicated one per die. `cssBounds` is in CSS pixels
// relative to the container; the canvas itself can be rendered at a
// higher internal resolution (devicePixelRatio), so the crop's source
// rect is scaled up by canvas-px-per-CSS-px before reading, and clamped to
// the canvas's actual bounds — a die resting right at the tray's edge can
// otherwise produce a bounding box that pokes slightly past the canvas,
// which would make drawImage throw or silently draw nothing.
function cropDieImage(box, cssBounds) {
  const canvas = box.renderer.domElement;
  const scaleX = canvas.width / box.container.clientWidth;
  const scaleY = canvas.height / box.container.clientHeight;

  const sx = Math.max(0, cssBounds.left * scaleX);
  const sy = Math.max(0, cssBounds.top * scaleY);
  const sw = Math.min(canvas.width - sx, cssBounds.width * scaleX);
  const sh = Math.min(canvas.height - sy, cssBounds.height * scaleY);

  const offscreen = document.createElement("canvas");
  offscreen.width = Math.max(1, sw);
  offscreen.height = Math.max(1, sh);
  offscreen.getContext("2d").drawImage(canvas, sx, sy, sw, sh, 0, 0, offscreen.width, offscreen.height);
  return offscreen.toDataURL("image/png");
}

// viewportRectForBounds converts a container-relative CSS-pixel rect (see
// boundsForMesh) into a viewport-absolute rect suitable as the *starting*
// position for a `position: fixed` fly-home animation — see app.js's
// settleDie, which is the only caller that needs this: it animates a
// freshly-cropped `<img>` from here to the die button's own
// `getBoundingClientRect()`. `#dice-arena` never scrolls (it's an overlay
// on `.log-frame`, not `#log` — see index.html's own comment on that
// wrapper), so the arena's own `getBoundingClientRect()` is stable for the
// short lifetime of one fly-home animation; it's still read fresh here
// rather than cached, since nothing about this module assumes it can't
// change between throws (a sidebar toggling, a window resize).
function viewportRectForBounds(box, cssBounds) {
  const arenaRect = box.container.getBoundingClientRect();
  return {
    left: arenaRect.left + cssBounds.left,
    top: arenaRect.top + cssBounds.top,
    width: cssBounds.width,
    height: cssBounds.height,
  };
}

// D4_FORCING_IS_UNRELIABLE documents a real, confirmed bug in the
// vendored library, found and fully diagnosed this session — this
// project's own equivalent of the sibling dice-themes forcing bug found
// and fixed elsewhere this session, except this one could not be fixed
// from outside the vendored file (see below).
//
// Every other shape's forced value was verified correct both in the
// library's own reported result AND in a real rendered screenshot (see
// master/web/README.md's changelog entry for the specifics of that
// sweep). d4 is the one exception: `swapDiceFace_D4` (the vendored
// library's own d4-specific forcing path, structurally different from
// every other shape's `swapDiceFace` — it rewrites geometry-group
// `materialIndex`es by a modular rotation offset instead of directly
// swapping two groups) does not actually change what a player would
// read. Diagnosed by comparing three independent signals across many
// throws, not assumed from one look:
//   1. `getDiceResults()`'s own reported `value` for a *forced* d4 throw
//      consistently comes back as the die's natural, pre-forced roll —
//      confirmed by reading swapDiceFace_D4 itself: unlike its sibling
//      swapDiceFace (which ends with `e.result = []`, clearing the cache
//      so the next read is fresh), swapDiceFace_D4 never touches
//      `e.result` at all, so `getLastValue()` keeps returning the old
//      cached natural roll forever after.
//   2. That alone could just be a metadata bug with the *visual* still
//      correct — so this was checked independently, by projecting the
//      settled mesh's own geometry (its material-group face normals) to
//      find exactly which face sits at the die's true "up" reading
//      position (confirmed separately, via *unforced* natural rolls,
//      that the number at that position is the one `getDiceResults()`
//      reports for a natural roll — establishing that this position
//      really is what a player reads, before ever looking at the forced
//      case). For a *forced* roll, the number actually sitting at that
//      same position — in real rendered screenshots, at production
//      colors/contrast — is the natural pre-forced roll, not the forced
//      value. The geometry itself was never relabeled where it visually
//      matters, not just the metadata describing it.
//   3. A *different* geometry group (the hidden, table-touching bottom
//      face — never seen by any camera angle a player would use) DOES
//      decode to the correct forced value via the library's own
//      materialIndex convention, confirmed by direct inspection. So
//      `swapDiceFace_D4` is doing *something* consistent, just not the
//      thing that makes a player see the right number.
//
// No client-side fix was viable: the relabeling itself happens entirely
// inside the vendored library before this module ever gets the settled
// mesh, and re-deriving "which face should show the forced value" well
// enough to hand-paint it back in would mean reimplementing this
// library's own d4 texture-atlas/UV scheme from scratch — squarely out
// of scope for this integration. This is shipped anyway, same as every
// other shape, rather than special-cased with some other visual (a text
// overlay would contradict the operator's own standing "the die's face
// is the only thing communicating the result, no text layer" preference
// — see this file's own doc block on that same point) — a d4 roll in
// this app may show the wrong face. The *authoritative* result is
// unaffected: it's decided server-side and always shown as text
// elsewhere in the same bubble (design doc's "never something a client
// computes or can override") regardless of what this one die's own
// picture happens to show. This is a wider, more definite version of
// dice3d.js's own d4 caveat (see that file's history) — that one wasn't
// sure which of a face's three numbers a viewer's eye would land on;
// this one is a confirmed case of the wrong number being paintable at
// all.
const D4_FORCING_IS_UNRELIABLE = true;

// throwDie is this module's one public entry point: throw a single die of
// `sides` (4/6/8/10/12/20) into the shared arena, forced to `result` (the
// server-decided value — design doc §3.1/§4; this module only ever plays
// it back, never computes it), and resolve once it has physically settled
// and been captured as a cropped image ready to fly home. `result` is
// passed straight through to the library's own "@value" forcing notation
// unchanged, including for d10: a physical d10 is printed 0-9, and (like
// dice3d.js before it, and confirmed by reading swapDiceFace in the
// vendored source — it special-cases `d10 && value == 0` to mean the same
// face as `10`) dice-box-threejs already treats a result of 10 as that
// printed-0 face, so no client-side remapping is needed here either. See
// D4_FORCING_IS_UNRELIABLE above for d4's own, different story.
//
// Resolves to `{ dataUrl, rect }`: `dataUrl` is the cropped PNG of the
// settled die (see cropDieImage), and `rect` is its current on-screen
// position in viewport CSS pixels (see viewportRectForBounds) — the
// starting rect for a fly-home CSS transition. The die is removed from
// the live arena (`box.remove`) before this resolves, in the same queued
// turn that threw and captured it — see the "Why a serial queue" doc
// block above for why that matters for *concurrency* safety, but the
// remove() call specifically is also about not leaving a duplicate: if
// the 3D die stayed in the arena after being captured, it would still be
// visibly sitting at its resting spot in the tray *while* the cropped
// `<img>` copy of it also animates away from that same spot, reading as
// two dice where there was only ever one.
export async function throwDie(sides, result) {
  return runQueued(async () => {
    const box = await ensureBox();
    const notation = `1d${sides}@${result}`;
    const raw = await box.add(notation);
    const [entry] = normalizeAddResult(raw);

    if (!entry) {
      throw new Error(`dice-arena: throw of "${notation}" produced no die`);
    }
    if (Number(entry.value) !== Number(result) && !(sides === 4 && D4_FORCING_IS_UNRELIABLE)) {
      // The forced value SHOULD always match what was asked for, for
      // every shape except the confirmed, documented d4 exception above
      // (skipped here so a known, understood library bug doesn't spam
      // the console on every single d4 roll in production). This branch
      // firing for any OTHER shape would mean a new, not-yet-seen
      // forcing failure — the authoritative result was already decided
      // server-side and is rendered as text elsewhere in this bubble
      // regardless (design doc's "never something a client computes or
      // can override"); only this die's own visual face would be wrong,
      // not the game state.
      console.error(
        `dice-arena: forced d${sides} to ${result} but the die reports ${entry.value} - the visual face may be wrong; the recorded result is unaffected`,
      );
    }

    const mesh = box.diceList[entry.id];
    const cssBounds = boundsForMesh(box, mesh);
    const dataUrl = cropDieImage(box, cssBounds);
    const rect = viewportRectForBounds(box, cssBounds);

    await box.remove([entry.id]);

    return { dataUrl, rect };
  });
}
