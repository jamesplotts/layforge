# AI DM Harness — Design Document

**Status:** Pre-implementation design
**License target:** MIT
**Author:** James

## 1. Vision

A chat-based virtual TTRPG platform where an AI acts as Dungeon Master. The system splits into a **Master** node (the only component that talks to an LLM provider and holds authoritative state) and any number of **Slave** clients (thin, no provider credentials, no local rules engine — just UI + a network connection). All humans, including the person running the Master process, are players; none of them are the DM.

Everything beyond the core loop — rendering, rules system, campaign content, image generation, content-maturity policy — is designed as a documented plugin interface Master calls against, not code baked into the core. Only a D&D implementation (via OpenCombatEngine) ships in the reference repo; other systems, viewports, and content are left to the community to build and distribute at their own risk.

**Core design rule threaded through every subsystem:** anything with real mechanical or trust consequences (dice results, rules resolution, PvP, character validation, content policy enforcement) is a gate enforced in code at the tool-call layer. The LLM narrates; it does not adjudicate outcomes that matter. Schemas and protocol messages are designed with more fields than currently needed — a field an early consumer ignores is free; a field missing from an already-shipped protocol version is a migration.

---

## 2. High-Level Architecture

```
                    ┌─────────────────────────┐
                    │         MASTER          │
                    │  (only node with LLM     │
                    │   provider credentials)  │
                    │                          │
                    │  - Session orchestration │
                    │  - Turn-order state      │
                    │    machine               │
                    │  - Authoritative dice    │
                    │  - Campaign state store  │
                    │  - Narrative-transform   │
                    │    pipeline              │
                    │  - Tool-use dispatch     │
                    │  - Whisper (STT)         │
                    └────────────┬─────────────┘
                                 │ WebSocket
                                 │ (typed message channels,
                                 │  protocol_version on every msg)
              ┌──────────────────┼──────────────────┐
              │                  │                   │
      ┌───────▼──────┐   ┌───────▼──────┐   ┌────────▼───────┐
      │ Chat/Player   │   │  Viewport     │   │  Other 3rd-    │
      │ Client (V1)   │   │  Plugin (V3)  │   │  party clients │
      │ - Narrative   │   │  - Talespire- │   │                │
      │   scrollback  │   │    style OR   │   │                │
      │ - Stat panel  │   │  - Gold Box-  │   │                │
      │ - Dice tray   │   │    style      │   │                │
      │ - Push-to-talk│   │                │   │                │
      └───────────────┘   └───────────────┘   └────────────────┘
```

**Pluggable interfaces Master calls against** (each is a documented contract; only the first ships in-repo):

| Interface | Reference implementation (ships in repo) | Community-buildable |
|---|---|---|
| System Engine | OpenCombatEngine (D&D) | Vampire, Pathfinder, etc. |
| Viewport | none — text/grid description only | Talespire-style 3D, Gold Box-style crawler |
| Image-gen provider | x402/ComfyUI on `videogen` | Any other image API |
| Auth/identity provider | Discord OAuth | Other OAuth, bare room codes |
| Maturity tier | `family_friendly`, `standard`, `mature` | Host-authored custom tiers |

---

## 3. Master Node

### 3.1 Responsibilities
- Holds all LLM provider credentials (OpenRouter, or direct Claude/OpenAI keys). Never transmits these to any client.
- Runs the DM session: narrative generation, NPC behavior, campaign-state reasoning.
- Owns the **turn-order state machine** — independent of the LLM. Tracks whose turn it is, skips unconscious/dead characters based on `get_character_status()` from the system engine. The AI is not trusted to reliably track this over a long session.
- Owns **authoritative randomness** — all dice/check resolution happens server-side. Clients only ever receive results to animate, never determine them.
- Runs **Whisper (faster-whisper / whisper.cpp)** for voice-to-text, likely hosted on `videogen` alongside existing ComfyUI GPU work.
- Hosts the **narrative-transform pipeline** (see §7).
- Dispatches **tool-use calls** (see §8) and enforces **governance gates** (see §9) before executing anything with mechanical consequences.
- Implements its own **privileged operator views**, separate from the general "player" view:
  - A firewalled player view for the Master's own human — no visibility into DM secrets other players can't see.
  - A separate **character-review panel** for approving/rejecting imported characters (see §9.4).

