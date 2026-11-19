// Package config loads and validates process configuration from environment
// variables. Loading fails fast: every invalid variable is reported at once so
// a misconfigured deployment never starts.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"time"
)

// LookupFunc matches os.LookupEnv; injected so tests don't touch the real env.
type LookupFunc func(key string) (string, bool)

// Config is the fully validated process configuration.
type Config struct {
	HTTP     HTTP
	LogLevel slog.Level
}

// HTTP holds listener and server timeout settings.
type HTTP struct {
	Addr              string
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration
}

// Load reads configuration using lookup, applying defaults for unset variables.
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
		},
		LogLevel: p.logLevel("LOG_LEVEL", slog.LevelInfo),
	}

	if err := errors.Join(p.errs...); err != nil {
		return Config{}, fmt.Errorf("invalid configuration: %w", err)
	}
	return cfg, nil
}

// parser accumulates errors so all problems are reported in one go.
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
