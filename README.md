# Splatty (Go)

Go client for [Splatty](https://github.com/splatty-hq/splatty). Captures errors,
panics and logs and ships them over the envelope protocol. Mirrors
[`splatty-ruby`](https://github.com/splatty-hq/splatty-ruby) and
[`splatty-js`](https://github.com/splatty-hq/splatty-js).

Standard library only — no third-party dependencies.

- [Installation](#installation)
- [Quick start](#quick-start)
- [Configuration](#configuration)
- [Capturing events](#capturing-events)
- [Panics](#panics)
- [HTTP](#http)
- [Background jobs](#background-jobs)
- [Logs](#logs)
- [Shutting down](#shutting-down)
- [API reference](#api-reference)
- [Wire protocol](#wire-protocol)
- [Differences from the Ruby and JS clients](#differences-from-the-ruby-and-js-clients)

## Installation

```sh
go get github.com/splatty-hq/splatty-go
```

Requires Go 1.21 or newer (for `log/slog`).

```go
import splatty "github.com/splatty-hq/splatty-go"
```

## Quick start

```go
func main() {
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

	if err := run(); err != nil {
		splatty.CaptureException(err)
	}
}
```

`Init` installs a process-wide client and never fails: a config that does not
validate logs one warning and turns every capture into a no-op, so a bad DSN
can't stop your service from booting.

Captures are **asynchronous** by default. `CaptureException` hands the event to
a background sender and returns immediately with the event id, so it is safe on
a request path. `Close` (or `Flush`) waits for the backlog to drain — see
[Shutting down](#shutting-down).

## Configuration

`Config`'s zero value is usable, and its booleans are named so the zero value is
the recommended setting.

| field | env | default | what it does |
|---|---|---|---|
| `URL` | `SPLATTY_URL` | `https://splatty.app` | Server base URL; events go to `<URL>/api/envelope` |
| `DSN` | `SPLATTY_DSN` | — (required) | Project key, sent as `Authorization: Bearer <dsn>` |
| `Environment` | `SPLATTY_ENVIRONMENT`, `GO_ENV`, `APP_ENV` | `development` | Stamped on every event and log entry |
| `Release` | `SPLATTY_RELEASE` | — | Stamped on every event and log entry |
| `ServerName` | — | `os.Hostname()` | Overrides the reported host |
| `Disabled` | — | `false` | Turns every capture into a no-op |
| `DisableLogs` | — | `false` | Skips installing the log appender |
| `SendDefaultPII` | — | `false` | Sends request headers verbatim instead of filtering them |
| `Synchronous` | — | `false` | Sends inline instead of via the background sender |
| `OpenTimeout` | — | `5s` | Connection setup timeout |
| `ReadTimeout` | — | `10s` | Whole-request timeout |
| `QueueSize` | — | `1000` | Background sender backlog; captures past it are dropped |
| `Logger` | — | `log.Default()` | Where the SDK writes its own warnings |
| `HTTPClient` | — | — | Overrides the transport's client |
| `BeforeSend` | — | — | Last chance to mutate an event, or drop it by returning `nil` |
| `LogOptions` | — | — | Appender tuning, see [Logs](#logs) |

### Filtering sensitive data

By default (`SendDefaultPII: false`) sensitive request headers — `Cookie`,
`Authorization`, CSRF tokens, API keys, session and password headers — are
replaced with `[Filtered]` before an event leaves the process. Matching is
case-insensitive.

Set `SendDefaultPII: true` only if you understand that cookies and auth tokens
will then be transmitted and stored.

## Capturing events

```go
id := splatty.CaptureException(err)
id := splatty.CaptureMessage("cache miss storm", splatty.WithLevel(splatty.LevelWarn))
```

Both return the event id, or `""` when nothing was sent (disabled client,
already-reported error, dropped by `BeforeSend`, or a full backlog).

Scope is applied with options:

```go
splatty.CaptureException(err,
	splatty.WithLevel(splatty.LevelWarn),
	splatty.WithTransaction("POST /checkout"),
	splatty.WithTag("area", "billing"),
	splatty.WithTags(map[string]string{"tenant": "acme"}),
	splatty.WithExtra("order_id", 4711),
	splatty.WithExtras(map[string]any{"cart_size": 12}),
	splatty.WithContext("app", map[string]any{"build": "1.2.3"}),
	splatty.WithRequest(splatty.RequestContext(r)),
)
```

### Stack traces

Go errors carry no stack of their own, so the trace is captured **where you call
`CaptureException`**, not where the error was created. Capture close to the
failure for the most useful frames. Frames are reported oldest-first, and
`in_app` is true for anything that is neither standard library nor a module
under `pkg/mod`.

### Error chains

`errors.Unwrap` chains are reported root-cause first, matching the other
clients. `errors.Join` is flattened depth-first. The captured stack hangs off
the outermost error.

```go
err := fmt.Errorf("charging customer: %w", stripe.ErrCardDeclined)
splatty.CaptureException(err)
// values[0] = stripe.ErrCardDeclined
// values[1] = "charging customer: card declined"  ← carries the stack
```

By default an event's `type` is the error's concrete Go type
(`*fmt.wrapError`). Implement `TypedError` to choose a friendlier name:

```go
func (e *DeclinedError) ExceptionType() string { return "billing.DeclinedError" }
```

### Reported once

An error value is only reported once, so a failure that surfaces through both a
worker hook and the HTTP middleware produces a single event. Go has no weak
references, so the set of seen errors is bounded at 1024 and evicts oldest-first;
errors with no stable identity (comparable value types rather than pointers) are
always captured.

## Panics

Go has no process-wide panic hook, so panics are caught where they happen.

```go
go func() {
	defer splatty.Recover() // report, flush, then re-panic
	work()
}()
```

`Recover` preserves Go's crash behaviour. For a worker loop that must survive:

```go
for job := range jobs {
	func() {
		defer splatty.RecoverAndContinue(splatty.WithTag("worker", id))
		handle(job)
	}()
}
```

`CapturePanic(recovered)` reports a value you recovered yourself. Panics are
reported at `fatal` with a `mechanism: panic` tag; a non-error panic value is
wrapped in `PanicError` and reported as `panic: <type>`.

## HTTP

```go
mux := http.NewServeMux()
// ...routes...
http.ListenAndServe(":8080", splatty.Middleware(mux))
```

Reports panics with the request that caused them and re-panics, so your own
recovery and error rendering still run. `http.ErrAbortHandler` is passed
through untouched — an aborted response is not a failure. Events carry the URL,
method and headers, plus a `request_id` tag from `X-Request-Id`,
`X-Request-ID` or `X-Correlation-Id`.

No transaction is set by default: only your router knows the route template,
and a raw path would blow up cardinality. Supply one for chi, gorilla, or
Go 1.23+ `http.Request.Pattern`:

```go
mw := splatty.NewMiddleware(splatty.MiddlewareOptions{
	Transaction:  func(r *http.Request) string { return r.Method + " " + chi.RouteContext(r.Context()).RoutePattern() },
	SwallowPanic: false, // default: re-panic after reporting
})
router.Use(mw)
```

To report a handled error with the same request context, reuse `RequestScope`:

```go
if err := loadUser(id); err != nil {
	splatty.CaptureException(err, splatty.WithScope(splatty.RequestScope(r)))
	http.Error(w, "internal error", http.StatusInternalServerError)
}
```

## Background jobs

The Go queue ecosystem has no single winner, so the client stays queue-agnostic:
map whatever your queue gives you onto `JobContext`.

```go
// asynq
srv := asynq.NewServer(redis, asynq.Config{
	ErrorHandler: asynq.ErrorHandlerFunc(func(ctx context.Context, task *asynq.Task, err error) {
		retried, _ := asynq.GetRetryCount(ctx)
		maxRetry, _ := asynq.GetMaxRetry(ctx)
		if retried < maxRetry {
			return // asynq will try again; stay quiet like the Ruby client does
		}
		splatty.CaptureJobException(err, splatty.JobContext{
			Backend:  "asynq",
			JobClass: task.Type(),
			Queue:    "default",
			Attempts: retried + 1,
			Args:     json.RawMessage(task.Payload()),
		})
	}),
})
```

Events are tagged with `job_backend`, `job_class` and `job_queue`, get a
`transaction` of the job class, and carry `job_id`, `job_attempts` and
`job_args` as extra data. Arguments are JSON-encoded and truncated at 2048
bytes with a `...(truncated)` suffix.

## Logs

`Init` installs a batching appender unless you set `DisableLogs`. It buffers
entries and ships them as `log` envelope items every 15 seconds, or immediately
once `BatchSize` entries are queued.

Entries about Splatty's own intake paths are dropped, so a dogfooded app can't
feed itself: every shipped batch would otherwise become a new request log, which
becomes another batch. The dropped paths are `/api/envelope`, `/api/logs` and
`/api/metrics`, each also matching an optional numeric project id
(`/api/42/logs`) and a trailing slash.

### slog

The stdlib has no fan-out handler, so the client ships one:

```go
slog.SetDefault(slog.New(splatty.NewSlogHandlerTee(
	slog.NewJSONHandler(os.Stdout, nil),
	&splatty.SlogOptions{Level: slog.LevelInfo},
)))

slog.Info("checkout completed", "path", "/checkout", "status", 200, "duration_ms", 42)
```

Use `splatty.NewSlogHandler(nil)` on its own if you only want Splatty, or if
you already have a fan-out handler. `WithAttrs` and `WithGroup` are honoured;
groups are flattened into dotted keys, so `slog.Group("db", "rows", 3)` becomes
the field `db.rows`.

### Anything else

```go
splatty.CaptureLog(splatty.LogRecord{
	Level:   splatty.LevelInfo,
	Message: "checkout completed",
	Time:    time.Now(),
	Fields:  map[string]any{"request_id": id, "path": "/checkout", "status": 200},
})
```

Returns `false` when the entry was dropped.

### Entry shape

`request_id`, `method`, `path`, `status`, `duration_ms` (or `duration`),
`controller` and `action` are lifted out of `Fields` into top-level columns.
What is left is stringified into a string map. A `sql` field is inlined into
the message: `Load — SELECT 1`. Levels are normalized to `debug`, `info`,
`warn`, `error` or `fatal`.

### Tuning

```go
splatty.Init(splatty.Config{
	DSN: dsn,
	LogOptions: splatty.LogOptions{
		Level:         splatty.LevelInfo, // drop anything below this
		BatchSize:     100,               // flush once this many are queued
		FlushInterval: 15 * time.Second,
		QueueLimit:    5000,              // past this the oldest entry is dropped
		Host:          "web-1",
	},
})
```

## Shutting down

```go
splatty.Flush(ctx) // wait for queued events and logs, keep running
splatty.Close(ctx) // flush, stop the workers, release connections
```

Both take a context so shutdown stays bounded. Always `Close` before exiting:
the background sender is asynchronous, and a process that exits immediately
after a capture will drop it.

## API reference

**Lifecycle** — `Init(Config) *Client`, `CurrentClient()`, `Configuration()`,
`Appender()`, `Enabled()`, `Flush(ctx)`, `Close(ctx)`.

**Capture** — `CaptureException(err, ...ScopeOption)`, `CaptureMessage(msg,
...ScopeOption)`, `CaptureLog(LogRecord)`, `CaptureJobException(err,
JobContext, ...ScopeOption)`, `CapturePanic(recovered, ...ScopeOption)`.

**Panic helpers** — `Recover(...ScopeOption)`, `RecoverAndContinue(...ScopeOption)`.

**HTTP** — `Middleware(http.Handler)`, `NewMiddleware(MiddlewareOptions)`,
`RequestScope(*http.Request)`, `RequestContext(*http.Request)`.

**Logging** — `NewSlogHandler(*SlogOptions)`, `NewSlogHandlerTee(slog.Handler,
*SlogOptions)`, `MapLevel(string)`.

**Scope options** — `WithScope`, `WithLevel`, `WithTransaction`, `WithTag`,
`WithTags`, `WithExtra`, `WithExtras`, `WithContext`, `WithRequest`.

**Building blocks** — `NewClient`, `NewTransport`, `NewScrubber`,
`NewExceptionEvent`, `NewMessageEvent`, `JobScope`, `EncodeArgs`.

**Constants** — `Version`, `SDKName`, `DefaultURL`, `Filtered`,
`SensitiveHeaderPattern`, `IntakePathPattern`, `MaxArgsLength`,
`DefaultBatchSize`, `DefaultFlushInterval`, `DefaultQueueLimit`, and the
`Level*` values.

## Wire protocol

Everything is POSTed gzipped to `<URL>/api/envelope` over a keep-alive
connection, with `Content-Type: application/x-splatty-envelope` and
`Authorization: Bearer <dsn>`. The body is three newline-separated lines: an
envelope header, an item header, and the JSON payload.

```
{"event_id":"…","sent_at":"…","dsn":"…","sdk":{"name":"splatty.go","version":"0.1.0"}}
{"type":"event","content_type":"application/json","length":1234}
{"event_id":"…","timestamp":"…","platform":"go","level":"error","exception":{…}}
```

Log batches use the same shape. Their envelope header carries no `event_id`,
and the item header is `{"type":"log","item_count":N,"content_type":
"application/vnd.splatty.items.log+json","length":…}` over a
`{"host":…,"items":[…]}` payload.

## Differences from the Ruby and JS clients

The wire format, scrubbing rules, log batching and capture-once semantics are
the same. What Go does differently, and why:

- **Async by default.** Ruby and JS send inline. Blocking a Go request handler
  on an HTTP round trip to the error backend is not acceptable, so captures are
  queued and `Close`/`Flush` drain them. Set `Synchronous: true` to opt out.
- **Stacks are captured at the capture site**, because Go errors carry none.
- **Panics replace uncaught-exception hooks.** Go has no process-wide handler,
  so `Recover`, `RecoverAndContinue` and the HTTP middleware cover that ground.
- **No per-queue job adapters.** Ruby has Active Job, Sidekiq and Solid Queue;
  JS has BullMQ. Go has no dominant queue, so `CaptureJobException` takes a
  `JobContext` you fill from whichever one you use.
- **Inverted config booleans** (`Disabled`, `DisableLogs`) so `Config{}` is a
  working default, rather than pointer fields to distinguish unset from false.

## Development

```sh
gofmt -l .
go vet ./...
go test -race ./...
```

## License

MIT.
