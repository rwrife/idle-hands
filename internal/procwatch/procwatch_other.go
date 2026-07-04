//go:build !linux

package procwatch

// NewNameSampler on platforms without a native CPU sampler returns an
// unsupportedSampler. Construction itself succeeds so the caller can print a
// consistent, caveat-aware message via IsUnsupported once it actually polls,
// rather than the package pretending the feature exists. A real macOS
// (libproc / `ps`) and Windows (PDH / Toolhelp) sampler are tracked as
// follow-ups on issue #10; the portable Poller and detector wiring already work
// against any Sampler, so adding them is self-contained.
func NewNameSampler(name string) (Sampler, error) {
	return unsupportedSampler{name: name}, nil
}
