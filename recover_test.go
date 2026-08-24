package splatty

import (
	"errors"
	"testing"
)

func TestCapturePanic(t *testing.T) {
	rec := newTestGlobal(t)

	if got := CapturePanic(nil); got != "" {
		t.Errorf("CapturePanic(nil) = %q, want \"\"", got)
	}
	if got := CapturePanic("kaboom"); got == "" {
		t.Errorf("CapturePanic returned no event id")
	}

	event := rec.events(t)[0]
	if got, want := event.Level, LevelFatal; got != want {
		t.Errorf("Level = %q, want %q", got, want)
	}
	if got, want := event.Tags["mechanism"], "panic"; got != want {
		t.Errorf("Tags[mechanism] = %q, want %q", got, want)
	}
	if got, want := event.Exception.Values[0].Type, "panic: string"; got != want {
		t.Errorf("Type = %q, want %q", got, want)
	}
	if got, want := event.Exception.Values[0].Value, "kaboom"; got != want {
		t.Errorf("Value = %q, want %q", got, want)
	}
}

func TestCapturePanicKeepsErrorType(t *testing.T) {
	rec := newTestGlobal(t)
	CapturePanic(errors.New("real error"))

	value := rec.events(t)[0].Exception.Values[0]
	if got, want := value.Value, "real error"; got != want {
		t.Errorf("Value = %q, want %q", got, want)
	}
	if got := value.Type; got == "panic: string" {
		t.Errorf("Type = %q, want the error's own type", got)
	}
}

func TestRecoverReportsAndRepanics(t *testing.T) {
	rec := newTestGlobal(t)

	var recovered any
	func() {
		defer func() { recovered = recover() }()
		func() {
			defer Recover()
			panic("goroutine died")
		}()
	}()

	if recovered != "goroutine died" {
		t.Errorf("recovered = %v, want the original panic re-raised", recovered)
	}
	events := rec.events(t)
	if len(events) != 1 {
		t.Fatalf("sent %d events, want 1", len(events))
	}
	if got, want := events[0].Exception.Values[0].Value, "goroutine died"; got != want {
		t.Errorf("Value = %q, want %q", got, want)
	}
}

func TestRecoverAndContinueSwallows(t *testing.T) {
	rec := newTestGlobal(t)

	var reached bool
	func() {
		defer func() { reached = true }()
		defer RecoverAndContinue(WithTag("worker", "w-1"))
		panic("worker died")
	}()

	if !reached {
		t.Errorf("RecoverAndContinue did not swallow the panic")
	}
	event := rec.events(t)[0]
	if got, want := event.Tags["worker"], "w-1"; got != want {
		t.Errorf("Tags[worker] = %q, want %q", got, want)
	}
}

func TestRecoverIsANoOpWithoutAPanic(t *testing.T) {
	rec := newTestGlobal(t)
	func() { defer Recover() }()
	func() { defer RecoverAndContinue() }()
	if got := len(rec.all()); got != 0 {
		t.Errorf("sent %d events without a panic, want 0", got)
	}
}
