package core

import (
	"context"
	"fmt"

	"github.com/akado2009/dbx/server/db"
	"github.com/jackc/pgx/v5"
)

type BranchStatusResult struct {
	Branch        string `json:"branch"`
	Status        string `json:"status"`
	BranchLSN     string `json:"branch_lsn"`
	MainLSN       string `json:"main_lsn"`
	BehindMain    bool   `json:"behind_main"`
	PendingMain   int    `json:"pending_main_changes"`   // changes in main not yet in branch
	PendingBranch int    `json:"pending_branch_changes"` // changes in branch not yet in main
	Conflicts     int    `json:"conflicts"`
}

func BranchStatus(ctx context.Context, mainConnStr string, branch *db.Branch) (*BranchStatusResult, error) {
	mainConn, err := pgx.Connect(ctx, mainConnStr)
	if err != nil {
		return nil, fmt.Errorf("connect to main: %w", err)
	}
	defer mainConn.Close(ctx)

	var mainLSN string
	mainConn.QueryRow(ctx, `SELECT pg_current_wal_lsn()::text`).Scan(&mainLSN)

	branchConnStr := BranchConnString(mainConnStr, branch.PgPort)
	branchConn, err := pgx.Connect(ctx, branchConnStr)
	if err != nil {
		return nil, fmt.Errorf("connect to branch: %w", err)
	}
	defer branchConn.Close(ctx)

	var branchLSN string
	branchConn.QueryRow(ctx, `SELECT pg_current_wal_lsn()::text`).Scan(&branchLSN)

	mainSlot := SlotName("main_" + branch.ProjectID + "-" + branch.Name)
	branchSlot := BranchSlotName(branch)

	mainChanges, err := GetChanges(ctx, mainConnStr, mainSlot, mainLSN)
	if err != nil {
		mainChanges = nil // slot may not exist yet
	}

	branchChanges, err := GetChanges(ctx, branchConnStr, branchSlot, branchLSN)
	if err != nil {
		branchChanges = nil
	}

	conflicts := DetectConflicts(mainChanges, branchChanges)

	return &BranchStatusResult{
		Branch:        branch.Name,
		Status:        branch.Status,
		BranchLSN:     branchLSN,
		MainLSN:       mainLSN,
		BehindMain:    mainLSN != branch.ParentLSN,
		PendingMain:   len(mainChanges),
		PendingBranch: len(branchChanges),
		Conflicts:     len(conflicts),
	}, nil
}
