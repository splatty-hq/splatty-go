package splatty

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCaptureExceptionReportsOnce(t *testing.T) {
	rec := newTestGlobal(t)
	err := errors.New("boom")

	first := CaptureException(err)
	second := CaptureException(err)

	if first == "" {
		t.Errorf("first capture returned no event id")
	}
	if second != "" {
		t.Errorf("second capture returned %q, want \"\"", second)
	}
	if got := len(rec.events(t)); got != 1 {
		t.Errorf("sent %d events, want 1", got)
	}
}

func TestDistinctErrorsAreBothReported(t *testing.T) {
	rec := newTestGlobal(t)
	CaptureException(errors.New("boom"))
	CaptureException(errors.New("boom"))
	if got := len(rec.events(t)); got != 2 {
		t.Errorf("sent %d events, want 2", got)
	}
}

func TestCaptureMessageIsNotDeduplicated(t *testing.T) {
	rec := newTestGlobal(t)
	CaptureMessage("hello", WithLevel(LevelInfo))
	CaptureMessage("hello", WithLevel(LevelInfo))

	events := rec.events(t)
	if len(events) != 2 {
		t.Fatalf("sent %d events, want 2", len(events))
	}
	if got, want := events[0].Message.Formatted, "hello"; got != want {
		t.Errorf("message = %q, want %q", got, want)
	}
	if got, want := events[0].Level, LevelInfo; got != want {
		t.Errorf("level = %q, want %q", got, want)
	}
}

func TestScopeOptions(t *testing.T) {
	rec := newTestGlobal(t)
	CaptureException(errors.New("boom"),
		WithLevel(LevelWarn),
		WithTransaction("POST /checkout"),
		WithTag("area", "billing"),
		WithTags(map[string]string{"tenant": "acme"}),
		WithExtra("order_id", 42),
		WithExtras(map[string]any{"cart_size": 12}),
		WithContext("app", map[string]any{"build": "1.2.3"}),
		WithRequest(&Request{URL: "http://x/y", Method: "POST"}),
	)

	event := rec.events(t)[0]
	if got, want := event.Level, LevelWarn; got != want {
		t.Errorf("Level = %q, want %q", got, want)
	}
	if got, want := event.Transaction, "POST /checkout"; got != want {
		t.Errorf("Transaction = %q, want %q", got, want)
	}
	if event.Tags["area"] != "billing" || event.Tags["tenant"] != "acme" {
		t.Errorf("Tags = %v", event.Tags)
	}
	if event.Extra["order_id"] != float64(42) || event.Extra["cart_size"] != float64(12) {
		t.Errorf("Extra = %v", event.Extra)
	}
	if _, ok := event.Contexts["app"]; !ok {
		t.Errorf("Contexts = %v, want an app block", event.Contexts)
	}
	if _, ok := event.Contexts["runtime"]; !ok {
		t.Errorf("Contexts = %v, want the runtime block", event.Contexts)
	}
	if got, want := event.Request.Method, "POST"; got != want {
		t.Errorf("Request.Method = %q, want %q", got, want)
	}
}

func TestCaptureIsNoOpWhenDisabled(t *testing.T) {
	rec := newTestGlobal(t, func(c *Config) { c.Disabled = true })

	if Enabled() {
		t.Errorf("Enabled() = true, want false")
	}
	if got := CaptureException(errors.New("boom")); got != "" {
		t.Errorf("CaptureException = %q, want \"\"", got)
	}
	if got := CaptureMessage("hi"); got != "" {
		t.Errorf("CaptureMessage = %q, want \"\"", got)
	}
	if got := len(rec.all()); got != 0 {
		t.Errorf("sent %d requests, want 0", got)
	}
}

