// Copyright (c) 2026 James Duane Plotts. Licensed under the MIT License.
// See LICENSE in the repository root.
//
// A real WebGL d20 (three.js) with physics-driven tumbling (cannon-es) —
// see vendor/README.md for both. Replaces an earlier pure-CSS-3D attempt
// (hand-rolled icosahedron transforms) that couldn't produce a true
// shared-vertex mesh, so edges never quite lined up; THREE.IcosahedronGeometry
// gives that for free. Physics is purely cosmetic, same principle as
// everything else about a roll's appearance (design doc §3.1, §4): it
// never determines the result, only how the die visually gets there —
// settleOnResult always forces the exact server-authoritative face,
// overriding wherever physics happened to leave it.
//
// The die is tossed INTO the message log: dice.js renders into an overlay
// canvas sized to the visible log viewport, and the tumbling box's four
// side walls are rebuilt from that viewport's live pixel dimensions
// (resizeArena) while a static box collider is placed over every visible
// chat bubble (setBubbleColliders), so the die caroms off the real edges
// of the leather area and off the bubbles before it comes to rest.
//
// Skins (see dice-skins.js) drive color/texture/font only, the same
// "restyle without touching this file" contract the CSS-skin version
// made — a community skin is a new entry in that manifest (plus optional
// PNGs), never a change here.

import * as THREE from "./vendor/three.module.min.js";
import * as CANNON from "./vendor/cannon-es.js";
import { SKINS, DEFAULT_SKIN_ID } from "./dice-skins.js";

// ES modules are always strict mode — no "use strict" directive needed.

const DIE_RADIUS = 1;
const ATLAS_COLS = 5;
const ATLAS_ROWS = 4;
const FACE_COUNT = ATLAS_COLS * ATLAS_ROWS; // 20 — matches the icosahedron.

// --- Face geometry extraction ---
//
// THREE.IcosahedronGeometry(radius, 0) is non-indexed at detail 0: its
// position attribute is 20 faces * 3 unique vertices, in face order. That
// means face i's three vertices are exactly attribute entries
// [3*i, 3*i+1, 3*i+2] — no separate face-index table to keep in sync
// with the actual rendered mesh (the CSS version's real bug), since this
// reads the geometry three.js itself generated.
function extractFaces(geometry) {
  const pos = geometry.attributes.position;
  const faces = [];
  for (let i = 0; i < FACE_COUNT; i++) {
    const a = new THREE.Vector3().fromBufferAttribute(pos, i * 3);
    const b = new THREE.Vector3().fromBufferAttribute(pos, i * 3 + 1);
    const c = new THREE.Vector3().fromBufferAttribute(pos, i * 3 + 2);
    const centroid = a.clone().add(b).add(c).divideScalar(3);
    const normal = centroid.clone().normalize();
    faces.push({ centroid, normal });
  }
  return faces;
}

// --- Number atlas (canvas-rendered, or a skin-supplied PNG) ---
//
// One texture holds all 20 digits in a 5x4 grid, so every face reuses
// the same GPU upload with a different offset/repeat window rather than
// 20 separate textures. A skin's numberTexture (see dice-skins.js) is
// expected to follow the same grid convention (cell index = number - 1,
// row-major) if it supplies one instead of a font.
function buildNumberAtlasTexture(skin) {
  const canvas = document.createElement("canvas");
  const cellSize = 128;
  canvas.width = cellSize * ATLAS_COLS;
  canvas.height = cellSize * ATLAS_ROWS;
  const ctx = canvas.getContext("2d");
  ctx.font = skin.font;
  ctx.fillStyle = skin.numberColor;
  ctx.textAlign = "center";
  ctx.textBaseline = "middle";
  for (let n = 1; n <= FACE_COUNT; n++) {
    const col = (n - 1) % ATLAS_COLS;
    const row = Math.floor((n - 1) / ATLAS_COLS);
    const cx = col * cellSize + cellSize / 2;
    const cy = row * cellSize + cellSize / 2;
    ctx.fillText(String(n), cx, cy);
  }
  const texture = new THREE.CanvasTexture(canvas);
  texture.colorSpace = THREE.SRGBColorSpace;
  return texture;
}

function loadTexture(url) {
  if (!url) return null;
  return new THREE.TextureLoader().load(url);
}

