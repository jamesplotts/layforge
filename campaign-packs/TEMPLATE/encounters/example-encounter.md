---
# ── encounters/*.md — one file per set-piece ─────────────────────────
# An "encounter" is any authored moment of consequence: a fight, a
# negotiation, a trap, a choice. It does not have to be combat.
id: example-encounter         # required, non-empty, unique among encounters
location: example-location    # where it happens (a locations/*.md id)
involves:                     # ids of NPCs (or other entities) in play here
  - example-npc
# chapter: chapter-1          # optional
# side_quest: the-lost-cart   # optional, use instead of chapter
# min_players: 3              # optional — only set for a side quest sized
# max_players: 4              # for a specific subset of the table
---

# Example Encounter

Replace this body with what actually happens and, more importantly, how
the DM should adjudicate the ways it can go:

- **If the party does X** — what follows.
- **If the party does Y** — what follows instead. Give the DM real
  branches, not one scripted outcome.

Call out the checks by name and DC (`resolve_check`, Charisma, DC 13),
name the SRD stat blocks involved for any fight, and state what's at
stake — what the party gains or loses depending on how this resolves.
Loot and consequences that carry forward belong here, not in a separate
reward step.
