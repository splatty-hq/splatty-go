package splatty

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Log appender defaults, matching the Ruby and JS clients.
const (
	DefaultBatchSize     = 100
	DefaultFlushInterval = 15 * time.Second
	DefaultQueueLimit    = 5000
)

// IntakePathPattern matches Splatty's own intake endpoints. Log entries about
// them are dropped: without this a dogfooded app feeds itself, because every
// shipped batch becomes a new request log, which becomes another batch.
var IntakePathPattern = regexp.MustCompile(`^/api/(?:\d+/)?(?:logs|metrics|envelope)/?$`)

// LogRecord is the neutral shape adapters translate their own records into.
type LogRecord struct {
	Time    time.Time
	Level   string
	Message string
	Fields  map[string]any
}

// LogEntry is one shipped log line.
type LogEntry struct {
	Timestamp   int64             `json:"timestamp"`
	Level       string            `json:"level"`
	Message     string            `json:"message"`
	RequestID   string            `json:"request_id"`
	Method      string            `json:"method"`
	Path        string            `json:"path"`
	Status      int               `json:"status"`
	DurationMS  *float64          `json:"duration_ms"`
	Controller  string            `json:"controller"`
	Action      string            `json:"action"`
	Environment string            `json:"environment"`
	Release     string            `json:"release"`
	Host        string            `json:"host"`
	Fields      map[string]string `json:"fields"`
}

// LogOptions tunes the batching appender.
type LogOptions struct {
	// Level drops anything below it. Empty means ship everything.
	Level string
	// BatchSize flushes early once this many entries are queued.
	BatchSize int
	// FlushInterval is how often the appender ships on its own.
	FlushInterval time.Duration
	// QueueLimit bounds the backlog; past it the oldest entry is dropped.
	QueueLimit int
	// Host is reported with every batch. Defaults to os.Hostname().
	Host string
}

func (o *LogOptions) applyDefaults() {
	if o.BatchSize <= 0 {
		o.BatchSize = DefaultBatchSize
	}
	if o.FlushInterval == 0 {
		o.FlushInterval = DefaultFlushInterval
	}
	if o.QueueLimit <= 0 {
		o.QueueLimit = DefaultQueueLimit
	}
	if o.Host == "" {
		o.Host, _ = os.Hostname()
	}
}

var levelOrder = map[string]int{
	LevelDebug: 10,
	LevelInfo:  20,
	LevelWarn:  30,
	LevelError: 40,
	LevelFatal: 50,
}

// MapLevel normalizes a logger's level name onto the five the server accepts.
func MapLevel(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "trace", "debug", "verbose":
		return LevelDebug
	case "info", "http", "log", "notice":
		return LevelInfo
	case "warn", "warning":
		return LevelWarn
	case "error", "err":
		return LevelError
	case "fatal", "crit", "critical", "alert", "emerg", "panic", "dpanic":
		return LevelFatal
	default:
		return LevelInfo
	}
}

// LogAppender buffers log entries and ships them as log envelope items.
type LogAppender struct {
	client *Client
	opts   LogOptions

	mu     sync.Mutex
	queue  []LogEntry
	closed bool

	flushNow chan struct{}
	done     chan struct{}
	wg       sync.WaitGroup
}

func newLogAppender(client *Client, opts LogOptions) *LogAppender {
	opts.applyDefaults()
	a := &LogAppender{
		client:   client,
		opts:     opts,
		flushNow: make(chan struct{}, 1),
		done:     make(chan struct{}),
	}
	a.wg.Add(1)
	go a.run()
	return a
}

// Host is the hostname reported with every batch.
func (a *LogAppender) Host() string { return a.opts.Host }

// Size is the number of entries currently queued.
func (a *LogAppender) Size() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.queue)
}

// Log enqueues a record. It reports false when the entry was dropped.
func (a *LogAppender) Log(rec LogRecord) bool {
	if a == nil || a.client == nil || !a.client.Enabled() {
		return false
	}
	if intakeRequest(rec) {
		return false
	}

	entry := a.buildEntry(rec)
	if !a.passesLevel(entry.Level) {
		return false
	}

	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return false
	}
	if len(a.queue) >= a.opts.QueueLimit {
		a.queue = a.queue[1:]
	}
	a.queue = append(a.queue, entry)
	full := len(a.queue) >= a.opts.BatchSize
	a.mu.Unlock()

	if full {
		a.signalFlush()
	}
	return true
}

