package splatty

import (
	"context"
	"testing"
	"time"
)

func logClient(t *testing.T, mutate ...func(*Config)) (*Client, *recorder) {
	t.Helper()
	all := append([]func(*Config){func(c *Config) {
		c.DisableLogs = false
		c.LogOptions = LogOptions{Host: "h-1", FlushInterval: time.Hour}
	}}, mutate...)
	return newTestClient(t, all...)
}

func TestAppenderEnqueuesAndDispatches(t *testing.T) {
	client, rec := logClient(t, func(c *Config) {
		c.LogOptions = LogOptions{Host: "h-1", FlushInterval: time.Hour, Level: LevelInfo, BatchSize: 10}
	})

	ok := client.CaptureLog(LogRecord{
		Time:    time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC),
		Level:   "info",
		Message: "hi",
		Fields: map[string]any{
			"request_id":  "rid",
			"method":      "GET",
			"path":        "/x",
			"status":      200,
			"duration_ms": 1.5,
			"user":        "u",
		},
	})
	if !ok {
		t.Fatalf("CaptureLog returned false")
	}
	flush(t, client)

	batches := rec.logBatches(t)
	if len(batches) != 1 {
		t.Fatalf("got %d batches, want 1", len(batches))
	}
	if got, want := batches[0].Host, "h-1"; got != want {
		t.Errorf("host = %q, want %q", got, want)
	}

	entry := batches[0].Items[0]
	if got, want := entry.Message, "hi"; got != want {
		t.Errorf("Message = %q, want %q", got, want)
	}
	if got, want := entry.Level, LevelInfo; got != want {
		t.Errorf("Level = %q, want %q", got, want)
	}
	if got, want := entry.RequestID, "rid"; got != want {
		t.Errorf("RequestID = %q, want %q", got, want)
	}
	if got, want := entry.Method, "GET"; got != want {
		t.Errorf("Method = %q, want %q", got, want)
	}
	if got, want := entry.Path, "/x"; got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
	if got, want := entry.Status, 200; got != want {
		t.Errorf("Status = %d, want %d", got, want)
	}
	if entry.DurationMS == nil || *entry.DurationMS != 1.5 {
		t.Errorf("DurationMS = %v, want 1.5", entry.DurationMS)
	}
	if got, want := entry.Environment, "test"; got != want {
		t.Errorf("Environment = %q, want %q", got, want)
	}
	if got, want := entry.Release, "0.0.1"; got != want {
		t.Errorf("Release = %q, want %q", got, want)
	}
	if got, want := entry.Timestamp, time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC).UnixMilli(); got != want {
		t.Errorf("Timestamp = %d, want %d", got, want)
	}
	if len(entry.Fields) != 1 || entry.Fields["user"] != "u" {
		t.Errorf("Fields = %v, want only the unpromoted user field", entry.Fields)
	}
}

func TestAppenderSkipsWhenDisabled(t *testing.T) {
	client, rec := logClient(t, func(c *Config) { c.Disabled = true })
	if client.CaptureLog(LogRecord{Level: "info", Message: "hi"}) {
		t.Errorf("CaptureLog = true, want false when disabled")
	}
	flush(t, client)
	if got := len(rec.all()); got != 0 {
		t.Errorf("sent %d requests, want 0", got)
	}
}

func TestAppenderDropsIntakePaths(t *testing.T) {
	client, rec := logClient(t)

	for _, path := range []string{"/api/4/logs", "/api/42/metrics", "/api/1/envelope/", "/api/envelope", "/api/logs"} {
		if client.CaptureLog(LogRecord{Level: "info", Message: "req", Fields: map[string]any{"path": path}}) {
			t.Errorf("path %q was shipped, want it dropped", path)
		}
	}
	if !client.CaptureLog(LogRecord{Level: "info", Message: "real", Fields: map[string]any{"path": "/users/42"}}) {
		t.Fatalf("a real request path was dropped")
	}
	flush(t, client)

	entries := rec.logEntries(t)
	if len(entries) != 1 {
		t.Fatalf("shipped %d entries, want 1", len(entries))
	}
	if got, want := entries[0].Path, "/users/42"; got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
}

