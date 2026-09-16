# V1 Web Client

The default Slave client (design doc §4): no LLM credentials, no rules
engine, no local game logic — renders what Master sends over the
protocol. Plain HTML/CSS/JS, no build step, matching the protocol's own
"devtools-readable" ethos (design doc §6) — dice rolls render as plain
SVG/CSS in the chat log itself (see "Status" below), so this client has
no third-party dependency at all today; `vendor/` (see `vendor/README.md`)
is kept empty for whatever needs one next.

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
an interactive die roll rendered right in the chat log: a resolved check
(`roll.check_request`, or a DM-triggered `resolve_check`, or an automatic
death save) arrives as `client.roll` — a clickable SVG-outline die bubble
whose result was already decided server-side and stays hidden until the
roller clicks it, playing a short CSS tumble (`@keyframes roll-tumble`)
before showing the number and a labeled breakdown line ("Charisma
(Persuasion) Check: 13 -1 = 12"). Everyone else at the table sees the
same bubble as a greyed, non-interactive ghost (`client.roll_spectate`,
no result on the wire — an anti-metagaming gate, design doc §9.7) that
fills in the instant the roller reveals it (`client.roll_spectate_reveal`)
— see `app.js`'s "client.roll* interactive dice" section. Joining now
goes straight into a real character-creation flow instead of a silent
stock-character upload — see the new section below.

There's also now a read-only character sheet (`character-sheet.js`),
rendered generically from whatever `json_schema`
`character.schema_response` publishes — walking `properties`/`items`/
`$ref` recursively, not a hardcoded D&D field list — populated with a
`character.get` for the sheet the client already knows it owns
(`state.rollCharacterId`, the same character `client.roll` rolls for).
It lives in a fixed-width sidebar alongside the chat log (`.chat-body`'s
two-column layout in `style.css`; flip `.character-sidebar`'s side with
one CSS edit, no markup or `app.js` change needed) rather than an
in-flow collapsible panel, and its contents are tabbed
(`renderCharacterSheetTabs`): every top-level schema property with real
object/array structure of its own (ability scores, hit points, combat
stats, inventory, ...) gets its own tab, everything else (id, name,
team, a scalar resistances list) lands on an always-present Overview
tab — a structural test on each property's own shape
(`isGroupableProperty`), not a hardcoded "Stats/Abilities/Spells/
Inventory" tab list, so a schema this has never seen (OpenCombatEngine's
current one already includes a `spellcasting` block, which just becomes
a "Spellcasting" tab with no code change) still tabs sensibly. Switching
tabs is local UI state, not a re-fetch; the currently-selected tab
survives a re-render (e.g. after Take Damage/Heal) via
`panelsEl.dataset.activeTab`, so acting on a character doesn't bounce
the sidebar back to Overview. Alongside it, a small Take Damage/Heal
control sends `character.apply_effect` (`onApplyEffectClick` in
`app.js`) and re-renders the sheet from the response — the sheet display
itself stays read-only (no per-field editing), this is a separate,
narrower mutation path.

There's also now the DM's side of the conversation (design doc §7's
slow pass, §8's tool-use): after a `narrative.player_bubble` renders,
Master's LLM narrates a DM/NPC reaction — rendered as a visually
distinct `dm-bubble` — and along the way can call real tools
(`resolve_check`, `apply_effect`, `get_character_status`) against the
character's actual mechanical state. Every tool call is broadcast as
`tool.result` and rendered as a small note in the log (🎲 on success, ⚠
plus a reason code on failure) regardless of outcome, so a table can see
what the DM actually did, not just read the prose that followed. A
DM-triggered `resolve_check` reuses the same `client.roll` family a
player-initiated `roll.check_request` uses, so it renders as the same
interactive die bubble either way. The label ("Strength Check",
"Charisma (Persuasion) Check", "Dexterity Saving Throw", "Death Save")
is composed server-side (`checkLabel` in Master) from the actual check
type/ability/skill Master resolved, not guessed client-side — correct
for anyone's roll, including a DM-triggered one (initiative,
resolve_check) that never came from this client's own input at all.

