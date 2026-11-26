package main

import "fmt"

// redactedSecret is what every fmt form of a Secret prints.
//
// Example: fmt.Sprint(Secret("pw")) == redactedSecret.
const redactedSecret = "<redacted>"

// Secret is a credential string that never appears in formatted output; JSON keeps the raw value so stored state
// (session.json) round-trips unchanged.
//
// Example: HermesCredentials.Password and Session.AccessToken are Secrets; pass string(s) only to the Matrix client.
type Secret string

// String redacts the secret.
//
// Example: Secret("pw").String() == "<redacted>".
func (Secret) String() string { return redactedSecret }

// GoString redacts the secret under %#v.
//
// Example: fmt.Sprintf("%#v", Secret("pw")) == "<redacted>".
func (Secret) GoString() string { return redactedSecret }

// Format redacts the secret under every verb, including %x and %d, which bypass String.
//
// Example: fmt.Sprintf("%x", Secret("pw")) == "<redacted>".
func (Secret) Format(
	f fmt.State,
	_ rune,
) {
	_, _ = f.Write([]byte(redactedSecret))
}
