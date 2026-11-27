package main

import (
	"errors"
	"fmt"
)

// Category is the fixed, secret-free class of a failure; it is the only failure detail that leaves the process.
//
// Example: report writes `"error":"network"` for an error categorized as CategoryNetwork.
type Category int

// Failure categories, in wire order of categoryNames.
//
// Example: categorize(CategoryAuth, err) marks a rejected login.
const (
	// CategoryInternal is an uncategorized, unexpected failure.
	//
	// Example: categoryOf(errors.New("x")) == CategoryInternal.
	CategoryInternal Category = iota
	// CategoryInput is a bad alias or an empty message.
	//
	// Example: parseRecipient("bob") fails with CategoryInput.
	CategoryInput
	// CategoryCredentials is a missing or invalid Matrix setting in Hermes's .env.
	//
	// Example: an http:// homeserver fails with CategoryCredentials.
	CategoryCredentials
	// CategoryState is an unreadable or unwritable state directory or file.
	//
	// Example: a short pickle.key fails with CategoryState.
	CategoryState
	// CategoryIdentity is a saved session or store that is not this tool's own device on Hermes's account.
	//
	// Example: a crypto store without a session fails with CategoryIdentity.
	CategoryIdentity
	// CategoryAuth is a rejected password or access token.
	//
	// Example: /login answered 403 M_FORBIDDEN fails with CategoryAuth.
	CategoryAuth
	// CategoryNetwork is a homeserver call that got no Matrix answer.
	//
	// Example: a refused connection fails with CategoryNetwork.
	CategoryNetwork
	// CategoryRoom is a failed room state, m.direct or room creation call.
	//
	// Example: createRoom answered 403 fails with CategoryRoom.
	CategoryRoom
	// CategoryCrypto is a failed key query, key share or encryption.
	//
	// Example: a send without an initialized crypto helper fails with CategoryCrypto.
	CategoryCrypto
	// CategorySend is a /send the homeserver answered with a Matrix error. Only a 4xx answer reaches the result,
	// because deliver reports any other /send failure as unknown.
	//
	// Example: /send answered 400 M_UNKNOWN fails with CategorySend.
	CategorySend
)

// categoryNames holds each Category's fixed wire name.
//
// Example: categoryNames[CategoryInput] is "input".
var categoryNames = [...]string{
	CategoryInternal:    "internal",
	CategoryInput:       "input",
	CategoryCredentials: "credentials",
	CategoryState:       "state",
	CategoryIdentity:    "identity",
	CategoryAuth:        "auth",
	CategoryNetwork:     "network",
	CategoryRoom:        "room",
	CategoryCrypto:      "crypto",
	CategorySend:        "send",
}

// String returns the category's wire name.
//
// Example: CategoryAuth.String() == "auth".
func (c Category) String() string {
	if c < 0 || int(c) >= len(categoryNames) {
		return fmt.Sprintf("Category(%d)", int(c))
	}
	return categoryNames[c]
}

// MarshalText encodes the category as its wire name.
//
// Example: json.Marshal(CategoryRoom) yields `"room"`.
func (c Category) MarshalText() ([]byte, error) {
	if c < 0 || int(c) >= len(categoryNames) {
		return nil, fmt.Errorf("invalid category %d", int(c))
	}
	return []byte(categoryNames[c]), nil
}

// CategorizedError attaches a Category to an underlying error.
//
// Example: errors.As(err, &ce) recovers the category of a wrapped failure.
type CategorizedError struct {
	Category Category
	Err      error
}

// Error returns the category followed by the underlying error text.
//
// Example: "network: dial tcp: refused".
func (e *CategorizedError) Error() string { return e.Category.String() + ": " + e.Err.Error() }

// Unwrap exposes the underlying error to errors.Is and errors.As.
//
// Example: errors.Is(categorize(CategoryState, ctx.Err()), context.Canceled) is true.
func (e *CategorizedError) Unwrap() error { return e.Err }

// categorize assigns c to err unless err already carries a category, so each failure keeps exactly the category
// assigned where it was first understood.
//
// Example: categorize(CategorySend, classifyHTTP(err, CategorySend)) keeps the network category of a dial failure.
func categorize(
	c Category,
	err error,
) error {
	if _, ok := errors.AsType[*CategorizedError](err); ok {
		return err
	}
	return &CategorizedError{Category: c, Err: err}
}

// categorizeIfErr is categorize for a possibly-nil error.
//
// Example: return categorizeIfErr(CategoryState, f.Close()).
func categorizeIfErr(
	c Category,
	err error,
) error {
	if err == nil {
		return nil
	}
	return categorize(c, err)
}

// categoryOf returns err's category, or CategoryInternal when none was assigned.
//
// Example: categoryOf(errors.New("x")) == CategoryInternal.
func categoryOf(err error) Category {
	if ce, ok := errors.AsType[*CategorizedError](err); ok {
		return ce.Category
	}
	return CategoryInternal
}