There's also now a `turn.state` note (design doc §3.1, §9.3) — whenever
the DM starts, advances, or ends structured combat, a "⚔ Round N —
{name}'s turn" (or "⚔ Combat ends") line appears in the log, resolving
the current character's name the same way narrative bubbles do. Once
combat is active, an out-of-turn Roll Check or Take Damage/Heal click
now gets genuinely rejected server-side — it just surfaces as an
ordinary error note (`appendErrorNote`), the same as any other
rejection reason (e.g. not owning the character); there's no dedicated
"it's not your turn" UI treatment, and the Roll Check/Take Damage/Heal
buttons aren't disabled out of turn — a player can still click them,
they just get told no.

There's also now `client.image` rendering — a DM-generated illustration
(design doc §6.3) appears inline in the log as a bordered `<figure>`
with the image and its caption (or, failing that, the generator prompt)
underneath. Verified live against a real, running self-hosted ComfyUI
instance: a real generated image loads correctly via its `/view`
endpoint like any other `<img src>`, with no code changes needed after
the fact.

There's also now a real character-creation flow (design doc §9.4) in
place of the old stopgap stock-character upload. `onJoined` sends
`character.creation_start` instead, and Master then drives the whole
conversation with the generic `client.*` prompt family (design doc §4):
`client.choice` (a prompt + one button per option, each option a
`{value, label}` pair) and `client.query` (a prompt + textarea + Send,
used for pasting JSON and any free-text engine question). Each renders
as an ordinary-looking chat bubble (`onClientChoice` / `onClientQuery`
in `app.js`, sharing `clientPromptWrap`). Clicking a choice sends
`client.choice_response { prompt_id, value }`; submitting text sends
`client.query_response { prompt_id, text }`; the bubble dims and
disables itself (`.answered`) once answered, and the next prompt appends
below it — no separate wizard screen, no modal, the whole exchange just
reads as a sequence of chat messages the player answers at their own
pace.
The very first prompt offers four paths: **import** (falls straight
into the existing paste-JSON flow, now entered this way instead of a
dedicated screen — its own free-text prompt sets
`accepts_file_upload`, which adds a real file picker next to the
textarea, `FileReader`-ing a chosen file straight into it so a player
can bring in a saved character file instead of copy-pasting; still the
same `client.query_response` on the wire either way), **quick_roll**
(three real questions — race, class,
gender — then everything else, including a spellcaster's cantrips/
prepared spells/slots, is rolled up automatically), **detailed_roll**
(every choice surfaced, including manual ability-score assignment and
picking spells one at a time), and **pregen** (claim one of the Host's
pregenerated characters — see the admin-web README for authoring one).
Whichever path is taken, completion is signalled the same way
`character.upload`'s own successful import already was
(`character.validation_result`) — the existing completion handling
(`state.rollCharacterId`, schema fetch) fires exactly as before, just
from a new caller.

Live-verified against a real sidecar + Master + browser: joined fresh,
worked through a full quick-roll (Human Wizard, race/class/gender only)
in the actual chat log, and confirmed the finished character sheet's
Spellcasting tab held real, engine-generated cantrips and prepared
spells the player never explicitly chose — not placeholder data. Also
verified separately (Go WS driver, both repos' real live processes, no
browser needed for these): a full detailed roll through every prompt
including manual ability-score assignment and spell picks; the rolled
Cleric's real generated spellcasting data actually casting a prepared
spell and a cantrip via the System Engine's `cast_spell` RPC, with an
unprepared spell correctly rejected by the engine's own gate; two
players joining the same campaign and detailed-rolling *concurrently*
without ever seeing each other's prompts or answers; a pregen authored
through the real admin panel claimed independently by two different
players, producing two distinct characters and leaving the template
untouched; and the import sub-flow entered via the new top-level prompt
instead of a dedicated screen.

