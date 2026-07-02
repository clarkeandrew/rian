package main

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestSplitCommand(t *testing.T) {
	tests := []struct {
		name     string
		in       []string
		wantCmd  string
		wantRest []string
	}{
		{"command first", []string{"migrate", "-url=x"}, "migrate", []string{"-url=x"}},
		{"flags before command ('=' form)", []string{"-url=x", "validate", "-user=sa"}, "validate", []string{"-url=x", "-user=sa"}},
		{"no command", []string{"-url=x"}, "", []string{"-url=x"}},
		{"empty", nil, "", nil},
		{"terminator stops the search", []string{"--", "migrate"}, "", []string{"--", "migrate"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, rest := splitCommand(tt.in)
			if cmd != tt.wantCmd || !reflect.DeepEqual(rest, tt.wantRest) {
				t.Errorf("splitCommand(%v) = %q,%v want %q,%v", tt.in, cmd, rest, tt.wantCmd, tt.wantRest)
			}
		})
	}
}

func TestRunVersion(t *testing.T) {
	for _, args := range [][]string{{"--version"}, {"-version"}, {"-v"}} {
		var out, errOut bytes.Buffer
		if err := run(args, &out, &errOut); err != nil {
			t.Fatalf("run(%v): %v", args, err)
		}
		if !strings.Contains(out.String(), "rian version") {
			t.Errorf("run(%v) stdout = %q, want version string", args, out.String())
		}
	}
}

func TestRunHelpAndNoArgs(t *testing.T) {
	for _, args := range [][]string{{"-h"}, {"--help"}, nil} {
		var out, errOut bytes.Buffer
		if err := run(args, &out, &errOut); err != nil {
			t.Fatalf("run(%v): %v", args, err)
		}
		for _, frag := range []string{"Usage:", "migrate", "repair", "-url"} {
			if !strings.Contains(out.String(), frag) {
				t.Errorf("run(%v) usage missing %q:\n%s", args, frag, out.String())
			}
		}
	}
}

func TestRunUnknownCommand(t *testing.T) {
	var out, errOut bytes.Buffer
	err := run([]string{"clean"}, &out, &errOut)
	if err == nil || !strings.Contains(err.Error(), `unknown command "clean"`) {
		t.Fatalf("expected unknown-command error, got %v", err)
	}
}

func TestRunUnknownFlagIsUsageError(t *testing.T) {
	var out, errOut bytes.Buffer
	err := run([]string{"migrate", "-nope"}, &out, &errOut)
	if !errors.Is(err, errUsage) {
		t.Fatalf("expected errUsage, got %v", err)
	}
	if !strings.Contains(errOut.String(), "-nope") {
		t.Errorf("stderr should mention the bad flag: %q", errOut.String())
	}
}

// TestRunMissingURL exercises the whole parse -> config path: flags are
// accepted (in Flyway single-dash form) and the run fails at connect time with
// the missing-url error rather than a parse error.
func TestRunMissingURL(t *testing.T) {
	var out, errOut bytes.Buffer
	err := run([]string{"migrate", "-placeholders", "a=b", "-locations", "filesystem:x"}, &out, &errOut)
	if err == nil || !strings.Contains(err.Error(), "no database url") {
		t.Fatalf("expected missing-url error, got %v", err)
	}
}

func TestListFlag(t *testing.T) {
	var l listFlag
	_ = l.Set("filesystem:a, filesystem:b")
	_ = l.Set("filesystem:c")
	want := listFlag{"filesystem:a", "filesystem:b", "filesystem:c"}
	if !reflect.DeepEqual(l, want) {
		t.Errorf("listFlag = %v, want %v", l, want)
	}
}

func TestMapFlag(t *testing.T) {
	m := mapFlag{}
	if err := m.Set("env=prod,kv=x=y"); err != nil {
		t.Fatal(err)
	}
	if err := m.Set("region=eu"); err != nil {
		t.Fatal(err)
	}
	want := mapFlag{"env": "prod", "kv": "x=y", "region": "eu"}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("mapFlag = %v, want %v", m, want)
	}
	if err := m.Set("novalue"); err == nil {
		t.Error("expected error for pair without '='")
	}
}
