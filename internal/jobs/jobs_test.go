package jobs

import (
	"context"
	"errors"
	"testing"
)

func collect(j *Job) []Event {
	var evs []Event
	for e := range j.Events() {
		evs = append(evs, e)
	}
	return evs
}

func TestRun_SuccessEmitsTerminalDone(t *testing.T) {
	j := Run(context.Background(), 8, func(ctx context.Context, emit func(Event)) error {
		emit(Event{Kind: KindProgress, Message: "one"})
		emit(Event{Kind: KindProgress, Message: "two"})
		return nil
	})
	evs := collect(j)
	if err := j.Wait(); err != nil {
		t.Fatalf("Wait = %v, want nil", err)
	}
	if len(evs) != 3 || evs[2].Kind != KindDone {
		t.Fatalf("events = %+v, want 2 progress + terminal done", evs)
	}
}

func TestRun_FailurePropagates(t *testing.T) {
	sentinel := errors.New("boom")
	j := Run(context.Background(), 8, func(ctx context.Context, emit func(Event)) error {
		return sentinel
	})
	evs := collect(j)
	if err := j.Wait(); !errors.Is(err, sentinel) {
		t.Fatalf("Wait = %v, want %v", err, sentinel)
	}
	last := evs[len(evs)-1]
	if last.Kind != KindFailed || last.Err == nil {
		t.Fatalf("terminal event = %+v, want failed with error", last)
	}
}

func TestRun_CancelStopsWorker(t *testing.T) {
	started := make(chan struct{})
	j := Run(context.Background(), 1, func(ctx context.Context, emit func(Event)) error {
		close(started)
		<-ctx.Done() // block until cancelled
		return ctx.Err()
	})
	<-started
	j.Cancel()
	collect(j)
	if err := j.Wait(); !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait = %v, want context.Canceled", err)
	}
}