// atlasUvFor returns {offset, repeat} for face number n (1-20) into a
// grid atlas texture — used for both the canvas-drawn atlas above and a
// skin-supplied numberTexture PNG, so both paths share one convention.
function atlasUvFor(n) {
  const col = (n - 1) % ATLAS_COLS;
  const row = Math.floor((n - 1) / ATLAS_COLS);
  return {
    offsetX: col / ATLAS_COLS,
    // Canvas Y grows downward but texture V grows upward, and the grid
    // was filled row-major top-to-bottom — flip the row here rather than
    // fight it in canvas-drawing code.
    offsetY: 1 - (row + 1) / ATLAS_ROWS,
    repeatX: 1 / ATLAS_COLS,
    repeatY: 1 / ATLAS_ROWS,
  };
}

// --- Materials ---

function buildBaseMaterial(skin) {
  return new THREE.MeshStandardMaterial({
    color: skin.baseColor,
    map: loadTexture(skin.baseTexture),
    flatShading: true,
    roughness: 0.45,
    metalness: 0.08,
  });
}

function buildNumberMaterials(skin, faces) {
  const atlas = skin.numberTexture ? loadTexture(skin.numberTexture) : buildNumberAtlasTexture(skin);
  atlas.wrapS = atlas.wrapT = THREE.ClampToEdgeWrapping;
  return faces.map((_, i) => {
    const tex = atlas.clone();
    tex.needsUpdate = true;
    const uv = atlasUvFor(i + 1);
    tex.offset.set(uv.offsetX, uv.offsetY);
    tex.repeat.set(uv.repeatX, uv.repeatY);
    return new THREE.MeshBasicMaterial({
      map: tex,
      transparent: true,
      depthTest: true,
      polygonOffset: true,
      polygonOffsetFactor: -4,
    });
  });
}

// --- Physics arena ---
//
// The die tumbles inside a slab whose four side walls are rebuilt from
// the message log's live pixel size (resizeArena), plus a static box
// collider over every on-screen chat bubble (setBubbleColliders). All of
// it is cosmetic — settleOnResult still forces the exact
// server-authoritative face wherever physics left the die.

const PX_PER_UNIT = 30; // screen px per physics unit — the die is 2 units (~60px) across.
const DEPTH_HALF = 1.6; // half-depth of the tumbling slab (toward/away from camera).
const GRAVITY_Y = -42;  // strong-ish so a tossed die commits down into the message area.
const REST_LINEAR = 1.15; // below this speed (and REST_ANGULAR) the die counts as "at rest".
const REST_ANGULAR = 2.3;
const MIN_TUMBLE_MS = 1100; // don't let rest-detection cut the toss short — the player should see it travel.
const MAX_SPEED = 24; // cap linear speed each step — keeps a die that starts slightly overlapping a
const MAX_SPIN = 34; // bubble collider (or gets pinched between two) from rocketing off unrealistically.
const SETTLE_MAX_WAIT_MS = 2500; // hard cap: settle even if the die never fully rests.
const SETTLE_DURATION_MS = 420;
const REST_FADE_DELAY_MS = 2600;
const REST_OPACITY = 0.14;

function buildArena() {
  const world = new CANNON.World({ gravity: new CANNON.Vec3(0, GRAVITY_Y, 0) });
  world.allowSleep = false;

  const dieMaterial = new CANNON.Material("die");
  const trayMaterial = new CANNON.Material("tray");
  world.addContactMaterial(
    new CANNON.ContactMaterial(dieMaterial, trayMaterial, { friction: 0.3, restitution: 0.58 })
  );

  // CANNON.Plane's face normal is +Z; each wall is that plane rotated so
  // its normal points inward. Positions are placeholders until
  // resizeArena maps them onto the real message-area edges.
  const makePlane = (axis, angle) => {
    const body = new CANNON.Body({ mass: 0, material: trayMaterial, shape: new CANNON.Plane() });
    body.quaternion.setFromAxisAngle(axis, angle);
    world.addBody(body);
    return body;
  };
  const walls = {
    floor: makePlane(new CANNON.Vec3(1, 0, 0), -Math.PI / 2),
    ceiling: makePlane(new CANNON.Vec3(1, 0, 0), Math.PI / 2),
    left: makePlane(new CANNON.Vec3(0, 1, 0), Math.PI / 2),
    right: makePlane(new CANNON.Vec3(0, 1, 0), -Math.PI / 2),
    back: makePlane(new CANNON.Vec3(0, 1, 0), 0),
    front: makePlane(new CANNON.Vec3(0, 1, 0), Math.PI),
  };
  walls.back.position.set(0, 0, -DEPTH_HALF);
  walls.front.position.set(0, 0, DEPTH_HALF);

  const dieBody = new CANNON.Body({
    mass: 1,
    material: dieMaterial,
    shape: new CANNON.Sphere(DIE_RADIUS), // physics uses a sphere approximation — see vendor/README.md.
    linearDamping: 0.12,
    angularDamping: 0.16,
  });
  world.addBody(dieBody);

  return { world, dieBody, walls, trayMaterial };
}

