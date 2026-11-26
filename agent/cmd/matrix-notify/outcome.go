package main

import (
	"fmt"

	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// Status is the result kind of one run.
//
// Example: StatusSent means Matrix accepted an encrypted event; StatusKeyPending means nothing was sent.
type Status int

// Run result kinds, in wire order of statusNames.
//
// Example: a resolve-only run returns StatusResolved or StatusAbsent.
const (
	StatusUnset Status = iota
	StatusSent
	StatusResolved
	StatusAbsent // resolve-only: no valid DM exists; a send would create one
	StatusInvitePending
	StatusKeyPending
	StatusUnknown // the send request may have been stored: no event ID and no proof it was rejected
	StatusError
)

// statusNames holds each Status's fixed wire name read by the omp extension.
//
// Example: statusNames[StatusKeyPending] is "key_pending".
var statusNames = [...]string{
	StatusUnset:         "",
	StatusSent:          "sent",
	StatusResolved:      "resolved",
	StatusAbsent:        "absent",
	StatusInvitePending: "invite_pending",
	StatusKeyPending:    "key_pending",
	StatusUnknown:       "unknown",
	StatusError:         "error",
}

// String returns the status's wire name.
//
// Example: StatusSent.String() == "sent".
func (s Status) String() string {
	if s < 0 || int(s) >= len(statusNames) {
		return fmt.Sprintf("Status(%d)", int(s))
	}
	return statusNames[s]
}

// MarshalText encodes the status as its wire name.
//
// Example: json.Marshal(StatusAbsent) yields `"absent"`.
func (s Status) MarshalText() ([]byte, error) {
	if s < 0 || int(s) >= len(statusNames) {
		return nil, fmt.Errorf("invalid status %d", int(s))
	}
	return []byte(statusNames[s]), nil
}

// Outcome is what resolve or deliver established about one recipient's DM; once returned it is final.
//
// Example: Outcome{Status: StatusSent, RoomID: room, EventID: ev}.
type Outcome struct {
	Status              Status           `json:"status"`
	RoomID              id.RoomID        `json:"room_id,omitempty"`
	RecipientMembership event.Membership `json:"recipient_membership,omitempty"`
	EventID             id.EventID       `json:"event_id,omitempty"`
}
