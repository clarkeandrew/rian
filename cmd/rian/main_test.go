package main

import (
	"reflect"
	"testing"
)

func TestNormalizeFlywayArgs(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{
			"flyway-style long flags become double-dash",
			[]string{"validate", "-url=jdbc:postgresql://h/db", "-user", "sa", "-password=p", "-locations=filesystem:./sql"},
			[]string{"validate", "--url=jdbc:postgresql://h/db", "--user", "sa", "--password=p", "--locations=filesystem:./sql"},
		},
		{"short help unchanged", []string{"-h"}, []string{"-h"}},
		{"short version unchanged", []string{"-v"}, []string{"-v"}},
		{"already double-dash unchanged", []string{"--url=x"}, []string{"--url=x"}},
		{"double-dash terminator unchanged", []string{"--"}, []string{"--"}},
		{"subcommand unchanged", []string{"migrate"}, []string{"migrate"}},
		{"plain value unchanged", []string{"filesystem:./sql"}, []string{"filesystem:./sql"}},
		{"single dash unchanged", []string{"-"}, []string{"-"}},
		{"empty", nil, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeFlywayArgs(tt.in); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("normalizeFlywayArgs(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}