### 3.2 Suggested implementation
- **Go** as the primary candidate for the Master process: compiles to a single static binary per target OS/arch with no runtime dependency to install, first-class WebSocket/concurrency support for the connection-heavy Master role, and trivially cross-compiles for Linux/macOS/Windows from one machine — a good fit for an MIT repo other people will self-host without wanting to install a language runtime first. Rust is a reasonable alternative with the same static-binary/cross-platform properties at the cost of steeper development speed; Node/TypeScript is worth considering only if the WebSocket/JS ecosystem tooling outweighs wanting a single compiled binary.
- Deliberately **not** .NET/C#/VB for the Master or client codebases — keeps the harness itself free of a language-runtime dependency for self-hosters, independent of what any individual system-engine plugin happens to be written in (see §6.1 for how this affects the OpenCombatEngine integration specifically).
- Reverse-proxied via existing infrastructure pattern (e.g. Caddy/nginx, same pattern as `ironclad`) for TLS termination and rate-limiting. Cloudflare Tunnel is a reasonable alternative to avoid inbound port-forwarding, especially since self-hosters won't all be comfortable with NAT/firewall config.
- Persistence: repository/DAO abstraction over storage rather than direct file I/O — SQLite as the zero-config default, Postgres as an option for larger/concurrent deployments.

---

## 4. Slave / Client

- No LLM credentials, no rules engine, no local game logic of consequence.
- V1 default client renders:
  - Scrollable narrative chat log (DM prose + in-character narration bubbles — see §7).
  - Schema-driven stat/inventory/spells/actions panel — rendered from whatever fields the active **system engine's character schema** declares, not hardcoded to D&D's HP/AC/spell-slots shape. This matters even in V1 because retrofitting a hardcoded UI later, once other systems and community campaign packs depend on schema-driven rendering, is expensive.
  - Dice tray: grab-and-release animation. Master sends the *authoritative result*; client animates a tumble that lands on that predetermined outcome. Roll spec (die types, pool size, success threshold, botch rules) comes from the system engine, not assumed to be d20-shaped.
  - Push-to-talk button: streaming (chunked) audio to Master, live partial-transcription feedback shown to the speaking player, finalized text becomes a normal narrative-transform input. Voice Activity Detection (Silero VAD or similar) segments speech within the held-button window.
  - No synthesized DM voice output — the DM only communicates via chat text. No ambient/music system — remote players use their own music or silence.
- Any client can send a `safety.flag` message (X-card/veil) at any time — see §9.2.
- Third-party clients (viewports, alternate UIs) are legitimate first-class consumers of the same protocol, not an afterthought.

---

## 5. Protocol Layer

- Single WebSocket connection per client, multiple typed message channels multiplexed over it, rather than separate transports per feature (chat, map state, audio all share one connection).
- Every message carries: `protocol_version`, `message_id` (for ack/dedup/future threading), `timestamp`, `sender_id`, `campaign_id` — even where it looks redundant given connection context, since features like message editing, audit logging, or replay/spectator mode will want to address a specific message independent of arrival order.
- Spec format: **AsyncAPI** (pub/sub equivalent of OpenAPI) rather than prose documentation — since contributors (including AI coding assistants scoping implementations) need a machine-parseable contract, not paragraphs to interpret.
- Message categories (non-exhaustive):
  - `narrative.*` — DM prose, player narrative bubbles, system notes
  - `map.*` — token position/facing, room/map ID, room-adjacency graph (renderer-agnostic; no camera-model assumptions, so both a top-down tactical viewport and a first-person grid-crawler can consume the same feed)
  - `roll.*` — roll requests, authoritative results
  - `audio.*` — chunked upload, transcription results (partial + final)
  - `character.*` — upload, validation results, `pending_review` / `approved` / `rejected`
  - `safety.*` — X-card/veil flags
  - `tool.*` — DM tool-use calls and results (see §8)
  - `system.*` — connection/session lifecycle

---

## 6. Extensibility Interfaces

Two different contract formats are used deliberately, matched to each audience rather than kept uniform for its own sake: **gRPC/protobuf** for the System Engine boundary (§6.1), where typed codegen matters most and the contributor audience skews toward backend/systems developers; **JSON over WebSocket + AsyncAPI** for everything player/client-facing (§5, §6.2–6.5), where no-compile-step, devtools-readable payloads matter more and the contributor audience skews toward web developers. Reference SDKs (thin, pre-generated client packages — at minimum a Python and a TypeScript starter for the System Engine gRPC contract, plus a JS helper for the WebSocket protocol) should ship in or alongside the repo, since a working example to fork does more for uptake than a spec to interpret from scratch.

