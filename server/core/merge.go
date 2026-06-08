package core

import (
	"context"
	"errors"
	"fmt"

	"github.com/akado2009/dbx/server/db"
	"github.com/jackc/pgx/v5"
)

func MergeBranch(ctx context.Context, mainConnStr string, branch *db.Branch) error {
	mainConn, err := pgx.Connect(ctx, mainConnStr)
	if err != nil {
		return err
	}
	defer mainConn.Close(ctx)

	var currentLSN string
	mainConn.QueryRow(ctx, `SELECT pg_current_wal_lsn()::text`).Scan(&currentLSN)

	if currentLSN != branch.ParentLSN {
		return errors.New("branch is behind main — run: dbx branch rebase " + branch.Name)
	}

	branchConnStr := branchConnString(mainConnStr, branch.PgPort)
	branchSlot := BranchSlotName(branch)

	branchConn, err := pgx.Connect(ctx, branchConnStr)
	if err != nil {
		return err
	}
	defer branchConn.Close(ctx)

	var branchLSN string
	branchConn.QueryRow(ctx, `SELECT pg_current_wal_lsn()::text`).Scan(&branchLSN)

	branchChanges, err := GetChanges(ctx, branchConnStr, branchSlot, branchLSN)
	if err != nil {
		return fmt.Errorf("get branch changes: %w", err)
	}

	return ApplyChanges(ctx, mainConnStr, branchChanges)
}

func DiffBranch(ctx context.Context, mainConnStr string, branch *db.Branch) ([]*Change, error) {
	branchConnStr := branchConnString(mainConnStr, branch.PgPort)
	branchSlot := BranchSlotName(branch)

	branchConn, err := pgx.Connect(ctx, branchConnStr)
	if err != nil {
		return nil, err
	}
	defer branchConn.Close(ctx)

	var branchLSN string
	branchConn.QueryRow(ctx, `SELECT pg_current_wal_lsn()::text`).Scan(&branchLSN)

	return GetChanges(ctx, branchConnStr, branchSlot, branchLSN)
}

// BranchSlotName returns the replication slot name for a branch.
// Uses stored slot_name if available, falls back to computing it.
func BranchSlotName(branch *db.Branch) string {
	if branch.SlotName != "" {
		return branch.SlotName
	}
	return SlotName(branch.ProjectID + "-" + branch.Name)
}

// branchConnString builds a connection string for a branch PG instance
// using the same user/db as main but on a different port.
func branchConnString(mainConnStr string, port int) string {
	cfg, err := pgx.ParseConfig(mainConnStr)
	if err != nil {
		return fmt.Sprintf("postgresql://localhost:%d/myapp?sslmode=disable", port)
	}
	return fmt.Sprintf("postgresql://%s@localhost:%d/%s?sslmode=disable",
		cfg.User, port, cfg.Database)
}
