package engine

import (
	"errors"
	"strings"
)

// redact rewrites an error that may quote a password. pgx and libpq both
// include the connection string in some failures, and an operator pasting a
// failure into a ticket should not be pasting production's password with it.
func redact(err error, secret string) error {
	if err == nil || secret == "" {
		return err
	}
	msg := err.Error()
	if !strings.Contains(msg, secret) {
		return err
	}
	return errors.New(strings.ReplaceAll(msg, secret, "«redacted»"))
}
