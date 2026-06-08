package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// TablePK describes the primary key of a table.
type TablePK struct {
	Columns []string // ordered list of PK columns
}

// PKCache maps "schema.table" → TablePK
type PKCache map[string]*TablePK

// LoadPKs fetches primary key info for all tables from pg_constraint.
// Cheap to call once per connection — results should be cached.
func LoadPKs(ctx context.Context, conn *pgx.Conn) (PKCache, error) {
	rows, err := conn.Query(ctx, `
		SELECT
			n.nspname                          AS schema,
			c.relname                          AS table,
			array_agg(a.attname ORDER BY k.pos) AS pk_columns
		FROM pg_constraint con
		JOIN pg_class c        ON c.oid = con.conrelid
		JOIN pg_namespace n    ON n.oid = c.relnamespace
		JOIN LATERAL unnest(con.conkey) WITH ORDINALITY AS k(col, pos)
			ON true
		JOIN pg_attribute a    ON a.attrelid = c.oid AND a.attnum = k.col
		WHERE con.contype = 'p'         -- primary key only
		  AND n.nspname NOT IN ('pg_catalog', 'information_schema')
		GROUP BY n.nspname, c.relname
	`)
	if err != nil {
		return nil, fmt.Errorf("query pk info: %w", err)
	}
	defer rows.Close()

	cache := make(PKCache)
	for rows.Next() {
		var schema, table string
		var cols []string
		if err := rows.Scan(&schema, &table, &cols); err != nil {
			return nil, err
		}
		key := schema + "." + table
		cache[key] = &TablePK{Columns: cols}
	}
	return cache, rows.Err()
}

// ExtractPK builds a composite PK value from a row using the cache.
// Returns a string key for single-column PKs, or "col1=v1,col2=v2" for composite.
// Falls back to guessing common names if table not in cache.
func (c PKCache) ExtractPK(table string, row map[string]any) any {
	// normalize table name to "schema.table"
	key := normalizeTable(table)

	pk, ok := c[key]
	if !ok {
		return guessPK(row)
	}

	if len(pk.Columns) == 1 {
		return row[pk.Columns[0]]
	}

	// composite PK → "col1=v1,col2=v2"
	parts := make([]string, len(pk.Columns))
	for i, col := range pk.Columns {
		parts[i] = fmt.Sprintf("%s=%v", col, row[col])
	}
	return strings.Join(parts, ",")
}

// BuildWhere returns a WHERE clause and args for a PK lookup.
// Used in UPDATE and DELETE statements.
func (c PKCache) BuildWhere(table string, row map[string]any) (clause string, args []any, err error) {
	key := normalizeTable(table)

	pk, ok := c[key]
	if !ok {
		// fallback: guess
		for _, name := range []string{"id", "uuid", "ID"} {
			if v, exists := row[name]; exists {
				return fmt.Sprintf(`"%s" = $1`, name), []any{v}, nil
			}
		}
		return "", nil, fmt.Errorf("no primary key found for table %s", table)
	}

	parts := make([]string, len(pk.Columns))
	for i, col := range pk.Columns {
		parts[i] = fmt.Sprintf(`"%s" = $%d`, col, i+1)
		args = append(args, row[col])
	}
	return strings.Join(parts, " AND "), args, nil
}

// BuildWhereOffset is like BuildWhere but placeholder numbers start at offset+1.
func (c PKCache) BuildWhereOffset(table string, row map[string]any, offset int) (clause string, args []any, err error) {
	key := normalizeTable(table)

	pk, ok := c[key]
	if !ok {
		for _, name := range []string{"id", "uuid", "ID"} {
			if v, exists := row[name]; exists {
				return fmt.Sprintf(`"%s" = $%d`, name, offset+1), []any{v}, nil
			}
		}
		return "", nil, fmt.Errorf("no primary key found for table %s", table)
	}

	parts := make([]string, len(pk.Columns))
	for i, col := range pk.Columns {
		parts[i] = fmt.Sprintf(`"%s" = $%d`, col, offset+i+1)
		args = append(args, row[col])
	}
	return strings.Join(parts, " AND "), args, nil
}

// pkColumns returns the PK column names for a table, or ["id"] as fallback.
func (c PKCache) pkColumns(table string) []string {
	key := normalizeTable(table)
	if pk, ok := c[key]; ok {
		return pk.Columns
	}
	return []string{"id"}
}

func normalizeTable(table string) string {
	if strings.Contains(table, ".") {
		return table
	}
	return "public." + table
}

func guessPK(row map[string]any) any {
	for _, name := range []string{"id", "uuid", "ID"} {
		if v, ok := row[name]; ok {
			return v
		}
	}
	return nil
}
