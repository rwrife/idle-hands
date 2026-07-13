//go:build windows

package signal

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
)

// tcpPortFile records the localhost port the Windows fallback bound, so the CLI
// client can find the running listener without a fixed port (which could clash
// with other software). It lives next to where the Unix socket would be.
const tcpPortFile = "signal.port"

// Listen provides the Windows fallback path the issue requires: Windows has no
// Unix domain sockets in the standard net package pre-1.22 on all builds, so we
// bind a loopback-only TCP listener (127.0.0.1:0) and publish the chosen port
// to ~/.idle-hands/signal.port. Binding to 127.0.0.1 keeps it local-only, the
// same security posture as the 0600 Unix socket, and is documented as the
// opt-in localhost-TCP mode in the README.
func Listen() (Listener, error) {
	dir, err := ensureDir()
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("signal: bind loopback: %w", err)
	}
	portPath := filepath.Join(dir, tcpPortFile)
	addr := ln.Addr().(*net.TCPAddr)
	if err := os.WriteFile(portPath, []byte(fmt.Sprintf("%d\n", addr.Port)), 0o600); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("signal: publish port: %w", err)
	}
	return &tcpListener{ln: ln, portPath: portPath, addr: addr.String()}, nil
}

type tcpListener struct {
	ln       net.Listener
	portPath string
	addr     string
}

func (t *tcpListener) Accept() (net.Conn, error) { return t.ln.Accept() }

func (t *tcpListener) Close() error {
	err := t.ln.Close()
	_ = os.Remove(t.portPath) // clean shutdown removes the published port
	return err
}

func (t *tcpListener) Addr() string { return "127.0.0.1:" + strings.TrimPrefix(t.addr, "127.0.0.1:") }

// SocketPath on Windows points at the published-port file, so error messages
// and docs have a concrete path to name even though the transport is TCP.
func SocketPath() (string, error) {
	dir, err := ensureDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, tcpPortFile), nil
}

// Dial reads the published port and connects to the loopback listener so the
// CLI client works identically to the Unix path.
func Dial() (net.Conn, error) {
	path, err := SocketPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("signal: no listener (start one with `idle-hands signal` first): %w", err)
	}
	port := strings.TrimSpace(string(data))
	c, err := net.Dial("tcp", "127.0.0.1:"+port)
	if err != nil {
		return nil, fmt.Errorf("signal: no listener at 127.0.0.1:%s (start one with `idle-hands signal` first): %w", port, err)
	}
	return c, nil
}
