# V1 Web Client

The default Slave client (design doc §4): no LLM credentials, no rules
engine, no local game logic — renders what Master sends over the
protocol. Plain HTML/CSS/JS, no build step, matching the protocol's own
"devtools-readable" ethos (design doc §6).

## Status

Working: join a campaign (`system.connect` handshake), narrative
scrollback with the fast-pass narrative-transform pipeline
(`narrative.player_input` → `narrative.player_bubble`), the safety-flag
control (`safety.flag` → `safety.flag_broadcast`), and automatic history
paging on join (`log.history_request`/`log.history_response`).

Not implemented — Master doesn't have anything for these to talk to yet:
schema-driven stat/inventory/spells/actions panel (no system engine),
dice tray (no `roll.*`), push-to-talk voice input (no audio pipeline).

## Running

Needs a Master to connect to. Either:

```
cd ../../master && go run . -web-dir=../clients/web -llm-url=http://<ollama-host>:11434
```

then open `http://localhost:8080/`, or open `index.html` directly and
enter Master's WebSocket URL on the join screen (e.g.
`ws://localhost:8080/ws`) if you're running Master separately without
`-web-dir`.

## Known limitations

- **History only pages forward.** `log.history_request` supports
  `after_sequence` (design doc §10), not "give me the most recent N" or
  "load earlier" — so on join this client walks every page from the
  *beginning* of the campaign automatically. Fine for small test
  campaigns; a real "jump to recent, scroll up for more" UX needs a
  backend change first (see `internal/store` in `master/`).
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
