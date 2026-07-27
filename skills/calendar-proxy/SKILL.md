# Calendar Proxy Skill

You have access to a calendar management system via the SchedLock API. This allows you to view and modify calendar events with human approval.

## API Endpoint

All requests should be made to the SchedLock server at `${SCHEDLOCK_API_URL}`.

## Authentication

Include your API key in the Authorization header:
```
Authorization: Bearer ${SCHEDLOCK_API_KEY}
```

## Available Operations

### Read Operations (instant response)

#### List Calendars
```bash
curl -H "Authorization: Bearer $SCHEDLOCK_API_KEY" \
  "$SCHEDLOCK_API_URL/api/calendar/list"
```

#### List Events
```bash
curl -H "Authorization: Bearer $SCHEDLOCK_API_KEY" \
  "$SCHEDLOCK_API_URL/api/calendar/primary/events?timeMin=2026-01-01T00:00:00Z&timeMax=2026-01-31T23:59:59Z"
```

#### Get Free/Busy
```bash
curl -H "Authorization: Bearer $SCHEDLOCK_API_KEY" \
  "$SCHEDLOCK_API_URL/api/calendar/freebusy?timeMin=2026-01-15T00:00:00Z&timeMax=2026-01-15T23:59:59Z"
```

### Write Operations (require human approval)

All write operations return immediately with a `request_id` and status `pending_approval`. The operation will execute after human approval.

#### Create Event
```bash
curl -X POST -H "Authorization: Bearer $SCHEDLOCK_API_KEY" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: unique-request-id" \
  "$SCHEDLOCK_API_URL/api/calendar/events/create" \
  -d '{
    "calendarId": "primary",
    "summary": "Meeting Title",
    "start": "2026-01-15T10:00:00-05:00",
    "end": "2026-01-15T11:00:00-05:00",
    "location": "Conference Room",
    "description": "Meeting agenda...",
    "attendees": ["person@example.com"]
  }'
```

#### Update Event
```bash
curl -X POST -H "Authorization: Bearer $SCHEDLOCK_API_KEY" \
  -H "Content-Type: application/json" \
  "$SCHEDLOCK_API_URL/api/calendar/events/update" \
  -d '{
    "calendarId": "primary",
    "eventId": "existing-event-id",
    "summary": "Updated Title",
    "start": "2026-01-15T14:00:00-05:00",
    "end": "2026-01-15T15:00:00-05:00"
  }'
```

#### Delete Event
```bash
curl -X POST -H "Authorization: Bearer $SCHEDLOCK_API_KEY" \
  -H "Content-Type: application/json" \
  "$SCHEDLOCK_API_URL/api/calendar/events/delete" \
  -d '{
    "calendarId": "primary",
    "eventId": "event-to-delete"
  }'
```

### Request Management

#### Check Request Status
```bash
curl -H "Authorization: Bearer $SCHEDLOCK_API_KEY" \
  "$SCHEDLOCK_API_URL/api/requests/$REQUEST_ID"
```

Possible statuses:
- `pending_approval` - Waiting for human decision
- `approved` - Approved, executing
- `denied` - Rejected by human
- `change_requested` - Human suggested modifications
- `completed` - Successfully executed
- `failed` - Execution error
- `expired` - No response within timeout

#### Cancel Request
```bash
curl -X POST -H "Authorization: Bearer $SCHEDLOCK_API_KEY" \
  "$SCHEDLOCK_API_URL/api/requests/$REQUEST_ID/cancel"
```

## Guidelines

1. **Send an `Idempotency-Key` header on every write.** A retry then returns the
   original request instead of queuing a second approval for the same change.
2. **Expect to wait.** A write returns `202 Accepted` with
   `status: pending_approval`; the change happens only after a person approves.
   A `200 OK` means the key's policy allowed it to run immediately.
3. **Poll for the outcome**, or receive it on the configured status webhook:
   - First check about 5 seconds after submitting
   - Then every 30 seconds
   - Give up after the request's `expires_at`
4. **Handle `change_requested`.** The reviewer has asked for something different.
   Read the `suggestion` field, adjust, and submit a new request.
5. **Use RFC 3339 timestamps with an offset**, for example
   `2026-01-15T10:00:00-05:00`. A time without an offset is ambiguous.
6. **Use `"primary"`** for the user's main calendar.
7. **Send only documented fields.** Unknown fields are rejected, so a typo is
   reported rather than silently dropped.
8. **Do not retry a denial.** A denied request means the person said no;
   resubmitting the same change is not an appropriate response.

## Errors

Errors carry a machine-readable code:

```json
{"error": {"code": "CONSTRAINT_VIOLATION", "message": "Calendar is not in the allowed list", "details": {"constraint": "calendar_allowlist"}}}
```

| Code | Meaning |
|---|---|
| `INVALID_API_KEY` | The key is missing, unknown, revoked, or expired |
| `INSUFFICIENT_PERMISSIONS` | The key's tier cannot perform this operation |
| `CONSTRAINT_VIOLATION` | A policy on this key refused the operation |
| `VALIDATION_ERROR` | The request body was malformed or out of range |
| `RATE_LIMITED` | Slow down; honour the `Retry-After` header |
| `CONFLICT` | The request was already decided or is no longer pending |
| `GOOGLE_API_ERROR` | Google rejected or could not serve the operation |

## Response Format

### Write Operation Response
```json
{
  "request_id": "req_kX9mP4qzA1b2C3d4",
  "status": "pending_approval",
  "expires_at": "2026-01-14T10:15:00Z",
  "message": "Request submitted for approval"
}
```

### Completed Request Response
```json
{
  "id": "req_kX9mP4qzA1b2C3d4",
  "status": "completed",
  "result": {
    "id": "google-event-id",
    "htmlLink": "https://calendar.google.com/event?eid=..."
  }
}
```

### Change Requested Response
```json
{
  "id": "req_kX9mP4qzA1b2C3d4",
  "status": "change_requested",
  "suggestion": {
    "text": "Please change the meeting time to 3pm instead",
    "suggested_by": "telegram:@username",
    "suggested_at": "2026-01-14T10:05:00Z"
  }
}
```

## Example Workflow

1. User asks to schedule a meeting
2. Call `/api/calendar/freebusy` to check availability
3. Submit event creation with `/api/calendar/events/create`
4. Inform user: "I've requested to create the meeting. Waiting for approval."
5. Poll `/api/requests/{id}` for status
6. On `completed`: "Meeting created successfully!"
7. On `denied`: "The meeting request was declined."
8. On `change_requested`: Read suggestion and ask user about modifications
