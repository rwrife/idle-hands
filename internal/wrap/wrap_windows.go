//go:build windows

package wrap

import "fmt"

// ProbePTY reports the same platform limitation as Run's stdio fallback.
func ProbePTY() error {
	return fmt.Errorf("native PTY integration is unavailable on Windows; watch uses stdio passthrough")
}

// Run on Windows uses direct stdio passthrough. A full ConPTY integration is
// possible but out of scope for M2; the fallback keeps the wrapper functional
// (transparent I/O + exit code + output tap) on Windows today.
func Run(cfg Config) (Result, error) {
	return runFallback(cfg)
}
