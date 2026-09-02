# V1 Web Client

The default Slave client (design doc §4): no LLM credentials, no rules
engine, no local game logic — renders what Master sends over the
protocol. Plain HTML/CSS/JS, no build step, matching the protocol's own
"devtools-readable" ethos (design doc §6) — the dice tray's two
dependencies (`vendor/`, see `vendor/README.md`) are plain ES modules
loaded via `<script type="module">`, not an npm/bundler toolchain.

Lives under `master/` and is served by Master itself by default (see
`../README.md`'s Running section) — plain files on disk, not embedded
into the Go binary, specifically so a table can restyle the interface
(swap this directory's `style.css`, fork `index.html`/`app.js`) without
touching Go or rebuilding anything. That's a packaging/distribution
choice, not a coupling one: this stays a normal protocol client, with no
special access to Master beyond what any other client connecting to
`/ws` has (design doc §4 — "third-party clients are legitimate
first-class consumers, not an afterthought").

## Status

Working: join a campaign (`system.connect` handshake), narrative
scrollback with the fast-pass narrative-transform pipeline
(`narrative.player_input` → `narrative.player_bubble`), the safety-flag
control (`safety.flag` → `safety.flag_broadcast`), history: on join,
the most recent page loads automatically (design doc §10's tail default,
not the campaign's first message), with a "Load earlier history" button
to page further back (`log.history_request`'s `before_sequence`); and now
a dice tray — a real WebGL d20 (`dice.js`, three.js's own
`IcosahedronGeometry` — a proper shared-vertex mesh, not hand-rolled
transforms) that physically tumbles (cannon-es) then settles on the
authoritative face once `roll.check_request` gets back
`roll.request`/`roll.result` from Master. The physics is purely cosmetic,
same as the tumble itself — the settle is always forced to the server's
actual result, never determined by the simulation. Since there's no real
character-creation UI yet, `onJoined` silently uploads a minimal stock
character (`character.upload`) so the roll has something to roll for —
see the stopgap note at the top of `app.js`.

There's also now a read-only character sheet (`character-sheet.js`),
rendered generically from whatever `json_schema`
`character.schema_response` publishes — walking `properties`/`items`/
`$ref` recursively, not a hardcoded D&D field list — populated with a
`character.get` for the sheet the client already knows it owns
(`state.rollCharacterId`, the same character the dice tray rolls for).
Alongside it, a small Take Damage/Heal control sends
`character.apply_effect` (`onApplyEffectClick` in `app.js`) and
re-renders the sheet from the response — the sheet display itself stays
read-only (no per-field editing), this is a separate, narrower mutation
path.

Not implemented: schema-driven sheet *editing* (still view-only, no
per-field form submission), effects tied automatically to a check
result (a hit doesn't apply its own damage — the tray's Roll Check and
the sheet's Take Damage/Heal are two independent actions today), push-
to-talk voice input (no audio pipeline).

## Running

From `master/`:

```
go run . -llm-url=http://<ollama-host>:11434
```

then open `http://localhost:8080/` — Master serves this directory at `/`
by default. To point a copy of this client at a *different* Master
instead (e.g. a remote one, or while comparing behavior across builds),
open `index.html` directly and enter that Master's WebSocket URL on the
join screen (e.g. `wss://some-other-host/ws`).

## Skinning

Because this is served straight from disk, restyling doesn't require
Go, a rebuild, or even a restart:

- **Colors/spacing/fonts** — edit `style.css`. Everything's driven off
  the CSS custom properties at the top of the file (`--bg`, `--accent`,
  `--bubble-bg`, etc.) — change those first before touching individual
  rules.
- **Layout/markup** — edit `index.html`.
- **Behavior** — edit `app.js`; it's plain functions and DOM calls, no
  framework or build tooling to fight.

### Dice skins

The dice tray takes this further: a die's *material* is entirely
data-driven from `dice-skins.js` — the only file a community skin needs
to touch (plus optional PNGs alongside it), never `dice.js` itself. Each
entry is:

```js
{
  id: "mint",
  label: "Mint",
  baseColor: "#bdead9",       // fallback/base material color
  baseTexture: null,          // optional PNG: marble, wood grain, metal, ...
  numberTexture: null,        // optional PNG: a 5x4 grid of hand-drawn digits 1-20
  font: "700 72px Georgia, serif", // used only when numberTexture is null
  numberColor: "#204030",     // used only when numberTexture is null
}
```

The skin `<select>` on the join screen is populated from this list at
load (`Dice.listSkins()`), so a new entry shows up with no `index.html`
change either. Full field reference is the comment at the top of
`dice-skins.js`.

**Honest limitation:** the three built-in skins (ivory, obsidian,
emerald) only exercise the color+font path — this repo has no way to
author actual PNG texture art, so `baseTexture`/`numberTexture` are
fully implemented (loaded via `THREE.TextureLoader`, applied as a
standard material map / atlas UV window) but untested against real
community art. `baseTexture` also isn't unwrapped per-face — it's a
single UV map across the whole mesh, so expect some stretching; that's
an acceptable v1 tradeoff for a general material look, not a promise of
per-facet precision.

Master doesn't cache these files beyond what `http.FileServer` does — a
browser reload picks up the change immediately. To run a genuinely
different skin rather than editing in place, copy this whole directory
and point `-web-dir` at the copy instead.

## Known limitations

- **Scroll position isn't preserved across "Load earlier."** New content
  gets prepended above what's visible, but the client doesn't adjust
  scroll offset to compensate — the viewport can jump. A real
  implementation would anchor scroll to the insertion point.
- **No reconnect.** If the WebSocket drops, the status line says
  "disconnected" and that's it — reload to rejoin.
- **`sender_id` is just the character name you type in.** There's no
  auth/account system yet (design doc §6.6's Discord OAuth isn't
  implemented), so nothing stops two people from joining as the same
  name.
- Hand-written, not generated. Design doc §6 envisions a proper JS
  reference SDK against `protocol/asyncapi.yaml`; this predates that and
  has to be kept in sync with the protocol by hand (see
  `PROTOCOL_VERSION` in `app.js`).
- **The stock character upload is a stopgap, not real character
  creation.** Every join silently creates a fresh minimal character
  (uniform 12s, 10 HP) with no way to customize ability scores, equipment,
  or anything else — there's no schema-driven character *creation* UI
  yet (only a read-only schema-driven *viewer*, `character-sheet.js`).
  It also means every rejoin makes Master store a *new* character record
  rather than reusing one.
- **The character sheet is read-only** — no write-back, no editing;
  `character-sheet.js` only walks `properties`/`items`/`$ref`, not the
  full JSON Schema spec (no `oneOf`/`anyOf`/`patternProperties`/etc.),
  since nothing today's schema uses needs more than that.
- **The d20's face numbering is synthetic**, not a claim to reproduce any
  particular physical die's layout (opposite faces on a real d20 sum to
  21; this one doesn't) — see `extractFaces`'s doc comment in `dice.js`.
- **The die only shows results from checks (d20).** Nothing here rolls
  damage dice or any non-d20 shape yet — `roll.check_request` only
  triggers `ResolveCheck`, not `ApplyEffect`.
- **Physics uses a sphere collider, not the true icosahedron shape** —
  cannon-es doesn't need face-accurate collision for a small cosmetic
  tumble to read as believable, and a sphere is dramatically simpler/more
  stable than convex-polyhedron collision. The *visible* mesh is a real
  icosahedron regardless; only the invisible physics body is approximate.
