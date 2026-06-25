package sql

import (
	"strings"
	"testing"
)

func TestSubstituteBasic(t *testing.T) {
	ph := map[string]string{"schema": "app", "env": "prod"}
	got, err := Substitute("CREATE TABLE ${schema}.t (env text default '${env}');", ph, "${", "}", true)
	if err != nil {
		t.Fatal(err)
	}
	want := "CREATE TABLE app.t (env text default 'prod');"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSubstituteUnknownPlaceholderErrors(t *testing.T) {
	_, err := Substitute("SELECT ${missing};", map[string]string{}, "${", "}", true)
	if err == nil {
		t.Fatal("expected error for unknown placeholder")
	}
	if !strings.Contains(err.Error(), "${missing}") {
		t.Errorf("error %q should name the missing placeholder", err.Error())
	}
}

func TestSubstituteDisabled(t *testing.T) {
	in := "SELECT ${missing};"
	got, err := Substitute(in, nil, "${", "}", false)
	if err != nil || got != in {
		t.Errorf("disabled replacement should pass through unchanged: got %q err %v", got, err)
	}
}

func TestSubstituteNoClosingSuffix(t *testing.T) {
	// A lone prefix with no suffix is not a placeholder and must not error.
	in := "SELECT 100 ${ not closed"
	got, err := Substitute(in, map[string]string{}, "${", "}", true)
	if err != nil || got != in {
		t.Errorf("unterminated placeholder should pass through: got %q err %v", got, err)
	}
}

func TestSubstituteCustomDelimiters(t *testing.T) {
	got, err := Substitute("@@name@@", map[string]string{"name": "x"}, "@@", "@@", true)
	if err != nil {
		t.Fatal(err)
	}
	if got != "x" {
		t.Errorf("got %q, want %q", got, "x")
	}
}
