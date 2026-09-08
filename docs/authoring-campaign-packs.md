# Authoring a Campaign Pack

A **campaign pack** is a directory of Markdown files that gives the DM AI
its ground truth for a campaign: the places, the people, the set-pieces,
and the governance rules the table plays under. Everything the AI narrates
is anchored to what's written here, so a session doesn't drift or
re-improvise facts it already established.

This is the how-to. [`docs/design.md`](design.md) §6.4 is the
authoritative spec; this document doesn't restate it, it walks you
through writing one.

- **Fastest start:** copy [`campaign-packs/TEMPLATE/`](../campaign-packs/TEMPLATE),
  rename the directory, and fill in the placeholders — every file there is
  commented.
- **A complete real example:** [`campaign-packs/sable-ravine/`](../campaign-packs/sable-ravine)
  — a three-session level 1–3 adventure, no chapters, ~14 files.

---

## Directory shape

```
your-pack-slug/
  campaign.md          ← the only required file
  locations/*.md       ← one file per place
  npcs/*.md            ← one file per named NPC
  encounters/*.md      ← one file per set-piece (fight, parley, trap, choice)
  state.json           ← starting mutable state (optional; see below)
```

- The directory name is the pack **slug**: lowercase letters, digits, and
  hyphens only, no spaces. Conventionally it matches `campaign.md`'s `id`.
- `locations/`, `npcs/`, and `encounters/` are each optional and flat — no
  nested subdirectories. Files load in sorted filename order.
- Every file is [YAML front matter](#front-matter-reference) between `---`
  lines, followed by a Markdown body.

## Validation — what "valid" means

A pack is loaded by `campaignpack.LoadPack` (the same gate the admin
panel's **Bind Pack** button and the pack-library installer run). It
accepts a pack when:

- `campaign.md` exists and its front matter has a **non-empty `id`**.
- Every `locations/`, `npcs/`, and `encounters/` file has a **non-empty
  `id`** in its front matter.
- All front matter parses as YAML.

Everything else is advisory. A pack with only a `campaign.md` is valid. A
`connections:` entry pointing at a location that doesn't exist won't fail
loading — but it will confuse the DM, so keep them honest.

### The one YAML gotcha: colons in values

`voice: clipped, dry: and terse` is invalid YAML — the second colon
breaks it. **Quote any value containing a colon or that starts with a
special character:**

```yaml
voice: "clipped, dry: and terse"
title: "The Bridge at Kettle's Crossing: A Reckoning"
```

Master auto-repairs unquoted colons in *generated* packs as a
belt-and-suspenders measure, but hand-authored packs get no such
treatment — quote it yourself.

---

## `campaign.md`

**Front matter** carries the pack's identity and governance. **Body** is
the overview a DM reads once to understand the whole adventure: where it
happens, what's wrong, who wants the party involved, and what resolving it
looks like — written as a briefing of concrete facts, not atmospheric
prose. Follow it with `## Hooks` (how the party gets pulled in) and
`## Running this pack` (what a DM needs to know that isn't obvious from
the individual files — the *real* problem behind the visible symptoms,
how `shared_knowledge` bites, which threads are optional).

## `locations/*.md`

Front matter: `id` (required), `connections` (ids of other locations
reachable from here — powers `travel_to` and the AI's sense of
geography), optional `chapter` / `side_quest`.

Body: what the party perceives on arrival, what rewards a closer look
(name the check and rough DC), and pointers to the NPCs and encounters
found here. Describe what's *true* about the place; leave what *happens*
to the encounter files.

## `npcs/*.md`

Front matter: `id` (required), `location` (where they're normally found —
a location id), `stat_block_ref` (a plain **SRD** creature or class type
in quotes, e.g. `"SRD Veteran"` — never a named published monster; only
matters if they fight), `voice` (a short phrase on how they talk),
optional `chapter` / `side_quest`.

Body: personality and motivation — what they want, what they know (and
under `shared_knowledge: strict`, what they *don't*), how they treat the
party before and after trust, what turns them hostile. One to three tight
paragraphs the AI can act on.

## `encounters/*.md`

An encounter is any authored moment of consequence — not just combat.

Front matter: `id` (required), `location`, `involves` (NPC/entity ids in
play), optional `chapter` / `side_quest`, optional `min_players` /
`max_players` (set these only for a side quest sized for a subset of the
table).

Body: what happens, and **how the DM should adjudicate the branches** —
"if the party does X … / if they do Y …", with named checks and DCs, the
SRD stat blocks for any fight, and what's at stake. Loot and forward
consequences live here, not in a separate reward step — so an NPC can be
carrying the item the party would otherwise only find after beating them.

## `state.json`

The starting values for a campaign's *mutable* state — party location,
story flags, NPC status — kept out of the static Markdown so the pack
stays clean and diffable in git. The current Master tracks live state in
its own store rather than rewriting this file; it documents the shape a
pack expects and gives a new table a clean slate to start from. Optional.

---

## Chapters and side quests

Both are optional, lightweight tags — not a directory structure.

- **Chapters** group content into roughly one-level arcs. Add a
  `chapters:` outline to `campaign.md` (`id` / `title` / `level_range` /
  `summary` per entry), then set `chapter: <id>` on the location/npc/
  encounter files that belong to each. Generation deliberately produces
  only the outline plus the first chapter; later chapters are generated
  (or written) separately, once the party is near them.
- **Side quests** use `side_quest: <id>` *instead of* `chapter` for a
  short, self-contained 1–2 encounter detour that doesn't touch the main
  progression — handy when the table is short a player for a session.

A pack that uses neither (like `sable-ravine`) is exactly as valid as one
that uses both.

## Governance fields

Set in `campaign.md` front matter, enforced by Master at the tool-call
layer, not left to the AI (design doc §9):

| Field | Values | Meaning |
|---|---|---|
| `pvp_policy` | `pve_only`, `pvp_allowed` | Whether player-vs-player actions resolve at all |
| `maturity_tier` | a `maturity-tiers/<id>.md` id | Content-maturity constraint prompt |
| `image_maturity_tier` | a `maturity-tiers/<id>.md` id | Same, for generated images (must be no less strict) |
| `shared_knowledge` | `strict` (recommended) | `strict` = NPCs know only what they've learned in play |
| `lines` | list of strings | Hard "never, on or off screen" limits |
| `veils` | list of strings | "Happens, but off screen / undescribed" |
| `content_warnings` | list of strings | Advisory, shown to players |

## Legal — SRD only

Everything in a pack that ships in this repo (or is generated by default)
must be **original, SRD 5.1-legal content**: no proprietary terms
("Dungeons & Dragons", "D&D", "WotC"), no named published characters, no
non-SRD monster names, no setting flavor text, no reproduced module
plots or locations. Tone- and genre-inspired is fine; direct reuse is
not. `stat_block_ref` values must be plain SRD types. See
[`CLAUDE.md`](../CLAUDE.md) and design doc §6.4 / §12.

(A pack you keep privately on your own Master is yours — this constraint
is about what's committed here and what Master generates out of the box.)

---

## Getting a pack onto a Master

1. Put the pack directory somewhere Master can read.
2. Admin panel → **Campaign** tab → set the **Campaign Pack Directory**
   to its path → **Bind Pack**. Binding runs the same validation
   described above and reports any error.

Or pull a whole shelf of ready-made packs: **Campaign** tab → **Install
the campaign pack library from layforge.org**.
