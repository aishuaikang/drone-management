package main

import "testing"

func TestCleanRemotePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "empty", input: " ", want: ""},
		{name: "relative", input: "opt/drone-management", want: "/opt/drone-management"},
		{name: "slashes", input: "//opt//drone-management//", want: "/opt/drone-management"},
		{name: "root", input: "/", want: "/"},
		{name: "windows separators", input: `\opt\drone-management`, want: "/opt/drone-management"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := cleanRemotePath(tt.input); got != tt.want {
				t.Fatalf("cleanRemotePath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestShellQuote(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "empty", input: "", want: "''"},
		{name: "simple", input: "/spbatc/drone-management", want: "'/spbatc/drone-management'"},
		{name: "single quote", input: "/tmp/ask's/pkg.tar.gz", want: "'/tmp/ask'\\''s/pkg.tar.gz'"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := shellQuote(tt.input); got != tt.want {
				t.Fatalf("shellQuote(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestRemoteJoin(t *testing.T) {
	t.Parallel()

	if got := remoteJoin("/opt/", "/drone-management/", "data"); got != "/opt/drone-management/data" {
		t.Fatalf("remoteJoin() = %q", got)
	}
}
