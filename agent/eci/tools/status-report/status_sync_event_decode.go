package main

import (
	"bytes"
	"encoding/json"
)

// UnmarshalJSON decodes optional status items independently and preserves their ordinals.
//
// Example: A malformed update cannot discard a later valid sibling.
func (event *syncEvent) UnmarshalJSON(data []byte) error {
	var envelope struct {
		Event     string `json:"event"`
		Timestamp string `json:"timestamp"`
		Details   struct {
			Corrects     json.RawMessage `json:"corrects"`
			StatusUpdate json.RawMessage `json:"status_update"`
		} `json:"details"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return err
	}
	*event = syncEvent{Event: envelope.Event, Timestamp: envelope.Timestamp}
	event.Details.Corrects = envelope.Details.Corrects
	raw := envelope.Details.StatusUpdate
	if len(raw) == 0 {
		return nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		event.InvalidStatus = true
		return nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		event.InvalidStatus = true
		return nil
	}
	status := &syncEventStatusUpdate{}
	event.Details.StatusUpdate = status
	if rawUpdates, exists := fields["updates"]; exists {
		var items []json.RawMessage
		if err := json.Unmarshal(rawUpdates, &items); err != nil {
			event.InvalidStatus = true
		} else {
			for _, item := range items {
				var update syncUpdate
				if json.Unmarshal(item, &update) != nil {
					event.InvalidStatus = true
					update = syncUpdate{}
				}
				status.Updates = append(status.Updates, update)
			}
		}
	}
	if rawAdditions, exists := fields["additions"]; exists {
		var items []json.RawMessage
		if err := json.Unmarshal(rawAdditions, &items); err != nil {
			event.InvalidStatus = true
		} else {
			for _, item := range items {
				var addition syncAddition
				if json.Unmarshal(item, &addition) != nil {
					event.InvalidStatus = true
					addition = syncAddition{}
				}
				status.Additions = append(status.Additions, addition)
			}
		}
	}
	if rawTimeline, exists := fields["timeline"]; exists && !bytes.Equal(bytes.TrimSpace(rawTimeline), []byte("null")) {
		var snapshot syncTimelineSnapshot
		if json.Unmarshal(rawTimeline, &snapshot) != nil {
			event.InvalidStatus = true
		} else {
			status.Timeline = &snapshot
		}
	}
	return nil
}
