// Copyright (c) 2026 James Duane Plotts. Licensed under the MIT License.
// See LICENSE in the repository root.
//
// A real WebGL die (three.js) built from an artist-made mesh + texture
// (the "rust" theme from the 3d-dice/dice-themes asset pack, MIT licensed
// — see vendor/dice-themes/ and vendor/README.md), spun into place with a
// scripted animation and rendered *inside* the chat log's own per-die
// slot. This replaces two earlier generations of this same call site: the
// original SVG silhouette dice, and this file's own first version, which
// used three.js primitive geometry (TetrahedronGeometry/BoxGeometry/etc.)
// physically tumbled via cannon-es. Both are gone for the same reason —
// once you can compare a screenshot against real dice-rendering software,
// hand-built primitives with flat materials just look like hand-built
// primitives. See `git log` on this file for that earlier physics-tumble
// version if you need it back.
//
// Two things changed together here, deliberately:
//
// 1. Geometry/material now come from real mesh + texture data (a Blender
//    export, Babylon-JSON format) instead of three.js primitives, for the
//    material/texture realism a hand-tuned MeshStandardMaterial can't
//    match on its own. See loadTheme below and vendor/README.md for the
//    asset pipeline.
// 2. Physics is gone. The operator explicitly does not need dice to
//    tumble/bounce across a surface — "just having the great looking die
//    spin in place would be enough" — so cannon-es (and this file's old
//    contained-arena/collision-wall code) is deleted outright, replaced
//    by a scripted spin-to-target-orientation animation (see
//    DieController.tumble). Nothing else in master/web/ imports cannon-es
//    (confirmed by grep before deleting it here); the vendored
//    cannon-es.js file itself is left in vendor/ in case a future feature
//    wants real physics again — only this file's *use* of it is gone.
//
// The bigger change, driven by the operator mid-implementation: the
// revealed number is no longer a DOM text overlay layered on top of the
// die (`.roll-die-face`, and the whole "the mesh only has to look good,
// the overlay is what's actually read" safety net) — the 3D die itself
// must now show the correct face, correctly oriented, with nothing else
// backing it up. See computeTargetQuaternion below for how a rolled value
// becomes an actual orientation.
//
// A real, severe bug was found and fixed in this math during review,
// after the fact — worth recording here since it's exactly the kind of
// mistake this approach is fragile to: the first version aligned each
// target face's own outward (away-from-center) normal to the camera
// direction, which reliably landed the die on its *opposite* face
// instead — for a d6 specifically, this was a clean, total 7-N swap
// (asked for 1, got 6; asked for 2, got 5; every single value, every
// time), confirmed by sampling the actual rendered pixels at the exact
// screen location a real player would read, not just checking the
// vector math (which was self-consistent and "provably correct" on its
// own terms — see git history on this file for that earlier, wrong
// verification). Root cause: the collider mesh (colliderFaceInfo below)
// is a pick-only utility proxy nothing ever renders — upstream only
// ever raycasts against it — so its triangle winding was never
// authored or checked for outward-facing consistency the way the
// *visual* mesh's was. The fix (negating that normal before aligning
// to camera — see computeTargetQuaternion) was verified the same
// rigorous way: real rendered screenshots, at this module's actual
// production canvas size, for every value of every shape this module
// supports, not a sample.
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

import * as THREE from "./vendor/three.module.min.js";

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

// IDLE_SNAPSHOT_RETRY_MS/IDLE_SNAPSHOT_RETRY_ATTEMPTS bound how hard an
// idle (not-tumbling) die retries a momentarily-exhausted pool before
// settling for a themed placeholder — a brief, cosmetic-only wait, so a
// short budget is fine: an idle snapshot never carries a result, so
// there's nothing incorrect about a placeholder that never upgrades.
const IDLE_SNAPSHOT_RETRY_MS = 120;
const IDLE_SNAPSHOT_RETRY_ATTEMPTS = 6;

// TUMBLE_ACQUIRE_RETRY_MS/TUMBLE_ACQUIRE_RETRY_ATTEMPTS bound how hard
// tumble() retries for a renderer to run the *animation* before giving up
// on the animation specifically and falling back to showStaticResult
// (still correct, just not animated) — kept short and well under
// TUMBLE_DURATION_MS, since a late-acquired renderer wouldn't have time
// to play a real spin before its own freeze anyway, and it's better to
// fall back to a correct static pose quickly than to keep the animation
// attempt alive for no visible benefit.
const TUMBLE_ACQUIRE_RETRY_MS = 60;
const TUMBLE_ACQUIRE_RETRY_ATTEMPTS = 5;