// --- Public API ---
//
// mountDie owns everything about one die instance: renderer, scene,
// physics world, the mesh and its per-face number planes. Everything
// else (startTumble/settleOnResult/resizeArena/setBubbleColliders/reskin)
// takes the handle it returns.
function mountDie(containerEl, skinId) {
  const width = containerEl.clientWidth || 160;
  const height = containerEl.clientHeight || 160;

  const scene = new THREE.Scene();
  // Orthographic so screen px map linearly to world units: a bubble rect
  // in the overlay becomes a box collider at the same apparent place.
  const camera = new THREE.OrthographicCamera(
    -width / 2 / PX_PER_UNIT,
    width / 2 / PX_PER_UNIT,
    height / 2 / PX_PER_UNIT,
    -height / 2 / PX_PER_UNIT,
    0.1,
    100
  );
  camera.position.set(0, 0, 20);
  camera.lookAt(0, 0, 0);

  const renderer = new THREE.WebGLRenderer({ antialias: true, alpha: true });
  renderer.setPixelRatio(Math.min(window.devicePixelRatio || 1, 2));
  renderer.setSize(width, height);
  renderer.setClearColor(0x000000, 0);
  containerEl.appendChild(renderer.domElement);
  // Start dimmed: a die parked in the corner shouldn't compete with the
  // conversation until the player actually rolls.
  containerEl.style.opacity = String(REST_OPACITY);

  scene.add(new THREE.AmbientLight(0xffffff, 0.6));
  const key = new THREE.DirectionalLight(0xfff4e0, 1.15);
  key.position.set(2.5, 4, 4);
  scene.add(key);
  const rim = new THREE.DirectionalLight(0xaac8ff, 0.4);
  rim.position.set(-2, -1, -3);
  scene.add(rim);

  const geometry = new THREE.IcosahedronGeometry(DIE_RADIUS, 0);
  geometry.computeVertexNormals();
  const faces = extractFaces(geometry);

  const skin = SKINS.find((s) => s.id === skinId) || SKINS.find((s) => s.id === DEFAULT_SKIN_ID);
  const baseMesh = new THREE.Mesh(geometry, buildBaseMaterial(skin));
  scene.add(baseMesh);

  const numberMaterials = buildNumberMaterials(skin, faces);
  const numberPlaneGeometry = new THREE.PlaneGeometry(0.62, 0.62);
  faces.forEach((face, i) => {
    const plane = new THREE.Mesh(numberPlaneGeometry, numberMaterials[i]);
    plane.position.copy(face.centroid).multiplyScalar(1.015);
    plane.quaternion.setFromUnitVectors(new THREE.Vector3(0, 0, 1), face.normal);
    baseMesh.add(plane);
  });

  // A pleasant resting tilt so the die doesn't look flat/dead pre-roll.
  baseMesh.quaternion.setFromEuler(new THREE.Euler(0.4, 0.5, 0));

  const arena = buildArena();
  const toCamera = camera.position.clone().normalize();

  const handle = {
    containerEl,
    scene,
    camera,
    renderer,
    baseMesh,
    faces,
    numberMaterials,
    skin,
    world: arena.world,
    dieBody: arena.dieBody,
    walls: arena.walls,
    trayMaterial: arena.trayMaterial,
    bubbleBodies: [],
    toCamera,
    view: {
      width,
      height,
      halfW: width / 2 / PX_PER_UNIT,
      halfH: height / 2 / PX_PER_UNIT,
    },
    everRolled: false,
    mode: "idle", // "idle" | "tumbling" | "settling"
    settleFromPos: null,
    settleFromQuat: null,
    settleTarget: null,
    settleTo: null,
    settleStart: 0,
    onSettled: null,
    pendingResult: null,
    tumbleStartTime: 0,
    fadeTimer: null,
    lastFrameTime: null,
  };

  resizeArena(handle, width, height);
  requestAnimationFrame((t) => renderLoop(handle, t));
  return handle;
}

