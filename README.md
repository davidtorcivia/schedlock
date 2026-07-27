# SchedLock

SchedLock sits between your AI agents and Google Calendar so that every calendar
modification passes through human review before it happens. When an agent asks
to create an event, move a meeting, or delete an appointment, SchedLock records
the request, notifies you through your preferred channel, and waits. Nothing
reaches your calendar until you approve it.

This exists because of a plain fact about today's AI systems: agents misread
context, invent details, and make choices that look reasonable in isolation but
wreck a real schedule. SchedLock is the checkpoint that lets you hand an agent
calendar access without handing it the last word.

![SchedLock dashboard](https://images.disinfo.zone/uploads/Bkn7gnKuOtksjY0JDt586bMpk9heYvyEnOOaysi6.jpg)

## How it works

```
┌─────────────┐   write request   ┌─────────────┐   after approval   ┌────────────┐
│  AI agent   │ ────────────────▶ │  SchedLock  │ ─────────────────▶ │   Google   │
│             │ ◀──── status ──── │             │                    │  Calendar  │
└─────────────┘                   └──────┬──────┘                    └────────────┘
                                         │ notify
                                  ┌──────┴───────┐
                                  ▼              ▼
                            ┌──────────┐   ┌──────────┐
                            │  Phone   │   │  Web UI  │
                            │ (ntfy…)  │   │ (admin)  │
                            └──────────┘   └──────────┘
```

Reads pass straight through. Writes stop, wait for a decision, and expire on
their own if nobody answers.

## Features

- **REST API** mirroring the Google Calendar operations an agent needs
- **Three API key tiers** — read, write, admin — with optional per-key policy
- **Human approval** for every write, with a one-tap approval page
- **Notifications** via ntfy, Pushover, Telegram, or a generic webhook
- **Change requests**, so you can send a request back instead of rejecting it
- **Web UI** for review, configuration, and the audit trail
- **Single static binary**, or Docker with a SQLite volume

## Quick start

```bash
git clone https://github.com/dtorcivia/schedlock.git
cd schedlock
docker compose up -d
```

Open <http://localhost:8080>. The first-run wizard asks for an admin password,
generates the server secret and encryption key, and writes them to
`/data/config.yaml`. The service restarts itself when setup completes.

Then:

1. Sign in with the password you chose.
2. **Settings → Google OAuth credentials** — paste a client ID and secret from
   Google Cloud Console. The page shows the exact redirect URI to register.
3. **Settings → Connect Google Calendar** — authorize the account.
4. **Settings → Notifications** — enable at least one channel, so approval
   requests reach you when you are away from the browser.
5. **API keys** — create a `write` key for your agent. It is shown once.
6. **Point your agent at the instructions.** SchedLock serves usage
   documentation for agents at `<your-base-url>/SKILL.md`. Give your agent that
   URL along with the API key.

Running without Docker:

```bash
go build -o schedlock ./cmd/server
SCHEDLOCK_DATA_DIR=./data ./schedlock
```

## API

Authenticate with the key in an `Authorization` header:

```
Authorization: Bearer sk_write_xxxxxxxxxxxxxxxxxxxxxx
```

### Reads — answered immediately

```
GET /api/calendar/list
GET /api/calendar/{calendarId}/events?timeMin=2026-01-01T00:00:00Z&timeMax=2026-02-01T00:00:00Z
GET /api/calendar/{calendarId}/events/{eventId}
GET /api/calendar/freebusy?timeMin=…&timeMax=…&calendars=primary
```

### Writes — held for approval

```bash
curl -X POST https://schedlock.example.com/api/calendar/events/create \
  -H "Authorization: Bearer $SCHEDLOCK_API_KEY" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: $(uuidgen)" \
  -d '{
    "calendarId": "primary",
    "summary": "Team meeting",
    "start": "2026-01-15T10:00:00-05:00",
    "end": "2026-01-15T11:00:00-05:00",
    "location": "Conference Room A",
    "attendees": ["alice@example.com"]
  }'
```

```json
{
  "request_id": "req_kX9mP4qzA1b2C3d4",
  "status": "pending_approval",
  "expires_at": "2026-01-14T10:15:00Z",
  "message": "Request submitted for approval"
}
```

`202 Accepted` means the request is waiting for a human. `200 OK` means the
key's policy allowed it to run immediately.

Also available: `POST /api/calendar/events/update`, `POST
/api/calendar/events/delete`, `GET /api/requests`, `GET
/api/requests/{requestId}`, and `POST /api/requests/{requestId}/cancel`.

Send an `Idempotency-Key` header on writes. A retried submission then returns
the original request instead of queuing a second approval.

Unknown JSON fields are rejected rather than ignored, so a misspelled
`attendees` is reported instead of quietly producing an event with no guests.

## Approval flow

1. The agent submits a write. SchedLock stores it and returns a request ID.
2. Every enabled notification channel receives the request, with links.
3. You approve, deny, or send it back with a note — from the notification, the
   approval page, or the web UI.
4. On approval the operation runs against Google Calendar.
5. The result is delivered back to the agent's webhook and to you.

Requests expire after `approval_timeout_minutes` (60 by default). An expired
request is denied unless you have configured otherwise; an unanswered request
never reaches your calendar by default.

Approval links carry a single-use token. Opening one shows what is being asked
for and waits for a click — fetching the URL never decides anything, so link
previews and mail scanners cannot approve on your behalf.

## Configuration

Settings are resolved in this order, each layer overriding the one before:
built-in defaults, `/data/config.yaml`, environment variables, then the runtime
settings you edit in the web UI.

Anything editable in **Settings** takes effect immediately — no restart.

| Environment variable | Description | Required |
|---|---|---|
| `SCHEDLOCK_SERVER_SECRET` | HMAC key for API key hashing | Generated at setup |
| `SCHEDLOCK_ENCRYPTION_KEY` | Encrypts OAuth tokens and provider credentials | Generated at setup |
| `SCHEDLOCK_AUTH_PASSWORD_HASH` | Admin password (Argon2id) | Set at setup |
| `SCHEDLOCK_BASE_URL` | Public URL; approval links point here | Yes |
| `SCHEDLOCK_GOOGLE_CLIENT_ID` | Google OAuth client ID | Or set in Settings |
| `SCHEDLOCK_GOOGLE_CLIENT_SECRET` | Google OAuth client secret | Or set in Settings |
| `SCHEDLOCK_TRUSTED_PROXIES` | Proxy IPs/CIDRs whose forwarded-for headers are believed | No |
| `SCHEDLOCK_APPROVAL_TIMEOUT` | Minutes to wait for a decision (default 60) | No |
| `SCHEDLOCK_APPROVAL_DEFAULT_ACTION` | `deny` (default) or `approve` on timeout | No |
| `SCHEDLOCK_DISPLAY_TIMEZONE` | Timezone for displayed times (default UTC) | No |
| `SCHEDLOCK_LOG_LEVEL` / `SCHEDLOCK_LOG_FORMAT` | `debug`…`error`, `json` or `text` | No |

See `.env.example` for the complete list, including notification providers and
retention windows.

Generate a password hash without starting the server:

```bash
./schedlock hash-password "your password"
```

### Behind a reverse proxy

Set `SCHEDLOCK_TRUSTED_PROXIES` to your proxy's address (for example
`10.0.0.0/8`). Forwarded client-IP headers are honoured only from those
addresses; otherwise anyone could rotate a header value to evade the login rate
limit.