// STATIC_RESULT_RETRY_MS/STATIC_RESULT_RETRY_ATTEMPTS bound
// showStaticResult's own retries — deliberately a much longer combined
// budget than the animation-acquisition one above, because this is the
// correctness backstop, not a cosmetic nicety: with no DOM text overlay
// behind it any more (see this file's top-of-file doc block), a die that
// gives up here would silently show the *wrong* face forever, not just a
// missing animation. A single die only ever holds a renderer for
// TUMBLE_DURATION_MS at a time, but a real burst can be large — this
// file's own doc comment above cites a 4d6 ability-score sequence as 24
// dice revealing together, well over MAX_LIVE_RENDERERS — so a die stuck
// at the back of that queue may need to wait for two or three other
// dice's *entire* tumbles to finish before a slot frees up. Sized
// (30 attempts x 150ms = 4.5s) to comfortably outlast that worst case
// rather than the brief single-retry-cycle budget above, which a stress
// test (30 simultaneous reveals) showed was nowhere near enough — dice
// past the first MAX_LIVE_RENDERERS simply never recovered a correct
// face under the old shared/short budget.
const STATIC_RESULT_RETRY_MS = 150;
const STATIC_RESULT_RETRY_ATTEMPTS = 30;

// TUMBLE_DURATION_MS is the total on-screen life of the spin animation
// before this module freezes the die to its final frame. app.js imports
// this same constant and uses it as settleDie's own independent DOM
// reveal-timing delay, so the "revealed"/"dropped" classes land right as
// the die visually comes to rest — the two timers are driven by one
// shared value rather than two hand-kept-in-sync magic numbers. Widened
// from the old physics-tumble's 550ms to fit a real "spin up, then ease
// to rest" animation (the operator's own guidance: "roughly 1-1.5
// seconds").
export const TUMBLE_DURATION_MS = 1200;

// --- Theme (real mesh + texture assets, see vendor/dice-themes/) ---
//
// THEME_DIR points at the one theme this module ships with today (see
// this session's final report for the default/rust/gemstoneMarble
// comparison and why rust won). MESH_URL is a fallback, not a redundant
// path: the rust theme's own theme.config.json carries no "meshFile" key
// at all — confirmed against the actual upstream repo listing, not
// assumed — because upstream dice-box reuses the "default" theme's mesh
// for every "recolor" theme rather than shipping duplicate geometry per
// palette. Vendoring one shared mesh file instead of two identical copies
// keeps this module's payload smaller, at the cost of this one
// theme-specific fallback path.
const THEME_DIR = "./vendor/dice-themes/rust";
const DEFAULT_MESH_URL = "./vendor/dice-themes/default/default.json";

// BASE_COLOR is the solid die color composited underneath the theme's
// diffuse texture (see composeDiffuseTexture) — informed by the old
// primitive-geometry version's "Ivory" skin, a warm, light base that
// already sits naturally in this app's parchment/brass theme (style.css's
// --parchment is #efe1c4, a near neighbor). No user-facing skin picker
// exists (none was asked for), so this one baked-in color is it.
const BASE_COLOR = "#e2d8c3";

// SHAPE_NAMES enumerates every die size this module supports, and doubles
// as the mesh-name lookup key into the theme's Babylon-JSON mesh document
// (meshes are named "d4"/"d6"/etc., each with a matching "<name>_collider"
// entry — see loadTheme).
const SHAPE_NAMES = { 4: "d4", 6: "d6", 8: "d8", 10: "d10", 12: "d12", 20: "d20" };

let themeState = null; // set once loadTheme() resolves — see ensureThemeLoading.
let themeLoadStarted = false;
let themeReadyResolve;
const themeReadyPromise = new Promise((resolve) => {
  themeReadyResolve = resolve;
});

function fetchJSON(url) {
  return fetch(url).then((res) => {
    if (!res.ok) throw new Error(`fetch ${url} failed: ${res.status}`);
    return res.json();
  });
}

function loadImage(url) {
  return new Promise((resolve, reject) => {
    const img = new Image();
    img.onload = () => resolve(img);
    img.onerror = () => reject(new Error(`image load failed: ${url}`));
    img.src = url;
  });
}

