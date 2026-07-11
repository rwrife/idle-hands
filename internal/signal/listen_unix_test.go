//go:build !windows

package signal

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestListenAndServe exercises the real Unix-socket transport end to end: bind
// a listener under a temp HOME, connect as the CLI client would, send a busy
// then an idle event, and confirm both are decoded and dispatched in order.
func TestListenAndServe(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	ln, err := Listen()
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	// The socket must exist with 0600 perms (user-owned, local-only).
	path, err := SocketPath()
	if err != nil {
		t.Fatalf("SocketPath: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("socket perms = %o, want 600", perm)
	}

	var mu sync.Mutex
	var got []State
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = Serve(ln, func(ev Event) {
			mu.Lock()
			got = append(got, ev.State)
			mu.Unlock()
		}, func(error) {})
	}()

	conn, err := Dial()
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	if _, err := conn.Write(Encode(Event{State: Busy, Source: "cli"})); err != nil {
		t.Fatalf("write busy: %v", err)
	}
	if _, err := conn.Write(Encode(Event{State: Idle, Source: "cli"})); err != nil {
		t.Fatalf("write idle: %v", err)
	}
	conn.Close()

	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(got) == 2
	})
	mu.Lock()
	if got[0] != Busy || got[1] != Idle {
		mu.Unlock()
		t.Fatalf("events = %v, want [busy idle]", got)
	}
	mu.Unlock()

	// Clean shutdown must remove the socket file (no stale leftover).
	if err := ln.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	<-done
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("socket file not removed on Close: err=%v", err)
	}
}

// TestStaleSocketReplaced covers the crash-recovery path: a leftover socket
// file with no live listener behind it must be detected as stale and replaced,
// so a restart after a crash succeeds instead of failing with EADDRINUSE.
func TestStaleSocketReplaced(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir := filepath.Join(home, ".idle-hands")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, SocketName)
	// A plain file at the socket path simulates a stale leftover (nothing is
	// listening on it).
	if err := os.WriteFile(path, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}

	if !staleSocket(path) {
		t.Fatal("a leftover file with no listener should be reported stale")
	}

	ln, err := Listen()
	if err != nil {
		t.Fatalf("Listen over stale socket: %v", err)
	}
	defer ln.Close()

	// And with a live listener bound, the socket is no longer stale.
	if staleSocket(path) {
		t.Fatal("a live listener's socket must not be reported stale")
	}
}

// TestListenRejectsSecondInstance ensures we never steal a socket that another
// live listener owns.
func TestListenRejectsSecondInstance(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	ln, err := Listen()
	if err != nil {
		t.Fatalf("first Listen: %v", err)
	}
	defer ln.Close()

	if _, err := Listen(); err == nil {
		t.Fatal("second Listen should fail while the first is live")
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met before deadline")
}