## Notification providers

Configure these in **Settings**, or by environment variable. Stored credentials
are encrypted, and once saved they are never rendered back into the page.

### ntfy

```env
SCHEDLOCK_NTFY_ENABLED=true
SCHEDLOCK_NTFY_TOPIC=your-topic
SCHEDLOCK_NTFY_SERVER_URL=https://ntfy.sh
```

Notifications carry Approve and Deny action buttons plus a Review link. Use a
random topic name, or an access token: an ntfy topic is public by default.

### Pushover

```env
SCHEDLOCK_PUSHOVER_ENABLED=true
SCHEDLOCK_PUSHOVER_APP_TOKEN=your-app-token
SCHEDLOCK_PUSHOVER_USER_KEY=your-user-key
```

### Telegram

```env
SCHEDLOCK_TELEGRAM_ENABLED=true
SCHEDLOCK_TELEGRAM_BOT_TOKEN=123456:ABC...
SCHEDLOCK_TELEGRAM_CHAT_ID=your-chat-id
SCHEDLOCK_TELEGRAM_WEBHOOK_SECRET=a-random-secret
```

Approve and deny are inline buttons; replying to the message records a change
request. Decisions are accepted only from the configured chat, and only when
Telegram presents the webhook secret — SchedLock generates one if you leave it
blank, and refuses to register a webhook without it.