// Flush ships everything currently queued.
func (a *LogAppender) Flush(ctx context.Context) error {
	if a == nil {
		return nil
	}
	for {
		batch := a.take()
		if len(batch) == 0 {
			return nil
		}
		if err := a.client.transport.SendLogs(ctx, a.opts.Host, batch); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
}

// Close stops the background flusher and ships whatever is left.
func (a *LogAppender) Close(ctx context.Context) error {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil
	}
	a.closed = true
	a.mu.Unlock()

	close(a.done)
	a.wg.Wait()
	return a.Flush(ctx)
}

func (a *LogAppender) run() {
	defer a.wg.Done()
	ticker := time.NewTicker(a.opts.FlushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-a.done:
			return
		case <-ticker.C:
			a.flushQuietly()
		case <-a.flushNow:
			a.flushQuietly()
		}
	}
}

func (a *LogAppender) flushQuietly() {
	ctx, cancel := context.WithTimeout(context.Background(), a.client.config.ReadTimeout)
	defer cancel()
	_ = a.Flush(ctx)
}

func (a *LogAppender) signalFlush() {
	select {
	case a.flushNow <- struct{}{}:
	default: // a flush is already pending
	}
}

// take pops up to four batches' worth of entries in one go.
func (a *LogAppender) take() []LogEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.queue) == 0 {
		return nil
	}
	n := a.opts.BatchSize * 4
	if n > len(a.queue) {
		n = len(a.queue)
	}
	batch := make([]LogEntry, n)
	copy(batch, a.queue[:n])
	a.queue = append(a.queue[:0], a.queue[n:]...)
	return batch
}

func (a *LogAppender) passesLevel(level string) bool {
	if a.opts.Level == "" {
		return true
	}
	return levelOrder[level] >= levelOrder[MapLevel(a.opts.Level)]
}

func (a *LogAppender) buildEntry(rec LogRecord) LogEntry {
	cfg := a.client.config
	fields := stringifyFields(rec.Fields)
	ts := rec.Time
	if ts.IsZero() {
		ts = time.Now()
	}

	return LogEntry{
		Timestamp:   ts.UnixMilli(),
		Level:       MapLevel(rec.Level),
		Message:     buildMessage(rec.Message, fields),
		RequestID:   extractString(fields, "request_id"),
		Method:      extractString(fields, "method"),
		Path:        extractString(fields, "path"),
		Status:      extractInt(fields, "status"),
		DurationMS:  extractFloat(fields, "duration_ms", "duration"),
		Controller:  extractString(fields, "controller"),
		Action:      extractString(fields, "action"),
		Environment: cfg.Environment,
		Release:     cfg.Release,
		Host:        a.opts.Host,
		Fields:      fields,
	}
}

func intakeRequest(rec LogRecord) bool {
	raw, ok := rec.Fields["path"]
	if !ok {
		return false
	}
	path, ok := raw.(string)
	if !ok || path == "" {
		return false
	}
	return IntakePathPattern.MatchString(path)
}

func buildMessage(message string, fields map[string]string) string {
	sql := strings.TrimSpace(fields["sql"])
	if sql == "" {
		return message
	}
	if message == "" {
		return sql
	}
	return message + " — " + sql
}

func stringifyFields(src map[string]any) map[string]string {
	out := make(map[string]string, len(src))
	for k, v := range src {
		if v == nil {
			continue
		}
		if s, ok := v.(string); ok {
			out[k] = s
			continue
		}
		out[k] = fmt.Sprintf("%v", v)
	}
	return out
}

func extractString(fields map[string]string, key string) string {
	v := fields[key]
	delete(fields, key)
	return v
}

func extractInt(fields map[string]string, key string) int {
	v, ok := fields[key]
	delete(fields, key)
	if !ok {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return 0
	}
	return n
}

func extractFloat(fields map[string]string, keys ...string) *float64 {
	for _, key := range keys {
		v, ok := fields[key]
		if !ok {
			continue
		}
		delete(fields, key)
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return nil
		}
		return &f
	}
	return nil
}