func TestCaptureIsNoOpWithoutInit(t *testing.T) {
	currentMu.Lock()
	previous := current
	current = nil
	currentMu.Unlock()
	t.Cleanup(func() {
		currentMu.Lock()
		current = previous
		currentMu.Unlock()
	})

	if Enabled() {
		t.Errorf("Enabled() = true before Init")
	}
	if got := CaptureException(errors.New("boom")); got != "" {
		t.Errorf("CaptureException = %q, want \"\"", got)
	}
	if CaptureLog(LogRecord{Message: "x"}) {
		t.Errorf("CaptureLog = true, want false")
	}
	if err := Flush(context.Background()); err != nil {
		t.Errorf("Flush = %v, want nil", err)
	}
}

func TestCaptureNilErrorIsIgnored(t *testing.T) {
	rec := newTestGlobal(t)
	if got := CaptureException(nil); got != "" {
		t.Errorf("CaptureException(nil) = %q, want \"\"", got)
	}
	if got := len(rec.all()); got != 0 {
		t.Errorf("sent %d requests, want 0", got)
	}
}

func TestBeforeSendCanDropAnEvent(t *testing.T) {
	rec := newTestGlobal(t, func(c *Config) {
		c.BeforeSend = func(*Event) *Event { return nil }
	})
	if got := CaptureException(errors.New("boom")); got != "" {
		t.Errorf("CaptureException = %q, want \"\"", got)
	}
	if got := len(rec.all()); got != 0 {
		t.Errorf("sent %d requests, want 0", got)
	}
}

func TestBeforeSendRunsAfterScrubbing(t *testing.T) {
	rec := newTestGlobal(t, func(c *Config) {
		c.BeforeSend = func(e *Event) *Event {
			// The scrubber has already run, so the hook observes the filtered value.
			e.Tags["cookie_seen"] = e.Request.Headers["Cookie"]
			return e
		}
	})
	CaptureException(errors.New("boom"),
		WithRequest(&Request{Headers: map[string]string{"Cookie": "session=abc"}}))

	event := rec.events(t)[0]
	if got, want := event.Tags["cookie_seen"], Filtered; got != want {
		t.Errorf("beforeSend saw %q, want %q", got, want)
	}
}

func TestAsyncCaptureIsFlushed(t *testing.T) {
	rec := newRecorder(t)
	client := NewClient(Config{
		URL:         rec.server.URL,
		DSN:         "abc",
		DisableLogs: true,
		Logger:      discardLogger,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	defer client.Close(ctx)

	if id := client.CaptureException(errors.New("async boom")); id == "" {
		t.Fatalf("async capture returned no event id")
	}
	flush(t, client)

	if got := len(rec.events(t)); got != 1 {
		t.Errorf("sent %d events, want 1", got)
	}
}

func TestCloseIsIdempotentAndStopsCapturing(t *testing.T) {
	rec := newRecorder(t)
	client := NewClient(Config{
		URL:    rec.server.URL,
		DSN:    "abc",
		Logger: discardLogger,
	})

	ctx := context.Background()
	if err := client.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := client.Close(ctx); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if client.Enabled() {
		t.Errorf("Enabled() = true after Close")
	}
	if got := client.CaptureException(errors.New("boom")); got != "" {
		t.Errorf("CaptureException after Close = %q, want \"\"", got)
	}
}

func TestInitReplacesThePreviousClient(t *testing.T) {
	rec := newRecorder(t)
	currentMu.Lock()
	previous := current
	current = nil
	currentMu.Unlock()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = Close(ctx)
		currentMu.Lock()
		current = previous
		currentMu.Unlock()
	})

	first := Init(Config{URL: rec.server.URL, DSN: "one", Synchronous: true, DisableLogs: true, Logger: discardLogger})
	second := Init(Config{URL: rec.server.URL, DSN: "two", Synchronous: true, DisableLogs: true, Logger: discardLogger})

	if first == second {
		t.Fatalf("Init returned the same client twice")
	}
	if got := CurrentClient(); got != second {
		t.Errorf("CurrentClient is not the most recent client")
	}
	if got, want := Configuration().DSN, "two"; got != want {
		t.Errorf("Configuration().DSN = %q, want %q", got, want)
	}
}
