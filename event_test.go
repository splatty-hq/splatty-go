package splatty

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
)

var hexID = regexp.MustCompile(`^[a-f0-9]{32}$`)

func failing() error {
	return errors.New("boom")
}

func TestNewExceptionEventBuildsPayload(t *testing.T) {
	cfg := testConfig()
	event := NewExceptionEvent(failing(), cfg, Scope{}, 0)

	if !hexID.MatchString(event.EventID) {
		t.Errorf("EventID = %q, want 32 hex chars", event.EventID)
	}
	if got, want := event.Platform, "go"; got != want {
		t.Errorf("Platform = %q, want %q", got, want)
	}
	if got, want := event.Environment, "test"; got != want {
		t.Errorf("Environment = %q, want %q", got, want)
	}
	if got, want := event.Release, "0.0.1"; got != want {
		t.Errorf("Release = %q, want %q", got, want)
	}
	if got, want := event.Level, LevelError; got != want {
		t.Errorf("Level = %q, want %q", got, want)
	}
	if event.Exception == nil || len(event.Exception.Values) != 1 {
		t.Fatalf("Exception = %+v, want one value", event.Exception)
	}

	value := event.Exception.Values[0]
	if got, want := value.Value, "boom"; got != want {
		t.Errorf("Value = %q, want %q", got, want)
	}
	if !strings.Contains(value.Type, "errorString") {
		t.Errorf("Type = %q, want the concrete Go type", value.Type)
	}
	if len(value.Stacktrace.Frames) == 0 {
		t.Fatalf("no stack frames captured")
	}

	var sawInApp bool
	for _, frame := range value.Stacktrace.Frames {
		if frame.InApp {
			sawInApp = true
		}
		if frame.Lineno <= 0 {
			t.Errorf("frame %q has no line number", frame.Function)
		}
	}
	if !sawInApp {
		t.Errorf("no in_app frames; frames = %+v", value.Stacktrace.Frames)
	}

	// The innermost frame is this test, and it must come last (oldest first).
	last := value.Stacktrace.Frames[len(value.Stacktrace.Frames)-1]
	if !strings.Contains(last.Function, "TestNewExceptionEventBuildsPayload") {
		t.Errorf("last frame = %q, want the calling test", last.Function)
	}
}

func TestExceptionChainIsRootCauseFirst(t *testing.T) {
	root := errors.New("root")
	wrapped := fmt.Errorf("wrapped: %w", root)
	event := NewExceptionEvent(wrapped, testConfig(), Scope{}, 0)

	values := event.Exception.Values
	if len(values) != 2 {
		t.Fatalf("got %d values, want 2", len(values))
	}
	if got, want := values[0].Value, "root"; got != want {
		t.Errorf("values[0] = %q, want %q", got, want)
	}
	if got, want := values[1].Value, "wrapped: root"; got != want {
		t.Errorf("values[1] = %q, want %q", got, want)
	}
	if len(values[0].Stacktrace.Frames) != 0 {
		t.Errorf("inner error should carry no frames")
	}
	if len(values[1].Stacktrace.Frames) == 0 {
		t.Errorf("outermost error should carry the captured stack")
	}
}

func TestExceptionChainFlattensJoinedErrors(t *testing.T) {
	joined := errors.Join(errors.New("first"), errors.New("second"))
	event := NewExceptionEvent(joined, testConfig(), Scope{}, 0)

	var values []string
	for _, v := range event.Exception.Values {
		values = append(values, v.Value)
	}
	if len(values) != 3 {
		t.Fatalf("got %v, want the join plus both branches", values)
	}
}

type customError struct{ msg string }

func (c *customError) Error() string         { return c.msg }
func (c *customError) ExceptionType() string { return "billing.DeclinedError" }

func TestTypedErrorOverridesTheReportedType(t *testing.T) {
	event := NewExceptionEvent(&customError{msg: "card declined"}, testConfig(), Scope{}, 0)
	if got, want := event.Exception.Values[0].Type, "billing.DeclinedError"; got != want {
		t.Errorf("Type = %q, want %q", got, want)
	}
}

func TestPanicErrorReportsThePanickedType(t *testing.T) {
	event := NewExceptionEvent(&PanicError{Value: "kaboom"}, testConfig(), Scope{}, 0)
	if got, want := event.Exception.Values[0].Type, "panic: string"; got != want {
		t.Errorf("Type = %q, want %q", got, want)
	}
	if got, want := event.Exception.Values[0].Value, "kaboom"; got != want {
		t.Errorf("Value = %q, want %q", got, want)
	}
}

func TestNewMessageEvent(t *testing.T) {
	event := NewMessageEvent("hi there", testConfig(), Scope{
		Level: LevelWarn,
		Tags:  map[string]string{"service": "api"},
	})

	if got, want := event.Level, LevelWarn; got != want {
		t.Errorf("Level = %q, want %q", got, want)
	}
	if event.Message == nil || event.Message.Formatted != "hi there" {
		t.Errorf("Message = %+v", event.Message)
	}
	if got, want := event.Tags["service"], "api"; got != want {
		t.Errorf("Tags[service] = %q, want %q", got, want)
	}
	if event.Exception != nil {
		t.Errorf("message events must carry no exception")
	}
}

func TestScopeRequestAndTransactionPassThrough(t *testing.T) {
	event := NewMessageEvent("x", testConfig(), Scope{
		Request:     &Request{URL: "/x", Method: "GET"},
		Transaction: "GET /x",
	})
	if got, want := event.Request.URL, "/x"; got != want {
		t.Errorf("Request.URL = %q, want %q", got, want)
	}
	if got, want := event.Transaction, "GET /x"; got != want {
		t.Errorf("Transaction = %q, want %q", got, want)
	}
}

func TestEmptyScopeYieldsEmptyMaps(t *testing.T) {
	event := NewMessageEvent("x", testConfig(), Scope{})
	if event.Tags == nil || len(event.Tags) != 0 {
		t.Errorf("Tags = %v, want an empty map", event.Tags)
	}
	if event.Extra == nil || len(event.Extra) != 0 {
		t.Errorf("Extra = %v, want an empty map", event.Extra)
	}
	if event.Transaction != "" || event.Request != nil {
		t.Errorf("unset scope fields must stay empty")
	}
}

func TestIsStdlib(t *testing.T) {
	tests := map[string]bool{
		"net/http.(*conn).serve": true,
		"runtime.goexit":         true,
		"main.main":              true,
		"github.com/splatty-hq/splatty-go.CaptureError": false,
		"github.com/k0va1/app/internal/svc.(*S).Do":     false,
	}
	for fn, want := range tests {
		if got := isStdlib(fn); got != want {
			t.Errorf("isStdlib(%q) = %v, want %v", fn, got, want)
		}
	}
}
