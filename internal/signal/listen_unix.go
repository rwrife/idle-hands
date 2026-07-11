//go:build !windows

package signal

import (
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/rwrife/idle-hands/internal/config"
)

// SocketPath returns the absolute path to the plugin-signal Unix socket
// (~/.idle-hands/signal.sock). It is exported so both the server and the CLI
// client resolve the same location.
func SocketPath() (string, error) {
	dir, err := config.DirPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, SocketName), nil
}

// Listen binds the plugin-signal Unix domain socket at ~/.idle-hands/signal.sock
// with 0600 permissions (user-owned, local-only, no network). It handles the
// stale-socket case the issue calls out: if a previous run crashed and left the
// socket file behind, a fresh bind would fail with EADDRINUSE, so we probe it —
// if nothing is actually listening, the file is stale and is removed and the
// bind retried; if something *is* listening, another idle-hands is already up
// and we report that clearly instead of stealing its socket.
func Listen() (Listener, error) {
	if _, err := ensureDir(); err != nil {
		return nil, err
	}
	path, err := SocketPath()
	if err != nil {
		return nil, err
	}

	ln, err := net.Listen("unix", path)
	if err != nil {
		if isAddrInUse(err) && staleSocket(path) {
			// Left over from a crashed run: clear it and try once more.
			_ = os.Remove(path)
			ln, err = net.Listen("unix", path)
		}
		if err != nil {
			if isAddrInUse(err) {
				return nil, fmt.Errorf("signal: another idle-hands signal listener is already running at %s", path)
			}
			return nil, fmt.Errorf("signal: bind %s: %w", path, err)
		}
	}

	// Lock the socket down to the owner: local-only, no group/other access.
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("signal: secure %s: %w", path, err)
	}

	return &unixListener{ln: ln.(*net.UnixListener), path: path}, nil
}

// staleSocket reports whether the socket file at path exists but has no live
// listener behind it (a dial is refused). Only then is removing it safe.
func staleSocket(path string) bool {
	if _, err := os.Stat(path); err != nil {
		return false // nothing there; not "stale", just absent
	}
	c, err := net.Dial("unix", path)
	if err != nil {
		return true // present but nobody listening → stale
	}
	_ = c.Close()
	return false // someone is actually serving it
}

// unixListener adapts *net.UnixListener to the Listener interface, removing the
// socket file on Close so a clean shutdown never leaves a stale file behind.
type unixListener struct {
	ln   *net.UnixListener
	path string
}

func (u *unixListener) Accept() (net.Conn, error) { return u.ln.Accept() }

func (u *unixListener) Close() error {
	err := u.ln.Close()
	// SetUnlinkOnClose defaults on for a path-bound listener, but remove
	// explicitly too so a stale file never survives an abrupt Close path.
	_ = os.Remove(u.path)
	return err
}

func (u *unixListener) Addr() string { return u.path }

// Dial connects to the running listener's Unix socket so the CLI client can
// deliver one event. A refused dial means no listener is running.
func Dial() (net.Conn, error) {
	path, err := SocketPath()
	if err != nil {
		return nil, err
	}
	c, err := net.Dial("unix", path)
	if err != nil {
		return nil, fmt.Errorf("signal: no listener at %s (start one with `idle-hands signal` first): %w", path, err)
	}
	return c, nil
}
