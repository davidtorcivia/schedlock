// Package google provides the Google Calendar API client.
package google

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"
)

// CalendarClient provides access to Google Calendar API.
type CalendarClient struct {
	oauth *OAuthManager
}

// NewCalendarClient creates a new Calendar API client.
func NewCalendarClient(oauth *OAuthManager) *CalendarClient {
	return &CalendarClient{oauth: oauth}
}

// getService returns a configured Calendar API service.
func (c *CalendarClient) getService(ctx context.Context) (*calendar.Service, error) {
	httpClient, err := c.oauth.GetClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get OAuth client: %w", err)
	}

	service, err := calendar.NewService(ctx, option.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("failed to create Calendar service: %w", err)
	}

	return service, nil
}

// ListCalendars returns all accessible calendars.
func (c *CalendarClient) ListCalendars(ctx context.Context) ([]Calendar, error) {
	service, err := c.getService(ctx)
	if err != nil {
		return nil, err
	}

	list, err := service.CalendarList.List().Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("failed to list calendars: %w", err)
	}

	var calendars []Calendar
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

	calendarID := opts.CalendarID
	if calendarID == "" {
		calendarID = "primary"
	}

	call := service.Events.List(calendarID).Context(ctx)

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
	if opts.SingleEvents {
		call = call.SingleEvents(true)
	}
	if opts.OrderBy != "" {
		call = call.OrderBy(opts.OrderBy)
	}

	events, err := call.Do()
	if err != nil {
		return nil, fmt.Errorf("failed to list events: %w", err)
	}

	return &EventListResponse{
		Events:        convertEvents(events.Items),
		NextPageToken: events.NextPageToken,
	}, nil
}

// GetEvent returns a single event by ID.
func (c *CalendarClient) GetEvent(ctx context.Context, calendarID, eventID string) (*Event, error) {
	service, err := c.getService(ctx)
	if err != nil {
		return nil, err
	}

	if calendarID == "" {
		calendarID = "primary"
	}

	event, err := service.Events.Get(calendarID, eventID).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("failed to get event: %w", err)
	}

	converted := convertEvent(event)
	return &converted, nil
}

// CreateEvent creates a new event.
func (c *CalendarClient) CreateEvent(ctx context.Context, intent *EventIntent) (*Event, error) {
	service, err := c.getService(ctx)
	if err != nil {
		return nil, err
	}

	calendarID := intent.CalendarID
	if calendarID == "" {
		calendarID = "primary"
	}

	// Build Google Calendar event
	gcalEvent := &calendar.Event{
		Summary:     intent.Summary,
		Description: intent.Description,
		Location:    intent.Location,
		Start: &calendar.EventDateTime{
			DateTime: intent.Start.Format(time.RFC3339),
		},
		End: &calendar.EventDateTime{
			DateTime: intent.End.Format(time.RFC3339),
		},
	}

	// Add attendees
	if len(intent.Attendees) > 0 {
		for _, email := range intent.Attendees {
			gcalEvent.Attendees = append(gcalEvent.Attendees, &calendar.EventAttendee{
				Email: email,
			})
		}
	}

	// Add optional fields
	if intent.ColorID != "" {
		gcalEvent.ColorId = intent.ColorID
	}
	if intent.Visibility != "" {
		gcalEvent.Visibility = intent.Visibility
	}
	if intent.Reminders != nil {
		gcalEvent.Reminders = &calendar.EventReminders{
			UseDefault: intent.Reminders.UseDefault,
		}
		for _, r := range intent.Reminders.Overrides {
			gcalEvent.Reminders.Overrides = append(gcalEvent.Reminders.Overrides,
				&calendar.EventReminder{
					Method:  r.Method,
					Minutes: int64(r.Minutes),
				})
		}
	}

	created, err := service.Events.Insert(calendarID, gcalEvent).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("failed to create event: %w", err)
	}

	converted := convertEvent(created)
	return &converted, nil
}

