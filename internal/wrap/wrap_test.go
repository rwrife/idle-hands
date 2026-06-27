package wrap

import (
	"bytes"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// drainTap collects everything sent to a tap channel until it's closed.
type tapSink struct {
	mu   sync.Mutex
	buf  bytes.Buffer
	done chan struct{}
}

func newTapSink(ch <-chan []byte) *tapSink {
	s := &tapSink{done: make(chan struct{})}
	go func() {
		for chunk := range ch {
			s.mu.Lock()
			s.buf.Write(chunk)
			s.mu.Unlock()
		}
		close(s.done)
	}()
	return s
}

func (s *tapSink) wait() string {
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// sh returns a Config that runs a small shell snippet portably.
func shConfig(script string, tap chan []byte) Config {
	if runtime.GOOS == "windows" {
		return Config{Name: "cmd", Args: []string{"/c", script}, Tap: tap}
	}
	return Config{Name: "sh", Args: []string{"-c", script}, Tap: tap}
}

func TestRunExitZero(t *testing.T) {
	res, err := Run(shConfig("exit 0", nil))
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", res.ExitCode)
	}
}

func TestRunExitCodePropagates(t *testing.T) {
	res, err := Run(shConfig("exit 7", nil))
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if res.ExitCode != 7 {
		t.Fatalf("ExitCode = %d, want 7", res.ExitCode)
	}
}

func TestRunCapturesOutputViaTap(t *testing.T) {
	tap := make(chan []byte, 64)
	sink := newTapSink(tap)

	// Echo a known marker; the tap must observe it.
	cfg := shConfig("echo idle-hands-marker", tap)
	// Discard the "real" stdout so the test stays quiet.
	cfg.Stdout = &bytes.Buffer{}
	cfg.Stderr = &bytes.Buffer{}

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", res.ExitCode)
	}

	got := sink.wait()
	if !strings.Contains(got, "idle-hands-marker") {
		t.Fatalf("tap output %q does not contain marker", got)
	}
}

func TestRunTapClosedOnFinish(t *testing.T) {
	tap := make(chan []byte, 8)
	cfg := shConfig("echo hi", tap)
	cfg.Stdout = &bytes.Buffer{}
	cfg.Stderr = &bytes.Buffer{}

	if _, err := Run(cfg); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	// After Run returns the tap must be closed; a receive should not block and
	// must report closed once drained.
	for {
		if _, ok := <-tap; !ok {
			return // closed as expected
		}
	}
}

func TestRunCommandNotFound(t *testing.T) {
	_, err := Run(Config{Name: "definitely-not-a-real-binary-xyz"})
	if err == nil {
		t.Fatal("expected error for missing command, got nil")
	}
}

func TestRunUsesPTYOnUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no PTY on windows")
	}
	cfg := shConfig("exit 0", nil)
	cfg.Stdout = &bytes.Buffer{}
	cfg.Stderr = &bytes.Buffer{}
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if !res.PTY {
		t.Fatal("expected PTY=true on unix")
	}
}

func TestFallbackExitCode(t *testing.T) {
	// Exercise the fallback path directly so it's covered on all platforms.
	res, err := runFallback(shConfig("exit 5", nil))
	if err != nil {
		t.Fatalf("runFallback error: %v", err)
	}
	if res.ExitCode != 5 {
		t.Fatalf("fallback ExitCode = %d, want 5", res.ExitCode)
	}
	if res.PTY {
		t.Fatal("fallback should report PTY=false")
	}
}
