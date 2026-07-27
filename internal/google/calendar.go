// Package google provides the Google Calendar API client.
package google

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

// CalendarClient wraps the Google Calendar API.
type CalendarClient struct {
	oauth *OAuthManager

	// mu guards the memoized service. Building one performs credential setup
	// and HTTP transport construction, which should not be repeated per call.
	mu      sync.Mutex
	service *calendar.Service
}

// NewCalendarClient creates a Calendar API client.
func NewCalendarClient(oauth *OAuthManager) *CalendarClient {
	return &CalendarClient{oauth: oauth}
}

// InvalidateService drops the memoized service after credentials change.
func (c *CalendarClient) InvalidateService() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.service = nil
}

// service returns a Calendar service, constructing it on first use.
func (c *CalendarClient) getService(ctx context.Context) (*calendar.Service, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.service != nil {
		return c.service, nil
	}

	// The HTTP client holds a refreshing token source, so the memoized service
	// keeps working as access tokens expire.
	httpClient, err := c.oauth.Client(context.WithoutCancel(ctx))
	if err != nil {
		return nil, err
	}

	service, err := calendar.NewService(context.WithoutCancel(ctx), option.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("failed to create Calendar service: %w", err)
	}

	c.service = service
	return service, nil
}

// ListCalendars returns every calendar the connected account can access.
func (c *CalendarClient) ListCalendars(ctx context.Context) ([]Calendar, error) {
	service, err := c.getService(ctx)
	if err != nil {
		return nil, err
	}

	list, err := service.CalendarList.List().Context(ctx).Do()
	if err != nil {
		return nil, c.wrap("list calendars", err)
	}

	calendars := make([]Calendar, 0, len(list.Items))
	for _, item := range list.Items {
		calendars = append(calendars, Calendar{
			ID:          item.Id,
			Summary:     item.Summary,
			Description: item.Description,
			TimeZone:    item.TimeZone,
			Primary:     item.Primary,
			AccessRole:  item.AccessRole,
		})
	}
	return calendars, nil
}

// ListEvents returns events from a calendar.
func (c *CalendarClient) ListEvents(ctx context.Context, opts EventListOptions) (*EventListResponse, error) {
	service, err := c.getService(ctx)
	if err != nil {
		return nil, err
	}

	call := service.Events.List(calendarOrPrimary(opts.CalendarID)).Context(ctx)

	if !opts.TimeMin.IsZero() {
		call = call.TimeMin(opts.TimeMin.Format(time.RFC3339))
	}
	if !opts.TimeMax.IsZero() {
		call = call.TimeMax(opts.TimeMax.Format(time.RFC3339))
	}
	if opts.MaxResults > 0 {
		call = call.MaxResults(int64(opts.MaxResults))
	}
	if opts.PageToken != "" {
		call = call.PageToken(opts.PageToken)
	}
	if opts.Query != "" {
		call = call.Q(opts.Query)
	}
	call = call.SingleEvents(opts.SingleEvents)
	if opts.OrderBy != "" {
		call = call.OrderBy(opts.OrderBy)
	}

	events, err := call.Do()
	if err != nil {
		return nil, c.wrap("list events", err)
	}

	return &EventListResponse{
		Events:        convertEvents(events.Items),
		NextPageToken: events.NextPageToken,
	}, nil
}

// GetEvent returns a single event.
func (c *CalendarClient) GetEvent(ctx context.Context, calendarID, eventID string) (*Event, error) {
	service, err := c.getService(ctx)
	if err != nil {
		return nil, err
	}

	event, err := service.Events.Get(calendarOrPrimary(calendarID), eventID).Context(ctx).Do()
	if err != nil {
		if IsNotFound(err) {
			return nil, nil
		}
		return nil, c.wrap("get event", err)
	}

	converted := convertEvent(event)
	return &converted, nil
}

// CreateEvent creates an event from an approved intent.
func (c *CalendarClient) CreateEvent(ctx context.Context, intent *EventIntent) (*Event, error) {
	service, err := c.getService(ctx)
	if err != nil {
		return nil, err
	}

	gcalEvent := &calendar.Event{
		Summary:     intent.Summary,
		Description: intent.Description,
		Location:    intent.Location,
		Start:       &calendar.EventDateTime{DateTime: intent.Start.Format(time.RFC3339)},
		End:         &calendar.EventDateTime{DateTime: intent.End.Format(time.RFC3339)},
		ColorId:     intent.ColorID,
		Visibility:  intent.Visibility,
		Attendees:   attendeesFromEmails(intent.Attendees),
		Reminders:   remindersToAPI(intent.Reminders),
	}

	created, err := service.Events.Insert(calendarOrPrimary(intent.CalendarID), gcalEvent).Context(ctx).Do()
	if err != nil {
		return nil, c.wrap("create event", err)
	}

	converted := convertEvent(created)
	return &converted, nil
}

