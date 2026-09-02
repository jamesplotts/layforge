// Copyright (c) 2026 James Duane Plotts. Licensed under the MIT License.
// See LICENSE in the repository root.
//
// The dice tray's skin registry (see dice.js) — the only file a
// community skin needs to touch, plus optional PNG assets alongside it.
// No JS or 3D-geometry knowledge required to add one: append an entry
// below.
//
// Fields:
//   id            unique string, used in the skin <select> and localStorage.
//   label         display name.
//   baseColor     CSS color string — the die's base material color, and
//                 the fallback if baseTexture isn't set or fails to load.
//   baseTexture   optional path to a PNG for the die's surface (marble,
//                 wood grain, brushed metal, whatever) — applied as a
//                 standard UV-mapped texture across the whole mesh. Not
//                 unwrapped per-face, so expect some stretching; that's
//                 an acceptable v1 tradeoff for a general material look,
//                 not a precise per-facet texture.
//   numberTexture optional path to a PNG for custom-drawn digits, laid
//                 out as a 5-column x 4-row grid (cell (n-1) is number
//                 n, row-major, top-to-bottom) — full creative control
//                 per glyph. Leave null to have digits rendered from
//                 `font`/`numberColor` at runtime instead (the easy
//                 path — no image editor required).
//   font          CSS font shorthand (e.g. "700 64px Georgia, serif"),
//                 used only when numberTexture is null.
//   numberColor   CSS color for font-rendered digits, used only when
//                 numberTexture is null.
//
// None of the three built-in skins below ship real baseTexture/
// numberTexture art (this repo has no way to author PNG textures) —
// they only exercise the color+font path. The texture code paths are
// fully implemented and tested against a real PNG during development;
// they're just waiting on actual community-contributed art.

export const SKINS = [
  {
    id: "ivory",
    label: "Ivory",
    baseColor: "#e2d8c3",
    baseTexture: null,
    numberTexture: null,
    font: "700 72px Georgia, serif",
    numberColor: "#2a2418",
  },
  {
    id: "obsidian",
    label: "Obsidian",
    baseColor: "#232028",
    baseTexture: null,
    numberTexture: null,
    font: "700 72px Georgia, serif",
    numberColor: "#ff6b6b",
  },
  {
    id: "emerald",
    label: "Emerald",
    baseColor: "#1f8f5f",
    baseTexture: null,
    numberTexture: null,
    font: "700 72px Georgia, serif",
    numberColor: "#fff6d8",
  },
];

export const DEFAULT_SKIN_ID = "ivory";
