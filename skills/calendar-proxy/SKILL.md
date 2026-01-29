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
  "$SCHEDLOCK_API_URL/api/calendar/primary/events?timeMin=2024-01-01T00:00:00Z&timeMax=2024-01-31T23:59:59Z"
```

#### Get Free/Busy
```bash
curl -H "Authorization: Bearer $SCHEDLOCK_API_KEY" \
  "$SCHEDLOCK_API_URL/api/calendar/freebusy?timeMin=2024-01-15T00:00:00Z&timeMax=2024-01-15T23:59:59Z"
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
    "start": "2024-01-15T10:00:00-05:00",
    "end": "2024-01-15T11:00:00-05:00",
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
    "start": "2024-01-15T14:00:00-05:00",
    "end": "2024-01-15T15:00:00-05:00"
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

## Important Guidelines

1. **Always use Idempotency-Key** for create operations to prevent duplicates
2. **Poll for status** after submitting write requests:
   - Initial poll: 5 seconds after submission
   - Subsequent polls: Every 30 seconds
   - Timeout: 15 minutes (configurable)
3. **Handle `change_requested` status** - The human may suggest modifications. Read the `suggestion` field and adjust your request.
4. **Use ISO 8601 format** for all dates/times with timezone
5. **Primary calendar** - Use `"primary"` as calendar_id for the user's main calendar

## Response Format

### Write Operation Response
```json
{
  "request_id": "req_abc123def456",
  "status": "pending_approval",
  "expires_at": "2024-01-14T10:15:00Z",
  "message": "Event creation request submitted for approval"
}
```

### Completed Request Response
```json
{
  "id": "req_abc123def456",
  "status": "completed",
  "result": {
    "id": "google-event-id",
    "html_link": "https://calendar.google.com/event?eid=..."
  }
}
```

### Change Requested Response
```json
{
  "id": "req_abc123def456",
  "status": "change_requested",
  "suggestion": {
    "text": "Please change the meeting time to 3pm instead",
    "suggested_by": "telegram:@username",
    "suggested_at": "2024-01-14T10:05:00Z"
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
