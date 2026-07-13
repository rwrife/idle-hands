package signal

import (
	"net"
	"os"

	"github.com/rwrife/idle-hands/internal/config"
)

// ensureDir creates the idle-hands state directory (0700) if it does not exist,
// so the socket can be bound on a fresh install where ~/.idle-hands has never
// been written. It returns the directory path.
func ensureDir() (string, error) {
	dir, err := config.DirPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// Handler consumes decoded events from the listener. It returns the detector
// transition to dispatch (and whether one occurred); the server calls it under
// its single read goroutine, so it need not be concurrency-safe.
type Handler func(Event)

// Listener is the transport-independent surface the server loop uses, so the
// Unix-socket and TCP-fallback backends share one Serve implementation.
type Listener interface {
	// Accept blocks for the next client connection.
	Accept() (net.Conn, error)
	// Close stops listening and removes any on-disk socket file.
	Close() error
	// Addr describes where the listener is reachable, for the startup notice.
	Addr() string
}
