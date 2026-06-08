package core

import (
	"context"
	"fmt"

	"github.com/akado2009/dbx/server/db"
	"github.com/jackc/pgx/v5"
)

type RebaseResult struct {
	Conflicts []*Conflict
	NewLSN    string
}

func RebaseBranch(ctx context.Context, mainConnStr string, branch *db.Branch) (*RebaseResult, error) {
	mainConn, err := pgx.Connect(ctx, mainConnStr)
	if err != nil {
		return nil, err
	}
	defer mainConn.Close(ctx)

	var currentLSN string
	err = mainConn.QueryRow(ctx, `SELECT pg_current_wal_lsn()::text`).Scan(&currentLSN)
	if err != nil {
		return nil, err
	}

	if currentLSN == branch.ParentLSN {
		return &RebaseResult{NewLSN: currentLSN}, nil
	}

	branchConnStr := branchConnString(mainConnStr, branch.PgPort)

	// each PG instance has its own independent LSN sequence
	branchConn, err := pgx.Connect(ctx, branchConnStr)
	if err != nil {
		return nil, err
	}
	defer branchConn.Close(ctx)

	var branchLSN string
	branchConn.QueryRow(ctx, `SELECT pg_current_wal_lsn()::text`).Scan(&branchLSN)

	mainSlot := SlotName("main_" + branch.ProjectID + "-" + branch.Name)
	branchSlot := BranchSlotName(branch)

	mainChanges, err := GetChanges(ctx, mainConnStr, mainSlot, currentLSN)
	if err != nil {
		return nil, fmt.Errorf("get main changes: %w", err)
	}

	branchChanges, err := GetChanges(ctx, branchConnStr, branchSlot, branchLSN)
	if err != nil {
		return nil, fmt.Errorf("get branch changes: %w", err)
	}

	conflicts := DetectConflicts(mainChanges, branchChanges)
	if len(conflicts) > 0 {
		return &RebaseResult{Conflicts: conflicts}, nil
	}

	if err := ApplyChanges(ctx, branchConnStr, mainChanges); err != nil {
		return nil, fmt.Errorf("apply changes: %w", err)
	}

	return &RebaseResult{NewLSN: currentLSN}, nil
}