The Host/DM-AI import veto above is separately live-verified end to
end, including in a real browser: picked a real local JSON file
through the new file input, watched it populate the textarea, sent it,
and watched a genuine real-model review note ("Your character was
approved: ability scores... within normal creation range...") appear
in the log live once the automatic review pass concluded.

An imported character isn't necessarily playable the moment it's saved,
though (design doc §9.4's review flow, `internal/server/character_review.go`):
a deterministic campaign level-range check and then, if configured, the
DM AI's own balance judgment run automatically, and the Host can
approve or reject (or override either outcome) from the admin panel at
any time. Either way, the outcome arrives as its own
`character.review_result` — rendered as a plain log note
(`appendCharacterReviewNote`) distinct from the ordinary
`character.validation_result` completion note, since "your upload
parsed" and "a review of it just concluded" are genuinely different
events that can land seconds or minutes apart. A character that isn't
yet `Approved` still shows its own sheet (`character.get` always
works) but gets a real rejection from Roll Check and the sheet's Take
Damage/Heal — not a silent no-op.

Not implemented: schema-driven sheet *editing* (still view-only, no
per-field form submission), effects tied automatically to a *player's
own* check result (a hit doesn't apply its own damage outside the DM
slow pass — Roll Check and the sheet's Take Damage/Heal are two
independent actions for a player), and any client-side way to
restart character creation after a rejection — the player sees the
note but has to rejoin to try again.

Push-to-talk (design doc §4) is now wired: a hold-to-talk mic button
next to the chat input, feature-detected (hidden if the browser has no
`MediaRecorder`/`getUserMedia`), streams `audio.chunk` while held. Now
also includes a genuine live-updating preview: Master periodically
re-transcribes the growing recording while the button is still held and
this client shows each result in `input-text` as it arrives
(`onAudioTranscription`, same handler as the final result), with
keyboard focus moving into the box only once the true, complete result
arrives on release (`is_final: true`) — not on every partial, which
would yank focus every couple of seconds while the player is still
mid-recording. Never auto-sent, edit-before-send either way. See
`../README.md`'s Status section for the full design rationale and live
verification; this file just notes it exists.

The client now reconnects on an unplanned drop instead of just sitting
on "disconnected." `openSocket`/`scheduleReconnect` in `app.js` retry
with exponential backoff (1s, 2s, 4s… capped at 30s, indefinitely — a
dropped WebSocket is assumed recoverable, not something the player has
to notice and reload for), showing "reconnecting (attempt N)…" in the
status line. On success, `onReconnected` — not `onJoined` again, so the
one-time setup (stopgap character upload, revealing the chat screen)
doesn't repeat — re-fetches the most recent history page as a catch-up:
every message on the wire already carries a real, unique `message_id`
(design doc §5), so that page is de-duped against everything already
rendered and only genuinely-missed messages get appended. Verified
live: joined a real running Master, raised a safety flag, killed the
Master process (status correctly showed "reconnecting (attempt N)…"),
posted a second flag from a separate connection while the client was
still down, restarted Master, and confirmed the client reconnected on
its own with the first flag shown exactly once and the second — posted
entirely while disconnected — correctly appended, no console errors.

There's also now an interactive reveal for `detailed_roll` character
creation's 4d6-drop-lowest ability-score method — a regression fix for a
live player report: choosing that method used to blind-assign six
already-summed totals with zero visibility into what was actually
rolled. `client.ability_score_rolls` arrives once, carrying all six sets
of four already-decided dice; `onClientAbilityScoreRolls` renders them as
six sequential bubbles (`renderAbilityScoreRollSet`, reusing the
`client.roll` dice machinery above — `buildDieEl`/`settleDie`/
`dieOutlineSvg`, which already renders a d6 correctly via its existing
unlisted-size hexagon fallback), each with four ghost d6 the player
clicks to reveal (a purely local animation — no round trip per die,
since there's no spectator to hide a result from in a private,
single-player conversation, unlike combat's `client.roll`). Once a set's
four dice are all revealed it shows its own "5 + 3 + 2 = 10" line (the
dropped die shown dimmed and struck through via a new `.roll-die.dropped`
class, not hidden), and the next ability score's bubble appears. Once
the sixth set is revealed, the client sends
`client.ability_score_rolls_ack`, which is what actually advances
Master's relay to the real by-ability assignment questions — those are
now asked as "Assign which score to Strength?" (one button per remaining
rolled total) rather than the old "Assign the score 14 to which
ability?", closing the other half of the original report (no visibility
into the other five totals while assigning). `standard_array` shares the
same assignment phase, so it gets the same by-ability wording too.

The narrative input box's placeholder now personalizes once a character
is loaded — "What does Reorx do?" instead of the generic "What do you
do?" — set alongside the identity line in `renderCharacterIdentity`, and
reset back to the generic text when there's no character data yet (still
mid-creation, or a fresh page with nothing loaded).

The sidebar's "Equipment" tab is now labeled "Item Slots" (a
`FIELD_LABEL_OVERRIDES` entry in `character-sheet.js`, since a tab's
label is just `humanizeFieldName` on its schema property name) —
clearer next to Inventory, which it's a subset of (what's actively
worn/wielded/attuned, not everything carried). It's also now actually
useful rather than empty or full of meaningless numbers: see
OpenCombatEngine's own `EquippedSlotState.SlotName`/`.ItemName` and
`EquipmentState.AttunedItemNames` for the engine-side half of this fix —
this client needed no other changes, since the existing uniform-scalar-
array table renderer (see the Ability Scores/Skills entry above) already
renders the newly-readable data correctly once it's readable.

