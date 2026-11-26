package main

import (
	"errors"
	"fmt"

	"maunium.net/go/mautrix/id"
)

// Recipient is one of the closed set of people this tool may message; the zero value is unset.
//
// Example: r, err := parseRecipient("void"); r.MXID() is void's Matrix ID.
type Recipient int

// The allowlisted recipients.
//
// Example: RecipientVickey.MXID().
const (
	RecipientUnset Recipient = iota
	RecipientVoid
	RecipientVickey
)

// recipientTable is the single alias→MXID table; the omp tool schema lists only the aliases.
//
// Example: recipientTable[RecipientVoid].mxid.
var recipientTable = map[Recipient]struct {
	alias string
	mxid  id.UserID
}{
	RecipientVoid:   {alias: "void", mxid: "@xaionaro:matrix.dx.center"},
	RecipientVickey: {alias: "vickey", mxid: "@danusya:matrix.dx.center"},
}

// parseRecipient maps an exact alias to its Recipient, or fails with CategoryInput.
//
// Example: parseRecipient("vickey") returns RecipientVickey.
func parseRecipient(alias string) (Recipient, error) {
	for r, e := range recipientTable {
		if e.alias == alias {
			return r, nil
		}
	}
	return RecipientUnset, categorize(CategoryInput, errors.New("unknown recipient; allowed: void, vickey"))
}

// MXID returns the recipient's Matrix user ID, or "" when unset.
//
// Example: RecipientVoid.MXID() == "@xaionaro:matrix.dx.center".
func (r Recipient) MXID() id.UserID { return recipientTable[r].mxid }

// String returns the recipient's alias, or "" when unset.
//
// Example: RecipientVickey.String() == "vickey".
func (r Recipient) String() string { return recipientTable[r].alias }

// MarshalText encodes the recipient as its alias.
//
// Example: json.Marshal(RecipientVoid) yields `"void"`.
func (r Recipient) MarshalText() ([]byte, error) {
	if _, ok := recipientTable[r]; !ok && r != RecipientUnset {
		return nil, fmt.Errorf("invalid recipient %d", int(r))
	}
	return []byte(r.String()), nil
}
