package api

import "testing"

func TestResolveEndpoint(t *testing.T) {
	cases := []struct {
		name, ghHost, apiURL, wantHost, wantBase string
	}{
		{"defaults", "", "", "github.com", "https://api.github.com"},
		{"actions runner on github.com", "", "https://api.github.com", "github.com", "https://api.github.com"},
		{"ghes runner sets GITHUB_API_URL", "", "https://ghe.example.com/api/v3", "ghe.example.com", "https://ghe.example.com/api/v3"},
		{"GITHUB_API_URL trailing slash", "", "https://ghe.example.com/api/v3/", "ghe.example.com", "https://ghe.example.com/api/v3"},
		{"GH_HOST alone", "ghe.example.com", "", "ghe.example.com", "https://ghe.example.com/api/v3"},
		{"GH_HOST beats mismatched GITHUB_API_URL", "ghe.example.com", "https://api.github.com", "ghe.example.com", "https://ghe.example.com/api/v3"},
		{"GH_HOST=github.com overrides GHES ambient", "github.com", "https://ghe.example.com/api/v3", "github.com", "https://api.github.com"},
		{"GH_HOST matches GITHUB_API_URL: URL wins verbatim", "ghe.example.com", "https://ghe.example.com/api/v3", "ghe.example.com", "https://ghe.example.com/api/v3"},
		{"GH_HOST case-insensitive", "GHE.Example.COM", "", "ghe.example.com", "https://ghe.example.com/api/v3"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("GH_HOST", c.ghHost)
			t.Setenv("GITHUB_API_URL", c.apiURL)
			host, base := resolveEndpoint()
			if host != c.wantHost || base != c.wantBase {
				t.Errorf("got (%q, %q), want (%q, %q)", host, base, c.wantHost, c.wantBase)
			}
		})
	}
}

func TestNewClientEnterpriseToken(t *testing.T) {
	t.Setenv("GITHUB_API_URL", "")
	t.Setenv("GITHUB_TOKEN", "dotcom-token")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GH_ENTERPRISE_TOKEN", "ghes-token")
	t.Setenv("GITHUB_ENTERPRISE_TOKEN", "")

	t.Setenv("GH_HOST", "")
	if c := NewClient(); c.Token != "dotcom-token" || c.Host != "github.com" {
		t.Errorf("github.com: token %q host %q; GH_ENTERPRISE_TOKEN must not apply", c.Token, c.Host)
	}

	t.Setenv("GH_HOST", "ghe.example.com")
	c := NewClient()
	if c.Token != "ghes-token" {
		t.Errorf("enterprise host: got token %q, want GH_ENTERPRISE_TOKEN to win", c.Token)
	}
	if c.BaseURL != "https://ghe.example.com/api/v3" || c.Host != "ghe.example.com" {
		t.Errorf("enterprise host: base %q host %q", c.BaseURL, c.Host)
	}

	// Without enterprise-specific tokens, GITHUB_TOKEN still applies (it is
	// the native token inside GHES Actions jobs).
	t.Setenv("GH_ENTERPRISE_TOKEN", "")
	if c := NewClient(); c.Token != "dotcom-token" {
		t.Errorf("enterprise host fallback: got token %q, want GITHUB_TOKEN", c.Token)
	}
}
