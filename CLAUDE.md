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

## Mandatory coding conventions (this repo, all languages)

Design doc §12 lays out the AI directives OpenCombatEngine's own repo
enforces. **The *principles* behind those directives are not scoped to
that repo — they apply here too, project-wide, for the same reason they
exist there: so any contributor, human or AI, can jump into unfamiliar
code and find the same discipline every time.** What's scoped to
OpenCombatEngine specifically is the *syntax* — XML doc comments, PascalCase
properties, `I`-prefixed interfaces, `Result<T>`, underscore-prefixed
fields, `UPPER_CASE` constants — because that syntax is idiomatic C#, and
this repo isn't C#. Copying it verbatim into Go or TypeScript would be
wrong twice over: unidiomatic for the language, and confusing for
contributors who know that language's real conventions. Translate the
principle, not the punctuation. Below is the Go translation (the one that
matters today, since Master is the only code so far); a TypeScript
translation should be added here once client code starts.

**Go — translating each §12 directive:**

1. **Documentation, no exceptions.** Every exported identifier
   (`Type`, `Func`, `Method`, exported `const`/`var`) gets a doc comment
   in standard godoc form: starts with the identifier's name, states what
   it does, notes parameter constraints/special values and what error
   conditions mean when non-obvious. Unexported identifiers get a comment
   when their *why* isn't obvious from the name — not required on every
   one, but never skip it on something a future reader would have to
   puzzle out.
2. **Enum-sentinel pattern → typed consts with an explicit unspecified
   zero value.** Go has no enums, but the same defensive intent (never
   let an unset value silently mean something valid) translates directly:
   define a named type, make its zero value an explicit `Unspecified`
   constant, and give it an `IsValid()` (or similar) helper that rejects
   `Unspecified` along with any out-of-range value. This is exactly the
   pattern already used for `CharacterStatus` in
   `protocol/system_engine.proto` (`CHARACTER_STATUS_UNSPECIFIED = 0`) —
   Go-side enums mirror it, e.g.:
   ```go
   type SessionState string

   const (
       SessionStateUnspecified SessionState = ""
       SessionStateJoined      SessionState = "joined"
       SessionStateLeft        SessionState = "left"
   )

   func (s SessionState) IsValid() bool {
       switch s {
       case SessionStateJoined, SessionStateLeft:
           return true
       default:
           return false
       }
   }
   ```
   No `LastValue` sentinel — Go has no way to iterate a type's value
   range the way the C# validation helper does, so the `switch` above
   *is* the range check; keep it exhaustive by hand.
3. **Naming — idiomatic Go, not transliterated C#.** Exported
   `PascalCase`, unexported `camelCase`, no underscore-prefixed fields, no
   `UPPER_CASE` constants (Go constants are `MixedCaps` like everything
   else), no `I`-prefix on interfaces — name interfaces for what they do
   (`Reader`, `TokenValidator`), not what they are. Run `gofmt`/`go vet`;
   don't hand-deviate from what they'd flag.
4. **Error handling — idiomatic `(T, error)`, not an emulated
   `Result<T>`.** Go's built-in `error` return *is* the Result pattern —
   don't build a generic `Result[T]` wrapper on top of it, that would be
   fighting the language to recreate something it already has. Guard
   clauses first; expected failures return a wrapped or sentinel error
   (`fmt.Errorf("...: %w", err)`, or a package-level `var ErrX = errors.New(...)`
   for callers that need to `errors.Is` it); exported functions never
   `panic` — a panic reaching an exported boundary is a bug, not a
   control-flow tool. The one exception: a goroutine handling a client
   connection should `recover()` at its own top level so one malformed
   message can't take down Master for every other player at the table.
5. **TDD — tests written first, same bar as OpenCombatEngine's ≥80%
   coverage.** Table-driven tests are Go's `[Theory]`/`[InlineData]`
   equivalent — use them for anything with more than one interesting
   case. Test naming: `TestFunctionName_Condition_ExpectedResult`, same
   convention as §12, since it reads identically well in Go. Arrange/
   Act/Assert structure; a small builder/fixture helper for constructing
   test inputs when a literal struct would be noisy.
6. **Interface design — this one needs *no* translation.** Interface
   segregation, composition over inheritance, and "define the interface
   at the point of consumption, keep it small" are already how idiomatic
   Go works (`io.Reader` is one method; Go has no inheritance at all, only
   embedding). Don't build a C#-style upfront "all contracts live in one
   package" layer — but *do* keep the actual swappable boundaries
   (system-engine client, image-gen provider, auth provider) behind a
   Go interface with the concrete adapter in its own file/package, which
   is the same intent as OpenCombatEngine's `Core`/`Implementation` split,
   achieved the Go way.
7. **File header — mandatory on every `.go` file, no exceptions:**
   ```go
   // Copyright (c) 2026 James Duane Plotts
   // Licensed under the MIT License. See LICENSE in the repository root.

   package foo
   ```
   Same pattern already used in `protocol/system_engine.proto`.

**Legal compliance (design doc §12) applies verbatim, not just
translated** — no proprietary D&D terms, named characters, non-SRD
monster names, or flavor text, in Go code, comments, test fixtures, or
anywhere else in this repo. See "Legal / content rules" above.

**Git/PR conventions apply verbatim, language-independent:** branches
`feature/add-{name}`, `fix/repair-{issue}`, `docs/update-{section}`,
`test/add-{area}`; commits `type(scope): description` (e.g.
`feat(master): add websocket handshake`). Use this format for new commits
in this repo going forward.

## Working across the OpenCombatEngine boundary

`jamesplotts/opencombatengine` is a **separate repo**, and its own
mandatory conventions as literally written in design doc §12 (XML docs,
`I`-prefixed interfaces, `Result<T>`, underscore fields, etc.) apply when
writing or touching code *in that repo* — that's C# syntax for a C#
codebase. In *this* repo, Master calls OpenCombatEngine only through its
generated Go gRPC client stub (§6.1); never assume its C# internals. The
*principles* those C# conventions express are not left behind at that
boundary, though — see "Mandatory coding conventions" above for how they
carry into this repo's own code.

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
