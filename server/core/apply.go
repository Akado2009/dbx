package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// ApplyChanges applies a list of changes to the target Postgres instance.
// Runs everything in a single transaction — all or nothing.
func ApplyChanges(ctx context.Context, connStr string, changes []*Change) error {
	if len(changes) == 0 {
		return nil
	}

	conn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(ctx)

	pkCache, err := LoadPKs(ctx, conn)
	if err != nil {
		return fmt.Errorf("load pk cache: %w", err)
	}

	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	for _, c := range changes {
		if err := applyChange(ctx, tx, pkCache, c); err != nil {
			return fmt.Errorf("apply %s on %s (pk=%v): %w", c.Operation, c.Table, c.PrimaryKey, err)
		}
	}

	return tx.Commit(ctx)
}

func applyChange(ctx context.Context, tx pgx.Tx, pkCache PKCache, c *Change) error {
	switch c.Operation {
	case "INSERT":
		return applyInsert(ctx, tx, c)
	case "UPDATE":
		return applyUpdate(ctx, tx, pkCache, c)
	case "DELETE":
		return applyDelete(ctx, tx, pkCache, c)
	default:
		return fmt.Errorf("unknown operation: %s", c.Operation)
	}
}

func applyInsert(ctx context.Context, tx pgx.Tx, c *Change) error {
	cols, vals, args := rowToInsertParts(c.NewRow)
	q := fmt.Sprintf(
		`INSERT INTO %s (%s) VALUES (%s) ON CONFLICT DO NOTHING`,
		quoteTable(c.Table), cols, vals,
	)
	_, err := tx.Exec(ctx, q, args...)
	return err
}

func applyUpdate(ctx context.Context, tx pgx.Tx, pkCache PKCache, c *Change) error {
	if c.PrimaryKey == nil {
		return fmt.Errorf("cannot update row without primary key")
	}

	pkCols := pkCache.pkColumns(c.Table)

	// SET args first ($1, $2, ...)
	setClauses, setArgs := rowToSetPartsOffset(c.NewRow, pkCols, 0)
	if setClauses == "" {
		return nil // nothing to update
	}

	// WHERE args after SET args ($len(setArgs)+1, ...)
	whereClause, whereArgs, err := pkCache.BuildWhereOffset(c.Table, c.NewRow, len(setArgs))
	if err != nil {
		return err
	}

	q := fmt.Sprintf(
		`UPDATE %s SET %s WHERE %s`,
		quoteTable(c.Table), setClauses, whereClause,
	)
	_, err = tx.Exec(ctx, q, append(setArgs, whereArgs...)...)
	return err
}

func applyDelete(ctx context.Context, tx pgx.Tx, pkCache PKCache, c *Change) error {
	if c.PrimaryKey == nil {
		return fmt.Errorf("cannot delete row without primary key")
	}
	row := c.OldRow
	if row == nil {
		row = map[string]any{"id": c.PrimaryKey}
	}
	whereClause, args, err := pkCache.BuildWhere(c.Table, row)
	if err != nil {
		return err
	}
	q := fmt.Sprintf(`DELETE FROM %s WHERE %s`, quoteTable(c.Table), whereClause)
	_, err = tx.Exec(ctx, q, args...)
	return err
}

// rowToInsertParts builds column list, placeholder list, and args slice for INSERT.
// Example: "id, email", "$1, $2", []any{1, "foo@bar.com"}
func rowToInsertParts(row map[string]any) (cols string, placeholders string, args []any) {
	i := 1
	var colList, phList []string
	for col, val := range row {
		colList = append(colList, quoteIdent(col))
		phList = append(phList, fmt.Sprintf("$%d", i))
		args = append(args, val)
		i++
	}
	return strings.Join(colList, ", "), strings.Join(phList, ", "), args
}

// rowToSetParts builds SET clause and args for UPDATE.
// PK is appended as the last arg (used in WHERE id = $N).
func rowToSetParts(row map[string]any, pk any) (setClauses string, args []any) {
	return rowToSetPartsOffset(row, []string{"id"}, 0)
}

// rowToSetPartsOffset builds SET clause skipping pkCols, with placeholder offset.
// offset = number of WHERE args that come AFTER set args in the final query.
func rowToSetPartsOffset(row map[string]any, pkCols []string, offset int) (setClauses string, args []any) {
	skip := map[string]bool{}
	for _, col := range pkCols {
		skip[col] = true
	}

	i := offset + 1
	var parts []string
	for col, val := range row {
		if skip[col] {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s = $%d", quoteIdent(col), i))
		args = append(args, val)
		i++
	}
	return strings.Join(parts, ", "), args
}

// quoteTable handles optional schema prefix: "public.users" → `"public"."users"`
func quoteTable(table string) string {
	parts := strings.SplitN(table, ".", 2)
	if len(parts) == 2 {
		return fmt.Sprintf(`"%s"."%s"`, parts[0], parts[1])
	}
	return fmt.Sprintf(`"%s"`, parts[0])
}

func quoteIdent(s string) string {
	return fmt.Sprintf(`"%s"`, s)
}
