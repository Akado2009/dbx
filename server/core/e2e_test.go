package core_test

import (
	"context"
	"testing"

	"github.com/akado2009/dbx/server/core"
	"github.com/akado2009/dbx/server/db"
	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// setupTwoPGs spins up main + branch postgres containers.
func setupTwoPGs(t *testing.T) (mainConn, branchConn string, cleanup func()) {
	t.Helper()
	ctx := context.Background()

	run := func(name string) (string, func()) {
		c, err := postgres.Run(ctx,
			"postgres:16",
			postgres.WithDatabase(name),
			postgres.WithUsername("test"),
			postgres.WithPassword("test"),
			testcontainers.WithCmd(
				"postgres",
				"-c", "wal_level=logical",
				"-c", "max_replication_slots=10",
				"-c", "max_wal_senders=10",
			),
			testcontainers.WithWaitStrategy(
				wait.ForLog("database system is ready to accept connections").
					WithOccurrence(2),
			),
		)
		if err != nil {
			t.Fatalf("start %s: %v", name, err)
		}
		dsn, err := c.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			c.Terminate(ctx)
			t.Fatalf("dsn %s: %v", name, err)
		}
		return dsn, func() { c.Terminate(ctx) }
	}

	mainDSN, cleanMain := run("maindb")
	branchDSN, cleanBranch := run("branchdb")

	return mainDSN, branchDSN, func() {
		cleanMain()
		cleanBranch()
	}
}

// seedSchema creates the same schema on both PGs and seeds initial data on main.
func seedSchema(t *testing.T, ctx context.Context, mainDSN, branchDSN string) {
	t.Helper()

	schema := `
		CREATE TABLE users (
			id    TEXT PRIMARY KEY,
			name  TEXT,
			email TEXT
		);
		ALTER TABLE users REPLICA IDENTITY FULL;
	`

	for _, dsn := range []string{mainDSN, branchDSN} {
		conn, err := pgx.Connect(ctx, dsn)
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
		if _, err := conn.Exec(ctx, schema); err != nil {
			t.Fatalf("schema: %v", err)
		}
		conn.Close(ctx)
	}

	// seed initial data on main
	conn, _ := pgx.Connect(ctx, mainDSN)
	defer conn.Close(ctx)
	conn.Exec(ctx, `INSERT INTO users VALUES ('u1', 'alice', 'alice@example.com')`)
	conn.Exec(ctx, `INSERT INTO users VALUES ('u2', 'bob', 'bob@example.com')`)
}

