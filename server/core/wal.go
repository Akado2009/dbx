package core

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

type Change struct {
	Table      string         `json:"table"`
	Operation  string         `json:"operation"` // INSERT, UPDATE, DELETE
	PrimaryKey any            `json:"primary_key"`
	Column     string         `json:"column,omitempty"`
	OldRow     map[string]any `json:"old_row,omitempty"`
	NewRow     map[string]any `json:"new_row,omitempty"`
}

type Conflict struct {
	Table      string `json:"table"`
	PrimaryKey any    `json:"primary_key"`
	Column     string `json:"column"`
	MainValue  any    `json:"main_value"`
	YourValue  any    `json:"your_value"`
}

// SlotName returns the replication slot name for a branch.
// Postgres slot names: lowercase alphanumeric + underscore, max 63 chars.
func SlotName(branchID string) string {
	name := "dbx_"
	for _, c := range branchID {
		switch {
		case c >= 'a' && c <= 'z':
			name += string(c)
		case c >= 'A' && c <= 'Z':
			name += string(c + 32) // to lower
		case c >= '0' && c <= '9':
			name += string(c)
		default:
			name += "_" // replace - and other chars with _
		}
	}
	if len(name) > 63 {
		name = name[:63]
	}
	return name
}

// EnsurePublication creates the publication if it doesn't exist.
func EnsurePublication(ctx context.Context, conn *pgx.Conn) error {
	_, err := conn.Exec(ctx, `
		DO $$ BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_publication WHERE pubname = 'dbx_pub') THEN
				CREATE PUBLICATION dbx_pub FOR ALL TABLES;
			END IF;
		END $$;
	`)
	return err
}

// CreateSlot creates a logical replication slot at the current WAL position.
// Returns the LSN at which the slot was created (= branch point).
// Call this at branch creation time, BEFORE any changes you want to capture.
func CreateSlot(ctx context.Context, conn *pgx.Conn, slotName string) (lsn string, err error) {
	if err := EnsurePublication(ctx, conn); err != nil {
		return "", fmt.Errorf("ensure publication: %w", err)
	}

	// drop if exists (idempotent)
	conn.Exec(ctx, fmt.Sprintf(`
		SELECT pg_drop_replication_slot(slot_name)
		FROM pg_replication_slots
		WHERE slot_name = '%s'
	`, slotName))

	err = conn.QueryRow(ctx, fmt.Sprintf(
		`SELECT lsn FROM pg_create_logical_replication_slot('%s', 'pgoutput')`,
		slotName,
	)).Scan(&lsn)
	return lsn, err
}

// AdvanceSlot consumes all changes in slotName up to toLSN.
// Call this after a successful rebase so the slot doesn't re-replay old changes.
func AdvanceSlot(ctx context.Context, connStr, slotName, toLSN string) error {
	conn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	_, err = conn.Exec(ctx, fmt.Sprintf(
		`SELECT pg_replication_slot_advance('%s', '%s')`,
		slotName, toLSN,
	))
	return err
}

// DropSlot removes a replication slot. Call on branch delete.
func DropSlot(ctx context.Context, conn *pgx.Conn, slotName string) error {
	_, err := conn.Exec(ctx, fmt.Sprintf(
		`SELECT pg_drop_replication_slot(slot_name) FROM pg_replication_slots WHERE slot_name = '%s'`,
		slotName,
	))
	return err
}

// GetChanges returns logical changes captured by slotName up to toLSN.
// The slot must have been created BEFORE the changes you want to capture.
// Uses peek (non-consuming) so changes can be read multiple times.
func GetChanges(ctx context.Context, connStr, slotName, toLSN string) ([]*Change, error) {
	conn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		return nil, err
	}
	defer conn.Close(ctx)

	pkCache, err := LoadPKs(ctx, conn)
	if err != nil {
		return nil, fmt.Errorf("load pk cache: %w", err)
	}

	rows, err := conn.Query(ctx, fmt.Sprintf(
		`SELECT data FROM pg_logical_slot_peek_binary_changes('%s', '%s', NULL, 'proto_version', '1', 'publication_names', 'dbx_pub')`,
		slotName, toLSN,
	))
	if err != nil {
		return nil, fmt.Errorf("peek changes: %w", err)
	}
	defer rows.Close()

	relations := make(RelationCache)
	var changes []*Change

	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			continue
		}

		msgs, err := ParseMessages(data, relations)
		if err != nil {
			continue
		}

		for _, msg := range msgs {
			switch m := msg.(type) {
			case *InsertMsg:
				rel := relations[m.RelationID]
				table := relName(rel)
				changes = append(changes, &Change{
					Table:      table,
					Operation:  "INSERT",
					NewRow:     m.Row,
					PrimaryKey: pkCache.ExtractPK(table, m.Row),
				})
			case *UpdateMsg:
				rel := relations[m.RelationID]
				table := relName(rel)
				changes = append(changes, &Change{
					Table:      table,
					Operation:  "UPDATE",
					OldRow:     m.OldRow,
					NewRow:     m.NewRow,
					PrimaryKey: pkCache.ExtractPK(table, m.NewRow),
				})
			case *DeleteMsg:
				rel := relations[m.RelationID]
				table := relName(rel)
				changes = append(changes, &Change{
					Table:      table,
					Operation:  "DELETE",
					OldRow:     m.OldRow,
					PrimaryKey: pkCache.ExtractPK(table, m.OldRow),
				})
			}
		}
	}

	return changes, nil
}

func relName(rel *RelationMsg) string {
	if rel == nil {
		return "unknown"
	}
	if rel.Namespace != "" && rel.Namespace != "public" {
		return rel.Namespace + "." + rel.Name
	}
	return rel.Name
}

// deduplicateChanges keeps only the last change per table+PK (latest wins).
func deduplicateChanges(changes []*Change) []*Change {
	type key struct {
		table string
		pk    any
	}
	last := map[key]int{}
	for i, c := range changes {
		last[key{c.Table, c.PrimaryKey}] = i
	}
	var out []*Change
	seen := map[key]bool{}
	for i := len(changes) - 1; i >= 0; i-- {
		c := changes[i]
		k := key{c.Table, c.PrimaryKey}
		if last[k] == i && !seen[k] {
			seen[k] = true
			out = append([]*Change{c}, out...)
		}
	}
	return out
}

func DetectConflicts(mainChanges, branchChanges []*Change) []*Conflict {
	// deduplicate: for same PK keep only last change
	mainChanges = deduplicateChanges(mainChanges)
	branchChanges = deduplicateChanges(branchChanges)

	var conflicts []*Conflict
	for _, mc := range mainChanges {
		for _, bc := range branchChanges {
			if mc.Table != bc.Table || mc.PrimaryKey != bc.PrimaryKey {
				continue
			}
			for col, mainVal := range mc.NewRow {
				if branchVal, ok := bc.NewRow[col]; ok && mainVal != branchVal {
					conflicts = append(conflicts, &Conflict{
						Table:      mc.Table,
						PrimaryKey: mc.PrimaryKey,
						Column:     col,
						MainValue:  mainVal,
						YourValue:  branchVal,
					})
				}
			}
		}
	}
	return conflicts
}
