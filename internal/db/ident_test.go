package db

import (
	"strings"
	"testing"
)

func TestQuoteIdentAcceptsRegistryStyleNames(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"tenant_acme", `"tenant_acme"`},
		{"tenant_globex_2", `"tenant_globex_2"`},
		{"_internal", `"_internal"`},
		{"a", `"a"`},
		// 63 bytes is the Postgres identifier limit; exactly at it is fine.
		{strings.Repeat("a", 63), `"` + strings.Repeat("a", 63) + `"`},
	}
	for _, tc := range cases {
		got, err := QuoteIdent(tc.in)
		if err != nil {
			t.Errorf("QuoteIdent(%q) returned error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("QuoteIdent(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

func TestQuoteIdentRejectsEverythingElse(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"leading digit", "1tenant"},
		{"uppercase", "Tenant_Acme"},
		{"unicode", "tenant_ähm"},
		{"embedded double quote", `tenant_"acme`},
		{"quote injection", `x"; drop schema public;--`},
		{"semicolon", "tenant;acme"},
		{"space", "tenant acme"},
		{"hyphen", "tenant-acme"},
		{"dot qualified", "public.notes"},
		{"64 bytes", strings.Repeat("a", 64)},
	}
	for _, tc := range cases {
		if got, err := QuoteIdent(tc.in); err == nil {
			t.Errorf("%s: QuoteIdent(%q) = %s, want error", tc.name, tc.in, got)
		}
	}
}
