// Package jobs is a cancellable background-work primitive with a bounded event
// stream, so long operations can report progress and be cancelled without
// blocking the UI.
package jobs

import (
	"context"
	"sync"

	"github.com/manju4682/findit/internal/model"
)

// Kind classifies an event on a job's stream.
type Kind string

const (
	KindProgress      Kind = "progress"
	KindSourceStarted Kind = "source_started"
	KindSourceDone    Kind = "source_done"
	KindLog           Kind = "log"
	KindDone          Kind = "done"   // terminal: completed successfully
	KindFailed        Kind = "failed" // terminal: fn returned an error
)

// Event is one message on a job's stream. Fields are populated per Kind.
type Event struct {
	Kind    Kind                  `json:"kind"`
	Phase   string                `json:"phase,omitempty"`
	Message string                `json:"message,omitempty"`
	Done    int                   `json:"done,omitempty"`
	Total   int                   `json:"total,omitempty"`
	Source  *model.RecoverySource `json:"source,omitempty"`
	Err     error                 `json:"-"`
}

// Job is a running unit of work. Consumers must drain Events() until it is
// closed; the final event is always KindDone or KindFailed. Wait returns the
// terminal error (nil on success).
type Job struct {
	events chan Event
	cancel context.CancelFunc
	done   chan struct{}

	mu  sync.Mutex
	err error
}

// Run starts fn in a goroutine with a bounded event channel of the given buffer
// (default 64) and a cancellable context. fn emits events via the provided
// callback; emitting respects cancellation so a stalled consumer cannot wedge
// the worker once the job is cancelled.
func Run(parent context.Context, buffer int, fn func(ctx context.Context, emit func(Event)) error) *Job {
	if buffer <= 0 {
		buffer = 64
	}
	ctx, cancel := context.WithCancel(parent)
	j := &Job{
		events: make(chan Event, buffer),
		cancel: cancel,
		done:   make(chan struct{}),
	}

	go func() {
		defer close(j.done)
		defer close(j.events)

		emit := func(e Event) {
			select {
			case j.events <- e:
			case <-ctx.Done():
			}
		}

		err := fn(ctx, emit)
		j.mu.Lock()
		j.err = err
		j.mu.Unlock()

		final := Event{Kind: KindDone}
		if err != nil {
			final = Event{Kind: KindFailed, Message: err.Error(), Err: err}
		}
		select {
		case j.events <- final:
		case <-ctx.Done():
		}
	}()

	return j
}

// Events returns the job's event stream, closed when the job finishes.
func (j *Job) Events() <-chan Event { return j.events }

// Cancel requests cancellation; in-flight work stops at the next check.
func (j *Job) Cancel() { j.cancel() }

// Wait blocks until the job finishes and returns its terminal error.
func (j *Job) Wait() error {
	<-j.done
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.err
}
