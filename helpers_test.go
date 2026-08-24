package splatty

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type capturedEnvelope struct {
	Path            string
	Auth            string
	ContentType     string
	ContentEncoding string
	UserAgent       string
	Header          map[string]any
	ItemHeader      map[string]any
	Payload         []byte
	Lines           int
}

type recorder struct {
	server *httptest.Server

	mu        sync.Mutex
	envelopes []capturedEnvelope
}

func newRecorder(t *testing.T) *recorder {
	t.Helper()
	r := &recorder{}
	r.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Errorf("reading body: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		captured := capturedEnvelope{
			Path:            req.URL.Path,
			Auth:            req.Header.Get("Authorization"),
			ContentType:     req.Header.Get("Content-Type"),
			ContentEncoding: req.Header.Get("Content-Encoding"),
			UserAgent:       req.Header.Get("User-Agent"),
		}

		decoded := body
		if captured.ContentEncoding == "gzip" {
			gz, err := gzip.NewReader(bytes.NewReader(body))
			if err != nil {
				t.Errorf("gunzip: %v", err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			decoded, _ = io.ReadAll(gz)
			_ = gz.Close()
		}

		lines := bytes.SplitN(decoded, []byte("\n"), 3)
		captured.Lines = len(lines)
		if len(lines) == 3 {
			_ = json.Unmarshal(lines[0], &captured.Header)
			_ = json.Unmarshal(lines[1], &captured.ItemHeader)
			captured.Payload = lines[2]
		}

		r.mu.Lock()
		r.envelopes = append(r.envelopes, captured)
		r.mu.Unlock()

		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(r.server.Close)
	return r
}

func (r *recorder) all() []capturedEnvelope {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]capturedEnvelope, len(r.envelopes))
	copy(out, r.envelopes)
	return out
}

func (r *recorder) events(t *testing.T) []*Event {
	t.Helper()
	var out []*Event
	for _, env := range r.all() {
		if env.ItemHeader["type"] != "event" {
			continue
		}
		var event Event
		if err := json.Unmarshal(env.Payload, &event); err != nil {
			t.Fatalf("decoding event: %v", err)
		}
		out = append(out, &event)
	}
	return out
}

type logBatch struct {
	Host  string     `json:"host"`
	Items []LogEntry `json:"items"`
}

func (r *recorder) logBatches(t *testing.T) []logBatch {
	t.Helper()
	var out []logBatch
	for _, env := range r.all() {
		if env.ItemHeader["type"] != "log" {
			continue
		}
		var batch logBatch
		if err := json.Unmarshal(env.Payload, &batch); err != nil {
			t.Fatalf("decoding log batch: %v", err)
		}
		out = append(out, batch)
	}
	return out
}

func (r *recorder) logEntries(t *testing.T) []LogEntry {
	t.Helper()
	var out []LogEntry
	for _, batch := range r.logBatches(t) {
		out = append(out, batch.Items...)
	}
	return out
}

var discardLogger = log.New(io.Discard, "", 0)

// newTestClient boots a synchronous client pointed at a recording server.
func newTestClient(t *testing.T, mutate ...func(*Config)) (*Client, *recorder) {
	t.Helper()
	rec := newRecorder(t)

	cfg := Config{
		URL:         rec.server.URL,
		DSN:         "abc",
		Environment: "test",
		Release:     "0.0.1",
		Synchronous: true,
		DisableLogs: true,
		Logger:      discardLogger,
	}
	for _, m := range mutate {
		m(&cfg)
	}

	client := NewClient(cfg)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = client.Close(ctx)
	})
	return client, rec
}

// newTestGlobal installs a test client as the process-wide client.
func newTestGlobal(t *testing.T, mutate ...func(*Config)) *recorder {
	t.Helper()
	client, rec := newTestClient(t, mutate...)

	currentMu.Lock()
	previous := current
	current = client
	currentMu.Unlock()

	t.Cleanup(func() {
		currentMu.Lock()
		current = previous
		currentMu.Unlock()
	})
	return rec
}

func testConfig(mutate ...func(*Config)) *Config {
	cfg := &Config{
		URL:         "http://localhost:3000",
		DSN:         "abc123",
		Environment: "test",
		Release:     "0.0.1",
		Logger:      discardLogger,
	}
	for _, m := range mutate {
		m(cfg)
	}
	cfg.applyDefaults()
	cfg.Validate()
	return cfg
}

func flush(t *testing.T, c *Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Flush(ctx); err != nil {
		t.Fatalf("flush: %v", err)
	}
}
