// Copyright (c) 2026 James Duane Plotts. Licensed under the MIT License.
// See LICENSE in the repository root.
//
// A real CSS 3D icosahedron (d20), built from actual polyhedron geometry
// rather than a sprite/image — 20 triangular face elements, each placed
// with a computed 3D transform. Appearance (face color, edge color, pip/
// number ink, material sheen) is driven entirely by CSS custom properties
// under a `data-dice-skin` attribute (see dice-skins.css) — this file
// only builds and animates geometry, never colors anything directly, so a
// community "skin" is a CSS rule block, not a JS change. That's the same
// "restyle without touching code or rebuilding" philosophy that's already
// why web/ is served from disk unembedded (see main.go's package doc).

"use strict";

// --- Icosahedron geometry ---
//
// Standard regular-icosahedron construction: 12 vertices at the cyclic
// permutations of (0, ±1, ±phi) using the golden ratio, and the canonical
// 20-triangle face list over those vertex indices (the same vertex/face
// table used by, e.g., three.js's IcosahedronGeometry and most
// procedural-geometry references). Computed once at module load — the
// numbers below are the only thing anyone would need to change to reuse
// this for a different die (see buildFaceElements' faceCount param for
// how a d6/d4/etc. would plug into the same rig).
const PHI = (1 + Math.sqrt(5)) / 2;

const ICOSAHEDRON_VERTICES = [
  [-1, PHI, 0], [1, PHI, 0], [-1, -PHI, 0], [1, -PHI, 0],
  [0, -1, PHI], [0, 1, PHI], [0, -1, -PHI], [0, 1, -PHI],
  [PHI, 0, -1], [PHI, 0, 1], [-PHI, 0, -1], [-PHI, 0, 1],
];

const ICOSAHEDRON_FACES = [
  [0, 11, 5], [0, 5, 1], [0, 1, 7], [0, 7, 10], [0, 10, 11],
  [1, 5, 9], [5, 11, 4], [11, 10, 2], [10, 7, 6], [7, 1, 8],
  [3, 9, 4], [3, 4, 2], [3, 2, 6], [3, 6, 8], [3, 8, 9],
  [4, 9, 5], [2, 4, 11], [6, 2, 10], [8, 6, 7], [9, 8, 1],
];

function normalize(v) {
  const len = Math.hypot(v[0], v[1], v[2]) || 1;
  return [v[0] / len, v[1] / len, v[2] / len];
}

function cross(a, b) {
  return [a[1] * b[2] - a[2] * b[1], a[2] * b[0] - a[0] * b[2], a[0] * b[1] - a[1] * b[0]];
}

function dot(a, b) {
  return a[0] * b[0] + a[1] * b[1] + a[2] * b[2];
}

// axisAngleTo returns the {axis, angleDeg} of the rotation that carries
// the unit vector (0, 0, 1) onto target (also expected to be a unit
// vector) — used both to place each static face (rotate the "camera-
// facing" template face out onto the polyhedron's surface) and, with
// from/to swapped, to spin the whole die so a chosen face ends up
// pointing at the viewer (see landingTransform).
function axisAngleTo(from, target) {
  const d = Math.max(-1, Math.min(1, dot(from, target)));
  const angle = Math.acos(d);
  const axis = cross(from, target);
  const axisLen = Math.hypot(...axis);
  if (axisLen < 1e-6) {
    // from/target are parallel or antiparallel; any perpendicular axis works.
    return d > 0 ? { axis: [0, 1, 0], angleDeg: 0 } : { axis: [0, 1, 0], angleDeg: 180 };
  }
  return { axis: axis.map((v) => v / axisLen), angleDeg: (angle * 180) / Math.PI };
}

// faceGeometry computes, for each of the 20 faces, its outward unit
// normal (== normalized centroid, since this polyhedron is centered on
// the origin) and the placement transform (rotate3d + translateZ) that
// moves a template face — a flat triangle lying in the XY plane, facing
// +Z, centered at the origin — out onto that face of the die.
function faceGeometry(radiusPx) {
  const verts = ICOSAHEDRON_VERTICES.map(normalize);
  return ICOSAHEDRON_FACES.map((face) => {
    const [a, b, c] = face.map((i) => verts[i]);
    const centroid = normalize([
      (a[0] + b[0] + c[0]) / 3,
      (a[1] + b[1] + c[1]) / 3,
      (a[2] + b[2] + c[2]) / 3,
    ]);
    const { axis, angleDeg } = axisAngleTo([0, 0, 1], centroid);
    return {
      normal: centroid,
      placeTransform: `rotate3d(${axis[0]}, ${axis[1]}, ${axis[2]}, ${angleDeg}deg) translateZ(${radiusPx}px)`,
    };
  });
}

// FACE_RADIUS_PX is the distance from the die's center to each face
// plane (the icosahedron's inradius, in the same unit scale as
// ICOSAHEDRON_VERTICES' un-normalized coordinates, then re-scaled to
// pixels by DIE_SIZE_PX below) — i.e. how far along its own normal each
// triangle is pushed out from center to sit flush on the polyhedron's
// surface for a die of DIE_SIZE_PX "radius."
const DIE_SIZE_PX = 40;

