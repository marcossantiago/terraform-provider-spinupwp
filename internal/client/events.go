package client

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// Event statuses with a fixed meaning. Other statuses ("queued", "creating", ...) mean in progress.
const (
	EventStatusDeployed = "deployed"
	EventStatusFailed   = "failed"
)

// Event is an asynchronous operation, such as creating a site.
type Event struct {
	ID     int64   `json:"id"`
	Name   string  `json:"name"`
	Status string  `json:"status"`
	Output *string `json:"output"`
}

// GetEvent returns an event by ID.
func (c *Client) GetEvent(ctx context.Context, id int64) (*Event, error) {
	var out struct {
		Data Event `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/events/%d", id), nil, &out); err != nil {
		return nil, err
	}
	return &out.Data, nil
}

// EventFailedError is returned when an event finishes with the "failed" status.
type EventFailedError struct {
	Event Event
}

func (e *EventFailedError) Error() string {
	msg := fmt.Sprintf("spinupwp: event %d (%s) failed", e.Event.ID, e.Event.Name)
	if e.Event.Output != nil && strings.TrimSpace(*e.Event.Output) != "" {
		msg += ":\n" + strings.TrimSpace(*e.Event.Output)
	}
	return msg
}

// WaitForEvent polls an event until it is deployed, fails, or ctx is done.
// An event ID of 0 means there is nothing to wait for.
func (c *Client) WaitForEvent(ctx context.Context, id int64) error {
	if id == 0 {
		return nil
	}
	for {
		ev, err := c.GetEvent(ctx, id)
		if err != nil {
			return err
		}
		switch strings.ToLower(ev.Status) {
		case EventStatusDeployed:
			return nil
		case EventStatusFailed:
			return &EventFailedError{Event: *ev}
		}
		if err := sleep(ctx, c.PollInterval); err != nil {
			return fmt.Errorf("spinupwp: waiting for event %d (%s, last status %q): %w", id, ev.Name, ev.Status, err)
		}
	}
}