### 6.1 System Engine
Contract Master calls against for all rules resolution, defined as a **`.proto` file** and exposed over **gRPC** — `protoc` then generates typed client/server stubs in Python, Node, Rust, Java, C#, Go, and more directly from that one file, so a contributor building a new system engine (Vampire, Pathfinder, etc.) gets working typed starter code the moment they run the generator, rather than reverse-engineering a shape from docs. This is the highest-leverage contract to get right for community system-engine contributions specifically.
- `resolve_check(actor, params) -> outcome`
- `apply_effect(actor, effect)`
- `get_character_schema() -> JSON Schema (draft 2020-12)`
- `get_character_status(character) -> active | unconscious | dying | dead | ...`
- `validate_character(character_data) -> [mechanical warnings]`
- `to_json(character)` / `from_json(json) -> character` (import/export pair, versioned via `schema_version` on the character file)

OpenCombatEngine is the D&D reference implementation. A future Vampire/Pathfinder/etc. engine is a separate community-distributed plugin against this same contract — **not shipped in this repo**, since systems vary wildly in how open their mechanical content actually is to redistribute (D&D's OGL/ORC-covered SRD content is broadly reusable; White Wolf's Storyteller System is not; Pathfinder and FATE are open; Call of Cthulhu is not). Game mechanics/algorithms themselves are generally not copyrightable — specific published text and named content is. Contributor docs should call this out per-system explicitly.

**Integration boundary, now that Master isn't .NET (§3.2):** OpenCombatEngine is C#/.NET (see §12) and Master is not, so this can no longer be an in-process library reference — the contract above needs to be realized as an **out-of-process gRPC service**. OpenCombatEngine runs as a lightweight sidecar (a thin gRPC layer wrapping its `Core` interfaces, listening on localhost or a Unix domain socket) that Master calls over local IPC using the generated Go client stub. This is a good fit rather than a compromise, and it doubles as the community-extensibility mechanism: it's the same "system engine is a swappable plugin" pattern already designed, just realized as a real process boundary with a typed, codegen-friendly contract instead of an in-language interface — any future system engine, in any language that has a protobuf compiler (which is nearly all of them), plugs into exactly the same seam a hand-rolled REST contract would have made harder to discover and implement correctly. The event-driven hooks noted in §12 (`TurnStarted`, `DamageDealt`, `CombatEnded`, etc.) surface across this boundary as a gRPC server-streaming feed rather than native C# event subscription.

### 6.2 Viewport
- Master publishes renderer-agnostic grid/coordinate state (`map.*` channel). No 3D engine, no camera model assumed.
- A viewport that lets players *move* their own token needs write-back: `token.move_request` → Master validates → broadcasts accepted state.
- Reference/example viewports are out of scope for the initial repo; the state feed being genuinely renderer-agnostic is the deliverable.


### 6.3 Image-gen provider
- Contract: `generate_scene_image(prompt, context, maturity_tier) -> image_url`.
- Reference implementation: existing x402/ComfyUI setup on `videogen`.
- Maturity tier passed into every call (see §9.5) — image-gen's effective tier may be configured stricter than the text tier for a campaign, but never more permissive by default.

### 6.4 Campaign Pack
Directory-based, markdown + YAML front matter, consistent with the maturity-tier and other content formats:

```
campaign.md          — front matter: title, level range, tone/style tags,
                        pvp_policy, maturity_tier, shared_knowledge policy,
                        lines/veils, contributor/author, content_warnings
                      — body: overview/hooks
locations/*.md        — front matter: id, connections
                      — body: description
npcs/*.md             — front matter: id, stat-block ref, voice/mannerism data
                      — body: personality
encounters/*.md       — pre-authored or generated set-pieces
state.json             — mutable session state (flags, party location,
                          discoveries) — kept separate from static content
                          so campaign packs stay clean/diffable in git
```

- One-line prompts ("run a level 1-3 Keep on the Borderlands-style adventure") are handled by having the DM **generate** a full campaign package of this same shape before play begins, rather than improvising ungrounded. This is the fix for long-session continuity drift — facts get committed to structured data instead of re-improvised each time they're referenced.
- Generated (and any repo-shipped example) content must be original, tone/genre-inspired only — never reproducing actual published module text, named NPCs, or stat blocks from copyrighted material. This constraint applies to what ships in the public repo and to default generation behavior; it does not restrict what a self-hoster privately types into their own campaign files.

