package core_test

import (
	"context"
	"testing"

	"github.com/akado2009/dbx/server/core"
	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// setupPG spins up a Postgres container with logical replication enabled.
func setupPG(t *testing.T) (connStr string, cleanup func()) {
	t.Helper()
	ctx := context.Background()

	container, err := postgres.Run(ctx,
		"postgres:16",
		postgres.WithDatabase("testdb"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		// enable logical replication
		postgres.WithSQLDriver("pgx"),
		testcontainers.WithEnv(map[string]string{
			"POSTGRES_INITDB_ARGS": "--encoding=UTF8",
		}),
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
		t.Fatalf("start postgres: %v", err)
	}

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		container.Terminate(ctx)
		t.Fatalf("connection string: %v", err)
	}

	return dsn, func() { container.Terminate(ctx) }
}

func TestLoadPKs(t *testing.T) {
	connStr, cleanup := setupPG(t)
	defer cleanup()

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	// create tables with various PK shapes
	_, err = conn.Exec(ctx, `
		CREATE TABLE users (
			id    SERIAL PRIMARY KEY,
			email TEXT NOT NULL
		);
		CREATE TABLE order_items (
			order_id   INT NOT NULL,
			product_id INT NOT NULL,
			qty        INT,
			PRIMARY KEY (order_id, product_id)
		);
		CREATE TABLE no_pk (
			name TEXT
		);
	`)
	if err != nil {
		t.Fatalf("create tables: %v", err)
	}

	cache, err := core.LoadPKs(ctx, conn)
	if err != nil {
		t.Fatalf("LoadPKs: %v", err)
	}

	// single PK
	pk, ok := cache["public.users"]
	if !ok {
		t.Fatal("expected 'public.users' in cache")
	}
	if len(pk.Columns) != 1 || pk.Columns[0] != "id" {
		t.Errorf("unexpected PK columns for users: %v", pk.Columns)
	}

	// composite PK
	pk, ok = cache["public.order_items"]
	if !ok {
		t.Fatal("expected 'public.order_items' in cache")
	}
	if len(pk.Columns) != 2 {
		t.Errorf("expected 2 PK columns for order_items, got %v", pk.Columns)
	}

	// table without PK — should not appear in cache
	if _, ok := cache["public.no_pk"]; ok {
		t.Error("table without PK should not be in cache")
	}
}

func TestApplyChanges_Integration(t *testing.T) {
	connStr, cleanup := setupPG(t)
	defer cleanup()

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	_, err = conn.Exec(ctx, `
		CREATE TABLE products (
			id    TEXT PRIMARY KEY,
			name  TEXT,
			price TEXT
		);
		INSERT INTO products VALUES ('p1', 'apple', '1.00');
		INSERT INTO products VALUES ('p2', 'banana', '0.50');
	`)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	changes := []*core.Change{
		// insert new row
		{
			Table:     "products",
			Operation: "INSERT",
			NewRow:    map[string]any{"id": "p3", "name": "cherry", "price": "2.00"},
			PrimaryKey: "p3",
		},
		// update existing row
		{
			Table:      "products",
			Operation:  "UPDATE",
			NewRow:     map[string]any{"id": "p1", "name": "apple", "price": "1.50"},
			OldRow:     map[string]any{"id": "p1", "name": "apple", "price": "1.00"},
			PrimaryKey: "p1",
		},
		// delete row
		{
			Table:      "products",
			Operation:  "DELETE",
			OldRow:     map[string]any{"id": "p2", "name": "banana", "price": "0.50"},
			PrimaryKey: "p2",
		},
	}

	if err := core.ApplyChanges(ctx, connStr, changes); err != nil {
		t.Fatalf("ApplyChanges: %v", err)
	}

	// verify results
	rows, err := conn.Query(ctx, `SELECT id, name, price FROM products ORDER BY id`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	type row struct{ id, name, price string }
	var got []row
	for rows.Next() {
		var r row
		rows.Scan(&r.id, &r.name, &r.price)
		got = append(got, r)
	}

	expected := []row{
		{"p1", "apple", "1.50"},  // updated price
		{"p3", "cherry", "2.00"}, // inserted
		// p2 deleted
	}

	if len(got) != len(expected) {
		t.Fatalf("expected %d rows, got %d: %+v", len(expected), len(got), got)
	}
	for i, e := range expected {
		if got[i] != e {
			t.Errorf("row[%d]: expected %+v, got %+v", i, e, got[i])
		}
	}
}

func TestGetChanges_Insert(t *testing.T) {
	connStr, cleanup := setupPG(t)
	defer cleanup()

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	_, err = conn.Exec(ctx, `
		CREATE TABLE events (
			id    TEXT PRIMARY KEY,
			name  TEXT
		);
	`)
	if err != nil {
		t.Fatalf("setup table: %v", err)
	}

	// create slot BEFORE changes — this is the branch point
	slotName := "dbx_test_insert"
	if _, err := core.CreateSlot(ctx, conn, slotName); err != nil {
		t.Fatalf("CreateSlot: %v", err)
	}

	// make changes after slot creation
	_, err = conn.Exec(ctx, `
		INSERT INTO events VALUES ('e1', 'signup');
		INSERT INTO events VALUES ('e2', 'purchase');
	`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	var toLSN string
	conn.QueryRow(ctx, `SELECT pg_current_wal_lsn()::text`).Scan(&toLSN)

	changes, err := core.GetChanges(ctx, connStr, slotName, toLSN)
	if err != nil {
		t.Fatalf("GetChanges: %v", err)
	}

	var eventsChanges []*core.Change
	for _, c := range changes {
		if c.Table == "events" || c.Table == "public.events" {
			eventsChanges = append(eventsChanges, c)
		}
	}

	if len(eventsChanges) != 2 {
		t.Fatalf("expected 2 INSERT changes for events, got %d (total: %d)", len(eventsChanges), len(changes))
	}
	for _, c := range eventsChanges {
		if c.Operation != "INSERT" {
			t.Errorf("expected INSERT, got %s", c.Operation)
		}
		if c.PrimaryKey == nil {
			t.Error("expected non-nil PrimaryKey")
		}
	}
}

func TestGetChanges_UpdateDelete(t *testing.T) {
	connStr, cleanup := setupPG(t)
	defer cleanup()

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	_, err = conn.Exec(ctx, `
		CREATE TABLE orders (
			id     TEXT PRIMARY KEY,
			status TEXT
		);
		-- REPLICA IDENTITY FULL needed for UPDATE/DELETE to include old row
		ALTER TABLE orders REPLICA IDENTITY FULL;
		INSERT INTO orders VALUES ('o1', 'pending');
		INSERT INTO orders VALUES ('o2', 'pending');
	`)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	// create slot BEFORE changes
	slotName := "dbx_test_upd_del"
	if _, err := core.CreateSlot(ctx, conn, slotName); err != nil {
		t.Fatalf("CreateSlot: %v", err)
	}

	_, err = conn.Exec(ctx, `
		UPDATE orders SET status = 'shipped' WHERE id = 'o1';
		DELETE FROM orders WHERE id = 'o2';
	`)
	if err != nil {
		t.Fatalf("changes: %v", err)
	}

	var toLSN string
	conn.QueryRow(ctx, `SELECT pg_current_wal_lsn()::text`).Scan(&toLSN)

	changes, err := core.GetChanges(ctx, connStr, slotName, toLSN)
	if err != nil {
		t.Fatalf("GetChanges: %v", err)
	}

	var ordersChanges []*core.Change
	for _, c := range changes {
		if c.Table == "orders" || c.Table == "public.orders" {
			ordersChanges = append(ordersChanges, c)
		}
	}

	if len(ordersChanges) != 2 {
		t.Fatalf("expected 2 changes (UPDATE+DELETE), got %d", len(ordersChanges))
	}

	ops := map[string]bool{}
	for _, c := range ordersChanges {
		ops[c.Operation] = true
	}
	if !ops["UPDATE"] {
		t.Error("expected UPDATE change")
	}
	if !ops["DELETE"] {
		t.Error("expected DELETE change")
	}
}

func TestGetChanges_EmptyRange(t *testing.T) {
	connStr, cleanup := setupPG(t)
	defer cleanup()

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	slotName := "dbx_test_empty"
	if _, err := core.CreateSlot(ctx, conn, slotName); err != nil {
		t.Fatalf("CreateSlot: %v", err)
	}

	// no changes after slot creation
	var toLSN string
	conn.QueryRow(ctx, `SELECT pg_current_wal_lsn()::text`).Scan(&toLSN)

	changes, err := core.GetChanges(ctx, connStr, slotName, toLSN)
	if err != nil {
		t.Fatalf("GetChanges: %v", err)
	}
	if len(changes) != 0 {
		t.Errorf("expected 0 changes, got %d", len(changes))
	}
}

func TestApplyChanges_RollbackOnError(t *testing.T) {
	connStr, cleanup := setupPG(t)
	defer cleanup()

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	_, err = conn.Exec(ctx, `
		CREATE TABLE items (id TEXT PRIMARY KEY, val TEXT);
		INSERT INTO items VALUES ('x1', 'original');
	`)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	changes := []*core.Change{
		{
			Table: "items", Operation: "UPDATE",
			NewRow: map[string]any{"id": "x1", "val": "changed"},
			PrimaryKey: "x1",
		},
		{
			// this will fail — table doesn't exist
			Table: "nonexistent_table", Operation: "INSERT",
			NewRow: map[string]any{"id": "y1", "val": "boom"},
			PrimaryKey: "y1",
		},
	}

	err = core.ApplyChanges(ctx, connStr, changes)
	if err == nil {
		t.Fatal("expected error from bad change, got nil")
	}

	// verify rollback — x1 should still have original value
	var val string
	conn.QueryRow(ctx, `SELECT val FROM items WHERE id = 'x1'`).Scan(&val)
	if val != "original" {
		t.Errorf("expected rollback, but val changed to %q", val)
	}
}
