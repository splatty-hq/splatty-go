package splatty

import (
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// DefaultURL is the hosted Splatty server.
const DefaultURL = "https://splatty.app"

// Config configures a Client. The zero value is usable: every field falls back
// to an environment variable or a sensible default, and the booleans are named
// so that the zero value is the recommended setting.
type Config struct {
	// URL is the Splatty server base URL. Env: SPLATTY_URL.
	URL string
	// DSN is the project key, sent as a bearer token. Env: SPLATTY_DSN.
	DSN string
	// Environment is stamped on every event. Env: SPLATTY_ENVIRONMENT, GO_ENV, APP_ENV.
	Environment string
	// Release is stamped on every event. Env: SPLATTY_RELEASE.
	Release string
	// ServerName overrides the reported host. Defaults to os.Hostname().
	ServerName string

	// Disabled turns every capture into a no-op.
	Disabled bool
	// DisableLogs skips installing the batching log appender.
	DisableLogs bool
	// SendDefaultPII sends request headers verbatim instead of filtering the
	// sensitive ones.
	SendDefaultPII bool
	// Synchronous sends events inline instead of handing them to the background
	// sender. Useful in tests and short-lived CLIs.
	Synchronous bool
	// DisableSourceContext stops the SDK reading source files to attach the
	// lines around each stack frame.
	DisableSourceContext bool

	// ContextLines is how many source lines to send either side of a frame.
	// Defaults to 5.
	ContextLines int

	// OpenTimeout bounds connection setup. Defaults to 5s.
	OpenTimeout time.Duration
	// ReadTimeout bounds the whole request. Defaults to 10s.
	ReadTimeout time.Duration
	// QueueSize bounds the background sender's backlog. Defaults to 1000.
	// Captures past this point are dropped rather than blocking the caller.
	QueueSize int

	// Logger receives the SDK's own warnings. Defaults to log.Default().
	Logger *log.Logger
	// HTTPClient overrides the transport's client.
	HTTPClient *http.Client
	// BeforeSend is the last chance to mutate an event, or to drop it by
	// returning nil. It runs after header scrubbing.
	BeforeSend func(*Event) *Event
	// LogOptions tunes the log appender.
	LogOptions LogOptions
}

// contextLines resolves the source-context window. It reads the config
// directly rather than through applyDefaults so that a hand-built Config still
// gets context.
func (c *Config) contextLines() int {
	if c.DisableSourceContext {
		return 0
	}
	if c.ContextLines <= 0 {
		return defaultContextLines
	}
	return c.ContextLines
}

func (c *Config) applyDefaults() {
	if c.URL == "" {
		c.URL = envOr("SPLATTY_URL", DefaultURL)
	}
	if c.DSN == "" {
		c.DSN = os.Getenv("SPLATTY_DSN")
	}
	if c.Environment == "" {
		c.Environment = firstNonEmpty(
			os.Getenv("SPLATTY_ENVIRONMENT"),
			os.Getenv("GO_ENV"),
			os.Getenv("APP_ENV"),
			"development",
		)
	}
	if c.Release == "" {
		c.Release = os.Getenv("SPLATTY_RELEASE")
	}
	if c.ServerName == "" {
		c.ServerName, _ = os.Hostname()
	}
	if c.OpenTimeout == 0 {
		c.OpenTimeout = 5 * time.Second
	}
	if c.ReadTimeout == 0 {
		c.ReadTimeout = 10 * time.Second
	}
	if c.QueueSize == 0 {
		c.QueueSize = 1000
	}
	if c.Logger == nil {
		c.Logger = log.Default()
	}
}

// Validate disables the config rather than failing, mirroring the Ruby and JS
// clients: a misconfigured SDK must never stop an application from booting.
func (c *Config) Validate() {
	if c.Disabled {
		return
	}
	if strings.TrimSpace(c.URL) == "" {
		c.disable("config.URL is required")
		return
	}
	if strings.TrimSpace(c.DSN) == "" {
		c.disable("config.DSN is required")
		return
	}
	parsed, err := url.Parse(c.URL)
	if err != nil {
		c.disable("config.URL is invalid: " + err.Error())
		return
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		c.disable("config.URL must include scheme + host")
	}
}

func (c *Config) disable(message string) {
	c.Disabled = true
	c.warn("[Splatty] disabled: " + message)
}

func (c *Config) warn(message string) {
	if c.Logger != nil {
		c.Logger.Print(message)
		return
	}
	log.Print(message)
}

// IsEnabled reports whether captures will be sent.
func (c *Config) IsEnabled() bool {
	return !c.Disabled && c.DSN != "" && c.URL != ""
}

// EnvelopeURL is the intake endpoint events and logs are posted to.
func (c *Config) EnvelopeURL() string {
	return strings.TrimRight(c.URL, "/") + "/api/envelope"
}

// DSNKey is the bearer token sent with every request.
func (c *Config) DSNKey() string { return c.DSN }

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
