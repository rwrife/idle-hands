//go:build linux

package procwatch

import (
	"os/exec"
	"testing"
	"time"
)

// TestNameSamplerResolvesRunningProcess spawns a short-lived child and verifies
// NewNameSampler can find it by name, that Sample reports it alive with a
// non-negative CPU time, and that once the child exits Sample reports it gone
// (Alive=false, nil error) so the Poller can cleanly report Exited.
func TestNameSamplerResolvesRunningProcess(t *testing.T) {
	// `sleep` is ubiquitous on Linux CI runners. Give it long enough that the
	// scan reliably sees it, but the test still finishes fast once we kill it.
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start helper process: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	// Give the kernel a beat to populate /proc for the child.
	time.Sleep(50 * time.Millisecond)

	s, err := NewNameSampler("sleep")
	if err != nil {
		t.Fatalf("NewNameSampler(sleep) = %v", err)
	}
	sample, err := s.Sample()
	if err != nil {
		t.Fatalf("Sample() = %v", err)
	}
	if !sample.Alive {
		t.Fatal("expected running sleep to be Alive")
	}
	if sample.CPU < 0 {
		t.Fatalf("CPU = %v, want >= 0", sample.CPU)
	}

	// Kill it and confirm the sampler eventually reports it gone.
	_ = cmd.Process.Kill()
	_ = cmd.Wait()

	deadline := time.Now().Add(2 * time.Second)
	for {
		sample, err := s.Sample()
		if err != nil {
			t.Fatalf("Sample() after kill = %v", err)
		}
		if !sample.Alive {
			return // success: reported gone
		}
		if time.Now().After(deadline) {
			t.Fatal("sampler never reported the killed process as gone")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestNameSamplerNotFound verifies an unmatched name returns ErrNotFound so the
// command layer can print a helpful "start it first" message.
func TestNameSamplerNotFound(t *testing.T) {
	_, err := NewNameSampler("idle-hands-no-such-process-xyzzy-42")
	if err == nil {
		t.Fatal("expected ErrNotFound for a bogus name")
	}
	if !IsNotFound(err) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// IsNotFound is a tiny local helper mirroring errors.Is(err, ErrNotFound) so the
// test reads clearly; it is not part of the package API.
func IsNotFound(err error) bool {
	for err != nil {
		if err == ErrNotFound {
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