The dice bubbles' SVG icons briefly looked like the actual die shape
being rolled instead of a generic regular polygon — d4/d6/d8/d10/d20
each got their own faceted silhouette (`DIE_SHAPE_BUILDERS` in
`app.js`). **Superseded by the entry below** — SVG facets still didn't
look as good as this app's original WebGL dice tray, so the tray's real
rendering came back, just embedded per-die instead of in a separate
tray. This entry's real, still-true fix: a physical d10 is printed 0-9,
not 1-10 — a server result of 10 on a d10 shows as "0" on the revealed
face; every other size shows its literal number. That mapping carried
forward unchanged into the WebGL version below.

There's also now a real WebGL die in every roll bubble (`dice3d.js`,
new) — **superseded twice over, see the real-thrown-physics-dice entry
much further below** (real mesh/texture assets replaced the primitive
geometry, cannon-es physics is gone, and the DOM text overlay this
paragraph describes was later removed entirely; then `dice3d.js` itself
was retired outright in favor of `dice-arena.js`) — this
app's original dice tray (a standalone WebGL/physics d20,
removed earlier when dice moved into chat-log bubbles) is back, just
rendered *inside* each small per-die bubble slot instead of a dedicated
tray area, since the SVG dice that briefly replaced it didn't look as
good. Real three.js primitive geometry per size (tetrahedron/box/
octahedron/icosahedron, plus a hand-built pentagonal trapezohedron for
d10 — three.js has no built-in for that shape), a physically-lit
material, and a cannon-es-driven tumble — but contained entirely inside
each die's own small physics arena, never simulating a die bouncing
around the actual page. The revealed number stays a plain DOM text
overlay (`.roll-die-face`, unchanged) rather than being baked into the
3D mesh — deliberately, so the die can settle in any resting pose
without needing to solve "orient the correct labeled face toward the
camera" per shape, and so the number's legibility can never regress the
way the SVG dropped-die dimming once did (a live "1" misread as "4" from
compounding opacity — see `dice3d.js`'s number-overlay design comment).

A chat log can accumulate far more dice than the old tray ever held at
once — a full 4d6 ability-score sequence alone is 24 dice, plus every
combat roll ever made stays in scrollback — and browsers cap concurrent
WebGL contexts (commonly ~16). `dice3d.js` never gives a die a
permanent context: only an actively-tumbling die borrows one from a
small fixed pool (`MAX_LIVE_RENDERERS`, well under that cap) for its
brief animation, then freezes to a plain captured-frame `<img>` with no
GPU resources held — an idle/ghost/already-revealed die is never more
than a static image. Verified against a 48-die stress run (double the
worst realistic in-session count) with zero contexts leaked.

**Superseded, see the real-thrown-physics-dice entry much further below**
— `dice3d.js`'s dice are now built from a real artist-made mesh + texture
(the "rust" theme from [3d-dice/dice-themes](https://github.com/3d-dice/dice-themes),
MIT licensed — see `vendor/README.md`) instead of three.js primitive
geometry, and cannon-es physics is gone entirely. Two changes, requested
together: the operator compared a screenshot of the old primitive dice
against a professional dice-rendering library's own marketing shot and
the gap was obvious (flat materials vs. real marbled/worn textures); and
the operator explicitly doesn't need dice to tumble/bounce — "just having
the great looking die spin in place would be enough" — so the old
contained-physics-arena tumble is replaced by a scripted spin: a random
start orientation, ease-out-cubic slerp to a resting orientation over
`TUMBLE_DURATION_MS` (now 1200ms, up from 550, to fit a real "spin up
then settle" feel) plus a decaying extra spin around a random axis so it
reads as a genuine spin rather than a small reorientation. cannon-es is
no longer imported by this file (confirmed nothing else in `master/web/`
uses it either) — `vendor/cannon-es.js` stays vendored regardless, in
case a future feature wants real physics again.

The bigger change: **the DOM text overlay (`.roll-die-face`) is gone.**
The operator's original ask kept it as a legibility safety net on top of
the mesh; mid-implementation the operator changed that explicitly — the
3D die's own settled orientation is now the *only* thing communicating a
roll's result, no text layer backing it up. That raised the bar
considerably on `dice3d.js`'s face-orientation math (`computeTargetQuaternion`
and friends): given a rolled value, it has to land the die on the
*correct* face, right-side up, every time, for every shape — not just
"plausible." The theme's mesh JSON ships a `colliderFaceMap` (a low-poly
proxy mesh's triangle-id → printed-value table) that answers "which
direction does face N point"; the harder part was getting a reliable
answer out of it. Three real bugs surfaced here — two caught during
initial implementation, a third, more severe one caught during review of
that work — worth recording all three in case this code gets touched
again:

- Averaging a collider triangle's three stored per-vertex normals (the
  obvious way to get "this triangle's direction") is **not** a reliable
  stand-in for that triangle's actual flat-face direction on these
  particular meshes — confirmed by comparing against the triangle's own
  geometric (position-only, cross-product) normal on every shape (up to
  124° off on a d6). The fix: `geometricFaceNormal` computes the normal
  straight from the triangle's own vertex positions and sign-corrects it
  against the die's own center (every shape here is a convex solid
  centered at its local origin, so "points away from center" is a
  reliable, mesh-independent way to pick the right one of the two
  possible cross-product signs) — never trusting either mesh's stored
  `normals` attribute for this particular purpose again.
