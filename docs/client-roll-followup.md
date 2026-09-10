# Follow-up brief: the `client.roll` interactive dice bubble

**Status:** the `client.roll*` message family is *specced only*. The Go
payloads (`master/internal/protocol/messages.go`), their `MessageType`
constants + `IsValid`, and the AsyncAPI entries all exist and round-trip
in `client_messages_test.go`, but nothing emits or consumes them yet.
This document is the brief for the agent that builds the rest.

The user's intent, verbatim from the planning session: *"We are changing
the way the die will be rolled with this implementation. This will
simplify the graphical display of die rolls."* Build it with the **Fable**
model.

## What already exists (don't re-spec it)

Six message types, one namespace:

| type | direction | payload | notes |
|---|---|---|---|
| `client.roll` | Master → the roller | `{prompt_id, character_id, purpose, text, dice[]}` where a die is `{id, sides, label, result}` | `result` is the engine's authoritative face value, **pre-sent** — the click is a reveal, not a roll |
| `client.roll_spectate` | Master → every *other* client | same shape, but each die is `{id, sides, label}` — **no `result`** | spectators must not see the numbers before the roller reveals them (anti-metagaming, design doc §9.7) |
| `client.roll_reveal` | roller → Master | `{prompt_id, die_id}` | one per die click |
| `client.roll_spectate_reveal` | Master → the other clients | `{prompt_id, die_id, result}` | Master relays each reveal so spectator ghost dice fill in |
| `client.roll_complete` | Master → the whole campaign | `{prompt_id, total, result_summary}` | sent once every die of `prompt_id` is revealed; also what unblocks the waiting slow pass |

`purpose` is a typed-const enum (`ClientRollPurpose`):
`attack | damage | save | check | initiative | death_save | custom`,
with an `Unspecified` zero and `IsValid()`.

## What to build

### 1. Server: the roll flow (`master/internal/server/`)

- A `sendClientRoll(...)` helper (sibling of `sendClientQuery`/`sendClientChoice`
  in `clientmsg.go`) that, given the authoritative dice the engine rolled:
  - sends `client.roll` (with results) to the roller's connection,
  - broadcasts `client.roll_spectate` (results stripped) to everyone else
    in the campaign,
  - registers a pending roll in a registry mirroring `pendingPrompts`,
    tracking the reveal count per `prompt_id`.
- Dispatch cases for `client.roll_reveal`: verify the sender owns the
  roll's `character_id` (the same ownership check `resolveCheck` uses),
  mark that die revealed, relay `client.roll_spectate_reveal` to the
  other clients, and when the last die is revealed send
  `client.roll_complete` and resolve the pending roll.
- **The DM waits** (user decision, human-in-the-loop): the slow pass
  pauses after asking for a roll and resumes on `client.roll_complete`.
  Give it a timeout — if it fires, Master reveals the remaining dice
  itself (it already has the authoritative results), sends
  `client.roll_complete`, and continues, so one idle player can't freeze
  the table. This is the part that needs a real `dm_slow_pass.go`
  restructure — the current bounded tool-call loop runs to completion
  synchronously; it needs a suspend/resume seam.
- `roll.request` / `roll.result` are **retired** when this lands — their
  one caller (`resolveCheck`) moves onto `client.roll`, and the old
  authoritative-broadcast-plus-cosmetic-client-animation split goes away.

### 2. Web client: the 2-D die (`master/web/`)

Replace the 3D dice tray entirely. The current implementation —
`web/dice.js` (three.js + cannon-es physics d20), `web/dice.css`,
`web/dice-skins.js` (Emerald/Obsidian/Ivory materials), and the
`window.Dice` API — all goes. The row that hosted it in the sidebar is
already gone from `index.html`/`style.css`.

New rendering, in the bubble itself (not a separate tray):

- One outlined SVG per die in the `client.roll` — a d20 is a 20-gon
  outline, a d6 a rounded square, d4 a triangle, etc.; the `sides` field
  says which. `label` (e.g. "1d8 slashing", "advantage") captions a die
  or a group.
- Click a die → a short CSS/JS tumble animation (a few quick face
  swaps), settling on the `result` that was already in the payload. Send
  `client.roll_reveal { prompt_id, die_id }`.
- The spectator twin (`client.roll_spectate`) renders the same outlines
  as greyed "ghost" dice showing "…"; on each `client.roll_spectate_reveal`
  the matching ghost die turns into the solid result die. No clicking.
- `client.roll_complete` settles the whole bubble (dim it, show
  `result_summary`), same `.answered` treatment the `client.query` /
  `client.choice` bubbles use.

**Dependencies to drop** once this is done: three.js, cannon-es (both
were only used by `dice.js`). No new external libraries — SVG + CSS
transitions are enough for a 2-D die, and the artifact/CDN constraints
make a physics engine not worth it anyway.

### 3. Tests

- Server: the full roller/spectator reveal sequence end-to-end (one
  connection rolls, another spectates, assert the spectator never
  receives a `result` until the matching reveal, assert
  `client.roll_complete` fires after the last die and unblocks the slow
  pass); the timeout path (roller goes silent → Master self-reveals and
  continues); the ownership check on `client.roll_reveal`.
- Retarget any `roll.request` / `roll.result` test to `client.roll*`.
- Web: `node --check`; a DOM test of the reveal → `client.roll_reveal`
  wiring if the client test setup allows it.

## Design rules that still apply (CLAUDE.md)

- Authoritative dice results are the engine's, computed server-side and
  sent as values to reveal — the client never computes or can override a
  face value.
- Every message carries the full envelope.
- Typed-const message types with an `Unspecified` zero (already done for
  `ClientRollPurpose`).
- Godoc on every exported identifier; table-driven tests; commit
  `type(scope): description` straight to `main`, push after each.
- No proprietary D&D terms in code, comments, or fixtures — SRD only.
