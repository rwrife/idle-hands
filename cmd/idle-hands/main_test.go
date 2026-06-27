package main

import (
	"runtime"
	"testing"
	"time"
)

func TestRunVersion(t *testing.T) {
	if code := run([]string{"version"}); code != 0 {
		t.Fatalf("version exit code = %d, want 0", code)
	}
}

func TestRunHelp(t *testing.T) {
	if code := run([]string{"help"}); code != 0 {
		t.Fatalf("help exit code = %d, want 0", code)
	}
}

func TestRunNoArgs(t *testing.T) {
	if code := run(nil); code != 2 {
		t.Fatalf("no-args exit code = %d, want 2", code)
	}
}

func TestRunUnknown(t *testing.T) {
	if code := run([]string{"definitely-not-a-command"}); code != 2 {
		t.Fatalf("unknown exit code = %d, want 2", code)
	}
}

func TestWatchTransparentSuccess(t *testing.T) {
	// `watch -- <true>` should run the command and exit 0.
	cmd := "true"
	if runtime.GOOS == "windows" {
		// `cmd /c exit 0` is the portable success on Windows runners.
		if code := run([]string{"watch", "--", "cmd", "/c", "exit", "0"}); code != 0 {
			t.Fatalf("watch success exit code = %d, want 0", code)
		}
		return
	}
	if code := run([]string{"watch", "--", cmd}); code != 0 {
		t.Fatalf("watch success exit code = %d, want 0", code)
	}
}

func TestWatchPropagatesExitCode(t *testing.T) {
	if runtime.GOOS == "windows" {
		if code := run([]string{"watch", "--", "cmd", "/c", "exit", "3"}); code != 3 {
			t.Fatalf("watch exit code = %d, want 3", code)
		}
		return
	}
	// `sh -c 'exit 3'` is portable across macOS/Linux.
	if code := run([]string{"watch", "--", "sh", "-c", "exit 3"}); code != 3 {
		t.Fatalf("watch exit code = %d, want 3", code)
	}
}

func TestWatchNoCommand(t *testing.T) {
	if code := run([]string{"watch"}); code != 2 {
		t.Fatalf("watch no-command exit code = %d, want 2", code)
	}
	if code := run([]string{"watch", "--"}); code != 2 {
		t.Fatalf("watch bare -- exit code = %d, want 2", code)
	}
}

// TestWatchMissingCommand guards against a regression where a child that fails
// to start would leave the output tap unclosed and deadlock the drain in
// cmdWatch. It must return promptly with a non-zero exit code.
func TestWatchMissingCommand(t *testing.T) {
	done := make(chan int, 1)
	go func() {
		done <- run([]string{"watch", "--", "idle-hands-no-such-binary-xyz"})
	}()
	select {
	case code := <-done:
		if code == 0 {
			t.Fatalf("missing-command exit code = %d, want non-zero", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("watch with missing command hung (tap not closed?)")
	}
}
