package db_test

import (
	"context"
	"testing"

	"github.com/akado2009/dbx/server/db"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func setupTestDB(t *testing.T) (*db.Store, func()) {
	t.Helper()
	ctx := context.Background()

	container, err := postgres.Run(ctx,
		"postgres:16",
		postgres.WithDatabase("dbx_test"),
		postgres.WithUsername("dbx"),
		postgres.WithPassword("dbx"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2),
		),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		container.Terminate(ctx)
		t.Fatalf("get connection string: %v", err)
	}

	store, err := db.New(ctx, dsn)
	if err != nil {
		container.Terminate(ctx)
		t.Fatalf("connect to db: %v", err)
	}

	if err := store.Migrate(ctx); err != nil {
		store.Close()
		container.Terminate(ctx)
		t.Fatalf("migrate: %v", err)
	}

	cleanup := func() {
		store.Close()
		container.Terminate(ctx)
	}
	return store, cleanup
}

func TestCreateAndGetProject(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	p, err := store.CreateProject(ctx, "my-project", "postgresql://localhost:5432/mydb", "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if p.ID == "" {
		t.Error("expected non-empty project ID")
	}
	if p.Name != "my-project" {
		t.Errorf("expected name 'my-project', got %q", p.Name)
	}

	got, err := store.GetProject(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if got.ID != p.ID {
		t.Errorf("expected id %q, got %q", p.ID, got.ID)
	}
	if got.ConnString != "postgresql://localhost:5432/mydb" {
		t.Errorf("unexpected conn string: %q", got.ConnString)
	}
}

func TestCreateAndListBranches(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	p, _ := store.CreateProject(ctx, "proj", "postgresql://localhost/db", "")

	b := &db.Branch{
		ProjectID: p.ID,
		Name:      "feature-auth",
		ParentLSN: "0/3A1F8B0",
		PgPort:    5433,
		PgDataDir: "/data/branches/feature-auth",
		Status:    "active",
	}
	if err := store.CreateBranch(ctx, b); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	if b.ID == "" {
		t.Error("expected non-empty branch ID")
	}

	branches, err := store.ListBranches(ctx, p.ID)
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	if len(branches) != 1 {
		t.Fatalf("expected 1 branch, got %d", len(branches))
	}
	if branches[0].Name != "feature-auth" {
		t.Errorf("expected branch name 'feature-auth', got %q", branches[0].Name)
	}
}

func TestGetBranch(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	p, _ := store.CreateProject(ctx, "proj2", "postgresql://localhost/db2", "")

	b := &db.Branch{
		ProjectID: p.ID,
		Name:      "fix-perf",
		ParentLSN: "0/1000000",
		PgPort:    5434,
		PgDataDir: "/data/branches/fix-perf",
	}
	store.CreateBranch(ctx, b)

	got, err := store.GetBranch(ctx, p.ID, "fix-perf")
	if err != nil {
		t.Fatalf("GetBranch: %v", err)
	}
	if got.PgPort != 5434 {
		t.Errorf("expected port 5434, got %d", got.PgPort)
	}
	if got.ParentLSN != "0/1000000" {
		t.Errorf("expected lsn '0/1000000', got %q", got.ParentLSN)
	}
}

func TestUpdateBranchLSN(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	p, _ := store.CreateProject(ctx, "proj3", "postgresql://localhost/db3", "")

	b := &db.Branch{
		ProjectID: p.ID,
		Name:      "branch-a",
		ParentLSN: "0/1000000",
		PgPort:    5435,
		PgDataDir: "/data/branches/a",
	}
	store.CreateBranch(ctx, b)

	if err := store.UpdateBranchLSN(ctx, b.ID, "0/2000000"); err != nil {
		t.Fatalf("UpdateBranchLSN: %v", err)
	}

	got, _ := store.GetBranch(ctx, p.ID, "branch-a")
	if got.ParentLSN != "0/2000000" {
		t.Errorf("expected updated lsn '0/2000000', got %q", got.ParentLSN)
	}
}

func TestDeleteBranch(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	p, _ := store.CreateProject(ctx, "proj4", "postgresql://localhost/db4", "")

	b := &db.Branch{
		ProjectID: p.ID,
		Name:      "temp-branch",
		ParentLSN: "0/1000000",
		PgPort:    5436,
		PgDataDir: "/data/branches/temp",
	}
	store.CreateBranch(ctx, b)
	store.DeleteBranch(ctx, b.ID)

	branches, _ := store.ListBranches(ctx, p.ID)
	if len(branches) != 0 {
		t.Errorf("expected 0 branches after delete, got %d", len(branches))
	}
}

func TestNextFreePort(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// no branches → should return 5433 (5432 + 1)
	port, err := store.NextFreePort(ctx)
	if err != nil {
		t.Fatalf("NextFreePort: %v", err)
	}
	if port != 5433 {
		t.Errorf("expected 5433, got %d", port)
	}

	p, _ := store.CreateProject(ctx, "proj5", "postgresql://localhost/db5", "")
	store.CreateBranch(ctx, &db.Branch{
		ProjectID: p.ID, Name: "b1", ParentLSN: "0/1",
		PgPort: 5433, PgDataDir: "/tmp/b1",
	})
	store.CreateBranch(ctx, &db.Branch{
		ProjectID: p.ID, Name: "b2", ParentLSN: "0/1",
		PgPort: 5434, PgDataDir: "/tmp/b2",
	})

	port, _ = store.NextFreePort(ctx)
	if port != 5435 {
		t.Errorf("expected 5435, got %d", port)
	}
}

func TestUniqueBranchNamePerProject(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	p, _ := store.CreateProject(ctx, "proj6", "postgresql://localhost/db6", "")

	b := &db.Branch{
		ProjectID: p.ID, Name: "same-name",
		ParentLSN: "0/1", PgPort: 5437, PgDataDir: "/tmp/same",
	}
	store.CreateBranch(ctx, b)

	b2 := &db.Branch{
		ProjectID: p.ID, Name: "same-name",
		ParentLSN: "0/2", PgPort: 5438, PgDataDir: "/tmp/same2",
	}
	err := store.CreateBranch(ctx, b2)
	if err == nil {
		t.Error("expected error for duplicate branch name, got nil")
	}
}
