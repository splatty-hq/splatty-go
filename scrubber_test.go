package splatty

import "testing"

func scrubbed(t *testing.T, headers map[string]string, mutate ...func(*Config)) map[string]string {
	t.Helper()
	cfg := testConfig(mutate...)
	event := &Event{Request: &Request{URL: "http://example.com/y", Method: "GET", Headers: headers}}
	return NewScrubber(cfg).Scrub(event).Request.Headers
}

func TestFiltersSensitiveHeadersByDefault(t *testing.T) {
	headers := scrubbed(t, map[string]string{
		"Cookie":        "session=abc",
		"Authorization": "Bearer secret",
		"X-Csrf-Token":  "tok",
		"X-Api-Key":     "k",
		"Accept":        "text/html",
		"User-Agent":    "curl",
	})

	for _, name := range []string{"Cookie", "Authorization", "X-Csrf-Token", "X-Api-Key"} {
		if got := headers[name]; got != Filtered {
			t.Errorf("headers[%q] = %q, want %q", name, got, Filtered)
		}
	}
	if got, want := headers["Accept"], "text/html"; got != want {
		t.Errorf("headers[Accept] = %q, want %q", got, want)
	}
	if got, want := headers["User-Agent"], "curl"; got != want {
		t.Errorf("headers[User-Agent] = %q, want %q", got, want)
	}
}

func TestFiltersLowercasedHeaderNames(t *testing.T) {
	headers := scrubbed(t, map[string]string{"cookie": "a=b", "authorization": "Bearer x"})
	if headers["cookie"] != Filtered || headers["authorization"] != Filtered {
		t.Errorf("lowercase headers were not filtered: %v", headers)
	}
}

func TestPassesHeadersThroughWithSendDefaultPII(t *testing.T) {
	headers := scrubbed(t, map[string]string{"Cookie": "session=abc"},
		func(c *Config) { c.SendDefaultPII = true })
	if got, want := headers["Cookie"], "session=abc"; got != want {
		t.Errorf("headers[Cookie] = %q, want %q", got, want)
	}
}

func TestScrubToleratesEventsWithoutRequest(t *testing.T) {
	cfg := testConfig()
	event := &Event{Level: LevelError}
	if got := NewScrubber(cfg).Scrub(event); got != event {
		t.Errorf("Scrub returned a different event")
	}
	if NewScrubber(cfg).Scrub(nil) != nil {
		t.Errorf("Scrub(nil) should be nil")
	}
}

func TestScrubToleratesRequestWithoutHeaders(t *testing.T) {
	cfg := testConfig()
	event := &Event{Request: &Request{URL: "http://example.com"}}
	if got, want := NewScrubber(cfg).Scrub(event).Request.URL, "http://example.com"; got != want {
		t.Errorf("URL = %q, want %q", got, want)
	}
}