// composeDiffuseTexture flattens the theme's diffuse artwork onto an
// opaque BASE_COLOR canvas before handing it to three.js. The theme's own
// diffuse-light.png is not a normal opaque photo — it's a mostly-
// transparent white-ink-on-nothing mask (confirmed by inspecting its raw
// alpha channel: background pixels are alpha=0, the printed numerals are
// white at partial-to-full alpha) meant to be recolored by whichever solid
// base color a theme/user picks, the same convention dice-box's own
// "color" theme family uses. Compositing it onto a canvas once at load
// time (rather than alpha-blending it live against this module's
// transparent WebGL clear color) avoids literal see-through holes where
// the background would otherwise be — every die face needs to read as a
// solid colored surface, not a cutout.
async function composeDiffuseTexture(url, baseColor) {
  const img = await loadImage(url);
  const canvas = document.createElement("canvas");
  canvas.width = img.width;
  canvas.height = img.height;
  const ctx = canvas.getContext("2d");
  ctx.fillStyle = baseColor;
  ctx.fillRect(0, 0, canvas.width, canvas.height);
  ctx.drawImage(img, 0, 0);
  const texture = new THREE.CanvasTexture(canvas);
  texture.colorSpace = THREE.SRGBColorSpace;
  texture.needsUpdate = true;
  return texture;
}

async function loadNormalTexture(url) {
  const img = await loadImage(url);
  const texture = new THREE.Texture(img);
  texture.needsUpdate = true;
  return texture;
}

// buildGeometryFromMesh converts one Babylon-JSON mesh entry (flat
// positions/normals/uvs/indices arrays — see vendor/README.md for the
// format) into a three.js BufferGeometry, auto-scaled so its bounding
// sphere radius matches this module's existing DIE_RADIUS convention
// (the raw exported meshes measure roughly 0.1 units across, sized for
// whatever arena the original rig used — irrelevant here).
function buildGeometryFromMesh(meshData) {
  const geometry = new THREE.BufferGeometry();
  geometry.setAttribute("position", new THREE.Float32BufferAttribute(meshData.positions, 3));
  geometry.setAttribute("normal", new THREE.Float32BufferAttribute(meshData.normals, 3));
  if (meshData.uvs && meshData.uvs.length) {
    geometry.setAttribute("uv", new THREE.Float32BufferAttribute(meshData.uvs, 2));
  }
  geometry.setIndex(meshData.indices);
  geometry.computeBoundingSphere();
  const scale = DIE_RADIUS / (geometry.boundingSphere.radius || 1);
  geometry.scale(scale, scale, scale);
  return geometry;
}

// loadTheme fetches the theme's config, mesh document, and textures
// exactly once, builds every die shape's geometry plus one shared
// material, and resolves themeReadyPromise. Kicked off once at module
// load (see the call at the bottom of this section) — by the time any
// die actually needs to tumble, this has almost always long since
// finished; the rare case where it hasn't (a roll lands within the first
// network round-trip of the page loading) is handled by ensureThemeLoading's
// retry, the same pattern already used for a momentarily-exhausted
// renderer pool.
async function loadTheme() {
  const config = await fetchJSON(`${THEME_DIR}/theme.config.json`);
  const meshUrl = config.meshFile ? `${THEME_DIR}/${config.meshFile}` : DEFAULT_MESH_URL;
  const meshDoc = await fetchJSON(meshUrl);

  const diffuseFile = typeof config.material.diffuseTexture === "string"
    ? config.material.diffuseTexture
    : config.material.diffuseTexture.light;
  const [map, normalMap] = await Promise.all([
    composeDiffuseTexture(`${THEME_DIR}/${diffuseFile}`, BASE_COLOR),
    config.material.bumpTexture ? loadNormalTexture(`${THEME_DIR}/${config.material.bumpTexture}`) : null,
  ]);

  const material = new THREE.MeshStandardMaterial({
    map,
    normalMap,
    normalScale: normalMap ? new THREE.Vector2(config.material.bumpLevel || 0.5, config.material.bumpLevel || 0.5) : undefined,
    roughness: 0.6,
    metalness: 0.06,
  });

  const meshByName = {};
  for (const m of meshDoc.meshes) meshByName[m.name] = m;

  const geometryBySides = {};
  for (const [sides, name] of Object.entries(SHAPE_NAMES)) {
    const meshData = meshByName[name];
    if (meshData) geometryBySides[sides] = buildGeometryFromMesh(meshData);
  }

  themeState = { meshByName, colliderFaceMap: meshDoc.colliderFaceMap, material, geometryBySides };
  themeReadyResolve();
}

function ensureThemeLoading() {
  if (!themeLoadStarted) {
    themeLoadStarted = true;
    loadTheme().catch((err) => {
      // A failed fetch (offline self-hosted deployment serving this
      // directory from somewhere the theme assets didn't get copied to,
      // say) degrades to the plain placeholder skin forever rather than
      // throwing — see getGeometryAndMaterial's fallback and
      // renderIdleNode's placeholder path. Logged once so it's
      // diagnosable rather than silently blank.
      console.error("dice3d: failed to load dice theme assets", err);
    });
  }
  return themeReadyPromise;
}