### 6.5 Maturity Tiers
Extensible, not a fixed enum — same front-matter + body pattern as other content:

```
maturity_tiers/
  family_friendly.md   — front matter: id, display_name, rank
                        — body: prompt-constraint text injected into DM generation
  standard.md
  mature.md
  <host-custom-tier>.md
```

- Referenced per-campaign via `maturity_tier` in campaign front matter.
- `rank` field allows Master to sanity-check that an image-gen override isn't set more permissive than the text tier (the direction worth guarding against; a stricter image tier than text is harmless).
- Tier files are host-authored and trusted exactly like any other host config or campaign pack — Master doesn't police tier content. This is a documented footgun, not a prevented one.
- This is a policy that shapes model behavior via prompting — not a hard technical content filter with a guaranteed backstop. Docs should say so plainly.
- Explicitly out of scope for design/reference content: tiers or prompt content aimed at eliciting explicit sexual content. The interface being open doesn't preclude a third party building something like that against it on their own hosted instance — that's an inherent property of open interfaces — but it won't be designed or shipped as part of this project.

### 6.6 Auth/Identity
- Discord OAuth as the reference/primary provider — natural fit given voice chat is already Discord-centric; one-click login, inherits avatar/identity.
- Designed as a provider interface generally, so a bare room-code scheme or another OAuth provider could substitute.

---

## 7. Narrative-Transform Pipeline

Player input (typed or voice-transcribed) describing character action/dialogue is **not** shown to other players verbatim — it's transformed into third-person DM-voiced prose before broadcast.

- **Two separate beats, not one LLM call:**
  1. Fast pass: render the player's own stated action/dialogue in-character prose (can use a cheaper/faster model). Streamed to the room quickly so the player isn't staring at a blank screen.
  2. Slower pass: DM/NPC reaction, using full campaign context — this is where the expensive/high-quality model call belongs.
- Character voice/mannerism data (speech patterns, tone) lives as data on or adjacent to the character sheet — the system engine's or a joined "narrative persona" record — so the transform renders consistently rather than generically.
- Raw player input is stored alongside the rendered bubble (for edit/regenerate, campaign log, debugging) even though only the rendered version displays.
- Player can regenerate or edit their own bubble after the fact rather than gating every message behind a pre-broadcast confirmation (protects flow/pacing).
- No dedicated OOC/IC toggle needed in the input box — Discord voice chat carries general out-of-character conversation. A lightweight escape hatch (prefix convention or a small "system note" button) covers rare cases (mic-shy player, "brb" notes).
- Message channel tagging (`narrative` vs `system`) lets alternate clients (e.g. a visual-novel-style viewport) render narrative bubbles differently without touching the core.

---

## 8. DM Tool-Use API

Generalization of "DM calls out to OpenCombatEngine" into the standard function-calling / MCP-style tool pattern, so the system engine is one tool provider among several rather than special-cased plumbing.

Example tool categories:
- System engine tools (`resolve_check`, `apply_effect`, `get_character_status`, etc.)
- Rules/SRD lookup
- Procedural generation (NPC, treasure, encounter)
- Campaign-notes retrieval (RAG over the static campaign-pack content)

Every tool call/result is logged with: caller (DM vs. specific player action), args, any model-provided reasoning/justification, success/failure + reason code. This is both a debugging aid and the data source for features like spotlight-balance tracking (see §9.6) and session history.

**Governance gates (§9) are enforced at this layer** — a tool call that would violate campaign policy (e.g. PvP damage under `pve_only`) fails here, before execution, regardless of what the LLM decided narratively.

---

## 9. Gameplay Governance

All of the following are **campaign-pack-scoped settings**, controllable only by the Master's human operator (not emergent from player character choices), enforced primarily at the tool-call layer rather than left to prompting alone.

### 9.1 PvP Policy
`pvp_policy: pve_only | pvp_allowed | pvp_with_consent`
- Gates whether `apply_effect(target=other_pc, hostile)`-type tool calls are permitted to execute at all.
- `pvp_with_consent` (likely the most commonly wanted default): hostile action against a PC requires either an in-the-moment Master confirmation, or a pre-session per-player opt-in flag.
- This governs the *mechanical* gate only — narrative tension, unpleasant/hostile roleplay short of an actual mechanical attack, is unaffected and should remain fully expressible even under `pve_only`.

