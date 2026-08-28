package engine

import (
	"crypto/tls"
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

// tlsPreferred is TLS without certificate verification: what `sslmode=require`
// means, which is what every client in this stack already uses against Azure.
// Named for what it is so that nobody reads InsecureSkipVerify in isolation and
// assumes it was an accident.
func tlsPreferred(host string) *tls.Config {
	return &tls.Config{
		ServerName: host,
		// #nosec G402 -- see Target.Connect: no CA bundle is available to
		// verify Azure's certificate against, and the alternative is plaintext.
		InsecureSkipVerify: true,
	}
}