func TestAppenderInlinesSQL(t *testing.T) {
	client, rec := logClient(t)
	client.CaptureLog(LogRecord{Level: "debug", Message: "Load", Fields: map[string]any{"sql": "SELECT 1"}})
	client.CaptureLog(LogRecord{Level: "debug", Message: "", Fields: map[string]any{"sql": "SELECT 2"}})
	flush(t, client)

	entries := rec.logEntries(t)
	if len(entries) != 2 {
		t.Fatalf("shipped %d entries, want 2", len(entries))
	}
	if got, want := entries[0].Message, "Load — SELECT 1"; got != want {
		t.Errorf("Message = %q, want %q", got, want)
	}
	if got, want := entries[1].Message, "SELECT 2"; got != want {
		t.Errorf("Message = %q, want %q", got, want)
	}
}

func TestAppenderHonoursMinimumLevel(t *testing.T) {
	client, rec := logClient(t, func(c *Config) {
		c.LogOptions = LogOptions{Host: "h", FlushInterval: time.Hour, Level: LevelWarn}
	})

	if client.CaptureLog(LogRecord{Level: "info", Message: "quiet"}) {
		t.Errorf("an info entry passed a warn threshold")
	}
	if !client.CaptureLog(LogRecord{Level: "error", Message: "loud"}) {
		t.Errorf("an error entry was dropped by a warn threshold")
	}
	flush(t, client)

	entries := rec.logEntries(t)
	if len(entries) != 1 || entries[0].Message != "loud" {
		t.Errorf("entries = %+v", entries)
	}
}

func TestAppenderDropsOldestPastTheQueueLimit(t *testing.T) {
	client, rec := logClient(t, func(c *Config) {
		c.LogOptions = LogOptions{Host: "h", FlushInterval: time.Hour, QueueLimit: 2, BatchSize: 1000}
	})

	for _, msg := range []string{"one", "two", "three"} {
		client.CaptureLog(LogRecord{Level: "info", Message: msg})
	}
	flush(t, client)

	var messages []string
	for _, e := range rec.logEntries(t) {
		messages = append(messages, e.Message)
	}
	if len(messages) != 2 || messages[0] != "two" || messages[1] != "three" {
		t.Errorf("messages = %v, want [two three]", messages)
	}
}

func TestAppenderFlushesOnTheInterval(t *testing.T) {
	client, rec := logClient(t, func(c *Config) {
		c.LogOptions = LogOptions{Host: "h", FlushInterval: 20 * time.Millisecond, BatchSize: 1000}
	})

	client.CaptureLog(LogRecord{Level: "info", Message: "tick"})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(rec.logEntries(t)) > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the interval flush never shipped the entry")
}

func TestAppenderFlushesOnceTheBatchIsFull(t *testing.T) {
	client, rec := logClient(t, func(c *Config) {
		c.LogOptions = LogOptions{Host: "h", FlushInterval: time.Hour, BatchSize: 3}
	})

	for i := 0; i < 3; i++ {
		client.CaptureLog(LogRecord{Level: "info", Message: "entry"})
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(rec.logEntries(t)) == 3 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("a full batch was not shipped, got %d entries", len(rec.logEntries(t)))
}

func TestCloseShipsTheFinalBatch(t *testing.T) {
	rec := newRecorder(t)
	client := NewClient(Config{
		URL:         rec.server.URL,
		DSN:         "abc",
		Synchronous: true,
		LogOptions:  LogOptions{Host: "h", FlushInterval: time.Hour},
		Logger:      discardLogger,
	})
	client.CaptureLog(LogRecord{Level: "info", Message: "last words"})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	entries := rec.logEntries(t)
	if len(entries) != 1 || entries[0].Message != "last words" {
		t.Errorf("entries = %+v", entries)
	}
}

func TestNoAppenderWhenLogsDisabled(t *testing.T) {
	client, _ := newTestClient(t)
	if client.Appender() != nil {
		t.Errorf("an appender was installed despite DisableLogs")
	}
	if client.CaptureLog(LogRecord{Level: "info", Message: "dropped"}) {
		t.Errorf("CaptureLog = true, want false")
	}
}

func TestMapLevel(t *testing.T) {
	tests := map[string]string{
		"trace": LevelDebug, "DEBUG": LevelDebug, "verbose": LevelDebug,
		"info": LevelInfo, "notice": LevelInfo, "": LevelInfo, "weird": LevelInfo,
		"warn": LevelWarn, "WARNING": LevelWarn,
		"error": LevelError, "err": LevelError,
		"fatal": LevelFatal, "panic": LevelFatal, "critical": LevelFatal,
	}
	for input, want := range tests {
		if got := MapLevel(input); got != want {
			t.Errorf("MapLevel(%q) = %q, want %q", input, got, want)
		}
	}
}