// UpdateEvent applies an approved partial update.
//
// Patch semantics matter here: sending a full event resource would clear every
// field the requester did not mention, silently destroying data on the user's
// calendar that no one approved removing.
func (c *CalendarClient) UpdateEvent(ctx context.Context, intent *EventUpdateIntent) (*Event, error) {
	service, err := c.getService(ctx)
	if err != nil {
		return nil, err
	}

	patch := &calendar.Event{}
	// ForceSendFields lists the fields Go's zero values would otherwise omit,
	// so clearing a description or location is transmitted rather than dropped.
	var forceSend []string

	if intent.Summary != nil {
		patch.Summary = *intent.Summary
		forceSend = append(forceSend, "Summary")
	}
	if intent.Description != nil {
		patch.Description = *intent.Description
		forceSend = append(forceSend, "Description")
	}
	if intent.Location != nil {
		patch.Location = *intent.Location
		forceSend = append(forceSend, "Location")
	}
	if intent.Start != nil {
		patch.Start = &calendar.EventDateTime{DateTime: intent.Start.Format(time.RFC3339)}
	}
	if intent.End != nil {
		patch.End = &calendar.EventDateTime{DateTime: intent.End.Format(time.RFC3339)}
	}
	if intent.ColorID != nil {
		patch.ColorId = *intent.ColorID
		forceSend = append(forceSend, "ColorId")
	}
	if intent.Visibility != nil {
		patch.Visibility = *intent.Visibility
		forceSend = append(forceSend, "Visibility")
	}
	if len(intent.Attendees) > 0 {
		patch.Attendees = attendeesFromEmails(intent.Attendees)
	}
	if intent.Reminders != nil {
		patch.Reminders = remindersToAPI(intent.Reminders)
	}
	patch.ForceSendFields = forceSend

	updated, err := service.Events.Patch(calendarOrPrimary(intent.CalendarID), intent.EventID, patch).Context(ctx).Do()
	if err != nil {
		return nil, c.wrap("update event", err)
	}

	converted := convertEvent(updated)
	return &converted, nil
}

// DeleteEvent removes an event.
func (c *CalendarClient) DeleteEvent(ctx context.Context, intent *EventDeleteIntent) error {
	service, err := c.getService(ctx)
	if err != nil {
		return err
	}

	if err := service.Events.Delete(calendarOrPrimary(intent.CalendarID), intent.EventID).Context(ctx).Do(); err != nil {
		// An already-deleted event is the state the caller asked for.
		if IsNotFound(err) || IsGone(err) {
			return nil
		}
		return c.wrap("delete event", err)
	}
	return nil
}

// FreeBusy reports busy intervals for the requested calendars.
func (c *CalendarClient) FreeBusy(ctx context.Context, req *FreeBusyRequest) (*FreeBusyResponse, error) {
	service, err := c.getService(ctx)
	if err != nil {
		return nil, err
	}

	fbReq := &calendar.FreeBusyRequest{
		TimeMin: req.TimeMin.Format(time.RFC3339),
		TimeMax: req.TimeMax.Format(time.RFC3339),
	}
	for _, item := range req.Items {
		fbReq.Items = append(fbReq.Items, &calendar.FreeBusyRequestItem{Id: item.ID})
	}

	resp, err := service.Freebusy.Query(fbReq).Context(ctx).Do()
	if err != nil {
		return nil, c.wrap("query free/busy", err)
	}

	result := &FreeBusyResponse{
		TimeMin:   req.TimeMin,
		TimeMax:   req.TimeMax,
		Calendars: make(map[string]FreeBusyCalendarInfo, len(resp.Calendars)),
	}
	for calID, calInfo := range resp.Calendars {
		info := FreeBusyCalendarInfo{}
		for _, busy := range calInfo.Busy {
			start, _ := time.Parse(time.RFC3339, busy.Start)
			end, _ := time.Parse(time.RFC3339, busy.End)
			info.Busy = append(info.Busy, TimePeriod{Start: start, End: end})
		}
		for _, calErr := range calInfo.Errors {
			info.Errors = append(info.Errors, Error{Domain: calErr.Domain, Reason: calErr.Reason})
		}
		result.Calendars[calID] = info
	}

	return result, nil
}

