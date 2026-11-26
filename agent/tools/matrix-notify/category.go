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
	CategoryInternal Category = iota
	CategoryInput
	CategoryCredentials
	CategoryState
	CategoryIdentity
	CategoryAuth
	CategoryNetwork
	CategoryRoom
	CategoryCrypto
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
	var ce *CategorizedError
	if errors.As(err, &ce) {
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
	var ce *CategorizedError
	if errors.As(err, &ce) {
		return ce.Category
	}
	return CategoryInternal
}
