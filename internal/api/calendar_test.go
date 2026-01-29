package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dtorcivia/schedlock/internal/apikeys"
	"github.com/dtorcivia/schedlock/internal/google"
	"github.com/dtorcivia/schedlock/internal/server/middleware"
)

type fakeCalendarClient struct {
	lastOpts google.EventListOptions
	resp     *google.EventListResponse
	err      error
}

func (f *fakeCalendarClient) ListCalendars(ctx context.Context) ([]google.Calendar, error) {
	return nil, nil
}

func (f *fakeCalendarClient) ListEvents(ctx context.Context, opts google.EventListOptions) (*google.EventListResponse, error) {
	f.lastOpts = opts
	return f.resp, f.err
}

func (f *fakeCalendarClient) GetEvent(ctx context.Context, calendarID, eventID string) (*google.Event, error) {
	return nil, nil
}

func (f *fakeCalendarClient) FreeBusy(ctx context.Context, req *google.FreeBusyRequest) (*google.FreeBusyResponse, error) {
	return nil, nil
}

func (f *fakeCalendarClient) CreateEvent(ctx context.Context, intent *google.EventIntent) (*google.Event, error) {
	return nil, nil
}

func (f *fakeCalendarClient) UpdateEvent(ctx context.Context, intent *google.EventUpdateIntent) (*google.Event, error) {
	return nil, nil
}

func (f *fakeCalendarClient) DeleteEvent(ctx context.Context, intent *google.EventDeleteIntent) error {
	return nil
}

func TestListEventsQueryParamsAndPagination(t *testing.T) {
	fake := &fakeCalendarClient{
		resp: &google.EventListResponse{
			Events:        []google.Event{{ID: "evt1"}},
			NextPageToken: "next123",
		},
	}

	h := &Handler{calendarClient: fake}

	req := httptest.NewRequest("GET",
		"http://example.com/api/calendar/primary/events?timeMin=2026-01-28T00:00:00Z&timeMax=2026-01-29T00:00:00Z&maxResults=123&pageToken=tok123&q=search&singleEvents=false&orderBy=updated",
		nil,
	)
	req.SetPathValue("calendarId", "primary")
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyAPIKey, &apikeys.AuthenticatedKey{
		ID:   "key1",
		Tier: "read",
	}))

	rr := httptest.NewRecorder()
	h.ListEvents(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	wantMin, _ := time.Parse(time.RFC3339, "2026-01-28T00:00:00Z")
	wantMax, _ := time.Parse(time.RFC3339, "2026-01-29T00:00:00Z")

	if fake.lastOpts.CalendarID != "primary" {
		t.Fatalf("calendar ID mismatch: got %q", fake.lastOpts.CalendarID)
	}
	if !fake.lastOpts.TimeMin.Equal(wantMin) || !fake.lastOpts.TimeMax.Equal(wantMax) {
		t.Fatalf("time range mismatch: got %v - %v", fake.lastOpts.TimeMin, fake.lastOpts.TimeMax)
	}
	if fake.lastOpts.MaxResults != 123 {
		t.Fatalf("maxResults mismatch: got %d", fake.lastOpts.MaxResults)
	}
	if fake.lastOpts.PageToken != "tok123" {
		t.Fatalf("pageToken mismatch: got %q", fake.lastOpts.PageToken)
	}
	if fake.lastOpts.Query != "search" {
		t.Fatalf("query mismatch: got %q", fake.lastOpts.Query)
	}
	if fake.lastOpts.SingleEvents {
		t.Fatalf("singleEvents mismatch: expected false")
	}
	if fake.lastOpts.OrderBy != "updated" {
		t.Fatalf("orderBy mismatch: got %q", fake.lastOpts.OrderBy)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["next_page_token"] != "next123" {
		t.Fatalf("next_page_token mismatch: got %#v", resp["next_page_token"])
	}
	if _, ok := resp["events"]; !ok {
		t.Fatalf("missing events in response")
	}
}

func TestListEventsInvalidSingleEvents(t *testing.T) {
	fake := &fakeCalendarClient{
		resp: &google.EventListResponse{},
	}

	h := &Handler{calendarClient: fake}

	req := httptest.NewRequest("GET",
		"http://example.com/api/calendar/primary/events?singleEvents=notabool",
		nil,
	)
	req.SetPathValue("calendarId", "primary")
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyAPIKey, &apikeys.AuthenticatedKey{
		ID:   "key1",
		Tier: "read",
	}))

	rr := httptest.NewRecorder()
	h.ListEvents(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", rr.Code)
	}
}
