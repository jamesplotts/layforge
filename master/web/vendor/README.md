# vendor

Vendored third-party assets `../dice-arena.js` imports/loads as plain
files — no npm, no bundler, matching this client's "no build step"
contract (see `../README.md`). Copied verbatim from upstream, license
files included alongside each.

- `dice-box-threejs.es.js` —
  [@3d-dice/dice-box-threejs](https://github.com/3d-dice/dice-box-threejs)
  0.0.12 (MIT, copyright "3D Dice"; `dice-box-threejs-LICENSE` here is
  that package's own `LICENSE` file, copied verbatim), from
  `registry.npmjs.org/@3d-dice/dice-box-threejs/-/dice-box-threejs-0.0.12.tgz`'s
  own `dist/dice-box-threejs.es.js`. A single ~700KB self-contained ES
  module — three.js r143 and [cannon-es](https://github.com/pmndrs/cannon-es)
  are bundled *inline* by this package's own build (confirmed by reading
  the file: it carries three.js's own `@license` header partway through,
  and `export default` is the one `DiceBox` class, nothing else), so
  vendoring is exactly one file plus its license, no separate three.js/
  cannon-es copies needed the way the previous `dice3d.js` integration
  required (see below for what that replaced). Drives a real thrown-
  physics dice tray: `new DiceBox("#dice-arena", options)`, `.initialize()`
  once, then `.roll(notation)`/`.add(notation)` with `"NdSIDES@v1,v2,..."`
  notation to force predetermined results — see `../dice-arena.js` for
  how this project drives it and works around a couple of gaps in its
  public surface (no supported throw-from-a-specific-pixel origin; a
  `this.rolling` internal flag that clears the whole tray if a second
  `add()`/`roll()` lands before the previous one's animation finishes,
  worked around by serializing calls rather than trusting the library's
  own "safe to call concurrently" framing at face value — confirmed by
  reading `startClickThrow`/`clearDice`, not assumed).
  Die-face numbers are drawn at runtime via Canvas 2D onto procedural
  per-shape geometry, not a baked texture atlas, and colors come from
  config (`theme_colorset`), so — unlike the mesh+texture assets this
  replaces — no separate binary asset files are needed at all. The
  package's own `public/textures/*.webp` (optional surface-pattern
  skins) and `public/sounds/*.mp3` are not vendored; this project doesn't
  use `theme_texture` and passes `sounds: false`.

  To update: re-run `curl -sL
  registry.npmjs.org/@3d-dice/dice-box-threejs` to find the current
  `dist-tags.latest` and that version's tarball URL, download and
  extract it, copy `dist/dice-box-threejs.es.js` and `LICENSE` over
  these two files, and update the version number above.

## Superseded (removed)

This project has vendored three.js (and, briefly, cannon-es) twice
before this file's current entry, each time for a different dice-
rendering approach; removed outright rather than left as unused dead
weight each time a rework retired the code that used them — `git log`
on this file and on `../dice3d.js` (`git show
<commit-before-removal>:master/web/dice3d.js`) has every prior
generation if one is ever needed again:

- **`three.module.min.js`** (three.js r0.160.0, from
  `unpkg.com/three@0.160.0/build/three.module.min.js`) and
  **`cannon-es.js`** (cannon-es 0.20.0, from
  `unpkg.com/cannon-es@0.20.0/dist/cannon-es.js`) drove `dice3d.js`'s
  per-die-bubble WebGL rendering — first hand-built primitive geometry
  physically tumbled via cannon-es, then (cannon-es dropped at that
  point, left vendored but unused) real artist-made mesh/texture assets
  spun in place with a scripted animation instead of real physics. Both
  files are gone now that `dice3d.js` itself is gone, replaced by the
  single shared physics arena described above — this is three.js's
  *third* tour of duty in this project (it originally drove a
  standalone WebGL dice tray before dice moved into chat-log bubbles at
  all — see `git show 2f4f3cc~1:master/web/dice.js`), and each previous
  copy was removed rather than accumulated once its call site stopped
  using it.
- **`dice-themes/`** — mesh + texture assets from
  [3d-dice/dice-themes](https://github.com/3d-dice/dice-themes) (MIT,
  "3D Dice"), the real Blender-made die models/textures `dice3d.js`
  drove with the three.js stack above. `dice-box-threejs`'s own
  procedural Canvas-2D face rendering (see above) needs no equivalent
  asset, so this wasn't replaced with anything — it's just gone.