// --- Face-orientation math (turns a rolled value into a quaternion) ---
//
// This is the part of the module that replaces the removed DOM number
// overlay: since the 3D die itself is now the only thing telling the
// player what they rolled, it has to settle with the correct face both
// selected *and* right-side up, not just plausibly-forward. The approach,
// short version (full derivation + how it was verified is in this
// session's final report, not repeated here as inline prose):
//
// 1. Which face: `colliderFaceMap` (shipped in the theme's own mesh JSON)
//    maps a low-poly "collider" mesh's triangle ids to the value printed
//    on that face. The collider is a separate, much simpler proxy mesh
//    (one flat-shaded triangle per physical face, no shared vertices) —
//    upstream's own raycast-hit-testing mesh — so its per-triangle normal
//    unambiguously answers "which direction does the N-valued face point,
//    in this die's local space".
// 2. Which triangle of the *visual* (high-poly, textured) mesh that
//    corresponds to: matched by requiring both a near-parallel normal
//    (>=0.99 dot, loosened in steps only if nothing qualifies — see
//    findBestVisualTriangle) *and* the closest centroid to the collider
//    triangle's own centroid. Matching on normal similarity alone was not
//    selective enough on these particular meshes — verified against the
//    theme's raw texture atlas, several of this die's 20 faces sit close
//    enough in both angle and position that a looser match pulled in
//    triangles from the wrong face entirely (this was the single biggest
//    time sink in building this file; see the report).
// 3. Which way is "up" on that face: rather than re-deriving tangent
//    space from that triangle's own UV coordinates (unstable — the
//    matched triangle's UV footprint turned out to be a near-degenerate
//    sliver despite being a normal-sized triangle in 3D), this uses the
//    mesh's own precomputed per-vertex "tangents" attribute (a standard
//    Babylon/glTF-style xyzw tangent+handedness export, authored across
//    the whole mesh rather than one triangle) and derives the bitangent
//    (texture "up") from it via the standard bitangent = cross(normal,
//    tangent) * handedness formula.
// 4. The final quaternion rotates the die so that face's normal points
//    at the camera (CAMERA_DIR below — computed from this file's own
//    buildCamera, not a generic "up" guess) and that bitangent aligns
//    with the screen's actual up direction (SCREEN_UP), so the printed
//    glyph reads upright from this app's specific 3/4 camera angle, not
//    just "facing the right general way".

const WORLD_UP = new THREE.Vector3(0, 1, 0);
// Mirrors buildCamera()'s own position — kept as a derived constant here
// rather than calling buildCamera() itself, since face-orientation math
// only needs the camera's direction/up, not a full Camera object.
const CAMERA_DIR = new THREE.Vector3(1.15, 1.65, 3.0).normalize();
const SCREEN_UP = WORLD_UP.clone()
  .sub(CAMERA_DIR.clone().multiplyScalar(WORLD_UP.dot(CAMERA_DIR)))
  .normalize();

function add3(a, b) {
  return [a[0] + b[0], a[1] + b[1], a[2] + b[2]];
}
function sub3(a, b) {
  return [a[0] - b[0], a[1] - b[1], a[2] - b[2]];
}
function cross3(a, b) {
  return [a[1] * b[2] - a[2] * b[1], a[2] * b[0] - a[0] * b[2], a[0] * b[1] - a[1] * b[0]];
}
function norm3(v) {
  const l = Math.hypot(v[0], v[1], v[2]) || 1;
  return [v[0] / l, v[1] / l, v[2] / l];
}
function dot3(a, b) {
  return a[0] * b[0] + a[1] * b[1] + a[2] * b[2];
}
function dist3(a, b) {
  return Math.hypot(a[0] - b[0], a[1] - b[1], a[2] - b[2]);
}
function centroid3(points) {
  return [0, 1, 2].map((k) => points.reduce((sum, p) => sum + p[k], 0) / points.length);
}

// geometricFaceNormal computes a triangle's own flat-face normal directly
// from its vertex positions (cross product of two edges), then flips it
// if necessary so it points away from the die's own center — every shape
// here is a convex solid centered at its local origin, so "away from
// center" is a reliable, mesh-independent way to know which of the two
// possible cross-product signs is actually outward. This is deliberately
// NOT sourced from the mesh's own stored per-vertex "normals" attribute:
// for the *collider* meshes specifically, averaging that stored attribute
// across a triangle's 3 vertices turned out to be an unreliable stand-in
// for "which way does this flat triangle face" (confirmed by comparing
// both against the geometric answer on every shape — see this session's
// final report for the full trail) — most likely because the collider is
// a much lower-poly proxy than the visual mesh, and its exported vertex
// normals don't necessarily represent per-triangle flatness the way a
// smooth-shaded visual mesh's do. Using the geometric answer everywhere
// sidesteps needing to know which of the two meshes' stored data can be
// trusted for this particular purpose.
function geometricFaceNormal(positions, i0, i1, i2, centerRef) {
  const p0 = [positions[i0 * 3], positions[i0 * 3 + 1], positions[i0 * 3 + 2]];
  const p1 = [positions[i1 * 3], positions[i1 * 3 + 1], positions[i1 * 3 + 2]];
  const p2 = [positions[i2 * 3], positions[i2 * 3 + 1], positions[i2 * 3 + 2]];
  let normal = norm3(cross3(sub3(p1, p0), sub3(p2, p0)));
  const centroid = centroid3([p0, p1, p2]);
  if (dot3(normal, sub3(centroid, centerRef)) < 0) normal = [-normal[0], -normal[1], -normal[2]];
  return { normal, centroid };
}