// UpdateEvent updates an existing event.
func (c *CalendarClient) UpdateEvent(ctx context.Context, intent *EventUpdateIntent) (*Event, error) {
	service, err := c.getService(ctx)
	if err != nil {
		return nil, err
	}

	calendarID := intent.CalendarID
	if calendarID == "" {
		calendarID = "primary"
	}

	// Get existing event first
	existing, err := service.Events.Get(calendarID, intent.EventID).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("failed to get existing event (calendar=%s, event=%s): %w", calendarID, intent.EventID, err)
	}

	// Apply updates (PATCH semantics)
	if intent.Summary != nil {
		existing.Summary = *intent.Summary
	}
	if intent.Description != nil {
		existing.Description = *intent.Description
	}
	if intent.Location != nil {
		existing.Location = *intent.Location
	}
	if intent.Start != nil {
		// Preserve existing TimeZone if set
		tz := ""
		if existing.Start != nil {
			tz = existing.Start.TimeZone
		}
		existing.Start = &calendar.EventDateTime{
			DateTime: intent.Start.Format(time.RFC3339),
			TimeZone: tz,
		}
	}
	if intent.End != nil {
		// Preserve existing TimeZone if set
		tz := ""
		if existing.End != nil {
			tz = existing.End.TimeZone
		}
		existing.End = &calendar.EventDateTime{
			DateTime: intent.End.Format(time.RFC3339),
			TimeZone: tz,
		}
	}

	// Validate Start < End after partial updates
	if existing.Start != nil && existing.End != nil &&
		existing.Start.DateTime != "" && existing.End.DateTime != "" {
		startTime, err1 := time.Parse(time.RFC3339, existing.Start.DateTime)
		endTime, err2 := time.Parse(time.RFC3339, existing.End.DateTime)
		if err1 == nil && err2 == nil && !startTime.Before(endTime) {
			return nil, fmt.Errorf("start time must be before end time")
		}
	}
	if len(intent.Attendees) > 0 {
		existing.Attendees = nil
		for _, email := range intent.Attendees {
			existing.Attendees = append(existing.Attendees, &calendar.EventAttendee{
				Email: email,
			})
		}
	}
	if intent.ColorID != nil {
		existing.ColorId = *intent.ColorID
	}
	if intent.Visibility != nil {
		existing.Visibility = *intent.Visibility
	}
	if intent.Reminders != nil {
		existing.Reminders = &calendar.EventReminders{
			UseDefault: intent.Reminders.UseDefault,
		}
		for _, r := range intent.Reminders.Overrides {
			existing.Reminders.Overrides = append(existing.Reminders.Overrides,
				&calendar.EventReminder{
					Method:  r.Method,
					Minutes: int64(r.Minutes),
				})
		}
	}

	updated, err := service.Events.Update(calendarID, intent.EventID, existing).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("failed to update event (calendar=%s, event=%s): %w", calendarID, intent.EventID, err)
	}

	converted := convertEvent(updated)
	return &converted, nil
}

// DeleteEvent deletes an event.
func (c *CalendarClient) DeleteEvent(ctx context.Context, intent *EventDeleteIntent) error {
	service, err := c.getService(ctx)
	if err != nil {
		return err
	}

	calendarID := intent.CalendarID
	if calendarID == "" {
		calendarID = "primary"
	}

	err = service.Events.Delete(calendarID, intent.EventID).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("failed to delete event (calendar=%s, event=%s): %w", calendarID, intent.EventID, err)
	}

	return nil
}

// FreeBusy checks availability.
func (c *CalendarClient) FreeBusy(ctx context.Context, req *FreeBusyRequest) (*FreeBusyResponse, error) {
	service, err := c.getService(ctx)
	if err != nil {
		return nil, err
	}

	// Build request
	fbReq := &calendar.FreeBusyRequest{
		TimeMin: req.TimeMin.Format(time.RFC3339),
		TimeMax: req.TimeMax.Format(time.RFC3339),
	}
	for _, item := range req.Items {
		fbReq.Items = append(fbReq.Items, &calendar.FreeBusyRequestItem{
			Id: item.ID,
		})
	}

	resp, err := service.Freebusy.Query(fbReq).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("failed to query free/busy: %w", err)
	}

	// Convert response
	result := &FreeBusyResponse{
		TimeMin:   req.TimeMin,
		TimeMax:   req.TimeMax,
		Calendars: make(map[string]FreeBusyCalendarInfo),
	}

	for calID, calInfo := range resp.Calendars {
		info := FreeBusyCalendarInfo{}
		for _, busy := range calInfo.Busy {
			start, _ := time.Parse(time.RFC3339, busy.Start)
			end, _ := time.Parse(time.RFC3339, busy.End)
			info.Busy = append(info.Busy, TimePeriod{
				Start: start,
				End:   end,
			})
		}
		for _, err := range calInfo.Errors {
			info.Errors = append(info.Errors, Error{
				Domain:  err.Domain,
				Reason:  err.Reason,
			})
		}
		result.Calendars[calID] = info
	}

	return result, nil
}

// Helper functions

func convertEvents(items []*calendar.Event) []Event {
	var events []Event
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
		HtmlLink:    e.HtmlLink,
		Status:      e.Status,
		ColorId:     e.ColorId,
		Visibility:  e.Visibility,
	}

	if e.Start != nil {
		event.Start = &EventTime{
			Date:     e.Start.Date,
			TimeZone: e.Start.TimeZone,
		}
		if e.Start.DateTime != "" {
			event.Start.DateTime, _ = time.Parse(time.RFC3339, e.Start.DateTime)
		}
	}

	if e.End != nil {
		event.End = &EventTime{
			Date:     e.End.Date,
			TimeZone: e.End.TimeZone,
		}
		if e.End.DateTime != "" {
			event.End.DateTime, _ = time.Parse(time.RFC3339, e.End.DateTime)
		}
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
		event.Creator = &Person{
			Email:       e.Creator.Email,
			DisplayName: e.Creator.DisplayName,
			Self:        e.Creator.Self,
		}
	}

	if e.Organizer != nil {
		event.Organizer = &Person{
			Email:       e.Organizer.Email,
			DisplayName: e.Organizer.DisplayName,
			Self:        e.Organizer.Self,
		}
	}

	if e.Reminders != nil {
		event.Reminders = &Reminders{
			UseDefault: e.Reminders.UseDefault,
		}
		for _, r := range e.Reminders.Overrides {
			event.Reminders.Overrides = append(event.Reminders.Overrides, Reminder{
				Method:  r.Method,
				Minutes: int(r.Minutes),
			})
		}
	}

	if e.Created != "" {
		event.Created, _ = time.Parse(time.RFC3339, e.Created)
	}
	if e.Updated != "" {
		event.Updated, _ = time.Parse(time.RFC3339, e.Updated)
	}

	return event
}
