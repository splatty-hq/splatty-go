package splatty

import (
	"context"
	"sync"
)

// Client captures events and ships them to Splatty. It is safe for concurrent
// use. Most applications use the package-level functions instead, which
// delegate to a process-wide Client installed by Init.
type Client struct {
	config    *Config
	transport *Transport
	scrubber  *Scrubber
	appender  *LogAppender
	dedupe    *dedupeSet

	mu     sync.RWMutex
	closed bool
	queue  chan task
	wg     sync.WaitGroup
}

type task struct {
	event *Event
	done  chan struct{}
}

// NewClient validates the config and starts the background sender. A config
// that fails validation yields a disabled client rather than an error.
func NewClient(cfg Config) *Client {
	cfg.applyDefaults()
	cfg.Validate()

	c := &Client{
		config:   &cfg,
		scrubber: NewScrubber(&cfg),
		dedupe:   newDedupeSet(dedupeLimit),
	}
	c.transport = NewTransport(&cfg)

	if !cfg.Synchronous {
		c.queue = make(chan task, cfg.QueueSize)
		c.wg.Add(1)
		go c.run()
	}
	if cfg.IsEnabled() && !cfg.DisableLogs {
		c.appender = newLogAppender(c, cfg.LogOptions)
	}
	return c
}

// Config returns the effective configuration.
func (c *Client) Config() *Config { return c.config }

// Transport returns the underlying transport.
func (c *Client) Transport() *Transport { return c.transport }

// Appender returns the log appender, or nil when logging is disabled.
func (c *Client) Appender() *LogAppender { return c.appender }

// Enabled reports whether captures will be sent.
func (c *Client) Enabled() bool {
	if c == nil {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return !c.closed && c.config.IsEnabled()
}

// CaptureException reports an error and returns the event id, or "" when the
// event was not sent. The stack is captured at the call site.
func (c *Client) CaptureException(err error, opts ...ScopeOption) string {
	return c.captureException(err, 1, opts...)
}

func (c *Client) captureException(err error, skip int, opts ...ScopeOption) string {
	if !c.Enabled() || err == nil {
		return ""
	}
	if !c.dedupe.markIfNew(err) {
		return ""
	}
	event := NewExceptionEvent(err, c.config, buildScope(opts), skip+1)
	return c.dispatch(event)
}

// CaptureMessage reports a standalone message and returns the event id.
func (c *Client) CaptureMessage(message string, opts ...ScopeOption) string {
	if !c.Enabled() {
		return ""
	}
	return c.dispatch(NewMessageEvent(message, c.config, buildScope(opts)))
}

// CaptureLog enqueues a log record. It reports false when the entry was dropped.
func (c *Client) CaptureLog(rec LogRecord) bool {
	if c == nil || c.appender == nil {
		return false
	}
	return c.appender.Log(rec)
}

func (c *Client) dispatch(event *Event) string {
	event = c.scrubber.Scrub(event)
	if hook := c.config.BeforeSend; hook != nil {
		event = hook(event)
		if event == nil {
			return ""
		}
	}

	if c.config.Synchronous {
		ctx, cancel := context.WithTimeout(context.Background(), c.config.ReadTimeout)
		defer cancel()
		if err := c.transport.SendEnvelope(ctx, event); err != nil {
			c.config.warn(err.Error())
			return ""
		}
		return event.EventID
	}

	if !c.enqueue(task{event: event}) {
		// The backlog is full. Dropping beats blocking the caller's hot path.
		return ""
	}
	return event.EventID
}

func (c *Client) enqueue(t task) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed || c.queue == nil {
		return false
	}
	select {
	case c.queue <- t:
		return true
	default:
		return false
	}
}

func (c *Client) enqueueBlocking(ctx context.Context, t task) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed || c.queue == nil {
		return false
	}
	select {
	case c.queue <- t:
		return true
	case <-ctx.Done():
		return false
	}
}

func (c *Client) run() {
	defer c.wg.Done()
	for t := range c.queue {
		if t.event == nil {
			if t.done != nil {
				close(t.done)
			}
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), c.config.ReadTimeout)
		if err := c.transport.SendEnvelope(ctx, t.event); err != nil {
			c.config.warn(err.Error())
		}
		cancel()
	}
}

// Flush waits for queued events and log entries to be sent.
func (c *Client) Flush(ctx context.Context) error {
	if c == nil {
		return nil
	}
	if !c.config.Synchronous {
		done := make(chan struct{})
		if c.enqueueBlocking(ctx, task{done: done}) {
			select {
			case <-done:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return c.appender.Flush(ctx)
}

// Close flushes everything, stops the background workers and releases
// connections. The client is unusable afterwards.
func (c *Client) Close(ctx context.Context) error {
	if c == nil {
		return nil
	}

	err := c.Flush(ctx)
	if appErr := c.appender.Close(ctx); err == nil {
		err = appErr
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return err
	}
	c.closed = true
	queue := c.queue
	c.mu.Unlock()

	if queue != nil {
		close(queue)
	}
	c.wg.Wait()
	c.transport.Close()
	return err
}
