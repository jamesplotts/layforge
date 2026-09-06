# Layforge Registry

The Go service behind `layforge.org`: a small, standalone, self-hostable
public directory of currently-joinable Layforge campaigns, plus the
project's own homepage (plain HTML/JS at `web/`, served off the same
binary). Its own Go module (`go.mod`), deliberately separate from
`master/`'s — this is a genuinely independent service, not a Master
subsystem, matching the same "single static binary, no shared runtime
dependency" posture [`CLAUDE.md`](../CLAUDE.md) already states for
Master.

**No central Layforge server your game depends on.** A self-hosted
Master never needs a registry to function — this is purely opt-in
discovery, layered on top. `master/internal/registry`'s own heartbeat
client is what Master uses to publish to one of these; see that
package's doc comment for the exact opt-in gate (a registry URL alone
lists nothing — each campaign additionally needs its own
`RegistryListed`/`JoinAddress` set via the admin panel's Campaign tab).

## What it stores

Nothing durable. `internal/lobby.Store` is a plain in-memory,
mutex-guarded map — the same ephemeral, TTL-based pattern Master itself
already uses for its own short-lived in-memory state (turn order, audio
stream buffers, character-creation sessions). A listing that stops
being heartbeated within `-ttl` (default 90s) is excluded from reads and
eventually swept; a registry restart loses every listing, and every
Master heartbeating it transparently re-registers on its very next tick
(a 404 on heartbeat is treated as "register again," not a fatal error —
see `master/internal/registry`'s own `HeartbeatLoop`).

## API

- `POST /api/v1/listings` — create a listing. Body: `adventure_name`,
  `min_level`, `max_level`, `players_joined`, `player_slots`,
  `password_protected`, `join_url`, `campaign_id`. Response:
  `{"id", "token"}` — `token` is never retrievable again after this
  call and authorizes every later call against this listing.
- `PUT /api/v1/listings/{id}` — heartbeat/update. Body: same fields
  plus `token`. `404` if the registry no longer knows `id` (expired or
  restarted — the caller should just `POST` again); `403` if `token`
  doesn't match.
- `DELETE /api/v1/listings/{id}` — deregister. Body: `{"token"}`.
- `GET /api/v1/listings` — public, no auth, no `token` in the response
  ever. Returns `{"listings": [...]}`.

There is deliberately no same-origin/CORS-style check on the write
endpoints the way `master/internal/admin`'s local operator panel has
one — this is a genuinely public, cross-host, server-to-server API any
self-hosted Master may call; authorization is the per-listing `token`
alone, the same trust model a lot of small public directory services
use (low-stakes data, real ownership check, no accounts).

## Running

```
go build ./...
./registry -addr :8091 -web-dir ./web -ttl 90s
```

Flags: `-addr` (default `:8091`), `-web-dir` (default a `web` directory
next to the binary; empty disables serving the site, leaving only the
JSON API), `-ttl` (default 90s — keep this comfortably above whatever
heartbeat interval self-hosted Masters use), `-sweep-interval` (default
30s — pure memory hygiene, `GET /api/v1/listings` already excludes
expired entries regardless of whether a sweep has run).

## Real deployment (layforge.org)

**Live**: `https://layforge.org` is this exact deployment, running on
this project's own home network, mirroring an already-working pattern
(`promptdna.org`) on the same infrastructure — documented here so it's
reproducible, not tribal knowledge:

- The `registry` binary + `web/` run on one LAN machine (`videogen`),
  bound to a LAN-reachable port (`8091`), as a systemd service:

  ```ini
  [Unit]
  Description=Layforge Registry
  After=network.target

  [Service]
  Type=simple
  User=jamesp
  WorkingDirectory=/home/jamesp/projects/layforge-registry
  ExecStart=/home/jamesp/projects/layforge-registry/registry -addr :8091 -web-dir /home/jamesp/projects/layforge-registry/web
  Restart=on-failure
  RestartSec=5

  [Install]
  WantedBy=multi-user.target
  ```

- A separate machine (`ironclad`) already runs Apache as this home
  network's real public front door — real TLS via `certbot`, already
  reverse-proxying several other domains straight through to specific
  ports on `videogen` over the LAN. `layforge.org`'s vhost pair mirrors
  those exactly:

  ```apache
  <VirtualHost *:80>
      ServerName layforge.org
      ServerAlias www.layforge.org

      ProxyPreserveHost On
      ProxyPass / http://192.168.1.56:8091/
      ProxyPassReverse / http://192.168.1.56:8091/

      ErrorLog ${APACHE_LOG_DIR}/layforge-error.log
      CustomLog ${APACHE_LOG_DIR}/layforge-access.log combined

      RewriteEngine on
      RewriteCond %{SERVER_NAME} =www.layforge.org [OR]
      RewriteCond %{SERVER_NAME} =layforge.org
      RewriteRule ^ https://%{SERVER_NAME}%{REQUEST_URI} [END,NE,R=permanent]
  </VirtualHost>
  ```

  (`192.168.1.56` is `videogen`'s LAN address; adjust for a different
  network.) `certbot --apache -d layforge.org -d www.layforge.org`
  issues the matching `-le-ssl.conf` half automatically.

- DNS (Porkbun): an A record for `layforge.org` and `www.layforge.org`
  pointing at the home network's public IP — the same one every other
  domain on this Apache instance already resolves to.

- `certbot --apache -d layforge.org -d www.layforge.org` issued the
  real cert and deployed `layforge.org-le-ssl.conf` automatically.

## Verification

- `go build ./... && go vet ./... && go test -race ./...` — 21 tests,
  covering `internal/lobby`'s create/heartbeat/remove/token-mismatch/
  not-found/TTL-expiry/sweep logic and its HTTP handlers (including
  confirming `token` never appears in a `GET /api/v1/listings`
  response).
- **Live-verified**, real separate processes (not `httptest` fakes): a
  real `registry` binary and a real `master` binary, `master`'s admin
  API used to opt a real test campaign in via `PUT
  /api/campaigns/{id}/policy` (`registry_listed: true`,
  `join_address`). Confirmed the listing appeared in
  `GET /api/v1/listings` and in the real browser lobby page within one
  heartbeat interval; confirmed opting the same campaign back out
  produced a real deregister (gone within one more interval); confirmed
  that killing Master ungracefully (no deregister call at all) still
  made the listing disappear on its own once the registry's TTL passed
  with no further heartbeats.
- **Re-verified against the real production deployment** at
  `https://layforge.org` (not just the local pair of processes above):
  a real Master pointed at `-registry-url https://layforge.org` opted a
  test campaign in via the admin API, and the listing appeared in
  `https://layforge.org/api/v1/listings` and the live homepage over
  real HTTPS within one heartbeat interval; opting back out produced a
  real deregister against production.
