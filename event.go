package splatty

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"runtime"
	"time"
)

// SDKInfo identifies the client that produced an event.
type SDKInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Message is the payload of a message event.
type Message struct {
	Formatted string `json:"formatted"`
}

// Stacktrace holds the frames of one exception value.
type Stacktrace struct {
	Frames []Frame `json:"frames"`
}

// ExceptionValue is one link of an error chain.
type ExceptionValue struct {
	Type       string     `json:"type"`
	Value      string     `json:"value"`
	Stacktrace Stacktrace `json:"stacktrace"`
}

// ExceptionValues wraps the chain, root cause first.
type ExceptionValues struct {
	Values []ExceptionValue `json:"values"`
}

// Event is the payload posted to the server.
type Event struct {
	EventID     string            `json:"event_id"`
	Timestamp   string            `json:"timestamp"`
	Platform    string            `json:"platform"`
	Environment string            `json:"environment,omitempty"`
	Release     string            `json:"release,omitempty"`
	ServerName  string            `json:"server_name,omitempty"`
	SDK         SDKInfo           `json:"sdk"`
	Transaction string            `json:"transaction,omitempty"`
	Level       string            `json:"level"`
	Tags        map[string]string `json:"tags"`
	Extra       map[string]any    `json:"extra"`
	Contexts    map[string]any    `json:"contexts"`
	Request     *Request          `json:"request,omitempty"`
	Message     *Message          `json:"message,omitempty"`
	Exception   *ExceptionValues  `json:"exception,omitempty"`
}

const maxChainDepth = 32

func newEventID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand only fails catastrophically; a timestamp-derived id still
		// beats dropping the event.
		return hex.EncodeToString([]byte(fmt.Sprintf("%016x", time.Now().UnixNano())))
	}
	return hex.EncodeToString(buf)
}

func basePayload(cfg *Config, scope Scope) *Event {
	contexts := map[string]any{
		"runtime": map[string]any{"name": "go", "version": runtime.Version()},
	}
	for k, v := range scope.Contexts {
		contexts[k] = v
	}

	tags := scope.Tags
	if tags == nil {
		tags = map[string]string{}
	}
	extra := scope.Extra
	if extra == nil {
		extra = map[string]any{}
	}

	return &Event{
		EventID:     newEventID(),
		Timestamp:   time.Now().UTC().Format(time.RFC3339Nano),
		Platform:    "go",
		Environment: cfg.Environment,
		Release:     cfg.Release,
		ServerName:  cfg.ServerName,
		SDK:         SDKInfo{Name: SDKName, Version: Version},
		Transaction: scope.Transaction,
		Tags:        tags,
		Extra:       extra,
		Contexts:    contexts,
		Request:     scope.Request,
	}
}

// NewExceptionEvent builds an event from an error. The stack is captured at
// the call site: Go errors carry no trace of their own, so skip controls how
// many intermediate frames to drop.
func NewExceptionEvent(err error, cfg *Config, scope Scope, skip int) *Event {
	event := basePayload(cfg, scope)
	event.Level = scope.Level
	if event.Level == "" {
		event.Level = LevelError
	}
	stack := captureStack(skip + 1)
	addSourceContext(stack, cfg)
	event.Exception = &ExceptionValues{Values: exceptionChain(err, stack)}
	return event
}

// NewMessageEvent builds an event from a plain message.
func NewMessageEvent(message string, cfg *Config, scope Scope) *Event {
	event := basePayload(cfg, scope)
	event.Level = scope.Level
	if event.Level == "" {
		event.Level = LevelInfo
	}
	event.Message = &Message{Formatted: message}
	return event
}

type multiUnwrapper interface{ Unwrap() []error }

// TypedError lets an error choose how its type appears on the event, instead
// of the default %T of its concrete Go type.
type TypedError interface {
	ExceptionType() string
}

func exceptionType(err error) string {
	if t, ok := err.(TypedError); ok {
		if name := t.ExceptionType(); name != "" {
			return name
		}
	}
	return fmt.Sprintf("%T", err)
}

// exceptionChain flattens an error's wrap chain, root cause first, and hangs
// the captured stack off the outermost error.
func exceptionChain(err error, stack []Frame) []ExceptionValue {
	if err == nil {
		return []ExceptionValue{}
	}

	chain := appendChain(nil, err, 0)
	// appendChain walks outermost-first; the wire format wants root cause first.
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	if len(chain) > 0 {
		chain[len(chain)-1].Stacktrace = Stacktrace{Frames: stack}
	}
	return chain
}

func appendChain(dst []ExceptionValue, err error, depth int) []ExceptionValue {
	// Depth-bounded rather than identity-tracked: comparing arbitrary error
	// values can panic on uncomparable dynamic types.
	if err == nil || depth >= maxChainDepth {
		return dst
	}
	dst = append(dst, ExceptionValue{
		Type:       exceptionType(err),
		Value:      err.Error(),
		Stacktrace: Stacktrace{Frames: []Frame{}},
	})

	switch u := err.(type) {
	case interface{ Unwrap() error }:
		return appendChain(dst, u.Unwrap(), depth+1)
	case multiUnwrapper:
		for _, inner := range u.Unwrap() {
			dst = appendChain(dst, inner, depth+1)
		}
	}
	return dst
}
