package signal

import (
	"bufio"
	"net"
)

// Serve accepts client connections on ln and, for each line received, parses an
// event and passes it to onEvent; parse errors go to onError (which may be nil
// to ignore them). It runs until ln.Close causes Accept to fail, then returns.
// Reads are serialized through a single goroutine loop so onEvent — which feeds
// the shared, non-concurrency-safe card/stats handler — is only ever called
// from one goroutine.
//
// Connections are handled inline (one line-oriented request stream at a time):
// signal clients are trivial, high-latency, low-volume event senders, so the
// simplicity of a serial loop beats a connection-per-goroutine fan-out here and
// keeps the coordinator's single-goroutine contract intact without locks.
func Serve(ln Listener, onEvent func(Event), onError func(error)) error {
	for {
		conn, err := ln.Accept()
		if err != nil {
			// A Close on the listener surfaces as an accept error; that's the
			// normal shutdown path, so return nil rather than treating it as a
			// failure. Callers distinguish shutdown via their own stop signal.
			if isClosedErr(err) {
				return nil
			}
			return err
		}
		serveConn(conn, onEvent, onError)
	}
}

// serveConn drains one connection line by line, dispatching each parsed event.
// A client may send many events on one long-lived connection (e.g. an IDE
// extension holding the socket open) or one event per connection (the CLI
// client); both work because we simply read to EOF.
func serveConn(conn net.Conn, onEvent func(Event), onError func(error)) {
	defer conn.Close()
	sc := bufio.NewScanner(conn)
	for sc.Scan() {
		ev, err := ParseEvent(sc.Bytes())
		if err != nil {
			if err == ErrEmpty {
				continue // keep-alive newline; ignore
			}
			if onError != nil {
				onError(err)
			}
			continue
		}
		onEvent(ev)
	}
}

// isClosedErr reports whether err is the "use of closed network connection"
// that a Listener.Close triggers in a blocked Accept, so Serve can treat it as
// a clean stop.
func isClosedErr(err error) bool {
	return err != nil && (err == net.ErrClosed ||
		// net.ErrClosed wrapping isn't guaranteed on every backend; match text
		// as a fallback so shutdown stays clean everywhere.
		containsClosed(err.Error()))
}

func containsClosed(s string) bool {
	const want = "use of closed network connection"
	return len(s) >= len(want) && indexOf(s, want) >= 0
}

// indexOf is a tiny substring search kept local so this file doesn't pull in
// strings for one call.
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
