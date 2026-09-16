# vendor

Vendored third-party assets `../dice3d.js` imports/loads as plain files —
no npm, no bundler, matching this client's "no build step" contract (see
`../README.md`). Copied verbatim from upstream, license files included
alongside each.

- `three.module.min.js` — [three.js](https://github.com/mrdoob/three.js)
  r0.160.0 (MIT), from `unpkg.com/three@0.160.0/build/three.module.min.js`.
  WebGL scene/camera/renderer, materials, and lighting for each die.
- `cannon-es.js` — [cannon-es](https://github.com/pmndrs/cannon-es)
  0.20.0 (MIT), from `unpkg.com/cannon-es@0.20.0/dist/cannon-es.js`.
  **No longer imported by `dice3d.js`** as of the dice-theme-assets
  rework (see below) — the operator explicitly doesn't need dice to
  physically tumble/bounce, only to spin in place, so the physics-driven
  contained-tumble code (and this import) was deleted from `dice3d.js`.
  Left vendored here rather than deleted outright, in case a future
  feature wants real physics again; confirmed via grep that nothing else
  in `master/web/` imports it either, before leaving it in this
  half-orphaned state.
- `dice-themes/` — mesh + texture assets from
  [3d-dice/dice-themes](https://github.com/3d-dice/dice-themes) (MIT,
  copyright "3D Dice"; `dice-themes/LICENSE` here is that repo's own
  license file, copied verbatim), commit-pinned to whatever `main` served
  on 2026-09-15 (the repo doesn't tag releases). Real Blender-made dice
  models + textures, decoupled from that org's Babylon.js/Ammo.js runtime
  (`dice-box`) — this project takes only the static mesh/texture assets
  and drives them itself with the three.js stack above. Two subfolders:
  - `dice-themes/default/default.json` — the "default" theme's mesh
    document (Babylon-JSON: flat positions/normals/uvs/indices arrays per
    die shape, plus a `colliderFaceMap` giving each face's printed value —
    see `dice3d.js`'s face-orientation math for how that's used). Vendored
    under `default/` rather than `rust/` because the **rust** theme's own
    `theme.config.json` carries no `meshFile` field at all — confirmed
    against the upstream repo's actual file listing, not assumed — since
    upstream reuses the base theme's geometry for every recolor rather
    than shipping duplicate geometry per palette. `dice3d.js` falls back
    to this file when a theme's config doesn't name its own mesh.
  - `dice-themes/rust/` — the "rust" theme actually shipped today:
    `theme.config.json`, `diffuse-light.png` (a mostly-transparent
    white-ink-on-nothing mask meant to be recolored — see `dice3d.js`'s
    `composeDiffuseTexture` for why it's composited onto a solid base
    color rather than alpha-blended live), and `normal.png` (downscaled
    from the upstream 1024×1024 to 256×256 before vendoring — the
    original was 726KB and this project cares about staying lightweight;
    a die renders at 56 logical px on screen, so the extra resolution
    bought nothing visible). `rust/specular.jpg` was **not** vendored —
    this theme's material doesn't use a specular/roughness map, a flat
    roughness value looks fine at this render size — see `dice3d.js` for
    why `default` and `gemstoneMarble` weren't shipped at all (a theme
    comparison, not an oversight).

This is three.js's (and formerly cannon-es's) second tour of duty here —
they originally drove a standalone WebGL dice tray, removed earlier in
this project's life when dice moved into chat-log message bubbles (`git
show 2f4f3cc~1:master/web/dice.js` if you want to see that earlier
shape). The bubbles' own SVG/CSS dice that briefly replaced them didn't
look as good, so `dice3d.js` brought real WebGL rendering back — first
with hand-built primitive geometry, then (this rework) with real
artist-made mesh/texture assets for the material realism primitives
couldn't match. See `dice3d.js`'s own top-of-file comment for the full
story, including why it never gives a die a permanent GPU context.

To update three.js: re-download the same URL pattern with a newer
version number, and update this file's version number. It's a small,
stable library; there's no expectation of frequent updates. To update the
dice-theme assets: re-fetch the same paths from
`https://raw.githubusercontent.com/3d-dice/dice-themes/main/themes/...`
and update this entry with whatever date/commit you pulled.
