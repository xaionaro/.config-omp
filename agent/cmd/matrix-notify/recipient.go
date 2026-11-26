package main

import (
	"fmt"
	"slices"
	"strings"

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
	// RecipientUnset is the zero value, never a valid target.
	//
	// Example: Result{}.To == RecipientUnset before parsing.
	RecipientUnset Recipient = iota
	// RecipientVoid is the user, @xaionaro:matrix.dx.center.
	//
	// Example: parseRecipient("void").
	RecipientVoid
	// RecipientVickey is @danusya:matrix.dx.center.
	//
	// Example: parseRecipient("vickey").
	RecipientVickey
)

// RecipientEntry is one row of the alias→Matrix ID table.
//
// Example: RecipientEntry{Alias: "void", MXID: "@xaionaro:matrix.dx.center"}.
type RecipientEntry struct {
	Alias string
	MXID  id.UserID
}

// recipientTable is the single alias→MXID table; the omp tool schema lists only the aliases.
//
// Example: recipientTable[RecipientVoid].MXID.
var recipientTable = map[Recipient]RecipientEntry{
	RecipientVoid:   {Alias: "void", MXID: "@xaionaro:matrix.dx.center"},
	RecipientVickey: {Alias: "vickey", MXID: "@danusya:matrix.dx.center"},
}

// parseRecipient maps an exact alias to its Recipient, or fails with CategoryInput.
//
// Example: parseRecipient("vickey") returns RecipientVickey.
func parseRecipient(alias string) (Recipient, error) {
	for r, e := range recipientTable {
		if e.Alias == alias {
			return r, nil
		}
	}
	return RecipientUnset, categorize(CategoryInput, fmt.Errorf("unknown recipient; allowed: %s", strings.Join(recipientAliases(), ", ")))
}

// recipientAliases lists every alias in recipientTable, sorted.
//
// Example: recipientAliases() == []string{"vickey", "void"}.
func recipientAliases() []string {
	aliases := make([]string, 0, len(recipientTable))
	for _, e := range recipientTable {
		aliases = append(aliases, e.Alias)
	}
	slices.Sort(aliases)
	return aliases
}

// MXID returns the recipient's Matrix user ID, or "" when unset.
//
// Example: RecipientVoid.MXID() == "@xaionaro:matrix.dx.center".
func (r Recipient) MXID() id.UserID { return recipientTable[r].MXID }

// String returns the recipient's alias, or "" when unset.
//
// Example: RecipientVickey.String() == "vickey".
func (r Recipient) String() string { return recipientTable[r].Alias }

// MarshalText encodes the recipient as its alias.
//
// Example: json.Marshal(RecipientVoid) yields `"void"`.
func (r Recipient) MarshalText() ([]byte, error) {
	if _, ok := recipientTable[r]; !ok && r != RecipientUnset {
		return nil, fmt.Errorf("invalid recipient %d", int(r))
	}
	return []byte(r.String()), nil
}
