# CLAUDE.md — Layforge

Read [`docs/design.md`](docs/design.md) first — it's the authoritative
design document for this project (architecture, protocol, extensibility
interfaces, governance model). This file is conventions/directives for
writing code in *this* repo; it doesn't restate the design.

## Non-negotiable design rules

- **Gates over prompting.** Anything with mechanical or trust consequences
  (dice results, rules resolution, PvP, character validation, content
  policy) must be enforced in code at the tool-call layer, before the
  effect executes — never left to the model deciding correctly. If you're
  adding a tool the DM can call and it has a mechanical consequence, ask
  "what governance gate (§9 of the design doc) checks this before it
  runs?" before writing the handler.
- **Master never leaks credentials or secrets to clients.** LLM provider
  keys, and anything from the Master's privileged/operator views, must
  never be serialized into a message a Slave client receives.
- **Authoritative state lives on Master.** Dice results, turn order, and
  rules outcomes are computed server-side and sent to clients as results
  to render, never as something a client computes or can override.
- **Protocol messages carry `protocol_version`, `message_id`, `timestamp`,
  `sender_id`, `campaign_id`** on every message, even when a field looks
  redundant given connection context (see design doc §5 for why). Don't
  drop these to save bytes.
- **Design fields forward.** Schemas and protocol messages should have
  more fields than the current feature strictly needs, when a near-future
  consumer is foreseeable — an unused field is free, a missing field in an
  already-shipped protocol version is a migration.
- **System engine calls go through the gRPC contract, never a
  language-specific shortcut.** Master talks to OpenCombatEngine (or any
  future system engine) only through the `.proto`-defined interface
  (design doc §6.1), even though today only one engine exists. Don't take
  a shortcut that assumes D&D/OpenCombatEngine specifically inside Master
  code that isn't the system-engine adapter itself.

## Legal / content rules

- No proprietary D&D terms ("Dungeons & Dragons", "D&D", "WotC"), named
  characters, non-SRD monster names, or spell/setting flavor text in any
  code, example content, campaign pack, or generated output shipped in
  this repo. SRD mechanics only — see design doc §6.4 and §12.
- Example/reference campaign packs and maturity tiers committed to this
  repo must be original content, tone-inspired only.
- Don't design or ship maturity-tier content aimed at eliciting explicit
  sexual content (design doc §6.5) — this is a hard line for what ships
  here, independent of what the open interface permits third parties to
  build.

## Working across the OpenCombatEngine boundary

`jamesplotts/opencombatengine` is a **separate repo** with its own
mandatory conventions (XML docs on every member, `Unspecified`/`LastValue`
enum sentinel pattern, `Result<T>` error handling, file-header copyright
block, TDD-first, interfaces confined to `Core`, etc. — reproduced in full
in design doc §12). Those conventions apply when writing or touching code
*in that repo*. In *this* repo, Master calls OpenCombatEngine only through
its generated Go gRPC client stub (§6.1) — never assume its C# internals,
and don't import its conventions into Go/TS code here.

## Language/stack choices already made

- **Master**: Go. Single static binary, no runtime dependency for
  self-hosters. Don't introduce a second Master-side language runtime
  without discussing it first — this was a deliberate call (design doc
  §3.2), not a default.
- **Client-facing protocol**: JSON over WebSocket, spec'd in AsyncAPI
  (`protocol/`). No compile step, devtools-readable.
- **System Engine boundary**: gRPC/protobuf (`protocol/`), because typed
  codegen matters more than devtools-readability for that audience.
- Persistence goes through a repository/DAO abstraction, not direct file
  I/O — SQLite default, Postgres optional (design doc §10).

## General

- Don't add features, viewports, image-gen backends, or auth providers
  beyond what's scoped for the current roadmap phase (design doc §11).
  The plugin interfaces exist so those can be built *against* this repo
  later, not necessarily *in* it now.
- When a design-doc section and the code seem to disagree, treat the
  design doc as authoritative and flag the discrepancy rather than
  silently picking one.
