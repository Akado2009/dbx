package core_test

import (
	"testing"

	"github.com/akado2009/dbx/server/core"
)

func TestDetectConflicts_Dedup(t *testing.T) {
	// branch slot accumulates: old-value, then user-resolved-value for same PK.
	// dedup should keep only the LAST change per PK.
	mainChanges := []*core.Change{
		{Table: "users", Operation: "UPDATE", PrimaryKey: "u1",
			NewRow: map[string]any{"id": "u1", "name": "main-v2"}},
	}
	branchChanges := []*core.Change{
		// older change (applied by previous rebase)
		{Table: "users", Operation: "UPDATE", PrimaryKey: "u1",
			NewRow: map[string]any{"id": "u1", "name": "main-v1"}},
		// user's actual change
		{Table: "users", Operation: "UPDATE", PrimaryKey: "u1",
			NewRow: map[string]any{"id": "u1", "name": "branch-v1"}},
	}

	conflicts := core.DetectConflicts(mainChanges, branchChanges)

	// should produce exactly 1 conflict (not 2), using branch-v1 as the branch value
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict after dedup, got %d", len(conflicts))
	}
	c := conflicts[0]
	if c.YourValue != "branch-v1" {
		t.Errorf("expected your_value='branch-v1' (last change wins), got %v", c.YourValue)
	}
	if c.MainValue != "main-v2" {
		t.Errorf("expected main_value='main-v2', got %v", c.MainValue)
	}
}

func TestDetectConflicts_NoConflict(t *testing.T) {
	mainChanges := []*core.Change{
		{Table: "users", Operation: "UPDATE", PrimaryKey: "u1",
			NewRow: map[string]any{"id": "u1", "name": "alice-main"}},
	}
	branchChanges := []*core.Change{
		{Table: "users", Operation: "UPDATE", PrimaryKey: "u2",
			NewRow: map[string]any{"id": "u2", "name": "bob-branch"}},
	}

	conflicts := core.DetectConflicts(mainChanges, branchChanges)
	if len(conflicts) != 0 {
		t.Errorf("expected no conflicts (different PKs), got %d", len(conflicts))
	}
}

func TestDetectConflicts_SameValueNoConflict(t *testing.T) {
	// same table+PK+column but same value → not a conflict
	mainChanges := []*core.Change{
		{Table: "users", Operation: "UPDATE", PrimaryKey: "u1",
			NewRow: map[string]any{"id": "u1", "name": "alice"}},
	}
	branchChanges := []*core.Change{
		{Table: "users", Operation: "UPDATE", PrimaryKey: "u1",
			NewRow: map[string]any{"id": "u1", "name": "alice"}},
	}

	conflicts := core.DetectConflicts(mainChanges, branchChanges)
	if len(conflicts) != 0 {
		t.Errorf("expected no conflicts (same value), got %d", len(conflicts))
	}
}

func TestDetectConflicts_MultipleColumns(t *testing.T) {
	// both touch same row but different columns → no conflict
	mainChanges := []*core.Change{
		{Table: "users", Operation: "UPDATE", PrimaryKey: "u1",
			NewRow: map[string]any{"id": "u1", "name": "alice-main", "email": "old@x.com"}},
	}
	branchChanges := []*core.Change{
		{Table: "users", Operation: "UPDATE", PrimaryKey: "u1",
			NewRow: map[string]any{"id": "u1", "name": "alice-main", "email": "branch@x.com"}},
	}

	conflicts := core.DetectConflicts(mainChanges, branchChanges)
	// only email conflicts (name is same)
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict (email column), got %d", len(conflicts))
	}
	if conflicts[0].Column != "email" {
		t.Errorf("expected conflict on 'email', got %q", conflicts[0].Column)
	}
}