### 9.2 Safety Tools (X-Card / Lines & Veils)
- Any player can send `safety.flag` (optional topic tag) at any time.
- Immediately interrupts the current scene for all clients and is injected as a hard constraint into the DM's next generation ("do not narrate X going forward").
- Pre-declared lines/veils can live in campaign-pack front matter as standing constraints from session zero.

### 9.3 Death/Turn Handling
- D&D doesn't need a permadeath *policy* setting — death is already mechanical (unconsciousness, death saves, revival). What's actually required is that the turn-order state machine reliably skips unconscious/dying/dead characters, driven by `get_character_status()` from the system engine — deterministic bookkeeping, not something the DM has to remember.
- Other future system engines may expose real permadeath-vs-not semantics; `get_character_status()` as a contract accommodates that without D&D needing to configure anything.

### 9.4 Character Import Veto
- `validate_character()` (system engine, mechanical) + DM narrative-flag pass (freeform, e.g. lore-breaking backstory content) both surface to a **privileged review panel** on the Master client only.
- Master's human operator approves/rejects/requests changes before an imported character enters shared play (`character.pending_review` → `approved`/`rejected`).
- Uploaded characters are personal **library** data (keyed to the player's account/Discord ID), not campaign state — joining a campaign snapshots the library character into that campaign's session state, so mutations (damage, XP, loot) in one campaign don't bleed into another.

### 9.5 Content Maturity
See §6.5. Governs both DM text generation and image-gen calls; enforced via prompt constraint, documented as behavior-shaping rather than a hard filter.

### 9.6 Spotlight Balance (soft signal, not a hard gate)
- Turns-since-last-spotlight per character, tracked from tool-use log data, surfaced to the DM's context so it can proactively prompt quieter players — quality-of-life feature enabled by the DM having perfect bookkeeping a human GM would have to rely on memory for.

### 9.7 Knowledge Scoping
`shared_knowledge: strict | party_omniscient`
- Governs whether split-party/private-perception narration is scoped per-player or broadcast to everyone regardless of character presence.
- Persisted chat log entries must carry the same visibility scope they had live (public / private-to-player-id) so reviewing history doesn't leak something that was private in the moment.

### 9.8 Out of scope for technical enforcement
- **Metagaming via side-channel voice chat** — since Discord conversation happens entirely outside the system, this isn't enforceable at the protocol layer. Worth a documented table-agreement convention, not a technical feature.
- **Party decision quorum** (unanimous/majority/first-mover on split decisions) — left to DM narrative judgment rather than a hard setting; noted as a known gap rather than assumed handled.

---

## 10. Persistence & Data Model

- Repository/DAO abstraction over storage; SQLite default, Postgres optional.
- Split between **static/reference content** (campaign packs, maturity tiers, system-engine data — read-mostly, git-diffable) and **mutable session state** (`state.json`-style flags, live character sheet mutations, chat/event log).
- Campaign memory itself is two-tier: static content retrieved via RAG/tagged lookup as needed, plus session-state that's authoritative and mutated only via tool calls — not trusted to "the model remembered."
- Chat/event log is the same durable narrative event stream already needed for summarize-and-compact context management, exposed as a pageable/searchable read surface for players reviewing history. Live partial-transcription text is explicitly ephemeral and never written to the durable log — only finalized transcriptions (which become narrative bubbles) persist.

---

## 11. Roadmap

**V1**
- Chat scrollback narrative UI with narrative-transform pipeline
- Schema-driven stat/inventory/spells/actions panel
- Dice tray (authoritative server roll, cosmetic client animation), roll spec from system engine
- Streaming push-to-talk voice input (Master-side Whisper, VAD, live partial transcription)
- Discord OAuth
- Remote-reachable Master/Slave over WebSocket, room tokens, TLS via reverse proxy
- Character upload/import with schema validation + Master veto/review panel
- Chat log / history review
- PvP policy, safety tools, death/turn handling, content maturity tiers — all as campaign-pack-scoped governance settings enforced at the tool layer
- DM tool-use API (generalized, beyond just the system engine)
- Protocol versioned from message 1

**V2**
- Image-gen as a pluggable provider contract (ComfyUI/x402 reference implementation)
- Viewport channel formalized (renderer-agnostic map/token state) — no viewport shipped, just the documented feed

**V3**
- No engine committed in-repo. Protocol already carries what either a Talespire-style tactical 3D viewport or a Gold Box/SSI-style first-person grid-crawler would need (token position, facing, room-adjacency graph), built by whoever takes it on — James is planning the Gold Box-style one himself.

---

## 12. OpenCombatEngine — AI Directives

Source: `jamesplotts/opencombatengine`, `.ai/project-context.md` (pulled 2026-09-01). These are the mandatory coding standards and architectural patterns for OpenCombatEngine itself — the reference D&D system-engine implementation this harness plugs into. Claude Code should treat this as binding whenever it touches OpenCombatEngine code or writes new code meant to interoperate with it as a system-engine plugin.

### Core Purpose & Principles
- Interface-driven combat engine library for D&D 5e-SRD-compatible games. .NET 8.0, C# 12, cross-platform.
- Test-Driven Development, minimum 80% coverage.
- License: MIT for code; OGL 1.0a compliance for game mechanics.
- Key principles: Interface-First (define contracts before implementations), Open-Source Friendly, AI-Assisted Development, Legally Compliant (SRD-only, no proprietary D&D content), Extensible (homebrew/variant rules supported).

### Mandatory Coding Conventions

**1. XML Documentation — enforced, no exceptions.** Every public, protected, and internal member requires XML docs: `<summary>` starting with an action verb (Creates, Validates, Calculates, Gets, Sets), `<param>` with purpose/valid ranges/special values, `<returns>`, all `<exception>`s documented, `<remarks>` for complex logic, examples for non-obvious usage.

**2. Enum pattern — critical, always required.** Every enum starts with `Unspecified = 0` and ends with a sentinel `LastValue`, each member has an XML summary, and a validation helper checks `value > Unspecified && value < LastValue`.

**3. Naming conventions:**
- Private fields: underscore prefix (`_logger`)
- Constants: `UPPER_CASE`
- Properties/Methods: `PascalCase`
- Parameters: `camelCase`, no prefix
- Local variables: `camelCase`
- Interfaces: always `I` prefix

**4. Error handling — Result pattern, not exceptions.** Public APIs return `Result<T>` (`IsSuccess`, `Value`, `Error`, with `Success()`/`Failure()` factory methods) rather than throwing. Guard clauses first, business-rule validation with logging, expected `GameRuleException`s caught and converted to `Result.Failure`, truly unexpected exceptions logged and rethrown.

**5. TDD — tests written before implementation.** Test naming: `MethodName_Condition_ExpectedResult`. Use `[Theory]`/`[InlineData]` for parameterized coverage (e.g. resistance/immunity/vulnerability cases). Arrange/Act/Assert structure, builder pattern for test object construction.

**6. Interface design rules:**
- Interface Segregation — keep interfaces focused, compose larger contracts (`ICreature : IIdentifiable, INameable, IHasHitPoints, IHasAbilityScores, ICanTakeActions`)
- Separate concerns into specific interfaces (`IHasHitPoints`, etc.)
- Generic interfaces for flexibility (`IModifiable<T>`)
- Prefer composition over inheritance (`ISpellcaster : ICreature`)

**7. File header — mandatory on every `.cs` file, no exceptions:**
```csharp
// Copyright (c) 2025 James Duane Plotts
// Licensed under MIT License for code
// Game mechanics under OGL 1.0a
// See LEGAL.md for full disclaimers

using System;
// other using statements...

namespace OpenCombatEngine.Core;
```

### Project Structure
```
OpenCombatEngine/
├── .ai/                                    # AI assistant context files
│   ├── project-context.md                 # This file
│   ├── current-tasks.md
│   ├── architecture-decisions.md
│   └── code-examples.md
├── src/
│   ├── OpenCombatEngine.Core/              # Interfaces and enums ONLY
│   │   ├── Interfaces/{Creatures,Actions,Combat,Dice}/
│   │   ├── Enums/
│   │   └── Results/                        # Result<T> pattern
│   ├── OpenCombatEngine.Implementation/    # Concrete implementations
│   │   └── {Creatures,Actions,Combat,Dice}/
│   └── OpenCombatEngine.Content/           # Content import system
│       ├── Importers/                      # 5eTools, Open5e, FoundryVTT, FightClub5, Native (.ocf)
│       ├── Schemas/opencombat-v1.schema.json
│       └── Mappings/
├── tests/ (Core.Tests, Implementation.Tests, Content.Tests, Integration.Tests)
├── docs/ (architecture/, api/, content-formats/, legal/)
└── examples/
```

**Layering rule directly relevant to this harness:** interfaces live in `Core` only; implementation never goes in `Core`. When the harness's tool-use API calls into OpenCombatEngine as its D&D system-engine implementation, it should be calling against `OpenCombatEngine.Core` interfaces (`ICreature`, `IDiceRoller`, `IAction`, etc.), not implementation-project concretes directly — this is what keeps the harness's system-engine contract (§6.1 of this document) swappable for a future non-D&D engine.

### Legal Compliance (CRITICAL — governs both this engine and any harness content built on top of it)

**Allowed (SRD content):** basic mechanics (ability scores, saving throws, advantage/disadvantage), generic creature statistics, spell *mechanics* (not descriptions), generic class features as mechanics, standard conditions, action economy.

**Forbidden (proprietary content):** the terms "Dungeons & Dragons"/"D&D"/"WotC", specific named characters (Drizzt, Elminster, etc.), non-SRD monster names (Beholder, Mind Flayer, etc.), spell flavor-text descriptions (mechanics only), setting-specific content (Forgotten Realms, etc.), artwork/visual assets.

This directly reinforces the harness's own campaign-pack and maturity-tier content rules (§6.4–6.5 of this document): anything generated for a campaign pack or shipped as example content must stay on the SRD-legal side of this same line.

**Content import strategy — "we provide the pipes, users provide the water."** `IContentImporter` interface (`CanImport(format)`, `ImportAsync(Stream) -> GameContent`) with a factory (`ContentImporterFactory`) dispatching by file extension. Supported formats: 5e.tools JSON (`.5et`/`.5etools`), Open5e API JSON, Foundry VTT (`.fvtt`), FightClub5 XML (`.fc5`), and the native OpenCombat Format (`.ocf`). The engine explicitly does not validate legality of imported content — that responsibility is the user's, documented in the interface's own XML remarks.

Native format envelope:
```json
{
  "format": "opencombat/v1",
  "version": "1.0.0",
  "source": "user-content",
  "legal": "OGL-1.0a",
  "content": { "creatures": [], "spells": [], "items": [], "actions": [] }
}
```

This `.ocf` native format is the natural candidate for the character-upload/import JSON schema referenced in §6.1 and §9.4 of this document (`get_character_schema()`, `to_json`/`from_json`) — worth using it directly rather than inventing a parallel schema, since OpenCombatEngine already defines and owns it.

### Architecture Decisions
- **Result pattern** for all error handling (see above).
- **Immutable DTOs** — records, e.g. `record DamageRoll(int Amount, DamageType Type, bool IsCritical = false, string Source = "")`.
- **Event-driven combat** — standard C# events on `ICombatManager` (`TurnStarted`, `DamageDealt`, `CombatEnded`, etc.), internal to OpenCombatEngine itself. A natural hook point for the harness's tool-use logging (§8) and turn-order state machine (§3.1, §9.3) — but since Master is not .NET (§3.2), it can't subscribe to these natively. The sidecar service described in §6.1 should re-expose them as a stream Master can consume (e.g. relaying each event as a small message over its gRPC/SSE feed) rather than Master polling for state changes.

### Git / PR conventions
- Branches: `feature/add-{name}`, `fix/repair-{issue}`, `docs/update-{section}`, `test/add-{area}`.
- Commits: `type(scope): description` (e.g. `feat(combat): add initiative tracking`).
- PR checklist: all tests pass, coverage ≥80%, XML docs complete, no compiler warnings, follows all conventions above.

### When Claude Code is providing code for OpenCombatEngine

**Always:** copyright header on every file; XML docs on every member; Unspecified/LastValue enum pattern; tests written first (TDD); `Result<T>` for public APIs; exact naming conventions; guard-clause input validation; appropriate logging; interfaces confined to the Core project.

**Never:** skip XML documentation; use proprietary D&D terms; include descriptive/flavor text for game content; put implementation in Core; throw exceptions from public methods; use `var` for non-obvious types; create enums without the sentinel pattern.

> "Every line of code should be written as if the person maintaining it is a violent psychopath who knows where you live. Document accordingly." — project-context.md
