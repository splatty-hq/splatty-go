package splatty

import (
	"net/http"
	"strings"
)

// MiddlewareOptions customizes the HTTP middleware.
type MiddlewareOptions struct {
	// Transaction derives the transaction name from the request. When nil no
	// transaction is set, matching the Ruby client's Rack middleware — a raw
	// path would blow up cardinality, and only your router knows the route
	// template.
	Transaction func(*http.Request) string
	// SwallowPanic keeps the panic from propagating after it is reported. By
	// default the panic is re-raised so your own recovery still runs.
	SwallowPanic bool
	// Client overrides the process-wide client, mainly for tests.
	Client *Client
}

// Middleware reports panics raised by next, together with the request that
// caused them, and re-panics so existing recovery still runs.
//
//	mux := http.NewServeMux()
//	http.ListenAndServe(":8080", splatty.Middleware(mux))
func Middleware(next http.Handler) http.Handler {
	return NewMiddleware(MiddlewareOptions{})(next)
}

// NewMiddleware returns a configurable middleware in the shape chi, gorilla
// and friends expect.
func NewMiddleware(opts MiddlewareOptions) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				recovered := recover()
				if recovered == nil {
					return
				}
				// http.ErrAbortHandler is the documented way to abort a
				// response; it is not an application failure.
				if recovered == http.ErrAbortHandler {
					panic(recovered)
				}

				client := opts.Client
				if client == nil {
					client = CurrentClient()
				}
				if client.Enabled() {
					scope := RequestScope(r)
					if opts.Transaction != nil {
						scope.Transaction = opts.Transaction(r)
					}
					err, ok := recovered.(error)
					if !ok {
						err = &PanicError{Value: recovered}
					}
					client.captureException(err, 1,
						WithScope(scope),
						WithLevel(LevelFatal),
						WithTag("mechanism", "panic"),
					)
				}

				if !opts.SwallowPanic {
					panic(recovered)
				}
				w.WriteHeader(http.StatusInternalServerError)
			}()

			next.ServeHTTP(w, r)
		})
	}
}

// RequestScope builds the scope an HTTP-borne event is reported with: the
// request details plus a request_id tag when the request carries one.
func RequestScope(r *http.Request) Scope {
	scope := Scope{Request: RequestContext(r)}
	if id := requestID(r); id != "" {
		scope.Tags = map[string]string{"request_id": id}
	}
	return scope
}

// RequestContext extracts the URL, method and headers of a request.
func RequestContext(r *http.Request) *Request {
	if r == nil {
		return nil
	}

	headers := make(map[string]string, len(r.Header))
	for name, values := range r.Header {
		headers[name] = strings.Join(values, ", ")
	}
	if r.Host != "" {
		headers["Host"] = r.Host
	}

	return &Request{
		URL:     requestURL(r),
		Method:  r.Method,
		Headers: headers,
	}
}

func requestURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if forwarded := r.Header.Get("X-Forwarded-Proto"); forwarded != "" {
		scheme = strings.TrimSpace(strings.Split(forwarded, ",")[0])
	} else if r.URL != nil && r.URL.Scheme != "" {
		scheme = r.URL.Scheme
	}

	host := r.Host
	if host == "" && r.URL != nil {
		host = r.URL.Host
	}

	path := r.RequestURI
	if path == "" && r.URL != nil {
		path = r.URL.RequestURI()
	}
	return scheme + "://" + host + path
}

func requestID(r *http.Request) string {
	if r == nil {
		return ""
	}
	for _, header := range []string{"X-Request-Id", "X-Request-ID", "X-Correlation-Id"} {
		if id := r.Header.Get(header); id != "" {
			return id
		}
	}
	return ""
}
