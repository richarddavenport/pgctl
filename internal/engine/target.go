package engine

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"

	"github.com/jackc/pgx/v5"

	"github.com/richarddavenport/pgctl/internal/config"
)

// Target is a resolved place to read from or write to: one database on one
// environment's server, with the credentials to reach it.
type Target struct {
	Env      config.Environment
	Database string
	Host     string
	Port     int
	User     string
	Password string

	// Secrets is the environment's whole decrypted set, kept because storage
	// credentials come from the same file.
	Secrets Secrets
}

// Resolve turns an environment and a database name into something connectable.
// Config overrides win over the secrets file, since an override exists only to
// contradict it.
func Resolve(ctx context.Context, cfg *config.Config, root, envName, database string) (*Target, error) {
	env, ok := cfg.LookupEnv(envName)
	if !ok {
		return nil, fmt.Errorf("unknown environment %q", envName)
	}

	secrets, err := LoadSecrets(ctx, root, env.Secrets.File)
	if err != nil {
		return nil, err
	}

	t := &Target{Env: env, Database: database, Secrets: secrets, Port: 5432}

	if host, ok := lookup(secrets, cfg.Credentials.HostKey); ok {
		t.Host = host
	}
	if port, ok := lookup(secrets, cfg.Credentials.PortKey); ok {
		if n, err := strconv.Atoi(port); err == nil {
			t.Port = n
		}
	}
	if user, ok := lookup(secrets, cfg.Credentials.UserKey); ok {
		t.User = user
	}
	if pw, ok := lookup(secrets, cfg.Credentials.PasswordKey); ok {
		t.Password = pw
	}

	if env.Server.Host != "" {
		t.Host = env.Server.Host
	}
	if env.Server.Port != 0 {
		t.Port = env.Server.Port
	}

	if t.Host == "" {
		return nil, fmt.Errorf("environment %q: no postgres host in %s or in the config",
			env.Name, orNone(env.Secrets.File))
	}
	return t, nil
}

// lookup reads a credential from the environment's decrypted secrets, falling
// back to pgctl's own process environment.
//
// The fallback is what makes an environment with no secrets file usable — a
// developer's local cluster, or a CI job handed credentials directly — without
// a second way of declaring credentials in the config. A real environment has
// a secrets file, so the fallback never fires for one.
func lookup(secrets Secrets, key string) (string, bool) {
	if v, ok := secrets.Get(key); ok {
		return v, true
	}
	if v := os.Getenv(key); v != "" {
		return v, true
	}
	return "", false
}

func orNone(s string) string {
	if s == "" {
		return "(no secrets file)"
	}
	return s
}

// Jobs is the parallelism to use against this target.
func (t *Target) Jobs(def config.Defaults) int {
	if t.Env.Server.Jobs > 0 {
		return t.Env.Server.Jobs
	}
	return def.Jobs
}

// MaintenanceDB is the database to connect to for work that cannot be done
// from inside the database being replaced.
func (t *Target) MaintenanceDB() string {
	if t.Env.Server.MaintenanceDB != "" {
		return t.Env.Server.MaintenanceDB
	}
	return "postgres"
}

// Connect opens a connection to the target's database. Pass an empty database
// to reach the maintenance database instead.
func (t *Target) Connect(ctx context.Context, database string) (*pgx.Conn, error) {
	if database == "" {
		database = t.MaintenanceDB()
	}
	cfg, err := pgx.ParseConfig("")
	if err != nil {
		return nil, err
	}
	cfg.Host = t.Host
	cfg.Port = uint16(t.Port)
	cfg.Database = database
	cfg.User = t.User
	cfg.Password = t.Password

	// Azure Database for PostgreSQL requires TLS and presents a certificate
	// pgx will not verify without a root store configured. Verifying the
	// hostname without a CA is not a thing sslmode offers, so this matches
	// what psql does by default: encrypt, do not verify. Worth revisiting if
	// pgctl ever runs somewhere the DigiCert roots are present.
	cfg.TLSConfig = nil
	if !isLoopback(t.Host) {
		cfg.TLSConfig = tlsPreferred(t.Host)
	}

	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		// pgx puts the whole connection string in its error, password
		// included. Report where we were going and not how we got in.
		return nil, fmt.Errorf("connect to %s/%s as %s: %w", t.Host, database, t.User, redact(err, t.Password))
	}
	return conn, nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// String describes the target for a log line or a confirmation prompt.
func (t *Target) String() string {
	return fmt.Sprintf("%s (%s@%s:%d/%s)", t.Env.Name, t.User, t.Host, t.Port, t.Database)
}
