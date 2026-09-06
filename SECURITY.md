# Security Policy

Layforge is a self-hosted platform: a Host runs their own Master
process and holds their own LLM provider credentials (see
[`docs/design.md`](docs/design.md) §3.1). A vulnerability here can
affect every self-hosted table running an affected version, as well as
the public [layforge.org](https://layforge.org) directory
([`registry/`](registry/)) — please report privately rather than
opening a public issue or PR.

## Reporting a vulnerability

Use GitHub's private vulnerability reporting on this repository:
**[Security → Report a vulnerability](https://github.com/jamesplotts/layforge/security/advisories/new)**.
This opens a private advisory only the maintainer (and anyone you add)
can see — it never becomes a public issue until a fix is out and the
advisory is deliberately published.

Please include:

- Which component is affected — `master/` (a self-hosted Host's own
  process), `registry/` (the public layforge.org directory service), or
  the protocol/`docs/design.md` itself.
- Steps to reproduce, or a minimal proof of concept.
- What you think the actual impact is (e.g. "lets one player affect
  another's character despite `pve_only`," "crashes the registry with a
  crafted request," "lets the DM AI be manipulated into X").

## Scope

**In scope**: `master/` (the Go backend and its admin panel),
`registry/` (the layforge.org service), the protocol definitions under
`protocol/`, and the reference web clients (`master/web/`,
`master/admin-web/`, `registry/web/`).

**Out of scope, but still worth reporting upstream**:
[`jamesplotts/opencombatengine`](https://github.com/jamesplotts/opencombatengine)
is a separate repository with its own security posture — please report
issues specific to that gRPC sidecar there instead.

**Not a vulnerability by itself**: an LLM producing an unexpected or
"in-character" narrative response is expected model behavior, not a
security bug. What *is* in scope: any prompt-injection-style input that
gets a DM tool call to bypass a real code-level gate (PvP policy,
character ownership, an amount/magnitude bound, or similar — see
`master/internal/server`'s own tool-dispatch gates) rather than just
producing odd narration. See CLAUDE.md's "gates over prompting" for
this project's own design intent on that boundary.

## Supported versions

This project doesn't yet have tagged releases — please report against
the current `main` branch. Once versioned releases exist, this section
will list which lines still receive security fixes.
