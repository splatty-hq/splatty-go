package splatty

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSendEnvelopePostsGzippedThreeLineBody(t *testing.T) {
	rec := newRecorder(t)
	cfg := testConfig(func(c *Config) { c.URL = rec.server.URL })
	transport := NewTransport(cfg)
	defer transport.Close()

	event := &Event{EventID: strings.Repeat("deadbeef", 4), Level: LevelError}
	if err := transport.SendEnvelope(context.Background(), event); err != nil {
		t.Fatalf("SendEnvelope: %v", err)
	}

	all := rec.all()
	if len(all) != 1 {
		t.Fatalf("got %d requests, want 1", len(all))
	}
	env := all[0]

	if got, want := env.Path, "/api/envelope"; got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
	if got, want := env.Auth, "Bearer abc123"; got != want {
		t.Errorf("Authorization = %q, want %q", got, want)
	}
	if got, want := env.ContentType, "application/x-splatty-envelope"; got != want {
		t.Errorf("Content-Type = %q, want %q", got, want)
	}
	if got, want := env.ContentEncoding, "gzip"; got != want {
		t.Errorf("Content-Encoding = %q, want %q", got, want)
	}
	if got, want := env.UserAgent, SDKName+"/"+Version; got != want {
		t.Errorf("User-Agent = %q, want %q", got, want)
	}
	if got, want := env.Lines, 3; got != want {
		t.Fatalf("body has %d lines, want %d", got, want)
	}

	if got, want := env.Header["event_id"], strings.Repeat("deadbeef", 4); got != want {
		t.Errorf("header.event_id = %v, want %v", got, want)
	}
	if got, want := env.Header["dsn"], "abc123"; got != want {
		t.Errorf("header.dsn = %v, want %v", got, want)
	}
	sdk, _ := env.Header["sdk"].(map[string]any)
	if got, want := sdk["name"], SDKName; got != want {
		t.Errorf("header.sdk.name = %v, want %v", got, want)
	}
	if got, want := env.ItemHeader["type"], "event"; got != want {
		t.Errorf("item.type = %v, want %v", got, want)
	}
	if got, want := env.ItemHeader["length"], float64(len(env.Payload)); got != want {
		t.Errorf("item.length = %v, want %v", got, want)
	}
}

func TestSendLogsPostsLogItem(t *testing.T) {
	rec := newRecorder(t)
	cfg := testConfig(func(c *Config) { c.URL = rec.server.URL })
	transport := NewTransport(cfg)
	defer transport.Close()

	logs := []LogEntry{{Level: LevelInfo, Message: "hello"}}
	if err := transport.SendLogs(context.Background(), "test-host", logs); err != nil {
		t.Fatalf("SendLogs: %v", err)
	}

	env := rec.all()[0]
	if got, want := env.Path, "/api/envelope"; got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
	if got, want := env.ItemHeader["type"], "log"; got != want {
		t.Errorf("item.type = %v, want %v", got, want)
	}
	if got, want := env.ItemHeader["item_count"], float64(1); got != want {
		t.Errorf("item.item_count = %v, want %v", got, want)
	}
	if _, ok := env.Header["event_id"]; ok {
		t.Errorf("log envelopes must not carry an event_id")
	}

	var batch logBatch
	if err := json.Unmarshal(env.Payload, &batch); err != nil {
		t.Fatalf("decoding payload: %v", err)
	}
	if got, want := batch.Host, "test-host"; got != want {
		t.Errorf("host = %q, want %q", got, want)
	}
	if len(batch.Items) != 1 || batch.Items[0].Message != "hello" {
		t.Errorf("items = %+v", batch.Items)
	}
}

func TestSendLogsSkipsEmptyBatch(t *testing.T) {
	rec := newRecorder(t)
	cfg := testConfig(func(c *Config) { c.URL = rec.server.URL })
	transport := NewTransport(cfg)
	defer transport.Close()

	if err := transport.SendLogs(context.Background(), "h", nil); err != nil {
		t.Fatalf("SendLogs: %v", err)
	}
	if got := len(rec.all()); got != 0 {
		t.Errorf("sent %d requests, want 0", got)
	}
}

func TestSendEnvelopeReportsTransportFailure(t *testing.T) {
	cfg := testConfig(func(c *Config) {
		c.URL = "http://127.0.0.1:1"
		c.OpenTimeout = 200 * time.Millisecond
		c.ReadTimeout = 200 * time.Millisecond
	})
	transport := NewTransport(cfg)
	defer transport.Close()

	err := transport.SendEnvelope(context.Background(), &Event{EventID: "x"})
	if err == nil {
		t.Fatalf("want a transport error, got nil")
	}
	if !strings.Contains(err.Error(), "transport failure") {
		t.Errorf("error = %v, want a transport failure", err)
	}
}

func TestSendEnvelopeReportsBadStatus(t *testing.T) {
	rec := newRecorder(t)
	rec.server.Config.Handler = nil // fall through to the default 404 mux
	cfg := testConfig(func(c *Config) { c.URL = rec.server.URL })
	transport := NewTransport(cfg)
	defer transport.Close()

	err := transport.SendEnvelope(context.Background(), &Event{EventID: "x"})
	if err == nil || !strings.Contains(err.Error(), "unexpected status") {
		t.Errorf("error = %v, want an unexpected-status error", err)
	}
}
