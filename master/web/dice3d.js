// Copyright (c) 2026 James Duane Plotts. Licensed under the MIT License.
// See LICENSE in the repository root.
//
// A real WebGL die (three.js) with a physics-driven tumble (cannon-es),
// rendered *inside* the chat log's own per-die slot, replacing the SVG
// silhouette dice that briefly lived at this call site. Recovered/adapted
// from the earlier standalone dice tray (deleted in git history — see
// `git show 2f4f3cc~1:master/web/dice.js`), which rendered a single
// physics-tumbled d20 in a dedicated tray area disconnected from the chat
// log. That tray is gone and is not coming back: this module owns no
// page-level arena, no viewport-sized walls, no per-bubble colliders —
// every die tumbles inside a small box scaled to its own canvas only
// (hard constraint: no cross-page physics). What's reused from the old
// code is the *substance* that made it look good: real primitive
// geometry, a physically-lit material, ambient+key+rim lighting, and
// cannon-es driving the tumble. Physics is purely cosmetic here, same
// principle as everything else about a roll's appearance (design doc
// §3.1, §4): it never determines the result, only how the die visually
// gets there.
//
// Unlike the old tray (one die, one canvas, ever), a chat log can
// accumulate dozens of dice over a session (up to six 4d6 ability-score
// bubbles alone is 24 dice, and every combat roll ever made stays in
// scrollback too). Browsers cap concurrent WebGL contexts (commonly
// ~16 in Chrome), so this module never gives a die a *permanent* live
// context: only a die that is actively mid-tumble holds one, borrowed
// from a small fixed-size pool (see rendererPool below) and returned the
// instant it settles. An idle (ghost, ability-score-not-yet-rolled) or
// already-revealed die is a plain `<img>` showing a captured frame — no
// GPU resources held at all. See buildDieVisual/tumbleAndSettle.
//
// The revealed number itself is deliberately NOT baked into the mesh —
// it's the existing `.roll-die-face` DOM span, unchanged, layered over
// the canvas/image. That sidesteps the old tray's real risk (orienting
// the one labeled face toward the camera on settle, a fiddly per-shape
// problem five different geometries would each get to solve separately)
// entirely: the die can settle in any pleasant resting pose and the
// number stays perfectly legible regardless. See settleDie in app.js.

import * as THREE from "./vendor/three.module.min.js";
import * as CANNON from "./vendor/cannon-es.js";

// ES modules are always strict mode — no "use strict" directive needed.

// --- Tunables ---

const DIE_RADIUS = 1;
// Matches .roll-die's on-screen size in style.css. Keep these in sync —
// this file renders at a fixed logical size rather than measuring its
// container (unlike the old viewport-filling tray), so there is no
// ResizeObserver wiring to keep it correct if that CSS value changes.
const CANVAS_LOGICAL_SIZE = 56;
const MAX_PIXEL_RATIO = 2;

// MAX_LIVE_RENDERERS bounds how many WebGLRenderer/canvas pairs this
// module will ever construct, however many dice have ever existed in the
// DOM — well under the ~16-context browser cap, leaving headroom for
// anything else on the page that might use WebGL. Only a die that is
// currently mid-tumble (or, very briefly, mid idle-snapshot) holds one of
// these; everything else is a static <img>.
const MAX_LIVE_RENDERERS = 10;

// IDLE_SNAPSHOT_RETRY_MS/IDLE_SNAPSHOT_RETRY_ATTEMPTS and
// TUMBLE_ACQUIRE_RETRY_MS/TUMBLE_ACQUIRE_RETRY_ATTEMPTS bound how hard
// this module tries to get a pooled renderer before giving up and
// degrading (a flat placeholder in place of a snapshot; no tumble
// animation, just the reveal timer in app.js firing on schedule
// regardless). A burst that saturates the pool — e.g. a single large
// multi-die damage roll all revealing within the same animation frame —
// is real and plausible, so it's worth a few retries rather than
// degrading on the very first miss; but retries must stay well short of
// TUMBLE_DURATION_MS or a late-acquired tumble would barely play before
// its own freeze.
const IDLE_SNAPSHOT_RETRY_MS = 120;
const IDLE_SNAPSHOT_RETRY_ATTEMPTS = 6;
const TUMBLE_ACQUIRE_RETRY_MS = 60;
const TUMBLE_ACQUIRE_RETRY_ATTEMPTS = 5;