### Generic webhook

For home automation, chat bridges, or anything without a dedicated provider:

```env
SCHEDLOCK_WEBHOOK_ENABLED=true
SCHEDLOCK_WEBHOOK_URL=https://your-server.example/webhook
SCHEDLOCK_WEBHOOK_SECRET=your-hmac-secret
```

```json
{
  "event": "approval_request",
  "timestamp": "2026-01-15T10:30:00Z",
  "request_id": "req_kX9mP4qzA1b2C3d4",
  "operation": "create_event",
  "summary": "Create: Team meeting",
  "expires_at": "2026-01-15T11:30:00Z",
  "urls": {
    "approve": "https://schedlock.example.com/api/callback/approve/dtok_…",
    "deny": "https://schedlock.example.com/api/callback/deny/dtok_…",
    "approve_page": "https://schedlock.example.com/approve/dtok_…",
    "web": "https://schedlock.example.com/requests/req_kX9mP4qzA1b2C3d4"
  },
  "details": {
    "title": "Team meeting",
    "start_time": "2026-01-20T10:00:00-05:00",
    "end_time": "2026-01-20T11:00:00-05:00",
    "attendees": ["alice@example.com"]
  }
}
```

With a secret configured, each request carries an HMAC-SHA256 of the raw body in
`X-SchedLock-Signature`. Verify it before acting: the `approve` URL acts on a
single POST.

## Per-key policy

An API key can carry constraints that decide, per operation, whether it runs,
waits for approval, or is refused outright:

```json
{
  "calendar_allowlist": ["primary"],
  "operations": {"delete_event": "deny", "create_event": "require_approval"},
  "max_duration_minutes": 240,
  "max_attendees": 20,
  "attendee_domain_allowlist": ["example.com"],
  "allow_external_attendees": false,
  "block_all_day_events": true
}
```

Evaluation is fail-closed: anything the policy cannot conclusively allow ends up
waiting for a human. An update whose existing event cannot be read, for example,
requires approval rather than being assumed harmless.

## Security

- API keys are stored as HMAC-SHA256 under a server secret, never in plaintext
- OAuth tokens and provider credentials are encrypted with AES-256-GCM
- The admin password is hashed with Argon2id
- Approval tokens are single-use, stored hashed, and expire with the request
- Decisions require a POST; a GET on an approval link only shows the request
- Optional approval PIN, rate-limited, for the public approval page
- CSRF tokens bound to the session, compared in constant time
- Rate limiting per API key tier and per client address on login
- Content-Security-Policy with no third-party script sources
- Approval tokens and secrets are redacted from logs

## Development

```bash
go test ./...              # full suite
go test -race ./...        # with the race detector (needs CGO_ENABLED=1)
go vet ./...
gofmt -l .

go run ./cmd/server        # run locally; set SCHEDLOCK_DATA_DIR first
```

The SQLite driver is pure Go, so tests and builds need no C toolchain and the
binary is static.

Templates and static assets are embedded in the binary, so the server has no
runtime dependency on its working directory.

## Data and backups

Everything lives in one SQLite database under the data directory. Back it up by
copying the file while the service is stopped, or continuously with
[Litestream](https://litestream.io) — see `litestream.yml`.

Retention defaults: completed requests 90 days, audit entries 365 days, webhook
failures 30 days. The audit trail outlives the requests it describes.

## License

MIT. See LICENSE.
