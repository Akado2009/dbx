package core

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/akado2009/dbx/server/db"
	"github.com/jackc/pgx/v5"
)

// CreateBranch creates a new Postgres instance as a branch via base backup.
// Flow:
//  1. Create replication slots on main (to track main changes from this point)
//  2. pg_basebackup → copy main data to branch dataDir
//  3. Start branch PG on given port
//  4. Create replication slot on branch (to track branch changes)
// CreateBranch creates a new Postgres branch via pg_basebackup.
// sourceConnStr is the PG to copy from — either main or another branch.
// mainConnStr is always the main PG (for slot tracking divergence from main).
func CreateBranch(ctx context.Context, mainConnStr, sourceConnStr, branchID, name string, port int, dataDir string) (*db.Branch, error) {
	mainConn, err := pgx.Connect(ctx, mainConnStr)
	if err != nil {
		return nil, fmt.Errorf("connect to main: %w", err)
	}
	defer mainConn.Close(ctx)

	// create main slot BEFORE backup — captures all main changes from this point
	mainSlot := SlotName("main_" + branchID)
	lsn, err := CreateSlot(ctx, mainConn, mainSlot)
	if err != nil {
		return nil, fmt.Errorf("create main slot: %w", err)
	}

	if err := baseBackup(sourceConnStr, dataDir); err != nil {
		return nil, fmt.Errorf("base backup: %w", err)
	}

	if err := startPostgres(dataDir, port); err != nil {
		return nil, fmt.Errorf("start postgres: %w", err)
	}

	// wait for branch PG to be ready
	if err := waitForPostgres(ctx, port, 30*time.Second); err != nil {
		return nil, fmt.Errorf("wait for branch pg: %w", err)
	}

	// connect to branch — use same user/db as main
	cfg, _ := pgx.ParseConfig(mainConnStr)
	branchConnStr := fmt.Sprintf("postgresql://%s@localhost:%d/%s?sslmode=disable",
		cfg.User, port, cfg.Database)

	// create branch slot to track branch changes from this point
	branchConn, err := pgx.Connect(ctx, branchConnStr)
	if err != nil {
		return nil, fmt.Errorf("connect to branch: %w", err)
	}
	defer branchConn.Close(ctx)

	branchSlot := SlotName(branchID)
	if _, err := CreateSlot(ctx, branchConn, branchSlot); err != nil {
		return nil, fmt.Errorf("create branch slot: %w", err)
	}

	return &db.Branch{
		Name:      name,
		ParentLSN: lsn,
		PgPort:    port,
		PgDataDir: dataDir,
		SlotName:  branchSlot,
		Status:    "active",
	}, nil
}

// DeleteBranch drops replication slots and stops the branch PG instance.
func DeleteBranch(ctx context.Context, mainConnStr string, branch *db.Branch) error {
	cfg, _ := pgx.ParseConfig(mainConnStr)
	branchConnStr := fmt.Sprintf("postgresql://%s@localhost:%d/%s?sslmode=disable",
		cfg.User, branch.PgPort, cfg.Database)

	// drop slots from branch PG (best effort)
	if branchConn, err := pgx.Connect(ctx, branchConnStr); err == nil {
		branchSlot := BranchSlotName(branch)
		DropSlot(ctx, branchConn, branchSlot)
		branchConn.Close(ctx)
	}

	// drop main-tracking slot from main PG (best effort)
	if mainConn, err := pgx.Connect(ctx, mainConnStr); err == nil {
		mainSlot := SlotName("main_" + branch.ProjectID + "-" + branch.Name)
		DropSlot(ctx, mainConn, mainSlot)
		mainConn.Close(ctx)
	}

	return stopPostgres(branch.PgDataDir)
}

// StopBranch stops the branch PG instance without deleting data or slots.
// Used after merge — data is no longer needed but we don't want to clean slots twice.
func StopBranch(branch *db.Branch) error {
	cmd := pgCommand("pg_ctl", "stop", "-D", branch.PgDataDir, "-m", "fast")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	_ = cmd.Run() // best effort
	return os.RemoveAll(branch.PgDataDir)
}

func stopPostgres(dataDir string) error {
	cmd := pgCommand("pg_ctl", "stop", "-D", dataDir, "-m", "fast")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Run() // best effort — ignore error, PG may already be stopped
	return os.RemoveAll(dataDir)
}

func pgDatabase(connStr string) string {
	cfg, err := pgx.ParseConfig(connStr)
	if err != nil {
		return "myapp"
	}
	return cfg.Database
}

func baseBackup(mainConnStr, dataDir string) error {
	// remove stale data dir if exists (e.g. leftover from previous branch with same name)
	if err := os.RemoveAll(dataDir); err != nil {
		return fmt.Errorf("cleanup datadir: %w", err)
	}
	// create dir with 0700 — postgres requires this
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	cfg, err := pgx.ParseConfig(mainConnStr)
	if err != nil {
		return err
	}

	var stderr bytes.Buffer
	cmd := pgCommand(
		"pg_basebackup",
		"-h", cfg.Host,
		"-p", fmt.Sprintf("%d", cfg.Port),
		"-U", cfg.User,
		"-D", dataDir,
		"-Xs",
		"-P",
		"-R",
	)
	cmd.Env = append(os.Environ(), fmt.Sprintf("PGPASSWORD=%s", cfg.Password))
	cmd.Stdout = os.Stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, stderr.String())
	}
	// postgres requires 0700 on data dir
	if err := os.Chmod(dataDir, 0700); err != nil {
		return fmt.Errorf("chmod datadir: %w", err)
	}
	return nil
}

func startPostgres(dataDir string, port int) error {
	confPath := dataDir + "/postgresql.conf"
	f, err := os.OpenFile(confPath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	fmt.Fprintf(f, "\nport = %d\n", port)
	fmt.Fprintf(f, "listen_addresses = '*'\n")
	fmt.Fprintf(f, "wal_level = logical\n")
	fmt.Fprintf(f, "max_replication_slots = 20\n")
	fmt.Fprintf(f, "max_wal_senders = 20\n")
	f.Close()

	os.Remove(dataDir + "/standby.signal")
	os.Remove(dataDir + "/postmaster.pid")

	var stderr bytes.Buffer
	cmd := pgCommand("pg_ctl", "start", "-D", dataDir, "-w", "-t", "30")
	cmd.Stdout = os.Stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, stderr.String())
	}
	return nil
}

// pgCommand runs a pg command. Uses gosu if running as root.
func pgCommand(name string, args ...string) *exec.Cmd {
	if os.Getuid() == 0 {
		return exec.Command("gosu", append([]string{"postgres", name}, args...)...)
	}
	return exec.Command(name, args...)
}


func waitForPostgres(ctx context.Context, port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		cmd := pgCommand("pg_isready", "-p", fmt.Sprintf("%d", port), "-q")
		if cmd.Run() == nil {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("postgres did not become ready within %s", timeout)
}
