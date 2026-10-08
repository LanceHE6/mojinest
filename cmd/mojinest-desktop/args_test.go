package main

import (
	"flag"
	"io"
	"testing"
)

// newTestFlagSet mirrors the real flag set closely enough to exercise parsing.
func newTestFlagSet() *flag.FlagSet {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.String("config", "", "")
	fs.String("server", "", "")
	fs.String("token", "", "")
	fs.Bool("quiet", false, "")
	fs.Bool("version", false, "")
	return fs
}

// Go's flag package stops at the first positional argument. The real CLI is
// invoked both ways, and a hotkey binding writes `pick -config …`, so flags
// after the subcommand must not be silently dropped (they were, and the relay
// never learned its port file).
func TestParseArgsAcceptsFlagsBeforeAndAfterSubcommand(t *testing.T) {
	cases := []struct {
		name     string
		argv     []string
		wantSub  string
		wantArgs []string
		wantCfg  string
		wantQuit bool
	}{
		{
			name: "flags first", argv: []string{"-config", "/a.json", "pick"},
			wantSub: "pick", wantCfg: "/a.json",
		},
		{
			name: "flags last", argv: []string{"pick", "-config", "/b.json"},
			wantSub: "pick", wantCfg: "/b.json",
		},
		{
			name: "flags both sides", argv: []string{"-config", "/c.json", "copy", "猫", "-quiet"},
			wantSub: "copy", wantArgs: []string{"猫"}, wantCfg: "/c.json", wantQuit: true,
		},
		{
			name: "equals form", argv: []string{"relay", "-config=/d.json", "-port-file", "/p"},
			wantSub: "relay", wantCfg: "/d.json",
		},
		{
			name: "no subcommand", argv: []string{"-config", "/e.json"},
			wantSub: "", wantCfg: "/e.json",
		},
		{
			name: "multiword query", argv: []string{"copy", "猫咪", "摇头", "-config", "/f.json"},
			wantSub: "copy", wantArgs: []string{"猫咪", "摇头"}, wantCfg: "/f.json",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fs := newTestFlagSet()
			portFile := fs.String("port-file", "", "")
			positionals, err := parseArgs(fs, c.argv)
			if err != nil {
				t.Fatalf("parseArgs: %v", err)
			}
			sub := ""
			if len(positionals) > 0 {
				sub = positionals[0]
			}
			if sub != c.wantSub {
				t.Errorf("subcommand = %q, want %q", sub, c.wantSub)
			}
			gotArgs := positionals
			if sub != "" {
				gotArgs = positionals[1:]
			}
			if len(gotArgs) != len(c.wantArgs) {
				t.Fatalf("args = %v, want %v", gotArgs, c.wantArgs)
			}
			for i := range c.wantArgs {
				if gotArgs[i] != c.wantArgs[i] {
					t.Errorf("args[%d] = %q, want %q", i, gotArgs[i], c.wantArgs[i])
				}
			}
			if got := fs.Lookup("config").Value.String(); got != c.wantCfg {
				t.Errorf("config = %q, want %q", got, c.wantCfg)
			}
			if c.name == "equals form" && *portFile != "/p" {
				t.Errorf("port-file = %q, want /p", *portFile)
			}
			if got := fs.Lookup("quiet").Value.String(); (got == "true") != c.wantQuit {
				t.Errorf("quiet = %q, want %v", got, c.wantQuit)
			}
		})
	}
}

func TestParseArgsUnknownFlagFails(t *testing.T) {
	fs := newTestFlagSet()
	if _, err := parseArgs(fs, []string{"pick", "-nope"}); err == nil {
		t.Error("unknown flag should be an error, not silently ignored")
	}
}

// A flag value must never be mistaken for the subcommand.
func TestParseArgsValueThatLooksLikeSubcommand(t *testing.T) {
	fs := newTestFlagSet()
	positionals, err := parseArgs(fs, []string{"-config", "pick", "copy", "猫"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if len(positionals) == 0 || positionals[0] != "copy" {
		t.Errorf("subcommand = %v, want copy first (the -config value 'pick' must not win)", positionals)
	}
	if got := fs.Lookup("config").Value.String(); got != "pick" {
		t.Errorf("config = %q, want pick", got)
	}
}
