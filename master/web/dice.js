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

// --- Physics ---

function buildPhysicsWorld() {
  const world = new CANNON.World({ gravity: new CANNON.Vec3(0, -30, 0) });
  const dieMaterial = new CANNON.Material("die");
  const trayMaterial = new CANNON.Material("tray");
  world.addContactMaterial(new CANNON.ContactMaterial(dieMaterial, trayMaterial, { friction: 0.4, restitution: 0.45 }));

  const wallExtent = 2.1;
  const halfWall = 3;
  const addStaticPlane = (position, rotationAxis, rotationAngle) => {
    const body = new CANNON.Body({ mass: 0, material: trayMaterial, shape: new CANNON.Plane() });
    body.position.copy(position);
    body.quaternion.setFromAxisAngle(rotationAxis, rotationAngle);
    world.addBody(body);
  };
  addStaticPlane(new CANNON.Vec3(0, -wallExtent, 0), new CANNON.Vec3(1, 0, 0), -Math.PI / 2); // floor
  addStaticPlane(new CANNON.Vec3(0, wallExtent, 0), new CANNON.Vec3(1, 0, 0), Math.PI / 2); // ceiling
  addStaticPlane(new CANNON.Vec3(-wallExtent, 0, 0), new CANNON.Vec3(0, 1, 0), Math.PI / 2); // left
  addStaticPlane(new CANNON.Vec3(wallExtent, 0, 0), new CANNON.Vec3(0, 1, 0), -Math.PI / 2); // right
  addStaticPlane(new CANNON.Vec3(0, 0, -halfWall), new CANNON.Vec3(0, 1, 0), 0); // back
  addStaticPlane(new CANNON.Vec3(0, 0, halfWall), new CANNON.Vec3(0, 1, 0), Math.PI); // front

  const dieBody = new CANNON.Body({
    mass: 1,
    material: dieMaterial,
    shape: new CANNON.Sphere(DIE_RADIUS), // physics uses a sphere approximation — see vendor/README.md.
    linearDamping: 0.15,
    angularDamping: 0.2,
  });
  world.addBody(dieBody);

  return { world, dieBody };
}

// --- Public API ---
//
// mountDie owns everything about one die instance: renderer, scene,
// physics world, the mesh and its per-face number planes. Everything
// else (startTumble/settleOnResult/reskin) takes the handle it returns.
function mountDie(containerEl, skinId) {
  const width = containerEl.clientWidth || 110;
  const height = containerEl.clientHeight || 110;

  const scene = new THREE.Scene();
  const camera = new THREE.PerspectiveCamera(32, width / height, 0.1, 100);
  camera.position.set(0, 0.6, 5.4);
  camera.lookAt(0, 0, 0);

  const renderer = new THREE.WebGLRenderer({ antialias: true, alpha: true });
  renderer.setPixelRatio(Math.min(window.devicePixelRatio || 1, 2));
  renderer.setSize(width, height);
  renderer.setClearColor(0x000000, 0);
  containerEl.appendChild(renderer.domElement);

  scene.add(new THREE.AmbientLight(0xffffff, 0.55));
  const key = new THREE.DirectionalLight(0xfff4e0, 1.1);
  key.position.set(2.5, 3.5, 3);
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
  const numberPlanes = faces.map((face, i) => {
    const plane = new THREE.Mesh(numberPlaneGeometry, numberMaterials[i]);
    plane.position.copy(face.centroid).multiplyScalar(1.015);
    plane.quaternion.setFromUnitVectors(new THREE.Vector3(0, 0, 1), face.normal);
    baseMesh.add(plane);
    return plane;
  });

  // A pleasant resting tilt so the die doesn't look flat/dead pre-roll.
  baseMesh.quaternion.setFromEuler(new THREE.Euler(0.4, 0.5, 0));

  const { world, dieBody } = buildPhysicsWorld();
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
    world,
    dieBody,
    toCamera,
    mode: "idle", // "idle" | "tumbling" | "settling"
    settleFrom: null,
    settleTo: null,
    settleStart: 0,
    onSettled: null,
    lastFrameTime: null,
  };

  requestAnimationFrame((t) => renderLoop(handle, t));
  return handle;
}

const SETTLE_DURATION_MS = 380;

function renderLoop(handle, time) {
  requestAnimationFrame((t) => renderLoop(handle, t));

  const dt = handle.lastFrameTime === null ? 1 / 60 : Math.min((time - handle.lastFrameTime) / 1000, 1 / 20);
  handle.lastFrameTime = time;

  if (handle.mode === "tumbling") {
    handle.world.step(1 / 60, dt, 5);
    handle.baseMesh.position.set(handle.dieBody.position.x, handle.dieBody.position.y, handle.dieBody.position.z);
    handle.baseMesh.quaternion.set(
      handle.dieBody.quaternion.x,
      handle.dieBody.quaternion.y,
      handle.dieBody.quaternion.z,
      handle.dieBody.quaternion.w
    );
  } else if (handle.mode === "settling") {
    const t = Math.min((time - handle.settleStart) / SETTLE_DURATION_MS, 1);
    const eased = 1 - Math.pow(1 - t, 3); // ease-out cubic — a brisk, decisive "snap," not a lazy drift.
    handle.baseMesh.position.lerp(new THREE.Vector3(0, 0, 0), eased);
    handle.baseMesh.quaternion.slerpQuaternions(handle.settleFrom, handle.settleTo, eased);
    if (t >= 1) {
      handle.mode = "idle";
      const cb = handle.onSettled;
      handle.onSettled = null;
      if (cb) cb();
    }
  }

  handle.renderer.render(handle.scene, handle.camera);
}

// startTumble drops the die back into the physics tray with random
// velocity/spin — call on roll.request. Safe to call again mid-flight
// (e.g. a re-roll) since it just resets the body's state.
function startTumble(handle) {
  const body = handle.dieBody;
  body.position.set((Math.random() - 0.5) * 0.6, 1.6, (Math.random() - 0.5) * 0.6);
  body.velocity.set((Math.random() - 0.5) * 4, -2, (Math.random() - 0.5) * 4);
  body.angularVelocity.set((Math.random() - 0.5) * 18, (Math.random() - 0.5) * 18, (Math.random() - 0.5) * 18);
  body.quaternion.setFromEuler(Math.random() * Math.PI, Math.random() * Math.PI, Math.random() * Math.PI);
  handle.mode = "tumbling";
}

// settleOnResult stops physics and slerps the die to precisely display
// resultNumber's face toward the camera — call on roll.result. If
// startTumble was never called (roll.result with no preceding
// roll.request), it still settles cleanly from the die's current pose.
function settleOnResult(handle, resultNumber, onSettled) {
  const face = handle.faces[((resultNumber - 1) % handle.faces.length + handle.faces.length) % handle.faces.length];
  handle.settleFrom = handle.baseMesh.quaternion.clone();
  handle.settleTo = new THREE.Quaternion().setFromUnitVectors(face.normal, handle.toCamera);
  handle.settleStart = performance.now();
  handle.onSettled = onSettled || null;
  handle.mode = "settling";
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
  applyDiceSkin: applyDiceSkinTo,
  saveDiceSkin,
  loadSavedDiceSkin,
  listSkins: () => SKINS.map((s) => ({ id: s.id, label: s.label })),
  DEFAULT_SKIN_ID,
};
