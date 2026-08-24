// Package splatty is the Go client for Splatty. It captures errors, panics and
// logs and ships them over the envelope protocol.
//
// Most applications install a process-wide client once at startup and use the
// package-level functions:
//
//	splatty.Init(splatty.Config{DSN: os.Getenv("SPLATTY_DSN")})
//	defer splatty.Close(context.Background())
//
//	if err := doWork(); err != nil {
//	    splatty.CaptureException(err)
//	}
package splatty

import (
	"context"
	"fmt"
	"sync"
)

var (
	currentMu sync.RWMutex
	current   *Client
)

// Init builds a Client from cfg and installs it as the process-wide client,
// replacing and closing any previous one. It never fails: a config that does
// not validate yields a disabled client that turns every capture into a no-op.
func Init(cfg Config) *Client {
	client := NewClient(cfg)

	currentMu.Lock()
	previous := current
	current = client
	currentMu.Unlock()

	if previous != nil {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), previous.config.ReadTimeout)
			defer cancel()
			_ = previous.Close(ctx)
		}()
	}
	return client
}

// CurrentClient returns the process-wide client, or nil before Init.
func CurrentClient() *Client {
	currentMu.RLock()
	defer currentMu.RUnlock()
	return current
}

// Configuration returns the process-wide client's config, or nil before Init.
func Configuration() *Config {
	if c := CurrentClient(); c != nil {
		return c.config
	}
	return nil
}

// Appender returns the process-wide log appender, or nil when logging is off.
func Appender() *LogAppender {
	if c := CurrentClient(); c != nil {
		return c.appender
	}
	return nil
}

// Enabled reports whether captures will be sent.
func Enabled() bool { return CurrentClient().Enabled() }

// CaptureException reports an error and returns the event id, or "" when the
// event was not sent. An error value is only reported once.
func CaptureException(err error, opts ...ScopeOption) string {
	return CurrentClient().captureException(err, 1, opts...)
}

// CaptureMessage reports a standalone message and returns the event id.
func CaptureMessage(message string, opts ...ScopeOption) string {
	return CurrentClient().CaptureMessage(message, opts...)
}

// CaptureLog enqueues a log record on the process-wide appender.
func CaptureLog(rec LogRecord) bool { return CurrentClient().CaptureLog(rec) }

// Flush waits for queued events and logs to be sent.
func Flush(ctx context.Context) error { return CurrentClient().Flush(ctx) }

// Close flushes everything and shuts the process-wide client down.
func Close(ctx context.Context) error {
	currentMu.Lock()
	client := current
	current = nil
	currentMu.Unlock()
	return client.Close(ctx)
}

// PanicError wraps a non-error panic value so it can travel as an error.
type PanicError struct {
	Value any
}

func (p *PanicError) Error() string { return fmt.Sprintf("%v", p.Value) }

// ExceptionType reports the panicked value's type, so the event reads
// "panic: string" rather than "*splatty.PanicError".
func (p *PanicError) ExceptionType() string { return fmt.Sprintf("panic: %T", p.Value) }

// CapturePanic reports a value obtained from recover(). Passing nil is a no-op.
func CapturePanic(recovered any, opts ...ScopeOption) string {
	if recovered == nil {
		return ""
	}
	err, ok := recovered.(error)
	if !ok {
		err = &PanicError{Value: recovered}
	}
	scoped := append([]ScopeOption{WithLevel(LevelFatal), WithTag("mechanism", "panic")}, opts...)
	return CurrentClient().captureException(err, 1, scoped...)
}

// Recover reports a panic and then re-panics, leaving Go's crash behaviour
// intact. Use it as the first deferred call in a goroutine:
//
//	go func() {
//	    defer splatty.Recover()
//	    work()
//	}()
func Recover(opts ...ScopeOption) {
	if r := recover(); r != nil {
		CapturePanic(r, opts...)
		flushBeforeCrash()
		panic(r)
	}
}

// RecoverAndContinue reports a panic and swallows it, so a worker loop keeps
// running. Prefer Recover unless the caller genuinely can carry on.
func RecoverAndContinue(opts ...ScopeOption) {
	if r := recover(); r != nil {
		CapturePanic(r, opts...)
	}
}

// flushBeforeCrash gives the event a chance to leave the process before a
// re-panic takes it down.
func flushBeforeCrash() {
	client := CurrentClient()
	if client == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), client.config.ReadTimeout)
	defer cancel()
	_ = client.Flush(ctx)
}
