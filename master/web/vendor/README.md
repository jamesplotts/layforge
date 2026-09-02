# vendor

Vendored third-party JS libraries the dice tray (`../dice.js`) imports as
plain ES modules — no npm, no bundler, matching this client's "no build
step" contract (see `../README.md`). Copied verbatim from upstream,
license files included alongside each.

- `three.module.min.js` — [three.js](https://github.com/mrdoob/three.js)
  r0.160.0 (MIT), from `unpkg.com/three@0.160.0/build/three.module.min.js`.
  WebGL scene/camera/renderer, the die's real icosahedron geometry
  (`THREE.IcosahedronGeometry`), materials, lighting, and the canvas-based
  number textures.
- `cannon-es.js` — [cannon-es](https://github.com/pmndrs/cannon-es)
  0.20.0 (MIT), from `unpkg.com/cannon-es@0.20.0/dist/cannon-es.js`.
  Physics (gravity/collision/damping) driving the die's tumble — cosmetic
  only, same as everything else about the roll's *appearance*: the
  authoritative result always comes from Master (design doc §3.1, §4),
  physics never determines it, only how the die visually gets there.

To update either: re-download the same URL pattern with a newer version
number, and update this file's version numbers. Both are small, stable
libraries; there's no expectation of frequent updates.