- The azimuth (which way is "up" on the selected face, so the printed
  glyph reads correctly rather than sideways/upside-down) comes from the
  mesh's own precomputed per-vertex `tangents` attribute rather than
  re-deriving tangent space from the matched triangle's own UV
  coordinates — an earlier attempt at the latter was numerically unstable
  because the matched triangle's UV footprint turned out to be a
  near-degenerate sliver despite being a normal-sized triangle in 3D.
- **The severe one, caught in review, not by the original implementation
  pass**: aligning a target face's *outward* (away-from-center) normal to
  the camera direction reliably landed the die on its *opposite* face
  instead — for d6, a clean, total swap, every value, every time (asked
  for 1, got 6; asked for 2, got 5; and so on). The original
  implementation's own verification method — cropping the UV region the
  *matched visual triangle* samples and checking what's painted there —
  couldn't have caught this: that check validates `findBestVisualTriangle`
  in isolation, never the actual camera-facing orientation the align
  quaternion produces, so it stayed "verified" while every single die in
  the app would have shown the wrong number. Caught instead by sampling
  the real, rendered screen pixels at the exact point a player would
  read (a `Raycaster` cast from the live camera through a die rendered
  with the shipped code, its hit `uv` sampled against the real texture)
  — the only check that reflects what actually reaches the screen.
  Root cause: the collider mesh is a pick-only utility proxy nothing
  ever renders (upstream only ever raycasts against it — see
  `colliderFaceMap`'s own doc comment in `dice3d.js`), so its triangle
  winding was never authored or checked for outward-facing consistency
  the way the *visual* mesh's was — there was no reason for anyone to
  have made that guarantee. The fix (negate that normal before aligning
  to camera) was re-verified the same rigorous way, for every value of
  every shape, not a sample — see `computeTargetQuaternion`'s own
  comment in `dice3d.js`.

With the severe bug fixed, d6's on-screen orientation was re-checked
directly against real rendered screenshots (not texture-atlas crops,
which — see above — don't reflect final screen orientation) at this
module's actual production canvas size: every face reads correctly,
right-side up, with only a small (10-30°), clearly legible tilt, no
mirroring or 90°/180° confusion. d8/d10/d12/d20 were spot-checked the
same way and look consistent with that, though not exhaustively
re-verified face-by-face at full zoom the way d6 and the face-*selection*
sweep (all 60 values, every shape) were — a good next step if this file
gets touched again, rather than a known problem. **d4 is the one shape
none of this cleanly solves**: a real d4 prints three numbers per face
(one per vertex, read from whichever vertex ends up on top, shared
across the three surrounding faces) — a fundamentally different reading
convention than "one number centered on one face toward the camera,"
which is what every other shape here uses. The face-*selection* math
still picks a geometrically correct face (and, post-fix, the correct
one, not its antipode), but which of that face's three numbers a
viewer's eye lands on isn't controlled by this code, so d4 in particular
should be treated as lower-confidence than the rest until that gets real
dedicated attention.

Also worth noting for legibility, independent of the orientation math
above: at a standard (non-Retina, `devicePixelRatio` 1) display, the
printed glyph is small and low-contrast enough to be genuinely hard to
read at this module's real 56px production size — verified by rendering
the shipped code at both `devicePixelRatio` 1 and 2 and comparing. At 2×
(a very common but not universal density on modern laptops/phones) it
reads clearly. `MAX_PIXEL_RATIO` already caps at 2×, so a Retina display
gets the sharper render already; a 1×-display player may still find the
number harder to make out than a real physical die would be. Not fixed
here — a candidate follow-up (bump `CANVAS_LOGICAL_SIZE`, or push glyph
contrast/weight further in whatever theme ships) rather than something
this pass addressed.

Because the renderer pool (`MAX_LIVE_RENDERERS`, still 10, still bounded
the same way) can now be the sole path to a *correct-looking* die rather
than just an animation, a stress test (30 dice revealing at once, well
over the pool size) surfaced a real gap: the retry budget for "pool was
briefly exhausted" was sized for a cosmetic miss (fall back to the
resting snapshot, the text overlay still had the right number), not a
correctness one. `showStaticResult` (tumble's fallback when the
animation itself can't get a renderer) now has its own, much longer
retry budget (`STATIC_RESULT_RETRY_MS`/`STATIC_RESULT_RETRY_ATTEMPTS`,
~4.5s total) sized to outlast a realistic worst case — this file's own
24-die ability-score-reveal scenario needing 2-3 other dice's *entire*
tumbles to finish before a pool slot frees up — rather than the old
short window, which a stress test showed left dice at the back of a
large burst never recovering a face at all.

Vendoring: `vendor/dice-themes/` (new) holds the mesh JSON (shared with
the upstream "default" theme, since "rust" ships no mesh of its own —
see `vendor/README.md`) plus the rust theme's diffuse and normal-map
textures (~365KB combined; the normal map was downscaled from the
upstream 1024×1024 to 256×256 first — a die renders at 56 logical px on
screen, the extra resolution bought nothing visible and the original was
726KB on its own). `default` and `gemstoneMarble` were compared and not
shipped: `default`'s diffuse is a transparent alpha-mask meant to be
tinted, workable but softer-contrast at this render size than a
directly-painted texture; `gemstoneMarble` uses its own, more elongated
gem-cut mesh (not a plain icosahedron/etc.) that reads less immediately
as "a d20" at a glance, and would need its own full face-orientation
verification pass separate from everything above.

There's now an italic, gently-pulsing "The DM is pondering the scene."
bubble (`.dm-thinking`, `appendDmThinkingBubble`/`clearDmThinkingBubble`)
for the slow pass's own real latency — Master only actually sends the
underlying `narrative.dm_thinking` broadcast once a short delay has
passed with the pass still running, so a fast reply never flashes it (see
`master/README.md`'s own writeup for why). Tagged with the triggering
`narrative.player_input`'s `message_id` so the right one clears once the
matching `client.display` or `system.error` arrives; a spectator who was
never going to receive either (a slow-pass failure notice is private to
the acting player only) falls back to the bubble's own 5-minute
`setTimeout` instead of it sitting in the log forever.

**Dice are now real thrown physics, not a scripted per-bubble spin —
`dice3d.js` is retired outright, replaced by `dice-arena.js` driving one
shared arena overlaying the log (`#dice-arena`, see index.html) via the
vendored [`@3d-dice/dice-box-threejs`](https://github.com/3d-dice/dice-box-threejs)
0.0.12 (MIT — see `vendor/README.md`).** A die's button no longer holds
its own permanent visual; clicking it throws that one die into the shared
tray, lets real physics settle it, crops the settled die's own on-screen
region out of the tray's shared canvas (camera-projection math against
the library's real `PerspectiveCamera`, not a guess — see
`dice-arena.js`'s `boundsForMesh`), and flies that cropped image back to
the button's slot before swapping it in as the resting picture. Same
underlying contract app.js's `client.roll*`/`client.ability_score_rolls`
call sites already used (`settleDie` ending in a `.revealed` state,
`.dropped` marker support) — only `settleDie`'s internals and timing
changed: it's `async` now and genuinely awaits the real throw+flight
finishing, rather than a fixed timeout the way `dice3d.js`'s scripted
spin could rely on (real physics plus a serial queue — see below — don't
run on a constant this file could hardcode).

What was actually verified, not just assumed correct because that's the
library's whole stated purpose (the same rigor this project's own earlier
face-orientation bug — see the `dice3d.js` entries above — was caught
missing until pixel-level checks happened):
- **Predetermined results, every shape, several values each**: d6/d8/
  d10/d12/d20 all confirmed correct via real rendered screenshots at
  production settings, cross-checked against `getDiceResults()`'s own
  reported value and, for d20, directly against which geometry face the
  library's camera-facing convention actually painted the forced number
  onto (matched exactly, dot-product 1.0 alignment). d10@10 correctly
  shows the physically-printed "0" face, same convention `dice3d.js`
  always used.
- **A real, confirmed library bug for d4 specifically** — not fixed, not
  worked around, shipped with this same honest flag `dice3d.js` used to
  carry for d4 (see that file's own entries above), except this one is a
  harder failure than "not fully sure which glyph a viewer's eye lands
  on": the vendored library's own d4-forcing path (`swapDiceFace_D4`,
  structurally different code from every other shape's forcing) does not
  actually relabel the geometry at the position a player reads. Verified
  three independent ways — the metadata (`getDiceResults()`'s reported
  `value` for a forced d4 stays the pre-forced natural roll forever,
  because `swapDiceFace_D4`, unlike its sibling, never clears the die's
  cached result), the geometry (projecting the settled mesh's own face
  normals to find the true "up" reading position, confirmed independently
  via *unforced* natural rolls first, then checking what's actually
  painted there for a *forced* roll — the natural pre-forced number, not
  the requested one), and real rendered screenshots at high contrast,
  side by side. A d4 roll in this app may show the wrong face; the
  server-decided result is unaffected and always shown as text elsewhere
  in the same bubble regardless (design doc: never something a client
  computes or can override) — only this one die's own picture can be
  wrong. See `dice-arena.js`'s own `D4_FORCING_IS_UNRELIABLE` doc comment
  for the full trail if this needs to be revisited.
- **Fly-home lands on the actual button position, not a stale one** —
  confirmed by throwing a die, scrolling the log a large distance while
  it's still mid-air (well before physics settles), and checking the
  flying image's own real animation target: it matched the button's
  *post-scroll* screen position, not where the button was when the throw
  started.
- **Two dice thrown close together don't cross-wire results** — confirmed
  by clicking a second bubble's die while the first was still visibly
  mid-throw and checking both settled with their own correct shape/value/
  image, never swapped.
- **Bounce stays inside `.log-frame`'s box** at a normal desktop width, a
  narrow (560px) width, and after a live browser resize mid-session
  (`dice-arena.js`'s own `window.resize` handler calling the library's
  `setDimensions()` to rebuild the physics walls and camera for the new
  size — confirmed this is the correct call: the similarly-named
  `updateConfig()` is an unrelated *theme* reloader that never touches
  physics bounds at all, a mix-up this session caught by reading the
  vendored source rather than guessing from the method name).
- The 4d6-drop-lowest ability-score flow (`client.ability_score_rolls`)
  still works end to end under the new `async`/awaited `settleDie` —
  including clicking all four dice of a set in rapid succession, which
  exercises the serial queue below for real (four throws, correctly
  sequenced, not corrupted).

Two honest, known limitations, not fixed here:
- **The throw doesn't originate from the exact clicked pixel.** The
  vendored library's only throw path (`startClickThrow`, used by both
  `roll()` and `add()`) picks a randomized vector across the tray's own
  current width/height itself — there is no supported way to say "throw
  from pixel (x, y)". A click still throws a real, physically-simulated
  die into the shared tray; it just doesn't visibly originate from the
  button itself the way the fly-home *landing* does.
- **Two rolls "close together" now animate one after another, not
  physically overlapping in the tray**, despite that being this rework's
  original ambition (matching a real tabletop with more than one thing
  happening on it at once). Downgraded deliberately, not by oversight:
  the vendored library's `add()`/`roll()` both route through a shared
  `this.rolling` flag that, if a second throw call lands while an earlier
  one is still animating, clears every current die (including the
  still-mid-air one) and — worse — leaves that earlier throw's own
  promise unresolved *forever*, confirmed by reproducing it directly in a
  real browser before this module was ever built around a fix. Every
  `dice-arena.js` call into the shared `DiceBox` instance is funneled
  through one serial queue as a result (see that file's own "Why a serial
  queue" doc block) — correctness over the original concurrent-tumble
  ambition.

Vendoring: `vendor/dice-box-threejs.es.js` (new, ~700KB, three.js r143 and
cannon-es bundled inline by the package's own build — no separate copies
of either needed, unlike `dice3d.js`'s setup) plus its `LICENSE`.
`vendor/three.module.min.js`, `vendor/cannon-es.js`, and
`vendor/dice-themes/` are all gone — nothing in `master/web/` used them
once `dice3d.js` itself was removed (confirmed by grep immediately before
deleting each, the same check the last several dice-related commits have
all done). See `vendor/README.md` for the full entry, including how to
pull a newer version later.

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

Master doesn't cache these files beyond what `http.FileServer` does — a
browser reload picks up the change immediately. To run a genuinely
different skin rather than editing in place, copy this whole directory
and point `-web-dir` at the copy instead.

## Known limitations

- **Scroll position isn't preserved across "Load earlier."** New content
  gets prepended above what's visible, but the client doesn't adjust
  scroll offset to compensate — the viewport can jump. A real
  implementation would anchor scroll to the insertion point.
- **`sender_id` is just the character name you type in.** There's no
  auth/account system yet (design doc §6.6's Discord OAuth isn't
  implemented), so nothing stops two people from joining as the same
  name.
- **Bubble "who" labels can't resolve another player's name.** A
  bubble's `character_id` is Master's real `store.Character.ID` (it has
  to be — the DM tool-use slow pass hands it straight to the System
  Engine, so it can't be a display name; see `bubbleDisplayName` in
  `app.js`). This client only knows its *own* typed name, so it can
  render its own bubbles correctly but falls back to the raw ID for
  anyone else's — there's no campaign-roster/name-lookup endpoint yet.
  Not an issue in the single-character-per-connection testing this
  client is built for today.
- Hand-written, not generated. Design doc §6 envisions a proper JS
  reference SDK against `protocol/asyncapi.yaml`; this predates that and
  has to be kept in sync with the protocol by hand (see
  `PROTOCOL_VERSION` in `app.js`).
- **A rejoin mid-creation still starts over, not resumes.** A finished
  character is now correctly resumed on rejoin (see above) — but an
  in-progress creation session (picked "detailed roll," answered a few
  questions, then disconnected before finishing) isn't persisted
  anywhere durable, only in Master's in-memory `creationSessions` map, so
  a reconnect before finishing still restarts creation from the top-level
  choice. Narrower than the old "every rejoin restarts creation" gap,
  but a real one until creation sessions themselves survive a reconnect.
- **The character sheet is read-only** — no write-back, no editing;
  `character-sheet.js` only walks `properties`/`items`/`$ref`, not the
  full JSON Schema spec (no `oneOf`/`anyOf`/`patternProperties`/etc.),
  since nothing today's schema uses needs more than that.
- **The die outline is a generic polygon, not real d20 face art** — stale
  as of the real-thrown-physics-dice rework above: dice are now real
  physics-thrown 3D geometry with real per-face numbering for every shape
  this app supports (d4/d6/d8/d10/d12/d20), not an outline of any kind.
  Left here rather than deleted so `git blame` on this line still points
  at when it stopped being true.
- **Combat rolls only ever throw the check's own d20** — `roll.check_request`
  only triggers `ResolveCheck`, not `ApplyEffect`, so nothing here rolls
  damage dice yet. Not a dice-*rendering* limitation any more (every shape
  renders correctly — see above), purely that nothing today asks for a
  non-d20 combat roll; `client.ability_score_rolls` already throws d6 dice
  through the same pipeline.