// TUMBLE_DURATION_MS is the total on-screen life of the tumble animation
// before this module freezes the die to its final frame. app.js imports
// this same constant and uses it as settleDie's own independent DOM
// number-reveal delay, so the number appears right as the die visually
// comes to rest — the two timers are driven by one shared value rather
// than two hand-kept-in-sync magic numbers.
export const TUMBLE_DURATION_MS = 550;

// Local contained-tumble "box" the die bounces around inside — sized in
// world units to stay within the orthographic camera's own view, so the
// tumble never appears to leave its canvas (hard constraint: no
// cross-page physics). Tuned by feel against CANVAS_LOGICAL_SIZE below.
const ARENA_HALF = 1.55;
const ARENA_DEPTH_HALF = 1.0;
const GRAVITY_Y = -20;
const REST_LINEAR = 0.35;
const REST_ANGULAR = 0.8;
const MAX_SPEED = 9;
const MAX_SPIN = 16;

// --- Skin ---
//
// No user-facing skin picker (none was asked for; the old tray's picker
// lived on a join screen that no longer exists). One baked-in default,
// informed by the old tray's "Ivory" skin (dice-skins.js) — a warm,
// light base that already sits naturally in this app's parchment/brass
// theme (style.css's --parchment is #efe1c4, a near neighbor).
const SKIN = {
  color: 0xe2d8c3,
  roughness: 0.45,
  metalness: 0.08,
};

// --- Shared geometry/material cache ---
//
// Built once per shape and reused by every die instance of that size —
// N on-screen d20s share one GPU geometry/material upload, not N. A
// THREE.Mesh referencing a shared geometry/material is cheap and safe to
// instantiate per die; only per-die transforms differ.

const geometryCache = new Map();
let sharedMaterial = null;

function getSharedMaterial() {
  if (!sharedMaterial) {
    sharedMaterial = new THREE.MeshStandardMaterial({
      color: SKIN.color,
      flatShading: true,
      roughness: SKIN.roughness,
      metalness: SKIN.metalness,
    });
  }
  return sharedMaterial;
}

// buildD10Geometry builds the one shape three.js has no primitive for: a
// pentagonal trapezohedron (10 kite faces, 2 apex vertices, 10 equatorial
// vertices in a zigzag ring). Two triangle fans — one from each apex,
// wound around the same 10-edge ring — meet edge-to-edge in pairs to
// form each kite. Numbered 0-9 on a real d10, not 1-10 (see settleDie's
// display mapping in app.js, unrelated to this geometry).
function buildD10Geometry() {
  const vertices = [0, 0, 1, 0, 0, -1];
  for (let i = 0; i < 10; i++) {
    const b = (i * Math.PI * 2) / 10;
    vertices.push(Math.cos(b), Math.sin(b), 0.105 * (i % 2 ? 1 : -1));
  }
  const indices = [];
  for (let i = 0; i < 10; i++) {
    const a = 2 + i;
    const c = 2 + ((i + 1) % 10);
    indices.push(0, a, c); // top-apex fan triangle
    indices.push(1, c, a); // bottom-apex fan triangle, sharing edge a-c
  }
  const geometry = new THREE.PolyhedronGeometry(vertices, indices, DIE_RADIUS, 0);
  return fixOutwardWinding(geometry);
}

