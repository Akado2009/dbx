package core

import (
	"testing"
)

func TestExtractPK_SingleColumn(t *testing.T) {
	cache := PKCache{
		"public.users": {Columns: []string{"id"}},
	}
	row := map[string]any{"id": "42", "email": "foo@bar.com"}

	pk := cache.ExtractPK("users", row)
	if pk != "42" {
		t.Errorf("expected '42', got %v", pk)
	}
}

func TestExtractPK_WithSchema(t *testing.T) {
	cache := PKCache{
		"billing.invoices": {Columns: []string{"invoice_id"}},
	}
	row := map[string]any{"invoice_id": "INV-001", "amount": "100"}

	pk := cache.ExtractPK("billing.invoices", row)
	if pk != "INV-001" {
		t.Errorf("expected 'INV-001', got %v", pk)
	}
}

func TestExtractPK_CompositePK(t *testing.T) {
	cache := PKCache{
		"public.order_items": {Columns: []string{"order_id", "product_id"}},
	}
	row := map[string]any{"order_id": "1", "product_id": "99", "qty": "2"}

	pk := cache.ExtractPK("order_items", row)
	pkStr, ok := pk.(string)
	if !ok {
		t.Fatalf("expected string pk, got %T", pk)
	}
	if pkStr != "order_id=1,product_id=99" {
		t.Errorf("unexpected composite pk: %q", pkStr)
	}
}

func TestExtractPK_Fallback(t *testing.T) {
	cache := PKCache{} // empty cache
	row := map[string]any{"id": "7", "name": "alice"}

	pk := cache.ExtractPK("unknown_table", row)
	if pk != "7" {
		t.Errorf("expected fallback to id='7', got %v", pk)
	}
}

func TestExtractPK_FallbackUUID(t *testing.T) {
	cache := PKCache{}
	row := map[string]any{"uuid": "abc-123", "name": "bob"}

	pk := cache.ExtractPK("things", row)
	if pk != "abc-123" {
		t.Errorf("expected fallback to uuid='abc-123', got %v", pk)
	}
}

func TestExtractPK_NoKnownPK(t *testing.T) {
	cache := PKCache{}
	row := map[string]any{"foo": "bar"}

	pk := cache.ExtractPK("weird_table", row)
	if pk != nil {
		t.Errorf("expected nil pk, got %v", pk)
	}
}

func TestBuildWhere_SingleColumn(t *testing.T) {
	cache := PKCache{
		"public.users": {Columns: []string{"id"}},
	}
	row := map[string]any{"id": "42", "email": "foo@bar.com"}

	clause, args, err := cache.BuildWhere("users", row)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if clause != `"id" = $1` {
		t.Errorf("unexpected clause: %q", clause)
	}
	if len(args) != 1 || args[0] != "42" {
		t.Errorf("unexpected args: %v", args)
	}
}

func TestBuildWhere_CompositePK(t *testing.T) {
	cache := PKCache{
		"public.order_items": {Columns: []string{"order_id", "product_id"}},
	}
	row := map[string]any{"order_id": "1", "product_id": "99"}

	clause, args, err := cache.BuildWhere("order_items", row)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if clause != `"order_id" = $1 AND "product_id" = $2` {
		t.Errorf("unexpected clause: %q", clause)
	}
	if len(args) != 2 {
		t.Errorf("expected 2 args, got %d", len(args))
	}
}

func TestBuildWhere_Fallback(t *testing.T) {
	cache := PKCache{}
	row := map[string]any{"id": "5", "name": "x"}

	clause, args, err := cache.BuildWhere("any_table", row)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if clause != `"id" = $1` {
		t.Errorf("unexpected fallback clause: %q", clause)
	}
	if args[0] != "5" {
		t.Errorf("unexpected arg: %v", args[0])
	}
}

func TestBuildWhere_NoPK_Error(t *testing.T) {
	cache := PKCache{}
	row := map[string]any{"foo": "bar"} // no known PK column

	_, _, err := cache.BuildWhere("mystery_table", row)
	if err == nil {
		t.Error("expected error for table with no PK, got nil")
	}
}

func TestNormalizeTable(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"users", "public.users"},
		{"public.users", "public.users"},
		{"billing.invoices", "billing.invoices"},
	}
	for _, tt := range tests {
		got := normalizeTable(tt.input)
		if got != tt.expected {
			t.Errorf("normalizeTable(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}