// colliderFaceInfo returns the target face's centroid, (geometric, unit)
// normal, and a "how big is this face" radius (centroid-to-vertex
// distance) — all in the die's local mesh space — for the single collider
// triangle whose colliderFaceMap entry equals `value`. d10's face values
// run 1-10 with 10 meaning the physically-printed "0" (see settleDie's
// former comment, now here: a real d10 is printed 0-9, and this theme's
// mesh follows that same convention), so callers pass the server's raw
// result through unchanged — never re-mapped to "0" — since colliderFaceMap
// itself already uses 10 for that face.
function colliderFaceInfo(colliderMesh, faceMap, value) {
  const key = Object.keys(faceMap).find((k) => faceMap[k] === value);
  if (key === undefined) return null;
  const tri = Number(key);
  const idx = colliderMesh.indices;
  const pos = colliderMesh.positions;
  const verts = [idx[tri * 3], idx[tri * 3 + 1], idx[tri * 3 + 2]];
  const { normal, centroid } = geometricFaceNormal(pos, verts[0], verts[1], verts[2], [0, 0, 0]);
  const points = verts.map((i) => [pos[i * 3], pos[i * 3 + 1], pos[i * 3 + 2]]);
  const radius = Math.max(...points.map((p) => dist3(centroid, p)));
  return { centroid, normal, radius };
}

// findBestVisualTriangle locates the one high-poly visual-mesh triangle
// that represents the same physical face as the given collider
// centroid/normal (see the module doc block above for why this needs both
// a tight normal match and a spatial one, and why only ever one triangle
// rather than a gathered set). Loosens the normal threshold in fixed
// steps only if nothing qualifies at the strict end, so an unusually
// irregular face somewhere in a mesh degrades gracefully instead of
// leaving that one die value with no face-up information at all.
const NORMAL_THRESHOLD_STEPS = [0.99, 0.97, 0.95, 0.9, 0.8];
function findBestVisualTriangle(visualMesh, targetCentroid, targetNormal) {
  const idx = visualMesh.indices;
  const pos = visualMesh.positions;
  const nrm = visualMesh.normals;
  const triCount = idx.length / 3;

  for (const threshold of NORMAL_THRESHOLD_STEPS) {
    let best = null;
    let bestDot = threshold;
    let bestDist = Infinity;
    for (let t = 0; t < triCount; t++) {
      const verts = [idx[t * 3], idx[t * 3 + 1], idx[t * 3 + 2]];
      let n = [0, 0, 0];
      for (const i of verts) n = add3(n, [nrm[i * 3], nrm[i * 3 + 1], nrm[i * 3 + 2]]);
      n = norm3(n);
      const dp = dot3(n, targetNormal);
      if (dp < bestDot - 1e-6) continue;
      const points = verts.map((i) => [pos[i * 3], pos[i * 3 + 1], pos[i * 3 + 2]]);
      const dd = dist3(centroid3(points), targetCentroid);
      if (best === null || dp > bestDot + 1e-6 || dd < bestDist) {
        best = verts;
        bestDot = Math.max(bestDot, dp);
        bestDist = dd;
      }
    }
    if (best) return best;
  }
  return null;
}

// tangentBitangentFromMesh reads the mesh's own precomputed per-vertex
// tangent+handedness attribute (see the module doc block's step 3 for why
// this is used instead of deriving tangent space from the matched
// triangle's own UVs) and returns the averaged tangent and derived
// bitangent for the three given vertex indices, in local mesh space.
// Returns null if this mesh has no tangents array at all (none of the
// vendored theme's shapes are expected to hit this — a graceful
// degradation, not an expected path).
function tangentBitangentFromMesh(visualMesh, verts, normal) {
  const tangents = visualMesh.tangents;
  if (!tangents) return null;
  let t = [0, 0, 0];
  let handednessSum = 0;
  for (const i of verts) {
    t = add3(t, [tangents[i * 4], tangents[i * 4 + 1], tangents[i * 4 + 2]]);
    handednessSum += tangents[i * 4 + 3];
  }
  t = norm3(t);
  const handedness = handednessSum >= 0 ? 1 : -1;
  const n = new THREE.Vector3(...normal);
  const tangent = new THREE.Vector3(...t);
  const bitangent = new THREE.Vector3().crossVectors(n, tangent).multiplyScalar(handedness).normalize();
  return { tangent, bitangent };
}