// TestRebaseBranch_NoConflicts tests the happy path:
// branch and main change different rows → rebase succeeds.
func TestRebaseBranch_NoConflicts(t *testing.T) {
	mainDSN, branchDSN, cleanup := setupTwoPGs(t)
	defer cleanup()

	ctx := context.Background()
	seedSchema(t, ctx, mainDSN, branchDSN)

	// copy seed data to branch
	mainConn, _ := pgx.Connect(ctx, mainDSN)
	defer mainConn.Close(ctx)
	branchConn, _ := pgx.Connect(ctx, branchDSN)
	defer branchConn.Close(ctx)

	branchConn.Exec(ctx, `INSERT INTO users VALUES ('u1', 'alice', 'alice@example.com')`)
	branchConn.Exec(ctx, `INSERT INTO users VALUES ('u2', 'bob', 'bob@example.com')`)

	// create slots on BOTH sides (simulating branch creation)
	mainSlot := "dbx_main_branch1"
	branchSlot := "dbx_branch1"
	if _, err := core.CreateSlot(ctx, mainConn, mainSlot); err != nil {
		t.Fatalf("create main slot: %v", err)
	}
	if _, err := core.CreateSlot(ctx, branchConn, branchSlot); err != nil {
		t.Fatalf("create branch slot: %v", err)
	}

	// main changes u1's email
	mainConn.Exec(ctx, `UPDATE users SET email = 'alice@new.com' WHERE id = 'u1'`)

	// branch changes u2's name (different row — no conflict)
	branchConn.Exec(ctx, `UPDATE users SET name = 'bobby' WHERE id = 'u2'`)

	var mainLSN string
	mainConn.QueryRow(ctx, `SELECT pg_current_wal_lsn()::text`).Scan(&mainLSN)

	branch := &db.Branch{
		ID:        "branch1",
		Name:      "feature-x",
		ParentLSN: mainLSN,
		PgPort:    0, // not used in this test — we pass DSNs directly
	}

	// call rebase directly with explicit DSNs (bypass port-based connection)
	result, err := rebaseDirect(ctx, mainDSN, branchDSN, mainSlot, branchSlot, branch)
	if err != nil {
		t.Fatalf("RebaseBranch: %v", err)
	}
	if len(result.Conflicts) > 0 {
		t.Errorf("expected no conflicts, got %d: %+v", len(result.Conflicts), result.Conflicts)
	}

	// verify branch now has main's change applied
	var email string
	branchConn.QueryRow(ctx, `SELECT email FROM users WHERE id = 'u1'`).Scan(&email)
	if email != "alice@new.com" {
		t.Errorf("expected branch to have updated email 'alice@new.com', got %q", email)
	}

	// branch's own change should still be there
	var name string
	branchConn.QueryRow(ctx, `SELECT name FROM users WHERE id = 'u2'`).Scan(&name)
	if name != "bobby" {
		t.Errorf("expected branch to keep 'bobby', got %q", name)
	}
}

// TestRebaseBranch_WithConflicts tests conflict detection:
// both main and branch change the same column of the same row.
func TestRebaseBranch_WithConflicts(t *testing.T) {
	mainDSN, branchDSN, cleanup := setupTwoPGs(t)
	defer cleanup()

	ctx := context.Background()
	seedSchema(t, ctx, mainDSN, branchDSN)

	mainConn, _ := pgx.Connect(ctx, mainDSN)
	defer mainConn.Close(ctx)
	branchConn, _ := pgx.Connect(ctx, branchDSN)
	defer branchConn.Close(ctx)

	branchConn.Exec(ctx, `INSERT INTO users VALUES ('u1', 'alice', 'alice@example.com')`)

	mainSlot := "dbx_main_branch2"
	branchSlot := "dbx_branch2"
	core.CreateSlot(ctx, mainConn, mainSlot)
	core.CreateSlot(ctx, branchConn, branchSlot)

	// both change u1's email → conflict
	mainConn.Exec(ctx, `UPDATE users SET email = 'main@new.com' WHERE id = 'u1'`)
	branchConn.Exec(ctx, `UPDATE users SET email = 'branch@new.com' WHERE id = 'u1'`)

	var mainLSN string
	mainConn.QueryRow(ctx, `SELECT pg_current_wal_lsn()::text`).Scan(&mainLSN)

	branch := &db.Branch{ID: "branch2", Name: "feature-y", ParentLSN: mainLSN}
	result, err := rebaseDirect(ctx, mainDSN, branchDSN, mainSlot, branchSlot, branch)
	if err != nil {
		t.Fatalf("RebaseBranch: %v", err)
	}
	if len(result.Conflicts) == 0 {
		t.Fatal("expected conflicts, got none")
	}

	found := false
	for _, c := range result.Conflicts {
		if c.Column == "email" {
			found = true
			if c.MainValue != "main@new.com" {
				t.Errorf("expected main_value='main@new.com', got %v", c.MainValue)
			}
			if c.YourValue != "branch@new.com" {
				t.Errorf("expected your_value='branch@new.com', got %v", c.YourValue)
			}
		}
	}
	if !found {
		t.Error("expected conflict on 'email' column")
	}
}

