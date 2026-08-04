package main

import "testing"

// TestResolveVersion covers the --version fallback for binaries built
// without release ldflags (go install .../cmd/gha-doctor@vX.Y.Z).
func TestResolveVersion(t *testing.T) {
	cases := []struct {
		name, ldflags, buildInfo, want string
	}{
		{"release ldflags win", "0.58.1", "v0.58.1", "0.58.1"},
		{"ldflags win even over newer buildinfo", "0.58.1", "v0.99.0", "0.58.1"},
		{"go install module version", "dev", "v0.99.0", "0.99.0"},
		{"pseudo-version from local build", "dev", "v0.58.2-0.20260804041721-c9e81a517835+dirty", "0.58.2-0.20260804041721-c9e81a517835+dirty"},
		{"devel placeholder stays dev", "dev", "(devel)", "dev"},
		{"empty buildinfo stays dev", "dev", "", "dev"},
	}
	for _, c := range cases {
		if got := resolveVersion(c.ldflags, c.buildInfo); got != c.want {
			t.Errorf("%s: resolveVersion(%q, %q) = %q, want %q", c.name, c.ldflags, c.buildInfo, got, c.want)
		}
	}
}