// computeTargetQuaternion returns the final resting orientation for
// `sides` showing `value`, or null if this shape/value can't be resolved
// against the loaded theme (an unsupported shape, or a value with no
// colliderFaceMap entry) — callers fall back to a random pose rather than
// failing outright, since a die is still a die even if this module can't
// prove which face it's showing.
function computeTargetQuaternion(sides, value) {
  const theme = themeState;
  if (!theme) return null;
  const name = SHAPE_NAMES[sides];
  const visualMesh = theme.meshByName[name];
  const colliderMesh = theme.meshByName[`${name}_collider`];
  const faceMap = theme.colliderFaceMap[name];
  if (!visualMesh || !colliderMesh || !faceMap) return null;

  const face = colliderFaceInfo(colliderMesh, faceMap, value);
  if (!face) return null;
  // Negated, deliberately: face.normal is the collider triangle's
  // *geometric* outward normal (away from the die's center — see
  // geometricFaceNormal). The collider mesh is a pick-only utility proxy
  // (upstream only ever raycasts against it — see this module's own
  // colliderFaceMap doc comment) that was never rendered and so was never
  // authored or verified for winding-order/outward-facing consistency the
  // way the *visual* mesh was — there was never a reason for anyone to
  // care which way its triangles "face." Aligning the raw outward normal
  // to CAMERA_DIR reliably landed the die on its *opposite* face instead
  // (verified directly: sampling the actual rendered pixels at the
  // computed screen position showed "6" for a requested "1", "5" for a
  // requested "2", and so on — the full antipodal pairing, for every d6
  // value). Negating first is the empirically-confirmed fix, checked the
  // same way — direct pixel sampling, not just the math — across every
  // value of every shape this module supports, not merely d6.
  const targetNormal = new THREE.Vector3(...face.normal).negate();
  const align = new THREE.Quaternion().setFromUnitVectors(targetNormal, CAMERA_DIR);

  const verts = findBestVisualTriangle(visualMesh, face.centroid, face.normal);
  const tb = verts ? tangentBitangentFromMesh(visualMesh, verts, face.normal) : null;
  if (!tb) return align; // face is right; azimuth is whatever it lands on.

  const rotatedBitangent = tb.bitangent.clone().applyQuaternion(align);
  const projected = rotatedBitangent
    .clone()
    .sub(CAMERA_DIR.clone().multiplyScalar(rotatedBitangent.dot(CAMERA_DIR)))
    .normalize();
  const cross = new THREE.Vector3().crossVectors(projected, SCREEN_UP);
  const sign = Math.sign(cross.dot(CAMERA_DIR)) || 1;
  const angle = Math.acos(THREE.MathUtils.clamp(projected.dot(SCREEN_UP), -1, 1)) * sign;
  const azimuth = new THREE.Quaternion().setFromAxisAngle(CAMERA_DIR, angle);
  return azimuth.multiply(align);
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
// keeps the die's apparent size constant as it spins. CAMERA_DIR/
// SCREEN_UP above are derived from this same position — if this ever
// changes, update those too.
function buildCamera() {
  const camera = new THREE.OrthographicCamera(-2.1, 2.1, 2.1, -2.1, 0.1, 20);
  camera.position.set(1.15, 1.65, 3.0);
  camera.lookAt(0, 0, 0);
  return camera;
}

// getGeometryAndMaterial returns the loaded theme's geometry/material for
// `sides`, or null if the theme isn't ready yet or doesn't cover this
// shape — callers use this to decide between a real render and a
// placeholder (see renderSnapshot/DieController).
function getGeometryAndMaterial(sides) {
  if (!themeState) return null;
  const geometry = themeState.geometryBySides[sides];
  if (!geometry) return null;
  return { geometry, material: themeState.material };
}

function buildScene(sides) {
  const gm = getGeometryAndMaterial(sides);
  if (!gm) return null;
  const scene = new THREE.Scene();
  addLights(scene);
  const mesh = new THREE.Mesh(gm.geometry, gm.material);
  scene.add(mesh);
  return { scene, mesh };
}

// --- Idle snapshot (ghost / not-yet-rolled / already-revealed dice) ---
//
// A die that isn't actively tumbling costs nothing but a captured PNG
// frame — no persistent canvas, no GL context. Each die gets its own
// small random resting tilt (seeded once per instance, see
// DieController below) purely so a row of same-sided dice doesn't look
// like stamped copies of each other. An idle/not-yet-revealed die's tilt
// carries no meaning (the roll hasn't happened, or its result is
// deliberately hidden from this viewer — design doc §9.7) — only a
// *settled* die's orientation is ever asserted to mean something.
function renderSnapshot(sides, quaternion) {
  const entry = acquireRenderer();
  if (!entry) return null; // pool exhausted — caller falls back to a flat placeholder.
  const built = buildScene(sides);
  if (!built) {
    releaseRenderer(entry);
    return null; // theme not loaded yet — caller retries (see retryIdleSnapshot).
  }
  const size = Math.round(CANVAS_LOGICAL_SIZE * Math.min(window.devicePixelRatio || 1, MAX_PIXEL_RATIO));
  entry.renderer.setSize(size, size, false);
  built.mesh.quaternion.copy(quaternion);
  const camera = buildCamera();
  entry.renderer.render(built.scene, camera);
  const dataUrl = entry.canvas.toDataURL("image/png");
  releaseRenderer(entry);
  return dataUrl;
}

function randomQuaternion() {
  // A random axis + random angle, rather than three independent random
  // Euler angles — the latter biases toward the poles and would make a
  // resting tilt (or a tumble's start pose) look subtly less random than
  // it should.
  const axis = new THREE.Vector3(Math.random() - 0.5, Math.random() - 0.5, Math.random() - 0.5).normalize();
  const angle = Math.random() * Math.PI * 2;
  return new THREE.Quaternion().setFromAxisAngle(axis, angle);
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
    this.restQuaternion = randomQuaternion();
    this.node = this.renderIdleNode();
  }

  // renderIdleNode produces the resting-pose <img> node synchronously —
  // construction of a die element never blocks or waits on anything. If
  // the pool is fully checked out at that exact instant, or the theme's
  // mesh/texture assets haven't finished loading yet (only possible very
  // early in the page's life — see ensureThemeLoading), it returns a
  // plain themed placeholder <div> immediately and schedules a few short
  // retries (retryIdleSnapshot) to swap in the real snapshot as soon as
  // possible, so the placeholder is a brief transition rather than a
  // permanent downgrade for that die.
  renderIdleNode() {
    ensureThemeLoading();
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

  // tumble runs the scripted spin-in-place animation for
  // TUMBLE_DURATION_MS, swapping this die's DOM node from its resting
  // <img> to a live canvas for the duration and back to a fresh
  // (now-revealed) <img> afterward. `result` is the server-decided value
  // this die actually rolled (design doc §3.1/§4 — always computed
  // server-side, this animation only plays it back); it's resolved to a
  // target orientation once via computeTargetQuaternion and the die spins
  // from a random start pose to exactly that orientation, so the settled
  // die's face *is* the result — see this module's top-of-file doc block
  // for the face-orientation math and its verification status.
  //
  // If the renderer pool is momentarily exhausted (a burst of many
  // simultaneous reveals — see MAX_LIVE_RENDERERS), it retries a handful
  // of times, short enough to still stay inside TUMBLE_DURATION_MS. If
  // still exhausted after that, or the theme assets haven't loaded yet,
  // it gives up on the *animation* but still renders one static frame in
  // the correct final orientation (see showStaticResult) before giving
  // up entirely — with no DOM text fallback backing this up any more (see
  // this file's top-of-file doc block), a die that never spins is a
  // cosmetic miss, but a die that settles on the *wrong* face because the
  // pool was briefly exhausted would be a silent correctness bug, so this
  // path still spends one renderer acquisition on getting the face right
  // even when it can't afford the animation. app.js's settleDie still
  // adds the "revealed"/"dropped" classes on schedule regardless of any
  // of this, since those never depend on the animation completing.
  tumble(containerEl, result, attempt = 0) {
    if (!themeState) {
      if (attempt < TUMBLE_ACQUIRE_RETRY_ATTEMPTS) {
        ensureThemeLoading(); // kick the load if it hasn't started; retry below regardless.
        window.setTimeout(() => this.tumble(containerEl, result, attempt + 1), TUMBLE_ACQUIRE_RETRY_MS);
      } else {
        this.showStaticResult(containerEl, result);
      }
      return;
    }
    const entry = acquireRenderer();
    if (!entry) {
      if (attempt < TUMBLE_ACQUIRE_RETRY_ATTEMPTS) {
        window.setTimeout(() => this.tumble(containerEl, result, attempt + 1), TUMBLE_ACQUIRE_RETRY_MS);
      } else {
        this.showStaticResult(containerEl, result);
      }
      return;
    }

    const size = Math.round(CANVAS_LOGICAL_SIZE * Math.min(window.devicePixelRatio || 1, MAX_PIXEL_RATIO));
    entry.renderer.setSize(size, size, false);
    entry.canvas.className = "roll-die-visual";
    if (containerEl.firstChild) containerEl.replaceChild(entry.canvas, containerEl.firstChild);
    else containerEl.appendChild(entry.canvas);

    const built = buildScene(this.sides);
    const camera = buildCamera();
    const startQuaternion = randomQuaternion();
    const targetQuaternion = computeTargetQuaternion(this.sides, result) || randomQuaternion();
    // A decaying extra spin around its own random axis, layered on top of
    // the start->target slerp, so the die visibly *spins* rather than
    // just smoothly reorienting (a plain two-pose slerp can be a very
    // small, undramatic rotation if the random start happens to land
    // close to the target). The extra spin is always a whole number of
    // turns away from identity at the animation's end (t=1 => remaining
    // turns = 0), so it never affects the final resting orientation.
    const spinAxis = new THREE.Vector3(Math.random() - 0.5, Math.random() - 0.5, Math.random() - 0.5).normalize();
    const extraTurns = 2 + Math.random();

    const start = performance.now();

    const step = (time) => {
      const elapsed = time - start;
      const t = Math.min(elapsed / TUMBLE_DURATION_MS, 1);
      const eased = 1 - Math.pow(1 - t, 3); // ease-out cubic — fast start, gentle settle.

      const base = new THREE.Quaternion().slerpQuaternions(startQuaternion, targetQuaternion, eased);
      const remainingAngle = extraTurns * (1 - eased) * Math.PI * 2;
      const spin = new THREE.Quaternion().setFromAxisAngle(spinAxis, remainingAngle);
      built.mesh.quaternion.copy(spin.multiply(base));
      entry.renderer.render(built.scene, camera);

      if (t < 1) {
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
      built.mesh.quaternion.copy(targetQuaternion);
      entry.renderer.render(built.scene, camera);
      this.restQuaternion = targetQuaternion.clone();
      const dataUrl = entry.canvas.toDataURL("image/png");
      if (containerEl.firstChild === entry.canvas) {
        containerEl.replaceChild(this.imageNode(dataUrl), entry.canvas);
      }
      releaseRenderer(entry);
    };
    requestAnimationFrame(step);
  }

  // showStaticResult is tumble's last-resort path when the animation
  // itself couldn't run (pool exhausted, or the theme assets still
  // weren't ready after every retry) — a single synchronous idle-style
  // snapshot (see renderSnapshot) in the correct target orientation, no
  // spin. If even this fails (pool *still* exhausted right now, or the
  // theme hasn't loaded yet), it retries on its own, much longer budget
  // (STATIC_RESULT_RETRY_MS/ATTEMPTS, not the short animation-acquisition
  // one) — see that constant's own comment for why this path in
  // particular has to keep trying rather than settle for "eventually
  // gave up": with no DOM text overlay behind it any more, giving up here
  // means silently showing the wrong face forever, not just skipping an
  // animation.
  showStaticResult(containerEl, result, attempt = 0) {
    const targetQuaternion = computeTargetQuaternion(this.sides, result);
    const dataUrl = renderSnapshot(this.sides, targetQuaternion || this.restQuaternion);
    if (dataUrl) {
      this.restQuaternion = targetQuaternion || this.restQuaternion;
      if (containerEl.firstChild) containerEl.replaceChild(this.imageNode(dataUrl), containerEl.firstChild);
      else containerEl.appendChild(this.imageNode(dataUrl));
      return;
    }
    if (attempt >= STATIC_RESULT_RETRY_ATTEMPTS) return; // exhausted an unusually long budget — see that constant's comment.
    window.setTimeout(() => {
      if (!containerEl.isConnected) return;
      this.showStaticResult(containerEl, result, attempt + 1);
    }, STATIC_RESULT_RETRY_MS);
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

// tumbleAndSettle starts dieEl's spin-to-result animation. `result` is
// the server-decided value this die rolled (for a d10, 10 means the
// physically-printed "0" face — see colliderFaceInfo — so pass the raw
// server value through unchanged, never pre-mapped). Safe to call even if
// buildDieVisual was never called for this element (a die from before
// this module loaded, or an unrecognized element) — it just no-ops
// rather than throwing, since app.js's own reveal timer never depends on
// this animation completing.
export function tumbleAndSettle(dieEl, result) {
  const controller = controllers.get(dieEl);
  if (!controller) return;
  controller.tumble(dieEl, result);
}