// fixOutwardWinding guards against a hand-transcribed vertex/face table
// (buildD10Geometry above is the only shape built that way — the other
// four sizes are three.js's own tested primitives) being wound the wrong
// way, which would render an invisible or wrongly-lit die with no
// obvious error. Every die shape here is convex and centered on the
// origin, so a correctly-wound triangle's face normal always points away
// from the origin; sample a handful of faces and, if most disagree, flip
// every triangle's winding by swapping each triangle's last two
// vertices' position data in place.
function fixOutwardWinding(geometry) {
  const pos = geometry.attributes.position;
  const triCount = pos.count / 3;
  const sampleCount = Math.min(6, triCount);
  let outward = 0;
  const a = new THREE.Vector3();
  const b = new THREE.Vector3();
  const c = new THREE.Vector3();
  for (let i = 0; i < sampleCount; i++) {
    const base = i * 3;
    a.fromBufferAttribute(pos, base);
    b.fromBufferAttribute(pos, base + 1);
    c.fromBufferAttribute(pos, base + 2);
    const normal = new THREE.Vector3().subVectors(b, a).cross(new THREE.Vector3().subVectors(c, a));
    const centroid = new THREE.Vector3().add(a).add(b).add(c);
    if (normal.dot(centroid) > 0) outward++;
  }
  if (outward < sampleCount / 2) {
    const arr = pos.array;
    const size = pos.itemSize;
    for (let t = 0; t < triCount; t++) {
      const i1 = (t * 3 + 1) * size;
      const i2 = (t * 3 + 2) * size;
      for (let k = 0; k < size; k++) {
        const tmp = arr[i1 + k];
        arr[i1 + k] = arr[i2 + k];
        arr[i2 + k] = tmp;
      }
    }
    pos.needsUpdate = true;
  }
  return geometry;
}

// getGeometry returns (building + caching on first use) the shared
// geometry for a die size. An unrecognized size falls back to the d20
// icosahedron rather than failing to render, matching the old SVG code's
// "never fail to render an unknown size" spirit (buildFallbackShape).
function getGeometry(sides) {
  let geometry = geometryCache.get(sides);
  if (geometry) return geometry;
  switch (sides) {
    case 4:
      geometry = new THREE.TetrahedronGeometry(DIE_RADIUS, 0);
      break;
    case 6:
      geometry = new THREE.BoxGeometry(DIE_RADIUS * 1.3, DIE_RADIUS * 1.3, DIE_RADIUS * 1.3);
      break;
    case 8:
      geometry = new THREE.OctahedronGeometry(DIE_RADIUS, 0);
      break;
    case 10:
      geometry = buildD10Geometry();
      break;
    case 20:
      geometry = new THREE.IcosahedronGeometry(DIE_RADIUS, 0);
      break;
    default:
      geometry = new THREE.IcosahedronGeometry(DIE_RADIUS, 0);
  }
  geometry.computeVertexNormals();
  geometryCache.set(sides, geometry);
  return geometry;
}

// --- Renderer pool ---
//
// A fixed-size pool of {renderer, canvas} pairs, lazily created up to
// MAX_LIVE_RENDERERS. Both the momentary idle-snapshot render and the
// longer-lived tumble animation borrow from this same pool and return
// their entry the instant they're done with it — a tumble holds one for
// TUMBLE_DURATION_MS, an idle snapshot for a single synchronous frame.
// preserveDrawingBuffer is required for toDataURL to reliably capture a
// WebGL canvas's contents; these are small, simple scenes, so the extra
// cost is negligible.
const rendererPool = [];
let createdRendererCount = 0;

function createPoolEntry() {
  const canvas = document.createElement("canvas");
  const renderer = new THREE.WebGLRenderer({
    canvas,
    antialias: true,
    alpha: true,
    preserveDrawingBuffer: true,
  });
  renderer.setClearColor(0x000000, 0);
  return { renderer, canvas };
}

// acquireRenderer returns a pool entry, or null if the pool is at its cap
// and every entry is currently in use — callers must handle null by
// degrading gracefully (see tumbleAndSettle/renderIdleSnapshot), never by
// creating an unpooled renderer of their own.
function acquireRenderer() {
  if (rendererPool.length) return rendererPool.pop();
  if (createdRendererCount < MAX_LIVE_RENDERERS) {
    createdRendererCount++;
    return createPoolEntry();
  }
  return null;
}

function releaseRenderer(entry) {
  if (!entry) return;
  entry.renderer.setSize(1, 1);
  if (entry.canvas.parentNode) entry.canvas.parentNode.removeChild(entry.canvas);
  rendererPool.push(entry);
}

// --- Scene construction ---

