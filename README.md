# SchedLock

A Calendar Proxy service that provides human-in-the-loop approval for AI agent calendar operations.

## Features

- **REST API** mirroring Google Calendar operations
- **3-tier API key authentication** (read, write, admin)
- **Human approval workflow** for write operations
- **Multi-provider notifications** (ntfy, Pushover, Telegram)
- **Suggestion/change request** capability
- **Web UI** for configuration and manual approvals
- **Docker deployment** with SQLite persistence

## Quick Start

### Prerequisites

- Docker and Docker Compose
- Google Cloud project with Calendar API enabled
- OAuth 2.0 credentials (for headless/desktop app)

### Setup

1. **Clone and configure:**
   ```bash
   git clone https://github.com/dtorcivia/schedlock.git
   cd schedlock
   cp .env.example .env
   ```

2. **Generate secrets:**
   ```bash
   # Generate server secret
   openssl rand -base64 32

   # Generate encryption key
   openssl rand -base64 32

   # Generate password hash (run the binary)
   ./schedlock hash-password "YourPassword"

   # Or use an Argon2id tool
   # Format: $argon2id$v=19$m=65536,t=3,p=4$SALT$HASH
   ```

3. **Configure `.env`** with your:
   - Server secret
   - Encryption key
   - Password hash
   - Google OAuth credentials
   - Notification provider settings

4. **Start the server:**
   ```bash
   docker-compose up -d
   ```

5. **Connect Google Calendar:**
   - Open `http://localhost:8080/login`
   - Go to Settings → Connect Google Calendar
   - Follow the OAuth flow

6. **Create an API key:**
   - Go to API Keys in the web UI
   - Create a "write" tier key for Moltbot

## API Usage

### Authentication

Include your API key in the `Authorization` header:

```bash
Authorization: Bearer sk_write_xxxxxxxxxxxx
```

### Read Operations (no approval needed)

```bash
# List calendars
GET /api/calendar/list

# List events
GET /api/calendar/{calendarId}/events?timeMin=2024-01-01T00:00:00Z

# Get event
GET /api/calendar/{calendarId}/events/{eventId}

# Free/busy query
GET /api/calendar/freebusy?timeMin=...&timeMax=...
```

### Write Operations (require approval)

```bash
# Create event
POST /api/calendar/events/create
{
  "calendarId": "primary",
  "summary": "Team Meeting",
  "start": "2024-01-15T10:00:00-05:00",
  "end": "2024-01-15T11:00:00-05:00",
  "location": "Conference Room A",
  "attendees": ["alice@example.com"]
}

# Response
{
  "request_id": "req_abc123",
  "status": "pending_approval",
  "expires_at": "2024-01-14T10:15:00Z"
}
```

### Request Management

```bash
# List your requests
GET /api/requests

# Get request status
GET /api/requests/{requestId}

# Cancel pending request
POST /api/requests/{requestId}/cancel
```

## Approval Flow

1. Client submits write operation
2. SchedLock stores request and sends notifications
3. Human receives notification with approve/deny/suggest options
4. On approval, operation executes against Google Calendar
5. Webhook notifies client of result

## Configuration

| Environment Variable | Description | Required |
|---------------------|-------------|----------|
| `SCHEDLOCK_SERVER_SECRET` | HMAC key for API key hashing | Yes |
| `SCHEDLOCK_ENCRYPTION_KEY` | Encryption key for OAuth token storage | Yes |
| `SCHEDLOCK_AUTH_PASSWORD_HASH` | Admin password (Argon2id) | Yes |
| `SCHEDLOCK_ADMIN_PASSWORD` | Admin password (plaintext, dev only) | No |
| `SCHEDLOCK_GOOGLE_CLIENT_ID` | Google OAuth client ID | Yes |
| `SCHEDLOCK_GOOGLE_CLIENT_SECRET` | Google OAuth secret | Yes |
| `SCHEDLOCK_BASE_URL` | Public URL for callbacks | Yes |
| `SCHEDLOCK_NTFY_ENABLED` | Enable ntfy notifications | No |
| `SCHEDLOCK_PUSHOVER_ENABLED` | Enable Pushover notifications | No |
| `SCHEDLOCK_TELEGRAM_ENABLED` | Enable Telegram notifications | No |

See `.env.example` for full configuration options.

### Config File and Runtime Overrides

- Optional YAML config file: `/data/config.yaml` (or set `SCHEDLOCK_CONFIG_FILE`).
- Runtime settings saved in the web UI override config file/env for:
  - Approval timeout and default action
  - Retention enable/disable and retention windows
  - Logging level/format
  - Display timezone and formats

## Notification Providers

### ntfy

```env
SCHEDLOCK_NTFY_ENABLED=true
SCHEDLOCK_NTFY_TOPIC=your-topic
SCHEDLOCK_NTFY_SERVER_URL=https://ntfy.sh
```

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
SCHEDLOCK_TELEGRAM_WEBHOOK_SECRET=your-secret-token
```

## Security

- API keys use HMAC-SHA256 hashing (not stored in plain text)
- OAuth tokens encrypted with AES-256-GCM
- Single-use decision tokens for approval callbacks
- Rate limiting per API key tier
- Rate limiting on web UI login (per IP)
- CSRF protection on web UI
- Secure session management

## Architecture

```
┌─────────────┐     ┌─────────────┐     ┌─────────────┐
│   Moltbot   │────▶│  SchedLock  │────▶│   Google    │
│  (AI Agent) │     │   (Proxy)   │     │  Calendar   │
└─────────────┘     └──────┬──────┘     └─────────────┘
                          │
                    ┌─────┴─────┐
                    ▼           ▼
              ┌─────────┐ ┌─────────┐
              │  Human  │ │  Web UI │
              │ (ntfy)  │ │ (Admin) │
              └─────────┘ └─────────┘
```

## Development

```bash
# Run locally
go run ./cmd/server

# Run tests
go test ./...

# Build binary
go build -o schedlock ./cmd/server
```

## License

MIT License - see LICENSE file for details.
