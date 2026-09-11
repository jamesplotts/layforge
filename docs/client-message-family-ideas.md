# Candidate `client.*` bubble types

## Context

The `client.*` family (design doc §4, and `master/internal/protocol/messages.go`)
is the small, reusable set of chat-bubble interactions Master uses to
talk *to* a player. What exists today:

- **Built and in use:** `client.display`, `client.query` /
  `client.query_response`, `client.choice` / `client.choice_response`,
  `client.image`.
- **Specced, not built:** the `client.roll*` dice family — see
  [`client-roll-followup.md`](client-roll-followup.md).

This note collects candidate *additional* bubble types. **Nothing here
is committed work** — it's a design backlog. Each entry says what gap it
fills and whether it earns a new message type or is really just fields
on one we already have.

The bar for a new type: a genuinely distinct interaction pattern — a
different response shape, different timing, or different multiplicity
(one player vs. the whole table) — not just a different label or icon.

Governing rules carry over from `CLAUDE.md`: every message carries the
full envelope; authoritative state (dice results, what a check unlocks,
who may see what) is computed server-side and sent to the client as
values to render, never decided by the DM model or the client; typed
message-type constants with an `Unspecified` zero and an `IsValid`;
design fields forward; SRD-only content; commits `type(scope):
description` straight to `main`.

---

## Tier 1 — fills a real gap in D&D play

### `client.reaction` — the timed interrupt

The DM narrates the goblin's swing at Reorx and needs to pause:
*"Cast Shield? Counterspell? an opportunity attack? Cutting Words?"* — a
prompt that **interrupts** whatever's on screen, shows a visible
countdown, and **auto-resolves to a default** if the player doesn't
answer in time.

This is the biggest gap for D&D specifically. Reactions get forgotten at
every human table, and an LLM DM has no way to "look up expectantly and
wait" without a mechanism like the `client.roll` DM-waits flow. It is
architecturally the twin of that flow: the slow pass suspends on the
response, with a timeout that fires the default and continues so one
idle player can't freeze the table.

```
client.reaction {
  prompt_id,
  trigger_text,           // "The goblin's scimitar arcs toward Reorx."
  options: [{value, label, sublabel?}],   // sublabel: "1st-level slot"
  timeout_seconds,
  default_value           // what happens on no answer (usually "none")
}
client.reaction_response { prompt_id, value }
```

### `client.multi_choice` — pick N of M

`client.choice` is pick-one. Pick-*some* covers: spell preparation
("choose 4"), shopping ("select what you're buying"), a fireball's
targets ("which creatures are caught in the blast"), splitting loot,
choosing which rumors to chase. Add `min_selections` / `max_selections`.

```
client.multi_choice {
  prompt_id, prompt_text,
  options: [{value, label, sublabel?, enabled?, disabled_reason?}],
  min_selections, max_selections
}
client.multi_choice_response { prompt_id, values: [...] }
```

### `client.number` — bounded numeric input

"How much gold do you offer?" / "How many squares do you move?" / "Spend
how many Hit Dice?" A `client.query` returns free text you have to parse
and re-prompt on garbage; a real `{prompt, min, max, step, unit}`
validates client-side and can't come back malformed.

---

## Tier 2 — richer information delivery

### `client.card` — a structured handout

Not prose (`client.display`) and not just a picture (`client.image`):
the mechanical card for a magic item you identified, a monster stat
block you now know, an NPC's portrait + name + disposition, a quest-log
entry. Rendered from structured fields — it can reuse the schema-driven
sheet renderer (`character-sheet.js`).

A card has an identity so it can be **updated in place** — see the
knowledge-check section below, where a better roll fills in more rows of
the same card rather than spawning a new bubble.

```
client.card {
  card_id,                 // stable — a later card with this id replaces
  subject_id?,             // the creature / item / npc this is about
  kind: "item" | "creature_lore" | "npc" | "quest" | "location" | ...,
  title,
  fields: [{label, value, group?, tier?}],
  progress?: {tier, max_tier, source},   // for progressive reveals
  actions?: [ "share", ... ]              // controls the bubble offers
}
```