function addLights(scene) {
  scene.add(new THREE.AmbientLight(0xffffff, 0.6));
  const key = new THREE.DirectionalLight(0xfff4e0, 1.15);
  key.position.set(2.5, 4, 4);
  scene.add(key);
  const rim = new THREE.DirectionalLight(0xaac8ff, 0.4);
  rim.position.set(-2, -1, -3);
  scene.add(rim);
}

// buildCamera returns a small, slightly elevated orthographic camera — a
// 3/4 view shows several facets at once (a dead-on front view of, say, a
// d6 would just look like a flat square), and orthographic projection
// keeps the die's apparent size constant as it tumbles through the box,
// same reasoning the old tray used for its viewport mapping.
function buildCamera() {
  const camera = new THREE.OrthographicCamera(-2.1, 2.1, 2.1, -2.1, 0.1, 20);
  camera.position.set(1.15, 1.65, 3.0);
  camera.lookAt(0, 0, 0);
  return camera;
}

function buildScene(sides) {
  const scene = new THREE.Scene();
  addLights(scene);
  const mesh = new THREE.Mesh(getGeometry(sides), getSharedMaterial());
  scene.add(mesh);
  return { scene, mesh };
}

// --- Idle snapshot (ghost / not-yet-rolled / already-revealed dice) ---
//
// A die that isn't actively tumbling costs nothing but a captured PNG
// frame — no persistent canvas, no GL context. Each die gets its own
// small random resting tilt (seeded once per instance, see
// DieController below) purely so a row of same-sided dice doesn't look
// like stamped copies of each other.
function renderSnapshot(sides, quaternion) {
  const entry = acquireRenderer();
  if (!entry) return null; // pool exhausted — caller falls back to a flat placeholder.
  const size = Math.round(CANVAS_LOGICAL_SIZE * Math.min(window.devicePixelRatio || 1, MAX_PIXEL_RATIO));
  entry.renderer.setSize(size, size, false);
  const { scene, mesh } = buildScene(sides);
  mesh.quaternion.copy(quaternion);
  const camera = buildCamera();
  entry.renderer.render(scene, camera);
  const dataUrl = entry.canvas.toDataURL("image/png");
  releaseRenderer(entry);
  return dataUrl;
}

function randomRestQuaternion() {
  return new THREE.Quaternion().setFromEuler(
    new THREE.Euler(Math.random() * Math.PI, Math.random() * Math.PI, Math.random() * Math.PI)
  );
}

// --- Contained tumble physics ---
//
// A small, fixed-size cannon-es world scaled to this die's own canvas —
// not the message log, not the page. Six static walls form a closed box
// (floor/ceiling/left/right/front/back); the die (a sphere collider, same
// approximation the old tray used) is tossed from a corner with
// randomized velocity/spin and gravity pulls it down. This is cosmetic
// motion only, run for a fixed TUMBLE_DURATION_MS regardless of whether
// the die visually comes to rest before then (it usually does, well
// within the small box) — never a value the die's face outcome depends
// on, since the number is a DOM overlay decided entirely by the server
// result (see app.js's settleDie).
function buildContainedArena() {
  const world = new CANNON.World({ gravity: new CANNON.Vec3(0, GRAVITY_Y, 0) });
  world.allowSleep = false;

  const dieMaterial = new CANNON.Material("die");
  const wallMaterial = new CANNON.Material("wall");
  world.addContactMaterial(
    new CANNON.ContactMaterial(dieMaterial, wallMaterial, { friction: 0.35, restitution: 0.45 })
  );

  const makeWall = (axis, angle, position) => {
    const body = new CANNON.Body({ mass: 0, material: wallMaterial, shape: new CANNON.Plane() });
    body.quaternion.setFromAxisAngle(axis, angle);
    body.position.set(position[0], position[1], position[2]);
    world.addBody(body);
    return body;
  };
  makeWall(new CANNON.Vec3(1, 0, 0), -Math.PI / 2, [0, -ARENA_HALF, 0]); // floor
  makeWall(new CANNON.Vec3(1, 0, 0), Math.PI / 2, [0, ARENA_HALF, 0]); // ceiling
  makeWall(new CANNON.Vec3(0, 1, 0), Math.PI / 2, [-ARENA_HALF, 0, 0]); // left
  makeWall(new CANNON.Vec3(0, 1, 0), -Math.PI / 2, [ARENA_HALF, 0, 0]); // right
  makeWall(new CANNON.Vec3(0, 1, 0), 0, [0, 0, -ARENA_DEPTH_HALF]); // back
  makeWall(new CANNON.Vec3(0, 1, 0), Math.PI, [0, 0, ARENA_DEPTH_HALF]); // front

  const dieBody = new CANNON.Body({
    mass: 1,
    material: dieMaterial,
    shape: new CANNON.Sphere(DIE_RADIUS * 0.78),
    linearDamping: 0.2,
    angularDamping: 0.25,
  });
  const corner = Math.random() < 0.5 ? -1 : 1;
  dieBody.position.set(corner * (ARENA_HALF - 0.4), ARENA_HALF - 0.4, 0);
  dieBody.velocity.set(-corner * (4 + Math.random() * 3), -0.5 - Math.random(), (Math.random() - 0.5) * 2);
  dieBody.angularVelocity.set(
    (Math.random() - 0.5) * 18,
    (Math.random() - 0.5) * 18,
    (Math.random() - 0.5) * 18
  );
  world.addBody(dieBody);

  return { world, dieBody };
}

