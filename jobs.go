package splatty

import "encoding/json"

// MaxArgsLength caps the serialized job arguments attached to an event.
const MaxArgsLength = 2048

// EncodeArgs serializes job arguments for the job_args extra, truncating
// anything that would bloat the event. It returns "" when there is nothing
// useful to attach.
func EncodeArgs(args any) string {
	if args == nil {
		return ""
	}
	encoded, err := json.Marshal(args)
	if err != nil {
		return ""
	}
	if len(encoded) <= MaxArgsLength {
		return string(encoded)
	}
	return string(encoded[:MaxArgsLength]) + "...(truncated)"
}

// JobContext describes a background job failure. It is queue-agnostic: asynq,
// River, machinery, or a hand-rolled worker pool all map onto it.
type JobContext struct {
	// Backend names the queue, e.g. "asynq". Lands in the job_backend tag.
	Backend string
	// JobClass is the job or task type. Becomes the job_class tag and the
	// event transaction.
	JobClass string
	// Queue is the queue name, becoming the job_queue tag.
	Queue string
	// JobID identifies this run.
	JobID string
	// Attempts is how many times the job has run.
	Attempts int
	// Args is the job payload, JSON-encoded and truncated.
	Args any
	// Extra adds arbitrary values alongside the job fields.
	Extra map[string]any
}

// JobScope turns a JobContext into the scope job failures are reported with.
func JobScope(job JobContext) Scope {
	tags := map[string]string{}
	if job.Backend != "" {
		tags["job_backend"] = job.Backend
	}
	if job.JobClass != "" {
		tags["job_class"] = job.JobClass
	}
	if job.Queue != "" {
		tags["job_queue"] = job.Queue
	}

	extra := make(map[string]any, len(job.Extra)+3)
	for k, v := range job.Extra {
		extra[k] = v
	}
	if job.JobID != "" {
		extra["job_id"] = job.JobID
	}
	if job.Attempts > 0 {
		extra["job_attempts"] = job.Attempts
	}
	if args := EncodeArgs(job.Args); args != "" {
		extra["job_args"] = args
	}

	return Scope{Tags: tags, Extra: extra, Transaction: job.JobClass}
}

// CaptureJobException reports a background job failure.
func CaptureJobException(err error, job JobContext, opts ...ScopeOption) string {
	scoped := append([]ScopeOption{WithScope(JobScope(job))}, opts...)
	return CurrentClient().captureException(err, 1, scoped...)
}