// wrap annotates an upstream failure with the operation, preserving the
// googleapi error so retry classification still works.
func (c *CalendarClient) wrap(operation string, err error) error {
	var apiErr *googleapi.Error
	if errors.As(err, &apiErr) {
		return fmt.Errorf("failed to %s (google returned %d: %s): %w",
			operation, apiErr.Code, apiErr.Message, err)
	}
	return fmt.Errorf("failed to %s: %w", operation, err)
}

// IsNotFound reports whether an error is a Google 404.
func IsNotFound(err error) bool {
	var apiErr *googleapi.Error
	return errors.As(err, &apiErr) && apiErr.Code == 404
}

// IsGone reports whether an error is a Google 410 (already deleted).
func IsGone(err error) bool {
	var apiErr *googleapi.Error
	return errors.As(err, &apiErr) && apiErr.Code == 410
}

func calendarOrPrimary(calendarID string) string {
	if calendarID == "" {
		return "primary"
	}
	return calendarID
}

func attendeesFromEmails(emails []string) []*calendar.EventAttendee {
	if len(emails) == 0 {
		return nil
	}
	attendees := make([]*calendar.EventAttendee, 0, len(emails))
	for _, email := range emails {
		attendees = append(attendees, &calendar.EventAttendee{Email: email})
	}
	return attendees
}

func remindersToAPI(reminders *Reminders) *calendar.EventReminders {
	if reminders == nil {
		return nil
	}
	out := &calendar.EventReminders{
		UseDefault:      reminders.UseDefault,
		ForceSendFields: []string{"UseDefault"},
	}
	for _, r := range reminders.Overrides {
		out.Overrides = append(out.Overrides, &calendar.EventReminder{
			Method:  r.Method,
			Minutes: int64(r.Minutes),
		})
	}
	return out
}

func convertEvents(items []*calendar.Event) []Event {
	events := make([]Event, 0, len(items))
	for _, item := range items {
		events = append(events, convertEvent(item))
	}
	return events
}

func convertEvent(e *calendar.Event) Event {
	event := Event{
		ID:          e.Id,
		Summary:     e.Summary,
		Description: e.Description,
		Location:    e.Location,
		HTMLLink:    e.HtmlLink,
		Status:      e.Status,
		ColorID:     e.ColorId,
		Visibility:  e.Visibility,
		Start:       convertEventTime(e.Start),
		End:         convertEventTime(e.End),
	}

	for _, a := range e.Attendees {
		event.Attendees = append(event.Attendees, Attendee{
			Email:          a.Email,
			DisplayName:    a.DisplayName,
			ResponseStatus: a.ResponseStatus,
			Optional:       a.Optional,
			Organizer:      a.Organizer,
			Self:           a.Self,
		})
	}

	if e.Creator != nil {
		event.Creator = &Person{Email: e.Creator.Email, DisplayName: e.Creator.DisplayName, Self: e.Creator.Self}
	}
	if e.Organizer != nil {
		event.Organizer = &Person{Email: e.Organizer.Email, DisplayName: e.Organizer.DisplayName, Self: e.Organizer.Self}
	}
	if e.Reminders != nil {
		event.Reminders = &Reminders{UseDefault: e.Reminders.UseDefault}
		for _, r := range e.Reminders.Overrides {
			event.Reminders.Overrides = append(event.Reminders.Overrides, Reminder{
				Method:  r.Method,
				Minutes: int(r.Minutes),
			})
		}
	}

	event.Created = parseTimeOrNil(e.Created)
	event.Updated = parseTimeOrNil(e.Updated)

	return event
}

func convertEventTime(t *calendar.EventDateTime) *EventTime {
	if t == nil {
		return nil
	}
	converted := &EventTime{Date: t.Date, TimeZone: t.TimeZone}
	if t.DateTime != "" {
		if parsed, err := time.Parse(time.RFC3339, t.DateTime); err == nil {
			converted.DateTime = &parsed
		}
	}
	return converted
}

func parseTimeOrNil(value string) *time.Time {
	if value == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil
	}
	return &t
}
