// Package config loads and validates process configuration from environment
// variables. Loading fails fast: every invalid variable is reported at once so
// a misconfigured deployment never starts.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// LookupFunc matches os.LookupEnv; injected so tests don't touch the real env.
type LookupFunc func(key string) (string, bool)

// Config is the fully validated process configuration.
type Config struct {
	HTTP     HTTP
	DB       DB
	Pricing  Pricing
	Auth     Auth
	Admin    Admin
	LogLevel slog.Level
}

// HTTP holds listener, timeout and shutdown settings.
type HTTP struct {
	Addr              string
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration
	// DrainDelay is how long /readyz reports failure before the server stops
	// accepting connections, so load balancers can stop routing to us first.
	DrainDelay   time.Duration
	MaxBodyBytes int64
}

// DB holds PostgreSQL connection pool settings.
type DB struct {
	URL              Secret
	MaxConns         int32
	MinConns         int32
	MaxConnLifetime  time.Duration
	MaxConnIdleTime  time.Duration
	ConnectTimeout   time.Duration
	StatementTimeout time.Duration
	// MigrateOnStart runs pending migrations before serving. Off by default:
	// in production migrations run as a separate one-off step (`api migrate`).
	MigrateOnStart bool
}

// Pricing holds settings for the price comparison fan-out.
type Pricing struct {
	// Budget caps the whole fan-out; networks that have not answered by then
	// are reported as timed out instead of delaying the response.
	Budget time.Duration
}

// Auth holds the static API keys accepted by the service. An empty list is
// valid and means every protected request is rejected (deny by default).
type Auth struct {
	Keys []APIKey
}

// APIKey pairs a public identifier, safe to log, with the secret key.
type APIKey struct {
	ID  string
	Key Secret
}

// Admin configures the operational listener (pprof). It is separate from
// the API so profiling is never reachable through the public port.
type Admin struct {
	// Addr is empty by default, which disables the listener.
	Addr string
}

// Secret is a string that never prints its value (fmt, %+v, slog).
type Secret string

func (Secret) String() string                 { return "[REDACTED]" }
func (Secret) GoString() string               { return `"[REDACTED]"` }
func (Secret) LogValue() slog.Value           { return slog.StringValue("[REDACTED]") }
func (s Secret) Reveal() string               { return string(s) }
func (s Secret) MarshalText() ([]byte, error) { return []byte("[REDACTED]"), nil }

func Load(lookup LookupFunc) (Config, error) {
	p := parser{lookup: lookup}

	cfg := Config{
		HTTP: HTTP{
			Addr:              p.addr("HTTP_ADDR", ":8080"),
			ReadHeaderTimeout: p.duration("HTTP_READ_HEADER_TIMEOUT", 5*time.Second),
			ReadTimeout:       p.duration("HTTP_READ_TIMEOUT", 10*time.Second),
			WriteTimeout:      p.duration("HTTP_WRITE_TIMEOUT", 15*time.Second),
			IdleTimeout:       p.duration("HTTP_IDLE_TIMEOUT", 60*time.Second),
			ShutdownTimeout:   p.duration("SHUTDOWN_TIMEOUT", 20*time.Second),
			DrainDelay:        p.durationOrZero("SHUTDOWN_DRAIN_DELAY", 5*time.Second),
			MaxBodyBytes:      p.positiveInt64("HTTP_MAX_BODY_BYTES", 64<<10), // 64 KiB: a medication record is < 1 KiB
		},
		DB: DB{
			URL:              Secret(p.required("DATABASE_URL")),
			MaxConns:         p.positiveInt32("DB_MAX_CONNS", 10),
			MinConns:         p.nonNegativeInt32("DB_MIN_CONNS", 0),
			MaxConnLifetime:  p.duration("DB_MAX_CONN_LIFETIME", 30*time.Minute),
			MaxConnIdleTime:  p.duration("DB_MAX_CONN_IDLE_TIME", 5*time.Minute),
			ConnectTimeout:   p.duration("DB_CONNECT_TIMEOUT", 5*time.Second),
			StatementTimeout: p.duration("DB_STATEMENT_TIMEOUT", 5*time.Second),
			MigrateOnStart:   p.boolean("MIGRATE_ON_START", false),
		},
		Pricing: Pricing{
			Budget: p.duration("PRICING_BUDGET", 300*time.Millisecond),
		},
		Auth:     Auth{Keys: p.apiKeys("API_KEYS")},
		Admin:    Admin{Addr: p.addr("ADMIN_ADDR", "")},
		LogLevel: p.logLevel("LOG_LEVEL", slog.LevelInfo),
	}

	if cfg.DB.MinConns > cfg.DB.MaxConns {
		p.errs = append(p.errs, fmt.Errorf("DB_MIN_CONNS (%d) must not exceed DB_MAX_CONNS (%d)", cfg.DB.MinConns, cfg.DB.MaxConns))
	}

	if cfg.Admin.Addr != "" && cfg.Admin.Addr == cfg.HTTP.Addr {
		p.errs = append(p.errs, fmt.Errorf("ADMIN_ADDR must differ from HTTP_ADDR (%s)", cfg.HTTP.Addr))
	}

	// Past the write timeout the client would get a dropped connection instead
	// of the partial result the budget exists to deliver.
	if cfg.Pricing.Budget >= cfg.HTTP.WriteTimeout {
		p.errs = append(p.errs, fmt.Errorf("PRICING_BUDGET (%s) must be below HTTP_WRITE_TIMEOUT (%s)", cfg.Pricing.Budget, cfg.HTTP.WriteTimeout))
	}

	if err := errors.Join(p.errs...); err != nil {
		return Config{}, fmt.Errorf("invalid configuration: %w", err)
	}
	return cfg, nil
}