function clampMotion(body) {
  const v = body.velocity;
  const speed = Math.hypot(v.x, v.y, v.z);
  if (speed > MAX_SPEED) {
    const k = MAX_SPEED / speed;
    v.x *= k;
    v.y *= k;
    v.z *= k;
  }
  const w = body.angularVelocity;
  const spin = Math.hypot(w.x, w.y, w.z);
  if (spin > MAX_SPIN) {
    const k = MAX_SPIN / spin;
    w.x *= k;
    w.y *= k;
    w.z *= k;
  }
}

function atRest(body) {
  return body.velocity.length() < REST_LINEAR && body.angularVelocity.length() < REST_ANGULAR;
}

// --- Per-die controller ---
//
// One DieController per die element, tracked in a WeakMap keyed by the
// dieEl (see buildDieVisual) so settleDie's later call into
// tumbleAndSettle finds the right instance without app.js needing to
// hold onto anything beyond the DOM node it already has.
class DieController {
  constructor(sides) {
    this.sides = sides;
    this.restQuaternion = randomRestQuaternion();
    this.node = this.renderIdleNode();
  }

  // renderIdleNode produces the resting-pose <img> node synchronously —
  // construction of a die element never blocks or waits on anything. If
  // the pool is fully checked out at that exact instant (a burst of many
  // dice/tumbles at once — see MAX_LIVE_RENDERERS), it returns a plain
  // themed placeholder <div> immediately and schedules a few short
  // retries (retryIdleSnapshot) to swap in the real snapshot as soon as a
  // renderer frees up, so the placeholder is a brief transition rather
  // than a permanent downgrade for that die.
  renderIdleNode() {
    const dataUrl = renderSnapshot(this.sides, this.restQuaternion);
    if (dataUrl) return this.imageNode(dataUrl);
    const placeholder = document.createElement("div");
    placeholder.className = "roll-die-visual roll-die-visual-placeholder";
    this.retryIdleSnapshot(placeholder, 0);
    return placeholder;
  }

  retryIdleSnapshot(placeholder, attempt) {
    if (attempt >= IDLE_SNAPSHOT_RETRY_ATTEMPTS) return; // give up quietly — a themed placeholder is a fine resting state.
    window.setTimeout(() => {
      if (!placeholder.isConnected) return; // die was removed (e.g. history page scrolled away) before a retry landed.
      const dataUrl = renderSnapshot(this.sides, this.restQuaternion);
      if (dataUrl) {
        placeholder.replaceWith(this.imageNode(dataUrl));
        return;
      }
      this.retryIdleSnapshot(placeholder, attempt + 1);
    }, IDLE_SNAPSHOT_RETRY_MS);
  }

  imageNode(dataUrl) {
    const img = document.createElement("img");
    img.className = "roll-die-visual";
    img.alt = "";
    img.src = dataUrl;
    return img;
  }

