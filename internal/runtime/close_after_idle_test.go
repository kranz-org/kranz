package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kranz-org/kranz/internal/app"
	"github.com/kranz-org/kranz/internal/config"
)

// TestCloseAfterIdleWaitsForInFlightRequest proves the mechanism the
// runtime switcher relies on to retire a connection it has switched away
// from (PRD 3.3: "an operation already accepted by the supervisor keeps
// running after the user leaves"). A request already on the wire when
// CloseAfterIdle is called must still get its own answer, not a
// "connection closed" error manufactured by the teardown itself.
func TestCloseAfterIdleWaitsForInFlightRequest(t *testing.T) {
	cfg := &config.Config{Project: "CloseIdle", Services: map[string]config.Service{
		// Never started, so "running" never becomes true: Wait blocks for its
		// whole timeout, giving a deterministic in-flight request to close
		// the connection underneath.
		"idle": {Command: "sleep 60", Dir: ".", Shell: "sh"},
	}}
	client, cleanup := startTestSupervisor(t, cfg, nil)
	defer cleanup()

	type waitOutcome struct {
		result app.WaitResult
		err    error
	}
	outcome := make(chan waitOutcome, 1)
	go func() {
		result, err := client.Wait(context.Background(), app.WaitRequest{
			Selectors: []string{"idle"}, Condition: "running", Timeout: 300 * time.Millisecond,
		})
		outcome <- waitOutcome{result, err}
	}()

	// Give the request time to actually reach the server before retiring the
	// connection, so this exercises "close while in flight" rather than
	// "close before the request was even sent".
	time.Sleep(50 * time.Millisecond)
	client.CloseAfterIdle(5 * time.Second)

	select {
	case got := <-outcome:
		var waitErr *app.WaitError
		if !errors.As(got.err, &waitErr) {
			t.Fatalf("Wait returned %v (%#v), want its own timeout WaitError, not a connection-closed error", got.err, got.result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Wait never returned; CloseAfterIdle blocked or aborted it")
	}

	select {
	case <-client.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("the connection was never actually closed once idle")
	}
}

// TestCloseAfterIdleTimeoutClosesAWedgedConnection proves the backstop: a
// request that never returns does not keep the connection open forever.
func TestCloseAfterIdleTimeoutClosesAWedgedConnection(t *testing.T) {
	cfg := &config.Config{Project: "WedgedIdle", Services: map[string]config.Service{}}
	client, cleanup := startTestSupervisor(t, cfg, nil)
	defer cleanup()

	client.inFlight.Add(1) // simulates a call that will never itself return
	defer client.inFlight.Done()

	client.CloseAfterIdle(50 * time.Millisecond)
	select {
	case <-client.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("CloseAfterIdle's timeout backstop never closed the connection")
	}
}

func TestRetainCoversARequestScheduledBeforeRetirement(t *testing.T) {
	cfg := &config.Config{Project: "ScheduledIdle", Services: map[string]config.Service{
		"idle": {Command: "sleep 60", Dir: ".", Shell: "sh"},
	}}
	client, cleanup := startTestSupervisor(t, cfg, nil)
	defer cleanup()

	release := client.Retain()
	start := make(chan struct{})
	outcome := make(chan error, 1)
	go func() {
		<-start
		defer release()
		_, err := client.Wait(context.Background(), app.WaitRequest{
			Selectors: []string{"idle"}, Condition: "running", Timeout: 100 * time.Millisecond,
		})
		outcome <- err
	}()

	client.CloseAfterIdle(0)
	select {
	case <-client.Done():
		t.Fatal("retired connection closed before its scheduled request started")
	case <-time.After(20 * time.Millisecond):
	}
	close(start)
	select {
	case err := <-outcome:
		var waitErr *app.WaitError
		if !errors.As(err, &waitErr) {
			t.Fatalf("scheduled request returned %v, want its own WaitError", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("scheduled request never completed")
	}
	select {
	case <-client.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("retained connection did not close after the scheduled request completed")
	}
}
