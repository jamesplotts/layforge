---
# ── locations/*.md — one file per place the party can be ──────────────
id: example-location          # required, non-empty, unique among locations;
                              # DM tools look places up by this id
connections: []               # ids of OTHER locations/*.md files reachable
                              # from here, e.g. [village-square, north-road];
                              # powers travel_to and the DM's sense of
                              # geography. Empty = isolated.
# chapter: chapter-1          # optional — ties this location to a chapter
# side_quest: the-lost-cart   # optional — use INSTEAD of chapter for a
                              # self-contained side adventure
---

# Example Location

Replace this body with the **description** a DM reads when the party
arrives: what they see, hear, and smell; what's immediately interesting;
what takes a closer look (call for a check, and say which one and roughly
what DC — e.g. "Wisdom check, DC 12"). Reference NPCs and encounters by
their file: see `npcs/example-npc.md` and
`encounters/example-encounter.md`.

Keep it to what's true about the place. Save what *happens* here for the
encounter files.