  // tumble runs the contained physics animation for TUMBLE_DURATION_MS,
  // swapping this die's DOM node from its resting <img> to a live canvas
  // for the duration and back to a fresh (now post-tumble) <img>
  // afterward. If the renderer pool is momentarily exhausted (a burst of
  // many simultaneous reveals — see MAX_LIVE_RENDERERS), it retries a
  // handful of times, short enough to still stay inside
  // TUMBLE_DURATION_MS; if the pool is still exhausted after that it
  // gives up on the animation for this die and leaves the resting
  // snapshot in place — the die's number still gets revealed on schedule
  // by app.js's settleDie regardless, since that never depends on this
  // animation completing.
  tumble(containerEl, attempt = 0) {
    const entry = acquireRenderer();
    if (!entry) {
      if (attempt < TUMBLE_ACQUIRE_RETRY_ATTEMPTS) {
        window.setTimeout(() => this.tumble(containerEl, attempt + 1), TUMBLE_ACQUIRE_RETRY_MS);
      }
      return;
    }

    const size = Math.round(CANVAS_LOGICAL_SIZE * Math.min(window.devicePixelRatio || 1, MAX_PIXEL_RATIO));
    entry.renderer.setSize(size, size, false);
    entry.canvas.className = "roll-die-visual";
    if (containerEl.firstChild) containerEl.replaceChild(entry.canvas, containerEl.firstChild);
    else containerEl.appendChild(entry.canvas);

    const { scene, mesh } = buildScene(this.sides);
    const camera = buildCamera();
    const { world, dieBody } = buildContainedArena();

    const start = performance.now();
    let lastTime = null;

    const step = (time) => {
      const dt = lastTime === null ? 1 / 60 : Math.min((time - lastTime) / 1000, 1 / 20);
      lastTime = time;
      const elapsed = time - start;

      if (elapsed < TUMBLE_DURATION_MS && !(elapsed > 180 && atRest(dieBody))) {
        world.step(1 / 60, dt, 5);
        clampMotion(dieBody);
        mesh.position.set(dieBody.position.x, dieBody.position.y, dieBody.position.z);
        mesh.quaternion.set(dieBody.quaternion.x, dieBody.quaternion.y, dieBody.quaternion.z, dieBody.quaternion.w);
      }
      entry.renderer.render(scene, camera);

      if (elapsed < TUMBLE_DURATION_MS) {
        requestAnimationFrame(step);
        return;
      }

      // Freeze: capture the final frame and swap back to a static <img>
      // BEFORE releasing the renderer — releaseRenderer detaches
      // entry.canvas from whatever parent it's in (so the pool's next
      // borrower doesn't inherit this die's old canvas as a stray extra
      // child), so the swap has to happen first while containerEl's
      // first child is still definitely entry.canvas itself (checked by
      // identity, not just tag name, since a retried/overlapping tumble
      // could otherwise mismatch which canvas is actually there).
      this.restQuaternion = mesh.quaternion.clone();
      const dataUrl = entry.canvas.toDataURL("image/png");
      if (containerEl.firstChild === entry.canvas) {
        containerEl.replaceChild(this.imageNode(dataUrl), entry.canvas);
      }
      releaseRenderer(entry);
    };
    requestAnimationFrame(step);
  }
}

const controllers = new WeakMap();

// --- Public API consumed by app.js ---

// buildDieVisual mounts a new DieController for dieEl and returns the
// node to append inside it (a static resting-pose image, or a themed
// placeholder — see DieController.renderIdleNode). Call once per die
// element, before appending it to the DOM.
export function buildDieVisual(dieEl, sides) {
  const controller = new DieController(sides);
  controllers.set(dieEl, controller);
  return controller.node;
}

// tumbleAndSettle starts dieEl's contained tumble-and-settle animation.
// Safe to call even if buildDieVisual was never called for this element
// (a die from before this module loaded, or an unrecognized element) —
// it just no-ops rather than throwing, since the animation is cosmetic
// only and app.js's own reveal timer never depends on it.
export function tumbleAndSettle(dieEl) {
  const controller = controllers.get(dieEl);
  if (!controller) return;
  controller.tumble(dieEl);
}