/// buildDie creates one d20 DOM element (a `.die` container with 20
/// `.die-face` children, each numbered 1-20 in face-table order — this is
/// a synthetic numbering for a consistent, always-correct "land on the
/// resulted face" animation, not a claim to reproduce any particular
/// physical die's exact face layout). Returns the container; callers
/// position/skin it via CSS and drive rolls via startTumble/settleOnResult.
function buildDie() {
  const die = document.createElement("div");
  die.className = "die die-d20";

  const faces = faceGeometry(DIE_SIZE_PX);
  faces.forEach((face, i) => {
    const faceEl = document.createElement("div");
    faceEl.className = "die-face";
    faceEl.style.transform = face.placeTransform;
    faceEl.dataset.faceNormal = face.normal.join(",");
    const label = document.createElement("span");
    label.className = "die-face-number";
    label.textContent = String(i + 1);
    faceEl.appendChild(label);
    die.appendChild(faceEl);
  });

  return die;
}

// landingTransform returns the CSS transform that rotates die so its
// resultNumber face (1-20, matching buildDie's numbering) points at the
// viewer (+Z) — the inverse of how that face was placed on the
// polyhedron in the first place, so it reuses the same axisAngleTo helper
// with from/target swapped.
function landingTransform(resultNumber) {
  const faces = faceGeometry(DIE_SIZE_PX);
  const face = faces[((resultNumber - 1) % faces.length + faces.length) % faces.length];
  const { axis, angleDeg } = axisAngleTo(face.normal, [0, 0, 1]);
  return `rotate3d(${axis[0]}, ${axis[1]}, ${axis[2]}, ${angleDeg}deg)`;
}

// Per-die tumble state, keyed by the die element itself (a WeakMap so a
// removed die's in-flight animation doesn't leak). startTumble/
// settleOnResult are deliberately two separate calls rather than one
// combined "animate and reveal" function: roll.request (start the flight)
// and roll.result (the actual outcome) arrive as two separate messages
// over a real network, an unpredictable gap apart — driving the tumble
// via requestAnimationFrame rather than a fixed-duration CSS transition
// means it can keep spinning smoothly for however long that gap actually
// is, then settle the instant the real result shows up, rather than
// guessing a duration and hoping the result arrives in time.
const tumbleState = new WeakMap();

// startTumble begins an indefinite spin on dieEl — call on roll.request.
// Safe to call again before settleOnResult if a die is somehow re-rolled
// mid-flight; it just restarts with a fresh random axis.
function startTumble(dieEl) {
  stopTumble(dieEl);
  dieEl.classList.remove("die-settled");
  dieEl.style.transition = "none";

  const axis = normalize([Math.random() * 2 - 1, Math.random() * 2 - 1, Math.random() * 2 - 1]);
  const degPerMs = 0.35 + Math.random() * 0.15;
  let lastTime = null;
  let totalDeg = 0;

  const tick = (time) => {
    if (lastTime !== null) totalDeg += (time - lastTime) * degPerMs;
    lastTime = time;
    dieEl.style.transform = `rotate3d(${axis[0]}, ${axis[1]}, ${axis[2]}, ${totalDeg}deg)`;
    const entry = tumbleState.get(dieEl);
    if (entry) entry.rafId = requestAnimationFrame(tick);
  };
  tumbleState.set(dieEl, { rafId: requestAnimationFrame(tick) });
}

function stopTumble(dieEl) {
  const entry = tumbleState.get(dieEl);
  if (entry) {
    cancelAnimationFrame(entry.rafId);
    tumbleState.delete(dieEl);
  }
}

// settleOnResult stops any in-flight tumble and transitions dieEl to rest
// precisely on resultNumber's face — call on roll.result. If startTumble
// was never called first (e.g. roll.result arrived with no preceding
// roll.request for some reason), it still settles cleanly from wherever
// the die currently sits.
function settleOnResult(dieEl, resultNumber, onSettled) {
  stopTumble(dieEl);
  dieEl.style.transition = "transform 420ms cubic-bezier(.22,.9,.34,1.2)";
  dieEl.style.transform = landingTransform(resultNumber);

  const onLandEnd = () => {
    dieEl.removeEventListener("transitionend", onLandEnd);
    dieEl.classList.add("die-settled");
    if (onSettled) onSettled();
  };
  dieEl.addEventListener("transitionend", onLandEnd);
}

// --- Skins ---
//
// A skin is nothing but a CSS rule block setting custom properties under
// `[data-dice-skin="<name>"]` (see dice-skins.css) — applyDiceSkin only
// sets the attribute and remembers the choice; it has no idea what
// properties a skin defines, on purpose, so adding a new community skin
// never touches this file.
const DICE_SKIN_STORAGE_KEY = "layforge.diceSkin";

function applyDiceSkin(name) {
  document.documentElement.dataset.diceSkin = name;
  try {
    localStorage.setItem(DICE_SKIN_STORAGE_KEY, name);
  } catch {
    // Best-effort only — a private-browsing/storage-disabled session just
    // won't remember the choice across reloads, which is fine.
  }
}

function loadSavedDiceSkin(fallback) {
  try {
    return localStorage.getItem(DICE_SKIN_STORAGE_KEY) || fallback;
  } catch {
    return fallback;
  }
}
