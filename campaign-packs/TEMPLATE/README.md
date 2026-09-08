# Campaign Pack Template

A minimal, **valid** campaign pack you copy to start your own. Every file
here is heavily commented; the placeholder content exists only so the
pack loads as-is.

## Use it

1. Copy this whole directory and rename it to your pack's slug
   (lowercase, hyphens, no spaces — e.g. `the-bridge-at-kettles-crossing`).
2. Replace `campaign.md`'s `id` and `title`, then work through the body.
3. Replace or delete `locations/example-location.md`,
   `npcs/example-npc.md`, and `encounters/example-encounter.md` with your
   own — add as many as you need.
4. Set `campaign_pack_id` in `state.json` to your slug.
5. Bind it: admin panel → **Campaign** tab → set **Campaign Pack
   Directory** to your directory → **Bind Pack**. That runs the loader's
   validation and reports any problem.

## What makes a pack valid

- `campaign.md` with a non-empty `id` in its front matter.
- A non-empty `id` in every `locations/`, `npcs/`, and `encounters/` file.
- Front matter that parses as YAML — **quote any value containing a
  colon** (`voice: "dry, clipped: never warm"`).

Everything else is advisory.

## Full guidance

- [`docs/authoring-campaign-packs.md`](../../docs/authoring-campaign-packs.md)
  — the complete how-to: every front-matter field, chapters vs. side
  quests, governance, and the SRD-only content rule.
- [`campaign-packs/sable-ravine/`](../sable-ravine) — a complete worked
  example (level 1–3, no chapters).
- [`docs/design.md`](../../docs/design.md) §6.4 — the authoritative spec.
