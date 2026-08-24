package splatty

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
	"time"
)

func slogClient(t *testing.T) (*Client, *recorder) {
	t.Helper()
	return newTestClient(t, func(c *Config) {
		c.DisableLogs = false
		c.LogOptions = LogOptions{Host: "h", FlushInterval: time.Hour}
	})
}

func TestSlogHandlerForwardsRecords(t *testing.T) {
	client, rec := slogClient(t)
	logger := slog.New(NewSlogHandler(&SlogOptions{Client: client}))

	logger.Warn("watch out", "tenant", "acme", "status", 503)
	flush(t, client)

	entries := rec.logEntries(t)
	if len(entries) != 1 {
		t.Fatalf("shipped %d entries, want 1", len(entries))
	}
	entry := entries[0]
	if got, want := entry.Level, LevelWarn; got != want {
		t.Errorf("Level = %q, want %q", got, want)
	}
	if got, want := entry.Message, "watch out"; got != want {
		t.Errorf("Message = %q, want %q", got, want)
	}
	if got, want := entry.Status, 503; got != want {
		t.Errorf("Status = %d, want %d", got, want)
	}
	if got, want := entry.Fields["tenant"], "acme"; got != want {
		t.Errorf("Fields[tenant] = %q, want %q", got, want)
	}
	if entry.Timestamp == 0 {
		t.Errorf("Timestamp was not set")
	}
}

func TestSlogHandlerRespectsLevel(t *testing.T) {
	client, rec := slogClient(t)
	logger := slog.New(NewSlogHandler(&SlogOptions{Client: client, Level: slog.LevelWarn}))

	logger.Info("quiet")
	logger.Error("loud")
	flush(t, client)

	entries := rec.logEntries(t)
	if len(entries) != 1 || entries[0].Message != "loud" {
		t.Errorf("entries = %+v", entries)
	}
	if got, want := entries[0].Level, LevelError; got != want {
		t.Errorf("Level = %q, want %q", got, want)
	}
}

func TestSlogHandlerCarriesAttrsAndGroups(t *testing.T) {
	client, rec := slogClient(t)
	base := slog.New(NewSlogHandler(&SlogOptions{Client: client}))
	logger := base.With("service", "api").WithGroup("http").With("method", "GET")

	logger.Info("handled", slog.Int("bytes", 12))
	flush(t, client)

	entry := rec.logEntries(t)[0]
	if got, want := entry.Fields["service"], "api"; got != want {
		t.Errorf("Fields[service] = %q, want %q", got, want)
	}
	if got, want := entry.Fields["http.method"], "GET"; got != want {
		t.Errorf("Fields[http.method] = %q, want %q", got, want)
	}
	if got, want := entry.Fields["http.bytes"], "12"; got != want {
		t.Errorf("Fields[http.bytes] = %q, want %q", got, want)
	}
}

func TestSlogHandlerFlattensNestedGroups(t *testing.T) {
	client, rec := slogClient(t)
	logger := slog.New(NewSlogHandler(&SlogOptions{Client: client}))

	logger.Info("nested", slog.Group("db", slog.String("driver", "pgx"), slog.Int("rows", 3)))
	flush(t, client)

	entry := rec.logEntries(t)[0]
	if got, want := entry.Fields["db.driver"], "pgx"; got != want {
		t.Errorf("Fields[db.driver] = %q, want %q", got, want)
	}
	if got, want := entry.Fields["db.rows"], "3"; got != want {
		t.Errorf("Fields[db.rows] = %q, want %q", got, want)
	}
}

func TestSlogHandlerDisabledWhenClientIsOff(t *testing.T) {
	client, _ := newTestClient(t, func(c *Config) { c.Disabled = true })
	handler := NewSlogHandler(&SlogOptions{Client: client})
	if handler.Enabled(context.Background(), slog.LevelError) {
		t.Errorf("Enabled = true with a disabled client")
	}
}

func TestSlogHandlerTeeWritesToBoth(t *testing.T) {
	client, rec := slogClient(t)
	var buf bytes.Buffer
	primary := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})

	logger := slog.New(NewSlogHandlerTee(primary, &SlogOptions{Client: client}))
	logger.Info("both places", "k", "v")
	flush(t, client)

	if buf.Len() == 0 {
		t.Errorf("the primary handler received nothing")
	}
	if !bytes.Contains(buf.Bytes(), []byte("both places")) {
		t.Errorf("primary output = %q", buf.String())
	}
	entries := rec.logEntries(t)
	if len(entries) != 1 || entries[0].Message != "both places" {
		t.Errorf("splatty entries = %+v", entries)
	}
}

func TestSlogLevelName(t *testing.T) {
	tests := map[slog.Level]string{
		slog.LevelDebug:     LevelDebug,
		slog.LevelInfo:      LevelInfo,
		slog.LevelWarn:      LevelWarn,
		slog.LevelError:     LevelError,
		slog.LevelError + 4: LevelFatal,
	}
	for level, want := range tests {
		if got := slogLevelName(level); got != want {
			t.Errorf("slogLevelName(%v) = %q, want %q", level, got, want)
		}
	}
}
