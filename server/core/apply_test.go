package core

import (
	"context"
	"testing"
)

// Tests for pure helper functions — no DB required.

func TestQuoteTable(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"users", `"users"`},
		{"public.users", `"public"."users"`},
		{"myschema.orders", `"myschema"."orders"`},
	}
	for _, tt := range tests {
		got := quoteTable(tt.input)
		if got != tt.expected {
			t.Errorf("quoteTable(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestQuoteIdent(t *testing.T) {
	if got := quoteIdent("email"); got != `"email"` {
		t.Errorf("quoteIdent: got %q", got)
	}
}

func TestRowToInsertParts(t *testing.T) {
	row := map[string]any{
		"id":    "1",
		"email": "foo@bar.com",
	}

	cols, placeholders, args := rowToInsertParts(row)

	if len(args) != 2 {
		t.Fatalf("expected 2 args, got %d", len(args))
	}

	// cols and placeholders should have same count
	colCount := len(splitAndTrim(cols))
	phCount := len(splitAndTrim(placeholders))
	if colCount != phCount {
		t.Errorf("col count %d != placeholder count %d", colCount, phCount)
	}

	// args should contain both values
	argSet := map[any]bool{}
	for _, a := range args {
		argSet[a] = true
	}
	if !argSet["1"] {
		t.Error("args missing id='1'")
	}
	if !argSet["foo@bar.com"] {
		t.Error("args missing email='foo@bar.com'")
	}
}

func TestRowToSetParts(t *testing.T) {
	row := map[string]any{
		"id":    "42",
		"email": "new@bar.com",
		"name":  "alice",
	}

	setClauses, args := rowToSetParts(row, "42")

	// id should be excluded from SET
	if len(args) == 0 {
		t.Fatal("expected args, got none")
	}
	if contains(setClauses, `"id"`) {
		t.Error("SET clause should not include PK column 'id'")
	}
	if !contains(setClauses, `"email"`) {
		t.Error("SET clause missing 'email'")
	}
	if !contains(setClauses, `"name"`) {
		t.Error("SET clause missing 'name'")
	}
	// args should only contain non-PK values (WHERE args are separate now)
	for _, a := range args {
		if a == "42" {
			t.Error("PK should not be in SET args — it belongs in WHERE args")
		}
	}
}

func TestRowToSetParts_OnlyPK(t *testing.T) {
	row := map[string]any{"id": "1"}
	setClauses, args := rowToSetParts(row, "1")

	// no non-PK columns → empty SET clause and no args
	if setClauses != "" {
		t.Errorf("expected empty SET clause, got %q", setClauses)
	}
	if len(args) != 0 {
		t.Errorf("expected 0 args, got %v", args)
	}
}

func TestApplyChanges_EmptyNoop(t *testing.T) {
	// ApplyChanges with empty slice should not error even without a DB
	// (it returns early before connecting)
	err := ApplyChanges(context.TODO(), "", []*Change{})
	if err != nil {
		t.Errorf("expected nil error for empty changes, got %v", err)
	}
}

// --- helpers ---

func splitAndTrim(s string) []string {
	parts := []string{}
	cur := ""
	for _, c := range s {
		if c == ',' {
			parts = append(parts, cur)
			cur = ""
		} else {
			cur += string(c)
		}
	}
	if cur != "" {
		parts = append(parts, cur)
	}
	return parts
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr ||
		len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
