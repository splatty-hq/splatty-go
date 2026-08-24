package splatty

import (
	"errors"
	"strings"
	"testing"
)

func TestEncodeArgs(t *testing.T) {
	if got := EncodeArgs(nil); got != "" {
		t.Errorf("EncodeArgs(nil) = %q, want \"\"", got)
	}
	if got, want := EncodeArgs([]any{1, "two"}), `[1,"two"]`; got != want {
		t.Errorf("EncodeArgs = %q, want %q", got, want)
	}
	if got, want := EncodeArgs(map[string]string{"to": "x@example.com"}), `{"to":"x@example.com"}`; got != want {
		t.Errorf("EncodeArgs = %q, want %q", got, want)
	}
}

func TestEncodeArgsTruncates(t *testing.T) {
	encoded := EncodeArgs([]string{strings.Repeat("x", 4000)})
	if got, want := len(encoded), MaxArgsLength+len("...(truncated)"); got != want {
		t.Errorf("len = %d, want %d", got, want)
	}
	if !strings.HasSuffix(encoded, "...(truncated)") {
		t.Errorf("encoded does not end with the truncation marker")
	}
}

func TestEncodeArgsReturnsEmptyForUnserializable(t *testing.T) {
	if got := EncodeArgs(make(chan int)); got != "" {
		t.Errorf("EncodeArgs(chan) = %q, want \"\"", got)
	}
	if got := EncodeArgs(func() {}); got != "" {
		t.Errorf("EncodeArgs(func) = %q, want \"\"", got)
	}
}

func TestJobScope(t *testing.T) {
	scope := JobScope(JobContext{
		Backend:  "asynq",
		JobClass: "email:deliver",
		Queue:    "critical",
		JobID:    "j-1",
		Attempts: 3,
		Args:     map[string]any{"to": "x@example.com"},
		Extra:    map[string]any{"deadline": "2026-01-01"},
	})

	if got, want := scope.Tags["job_backend"], "asynq"; got != want {
		t.Errorf("job_backend = %q, want %q", got, want)
	}
	if got, want := scope.Tags["job_class"], "email:deliver"; got != want {
		t.Errorf("job_class = %q, want %q", got, want)
	}
	if got, want := scope.Tags["job_queue"], "critical"; got != want {
		t.Errorf("job_queue = %q, want %q", got, want)
	}
	if got, want := scope.Transaction, "email:deliver"; got != want {
		t.Errorf("Transaction = %q, want %q", got, want)
	}
	if got, want := scope.Extra["job_id"], "j-1"; got != want {
		t.Errorf("job_id = %v, want %v", got, want)
	}
	if got, want := scope.Extra["job_attempts"], 3; got != want {
		t.Errorf("job_attempts = %v, want %v", got, want)
	}
	if got, want := scope.Extra["job_args"], `{"to":"x@example.com"}`; got != want {
		t.Errorf("job_args = %v, want %v", got, want)
	}
	if got, want := scope.Extra["deadline"], "2026-01-01"; got != want {
		t.Errorf("deadline = %v, want %v", got, want)
	}
}

func TestCaptureJobException(t *testing.T) {
	rec := newTestGlobal(t)
	CaptureJobException(errors.New("smtp down"), JobContext{
		Backend:  "asynq",
		JobClass: "email:deliver",
		Queue:    "critical",
		JobID:    "j-1",
		Attempts: 3,
		Args:     []any{1, "two"},
	})

	event := rec.events(t)[0]
	if got, want := event.Exception.Values[0].Value, "smtp down"; got != want {
		t.Errorf("Value = %q, want %q", got, want)
	}
	if got, want := event.Tags["job_backend"], "asynq"; got != want {
		t.Errorf("job_backend = %q, want %q", got, want)
	}
	if got, want := event.Transaction, "email:deliver"; got != want {
		t.Errorf("Transaction = %q, want %q", got, want)
	}
	if got, want := event.Extra["job_args"], `[1,"two"]`; got != want {
		t.Errorf("job_args = %v, want %v", got, want)
	}
}

func TestCaptureJobExceptionWithoutJobDetails(t *testing.T) {
	rec := newTestGlobal(t)
	CaptureJobException(errors.New("redis gone"), JobContext{Backend: "asynq"})

	event := rec.events(t)[0]
	if got, want := event.Tags["job_backend"], "asynq"; got != want {
		t.Errorf("job_backend = %q, want %q", got, want)
	}
	if _, ok := event.Tags["job_class"]; ok {
		t.Errorf("job_class was set without a job class")
	}
	if event.Transaction != "" {
		t.Errorf("Transaction = %q, want empty", event.Transaction)
	}
}

func TestCaptureJobExceptionReportsOnce(t *testing.T) {
	rec := newTestGlobal(t)
	err := errors.New("boom")
	CaptureJobException(err, JobContext{Backend: "asynq"})
	CaptureJobException(err, JobContext{Backend: "asynq"})

	if got := len(rec.events(t)); got != 1 {
		t.Errorf("sent %d events, want 1", got)
	}
}

func TestCaptureJobExceptionExtraOptionsWin(t *testing.T) {
	rec := newTestGlobal(t)
	CaptureJobException(errors.New("boom"),
		JobContext{Backend: "asynq", JobClass: "email:deliver"},
		WithTransaction("custom"),
		WithTag("shard", "eu-1"),
	)

	event := rec.events(t)[0]
	if got, want := event.Transaction, "custom"; got != want {
		t.Errorf("Transaction = %q, want %q", got, want)
	}
	if got, want := event.Tags["shard"], "eu-1"; got != want {
		t.Errorf("Tags[shard] = %q, want %q", got, want)
	}
	if got, want := event.Tags["job_backend"], "asynq"; got != want {
		t.Errorf("Tags[job_backend] = %q, want %q", got, want)
	}
}