// resizeArena remaps the renderer, camera and the four side walls to the
// message log's current pixel size. Drive it from a ResizeObserver on the
// overlay (hard constraint #5). A zero/tiny size (chat screen still
// hidden) is ignored — the next real measurement will land.
function resizeArena(handle, width, height) {
  if (!width || !height || width < 4 || height < 4) return;

  const halfW = width / 2 / PX_PER_UNIT;
  const halfH = height / 2 / PX_PER_UNIT;
  handle.view = { width, height, halfW, halfH };

  handle.renderer.setSize(width, height);
  handle.camera.left = -halfW;
  handle.camera.right = halfW;
  handle.camera.top = halfH;
  handle.camera.bottom = -halfH;
  handle.camera.updateProjectionMatrix();

  handle.walls.left.position.set(-halfW, 0, 0);
  handle.walls.right.position.set(halfW, 0, 0);
  handle.walls.floor.position.set(0, -halfH, 0);
  handle.walls.ceiling.position.set(0, halfH, 0);

  // Park an un-rolled die in the top-left corner (where throws originate)
  // rather than dead-centre over the text.
  if (!handle.everRolled && handle.mode === "idle") {
    const m = DIE_RADIUS * 1.6;
    handle.baseMesh.position.set(-halfW + m, halfH - m, 0);
    handle.dieBody.position.set(-halfW + m, halfH - m, 0);
  }
}

// setBubbleColliders rebuilds the static box colliders the die bounces
// off — one per currently-visible chat bubble. rects are {x, y, w, h} in
// overlay pixels where (x, y) is the bubble's CENTRE relative to the
// overlay's top-left. Drive it (debounced/throttled) from a
// MutationObserver + scroll listener on #log (hard constraint #5), never
// per animation frame.
function setBubbleColliders(handle, rects) {
  for (const body of handle.bubbleBodies) handle.world.removeBody(body);
  handle.bubbleBodies.length = 0;
  if (!handle.view.width || !Array.isArray(rects)) return;

  const { width, height } = handle.view;
  for (const r of rects) {
    const hw = Math.max(r.w, 8) / 2 / PX_PER_UNIT;
    const hh = Math.max(r.h, 8) / 2 / PX_PER_UNIT;
    const wx = (r.x - width / 2) / PX_PER_UNIT;
    const wy = (height / 2 - r.y) / PX_PER_UNIT;
    const body = new CANNON.Body({
      mass: 0,
      material: handle.trayMaterial,
      shape: new CANNON.Box(new CANNON.Vec3(hw, hh, DEPTH_HALF * 0.9)),
    });
    body.position.set(wx, wy, 0);
    handle.world.addBody(body);
    handle.bubbleBodies.push(body);
  }
}

// clampMotion bounds the die's linear/angular speed so a rare collider
// overlap (die spawned touching a bubble box, or pinched when colliders
// rebuild under it) can't fling it across the screen — purely a
// visual-sanity guard, it never touches the result.
function clampMotion(body) {
  const v = body.velocity;
  const s = Math.hypot(v.x, v.y, v.z);
  if (s > MAX_SPEED) {
    const k = MAX_SPEED / s;
    v.x *= k;
    v.y *= k;
    v.z *= k;
  }
  const w = body.angularVelocity;
  const a = Math.hypot(w.x, w.y, w.z);
  if (a > MAX_SPIN) {
    const k = MAX_SPIN / a;
    w.x *= k;
    w.y *= k;
    w.z *= k;
  }
}

function copyBodyToMesh(handle) {
  const p = handle.dieBody.position;
  const q = handle.dieBody.quaternion;
  handle.baseMesh.position.set(p.x, p.y, p.z);
  handle.baseMesh.quaternion.set(q.x, q.y, q.z, q.w);
}

