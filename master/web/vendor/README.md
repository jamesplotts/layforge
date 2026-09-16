# vendor

Vendored third-party JS libraries `../dice3d.js` imports as plain ES
modules — no npm, no bundler, matching this client's "no build step"
contract (see `../README.md`). Copied verbatim from upstream, license
files included alongside each.

- `three.module.min.js` — [three.js](https://github.com/mrdoob/three.js)
  r0.160.0 (MIT), from `unpkg.com/three@0.160.0/build/three.module.min.js`.
  WebGL scene/camera/renderer, each die's real primitive geometry
  (tetrahedron/box/octahedron/icosahedron, plus a hand-built pentagonal
  trapezohedron for d10), materials, and lighting.
- `cannon-es.js` — [cannon-es](https://github.com/pmndrs/cannon-es)
  0.20.0 (MIT), from `unpkg.com/cannon-es@0.20.0/dist/cannon-es.js`.
  Physics (gravity/collision/damping) driving each die's tumble inside
  its own small contained arena — cosmetic only, same as everything else
  about a roll's *appearance*: the authoritative result always comes
  from Master (design doc §3.1, §4), physics never determines it, only
  how the die visually gets there.

This is these two libraries' second tour of duty here — they originally
drove a standalone WebGL dice tray, removed earlier in this project's
life when dice moved into chat-log message bubbles (`git show
2f4f3cc~1:master/web/dice.js` if you want to see that earlier shape).
The bubbles' own SVG/CSS dice that briefly replaced them didn't look as
good, so `dice3d.js` brought the real WebGL rendering back — adapted to
render *inside* each small per-die bubble slot instead of a dedicated
tray (see that file's own top-of-file comment for the full story,
including why it never gives a die a permanent GPU context).

To update either: re-download the same URL pattern with a newer version
number, and update this file's version numbers. Both are small, stable
libraries; there's no expectation of frequent updates.
