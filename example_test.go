package splatty_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"time"

	splatty "github.com/splatty-hq/splatty-go"
)

func ExampleInit() {
	splatty.Init(splatty.Config{
		DSN:         os.Getenv("SPLATTY_DSN"),
		Environment: "production",
		Release:     os.Getenv("SPLATTY_RELEASE"),
	})
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = splatty.Close(ctx)
	}()

	if err := errors.New("something broke"); err != nil {
		splatty.CaptureException(err)
	}
}

func ExampleCaptureException_scope() {
	err := errors.New("card declined")

	splatty.CaptureException(err,
		splatty.WithLevel(splatty.LevelWarn),
		splatty.WithTransaction("POST /checkout"),
		splatty.WithTag("area", "billing"),
		splatty.WithExtra("order_id", 4711),
	)
}

func ExampleMiddleware() {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	// Panics are reported with the request that caused them, then re-raised.
	_ = http.ListenAndServe(":8080", splatty.Middleware(mux))
}

func ExampleRecover() {
	go func() {
		defer splatty.Recover()
		panic("background work failed")
	}()
}

func ExampleNewSlogHandlerTee() {
	slog.SetDefault(slog.New(splatty.NewSlogHandlerTee(
		slog.NewJSONHandler(os.Stdout, nil),
		&splatty.SlogOptions{Level: slog.LevelInfo},
	)))

	slog.Info("checkout completed", "path", "/checkout", "status", 200, "duration_ms", 42)
}

func ExampleCaptureJobException() {
	err := errors.New("smtp timeout")

	splatty.CaptureJobException(err, splatty.JobContext{
		Backend:  "asynq",
		JobClass: "email:deliver",
		Queue:    "critical",
		JobID:    "j-8134",
		Attempts: 3,
		Args:     map[string]any{"to": "user@example.com"},
	})
}
