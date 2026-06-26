//go:build windows

package wrap

// Run on Windows uses direct stdio passthrough. A full ConPTY integration is
// possible but out of scope for M2; the fallback keeps the wrapper functional
// (transparent I/O + exit code + output tap) on Windows today.
func Run(cfg Config) (Result, error) {
	return runFallback(cfg)
}
