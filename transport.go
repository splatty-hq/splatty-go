package splatty

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

const keepAliveTimeout = 60 * time.Second

// Transport posts envelopes to the Splatty intake endpoint.
type Transport struct {
	config *Config
	client *http.Client
	owned  bool
}

// NewTransport builds a Transport for a config, reusing cfg.HTTPClient when set.
func NewTransport(cfg *Config) *Transport {
	if cfg.HTTPClient != nil {
		return &Transport{config: cfg, client: cfg.HTTPClient}
	}
	return &Transport{
		config: cfg,
		owned:  true,
		client: &http.Client{
			Timeout: cfg.ReadTimeout,
			Transport: &http.Transport{
				Proxy: http.ProxyFromEnvironment,
				DialContext: (&net.Dialer{
					Timeout:   cfg.OpenTimeout,
					KeepAlive: keepAliveTimeout,
				}).DialContext,
				MaxIdleConns:          16,
				MaxIdleConnsPerHost:   4,
				IdleConnTimeout:       keepAliveTimeout,
				TLSHandshakeTimeout:   cfg.OpenTimeout,
				ExpectContinueTimeout: time.Second,
			},
		},
	}
}

// SendEnvelope ships a single event.
func (t *Transport) SendEnvelope(ctx context.Context, event *Event) error {
	body, err := serializeEnvelope(t.config, event)
	if err != nil {
		return err
	}
	return t.post(ctx, body)
}

// SendLogs ships a batch of log entries. An empty batch is a no-op.
func (t *Transport) SendLogs(ctx context.Context, host string, logs []LogEntry) error {
	if len(logs) == 0 {
		return nil
	}
	body, err := serializeLogEnvelope(t.config, host, logs)
	if err != nil {
		return err
	}
	return t.post(ctx, body)
}

// Close releases idle keep-alive connections.
func (t *Transport) Close() {
	if !t.owned {
		return
	}
	if tr, ok := t.client.Transport.(*http.Transport); ok {
		tr.CloseIdleConnections()
	}
}

func serializeEnvelope(cfg *Config, event *Event) ([]byte, error) {
	payload, err := json.Marshal(event)
	if err != nil {
		return nil, fmt.Errorf("splatty: marshal event: %w", err)
	}
	header := map[string]any{
		"event_id": event.EventID,
		"sent_at":  time.Now().UTC().Format(time.RFC3339Nano),
		"dsn":      cfg.DSN,
		"sdk":      SDKInfo{Name: SDKName, Version: Version},
	}
	itemHeader := map[string]any{
		"type":         "event",
		"content_type": "application/json",
		"length":       len(payload),
	}
	return joinEnvelope(header, itemHeader, payload)
}

func serializeLogEnvelope(cfg *Config, host string, logs []LogEntry) ([]byte, error) {
	payload, err := json.Marshal(map[string]any{"host": host, "items": logs})
	if err != nil {
		return nil, fmt.Errorf("splatty: marshal logs: %w", err)
	}
	header := map[string]any{
		"sent_at": time.Now().UTC().Format(time.RFC3339Nano),
		"dsn":     cfg.DSN,
		"sdk":     SDKInfo{Name: SDKName, Version: Version},
	}
	itemHeader := map[string]any{
		"type":         "log",
		"item_count":   len(logs),
		"content_type": "application/vnd.splatty.items.log+json",
		"length":       len(payload),
	}
	return joinEnvelope(header, itemHeader, payload)
}

func joinEnvelope(header, itemHeader map[string]any, payload []byte) ([]byte, error) {
	head, err := json.Marshal(header)
	if err != nil {
		return nil, err
	}
	item, err := json.Marshal(itemHeader)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	buf.Grow(len(head) + len(item) + len(payload) + 2)
	buf.Write(head)
	buf.WriteByte('\n')
	buf.Write(item)
	buf.WriteByte('\n')
	buf.Write(payload)
	return buf.Bytes(), nil
}

func (t *Transport) post(ctx context.Context, body []byte) error {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(body); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.config.EnvelopeURL(), &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-splatty-envelope")
	req.Header.Set("Authorization", "Bearer "+t.config.DSNKey())
	req.Header.Set("User-Agent", SDKName+"/"+Version)
	req.Header.Set("Content-Encoding", "gzip")

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("splatty: transport failure %s: %w", t.config.EnvelopeURL(), err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= 300 {
		return fmt.Errorf("splatty: unexpected status %d from %s", resp.StatusCode, t.config.EnvelopeURL())
	}
	return nil
}
