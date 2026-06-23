package gkdb

import "github.com/google/uuid"

// newID returns a random UUIDv4 string. google/uuid is already in go.mod.
func newID() string { return uuid.NewString() }
