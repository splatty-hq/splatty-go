package splatty

import "testing"

func TestEnvelopeURLBuiltFromURL(t *testing.T) {
	cfg := testConfig(func(c *Config) { c.URL = "http://host.example:3001" })
	if got, want := cfg.DSNKey(), "abc123"; got != want {
		t.Errorf("DSNKey() = %q, want %q", got, want)
	}
	if got, want := cfg.EnvelopeURL(), "http://host.example:3001/api/envelope"; got != want {
		t.Errorf("EnvelopeURL() = %q, want %q", got, want)
	}
}

func TestEnvelopeURLStripsTrailingSlash(t *testing.T) {
	cfg := testConfig(func(c *Config) { c.URL = "https://example.com/" })
	if got, want := cfg.EnvelopeURL(), "https://example.com/api/envelope"; got != want {
		t.Errorf("EnvelopeURL() = %q, want %q", got, want)
	}
}

func TestValidateDisablesOnBadConfig(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"url blank", func(c *Config) { c.URL = "   " }},
		{"dsn missing", func(c *Config) { c.DSN = "" }},
		{"url invalid", func(c *Config) { c.URL = "not-a-url" }},
		{"url without host", func(c *Config) { c.URL = "https://" }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{URL: "https://example.com", DSN: "abc", Logger: discardLogger}
			tc.mutate(cfg)
			// An empty URL means "use the default", so only a blank-but-set
			// URL can reach the required check.
			if cfg.DSN == "" {
				t.Setenv("SPLATTY_DSN", "")
			}
			cfg.applyDefaults()
			cfg.Validate()
			if cfg.IsEnabled() {
				t.Errorf("IsEnabled() = true, want false")
			}
		})
	}
}

func TestValidateDoesNothingWhenAlreadyDisabled(t *testing.T) {
	cfg := &Config{Disabled: true, Logger: discardLogger}
	cfg.applyDefaults()
	cfg.Validate() // must not panic or re-warn
	if cfg.IsEnabled() {
		t.Errorf("IsEnabled() = true, want false")
	}
}

func TestDefaults(t *testing.T) {
	t.Setenv("SPLATTY_URL", "")
	t.Setenv("SPLATTY_DSN", "")
	t.Setenv("SPLATTY_ENVIRONMENT", "")
	t.Setenv("GO_ENV", "")
	t.Setenv("APP_ENV", "")

	cfg := &Config{}
	cfg.applyDefaults()

	if got, want := cfg.URL, DefaultURL; got != want {
		t.Errorf("URL = %q, want %q", got, want)
	}
	if got, want := DefaultURL, "https://splatty.app"; got != want {
		t.Errorf("DefaultURL = %q, want %q", got, want)
	}
	if got, want := cfg.Environment, "development"; got != want {
		t.Errorf("Environment = %q, want %q", got, want)
	}
	if cfg.SendDefaultPII {
		t.Errorf("SendDefaultPII = true, want false (PII must be opt-in)")
	}
	if cfg.DisableLogs {
		t.Errorf("DisableLogs = true, want false (logs on by default)")
	}
	if cfg.ServerName == "" {
		t.Errorf("ServerName is empty, want the hostname")
	}
}

func TestConfigReadsEnvironment(t *testing.T) {
	t.Setenv("SPLATTY_URL", "https://env.example")
	t.Setenv("SPLATTY_DSN", "env-dsn")
	t.Setenv("SPLATTY_RELEASE", "env-release")
	t.Setenv("SPLATTY_ENVIRONMENT", "staging")

	cfg := &Config{}
	cfg.applyDefaults()

	if got, want := cfg.URL, "https://env.example"; got != want {
		t.Errorf("URL = %q, want %q", got, want)
	}
	if got, want := cfg.DSN, "env-dsn"; got != want {
		t.Errorf("DSN = %q, want %q", got, want)
	}
	if got, want := cfg.Release, "env-release"; got != want {
		t.Errorf("Release = %q, want %q", got, want)
	}
	if got, want := cfg.Environment, "staging"; got != want {
		t.Errorf("Environment = %q, want %q", got, want)
	}
}
