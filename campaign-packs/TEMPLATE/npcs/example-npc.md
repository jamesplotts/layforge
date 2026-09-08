---
# ── npcs/*.md — one file per named NPC ───────────────────────────────
id: example-npc               # required, non-empty, unique among npcs
location: example-location    # the location id where this NPC is normally
                              # found (must match a locations/*.md id)
stat_block_ref: "SRD Commoner" # a plain SRD creature or class type in
                              # quotes — "SRD Veteran", "SRD Guard",
                              # "SRD Acolyte". NEVER a named published
                              # monster. Only matters if the NPC fights.
voice: "plain-spoken and blunt; trails off mid-sentence when nervous"
                              # a short phrase telling the DM how they talk.
                              # Quote it — it will usually contain a comma
                              # or colon.
# chapter: chapter-1          # optional, same meaning as on a location
# side_quest: the-lost-cart   # optional, use instead of chapter
---

Replace this body with the NPC's **personality and motivation**: what
they want, what they know (and, under `shared_knowledge: strict`, what
they *don't*), how they behave toward the party before and after trust is
earned, and what would make them turn hostile or flee. One to three
tight paragraphs. This is what the DM AI voices — give it something to
act on, not a resume.
