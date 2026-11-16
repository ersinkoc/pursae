package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"wpgo/internal/config"
	"wpgo/internal/server"
)

func TestProgName(t *testing.T) {
	cases := map[string]string{
		"pursae":                          "pursae",
		"/usr/local/bin/pursae":           "pursae",
		"pursae-linux-amd64":              "pursae",
		"./dist/pursae-darwin-arm64":      "pursae",
		`C:\bin\pursae-windows-amd64.exe`: "pursae",
		"Pursae.EXE":                      "pursae",
		"wpgo":                            "wpgo",
		"/usr/local/lib/pursae/wpgo":      "wpgo",
		"bin/wpgo":                        "wpgo",
		"wpgo.test":                       "wpgo",
		"wpgo-linux-arm64":                "wpgo",
		"main":                            "pursae",
	}
	for in, want := range cases {
		if got := progName(in); got != want {
			t.Errorf("progName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRenameUsage(t *testing.T) {
	if got := renameUsage("usage: wpgo adminsec x", "pursae"); got != "usage: pursae adminsec x" {
		t.Error(got)
	}
	if got := renameUsage("usage: wpgo adminsec x", "wpgo"); got != "usage: wpgo adminsec x" {
		t.Error(got)
	}
}

// The command line's help works without a database and names the invoked program.
func TestCLIHelpWithoutDatabase(t *testing.T) {
	cfg := config.Config{DatabaseURL: "postgres://invalid.invalid:1/none"}
	for _, prog := range []string{"pursae", "wpgo"} {
		var out, errb bytes.Buffer
		code := server.CLI(context.Background(), cfg, prog, []string{"help"}, strings.NewReader(""), &out, &errb)
		if code != 0 {
			t.Fatalf("%s help: code %d, stderr %s", prog, code, errb.String())
		}
		for _, want := range []string{"usage: " + prog + " <command>", "serve", "migrate", "headless", "plugin"} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("%s help missing %q:\n%s", prog, want, out.String())
			}
		}
	}
	var out, errb bytes.Buffer
	if code := server.CLI(context.Background(), cfg, "pursae", []string{"help", "plugin"}, nil, &out, &errb); code != 0 || !strings.Contains(out.String(), "keygen") {
		t.Errorf("help plugin: %d %s %s", code, out.String(), errb.String())
	}
	out.Reset()
	errb.Reset()
	if code := server.CLI(context.Background(), cfg, "pursae", []string{"sevre"}, nil, &out, &errb); code != 2 || !strings.Contains(errb.String(), "Did you mean 'serve'?") {
		t.Errorf("unknown command: %d %q", code, errb.String())
	}
}

func TestHelpTopic(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"setup-token", "--help"}, "setup-token"},
		{[]string{"migrate", "-h"}, "migrate"},
		{[]string{"plugin", "pack", "--help"}, "plugin pack"},
		{[]string{"plugin", "list", "--help"}, ""}, // the CLI handles it
		{[]string{"post", "list", "--help"}, ""},
		{[]string{"setup-token"}, ""},
		{[]string{"headless", "export", "--help"}, ""}, // only right after the command
	}
	for _, c := range cases {
		if got := strings.Join(helpTopic(c.args), " "); got != c.want {
			t.Errorf("helpTopic(%v) = %q, want %q", c.args, got, c.want)
		}
	}
}
