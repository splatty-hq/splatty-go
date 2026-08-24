package splatty

import "regexp"

// Filtered replaces the value of a sensitive header.
const Filtered = "[Filtered]"

// SensitiveHeaderPattern matches headers whose values are stripped unless
// Config.SendDefaultPII is set.
var SensitiveHeaderPattern = regexp.MustCompile(
	`(?i)authoriz|cookie|csrf|xsrf|secret|token|password|api[-_]?key|session`,
)

// Scrubber removes sensitive request headers from an event before it leaves
// the process.
type Scrubber struct {
	sendDefaultPII bool
}

// NewScrubber builds a Scrubber for a config.
func NewScrubber(cfg *Config) *Scrubber {
	return &Scrubber{sendDefaultPII: cfg.SendDefaultPII}
}

// Scrub filters the event in place and returns it.
func (s *Scrubber) Scrub(event *Event) *Event {
	if event == nil || s.sendDefaultPII {
		return event
	}
	if event.Request == nil {
		return event
	}
	for name := range event.Request.Headers {
		if SensitiveHeaderPattern.MatchString(name) {
			event.Request.Headers[name] = Filtered
		}
	}
	return event
}