function renderLoop(handle, time) {
  requestAnimationFrame((t) => renderLoop(handle, t));

  const dt = handle.lastFrameTime === null ? 1 / 60 : Math.min((time - handle.lastFrameTime) / 1000, 1 / 20);
  handle.lastFrameTime = time;

  if (handle.mode === "tumbling") {
    handle.world.step(1 / 60, dt, 5);
    clampMotion(handle.dieBody);
    copyBodyToMesh(handle);

    if (handle.pendingResult) {
      const travelledLongEnough = performance.now() - handle.tumbleStartTime > MIN_TUMBLE_MS;
      const atRest =
        travelledLongEnough &&
        handle.dieBody.velocity.length() < REST_LINEAR &&
        handle.dieBody.angularVelocity.length() < REST_ANGULAR;
      if (atRest || performance.now() >= handle.pendingResult.deadline) {
        const pr = handle.pendingResult;
        handle.pendingResult = null;
        beginSettle(handle, pr.resultNumber, pr.onSettled);
      }
    }
  } else if (handle.mode === "settling") {
    const t = Math.min((time - handle.settleStart) / SETTLE_DURATION_MS, 1);
    const eased = 1 - Math.pow(1 - t, 3); // ease-out cubic — a brisk, decisive "snap," not a lazy drift.
    handle.baseMesh.position.lerpVectors(handle.settleFromPos, handle.settleTarget, eased);
    handle.baseMesh.quaternion.slerpQuaternions(handle.settleFromQuat, handle.settleTo, eased);
    if (t >= 1) {
      handle.mode = "idle";
      const cb = handle.onSettled;
      handle.onSettled = null;
      if (cb) cb();
      scheduleRestFade(handle);
    }
  }

  handle.renderer.render(handle.scene, handle.camera);
}

// startTumble tosses the die in from near the top of the message area
// with a random throw + spin — call on roll.request. Safe to call again
// mid-flight (a re-roll) since it just resets the body's state.
function startTumble(handle) {
  const { halfW, halfH } = handle.view;
  const body = handle.dieBody;
  // Thrown in hard from a top corner, across the message area — a real
  // toss, not a drop: it flies across, caroms off the far wall and the
  // bubbles, and gravity walks it down before it rests.
  const side = Math.random() < 0.5 ? -1 : 1;
  const spawnX = side * Math.max(halfW - DIE_RADIUS * 1.6, 0.4);
  const spawnY = topClearY(handle, spawnX, halfH);
  body.position.set(spawnX, spawnY, 0);
  body.velocity.set(-side * (15 + Math.random() * 10), -1 - Math.random() * 3, (Math.random() - 0.5) * 5);
  body.angularVelocity.set(
    (Math.random() - 0.5) * 30,
    (Math.random() - 0.5) * 30,
    (Math.random() - 0.5) * 30
  );
  body.quaternion.setFromEuler(Math.random() * Math.PI, Math.random() * Math.PI, Math.random() * Math.PI);
  body.wakeUp();

  handle.everRolled = true;
  handle.pendingResult = null;
  handle.tumbleStartTime = performance.now();
  handle.mode = "tumbling";
  if (handle.fadeTimer) {
    clearTimeout(handle.fadeTimer);
    handle.fadeTimer = null;
  }
  setOverlayOpacity(handle, 1);
}

// topClearY finds a launch height near the ceiling that isn't already
// inside a bubble collider (bubbles can reach the very top of the log
// when it's scrolled up) — so the die enters the arena cleanly instead
// of being ejected from an overlap on the first physics step.
function topClearY(handle, x, halfH) {
  let y = halfH - DIE_RADIUS * 1.1;
  const bottomLimit = -halfH + DIE_RADIUS;
  for (let guard = 0; guard < 40 && y > bottomLimit; guard++) {
    let clear = true;
    for (const b of handle.bubbleBodies) {
      const he = b.shapes[0].halfExtents;
      const overlapX = x + DIE_RADIUS > b.position.x - he.x && x - DIE_RADIUS < b.position.x + he.x;
      const overlapY = y + DIE_RADIUS > b.position.y - he.y && y - DIE_RADIUS < b.position.y + he.y;
      if (overlapX && overlapY) {
        // spawn point is inside this box — drop just below it and retry.
        y = b.position.y - he.y - DIE_RADIUS * 1.1;
        clear = false;
        break;
      }
    }
    if (clear) return y;
  }
  return halfH - DIE_RADIUS * 1.1;
}

