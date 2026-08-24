package splatty

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func boomHandler() http.Handler {
	return http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("handler exploded")
	})
}

func serve(t *testing.T, h http.Handler, req *http.Request) (rec *httptest.ResponseRecorder, recovered any) {
	t.Helper()
	rec = httptest.NewRecorder()
	func() {
		defer func() { recovered = recover() }()
		h.ServeHTTP(rec, req)
	}()
	return rec, recovered
}

func TestMiddlewarePassesSuccessThrough(t *testing.T) {
	events := newTestGlobal(t)
	handler := Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))

	rec, recovered := serve(t, handler, httptest.NewRequest(http.MethodGet, "/x", nil))
	if recovered != nil {
		t.Fatalf("unexpected panic: %v", recovered)
	}
	if got, want := rec.Code, http.StatusOK; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got := len(events.all()); got != 0 {
		t.Errorf("sent %d events on a successful request, want 0", got)
	}
}

func TestMiddlewareCapturesAndRepanics(t *testing.T) {
	rec := newTestGlobal(t)
	req := httptest.NewRequest(http.MethodPost, "/x?y=1", nil)
	req.Host = "shop.test"

	_, recovered := serve(t, Middleware(boomHandler()), req)
	if recovered == nil {
		t.Fatalf("the panic was swallowed, want it re-raised")
	}

	events := rec.events(t)
	if len(events) != 1 {
		t.Fatalf("sent %d events, want 1", len(events))
	}
	event := events[0]
	if got, want := event.Level, LevelFatal; got != want {
		t.Errorf("Level = %q, want %q", got, want)
	}
	if got, want := event.Tags["mechanism"], "panic"; got != want {
		t.Errorf("Tags[mechanism] = %q, want %q", got, want)
	}
	if got, want := event.Exception.Values[0].Value, "handler exploded"; got != want {
		t.Errorf("Value = %q, want %q", got, want)
	}
	if got, want := event.Request.Method, http.MethodPost; got != want {
		t.Errorf("Request.Method = %q, want %q", got, want)
	}
	if !strings.Contains(event.Request.URL, "/x?y=1") {
		t.Errorf("Request.URL = %q, want it to contain the query", event.Request.URL)
	}
	if !strings.HasPrefix(event.Request.URL, "http://shop.test") {
		t.Errorf("Request.URL = %q, want the scheme and host", event.Request.URL)
	}
}

func TestMiddlewareCapturesErrorPanicsAsThemselves(t *testing.T) {
	rec := newTestGlobal(t)
	handler := Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrHandlerTimeout)
	}))

	_, recovered := serve(t, handler, httptest.NewRequest(http.MethodGet, "/x", nil))
	if recovered == nil {
		t.Fatalf("the panic was swallowed")
	}
	value := rec.events(t)[0].Exception.Values[0]
	if strings.HasPrefix(value.Type, "panic:") {
		t.Errorf("Type = %q, want the error's own type", value.Type)
	}
}

func TestMiddlewareTagsTheRequestID(t *testing.T) {
	rec := newTestGlobal(t)
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("X-Request-Id", "req-abc-123")

	_, _ = serve(t, Middleware(boomHandler()), req)

	if got, want := rec.events(t)[0].Tags["request_id"], "req-abc-123"; got != want {
		t.Errorf("Tags[request_id] = %q, want %q", got, want)
	}
}

func TestMiddlewareOmitsTheTagWithoutARequestID(t *testing.T) {
	rec := newTestGlobal(t)
	_, _ = serve(t, Middleware(boomHandler()), httptest.NewRequest(http.MethodGet, "/x", nil))

	if _, ok := rec.events(t)[0].Tags["request_id"]; ok {
		t.Errorf("a request_id tag was set without a request id")
	}
}

func TestMiddlewareScrubsSensitiveHeaders(t *testing.T) {
	rec := newTestGlobal(t)
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Cookie", "session=abc")
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("Accept", "text/html")

	_, _ = serve(t, Middleware(boomHandler()), req)

	headers := rec.events(t)[0].Request.Headers
	if got := headers["Cookie"]; got != Filtered {
		t.Errorf("Cookie = %q, want %q", got, Filtered)
	}
	if got := headers["Authorization"]; got != Filtered {
		t.Errorf("Authorization = %q, want %q", got, Filtered)
	}
	if got, want := headers["Accept"], "text/html"; got != want {
		t.Errorf("Accept = %q, want %q", got, want)
	}
}

func TestMiddlewareSendsHeadersVerbatimWithPII(t *testing.T) {
	rec := newTestGlobal(t, func(c *Config) { c.SendDefaultPII = true })
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Cookie", "session=abc")

	_, _ = serve(t, Middleware(boomHandler()), req)

	if got, want := rec.events(t)[0].Request.Headers["Cookie"], "session=abc"; got != want {
		t.Errorf("Cookie = %q, want %q", got, want)
	}
}

func TestMiddlewareOptions(t *testing.T) {
	rec := newTestGlobal(t)
	mw := NewMiddleware(MiddlewareOptions{
		SwallowPanic: true,
		Transaction:  func(r *http.Request) string { return r.Method + " /users/:id" },
	})

	resp, recovered := serve(t, mw(boomHandler()), httptest.NewRequest(http.MethodGet, "/users/7", nil))
	if recovered != nil {
		t.Fatalf("SwallowPanic did not swallow: %v", recovered)
	}
	if got, want := resp.Code, http.StatusInternalServerError; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := rec.events(t)[0].Transaction, "GET /users/:id"; got != want {
		t.Errorf("Transaction = %q, want %q", got, want)
	}
}

func TestMiddlewareIgnoresErrAbortHandler(t *testing.T) {
	rec := newTestGlobal(t)
	handler := Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))

	_, recovered := serve(t, handler, httptest.NewRequest(http.MethodGet, "/x", nil))
	if recovered != http.ErrAbortHandler {
		t.Errorf("recovered = %v, want ErrAbortHandler re-raised untouched", recovered)
	}
	if got := len(rec.all()); got != 0 {
		t.Errorf("sent %d events, want 0 (an aborted response is not a failure)", got)
	}
}

func TestMiddlewareIsInertWithoutAClient(t *testing.T) {
	currentMu.Lock()
	previous := current
	current = nil
	currentMu.Unlock()
	t.Cleanup(func() {
		currentMu.Lock()
		current = previous
		currentMu.Unlock()
	})

	_, recovered := serve(t, Middleware(boomHandler()), httptest.NewRequest(http.MethodGet, "/x", nil))
	if recovered == nil {
		t.Errorf("the panic must still propagate when Splatty is not installed")
	}
}

func TestRequestURLHonoursForwardedProto(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Host = "example.com"
	req.Header.Set("X-Forwarded-Proto", "https, http")

	if got, want := requestURL(req), "https://example.com/x"; got != want {
		t.Errorf("requestURL = %q, want %q", got, want)
	}
}
