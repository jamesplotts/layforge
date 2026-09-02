# V1 Web Client

The default Slave client (design doc §4): no LLM credentials, no rules
engine, no local game logic — renders what Master sends over the
protocol. Plain HTML/CSS/JS, no build step, matching the protocol's own
"devtools-readable" ethos (design doc §6).

Lives under `master/` and is served by Master itself by default (see
`../README.md`'s Running section) — plain files on disk, not embedded
into the Go binary, specifically so a table can restyle the interface
(swap this directory's `style.css`, fork `index.html`/`app.js`) without
touching Go or rebuilding anything. That's a packaging/distribution
choice, not a coupling one: this stays a normal protocol client, with no
special access to Master beyond what any other client connecting to
`/ws` has (design doc §4 — "third-party clients are legitimate
first-class consumers, not an afterthought").

## Status

Working: join a campaign (`system.connect` handshake), narrative
scrollback with the fast-pass narrative-transform pipeline
(`narrative.player_input` → `narrative.player_bubble`), the safety-flag
control (`safety.flag` → `safety.flag_broadcast`), and history: on join,
the most recent page loads automatically (design doc §10's tail default,
not the campaign's first message), with a "Load earlier history" button
to page further back (`log.history_request`'s `before_sequence`).

Not implemented — Master doesn't have anything for these to talk to yet:
schema-driven stat/inventory/spells/actions panel (no system engine),
dice tray (no `roll.*`), push-to-talk voice input (no audio pipeline).

## Running

From `master/`:

```
go run . -llm-url=http://<ollama-host>:11434
```

then open `http://localhost:8080/` — Master serves this directory at `/`
by default. To point a copy of this client at a *different* Master
instead (e.g. a remote one, or while comparing behavior across builds),
open `index.html` directly and enter that Master's WebSocket URL on the
join screen (e.g. `wss://some-other-host/ws`).

## Skinning

Because this is served straight from disk, restyling doesn't require
Go, a rebuild, or even a restart:

- **Colors/spacing/fonts** — edit `style.css`. Everything's driven off
  the CSS custom properties at the top of the file (`--bg`, `--accent`,
  `--bubble-bg`, etc.) — change those first before touching individual
  rules.
- **Layout/markup** — edit `index.html`.
- **Behavior** — edit `app.js`; it's plain functions and DOM calls, no
  framework or build tooling to fight.

Master doesn't cache these files beyond what `http.FileServer` does — a
browser reload picks up the change immediately. To run a genuinely
different skin rather than editing in place, copy this whole directory
and point `-web-dir` at the copy instead.

## Known limitations

- **Scroll position isn't preserved across "Load earlier."** New content
  gets prepended above what's visible, but the client doesn't adjust
  scroll offset to compensate — the viewport can jump. A real
  implementation would anchor scroll to the insertion point.
- **No reconnect.** If the WebSocket drops, the status line says
  "disconnected" and that's it — reload to rejoin.
- **`sender_id` is just the character name you type in.** There's no
  auth/account system yet (design doc §6.6's Discord OAuth isn't
  implemented), so nothing stops two people from joining as the same
  name.
- Hand-written, not generated. Design doc §6 envisions a proper JS
  reference SDK against `protocol/asyncapi.yaml`; this predates that and
  has to be kept in sync with the protocol by hand (see
  `PROTOCOL_VERSION` in `app.js`).