func TestAdvanceSlot(t *testing.T) {
	connStr, cleanup := setupPG(t)
	defer cleanup()

	ctx := setupCtx(t)
	conn := mustConnect(t, ctx, connStr)
	defer conn.Close(ctx)

	mustExec(t, conn, ctx, `
		CREATE TABLE docs (id TEXT PRIMARY KEY, body TEXT);
		ALTER TABLE docs REPLICA IDENTITY FULL;
	`)

	slotName := "dbx_test_advance"
	if _, err := core.CreateSlot(ctx, conn, slotName); err != nil {
		t.Fatalf("CreateSlot: %v", err)
	}

	mustExec(t, conn, ctx, `INSERT INTO docs VALUES ('d1', 'hello')`)

	var lsn string
	conn.QueryRow(ctx, `SELECT pg_current_wal_lsn()::text`).Scan(&lsn)

	// advance slot — consumes changes up to lsn
	if err := core.AdvanceSlot(ctx, connStr, slotName, lsn); err != nil {
		t.Fatalf("AdvanceSlot: %v", err)
	}

	// insert more changes AFTER advance
	mustExec(t, conn, ctx, `INSERT INTO docs VALUES ('d2', 'world')`)

	var lsn2 string
	conn.QueryRow(ctx, `SELECT pg_current_wal_lsn()::text`).Scan(&lsn2)

	// GetChanges should only see d2 (d1 was consumed by advance)
	changes, err := core.GetChanges(ctx, connStr, slotName, lsn2)
	if err != nil {
		t.Fatalf("GetChanges: %v", err)
	}

	var docChanges []*core.Change
	for _, c := range changes {
		if c.Table == "docs" || c.Table == "public.docs" {
			docChanges = append(docChanges, c)
		}
	}

	if len(docChanges) != 1 {
		t.Fatalf("expected 1 change after advance (only d2), got %d", len(docChanges))
	}
	if pk := docChanges[0].PrimaryKey; pk != "d2" {
		t.Errorf("expected PK='d2', got %v", pk)
	}
}

func TestRebase_SlotAdvanceAfterSuccess(t *testing.T) {
	// Verify that after a successful rebase, the main slot is advanced
	// so a subsequent rebase doesn't replay the same changes.
	mainDSN, branchDSN, cleanup := setupTwoPGs(t)
	defer cleanup()

	ctx := setupCtx(t)
	seedSchema(t, ctx, mainDSN, branchDSN)

	mainConn := mustConnect(t, ctx, mainDSN)
	defer mainConn.Close(ctx)
	branchConn := mustConnect(t, ctx, branchDSN)
	defer branchConn.Close(ctx)

	branchConn.Exec(ctx, `INSERT INTO users VALUES ('u1', 'alice', 'a@x.com')`)

	mainSlot := "dbx_main_advance_test"
	branchSlot := "dbx_branch_advance_test"
	core.CreateSlot(ctx, mainConn, mainSlot)
	core.CreateSlot(ctx, branchConn, branchSlot)

	// main changes u1
	mainConn.Exec(ctx, `UPDATE users SET name='alice-main' WHERE id='u1'`)

	var mainLSN string
	mainConn.QueryRow(ctx, `SELECT pg_current_wal_lsn()::text`).Scan(&mainLSN)

	// first rebase — no conflicts, applies main change to branch
	mainChanges, _ := core.GetChanges(ctx, mainDSN, mainSlot, mainLSN)
	branchLSN := ""
	branchConn.QueryRow(ctx, `SELECT pg_current_wal_lsn()::text`).Scan(&branchLSN)
	branchChanges, _ := core.GetChanges(ctx, branchDSN, branchSlot, branchLSN)

	conflicts := core.DetectConflicts(mainChanges, branchChanges)
	if len(conflicts) > 0 {
		t.Fatalf("unexpected conflicts on first rebase: %+v", conflicts)
	}
	core.ApplyChanges(ctx, branchDSN, mainChanges)
	core.AdvanceSlot(ctx, mainDSN, mainSlot, mainLSN) // <-- this is what we're testing

	// second rebase — main slot should be empty now
	var mainLSN2 string
	mainConn.QueryRow(ctx, `SELECT pg_current_wal_lsn()::text`).Scan(&mainLSN2)
	mainChanges2, _ := core.GetChanges(ctx, mainDSN, mainSlot, mainLSN2)

	if len(mainChanges2) != 0 {
		t.Errorf("expected 0 main changes after slot advance, got %d (slot not consumed)", len(mainChanges2))
	}
}