// settleOnResult ends with the die showing precisely resultNumber's face
// toward the camera — call on roll.result. While the die is still
// tumbling it defers the snap until the die rests (or SETTLE_MAX_WAIT_MS
// elapses) so the player sees the bounce first; the face shown is always
// exactly resultNumber regardless, physics only chooses the path. If
// startTumble was never called it settles immediately from the current
// pose.
function settleOnResult(handle, resultNumber, onSettled) {
  if (handle.mode === "tumbling") {
    handle.pendingResult = {
      resultNumber,
      onSettled: onSettled || null,
      deadline: performance.now() + SETTLE_MAX_WAIT_MS,
    };
    return;
  }
  beginSettle(handle, resultNumber, onSettled);
}

function beginSettle(handle, resultNumber, onSettled) {
  const idx = (((resultNumber - 1) % handle.faces.length) + handle.faces.length) % handle.faces.length;
  const face = handle.faces[idx];

  const { halfW, halfH } = handle.view;
  const cur = handle.baseMesh.position;
  const margin = DIE_RADIUS * 1.9;
  const target = new THREE.Vector3(
    THREE.MathUtils.clamp(cur.x, -halfW + margin, halfW - margin),
    THREE.MathUtils.clamp(cur.y, -halfH + margin, halfH - margin),
    0
  );

  handle.settleFromPos = cur.clone();
  handle.settleFromQuat = handle.baseMesh.quaternion.clone();
  handle.settleTarget = target;
  handle.settleTo = new THREE.Quaternion().setFromUnitVectors(face.normal, handle.toCamera);
  handle.settleStart = performance.now();
  handle.onSettled = onSettled || null;
  handle.mode = "settling";
  setOverlayOpacity(handle, 1);
}

function setOverlayOpacity(handle, value) {
  const el = handle.containerEl;
  if (!el) return;
  el.style.transition = "opacity 0.7s ease";
  el.style.opacity = String(value);
}

// scheduleRestFade dims the arena a few seconds after the die settles so
// a rested die never permanently sits on top of the conversation. The
// next startTumble restores full opacity.
function scheduleRestFade(handle) {
  if (handle.fadeTimer) clearTimeout(handle.fadeTimer);
  handle.fadeTimer = setTimeout(() => {
    handle.fadeTimer = null;
    setOverlayOpacity(handle, REST_OPACITY);
  }, REST_FADE_DELAY_MS);
}

// applyDiceSkin re-skins an already-mounted die in place (new base
// color/texture, regenerated number atlas) without rebuilding geometry
// or disturbing physics state.
function applyDiceSkinTo(handle, skinId) {
  const skin = SKINS.find((s) => s.id === skinId) || SKINS.find((s) => s.id === DEFAULT_SKIN_ID);
  handle.skin = skin;

  const oldBaseMap = handle.baseMesh.material.map;
  handle.baseMesh.material.dispose();
  if (oldBaseMap) oldBaseMap.dispose();
  handle.baseMesh.material = buildBaseMaterial(skin);

  const newMaterials = buildNumberMaterials(skin, handle.faces);
  handle.numberMaterials.forEach((mat, i) => {
    if (mat.map) mat.map.dispose();
    mat.dispose();
    handle.baseMesh.children[i].material = newMaterials[i];
  });
  handle.numberMaterials = newMaterials;
}

// --- Skin persistence (localStorage) ---

const DICE_SKIN_STORAGE_KEY = "layforge.diceSkin";

function saveDiceSkin(skinId) {
  try {
    localStorage.setItem(DICE_SKIN_STORAGE_KEY, skinId);
  } catch {
    // Best-effort — a private-browsing/storage-disabled session just
    // won't remember the choice across reloads.
  }
}

function loadSavedDiceSkin(fallback) {
  try {
    return localStorage.getItem(DICE_SKIN_STORAGE_KEY) || fallback;
  } catch {
    return fallback;
  }
}

window.Dice = {
  mountDie,
  startTumble,
  settleOnResult,
  resizeArena,
  setBubbleColliders,
  applyDiceSkin: applyDiceSkinTo,
  saveDiceSkin,
  loadSavedDiceSkin,
  listSkins: () => SKINS.map((s) => ({ id: s.id, label: s.label })),
  DEFAULT_SKIN_ID,
};
