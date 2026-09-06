// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

// Package terms holds the version and text of Layforge's Host/operator
// and player disclaimers (see internal/admin and internal/server's own
// gates, which require accepting the current Version before either side
// can use Master normally). A small, dependency-free leaf package —
// both internal/admin and internal/server need the same Version without
// either importing the other through it.
//
// IMPORTANT: OperatorText and PlayerText are placeholder disclaimer
// language drafted for this project, not text reviewed by a lawyer. A
// self-hoster relying on this for real liability protection (or the
// project maintainer, before treating acceptance of this text as
// legally meaningful) should have it reviewed before depending on it.
package terms

// Version identifies the current text of OperatorText/PlayerText.
// Bumping it invalidates every previously recorded acceptance (both the
// Host's own admin-panel acceptance and every player's per-connection
// acceptance) — change it whenever OperatorText or PlayerText actually
// changes in a way that matters, not on a cosmetic wording tweak.
const Version = "2026-09-07"

// OperatorText is shown to the Host running Master, once, before the
// admin panel (and, transitively, the player-facing listener — see
// internal/server's dispatch gate) will operate normally.
const OperatorText = `Layforge Host Terms & Disclaimer (` + Version + `)

You are about to run a self-hosted Layforge Master. By continuing, you
acknowledge:

1. You are the operator of this instance. Layforge (the project) does
   not run, monitor, or moderate your instance — you are solely
   responsible for its configuration, the content it generates or
   serves, and compliance with any laws or third-party terms that apply
   to you (including any LLM provider's own usage policy, if you've
   configured one).
2. This software integrates one or more third-party AI models. Their
   output is generated text, not verified or guaranteed by this project
   to be accurate, appropriate, or free of errors — you are responsible
   for how you configure and moderate what your table sees.
3. Game-mechanics content shipped with this project is intended to stay
   SRD-legal (see the design document) but is provided as-is, with no
   warranty of any kind, to the fullest extent permitted by law.
4. Any credentials you configure (LLM provider API keys, etc.) are your
   own responsibility to keep secure; this software is designed never to
   transmit them to a connecting player, but you are running it on your
   own infrastructure and at your own risk.

If you do not agree, do not continue running this instance.`

// PlayerText is shown to each player joining a Master instance, once
// per connection, before they can chat or take any in-game action.
const PlayerText = `Layforge Player Disclaimer (` + Version + `)

You're about to join a self-hosted tabletop game run by its own Host —
not a service operated by the Layforge project itself. Before you
continue:

1. The Dungeon Master in this game is an AI. Its narration and rulings
   are generated text — it can be wrong, inconsistent, or produce
   content the Host didn't intend. It is not a substitute for a human
   referee's judgment, and nothing it says is guaranteed accurate.
2. This Host's own instance, configuration, and moderation are entirely
   their own responsibility, not this project's. Content maturity and
   house rules are whatever this Host has configured.
3. Dice results and rules outcomes shown to you are computed
   server-side and are authoritative for this session, but the overall
   experience (including AI narration) is provided as-is, with no
   warranty of any kind.

If you do not agree, do not join this game.`
