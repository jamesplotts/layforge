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

## Security

Since this is a genuinely public, unauthenticated-write, internet-facing
API, a real security pass (prompted by an actual review of this exact
service) added several independent layers, each covering a different
attack the previous ones don't:

- **Request-size caps**, two independent layers: `internal/lobby`'s own
  handlers wrap every write body in `http.MaxBytesReader` (16KB — see
  `maxRequestBodyBytes`), so this binary itself never allocates unbounded
  memory decoding a request regardless of what's in front of it; the
  production Apache vhost additionally sets `LimitRequestBody 32768`, so
  an oversized upload is rejected before Apache spends effort relaying it
  to the backend at all. (A truly enormous body — multiple megabytes —
  can still surface as a `502` rather than a clean `413` through the full
  proxy chain, a known rough edge in how `mod_proxy` reacts to the
  backend closing the connection mid-upload; the actual protection —
  neither layer ever processes or buffers the oversized data — holds
  either way.)
- **Field length/shape validation** (`internal/lobby/handlers.go`'s
  `validateFields`): `adventure_name`/`campaign_id` capped at 200
  characters, `join_url` at 500 and required to parse as an absolute URL
  with a scheme and host — rejects obvious garbage before it ever reaches
  the public listings page.
- **Per-IP rate limiting on writes** (`internal/lobby.IPRateLimiter`, a
  hand-rolled token bucket — no dependency, matching this module's own
  zero-dependency `go.mod`): 2 req/s sustained, burst 20, applied only to
  POST/PUT/DELETE — `GET /api/v1/listings` (players' own browsers
  polling every 30s) is never throttled. Generous enough that a single
  operator running several campaigns off one IP never trips it in normal
  heartbeat traffic.
- **A hard cap on total listings** (`lobby.ErrStoreFull`, 10,000): the
  per-IP limiter above only bounds one source; this bounds a distributed
  attempt from many different IPs from exhausting memory one listing at
  a time. Existing listings still expire/sweep normally, so this
  self-heals the moment abusive traffic stops.
- **HTTP server timeouts** (`main.go`'s `*http.Server`): `ReadHeaderTimeout`/
  `ReadTimeout`/`WriteTimeout`/`IdleTimeout` are all set — Go's default
  `*http.Server` has none at all, which is a real Slowloris/connection-
  exhaustion exposure for an internet-facing listener.
- **Security response headers**, set twice — once in this binary
  (`securityHeaders` middleware, so they apply even to a direct request
  bypassing the reverse proxy) and again at the Apache vhost (`mod_headers`,
  matching in production): `Strict-Transport-Security`,
  `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`,
  `Referrer-Policy`, and a `Content-Security-Policy: default-src 'self'`
  (the site never loads anything but its own same-origin `style.css`/
  `app.js`, so this costs nothing functionally).
- **No stored-XSS surface**: `web/app.js` builds every listing row with
  `textContent`, never `innerHTML` — attacker-controlled listing data
  (which anyone can submit, by this API's own open-write design) can
  never execute as script on the public page.
- Apache's global `ServerTokens Prod`/`ServerSignature Off` (set on
  `ironclad`, the box fronting this and its sibling domains) suppress the
  `Server` response header's version string, a minor but free reduction
  in what a would-be attacker can fingerprint.

What this deliberately does **not** defend against: a self-hoster is
always free to publish a fake/spam listing (fabricated `adventure_name`,
a `join_url` pointing anywhere) — there's no way to cryptographically
prove a listing corresponds to a real, reachable Master, by this API's
own open-registration design (see "API" above). The mitigations above
bound the *scale* of abuse (rate/size/count), not its existence; the
lobby page's own copy already sets that expectation ("it does not create
an account or connect you to anything automatically").

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
      LimitRequestBody 32768

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
  issues the matching `-le-ssl.conf` half automatically, into which the
  same `LimitRequestBody` line and the following `mod_headers` block were
  also added (see "Security" above for why):

  ```apache
  <IfModule mod_headers.c>
      Header always set Strict-Transport-Security "max-age=63072000; includeSubDomains"
      Header always set X-Content-Type-Options "nosniff"
      Header always set X-Frame-Options "DENY"
      Header always set Referrer-Policy "strict-origin-when-cross-origin"
  </IfModule>
  ```

  `ironclad`'s global `/etc/apache2/conf-available/security.conf` also
  sets `ServerTokens Prod` / `ServerSignature Off` — box-wide, not
  specific to this vhost.

- DNS (Porkbun): an A record for `layforge.org` and `www.layforge.org`
  pointing at the home network's public IP — the same one every other
  domain on this Apache instance already resolves to.

- `certbot --apache -d layforge.org -d www.layforge.org` issued the
  real cert and deployed `layforge.org-le-ssl.conf` automatically.

## Verification

- `go build ./... && go vet ./... && go test -race ./...` — covering
  `internal/lobby`'s create/heartbeat/remove/token-mismatch/not-found/
  TTL-expiry/sweep logic and its HTTP handlers (including confirming
  `token` never appears in a `GET /api/v1/listings` response), plus the
  Security section's own additions: field-length/URL-shape validation,
  the 413 request-size cap, `ErrStoreFull`'s capacity cap,
  `IPRateLimiter`'s token-bucket behavior, and `main.go`'s security-
  headers/rate-limit middleware.
- **Security fixes re-verified against the real production deployment**
  (not just local `httptest`): confirmed all 5 security response headers
  present on `https://layforge.org` after the Apache reload; confirmed a
  40KB write returns a clean `413` through the full proxy chain (Apache's
  `LimitRequestBody` catching it before the backend); confirmed a bad
  `join_url` still returns `400`; confirmed `curl -I` no longer leaks an
  Apache version string; hammering `POST /api/v1/listings` past the
  burst locally returned `429` as expected.
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
