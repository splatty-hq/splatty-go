package splatty

import (
	"context"
	"log/slog"
	"strings"
	"time"
)

// SlogOptions configures a SlogHandler.
type SlogOptions struct {
	// Level is the minimum level the handler accepts. Defaults to slog.LevelInfo.
	Level slog.Leveler
	// Client overrides the process-wide client, mainly for tests.
	Client *Client
}

// SlogHandler forwards log/slog records to Splatty. It is the Go counterpart
// of the Ruby client's SemanticLogger appender.
//
//	logger := slog.New(slogmulti.Fanout(
//	    slog.NewJSONHandler(os.Stdout, nil),
//	    splatty.NewSlogHandler(nil),
//	))
//
// With no fan-out helper at hand, NewSlogHandler can also wrap another handler
// directly via NewSlogHandlerTee.
type SlogHandler struct {
	opts SlogOptions
	// preformatted holds attrs from WithAttrs, already qualified with the
	// groups that were open when they were added. Qualifying them later would
	// wrongly pull attrs added before a WithGroup into that group.
	preformatted map[string]any
	groups       []string
}

var _ slog.Handler = (*SlogHandler)(nil)

// NewSlogHandler builds a handler that ships every record it accepts.
func NewSlogHandler(opts *SlogOptions) *SlogHandler {
	h := &SlogHandler{}
	if opts != nil {
		h.opts = *opts
	}
	if h.opts.Level == nil {
		h.opts.Level = slog.LevelInfo
	}
	return h
}

func (h *SlogHandler) client() *Client {
	if h.opts.Client != nil {
		return h.opts.Client
	}
	return CurrentClient()
}

// Enabled implements slog.Handler.
func (h *SlogHandler) Enabled(_ context.Context, level slog.Level) bool {
	if !h.client().Enabled() {
		return false
	}
	return level >= h.opts.Level.Level()
}

// Handle implements slog.Handler.
func (h *SlogHandler) Handle(_ context.Context, record slog.Record) error {
	client := h.client()
	if client == nil {
		return nil
	}

	fields := make(map[string]any, record.NumAttrs()+len(h.preformatted))
	for key, value := range h.preformatted {
		fields[key] = value
	}
	record.Attrs(func(attr slog.Attr) bool {
		collectAttr(fields, h.groups, attr)
		return true
	})

	ts := record.Time
	if ts.IsZero() {
		ts = time.Now()
	}
	client.CaptureLog(LogRecord{
		Time:    ts,
		Level:   slogLevelName(record.Level),
		Message: record.Message,
		Fields:  fields,
	})
	return nil
}

// WithAttrs implements slog.Handler.
func (h *SlogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	next := h.clone()
	for _, attr := range attrs {
		collectAttr(next.preformatted, h.groups, attr)
	}
	return next
}

// WithGroup implements slog.Handler.
func (h *SlogHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	next := h.clone()
	next.groups = append(next.groups, name)
	return next
}

func (h *SlogHandler) clone() *SlogHandler {
	next := &SlogHandler{
		opts:         h.opts,
		preformatted: make(map[string]any, len(h.preformatted)),
	}
	for key, value := range h.preformatted {
		next.preformatted[key] = value
	}
	next.groups = append(next.groups[:0:0], h.groups...)
	return next
}

// collectAttr flattens an attr into dotted keys, so a group "http" holding
// "status" becomes the field "http.status".
func collectAttr(dst map[string]any, groups []string, attr slog.Attr) {
	attr.Value = attr.Value.Resolve()
	if attr.Equal(slog.Attr{}) {
		return
	}

	key := attr.Key
	if len(groups) > 0 {
		key = strings.Join(groups, ".") + "." + key
	}

	if attr.Value.Kind() == slog.KindGroup {
		inner := attr.Value.Group()
		if len(inner) == 0 {
			return
		}
		nested := groups
		if attr.Key != "" {
			nested = append(append([]string{}, groups...), attr.Key)
		}
		for _, sub := range inner {
			collectAttr(dst, nested, sub)
		}
		return
	}
	dst[key] = attr.Value.Any()
}

func slogLevelName(level slog.Level) string {
	switch {
	case level < slog.LevelInfo:
		return LevelDebug
	case level < slog.LevelWarn:
		return LevelInfo
	case level < slog.LevelError:
		return LevelWarn
	case level < slog.LevelError+4:
		return LevelError
	default:
		return LevelFatal
	}
}

// teeHandler forwards each record to two handlers.
type teeHandler struct {
	primary slog.Handler
	splatty slog.Handler
}

var _ slog.Handler = (*teeHandler)(nil)

// NewSlogHandlerTee wraps an existing handler so records go to it and to
// Splatty. The stdlib has no fan-out handler, and this is the common case.
//
//	slog.SetDefault(slog.New(splatty.NewSlogHandlerTee(
//	    slog.NewJSONHandler(os.Stdout, nil), nil,
//	)))
func NewSlogHandlerTee(primary slog.Handler, opts *SlogOptions) slog.Handler {
	return &teeHandler{primary: primary, splatty: NewSlogHandler(opts)}
}

func (t *teeHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return t.primary.Enabled(ctx, level) || t.splatty.Enabled(ctx, level)
}

func (t *teeHandler) Handle(ctx context.Context, record slog.Record) error {
	var err error
	if t.primary.Enabled(ctx, record.Level) {
		err = t.primary.Handle(ctx, record.Clone())
	}
	if t.splatty.Enabled(ctx, record.Level) {
		if serr := t.splatty.Handle(ctx, record.Clone()); err == nil {
			err = serr
		}
	}
	return err
}

func (t *teeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &teeHandler{primary: t.primary.WithAttrs(attrs), splatty: t.splatty.WithAttrs(attrs)}
}

func (t *teeHandler) WithGroup(name string) slog.Handler {
	return &teeHandler{primary: t.primary.WithGroup(name), splatty: t.splatty.WithGroup(name)}
}
