package splatty

// Level values accepted by the server.
const (
	LevelDebug = "debug"
	LevelInfo  = "info"
	LevelWarn  = "warn"
	LevelError = "error"
	LevelFatal = "fatal"
)

// Request describes the HTTP request an event happened in.
type Request struct {
	URL     string            `json:"url,omitempty"`
	Method  string            `json:"method,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// Scope carries the optional metadata attached to an event.
type Scope struct {
	Level       string
	Transaction string
	Tags        map[string]string
	Extra       map[string]any
	Contexts    map[string]any
	Request     *Request
}

// ScopeOption customizes the scope of a single capture call.
type ScopeOption func(*Scope)

func buildScope(opts []ScopeOption) Scope {
	var scope Scope
	for _, opt := range opts {
		if opt != nil {
			opt(&scope)
		}
	}
	return scope
}

// WithScope replaces the whole scope. Later options still apply on top.
func WithScope(s Scope) ScopeOption {
	return func(scope *Scope) { *scope = s }
}

// WithLevel overrides the event level.
func WithLevel(level string) ScopeOption {
	return func(s *Scope) { s.Level = level }
}

// WithTransaction names the operation the event happened in.
func WithTransaction(name string) ScopeOption {
	return func(s *Scope) { s.Transaction = name }
}

// WithTag adds a single indexed tag.
func WithTag(key, value string) ScopeOption {
	return func(s *Scope) {
		if s.Tags == nil {
			s.Tags = map[string]string{}
		}
		s.Tags[key] = value
	}
}

// WithTags merges indexed tags into the scope.
func WithTags(tags map[string]string) ScopeOption {
	return func(s *Scope) {
		if s.Tags == nil {
			s.Tags = make(map[string]string, len(tags))
		}
		for k, v := range tags {
			s.Tags[k] = v
		}
	}
}

// WithExtra adds a single free-form value.
func WithExtra(key string, value any) ScopeOption {
	return func(s *Scope) {
		if s.Extra == nil {
			s.Extra = map[string]any{}
		}
		s.Extra[key] = value
	}
}

// WithExtras merges free-form values into the scope.
func WithExtras(extra map[string]any) ScopeOption {
	return func(s *Scope) {
		if s.Extra == nil {
			s.Extra = make(map[string]any, len(extra))
		}
		for k, v := range extra {
			s.Extra[k] = v
		}
	}
}

// WithContext adds a named context block, e.g. "device" or "app".
func WithContext(key string, value any) ScopeOption {
	return func(s *Scope) {
		if s.Contexts == nil {
			s.Contexts = map[string]any{}
		}
		s.Contexts[key] = value
	}
}

// WithRequest attaches HTTP request details.
func WithRequest(r *Request) ScopeOption {
	return func(s *Scope) { s.Request = r }
}
