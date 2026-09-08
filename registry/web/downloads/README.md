# Campaign Pack Downloads

Two committed bundles the [layforge.org](https://layforge.org) homepage
links to:

- **`campaign-pack-library.zip`** — 21 ready-to-run packs. Master's admin
  panel downloads this one (Campaign tab → *Install the campaign pack
  library from layforge.org*), or unzip it into a `-campaign-packs-dir`
  by hand.
- **`campaign-pack-template.zip`** — the single `TEMPLATE/` authoring
  skeleton (same as `campaign-packs/TEMPLATE/` in the repo). Unzip it,
  rename the folder to your pack's slug, and follow
  [`docs/authoring-campaign-packs.md`](../../../docs/authoring-campaign-packs.md).
  Not meant for the bulk installer — it's a starting point to copy.

## campaign-pack-library.zip

## What's in it

21 self-contained campaign packs, each a `<slug>/` directory holding a
`campaign.md` plus `locations/`, `npcs/`, and `encounters/` markdown —
the same directory shape Master's loader (`campaignpack.LoadPack`) and
the *Bind Pack* button expect. Levels 2–10, mostly 3–8, four chapters
each.

- **The Thornwell Verdict** (L3–8) — `framed-in-havenbrook`
- **Harvest Hollow** (L3–8) — `harvest-hollow`
- **The Beast of Blackwater** (L3–8) — `the-beast-of-blackwater`
- **The Crown of Ash** (L3–8) — `the-crimson-blades`
- **The Crown of Stars** (L3–8) — `the-crown-of-stars`
- **The Crown of Whispers** (L3–8) — `the-crown-of-whispers`
- **The Debt of Ash** (L3–8) — `the-debt-of-mercy`
- **The Storm's Eye** (L3–8) — `the-eye-of-the-storm`
- **The Iron Covenant** (L2–6) — `the-frontier-garrison`
- **The Weight of Heroism** (L3–8) — `the-guardians-mistake`
- **The Unraveling** (L3–8) — `the-heir-within`
- **The Lion's Burden** (L3–8) — `the-lionhearts-lie`
- **The Masquerade of Shattered Faces** (L3–8) — `the-masquerade`
- **Iron and Ash: The Debt of Callowgate** (L3–8) — `the-mayors-debt`
- **Kaelthorne: The Island of Teeth and Thorn** (L2–10) — `the-monster-isle`
- **The Order of the Veil** (L3–8) — `the-order-of-the-veil`
- **The Fabricated Ashes** (L3–8) — `the-prophecy-of-ashes`
- **The Ruins of Calandris** (L2–8) — `the-ruined-city`
- **The Shattering** (L3–8) — `the-shattering`
- **The Accidental War** (L3–8) — `the-spark-of-war`
- **The Weight of Water** (L3–8) — `the-three-terrible-options`

## How it was built

Generated with a local LLM through Master's own
`POST /api/campaign-packs/generate` + `/save` admin endpoints (unmodified)
— the same pipeline the admin panel's "Generate a campaign pack with AI"
button uses. Prompts were pre-filtered for proprietary terms before
generation; every pack was re-validated through `LoadPack` and scanned
for brand/IP terms before being bundled here. Content is original,
tone-inspired only, SRD 5.1-legal — see [`CLAUDE.md`](../../../CLAUDE.md)
§ "Legal / content rules" and design doc §6.4 / §12.

## Installing without the admin panel

```
unzip campaign-pack-library.zip -d /path/to/your/campaign-packs-dir
```

Then bind any one of them from the admin panel's Campaign tab, or point
Master's `-campaign-packs-dir` at that directory.

## Regenerating

Both zips are committed build artifacts, not generated at deploy time.

`campaign-pack-template.zip` — from the repo's `campaign-packs/` directory:

```
zip -rq campaign-pack-template.zip TEMPLATE -x '.*'
```

`campaign-pack-library.zip` — from the directory holding the `<slug>/`
pack directories:

```
zip -rq campaign-pack-library.zip */ -x '.*'
```

Move the result to `registry/web/downloads/` in either case.