### `client.form` — multi-field, one submission

Session-zero questionnaire, character background (name / homeland / bond
/ flaw at once), a downtime-activity plan — instead of a five-message
`client.query` chain. Fields are typed (`text`, `number`, `choice`,
`multiline`), the whole thing submits once.

---

## Tier 3 — multi-player coordination

### `client.ready` — the gather

"Everyone tap **Ready** before we start the next scene / take the long
rest / skip ahead." Master waits for every connected player and reports
progress ("3 of 4 ready"). Scene transitions and rests are exactly where
a human DM asks "anything before we move on?"

### `client.poll` — table vote

"Which job do we take?" Everyone votes; the aggregated tally is shown to
all. Distinct from `client.choice` because it's collective and the count
is the point. Options for anonymous vs. attributed, and reveal-as-you-go
vs. reveal-at-close.

### `client.trade` — a two-player offer

"Reorx offers you 50 gp for the Wand of Magic Missiles. **Accept /
Counter / Decline**." Player-to-player gold and item exchange that
otherwise has to route through the DM narrating both sides. Master
escrows and validates both halves before it commits.

---

## Tier 4 — ambient / tension

### `client.clock` — a live gauge

A filling countdown (the ritual completes in 3 rounds; the chase; the
alarm level 2/4) or a discrete progress clock. Death saves are literally
a 3/3 clock; exhaustion is a 0–6 gauge. Tension you can *see* instead of
a line of text that scrolls away. Master owns the value and pushes
updates; the client just animates between them.

---

## Probably fields, not new types

- **Confirmation** ("drink the unidentified potion?") → `client.choice`
  with two options.
- **DM whisper / secret** → `client.display` already has `recipient`;
  add a `tone` hint (`whisper` / `narration` / `system`) and a
  "yours to reveal or keep" marker for the social contract.
- **"It's your turn" nudge** → `client.display` with an `attention`
  flag, distinct from the `turn.state` sidebar widget.
- **Choices that show their cost** ("force the door — DC 15 STR" vs.
  "pick it — DC 12 DEX, 1 minute" vs. "blast it — alerts everyone") →
  grow `ClientChoiceOption` with `sublabel`, `enabled`, and
  `disabled_reason`. This also lines up with the project's preference
  for full per-option enumeration over an abstract legality summary.

---

## Knowledge checks that reveal information — the lore card

A player rolls a knowledge check to recall what they know about the
monster they're facing (or an item, an NPC, a location). On a success
they get a `client.card` about that subject; the **margin of success
decides how much of it unlocks**. The check doesn't *generate*
information — the subject's real data already lives in the System Engine
— it **unredacts** it.

### Tiered reveal

The exact DCs and tier contents are the engine's / campaign pack's to
define (they're SRD/D&D knowledge, which `CLAUDE.md` keeps out of Master
outside the system-engine adapter). A workable creature scheme:

| tier | unlocked by | what you learn |
|---|---|---|
| 0 | fail | nothing, or "it seems unnatural — you can't place it" |
| 1 | meet DC | name, creature type, one-line nature |
| 2 | DC + 3 | resistances / immunities / vulnerabilities, condition immunities |
| 3 | DC + 5 | its attacks — what it does, damage types, roughly how hard it hits |
| 4 | DC + 8 | the "gotcha" — regeneration, pack tactics, magic resistance, legendary actions, spellcasting |
| 5 | crit / DC + 10 | hard numbers: AC, HP, save bonuses |

Resistances *before* attack details is deliberate — that's the
tactically useful thing a scholar recalls first, and it's what makes the
roll worth making.

### Keep the reveal off the LLM (gates over prompting)

The DM model decides nothing about what's shown:

1. Player: *"I try to recall what I know about this thing."* The slow
   pass calls `resolve_check` with `purpose: recall_lore` and a
   `subject_character_id`.
2. The engine resolves the check **and** returns the fact set scoped to
   that result — a new RPC, e.g.
   `RecallCreatureKnowledge(subject_id, check_total) -> {tier, fields[]}`.
   OCE owns this: *which field is a "resistance"* and *what DC a CR-5
   monstrosity warrants* is engine knowledge, not Master's.
3. Master sends a **private** `client.card` (only to the character who
   rolled — anti-metagaming, the same principle as `client.roll_spectate`
   stripping results) plus a `client.display` with the DM's flavor:
   *"The acrid stench gives it away — this thing spits acid, and steel
   barely scratches it."* The model writes the colour; the card carries
   the mechanics. The full stat block never reaches the client ungated.

### The card is upgradeable

Keyed by `subject_id` + `card_id`. A later card for the same subject at a
higher tier **fills in the existing bubble**:

- The wizard rolls a 22, the fighter rolls an 8 — each gets their own
  card at their own tier.
- The same player rolls again with advantage, or studies the corpse
  after the fight → the card gains rows.
- A spell that reveals resistances → that tier unlocks on every card.
- **Empirical rows fill in as the fight goes**: you hit it with fire and
  it doesn't flinch → the card gains an *observed: not resistant to
  fire* line, even for players who blew the knowledge roll. The party
  learns by doing, and the card is where that gets recorded.

### The Share button (adds intrigue)

The card from a check is private to the roller and carries a **Share**
control. Tapping it re-sends the card — at the sharer's tier, or a
chosen subset of rows — to the rest of the party as a `client.card`
attributed to the sharer ("via Mordo"), so the table sees *who* knew it
and is trusting a person, not the dice.

```
client.card_share {
  card_id, subject_id,
  scope: "party" | [character_ids],
  rows: "all" | [field labels]      // selective disclosure
}
```

Master validates the sharer actually holds that card at that tier, then
fans it out. The intrigue is in what *doesn't* get shared:

- The wizard can withhold entirely, share only the resistance and sit on
  the legendary actions, or say something false at the table out loud
  while the real card stays unshared.
- Nothing ever forces a share — the DM never auto-reveals one player's
  private knowledge.
- A shared card is visually second-hand; the recipients can't upgrade it
  themselves, only the original roller's future rolls do.
- Master can log *that* a share happened (for the DM model's awareness of
  table state) without the model being able to force the content.

### Combat-grade specifics

The monster read has to change what you do on your **next turn**, so
unlike slower lore it must be fast and glanceable:

- **Free or bonus-action cost**, offered once when the creature first
  appears — ideally right after initiative, so it lands before turn one
  rather than costing someone their action mid-fight.
- **Lives in the sidebar during the encounter**, pinned by the turn
  tracker, so you read it on your turn without scrolling the log.
- **Shared fast** — combat's too quick for "the wizard knows but won't
  say"; in practice the best roll at the table populates one card the
  whole party sees (a table setting: pooled vs. per-character knowledge).

### Generalizes beyond monsters

Same pattern — check → tiered `client.card` about a subject:

- **Magic item** (Arcana / attunement / *Identify*): "it's magical" →
  school → properties → command word / curse.
- **NPC or faction** (History / Insight): who they are → allegiance →
  what they want → their leverage.
- **Plant / poison / tracks / terrain** (Nature / Survival), **religious
  iconography** (Religion), **a location's history**.

The slower types can afford the full tiered-reveal ceremony; the combat
lore card is the same type running in a hurry.

### The long game: a Bestiary / Lore tab

Cards don't have to scroll away. A sidebar tab that accumulates every
card the party has unlocked turns this into a campaign-long journal —
one of the more satisfying things a digital table can do that paper
can't. Failed-by-a-lot rolls could even seed *plausibly wrong* entries
the party has to unlearn later, though that needs the engine to generate
convincing falsehoods, so it's a v2.