// parser accumulates errors so a bad deployment reports every problem at once.
type parser struct {
	lookup LookupFunc
	errs   []error
}

func (p *parser) get(key string) (string, bool) {
	v, ok := p.lookup(key)
	if !ok || v == "" {
		return "", false
	}
	return v, true
}

func (p *parser) fail(key, v, reason string) {
	p.errs = append(p.errs, fmt.Errorf("%s=%q: %s", key, v, reason))
}

// required never echoes the value: required settings are often secrets.
func (p *parser) required(key string) string {
	v, ok := p.get(key)
	if !ok {
		p.errs = append(p.errs, fmt.Errorf("%s: is required", key))
	}
	return v
}

// Keys shorter than this are guessable; 16 random characters already give
// far more entropy than an online attacker can search.
const minAPIKeyLength = 16

var apiKeyIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// apiKeys parses "id:key,id:key". Errors name the entry by position or ID,
// never by key, so a typo in a secret does not end up in deployment logs.
func (p *parser) apiKeys(name string) []APIKey {
	v, ok := p.get(name)
	if !ok {
		return nil
	}
	var keys []APIKey
	ids, secrets := map[string]bool{}, map[string]bool{}
	for i, entry := range strings.Split(v, ",") {
		id, key, found := strings.Cut(strings.TrimSpace(entry), ":")
		switch {
		case !found || !apiKeyIDPattern.MatchString(id):
			p.errs = append(p.errs, fmt.Errorf("%s: entry %d: want id:key with id of 1-64 letters, digits, '-' or '_'", name, i+1))
		case len(key) < minAPIKeyLength:
			p.errs = append(p.errs, fmt.Errorf("%s: key %q: must be at least %d characters", name, id, minAPIKeyLength))
		case ids[id]:
			p.errs = append(p.errs, fmt.Errorf("%s: key %q: duplicate id", name, id))
		case secrets[key]:
			// The same secret under two IDs would make audit logs ambiguous.
			p.errs = append(p.errs, fmt.Errorf("%s: key %q: reuses another entry's secret", name, id))
		default:
			ids[id], secrets[key] = true, true
			keys = append(keys, APIKey{ID: id, Key: Secret(key)})
		}
	}
	return keys
}

func (p *parser) addr(key, def string) string {
	v, ok := p.get(key)
	if !ok {
		return def
	}
	if _, _, err := net.SplitHostPort(v); err != nil {
		p.fail(key, v, "must be host:port (e.g. :8080)")
		return def
	}
	return v
}

func (p *parser) duration(key string, def time.Duration) time.Duration {
	v, ok := p.get(key)
	if !ok {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		p.fail(key, v, "must be a positive Go duration (e.g. 10s)")
		return def
	}
	return d
}

// durationOrZero is for delays where 0 legitimately means "disabled".
func (p *parser) durationOrZero(key string, def time.Duration) time.Duration {
	v, ok := p.get(key)
	if !ok {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		p.fail(key, v, "must be a non-negative Go duration (e.g. 5s, 0s)")
		return def
	}
	return d
}

func (p *parser) positiveInt64(key string, def int64) int64 {
	v, ok := p.get(key)
	if !ok {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		p.fail(key, v, "must be a positive integer")
		return def
	}
	return n
}

func (p *parser) int32InRange(key string, def, minVal int32, reason string) int32 {
	v, ok := p.get(key)
	if !ok {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 32)
	if err != nil || int32(n) < minVal {
		p.fail(key, v, reason)
		return def
	}
	return int32(n)
}

func (p *parser) positiveInt32(key string, def int32) int32 {
	return p.int32InRange(key, def, 1, "must be a positive integer")
}

func (p *parser) nonNegativeInt32(key string, def int32) int32 {
	return p.int32InRange(key, def, 0, "must be a non-negative integer")
}

func (p *parser) boolean(key string, def bool) bool {
	v, ok := p.get(key)
	if !ok {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		p.fail(key, v, "must be a boolean (true/false)")
		return def
	}
	return b
}

func (p *parser) logLevel(key string, def slog.Level) slog.Level {
	v, ok := p.get(key)
	if !ok {
		return def
	}
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(v)); err != nil {
		p.fail(key, v, "must be one of debug, info, warn, error")
		return def
	}
	return lvl
}
