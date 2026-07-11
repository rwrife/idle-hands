package signal

import (
	"errors"
	"syscall"
)

// isAddrInUse reports whether err is an "address already in use" bind failure,
// which for a path-bound Unix socket means either a live listener or a stale
// socket file. It is used to decide whether to probe-and-clean a stale socket.
func isAddrInUse(err error) bool {
	return errors.Is(err, syscall.EADDRINUSE)
}
