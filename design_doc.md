# Calendar Proxy Design Document

**Project**: SchedLock 
**Version**: 1.0 Draft  
**Author**: David Torcivia
**Date**: January 28, 2026  
**Status**: Implement

---

## Table of Contents

1. [Overview](#1-overview)
2. [Goals & Non-Goals](#2-goals--non-goals)
3. [Architecture](#3-architecture)
4. [API Design](#4-api-design)
5. [Authentication & Authorization](#5-authentication--authorization)
6. [Notification System](#6-notification-system)
7. [Approval Workflow](#7-approval-workflow)
8. [Database Schema](#8-database-schema)
9. [SKILL.md Integration](#9-skillmd-integration)
10. [Web UI](#10-web-ui)
11. [Deployment](#11-deployment)
12. [Configuration](#12-configuration)
13. [Security Considerations](#13-security-considerations)
14. [Future Considerations](#14-future-considerations)

---

## 1. Overview

### 1.1 Problem Statement

Moltbot (and similar AI agents) require access to Google Calendar for scheduling tasks. Granting direct API access creates risk:

- AI may create/modify/delete events without user awareness
- No audit trail of what the AI did and why
- No ability to constrain operations by type or require approval
- Credential exposure if Moltbot is compromised

### 1.2 Solution

A proxy service that sits between Moltbot and Google Calendar, providing:

- **Capability-constrained API keys**: Read-only, read-write, or admin tiers
- **Human-in-the-loop approval**: Configurable per operation type
- **Multi-channel notifications**: ntfy, Pushover, and Telegram support
- **Audit logging**: Complete record of requests, approvals, and executions
- **Web UI**: For configuration, manual approvals, and audit review

### 1.3 Key Terminology

| Term | Definition |
|------|------------|
| **Proxy** | This service—intercepts requests between bot and Google Calendar |
| **Bot** | Moltbot or any AI agent consuming the proxy API |
| **Approver** | Human user who approves/denies pending operations |
| **Operation** | A discrete calendar action (list, create, update, delete) |
| **Tier** | Permission level assigned to an API key |
| **Suggestion** | Approver's requested modifications (alternative to approve/deny) |

### 1.4 Key Rules

- **Never, ever use emojis**: Unicode, ASCII, and similar characters are fine
- **Always Engineer for Security**: Follow industry best practices and do careful security analysis at all times
- **Take Extra Steps for Performance**: Spend the extra time architecting for optimization and performance (this does not mean to needlesly complicate)
- **Document Thoroughly**: Record all pertinent and necessary information to a Readme - favor overviews and specific instructions for deployment, use, and development

---

## 2. Goals & Non-Goals

### 2.1 Goals

1. **G1**: Provide REST API that mirrors essential Google Calendar operations
2. **G2**: Support three API key tiers with distinct capabilities
3. **G3**: Require human approval for configurable operation types
4. **G4**: Deliver approval requests via ntfy, Pushover, and/or Telegram
5. **G5**: Allow approve/deny/suggest-change directly from notifications (bidirectional)
6. **G6**: Support "suggest change" flow where approver can request modifications
7. **G7**: Push status updates to Moltbot via webhook (in addition to polling)
8. **G8**: Provide web UI for configuration and manual approval
9. **G9**: Maintain complete audit log of all operations
10. **G10**: Deploy via Docker with minimal configuration
11. **G11**: Secure web UI behind Cloudflare Tunnel + Access (optional)
12. **G12**: Provide SKILL.md for Moltbot integration
13. **G13**: Auto-register Telegram webhook on configuration

### 2.2 Non-Goals

1. **NG1**: Support for calendars beyond Google Calendar (future consideration)
2. **NG2**: Multi-user/multi-tenant support (single user, multiple bots)
3. **NG3**: Mobile app (web UI is mobile-responsive)
4. **NG4**: Calendar UI for viewing/editing events (use Google Calendar directly)

---

## 3. Architecture

### 3.1 System Context Diagram

```
                        ┌─────────────────────────────────────────────────────────┐
┌─────────────┐         │                   Calendar Proxy                        │
│   Moltbot   │         │  ┌─────────┐  ┌──────────┐  ┌───────────────┐          │
│  (AI Agent) │◄───────►│  │   API   │──│  Core    │──│ Google Cal    │─────────────► Google
│             │  REST   │  │ Gateway │  │  Engine  │  │   Client      │          │    Calendar
│  SKILL.md   │  +Auth  │  └─────────┘  └──────────┘  └───────────────┘          │    API
│             │         │       │            │                                    │
│ ┌─────────┐ │         │       │            │         ┌───────────────┐          │
│ │Webhook  │◄──────────│───────│────────────│─────────│ Bot Webhook   │          │
│ │Endpoint │ │  POST   │       │            │         │ Client        │          │
│ └─────────┘ │ /hooks/ │       ▼            ▼         └───────────────┘          │
└─────────────┘  agent  │  ┌─────────┐  ┌──────────┐                              │
                        │  │ SQLite  │  │ Notifier │                              │
                        │  │   DB    │  │ Manager  │                              │
                        │  └─────────┘  └──────────┘                              │
                        │                    │                                    │
                        │       ┌────────────┼────────────┐                       │
                        │       ▼            ▼            ▼                       │
                        │  ┌────────┐   ┌─────────┐  ┌──────────┐                 │
                        │  │  ntfy  │   │Pushover │  │ Telegram │                 │
                        │  └────────┘   └─────────┘  └──────────┘                 │
                        │                                                         │
                        │  ┌─────────────────────────────────────────┐            │
                        │  │              Web UI                     │            │
                        │  │  • Dashboard    • Pending Approvals     │            │
                        │  │  • API Keys     • Audit Log             │            │
                        │  │  • Settings     • Notification Config   │            │
                        │  └─────────────────────────────────────────┘            │
                        └─────────────────────────────────────────────────────────┘
                                              │
                                              │ Cloudflare Tunnel (optional)
                                              ▼
                                        ┌───────────┐
                                        │  Approver │
                                        │  (Human)  │
                                        └───────────┘
```

### 3.2 Component Responsibilities

| Component | Responsibility |
|-----------|---------------|
| **API Gateway** | Request validation, API key auth, rate limiting, routing |
| **Core Engine** | Business logic, approval workflow orchestration, operation execution |
| **Google Cal Client** | OAuth token management, Google Calendar API calls |
| **Notifier Manager** | Dispatches to configured notification providers |
| **Bot Webhook Client** | Pushes status updates to Moltbot via `/hooks/agent` |
| **SQLite DB** | Persists API keys, pending approvals, audit logs, settings |
| **Web UI** | Configuration interface, manual approval, audit viewing |

### 3.3 Technology Stack

| Layer | Technology | Rationale |
|-------|------------|-----------|
| Language | **Go** | Single binary, excellent concurrency, low resource usage |
| HTTP Router | **net/http** (Go 1.22+) or **chi** | Minimal dependencies, built-in path routing |
| Database | **SQLite + WAL** | Zero-config, ACID, sufficient for single-user |
| Backup | **Litestream** (optional) | Continuous replication to S3/R2 |
| Web UI | **HTMX + Tailwind** | Server-rendered, minimal JS, fast development |
| Container | **Docker** | Single container, multi-stage build |

### 3.4 Concurrency Model

**SQLite Write Serialization**:

SQLite supports only one writer at a time. Use an in-process **execution queue** to serialize Google API calls and DB writes:

```go
type ExecutionQueue struct {
    ch chan ExecutionJob
}

func NewExecutionQueue(workers int) *ExecutionQueue {
    eq := &ExecutionQueue{ch: make(chan ExecutionJob, 100)}
    for i := 0; i < workers; i++ {
        go eq.worker()
    }
    return eq
}

func (eq *ExecutionQueue) worker() {
    for job := range eq.ch {
        // All Google API calls and result writes happen here
        // Single worker (workers=1) ensures serialized writes
        eq.executeApprovedRequest(job)
    }
}
```

**Recommended**: Use `workers=1` for simplicity. SQLite WAL mode handles concurrent readers, but writes must be serialized. If throughput becomes a bottleneck, consider migrating to PostgreSQL.

**Background Workers**:
- **Timeout Worker**: Checks for expired requests every 30 seconds
- **Cleanup Worker**: Daily data retention and VACUUM
- Both use `UPDATE ... WHERE status='pending_approval'` for atomic state transitions

---

## 4. API Design

### 4.1 Base URL & Versioning

```
Base URL: /api
Content-Type: application/json
```

### 4.2 Authentication Header

All requests require:
```
Authorization: Bearer <api_key>
```

### 4.3 Endpoints

#### 4.3.1 Calendar Operations

| Method | Endpoint | Description | Tiers |
|--------|----------|-------------|-------|
| GET | `/api/calendar/list` | List accessible calendars | read, write, admin |
| GET | `/api/calendar/{calendarId}/events` | List events (with query params) | read, write, admin |
| GET | `/api/calendar/{calendarId}/events/{eventId}` | Get single event | read, write, admin |
| GET/POST | `/api/calendar/freebusy` | Check availability | read, write, admin |
| POST | `/api/calendar/events/create` | Create event | write, admin |
| POST | `/api/calendar/events/update` | Update event | write, admin |
| POST | `/api/calendar/events/delete` | Delete event | write, admin |

#### 4.3.2 Request Management

| Method | Endpoint | Description | Tiers |
|--------|----------|-------------|-------|
| GET | `/api/requests` | List requests for API key | read, write, admin |
| GET | `/api/requests/{requestId}` | Get request status (includes result when completed) | read, write, admin |
| POST | `/api/requests/{requestId}/cancel` | Cancel pending request | write, admin (own requests) |

#### 4.3.3 Approval Callbacks (Internal)

| Method | Endpoint | Description | Auth |
|--------|----------|-------------|------|
| POST | `/api/callback/approve/{token}` | Approve request | decision token |
| POST | `/api/callback/deny/{token}` | Deny request | decision token |
| POST | `/api/callback/suggest/{token}` | Request changes | decision token |

#### 4.3.4 Suggestion Endpoint (Web UI)

| Method | Endpoint | Description | Auth |
|--------|----------|-------------|------|
| POST | `/requests/{requestId}/suggest` | Submit change suggestion | session cookie + CSRF |

Form field:
```
suggestion=Move to 3pm instead, and add Bob to attendees
```

#### 4.3.5 Telegram Webhook

| Method | Endpoint | Description | Auth |
|--------|----------|-------------|------|
| POST | `/webhooks/telegram` | Receive Telegram button/reply callbacks | webhook secret |

### 4.4 Request/Response Patterns

#### 4.4.1 Synchronous Response (Read Operations)

```http
GET /api/calendar/primary/events?timeMin=2026-01-28T00:00:00Z&maxResults=10
Authorization: Bearer sk_read_xxx

HTTP/1.1 200 OK
{
  "events": [
    {
      "id": "abc123",
      "summary": "Team Meeting",
      "start": "2026-01-28T14:00:00Z",
      "end": "2026-01-28T15:00:00Z",
      "location": "Conference Room A",
      "attendees": ["alice@example.com"]
    }
  ],
  "next_page_token": "token123"
}
```

#### 4.4.2 Asynchronous Response (Write Operations Requiring Approval)

```http
POST /api/calendar/events/create
Authorization: Bearer sk_write_xxx
Content-Type: application/json

{
  "calendarId": "primary",
  "summary": "Project Review",
  "start": "2026-01-30T10:00:00Z",
  "end": "2026-01-30T11:00:00Z",
  "location": "Conference Room A",
  "attendees": ["alice@example.com", "bob@example.com"]
}

HTTP/1.1 202 Accepted
{
  "request_id": "req_a1b2c3d4",
  "status": "pending_approval",
  "expires_at": "2026-01-28T13:00:00Z",
  "message": "Event creation request submitted"
}
```

#### 4.4.3 Polling for Status

```http
GET /api/requests/req_a1b2c3d4
Authorization: Bearer sk_write_xxx

HTTP/1.1 200 OK
{
  "id": "req_a1b2c3d4",
  "status": "approved",
  "operation": "create_event",
  "created_at": "2026-01-28T12:00:00Z",
  "expires_at": "2026-01-28T13:00:00Z",
  "decided_at": "2026-01-28T12:05:00Z",
  "decided_by": "pushover"
}
```

#### 4.4.4 Change Requested Response

When status is `change_requested`, the response includes the approver's suggestion:

```http
GET /api/requests/req_a1b2c3d4
Authorization: Bearer sk_write_xxx

HTTP/1.1 200 OK
{
  "id": "req_a1b2c3d4",
  "status": "change_requested",
  "operation": "create_event",
  "created_at": "2026-01-28T12:00:00Z",
  "expires_at": "2026-01-28T13:00:00Z",
  "suggestion": {
    "text": "Move to 3pm instead, and add Bob to attendees",
    "suggested_at": "2026-01-28T12:05:00Z",
    "suggested_by": "telegram"
  }
}
```

The bot should:
1. Parse the suggestion and modify the request accordingly
2. Submit a new request with the changes (which will go through approval again)
3. Optionally cancel the original request via `POST /api/requests/{id}/cancel`

**Status Values:**
| Status | Description |
|--------|-------------|
| `pending_approval` | Awaiting human decision |
| `change_requested` | Approver requested modifications (see `suggestion` field) |
| `approved` | Approved, executing or queued |
| `denied` | Human denied the request |
| `expired` | Timeout reached, default action applied |
| `executing` | Currently calling Google API |
| `completed` | Successfully executed |
| `failed` | Execution failed (see error) |

#### 4.4.5 Getting the Result

The request status endpoint includes the result once the request is completed:

```http
GET /api/requests/req_a1b2c3d4
Authorization: Bearer sk_write_xxx

HTTP/1.1 200 OK
{
  "id": "req_a1b2c3d4",
  "status": "completed",
  "result": {
    "id": "google_event_xyz",
    "html_link": "https://calendar.google.com/event?eid=xyz"
  }
}
```

### 4.5 Error Responses

```json
{
  "error": {
    "code": "APPROVAL_DENIED",
    "message": "Request was denied by approver",
    "details": {}
  }
}
```

| Code | HTTP Status | Meaning |
|------|-------------|---------|
| `INVALID_API_KEY` | 401 | API key missing or invalid |
| `INSUFFICIENT_PERMISSIONS` | 403 | Operation not allowed for tier |
| `RATE_LIMITED` | 429 | Too many requests |
| `APPROVAL_DENIED` | 403 | Human denied the request |
| `CHANGE_REQUESTED` | 200 | Approver requested modifications (not an error, check `suggestion`) |
| `APPROVAL_EXPIRED` | 408 | Approval timeout reached |
| `REQUEST_NOT_FOUND` | 404 | Request ID doesn't exist |
| `GOOGLE_API_ERROR` | 502 | Google Calendar API error |
| `VALIDATION_ERROR` | 400 | Invalid request payload |

### 4.6 Query Parameters for Event Listing

| Parameter | Type | Description |
|-----------|------|-------------|
| `timeMin` | datetime | Lower bound (exclusive), RFC3339 with timezone |
| `timeMax` | datetime | Upper bound (exclusive), RFC3339 with timezone |
| `maxResults` | integer | Max events to return (default: 50, max: 250) |
| `pageToken` | string | Pagination token |
| `q` | string | Free text search |
| `singleEvents` | boolean | Expand recurring events (default: true) |
| `orderBy` | string | `startTime` or `updated` |

### 4.7 EventIntent Schema (Payload Allowlisting)

The proxy accepts a **constrained schema** for write operations, not arbitrary Google Calendar JSON. Unknown fields are silently ignored to prevent bots from setting unexpected properties.

**Accepted Fields for Create/Update**:
```go
type EventIntent struct {
    CalendarID  string    `json:"calendarId"`            // Required: "primary" or calendar ID
    Summary     string    `json:"summary"`               // Required: Event title
    Description string    `json:"description,omitempty"` // Optional: Event description
    Location    string    `json:"location,omitempty"`    // Optional: Location text
    Start       time.Time `json:"start"`                 // Required: RFC3339 with timezone
    End         time.Time `json:"end"`                   // Required: RFC3339 with timezone
    Attendees   []string  `json:"attendees,omitempty"`   // Optional: Email addresses
    
    // Advanced (optional)
    ColorID     string    `json:"colorId,omitempty"`     // Event color (1-11)
    Visibility  string    `json:"visibility,omitempty"`  // "default", "public", "private"
    Reminders   *Reminders `json:"reminders,omitempty"`  // Custom reminders
}

type Reminders struct {
    UseDefault bool       `json:"useDefault"`
    Overrides  []Reminder `json:"overrides,omitempty"`
}

type Reminder struct {
    Method  string `json:"method"`  // "email" or "popup"
    Minutes int    `json:"minutes"` // Minutes before event
}
```

**Explicitly NOT Supported** (silently dropped):
- `conferenceData` — Video conferencing (security/cost implications)
- `guestsCanModify`, `guestsCanInviteOthers`, `guestsCanSeeOtherGuests` — Guest permissions
- `recurrence` — Recurring events (future consideration)
- `attachments` — File attachments
- `extendedProperties` — Custom metadata
- `source` — External source info

**Update Operations (PATCH semantics)**:
For `POST /api/calendar/events/update`, only provided fields are updated. The proxy shows a **diff** to the approver:

```
┌─────────────────────────────────────────────────────┐
│  Changes Requested:                                 │
│  ─────────────────────────────────────────────────  │
│  Summary:  "Team Meeting" → "Team Meeting (Updated)"│
│  Location: "Room A" → "Room B"                      │
│  Attendees: +bob@company.com                        │
└─────────────────────────────────────────────────────┘
```

### 4.8 Idempotency

To prevent duplicate requests from creating duplicate events (e.g., if Moltbot retries due to timeout):

**Request Header**:
```
Idempotency-Key: <unique_key>
```

**Behavior**:
1. If `(api_key_id, idempotency_key)` exists and is < 24 hours old:
   - Return the existing request (same `request_id`, current status)
   - Do NOT create a new pending request
2. If it doesn't exist:
   - Create new request, store the idempotency key
3. Idempotency keys are garbage-collected after 24 hours

**Database Schema Addition**:
```sql
CREATE TABLE idempotency_keys (
    api_key_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    request_id TEXT NOT NULL REFERENCES requests(id),
    created_at TEXT DEFAULT (datetime('now')),
    PRIMARY KEY (api_key_id, idempotency_key)
);

CREATE INDEX idx_idempotency_created ON idempotency_keys(created_at);
```

**Example**:
```bash
# First request
curl -X POST -H "Idempotency-Key: create-meeting-2026-01-30-v1" ...
# Returns: {"request_id": "req_abc123", "status": "pending_approval"}

# Retry (network timeout, etc.)
curl -X POST -H "Idempotency-Key: create-meeting-2026-01-30-v1" ...
# Returns: {"request_id": "req_abc123", "status": "approved"}  # Same request, current status
```

### 4.9 HTTP Status Code Semantics

**Write Operations (POST /api/calendar/events/create|update|delete)**:

| Scenario | Status | Body |
|----------|--------|------|
| Approval required | `202 Accepted` | `{request_id, status: "pending_approval"}` |
| Auto-approved (admin/policy) | `200 OK` | `{request_id, status: "approved"}` |
| Constraint violation | `403 Forbidden` | `{error: {code: "CONSTRAINT_VIOLATION", ...}}` |
| Validation error | `400 Bad Request` | `{error: {code: "VALIDATION_ERROR", ...}}` |

**Request Status (GET /api/requests/{id})**:

| Scenario | Status | Body |
|----------|--------|------|
| Request exists (any state) | `200 OK` | `{id, status, ...}` |
| Request not found | `404 Not Found` | `{error: {code: "REQUEST_NOT_FOUND"}}` |

**Request Result**

The request status endpoint includes `result` once the request is completed.

**Cancel Request (POST /api/requests/{id}/cancel)**:

| Scenario | Status | Body |
|----------|--------|------|
| Successfully cancelled | `200 OK` | `{message: "request cancelled"}` |
| Not allowed / already resolved | `400 Bad Request` | `{error: {...}}` |

Note: Cancel marks the request as `cancelled` and does NOT delete it (preserves audit trail).

---

## 5. Authentication & Authorization

### 5.1 API Key Structure

```
Format: sk_{tier}_{random_22_chars_base62}
Example: sk_write_7kX9mP4qR1sT3uV5wY2zA

Random portion: 22 base62 characters = ~131 bits of entropy
Stored as: HMAC-SHA256(server_secret, full_key) — NOT plain SHA-256
Display as: sk_write_7kX9...zA (first 8 + last 2 chars)
```

**Why HMAC instead of plain SHA-256?**
If the database leaks, an attacker with only the DB cannot brute-force keys offline—they'd also need `server_secret`. With plain SHA-256, they could attempt offline attacks directly.

```go
func hashAPIKey(key string, serverSecret []byte) string {
    mac := hmac.New(sha256.New, serverSecret)
    mac.Write([]byte(key))
    return hex.EncodeToString(mac.Sum(nil))
}
```

### 5.2 Tier Definitions

| Tier | Code | Capabilities | Use Case |
|------|------|--------------|----------|
| **Read** | `read` | List calendars, list/get events, freebusy | Viewing schedules |
| **Write** | `write` | All read + create/update/delete (with approval) | Moltbot main key |
| **Admin** | `admin` | All write + bypass approval + API key management | Emergency access |

**Note on Admin tier**: Even with approval bypass, all admin operations are logged to the audit trail. Consider requiring UI confirmation for admin writes (configurable).

### 5.3 Per-Key Policy Constraints

Tiers provide baseline capabilities, but individual keys can have **additional restrictions**:

```yaml
# Example: Restricted bot key
api_key:
  name: "moltbot-restricted"
  tier: "write"
  constraints:
    # Calendar restrictions
    calendar_allowlist: ["primary", "work@group.calendar.google.com"]
    
    # Operation restrictions (override tier defaults)
    operations:
      create_event: "require_approval"  # approve, deny, require_approval
      update_event: "require_approval"
      delete_event: "deny"              # This key cannot delete at all
    
    # Field constraints
    max_duration_minutes: 120           # No events longer than 2 hours
    attendee_domain_allowlist: ["company.com", "contractor.io"]
    allow_external_attendees: false     # Require approval if non-allowlisted domains
    
    # Advanced restrictions
    max_attendees: 10
    allowed_colors: null                # Any color allowed
    block_all_day_events: false
```

**Database Schema** (stored as JSON in `api_keys.constraints`):
```sql
ALTER TABLE api_keys ADD COLUMN constraints TEXT;  -- JSON
```

**Constraint Evaluation Order**:
1. Check tier allows operation type
2. Check per-key operation override
3. Check calendar allowlist
4. Check field constraints (attendees, duration, etc.)
5. If any constraint fails with "deny" → reject immediately
6. If any constraint triggers "require_approval" → queue for approval
7. Otherwise → auto-approve (for read operations or admin tier)

### 5.4 Approval Requirements Matrix

Default per-tier settings (overridable per-key):

| Operation | Read Tier | Write Tier (Default) | Admin Tier |
|-----------|-----------|---------------------|------------|
| List calendars | Auto | Auto | Auto |
| List events | Auto | Auto | Auto |
| Get event | Auto | Auto | Auto |
| Freebusy | Auto | Auto | Auto |
| Create event | Denied | Requires approval | Auto (logged) |
| Update event | Denied | Requires approval | Auto (logged) |
| Delete event | Denied | Requires approval | Auto (logged) |

### 5.5 Rate Limiting

| Tier | Requests/Minute | Burst |
|------|-----------------|-------|
| Read | 60 | 10 |
| Write | 30 | 5 |
| Admin | 120 | 20 |

Implementation: Token bucket algorithm with in-memory storage. Counters reset on restart (acceptable for single-user).

### 5.5 Web UI Authentication

**Primary**: Local password authentication
- Password hash set via `SCHEDLOCK_AUTH_PASSWORD_HASH` (Argon2id)
- Optional dev-only plaintext via `SCHEDLOCK_ADMIN_PASSWORD`
- Hash is configured via env and is not persisted to the database
- Session cookie: HTTP-only, Secure, SameSite=Strict

**Optional Enhancement**: Cloudflare Access
- Use Cloudflare Access at the reverse proxy or tunnel layer
- Built-in JWT validation is not implemented in the current build

### 5.6 Session Management

```
Cookie: session_id=<random_32_bytes_base64>
Expiry: 24 hours
Refresh: On each authenticated request (sliding window)
```

---

## 6. Notification System

### 6.1 Provider Architecture

```
┌─────────────────────────────────────────────────────────┐
│                   Notifier Manager                      │
│                                                         │
│  ┌─────────────────────────────────────────────────┐   │
│  │            NotificationDispatcher               │   │
│  │                                                 │   │
│  │  for provider in enabled_providers:            │   │
│  │      try:                                      │   │
│  │          provider.send(request)               │   │
│  │      except:                                  │   │
│  │          log_failure(provider, error)        │   │
│  │          continue  # Don't block others      │   │
│  └─────────────────────────────────────────────────┘   │
│                        │                                │
│         ┌──────────────┼──────────────┐                │
│         ▼              ▼              ▼                │
│  ┌───────────┐  ┌───────────┐  ┌───────────┐          │
│  │   Ntfy    │  │ Pushover  │  │ Telegram  │          │
│  │ Provider  │  │ Provider  │  │ Provider  │          │
│  │           │  │           │  │           │          │
│  │ Actions:  │  │ Actions:  │  │ Actions:  │          │
│  │ HTTP POST │  │ Open URL  │  │ Inline KB │          │
│  └───────────┘  └───────────┘  └───────────┘          │
└─────────────────────────────────────────────────────────┘
```

### 6.2 Provider Interface

```go
type NotificationProvider interface {
    // Name returns provider identifier (e.g., "ntfy", "pushover", "telegram")
    Name() string
    
    // Enabled returns whether provider is configured and active
    Enabled() bool
    
    // SendApprovalRequest sends notification for pending approval
    // Returns nil on success, error on failure
    SendApprovalRequest(ctx context.Context, req ApprovalNotification) error
    
    // SupportsDirectActions returns true if provider can handle approve/deny
    // directly (ntfy HTTP actions, Telegram inline keyboards)
    SupportsDirectActions() bool
    
    // SupportsInlineSuggestions returns true if provider can accept text input
    // for suggestions (currently only Telegram via reply-to-message)
    SupportsInlineSuggestions() bool
}

type ApprovalNotification struct {
    RequestID   string
    Operation   string            // "create_event", "update_event", "delete_event"
    Summary     string            // Human-readable title
    Details     EventDetails      // Structured event information
    ApproveURL  string            // Callback URL for approval
    DenyURL     string            // Callback URL for denial
    SuggestURL  string            // Web UI URL for suggesting changes
    WebURL      string            // Web UI URL for full review
    ExpiresAt   time.Time
    ExpiresIn   string            // Human-readable, e.g., "1 hour"
}

type EventDetails struct {
    Title       string
    StartTime   time.Time
    EndTime     time.Time
    Location    string
    Attendees   []string
    Description string
}
```

### 6.3 Provider Implementations

#### 6.3.1 ntfy Provider

**Capabilities**: Full bidirectional with HTTP action buttons

**Request Format**:
```http
POST https://ntfy.sh/{topic}
Authorization: Bearer {token}  // if auth configured

Headers:
  Title: Calendar: Create Event
  Priority: high
  Tags: calendar,moltbot
  Actions: http, Approve, {approve_url}, method=POST, clear=true; 
           http, Deny, {deny_url}, method=POST, clear=true;
           view, Suggest Change, {suggest_url}

Body:
Moltbot wants to create an event:

Title: {title}
When: {start_time} - {end_time}
Location: {location}
Attendees: {attendees}

Request: {request_id}
Expires: {expires_in}
```

**Configuration**:
```yaml
ntfy:
  enabled: true
  server: "https://ntfy.sh"      # or "https://ntfy.your-domain.com"
  topic: "calendar-approvals"    # should be secret/unguessable
  token: ""                      # optional access token
  priority: "high"               # low, default, high, urgent
  minimal_content: false         # Set true for third-party hosted ntfy
```

**Notes**:
- `clear=true` dismisses notification after action
- "Suggest Change" opens web UI for text input (ntfy can't do inline text)
- **Self-hosted ntfy strongly recommended for security**

**Content Minimization** (for third-party ntfy.sh):

When using the public ntfy.sh server, notification content traverses third-party infrastructure. Enable `minimal_content: true` to reduce information exposure:

| Field | Full Content | Minimal Content |
|-------|--------------|-----------------|
| Title | "Calendar: Create Event" | "Calendar Request" |
| Body | Full event details | "Review pending request" |
| Attendees | Listed | Not included |
| Location | Shown | Not included |

```yaml
# Minimal notification body
Body:
Moltbot submitted a calendar request.
Tap to review details.

Request: {request_id}
Expires: {expires_in}
```

**Recommendation**: Self-host ntfy if you need full event details in notifications. Use minimal content mode only if self-hosting isn't possible.

#### 6.3.2 Pushover Provider

**Capabilities**: Opens web UI for approval (no inline actions)

**Request Format**:
```http
POST https://api.pushover.net/1/messages.json
Content-Type: application/x-www-form-urlencoded

token={app_token}
&user={user_key}
&title=Calendar: Create Event
&message=Moltbot wants to create:

{title}
{start_time}

Tap to review and approve.

Request: {request_id}
&priority=1
&url={web_url}
&url_title=Review Request
&sound=pushover
```

**Configuration**:
```yaml
pushover:
  enabled: true
  app_token: "azGDORePK8..."    # from Pushover dashboard
  user_key: "uQiRzpo4Dx..."     # your user key
  priority: 1                    # -2 (silent) to 2 (emergency)
  sound: "pushover"              # notification sound
```

**Notes**:
- User must tap notification and open web UI to approve/deny
- Priority 2 (emergency) requires acknowledgment and repeats
- Good for users already in Pushover ecosystem

#### 6.3.3 Telegram Provider

**Capabilities**: Full bidirectional with inline keyboard buttons + reply-to-message for suggestions

**Send Message**:
```http
POST https://api.telegram.org/bot{token}/sendMessage
Content-Type: application/json

{
  "chat_id": "{chat_id}",
  "text": "*Calendar Request*\n\nMoltbot wants to create an event:\n\n*{title}*\nWhen: {start_time} - {end_time}\nLocation: {location}\n\nRequest: `{request_id}`\nExpires: {expires_in}\n\n_Reply to this message to suggest changes_",
  "parse_mode": "Markdown",
  "reply_markup": {
    "inline_keyboard": [[
      {"text": "Approve", "callback_data": "approve:{request_id}:{signature}"},
      {"text": "Deny", "callback_data": "deny:{request_id}:{signature}"}
    ], [
      {"text": "Suggest Change", "callback_data": "suggest:{request_id}:{signature}"},
      {"text": "View Details", "url": "{web_url}"}
    ]]
  }
}
```

**Suggest Change Flow**:
1. User taps "Suggest Change" button
2. Bot replies: "Please reply to this message with your suggested changes"
3. User replies with text (e.g., "Move to 3pm and add Bob")
4. Webhook receives the reply, matches it to the original request via `reply_to_message`
5. Proxy stores suggestion and notifies Moltbot

**CRITICAL: Message ID Persistence**

To match reply messages to pending requests, the proxy must persist Telegram's `message_id` when sending notifications:

```go
// When sending notification
resp, err := telegram.SendMessage(...)
if err == nil {
    // Store the message_id for reply matching
    db.UpdateNotificationLog(requestID, "telegram", NotificationLogUpdate{
        MessageID: strconv.Itoa(resp.Result.MessageID),
    })
}

// When receiving a reply
func handleTelegramMessage(update TelegramUpdate) {
    if update.Message.ReplyToMessage == nil {
        return  // Not a reply
    }
    
    replyToMsgID := update.Message.ReplyToMessage.MessageID
    
    // Find which request this reply belongs to
    notifLog, err := db.FindNotificationByMessageID("telegram", strconv.Itoa(replyToMsgID))
    if err != nil {
        // Unknown message - ignore or reply with error
        return
    }
    
    // Now we have the request_id
    processSuggestion(notifLog.RequestID, update.Message.Text)
}
```

**Schema** (notification_log table already includes message_id):
```sql
-- message_id column stores provider-specific message identifier
-- For Telegram: the message_id returned by sendMessage
-- Used to correlate reply_to_message with pending requests
```

**Edge Cases**:
- Service restart: message_id mappings persist in DB
- Multiple notifications for same request: each has its own message_id
- Reply to wrong message: check request status before processing

**Webhook Handler**:
```
POST /webhooks/telegram

Handles TWO types of updates:

1. Callback Query (button press):
   - Parse callback_data: "approve:req_abc123:sig_xyz"
   - Validate signature
   - Process approval/denial/suggest-prompt
   - Answer callback query to dismiss loading state
   - Edit message to show result

2. Message Reply (suggestion text):
   - Check reply_to_message.message_id exists
   - Look up message_id in notification_log to find request_id
   - Verify request status is still pending_approval
   - Extract suggestion text from message.text
   - Store suggestion, update status to change_requested
   - Reply with confirmation
   - Notify Moltbot via webhook

Validates:
- X-Telegram-Bot-Api-Secret-Token header matches configured secret
- Callback data signature is valid (for button presses)
- Request is still pending
```

**Configuration**:
```yaml
telegram:
  enabled: true
  bot_token: "123456789:ABC-DEF..."
  chat_id: "987654321"           # user or group chat ID
  webhook_secret: "random_string" # validates incoming webhooks
  auto_register_webhook: true    # automatically register webhook on startup
```

**Automatic Webhook Registration**:

On startup (if `auto_register_webhook: true`), the proxy automatically registers the Telegram webhook with **retry logic to handle Cloudflare Tunnel startup delays**:

```go
func (t *TelegramProvider) RegisterWebhookWithRetry(baseURL string) error {
    webhookURL := fmt.Sprintf("%s/webhooks/telegram", baseURL)
    
    // Retry with backoff - tunnel may take 5-15 seconds to establish
    maxRetries := 5
    backoff := []time.Duration{2*time.Second, 5*time.Second, 10*time.Second, 20*time.Second, 30*time.Second}
    
    for attempt := 0; attempt < maxRetries; attempt++ {
        // First, verify our own URL is reachable (tunnel is up)
        if err := t.checkURLReachable(webhookURL); err != nil {
            log.Printf("SCHEDLOCK_BASE_URL not reachable (attempt %d/%d): %v", attempt+1, maxRetries, err)
            if attempt < maxRetries-1 {
                time.Sleep(backoff[attempt])
                continue
            }
            return fmt.Errorf("SCHEDLOCK_BASE_URL not reachable after %d attempts: %w", maxRetries, err)
        }
        
        // URL is reachable, register with Telegram
        payload := map[string]interface{}{
            "url":          webhookURL,
            "secret_token": t.config.WebhookSecret,
            "allowed_updates": []string{
                "callback_query",  // Button presses
                "message",         // Reply messages for suggestions
            },
        }
        
        resp, err := http.Post(
            fmt.Sprintf("https://api.telegram.org/bot%s/setWebhook", t.config.BotToken),
            "application/json",
            bytes.NewReader(jsonPayload),
        )
        if err == nil && resp.StatusCode == 200 {
            log.Printf("Telegram webhook registered successfully: %s", webhookURL)
            return nil
        }
        
        log.Printf("Telegram registration failed (attempt %d/%d): %v", attempt+1, maxRetries, err)
        if attempt < maxRetries-1 {
            time.Sleep(backoff[attempt])
        }
    }
    
    return fmt.Errorf("failed to register Telegram webhook after %d attempts", maxRetries)
}

func (t *TelegramProvider) checkURLReachable(url string) error {
    client := &http.Client{Timeout: 5 * time.Second}
    // Hit our own health endpoint through the public URL
    resp, err := client.Get(strings.Replace(url, "/webhooks/telegram", "/health", 1))
    if err != nil {
        return err
    }
    defer resp.Body.Close()
    if resp.StatusCode != 200 {
        return fmt.Errorf("health check returned %d", resp.StatusCode)
    }
    return nil
}
```

**CRITICAL**: The Web UI Settings → Notifications page should have a **prominent "Re-register Webhook" button** as a fallback if auto-registration fails.

This runs:
- On first startup after Telegram is enabled (with retry)
- When `SCHEDLOCK_BASE_URL` changes
- Via "Re-register Webhook" button in Web UI settings (no retry, immediate feedback)

**Manual Setup** (if auto-registration fails):
1. Create bot via @BotFather
2. Get chat ID (send message to bot, use `/start`, then check `getUpdates`)
3. Use "Re-register Webhook" button in Web UI, or manually via Telegram API

### 6.4 Dispatch Strategy

When approval is required:

1. **Parallel dispatch**: All enabled providers receive notification simultaneously
2. **First response wins**: Once approved/denied via any channel, request is resolved
3. **Idempotent processing**: Duplicate responses (user clicks twice) are ignored
4. **Failure isolation**: One provider failing doesn't block others
5. **Web UI fallback**: Always available regardless of provider status

### 6.5 Callback Security

Callback URLs use **single-use decision tokens** stored server-side to prevent replay and forgery:

```
URL Format: /api/callback/{action}/{decision_token}
Example: /api/callback/approve/dtok_x7k9m2p4q8r1s5t3

Decision Token:
- Generated: Random 128-bit value, base62 encoded
- Stored: SHA-256 hash in decision_tokens table
- Scope: Tied to specific request_id and allowed action(s)
- Expiry: Same as request.expires_at
- Single-use: Consumed on first valid use
```

**Database Schema Addition**:
```sql
CREATE TABLE decision_tokens (
    token_hash TEXT PRIMARY KEY,          -- SHA-256 of token
    request_id TEXT NOT NULL REFERENCES requests(id),
    allowed_actions TEXT NOT NULL,        -- JSON array: ["approve", "deny", "suggest"]
    expires_at TEXT NOT NULL,
    consumed_at TEXT,
    consumed_action TEXT,
    created_at TEXT DEFAULT (datetime('now'))
);

CREATE INDEX idx_decision_tokens_request ON decision_tokens(request_id);
CREATE INDEX idx_decision_tokens_expires ON decision_tokens(expires_at) WHERE consumed_at IS NULL;
```

**Validation Steps**:
1. Hash incoming token, look up in `decision_tokens`
2. Check `consumed_at IS NULL` (not already used)
3. Check `expires_at > NOW()` (not expired)
4. Check action is in `allowed_actions`
5. Look up associated request, verify status is `pending_approval`
6. **Atomically**: Mark token consumed AND update request status
   ```sql
   BEGIN;
   UPDATE decision_tokens SET consumed_at = NOW(), consumed_action = ? WHERE token_hash = ? AND consumed_at IS NULL;
   UPDATE requests SET status = ?, decided_at = NOW(), decided_by = ? WHERE id = ? AND status = 'pending_approval';
   COMMIT;
   ```
7. If already consumed with same action, return 200 OK (idempotent)
8. If already consumed with different action, return 409 Conflict

**Why not stateless HMAC?**
Stateless tokens can't be single-use. With HMAC-only, a user who intercepts the URL could replay it. The decision token approach ensures each approval/denial can only happen once, and the "first response wins" rule is enforced at the database level.

**Token Generation** (one token per request, valid for all actions):
```go
func generateDecisionToken(requestID string, expiresAt time.Time) (string, error) {
    tokenBytes := make([]byte, 16) // 128 bits
    rand.Read(tokenBytes)
    token := "dtok_" + base62.Encode(tokenBytes)
    
    tokenHash := sha256.Sum256([]byte(token))
    
    _, err := db.Exec(`
        INSERT INTO decision_tokens (token_hash, request_id, allowed_actions, expires_at)
        VALUES (?, ?, ?, ?)`,
        hex.EncodeToString(tokenHash[:]),
        requestID,
        `["approve", "deny", "suggest"]`,
        expiresAt.Format(time.RFC3339),
    )
    return token, err
}
```

---

## 7. Approval Workflow

### 7.1 State Machine

```
                                ┌──────────────────┐
                                │                  │
                    ┌──────────►│ pending_approval │◄──────────┐
                    │           │                  │           │
                    │           └────────┬─────────┘           │
                    │                    │                     │
                    │      ┌─────────────┼─────────────┐       │
                    │      │             │             │       │
                    │      ▼             ▼             ▼       │
                    │ ┌─────────┐  ┌──────────┐  ┌─────────┐  │
                    │ │approved │  │  denied  │  │ expired │  │
                    │ └────┬────┘  └──────────┘  └────┬────┘  │
                    │      │                          │       │
                    │      │      ┌───────────────────┘       │
                    │      │      │ (if default=approve)      │
                    │      ▼      ▼                           │
                    │    ┌──────────┐                         │
                    │    │executing │                         │
                    │    └────┬─────┘                         │
                    │         │                               │
                    │    ┌────┴────┐                          │
                    │    │         │                          │
                    │    ▼         ▼                          │
                    │ ┌─────────┐ ┌──────┐                   │
                    │ │completed│ │failed│───────────────────┘
                    │ └─────────┘ └──────┘  (retry if transient)
                    │
               [new request]
                    │
                    │           ┌───────────────────┐
                    │           │                   │
                    └───────────│ change_requested  │
                      (cancel   │                   │
                       or new   └─────────┬─────────┘
                       request)           │
                                          │ Bot receives suggestion,
                                          │ creates new modified request
                                          │ (original stays in this state
                                          │  or is cancelled)
                                          ▼
                                   [new request with changes]
```

**change_requested State**:
- Entered when approver submits a suggestion instead of approve/deny
- Contains `suggestion` field with approver's requested changes
- Bot should parse suggestion, modify request, and submit new request
- Original request can be cancelled or left as-is (for audit trail)
- Does NOT auto-execute—requires new approval cycle

### 7.2 Timeout Handling

**Configuration**:
```yaml
approval:
  timeout_minutes: 60              # Default: 60 minutes
  default_action: "deny"           # "approve" or "deny"
  
  # Per-operation overrides (optional)
  operation_timeouts:
    delete_event:
      timeout_minutes: 30          # Shorter for destructive ops
      default_action: "deny"       # Always deny deletes on timeout
```

**Background Worker**:

Runs every 30 seconds. **Uses transactional state transitions** to avoid races with concurrent approval callbacks.

```go
func (w *TimeoutWorker) processExpiredRequests(ctx context.Context) {
    // Find expired requests
    rows, _ := w.db.Query(`
        SELECT id, operation FROM requests 
        WHERE status = 'pending_approval' AND expires_at < datetime('now')
    `)
    
    for rows.Next() {
        var id, operation string
        rows.Scan(&id, &operation)
        
        defaultAction := w.getDefaultAction(operation)
        newStatus := "expired"
        if defaultAction == "approve" {
            newStatus = "approved"
        }
        
        // CRITICAL: Transactional update prevents race with approval callback
        result, err := w.db.Exec(`
            UPDATE requests 
            SET status = ?, decided_at = datetime('now'), decided_by = 'timeout'
            WHERE id = ? AND status = 'pending_approval'
        `, newStatus, id)
        
        rowsAffected, _ := result.RowsAffected()
        if rowsAffected == 0 {
            // Request was already decided by callback - skip
            log.Debug("Request already decided, skipping timeout", "request_id", id)
            continue
        }
        
        // Log to audit trail
        w.auditLog.Record("request_" + newStatus, id, "timeout")
        
        // If auto-approved on timeout, queue for execution
        if newStatus == "approved" {
            w.executionQueue.Enqueue(id)
        }
        
        // Send webhook notification
        w.notifyMoltbot(id, newStatus)
    }
}
```

**Race Condition Prevention**:

| Scenario | Handling |
|----------|----------|
| Callback wins | `UPDATE ... WHERE status='pending'` affects 0 rows for timeout worker |
| Timeout wins | `UPDATE ... WHERE status='pending'` affects 0 rows for callback |
| Concurrent callbacks | First `UPDATE` wins, second affects 0 rows (idempotent) |

The `WHERE status = 'pending_approval'` clause ensures only one actor can transition the state.

### 7.3 Retry Logic

For transient Google API failures:

```yaml
retry:
  enabled: true
  max_attempts: 3
  backoff_base_seconds: 5         # 5s, 10s, 20s
  retryable_errors:
    - 429    # Rate limited
    - 500    # Internal error
    - 502    # Bad gateway
    - 503    # Service unavailable
```

Non-retryable errors (immediately mark as failed):
- 400 Bad Request
- 401 Unauthorized (trigger re-auth flow)
- 403 Forbidden
- 404 Not Found

### 7.4 Sequence Diagram

```
Bot                 Proxy               Notifiers           Approver        Google
 │                    │                     │                   │              │
 │ POST /api/calendar/events/create │         │                   │              │
 │───────────────────►│                     │                   │              │
 │                    │                     │                   │              │
 │                    │ Validate request    │                   │              │
 │                    │ Store in DB         │                   │              │
 │                    │ status=pending      │                   │              │
 │                    │                     │                   │              │
 │ 202 Accepted       │ Send to all enabled │                   │              │
 │◄───────────────────│────────────────────►│                   │              │
 │ {request_id}       │                     │                   │              │
 │                    │                     │ Push notification │              │
 │                    │                     │──────────────────►│              │
 │                    │                     │                   │              │
 │ GET /api/requests/{id} │                  │                   │              │
 │───────────────────►│                     │                   │              │
 │ {status:pending}   │                     │                   │              │
 │◄───────────────────│                     │                   │              │
 │                    │                     │                   │              │
 │      ...           │                     │   Tap Approve     │              │
 │                    │                     │◄──────────────────│              │
 │                    │                     │                   │              │
 │                    │ POST /api/callback/ │                   │              │
 │                    │   approve/{token}   │                   │              │
 │                    │◄────────────────────│                   │              │
 │                    │                     │                   │              │
 │                    │ Validate token      │                   │              │
 │                    │ Update status=approved                  │              │
 │                    │                     │                   │              │
 │                    │ POST Google Calendar API                 │              │
 │                    │─────────────────────────────────────────────────────►│
 │                    │                     │                   │              │
 │                    │ 200 OK {eventId}    │                   │              │
 │                    │◄─────────────────────────────────────────────────────│
 │                    │                     │                   │              │
 │                    │ Update status=completed                 │              │
 │                    │ Store result        │                   │              │
 │                    │                     │                   │              │
 │ GET /api/requests/{id} │                  │                   │              │
 │───────────────────►│                     │                   │              │
 │ {status:completed, result:{...}}          │                   │              │
 │◄───────────────────│                     │                   │              │
```

### 7.5 Suggest Change Sequence

```
Bot                 Proxy               Notifiers           Approver        Moltbot
 │                    │                     │                   │           Webhook
 │ POST /api/calendar/events/create │        │                   │              │
 │───────────────────►│                     │                   │              │
 │                    │ Store pending       │                   │              │
 │ 202 Accepted       │                     │                   │              │
 │◄───────────────────│ Send notifications  │                   │              │
 │                    │────────────────────►│                   │              │
 │                    │                     │──────────────────►│              │
 │                    │                     │                   │              │
 │                    │                     │  Tap "Suggest     │              │
 │                    │                     │   Change"         │              │
 │                    │                     │◄──────────────────│              │
 │                    │                     │                   │              │
 │                    │                     │  "Move to 3pm"    │              │
 │                    │                     │◄──────────────────│              │
 │                    │                     │   (via reply or   │              │
 │                    │ POST suggestion     │    web UI)        │              │
 │                    │◄────────────────────│                   │              │
 │                    │                     │                   │              │
 │                    │ Update status=      │                   │              │
 │                    │  change_requested   │                   │              │
 │                    │ Store suggestion    │                   │              │
 │                    │                     │                   │              │
 │                    │ POST /hooks/agent   │                   │              │
 │                    │─────────────────────────────────────────────────────►│
 │                    │ {sessionKey, message: "User suggested: Move to 3pm"} │
 │                    │                     │                   │              │
 │◄────────────────────────────────────────────────────────────────────────────
 │  (Moltbot receives │                     │                   │              │
 │   webhook, parses  │                     │                   │              │
 │   suggestion)      │                     │                   │              │
 │                    │                     │                   │              │
 │ POST /api/calendar/events/create │        │                   │              │
 │  (modified)        │                     │                   │              │
 │───────────────────►│                     │                   │              │
 │                    │ New approval cycle begins...            │              │
```

### 7.6 Moltbot Webhook Integration

The proxy pushes status updates to Moltbot via the `/hooks/agent` endpoint, enabling real-time notification without polling.

**Configuration**:
```yaml
moltbot:
  webhook:
    enabled: true
    url: "http://localhost:18789/hooks/agent"  # Moltbot gateway URL
    token: "shared-secret"                      # hooks.token from Moltbot config
    session_key_prefix: "calendar-proxy"        # Prefix for session keys
    
    # Delivery settings
    timeout_seconds: 10
    max_retries: 3
    retry_backoff: [1, 5, 15]                  # Seconds between retries
    
    # Which events trigger webhooks
    notify_on:
      - approved
      - denied
      - expired
      - change_requested
      - completed
      - failed
```

**Webhook Request**:
```http
POST http://localhost:18789/hooks/agent
Authorization: Bearer {token}
Content-Type: application/json
X-Webhook-ID: whk_a1b2c3d4e5f6           # Idempotency key for this delivery
X-Webhook-Timestamp: 1706450400          # Unix timestamp
X-Webhook-Signature: sha256=a1b2c3d4...  # HMAC-SHA256(token, timestamp + "." + body)

{
  "message": "...",
  "name": "CalendarProxy",
  "sessionKey": "calendar-proxy:req_a1b2c3d4",
  "wakeMode": "now",
  "deliver": true,
  "channel": "last"
}
```

**Signature Computation**:
```go
func signWebhook(token string, timestamp int64, body []byte) string {
    message := fmt.Sprintf("%d.%s", timestamp, body)
    mac := hmac.New(sha256.New, []byte(token))
    mac.Write([]byte(message))
    return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
```

**Webhook Payload** (for completed request):
```json
{
  "message": "Calendar request update: Your event creation request was approved and completed.\n\nEvent: Project Review\nTime: Jan 30, 2026 3:00 PM (America/New_York)\nLink: https://calendar.google.com/event?eid=xyz",
  "name": "CalendarProxy",
  "sessionKey": "calendar-proxy:req_a1b2c3d4",
  "wakeMode": "now",
  "deliver": true,
  "channel": "last"
}
```

**For change_requested** (prompt Moltbot to modify and resubmit):
```json
{
  "message": "Calendar request needs changes.\n\nOriginal request:\n- Operation: create_event\n- Summary: Project Review\n- Time: Jan 30, 2026 10:00 AM\n- Location: Conference Room A\n\nUser suggestion: \"Move to 3pm instead, and add Bob to attendees\"\n\nPlease modify the request based on this feedback and resubmit, or ask the user for clarification if the suggestion is unclear.",
  "name": "CalendarProxy", 
  "sessionKey": "calendar-proxy:req_a1b2c3d4",
  "wakeMode": "now",
  "deliver": false
}
```

**Delivery Semantics: "At Least Once"**

Webhooks may be delivered more than once due to retries. Moltbot should handle this:
- The `X-Webhook-ID` header is unique per status transition (not per retry)
- Moltbot can dedupe by tracking seen webhook IDs for 24 hours
- If Moltbot crashes mid-processing, the webhook may be re-delivered on proxy restart

**Retry Logic**:
```go
func (w *WebhookClient) Deliver(ctx context.Context, event WebhookEvent) error {
    webhookID := fmt.Sprintf("whk_%s_%s", event.RequestID, event.Status)
    
    for attempt := 0; attempt <= w.config.MaxRetries; attempt++ {
        if attempt > 0 {
            time.Sleep(w.config.RetryBackoff[attempt-1])
        }
        
        resp, err := w.send(ctx, webhookID, event)
        if err == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
            return nil  // Success
        }
        
        // Log failure, continue to next retry
        log.Printf("Webhook delivery failed (attempt %d): %v", attempt+1, err)
    }
    
    // All retries exhausted - log to webhook_failures table for manual review
    w.recordFailure(webhookID, event)
    return fmt.Errorf("webhook delivery failed after %d attempts", w.config.MaxRetries+1)
}
```

**Webhook Failures Table** (for observability):
```sql
CREATE TABLE webhook_failures (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    webhook_id TEXT NOT NULL,
    request_id TEXT NOT NULL,
    status TEXT NOT NULL,
    payload TEXT NOT NULL,
    error TEXT,
    attempts INTEGER,
    created_at TEXT DEFAULT (datetime('now')),
    resolved_at TEXT
);
```

Note: `deliver: false` for change_requested since the bot should process internally and respond, not just echo the message.

---

## 8. Database Schema

### 8.1 Entity Relationship

```
┌─────────────┐       ┌─────────────┐       ┌─────────────────┐
│  api_keys   │       │  requests   │       │   audit_log     │
├─────────────┤       ├─────────────┤       ├─────────────────┤
│ id (PK)     │◄──────│ api_key_id  │       │ id (PK)         │
│ key_hash    │       │ id (PK)     │◄──────│ request_id (FK) │
│ tier        │       │ operation   │       │ api_key_id (FK) │
│ name        │       │ status      │       │ event_type      │
│ ...         │       │ payload     │       │ timestamp       │
└─────────────┘       │ result      │       │ ...             │
                      │ ...         │       └─────────────────┘
                      └─────────────┘
                            │
                            ▼
                   ┌─────────────────┐
                   │notification_log │
                   ├─────────────────┤
                   │ id (PK)         │
                   │ request_id (FK) │
                   │ provider        │
                   │ status          │
                   │ ...             │
                   └─────────────────┘
```

### 8.2 Table Definitions

```sql
-- Enable WAL mode for better concurrency
PRAGMA journal_mode=WAL;
PRAGMA busy_timeout=5000;
PRAGMA synchronous=NORMAL;
PRAGMA foreign_keys=ON;

-- API Keys
CREATE TABLE api_keys (
    id TEXT PRIMARY KEY,                    -- "key_" + nanoid(16)
    key_hash TEXT UNIQUE NOT NULL,          -- SHA-256(full_key)
    key_prefix TEXT NOT NULL,               -- First 12 chars for display
    name TEXT NOT NULL,                     -- Human-readable identifier
    tier TEXT NOT NULL CHECK (tier IN ('read', 'write', 'admin')),
    created_at TEXT DEFAULT (datetime('now')),
    last_used_at TEXT,
    expires_at TEXT,                        -- NULL = never expires
    revoked_at TEXT,
    rate_limit_override INTEGER,            -- NULL = use tier default
    metadata TEXT                           -- JSON for future extensions
);

CREATE INDEX idx_api_keys_hash ON api_keys(key_hash);
CREATE INDEX idx_api_keys_tier ON api_keys(tier) WHERE revoked_at IS NULL;


-- Requests (pending and completed)
CREATE TABLE requests (
    id TEXT PRIMARY KEY,                    -- "req_" + nanoid(16)
    api_key_id TEXT NOT NULL REFERENCES api_keys(id),
    operation TEXT NOT NULL CHECK (operation IN (
        'create_event', 'update_event', 'delete_event'
    )),
    status TEXT NOT NULL DEFAULT 'pending_approval' CHECK (status IN (
        'pending_approval', 'change_requested', 'approved', 'denied', 'expired',
        'executing', 'completed', 'failed'
    )),
    payload TEXT NOT NULL,                  -- JSON: original request body
    result TEXT,                            -- JSON: Google API response
    error TEXT,                             -- Error message on failure
    suggestion_text TEXT,                   -- User's suggested changes (if change_requested)
    suggestion_at TEXT,                     -- When suggestion was submitted
    suggestion_by TEXT,                     -- 'ntfy', 'pushover', 'telegram', 'web_ui'
    created_at TEXT DEFAULT (datetime('now')),
    expires_at TEXT NOT NULL,
    decided_at TEXT,
    decided_by TEXT,                        -- 'ntfy', 'pushover', 'telegram', 'web_ui', 'timeout'
    executed_at TEXT,
    retry_count INTEGER DEFAULT 0,
    webhook_notified_at TEXT,               -- When Moltbot webhook was sent
    FOREIGN KEY (api_key_id) REFERENCES api_keys(id)
);

CREATE INDEX idx_requests_status ON requests(status);
CREATE INDEX idx_requests_pending ON requests(expires_at) 
    WHERE status = 'pending_approval';
CREATE INDEX idx_requests_api_key ON requests(api_key_id);
CREATE INDEX idx_requests_created ON requests(created_at);


-- Audit Log (append-only)
CREATE TABLE audit_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp TEXT DEFAULT (datetime('now')),
    event_type TEXT NOT NULL,               -- See event types below
    request_id TEXT REFERENCES requests(id),
    api_key_id TEXT REFERENCES api_keys(id),
    actor TEXT,                             -- Who/what triggered event
    details TEXT,                           -- JSON: event-specific data
    ip_address TEXT
);

CREATE INDEX idx_audit_timestamp ON audit_log(timestamp);
CREATE INDEX idx_audit_type ON audit_log(event_type);
CREATE INDEX idx_audit_request ON audit_log(request_id);

-- Event types:
-- api_key_created, api_key_revoked, api_key_used
-- request_created, request_approved, request_denied, request_expired
-- request_executing, request_completed, request_failed
-- notification_sent, notification_failed, callback_received
-- settings_changed, oauth_connected, oauth_refreshed


-- Notification Delivery Log
CREATE TABLE notification_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    request_id TEXT NOT NULL REFERENCES requests(id),
    provider TEXT NOT NULL CHECK (provider IN ('ntfy', 'pushover', 'telegram')),
    status TEXT NOT NULL CHECK (status IN ('pending', 'sent', 'failed', 'callback_received')),
    sent_at TEXT DEFAULT (datetime('now')),
    callback_at TEXT,
    error TEXT,
    response TEXT,                          -- JSON: provider response
    message_id TEXT                         -- Provider's message ID for updates
);

CREATE INDEX idx_notification_request ON notification_log(request_id);
CREATE INDEX idx_notification_provider ON notification_log(provider, status);


-- Google OAuth Tokens (encrypted at rest)
CREATE TABLE oauth_tokens (
    id TEXT PRIMARY KEY DEFAULT 'primary',
    access_token_enc BLOB NOT NULL,         -- AES-256-GCM encrypted
    refresh_token_enc BLOB NOT NULL,
    token_type TEXT DEFAULT 'Bearer',
    expiry TEXT,
    scopes TEXT,                            -- Space-separated scope list
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now'))
);


-- Settings (key-value store)
CREATE TABLE settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,                    -- JSON
    updated_at TEXT DEFAULT (datetime('now'))
);


-- Sessions for Web UI
CREATE TABLE sessions (
    id TEXT PRIMARY KEY,                    -- Secure random 32 bytes, base64
    created_at TEXT DEFAULT (datetime('now')),
    expires_at TEXT NOT NULL,
    last_activity TEXT,
    ip_address TEXT,
    user_agent TEXT
);

CREATE INDEX idx_sessions_expires ON sessions(expires_at);
```

### 8.3 Encryption

OAuth tokens are encrypted using AES-256-GCM:

```go
type TokenEncryption struct {
    key []byte  // 32 bytes from SCHEDLOCK_ENCRYPTION_KEY env var
}

func (e *TokenEncryption) Encrypt(plaintext string) ([]byte, error) {
    block, _ := aes.NewCipher(e.key)
    gcm, _ := cipher.NewGCM(block)
    nonce := make([]byte, gcm.NonceSize())
    io.ReadFull(rand.Reader, nonce)
    return gcm.Seal(nonce, nonce, []byte(plaintext), nil), nil
}

func (e *TokenEncryption) Decrypt(ciphertext []byte) (string, error) {
    block, _ := aes.NewCipher(e.key)
    gcm, _ := cipher.NewGCM(block)
    nonceSize := gcm.NonceSize()
    nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
    plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
    return string(plaintext), err
}
```

Note: `SCHEDLOCK_ENCRYPTION_KEY` is required in the current implementation (no automatic derivation).

### 8.4 Google OAuth Token Refresh

**CRITICAL**: Google access tokens expire after 1 hour. The proxy must handle token refresh **before every Google API call** to avoid failures mid-workflow (especially after approval when executing).

```go
type GoogleCalClient struct {
    db        *sql.DB
    encryptor *TokenEncryption
    mu        sync.Mutex  // Serialize token refresh
}

func (c *GoogleCalClient) GetValidToken(ctx context.Context) (*oauth2.Token, error) {
    c.mu.Lock()
    defer c.mu.Unlock()
    
    token, err := c.loadTokenFromDB()
    if err != nil {
        return nil, fmt.Errorf("no OAuth token configured: %w", err)
    }
    
    // Check if token expires within 5 minutes (buffer for request execution)
    if token.Expiry.After(time.Now().Add(5 * time.Minute)) {
        return token, nil  // Token is still valid
    }
    
    // Token expired or expiring soon - refresh it
    log.Info("Access token expired or expiring, refreshing...")
    
    newToken, err := c.refreshToken(ctx, token.RefreshToken)
    if err != nil {
        // Log the failure - this is critical
        c.db.AuditLog("oauth_refresh_failed", err.Error())
        return nil, fmt.Errorf("token refresh failed: %w", err)
    }
    
    // IMPORTANT: Save immediately - Google may rotate refresh_token
    if err := c.saveTokenToDB(newToken); err != nil {
        log.Error("Failed to save refreshed token to DB", "error", err)
        // Continue anyway - we have a valid token in memory
    }
    
    c.db.AuditLog("oauth_refreshed", map[string]interface{}{
        "new_expiry": newToken.Expiry,
    })
    
    return newToken, nil
}

func (c *GoogleCalClient) refreshToken(ctx context.Context, refreshToken string) (*oauth2.Token, error) {
    config := c.oauthConfig()
    
    // Use the refresh token to get a new access token
    tokenSource := config.TokenSource(ctx, &oauth2.Token{
        RefreshToken: refreshToken,
    })
    
    return tokenSource.Token()
}

func (c *GoogleCalClient) CallGoogleAPI(ctx context.Context, fn func(*calendar.Service) error) error {
    token, err := c.GetValidToken(ctx)
    if err != nil {
        return err
    }
    
    client := c.oauthConfig().Client(ctx, token)
    service, err := calendar.NewService(ctx, option.WithHTTPClient(client))
    if err != nil {
        return err
    }
    
    return fn(service)
}
```

**Token Refresh Scenarios**:

| Scenario | Handling |
|----------|----------|
| Bot submits request, token valid | Store request, token not used yet |
| Approval happens 45 min later, token expired | Refresh before executing |
| Refresh fails (refresh token revoked) | Mark request as `failed`, notify user to re-authenticate |
| Google rotates refresh token | Save new refresh token immediately |
| Multiple concurrent requests | Mutex ensures single refresh, others wait |

**Important**: Only store the refresh token in DB (encrypted). Access tokens can be kept in memory with their expiry. This reduces the value of a DB leak.

```sql
-- Simplified oauth_tokens table (refresh token only)
CREATE TABLE oauth_tokens (
    id TEXT PRIMARY KEY DEFAULT 'primary',
    refresh_token_enc BLOB NOT NULL,    -- AES-256-GCM encrypted
    scopes TEXT,                        -- Space-separated scope list
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now'))
);
```

### 8.5 Timezone Handling

**Principle**: All API inputs/outputs use UTC (RFC3339). All user-facing displays use configured timezone.

```go
// Embed timezone database in binary (required for Alpine/scratch containers)
import _ "time/tzdata"

type DisplayFormatter struct {
    Location *time.Location
    DateFmt  string  // "Jan 2, 2006"
    TimeFmt  string  // "3:04 PM"
}

func NewDisplayFormatter(tz string) (*DisplayFormatter, error) {
    loc, err := time.LoadLocation(tz)
    if err != nil {
        return nil, fmt.Errorf("invalid timezone %q: %w", tz, err)
    }
    return &DisplayFormatter{
        Location: loc,
        DateFmt:  "Jan 2, 2006",
        TimeFmt:  "3:04 PM",
    }, nil
}

func (f *DisplayFormatter) FormatDateTime(t time.Time) string {
    local := t.In(f.Location)
    return local.Format(f.DateFmt + " at " + f.TimeFmt)
}

func (f *DisplayFormatter) FormatDateTimeWithZone(t time.Time) string {
    local := t.In(f.Location)
    zone, _ := local.Zone()
    return local.Format(f.DateFmt + " at " + f.TimeFmt) + " " + zone
}
```

**Usage in Notifications**:
```
WRONG: "Meeting at 2026-01-30T15:00:00Z"
RIGHT: "Meeting at Jan 30, 2026 at 10:00 AM EST"
```

**Web UI Templates** (Go templates):
```html
<td>{{ .Event.Start | localTime }}</td>

<!-- Template function -->
func localTime(t time.Time) string {
    return displayFormatter.FormatDateTimeWithZone(t)
}
```

### 8.6 Data Retention & Cleanup

**Background Worker** (runs daily at configured time):

```go
func (w *CleanupWorker) Run(ctx context.Context) {
    // 1. Delete old completed requests
    completedCutoff := time.Now().AddDate(0, 0, -w.config.CompletedRequestsDays)
    _, err := w.db.Exec(`
        DELETE FROM requests 
        WHERE status IN ('completed', 'denied', 'expired', 'failed', 'cancelled')
        AND created_at < ?`, completedCutoff)
    
    // 2. Delete old audit logs
    auditCutoff := time.Now().AddDate(0, 0, -w.config.AuditLogDays)
    _, err = w.db.Exec(`DELETE FROM audit_log WHERE timestamp < ?`, auditCutoff)
    
    // 3. Delete resolved webhook failures
    webhookCutoff := time.Now().AddDate(0, 0, -w.config.WebhookFailuresDays)
    _, err = w.db.Exec(`
        DELETE FROM webhook_failures 
        WHERE resolved_at IS NOT NULL AND created_at < ?`, webhookCutoff)
    
    // 4. Delete expired decision tokens
    _, err = w.db.Exec(`DELETE FROM decision_tokens WHERE expires_at < ?`, time.Now())
    
    // 5. Delete old idempotency keys
    idemCutoff := time.Now().AddDate(0, 0, -1)  // 24 hours
    _, err = w.db.Exec(`DELETE FROM idempotency_keys WHERE created_at < ?`, idemCutoff)
    
    // 6. VACUUM to reclaim space
    _, err = w.db.Exec(`VACUUM`)
    
    log.Info("Cleanup completed", 
        "requests_deleted", requestsDeleted,
        "audit_deleted", auditDeleted,
        "db_size_after", dbSize)
}
```

**Retention Configuration**:
```yaml
retention:
  enabled: true
  completed_requests_days: 90   # Keep completed requests for 90 days
  audit_log_days: 365           # Keep audit logs for 1 year
  webhook_failures_days: 30     # Keep resolved failures for 30 days
  vacuum_schedule: "0 3 * * *"  # Run at 3 AM daily
```

### 9.1 Skill File

**Path**: `skills/calendar-proxy/SKILL.md`

```markdown
---
name: calendar-proxy
description: Manage Google Calendar through a secure approval-gated proxy
version: 1.0.0
author: Your Name
repository: https://github.com/yourname/calendar-proxy
metadata:
  moltbot:
    requires:
      bins:
        - curl
        - jq
      env:
        - CALENDAR_PROXY_URL
        - CALENDAR_PROXY_KEY
---

# Calendar Proxy Skill

This skill allows you to interact with Google Calendar through a secure proxy
that may require human approval for write operations.

## Configuration

Set these environment variables before using:

```bash
export CALENDAR_PROXY_URL="https://calendar.example.com"
export CALENDAR_PROXY_KEY="sk_write_..."
```

## Understanding Approval Flow

**Read operations** (list, get, freebusy) execute immediately.

**Write operations** (create, update, delete) require human approval:
1. Request is submitted and returns a `request_id`
2. User receives push notification to approve/deny
3. You must poll the status endpoint until resolved
4. Once approved, retrieve the result

**Always inform the user** when waiting for approval so they can check their phone.

## Available Operations

### List Calendars

```bash
curl -s -H "Authorization: Bearer $CALENDAR_PROXY_KEY" \
  "$CALENDAR_PROXY_URL/api/calendar/list" | jq '.calendars[] | {id, summary}'
```

### List Upcoming Events

```bash
# Events from now until end of week
curl -s -H "Authorization: Bearer $CALENDAR_PROXY_KEY" \
  "$CALENDAR_PROXY_URL/api/calendar/primary/events?\
timeMin=$(date -u +%Y-%m-%dT%H:%M:%SZ)&\
maxResults=20&\
orderBy=startTime" | jq '.events[] | {summary, start, end}'
```

### List Events in Date Range

```bash
# Events for specific date
curl -s -H "Authorization: Bearer $CALENDAR_PROXY_KEY" \
  "$CALENDAR_PROXY_URL/api/calendar/primary/events?\
timeMin=2026-01-30T00:00:00Z&\
timeMax=2026-01-31T00:00:00Z" | jq
```

### Check Availability (Free/Busy)

```bash
curl -s -X POST \
  -H "Authorization: Bearer $CALENDAR_PROXY_KEY" \
  -H "Content-Type: application/json" \
  "$CALENDAR_PROXY_URL/api/calendar/freebusy" \
  -d '{
    "timeMin": "2026-01-30T09:00:00Z",
    "timeMax": "2026-01-30T18:00:00Z",
    "items": [{"id": "primary"}]
  }' | jq '.calendars.primary.busy'
```

### Create Event (Requires Approval)

```bash
# Step 1: Submit request
RESPONSE=$(curl -s -X POST \
  -H "Authorization: Bearer $CALENDAR_PROXY_KEY" \
  -H "Content-Type: application/json" \
  "$CALENDAR_PROXY_URL/api/calendar/events/create" \
  -d '{
    "calendarId": "primary",
    "summary": "Meeting with Team",
    "description": "Discuss Q1 roadmap",
    "start": "2026-01-30T10:00:00Z",
    "end": "2026-01-30T11:00:00Z",
    "location": "Conference Room A",
    "attendees": ["alice@example.com", "bob@example.com"]
  }')

REQUEST_ID=$(echo "$RESPONSE" | jq -r '.request_id')
echo "Submitted request: $REQUEST_ID"
echo "Waiting for approval (check your phone)..."

# Step 2: Poll for status (or wait for webhook notification)
# Note: If Moltbot webhook is configured, you'll receive a message when status changes.
# This polling is a fallback or for non-webhook setups.

poll_status() {
  while true; do
    STATUS_RESPONSE=$(curl -s \
      -H "Authorization: Bearer $CALENDAR_PROXY_KEY" \
      "$CALENDAR_PROXY_URL/api/requests/$REQUEST_ID")
    
    STATUS=$(echo "$STATUS_RESPONSE" | jq -r '.status')
    
    case "$STATUS" in
      "pending_approval")
        echo "Still waiting for approval..."
        sleep 10
        ;;
      "change_requested")
        # User wants modifications - extract suggestion and handle it
        SUGGESTION=$(echo "$STATUS_RESPONSE" | jq -r '.suggestion.text')
        ORIGINAL=$(echo "$STATUS_RESPONSE" | jq -r '.payload')
        echo "User requested changes: $SUGGESTION"
        echo "Original request: $ORIGINAL"
        # Return special code so caller knows to modify and resubmit
        return 2
        ;;
      "approved"|"executing")
        echo "Approved! Executing..."
        sleep 2
        ;;
      "completed")
        echo "Event created successfully!"
        curl -s -H "Authorization: Bearer $CALENDAR_PROXY_KEY" \
          "$CALENDAR_PROXY_URL/api/requests/$REQUEST_ID" | jq '.result'
        return 0
        ;;
      "denied")
        echo "Request was denied by user."
        return 1
        ;;
      "expired")
        echo "Request expired (no response within timeout)."
        return 1
        ;;
      "failed")
        ERROR=$(echo "$STATUS_RESPONSE" | jq -r '.error // "Unknown error"')
        echo "Request failed: $ERROR"
        return 1
        ;;
      *)
        echo "Unknown status: $STATUS"
        return 1
        ;;
    esac
  done
}

poll_status
RESULT=$?

if [ $RESULT -eq 2 ]; then
  # Handle change_requested - modify the request based on suggestion and resubmit
  echo "Modify the request based on the suggestion and submit again."
fi
```

### Handling change_requested

When the user suggests changes instead of approving/denying, you'll receive:

```json
{
  "status": "change_requested",
  "suggestion": {
    "text": "Move to 3pm instead, and add Bob to attendees",
    "suggested_at": "2026-01-28T12:05:00Z",
    "suggested_by": "telegram"
  },
  "payload": { ... }
}
```

**How to handle this:**
1. Parse the suggestion text to understand what changes are needed
2. Modify the original payload accordingly
3. Submit a new request (which will go through approval again)
4. Optionally cancel the original request: `POST /api/requests/{original_id}/cancel`
5. Inform the user: "I've updated the request based on your feedback. Please approve the new version."

### Update Event (Requires Approval)

```bash
# Only include fields you want to change
RESPONSE=$(curl -s -X POST \
  -H "Authorization: Bearer $CALENDAR_PROXY_KEY" \
  -H "Content-Type: application/json" \
  "$CALENDAR_PROXY_URL/api/calendar/events/update" \
  -d '{
    "calendarId": "primary",
    "eventId": "eventId",
    "summary": "Updated: Meeting with Team",
    "location": "Virtual - Zoom"
  }')

# Then poll as above...
```

### Delete Event (Requires Approval)

```bash
RESPONSE=$(curl -s -X POST \
  -H "Authorization: Bearer $CALENDAR_PROXY_KEY" \
  -H "Content-Type: application/json" \
  "$CALENDAR_PROXY_URL/api/calendar/events/delete" \
  -d '{
    "calendarId": "primary",
    "eventId": "eventId"
  }')

# Then poll as above...
```

## Response Codes

| Code | Meaning |
|------|---------|
| 200 | Success (for read operations) |
| 202 | Accepted - awaiting approval |
| 400 | Invalid request parameters |
| 401 | Invalid or missing API key |
| 403 | Permission denied or approval denied |
| 408 | Approval timeout expired |
| 429 | Rate limited - wait and retry |
| 502 | Google Calendar API error |

## Supported Fields (Capabilities)

The proxy accepts a **constrained schema** for event operations. Unknown fields are silently ignored.

**Supported Fields**:
| Field | Type | Create | Update | Notes |
|-------|------|--------|--------|-------|
| `calendarId` | string | Required | Required | "primary" or calendar ID |
| `summary` | string | Required | Optional | Event title |
| `start` | datetime | Required | Optional | RFC3339 with timezone |
| `end` | datetime | Required | Optional | RFC3339 with timezone |
| `description` | string | Optional | Optional | Event description |
| `location` | string | Optional | Optional | Location text |
| `attendees` | string[] | Optional | Optional | Email addresses |
| `colorId` | string | Optional | Optional | Event color (1-11) |
| `visibility` | string | Optional | Optional | "default", "public", "private" |
| `reminders` | object | Optional | Optional | Custom reminders |

**NOT Supported** (silently dropped):
- `conferenceData` — Video conferencing
- `recurrence` — Recurring events
- `attachments` — File attachments
- `guestsCanModify`, `guestsCanInviteOthers`, `guestsCanSeeOtherGuests` — Guest permissions
- `extendedProperties` — Custom metadata
- `source` — External source info

Do not attempt to set unsupported fields—they will be ignored.

## Tips for AI Agents

1. **Always check freebusy before suggesting times** — Don't create conflicts
2. **Report approval status to user** — When you receive `pending_approval` status, 
   report "Request submitted. A notification has been sent for approval." Include
   the request ID so the controller can track it.
3. **Handle change_requested as a correction** — This means the user wants modifications,
   not a fresh start. Parse the suggestion in context of the original payload:
   "I tried to create event X. User said: 'Move to 3pm'. Generate updated JSON."
4. **If denied, ask for modifications** — "The request was denied. Would you 
   like me to try a different time?"
5. **Use descriptive event titles** — Helps approver understand what's being created
6. **Include relevant attendees** — Don't forget people who should be invited
7. **Set reasonable durations** — Don't create 8-hour meetings by accident
8. **Use Idempotency-Key header** — Prevents duplicate events if your request times out
9. **Webhook notifications are faster** — If configured, you'll receive webhook 
   notifications instead of needing to poll. Check your session for updates.
```

### 9.2 Helper Scripts

Optionally include reusable functions:

**`skills/calendar-proxy/lib/helpers.sh`**:
```bash
#!/bin/bash

# Calendar Proxy Helper Functions
# Source this file: source /path/to/helpers.sh

calendar_api() {
  local method="$1"
  local endpoint="$2"
  local data="$3"
  
  if [ -n "$data" ]; then
    curl -s -X "$method" \
      -H "Authorization: Bearer $CALENDAR_PROXY_KEY" \
      -H "Content-Type: application/json" \
      "$CALENDAR_PROXY_URL$endpoint" \
      -d "$data"
  else
    curl -s -X "$method" \
      -H "Authorization: Bearer $CALENDAR_PROXY_KEY" \
      "$CALENDAR_PROXY_URL$endpoint"
  fi
}

wait_for_approval() {
  local request_id="$1"
  local timeout="${2:-300}"  # Default 5 minutes
  local start=$(date +%s)
  
  while true; do
    local now=$(date +%s)
    local elapsed=$((now - start))
    
    if [ $elapsed -gt $timeout ]; then
      echo "Timed out waiting for status update"
      return 1
    fi
    
    local response=$(calendar_api GET "/api/requests/$request_id")
    local status=$(echo "$response" | jq -r '.status')
    
    case "$status" in
      "completed")
        echo "$response" | jq '.result'
        return 0
        ;;
      "denied"|"expired"|"failed")
        echo "$response"
        return 1
        ;;
      *)
        sleep 5
        ;;
    esac
  done
}
```

---

## 10. Web UI

### 10.1 Page Structure

| Route | Page | Description |
|-------|------|-------------|
| `/login` | Login | Password authentication |
| `/` | Dashboard | Overview and quick stats |
| `/pending` | Pending | List of awaiting approvals |
| `/pending/{id}` | Request Detail | Approve/deny with full details |
| `/history` | History | Past requests and audit log |
| `/api-keys` | API Keys | Create, view, revoke keys |
| `/settings` | Settings | General configuration |
| `/settings/google` | Google | OAuth connection |
| `/settings/notifications` | Notifications | Provider configuration |

### 10.2 Dashboard Layout

```
┌─────────────────────────────────────────────────────────────────────────┐
│  Calendar Proxy                                    [Settings] [Logout]  │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  ┌─────────────────────────┐  ┌─────────────────────────┐              │
│  │  Pending Approvals   │  │  Today's Activity     │              │
│  │                         │  │                         │              │
│  │        3                │  │   12 requests           │              │
│  │                         │  │   11 approved           │              │
│  │  [View All →]           │  │    1 denied             │              │
│  └─────────────────────────┘  └─────────────────────────┘              │
│                                                                         │
│  ┌─────────────────────────┐  ┌─────────────────────────┐              │
│  │  Active API Keys     │  │  Notifications       │              │
│  │                         │  │                         │              │
│  │   2 keys                │  │  ntfy (enabled)                 │              │
│  │   1 read, 1 write       │  │  Pushover (enabled)             │              │
│  │                         │  │  Telegram (disabled)             │              │
│  │  [Manage →]             │  │                         │              │
│  └─────────────────────────┘  └─────────────────────────┘              │
│                                                                         │
│  Recent Activity                                                        │
│  ─────────────────────────────────────────────────────────────────────  │
│  │ 12:45 │ OK │ Create Event │ Team Standup    │ sk_write_a1b2... │    │
│  │ 12:30 │ OK │ Create Event │ 1:1 with Alice  │ sk_write_a1b2... │    │
│  │ 11:15 │ FAIL │ Delete Event │ Old Meeting     │ sk_write_a1b2... │    │
│  │ 10:00 │ OK │ List Events  │ -               │ sk_read_x9y8...  │    │
│  └───────────────────────────────────────────────────────────────────┘  │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

### 10.3 Pending Request Detail

```
┌─────────────────────────────────────────────────────────────────────────┐
│  ← Back to Pending                                                      │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  Request: req_a1b2c3d4e5f6                                             │
│  Status: Pending Approval                                            │
│  Expires in: 47 minutes                                                 │
│                                                                         │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │  Operation: CREATE EVENT                                        │   │
│  │  Requested by: sk_write_a1b2... (Moltbot Main)                 │   │
│  │  Submitted: January 28, 2026 at 12:00 PM                       │   │
│  └─────────────────────────────────────────────────────────────────┘   │
│                                                                         │
│  Event Details                                                          │
│  ─────────────────────────────────────────────────────────────────────  │
│                                                                         │
│  Title:       Project Review Meeting                               │
│  Start:       January 30, 2026 at 10:00 AM                         │
│  End:         January 30, 2026 at 11:00 AM                         │
│  Location:    Conference Room A                                    │
│  Attendees:   alice@example.com, bob@example.com                   │
│  Description: Quarterly project status review and planning         │
│                                                                         │
│  Raw Request                                                            │
│  ─────────────────────────────────────────────────────────────────────  │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │ {                                                               │   │
│  │   "calendarId": "primary",                                     │   │
│  │   "summary": "Project Review Meeting",                         │   │
│  │   "start": "2026-01-30T10:00:00Z",                            │   │
│  │   "end": "2026-01-30T11:00:00Z",                              │   │
│  │   "location": "Conference Room A",                             │   │
│  │   "attendees": ["alice@example.com", "bob@example.com"],      │   │
│  │   "description": "Quarterly project status review..."         │   │
│  │ }                                                               │   │
│  └─────────────────────────────────────────────────────────────────┘   │
│                                                                         │
│  Suggest Changes (optional)                                             │
│  ─────────────────────────────────────────────────────────────────────  │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │ e.g., "Move to 3pm" or "Add Bob to attendees"                  │   │
│  │ [                                                              ]│   │
│  └─────────────────────────────────────────────────────────────────┘   │
│                                                                         │
│   ┌──────────────┐    ┌──────────────┐    ┌────────────────────┐      │
│   │  Approve  │    │   Deny    │    │  Suggest Change │      │
│   └──────────────┘    └──────────────┘    └────────────────────┘      │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

### 10.4 Notification Settings

```
┌─────────────────────────────────────────────────────────────────────────┐
│  Settings > Notifications                                               │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  Enable notifications on one or more providers. All enabled providers   │
│  will receive approval requests simultaneously.                         │
│                                                                         │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │  ntfy                                           [x] Enabled     │   │
│  │  ───────────────────────────────────────────────────────────── │   │
│  │  Server:   https://ntfy.sh        [________________]           │   │
│  │  Topic:    ●●●●●●●●●●●●●●●●       [________________]           │   │
│  │  Token:    (optional)              [________________]           │   │
│  │  Priority: [High ▼]                                             │   │
│  │                                                                 │   │
│  │  [Test Notification]                              [Save]        │   │
│  └─────────────────────────────────────────────────────────────────┘   │
│                                                                         │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │  Pushover                                       [x] Enabled     │   │
│  │  ───────────────────────────────────────────────────────────── │   │
│  │  App Token: ●●●●●●●●●●●●●●●●     [________________]            │   │
│  │  User Key:  ●●●●●●●●●●●●●●●●     [________________]            │   │
│  │  Priority:  [High (1) ▼]                                        │   │
│  │  Sound:     [pushover ▼]                                        │   │
│  │                                                                 │   │
│  │  [Test Notification]                              [Save]        │   │
│  └─────────────────────────────────────────────────────────────────┘   │
│                                                                         │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │  Telegram                                       [ ] Disabled    │   │
│  │  ───────────────────────────────────────────────────────────── │   │
│  │  Bot Token: [________________]                                  │   │
│  │  Chat ID:   [________________]                                  │   │
│  │                                                                 │   │
│  │  Webhook URL (configure in Telegram):                           │   │
│  │  https://calendar.example.com/webhooks/telegram                │   │
│  │                                                                 │   │
│  │  [Test Notification]                              [Save]        │   │
│  └─────────────────────────────────────────────────────────────────┘   │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

### 10.5 Mobile Responsiveness

Required breakpoints:
- **Desktop**: > 1024px - Full layout
- **Tablet**: 768px - 1024px - Collapsed sidebar
- **Mobile**: < 768px - Stacked layout, hamburger menu

Minimum touch targets: 44x44px for all interactive elements.

---

## 11. Deployment

### 11.1 Docker Compose (Recommended)

```yaml
version: "3.8"

services:
  calendar-proxy:
    image: yourname/calendar-proxy:latest
    container_name: calendar-proxy
    restart: unless-stopped
    environment:
      # === Required ===
      - SCHEDLOCK_SERVER_SECRET=${SCHEDLOCK_SERVER_SECRET}
      - SCHEDLOCK_ENCRYPTION_KEY=${SCHEDLOCK_ENCRYPTION_KEY}
      - SCHEDLOCK_GOOGLE_CLIENT_ID=${SCHEDLOCK_GOOGLE_CLIENT_ID}
      - SCHEDLOCK_GOOGLE_CLIENT_SECRET=${SCHEDLOCK_GOOGLE_CLIENT_SECRET}
      - SCHEDLOCK_AUTH_PASSWORD_HASH=${SCHEDLOCK_AUTH_PASSWORD_HASH}
      - SCHEDLOCK_ADMIN_PASSWORD=${SCHEDLOCK_ADMIN_PASSWORD:-}
      
      # === Notifications (enable at least one) ===
      # ntfy
      - SCHEDLOCK_NTFY_ENABLED=${SCHEDLOCK_NTFY_ENABLED:-true}
      - SCHEDLOCK_NTFY_SERVER_URL=${SCHEDLOCK_NTFY_SERVER_URL:-https://ntfy.sh}
      - SCHEDLOCK_NTFY_TOPIC=${SCHEDLOCK_NTFY_TOPIC}
      - SCHEDLOCK_NTFY_TOKEN=${SCHEDLOCK_NTFY_TOKEN:-}
      
      # Pushover
      - SCHEDLOCK_PUSHOVER_ENABLED=${SCHEDLOCK_PUSHOVER_ENABLED:-false}
      - SCHEDLOCK_PUSHOVER_APP_TOKEN=${SCHEDLOCK_PUSHOVER_APP_TOKEN:-}
      - SCHEDLOCK_PUSHOVER_USER_KEY=${SCHEDLOCK_PUSHOVER_USER_KEY:-}
      
      # Telegram
      - SCHEDLOCK_TELEGRAM_ENABLED=${SCHEDLOCK_TELEGRAM_ENABLED:-false}
      - SCHEDLOCK_TELEGRAM_BOT_TOKEN=${SCHEDLOCK_TELEGRAM_BOT_TOKEN:-}
      - SCHEDLOCK_TELEGRAM_CHAT_ID=${SCHEDLOCK_TELEGRAM_CHAT_ID:-}
      - SCHEDLOCK_TELEGRAM_WEBHOOK_SECRET=${SCHEDLOCK_TELEGRAM_WEBHOOK_SECRET:-}
      
      # Moltbot Webhook (push status updates to bot)
      - SCHEDLOCK_MOLTBOT_WEBHOOK_ENABLED=${SCHEDLOCK_MOLTBOT_WEBHOOK_ENABLED:-false}
      - SCHEDLOCK_MOLTBOT_WEBHOOK_URL=${SCHEDLOCK_MOLTBOT_WEBHOOK_URL:-}
      - SCHEDLOCK_MOLTBOT_WEBHOOK_SECRET=${SCHEDLOCK_MOLTBOT_WEBHOOK_SECRET:-}
      
      # === Optional ===
      - SCHEDLOCK_BASE_URL=${SCHEDLOCK_BASE_URL:-http://localhost:8080}
      - SCHEDLOCK_LOG_LEVEL=${SCHEDLOCK_LOG_LEVEL:-info}
      - SCHEDLOCK_DISPLAY_TIMEZONE=${SCHEDLOCK_DISPLAY_TIMEZONE:-America/New_York}
      
    volumes:
      - calendar-proxy-data:/data
    ports:
      - "127.0.0.1:8080:8080"
    healthcheck:
      test: ["CMD", "wget", "-q", "--spider", "http://localhost:8080/health"]
      interval: 30s
      timeout: 10s
      retries: 3
      start_period: 10s

  # === Optional: Cloudflare Tunnel ===
  cloudflared:
    image: cloudflare/cloudflared:latest
    container_name: cloudflared
    restart: unless-stopped
    command: tunnel run
    environment:
      - TUNNEL_TOKEN=${CF_TUNNEL_TOKEN}
    depends_on:
      - calendar-proxy
    profiles:
      - tunnel

  # === Optional: Litestream Backup ===
  litestream:
    image: litestream/litestream:latest
    container_name: litestream
    restart: unless-stopped
    volumes:
      - calendar-proxy-data:/data:ro
      - ./litestream.yml:/etc/litestream.yml:ro
    command: replicate
    environment:
      - LITESTREAM_ACCESS_KEY_ID=${AWS_ACCESS_KEY_ID:-}
      - LITESTREAM_SECRET_ACCESS_KEY=${AWS_SECRET_ACCESS_KEY:-}
    depends_on:
      - calendar-proxy
    profiles:
      - backup

volumes:
  calendar-proxy-data:

networks:
  default:
    name: calendar-proxy
```

### 11.2 Environment File

**`.env`** (copy from `.env.example`):

```bash
# === Required Secrets ===
# Generate with: openssl rand -base64 32
SCHEDLOCK_SERVER_SECRET=
SCHEDLOCK_ENCRYPTION_KEY=

# Google OAuth credentials (from Google Cloud Console)
SCHEDLOCK_GOOGLE_CLIENT_ID=
SCHEDLOCK_GOOGLE_CLIENT_SECRET=

# Web UI admin password (Argon2id hash)
SCHEDLOCK_AUTH_PASSWORD_HASH=
# Optional dev-only plaintext (not recommended)
SCHEDLOCK_ADMIN_PASSWORD=

# === Notification Providers ===
# Enable at least one!

# ntfy (recommended for self-hosted)
SCHEDLOCK_NTFY_ENABLED=true
SCHEDLOCK_NTFY_SERVER_URL=https://ntfy.sh
SCHEDLOCK_NTFY_TOPIC=your-secret-topic-change-this
SCHEDLOCK_NTFY_TOKEN=

# Pushover
SCHEDLOCK_PUSHOVER_ENABLED=false
SCHEDLOCK_PUSHOVER_APP_TOKEN=
SCHEDLOCK_PUSHOVER_USER_KEY=

# Telegram
SCHEDLOCK_TELEGRAM_ENABLED=false
SCHEDLOCK_TELEGRAM_BOT_TOKEN=
SCHEDLOCK_TELEGRAM_CHAT_ID=
SCHEDLOCK_TELEGRAM_WEBHOOK_SECRET=

# === Deployment ===
# Public URL (for callback URLs in notifications)
SCHEDLOCK_BASE_URL=https://calendar.example.com

# Logging: debug, info, warn, error
SCHEDLOCK_LOG_LEVEL=info

# Timezone for Web UI and notifications (IANA format)
# API always uses UTC, this is for human-readable display
SCHEDLOCK_DISPLAY_TIMEZONE=America/New_York

# === Moltbot Webhook (Optional but Recommended) ===
# Push status updates to Moltbot instead of requiring polling
SCHEDLOCK_MOLTBOT_WEBHOOK_ENABLED=true
SCHEDLOCK_MOLTBOT_WEBHOOK_URL=http://localhost:18789/hooks/agent
SCHEDLOCK_MOLTBOT_WEBHOOK_SECRET=your-moltbot-hooks-token

# === Optional: Cloudflare Tunnel ===
CF_TUNNEL_TOKEN=

# === Optional: Backup ===
AWS_ACCESS_KEY_ID=
AWS_SECRET_ACCESS_KEY=
LITESTREAM_REPLICA_URL=s3://your-bucket/calendar-proxy
```

### 11.3 Dockerfile

```dockerfile
# syntax=docker/dockerfile:1

# Build stage
FROM golang:1.22-alpine AS builder

RUN apk add --no-cache gcc musl-dev sqlite-dev

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 GOOS=linux go build \
    -ldflags="-s -w" \
    -o /calendar-proxy \
    ./cmd/server

# Runtime stage
# NOTE: Using Alpine (not scratch/distroless) for:
# - wget (required for Docker healthcheck)
# - tzdata (timezone database for user display)
# - sqlite tools (for debugging)
FROM alpine:3.19

RUN apk add --no-cache \
    ca-certificates \
    sqlite \
    tzdata \
    wget

# Non-root user
RUN adduser -D -h /app appuser
USER appuser
WORKDIR /app

COPY --from=builder /calendar-proxy .
COPY --from=builder /src/web ./web

ENV DATA_DIR=/data
ENV PORT=8080

VOLUME /data
EXPOSE 8080

# NOTE: healthcheck uses wget which requires Alpine (not scratch)
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD wget -q --spider http://localhost:8080/health || exit 1

ENTRYPOINT ["./calendar-proxy"]
```

**Important Build Notes**:
- **Alpine required**: The healthcheck uses `wget`. If you switch to `scratch` or `distroless`, the healthcheck will fail silently and the container will be marked unhealthy.
- **tzdata package**: Required for timezone-aware display formatting. The Go binary also embeds `time/tzdata` as a fallback.
- **CGO_ENABLED=1**: Required for SQLite (unless using pure-Go SQLite driver like `modernc.org/sqlite`).

### 11.4 Litestream Configuration

**`litestream.yml`**:

```yaml
dbs:
  - path: /data/calendar-proxy.db
    replicas:
      - type: s3
        bucket: your-bucket
        path: calendar-proxy/db
        region: us-east-1
        sync-interval: 10s
        snapshot-interval: 1h
```

### 11.5 First Run Setup

1. **Create `.env` file** from template
2. **Generate secrets**:
   ```bash
   echo "SCHEDLOCK_SERVER_SECRET=$(openssl rand -base64 32)" >> .env
   echo "SCHEDLOCK_ENCRYPTION_KEY=$(openssl rand -base64 32)" >> .env
   ```
3. **Set up Google OAuth**:
   - Go to Google Cloud Console
   - Create OAuth 2.0 credentials
   - Set redirect URI: `{SCHEDLOCK_BASE_URL}/oauth/callback`
   - Add to `.env`
4. **Configure at least one notification provider**
5. **Start the service**:
   ```bash
   docker compose up -d
   ```
6. **Access web UI**: `http://localhost:8080`
7. **Log in** with the password that matches `SCHEDLOCK_AUTH_PASSWORD_HASH` (or `SCHEDLOCK_ADMIN_PASSWORD` in dev)
8. **Connect Google Calendar** via Settings > Google
9. **Test notifications** via Settings > Notifications
10. **Create API key** via API Keys page
11. **Configure Moltbot** with key and URL

---

## 12. Configuration

### 12.1 Configuration Hierarchy

Current implementation uses **environment variables and built-in defaults only**.
YAML config files and database-stored settings are planned but not implemented yet.

1. **Environment variables** — Highest priority, required for secrets
2. **Defaults** — Built-in fallbacks

### 12.2 Full Configuration Reference

```yaml
# Planned format for /data/config.yaml (not implemented in current build)

server:
  host: "0.0.0.0"
  port: 8080
  SCHEDLOCK_BASE_URL: "${SCHEDLOCK_BASE_URL}"             # Used for callback URLs
  read_timeout: 30s
  write_timeout: 30s

# User display settings (all API exchanges remain UTC)
display:
  timezone: "America/New_York"        # IANA timezone for Web UI and notifications
  date_format: "Jan 2, 2006"          # Go date format
  time_format: "3:04 PM"              # Go time format
  datetime_format: "Jan 2, 2006 at 3:04 PM"

database:
  path: "/data/calendar-proxy.db"
  wal_mode: true
  busy_timeout_ms: 5000

# Data retention and cleanup
retention:
  enabled: true
  completed_requests_days: 90         # Delete completed/denied/expired requests older than this
  audit_log_days: 365                 # Delete audit logs older than this
  vacuum_schedule: "0 3 * * *"        # Cron: daily at 3 AM
  webhook_failures_days: 30           # Delete resolved webhook failures older than this

google:
  client_id: "${SCHEDLOCK_GOOGLE_CLIENT_ID}"
  client_secret: "${SCHEDLOCK_GOOGLE_CLIENT_SECRET}"
  scopes:
    - "https://www.googleapis.com/auth/calendar.events"  # Events only (minimal scope)
  redirect_uri: "${SCHEDLOCK_BASE_URL}/oauth/callback"

approval:
  timeout_minutes: 60
  default_action: "deny"              # "approve" or "deny"
  operations:
    create_event:
      requires_approval: true
      timeout_minutes: 60
      default_action: "deny"
    update_event:
      requires_approval: true
      timeout_minutes: 60
      default_action: "deny"
    delete_event:
      requires_approval: true
      timeout_minutes: 30             # Shorter for destructive
      default_action: "deny"          # Always deny on timeout

rate_limits:
  read:
    requests_per_minute: 60
    burst: 10
  write:
    requests_per_minute: 30
    burst: 5
  admin:
    requests_per_minute: 120
    burst: 20

retry:
  enabled: true
  max_attempts: 3
  backoff_seconds: [5, 10, 20]
  retryable_status_codes: [429, 500, 502, 503]

notifications:
  ntfy:
    enabled: "${SCHEDLOCK_NTFY_ENABLED}"
    server: "${SCHEDLOCK_NTFY_SERVER_URL}"
    topic: "${SCHEDLOCK_NTFY_TOPIC}"
    token: "${SCHEDLOCK_NTFY_TOKEN}"
    priority: "high"
    
  pushover:
    enabled: "${SCHEDLOCK_PUSHOVER_ENABLED}"
    app_token: "${SCHEDLOCK_PUSHOVER_APP_TOKEN}"
    user_key: "${SCHEDLOCK_PUSHOVER_USER_KEY}"
    priority: 1
    sound: "pushover"
    
  telegram:
    enabled: "${SCHEDLOCK_TELEGRAM_ENABLED}"
    bot_token: "${SCHEDLOCK_TELEGRAM_BOT_TOKEN}"
    chat_id: "${SCHEDLOCK_TELEGRAM_CHAT_ID}"
    webhook_secret: "${SCHEDLOCK_TELEGRAM_WEBHOOK_SECRET}"
    webhook_path: "/webhooks/telegram"
    auto_register_webhook: true          # Auto-register on startup

# Moltbot webhook for pushing status updates
moltbot:
  webhook:
    enabled: "${SCHEDLOCK_MOLTBOT_WEBHOOK_ENABLED}"
    url: "${SCHEDLOCK_MOLTBOT_WEBHOOK_URL}"        # e.g., http://localhost:18789/hooks/agent
    token: "${SCHEDLOCK_MOLTBOT_WEBHOOK_SECRET}"   # hooks.token from Moltbot config
    session_key_prefix: "calendar-proxy"
    notify_on:                            # Which status changes trigger webhooks
      - approved
      - denied
      - expired
      - change_requested
      - completed
      - failed

auth:
  admin_password_hash: "${SCHEDLOCK_AUTH_PASSWORD_HASH}"
  admin_password: "${SCHEDLOCK_ADMIN_PASSWORD}"
  session_duration: 24h
  session_refresh: true
  
  cloudflare_access:
    enabled: false
    team: "${CF_ACCESS_TEAM}"
    aud: "${CF_ACCESS_AUD}"

logging:
  level: "${SCHEDLOCK_LOG_LEVEL}"               # debug, info, warn, error
  format: "json"                      # json or text
  include_caller: false
```

### 12.3 Runtime-Changeable Settings

Planned for a future release. The current build does not persist runtime settings via the Web UI.

| Setting | Location | Description |
|---------|----------|-------------|
| Approval timeout | Settings > Approval | Minutes to wait |
| Default action | Settings > Approval | Approve/deny on timeout |
| Per-operation approval | Settings > Approval | Which ops need approval |
| Notification enable/disable | Settings > Notifications | Toggle providers |
| Notification priority | Settings > Notifications | Alert level |
| Moltbot webhook URL | Settings > Moltbot | Webhook endpoint |

---

## 13. Security Considerations

### 13.1 Threat Model

| Threat | Impact | Mitigation |
|--------|--------|------------|
| API key theft | Attacker can read/write calendar | Hashed storage, rate limiting, revocation |
| Notification spoofing | Fake approval/denial | Single-use decision tokens |
| OAuth token theft | Full calendar access | Encrypted at rest (AES-256-GCM) |
| Session hijacking | Web UI access | HTTP-only secure cookies, CSRF protection |
| Brute force login | Web UI access | Rate limiting on login endpoint |
| MITM attacks | Data interception | TLS required (via tunnel/proxy) |
| SQL injection | Database compromise | Parameterized queries only |
| XSS attacks | Session theft | CSP headers, output encoding |

### 13.2 Security Checklist

**Secrets Management**:
- [ ] All secrets in environment variables, never in code
- [ ] `SCHEDLOCK_SERVER_SECRET` and `SCHEDLOCK_ENCRYPTION_KEY` are unique, randomly generated
- [ ] `.env` file excluded from version control
- [ ] Secrets rotated periodically

**Data Protection**:
- [ ] OAuth tokens encrypted with AES-256-GCM
- [ ] API keys stored as HMAC-SHA256 hashes
- [ ] Database file permissions restricted (600)
- [ ] Backup encryption enabled (if using Litestream)

**Network Security**:
- [ ] HTTPS required in production (via Cloudflare Tunnel or reverse proxy)
- [ ] No direct port exposure (bind to localhost only)
- [ ] Cloudflare Access enabled at proxy/tunnel (optional)
- [ ] Same-origin web UI (no CORS headers)

**Authentication**:
- [ ] Password hashed with Argon2id
- [ ] Session cookies: HTTP-only, Secure, SameSite=Strict
- [ ] CSRF tokens on all state-changing forms
- [ ] Rate limiting on login endpoint

**Callback Security**:
- [ ] Single-use decision tokens on callback URLs
- [ ] Token expiration enforced
- [ ] Request status checked before processing
- [ ] Telegram webhook secret validated

**Audit & Monitoring**:
- [ ] All operations logged to audit table
- [ ] Failed auth attempts logged with IP
- [ ] Log aggregation configured (optional)
- [ ] Alerts on suspicious activity (optional)

### 13.3 Production Recommendations

1. **Use Cloudflare Tunnel** — No exposed ports, automatic HTTPS
2. **Enable Cloudflare Access** — Identity verification for web UI
3. **Self-host ntfy** — Don't send calendar data through public servers
4. **Use service tokens** — For Moltbot API access via Cloudflare Access
5. **Enable backup** — Litestream to encrypted S3/R2
6. **Review audit logs** — Regularly check for anomalies
7. **Rotate API keys** — Periodically regenerate and update Moltbot config

---

## 14. Future Considerations

### 14.1 Potential Enhancements

| Feature | Priority | Complexity | Notes |
|---------|----------|------------|-------|
| Multi-calendar (Outlook) | Medium | High | Separate provider interface |
| Approval delegation | Low | Medium | Multiple approvers, quorum |
| Quiet hours | Medium | Low | Auto-deny during sleep |
| Smart auto-approve | Low | High | ML-based risk scoring |
| Event templates | Low | Low | Pre-approved event types |
| Prometheus metrics | Medium | Low | For monitoring dashboards |
| Webhooks to bot | Low | Medium | Push instead of poll |
| Multi-tenant | Low | High | User isolation, billing |

### 14.2 Explicitly Out of Scope (v1)

- Direct calendar UI (use Google Calendar)
- Complex scheduling/availability algorithms  
- Integration with other calendar providers
- Multi-user support
- Mobile native app

---

## Appendix A: Google OAuth Setup

1. Go to [Google Cloud Console](https://console.cloud.google.com/)
2. Create new project or select existing
3. Enable **Google Calendar API**
4. Configure **OAuth consent screen**:
   - User type: External (or Internal for Workspace)
   - App name: "Calendar Proxy"
   - Scopes: `calendar`, `calendar.events`
5. Create **OAuth 2.0 Client ID**:
   - Application type: Web application
   - Authorized redirect URI: `{SCHEDLOCK_BASE_URL}/oauth/callback`
6. Copy Client ID and Client Secret to `.env`

---

## Appendix B: Notification Message Templates

Customizable via settings. Defaults:

**ntfy**:
```
Title: Calendar: {operation_title}
Body:
Moltbot wants to {operation_verb}:

Title: {event_title}
When: {event_start} - {event_end}
Location: {event_location}
Attendees: {event_attendees}

Request: {request_id}
Expires: {expires_in}
```

**Pushover**:
```
Title: Calendar: {operation_title}
Message: Moltbot wants to {operation_verb}: {event_title} on {event_date}. Tap to review and approve.
```

**Telegram**:
```
*Calendar Request*

Moltbot wants to {operation_verb}:

*{event_title}*
When: {event_start} - {event_end}
Location: {event_location}

Request: `{request_id}`
Expires: {expires_in}
```

---

## Appendix C: Glossary

| Term | Definition |
|------|------------|
| **Bot** | AI agent (Moltbot) that consumes the proxy API |
| **Proxy** | This service—mediates between bot and Google Calendar |
| **Approver** | Human who approves/denies requests via notification |
| **Tier** | Permission level (read, write, admin) |
| **Operation** | Calendar action (list, create, update, delete) |
| **Callback** | URL that notification providers call to approve/deny |
| **HITL** | Human-in-the-loop—approval workflow pattern |

---

*End of Design Document*