// TestMergeBranch tests merging branch changes back to main.
func TestMergeBranch(t *testing.T) {
	mainDSN, branchDSN, cleanup := setupTwoPGs(t)
	defer cleanup()

	ctx := context.Background()
	seedSchema(t, ctx, mainDSN, branchDSN)

	mainConn, _ := pgx.Connect(ctx, mainDSN)
	defer mainConn.Close(ctx)
	branchConn, _ := pgx.Connect(ctx, branchDSN)
	defer branchConn.Close(ctx)

	// sync seed data to branch
	branchConn.Exec(ctx, `INSERT INTO users VALUES ('u1', 'alice', 'alice@example.com')`)
	branchConn.Exec(ctx, `INSERT INTO users VALUES ('u2', 'bob', 'bob@example.com')`)

	mainSlot := "dbx_main_branch3"
	branchSlot := "dbx_branch3"
	core.CreateSlot(ctx, mainConn, mainSlot)
	core.CreateSlot(ctx, branchConn, branchSlot)

	// branch adds a new user and updates existing
	branchConn.Exec(ctx, `INSERT INTO users VALUES ('u3', 'charlie', 'charlie@example.com')`)
	branchConn.Exec(ctx, `UPDATE users SET name = 'alice smith' WHERE id = 'u1'`)

	// get toLSN from branch (not main — they have independent LSN sequences)
	var branchLSN string
	branchConn.QueryRow(ctx, `SELECT pg_current_wal_lsn()::text`).Scan(&branchLSN)

	// merge branch changes into main
	err := mergeDirect(ctx, mainDSN, branchDSN, branchSlot, branchLSN)
	if err != nil {
		t.Fatalf("MergeBranch: %v", err)
	}

	// verify main now has branch's changes
	var count int
	mainConn.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&count)
	if count != 3 {
		t.Errorf("expected 3 users after merge, got %d", count)
	}

	var name string
	mainConn.QueryRow(ctx, `SELECT name FROM users WHERE id = 'u1'`).Scan(&name)
	if name != "alice smith" {
		t.Errorf("expected 'alice smith' after merge, got %q", name)
	}

	var charlie string
	mainConn.QueryRow(ctx, `SELECT name FROM users WHERE id = 'u3'`).Scan(&charlie)
	if charlie != "charlie" {
		t.Errorf("expected 'charlie' to be merged, got %q", charlie)
	}
}

// --- helpers that bypass port-based connection (use explicit DSNs) ---

func rebaseDirect(ctx context.Context, mainDSN, branchDSN, mainSlot, branchSlot string, branch *db.Branch) (*core.RebaseResult, error) {
	mainConn, err := pgx.Connect(ctx, mainDSN)
	if err != nil {
		return nil, err
	}
	defer mainConn.Close(ctx)

	branchConn, err := pgx.Connect(ctx, branchDSN)
	if err != nil {
		return nil, err
	}
	defer branchConn.Close(ctx)

	// each PG has its own independent LSN sequence
	var mainLSN, branchLSN string
	mainConn.QueryRow(ctx, `SELECT pg_current_wal_lsn()::text`).Scan(&mainLSN)
	branchConn.QueryRow(ctx, `SELECT pg_current_wal_lsn()::text`).Scan(&branchLSN)

	mainChanges, err := core.GetChanges(ctx, mainDSN, mainSlot, mainLSN)
	if err != nil {
		return nil, err
	}
	branchChanges, err := core.GetChanges(ctx, branchDSN, branchSlot, branchLSN)
	if err != nil {
		return nil, err
	}

	conflicts := core.DetectConflicts(mainChanges, branchChanges)
	if len(conflicts) > 0 {
		return &core.RebaseResult{Conflicts: conflicts}, nil
	}

	if err := core.ApplyChanges(ctx, branchDSN, mainChanges); err != nil {
		return nil, err
	}
	return &core.RebaseResult{NewLSN: mainLSN}, nil
}

func mergeDirect(ctx context.Context, mainDSN, branchDSN, branchSlot, atLSN string) error {
	branchChanges, err := core.GetChanges(ctx, branchDSN, branchSlot, atLSN)
	if err != nil {
		return err
	}
	return core.ApplyChanges(ctx, mainDSN, branchChanges)
}
