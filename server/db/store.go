package db

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
}

func New(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() {
	s.pool.Close()
}

func (s *Store) Migrate(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS projects (
			id          TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
			name        TEXT NOT NULL UNIQUE,
			conn_string TEXT NOT NULL,
			created_at  TIMESTAMPTZ DEFAULT now()
		);

		CREATE TABLE IF NOT EXISTS branches (
			id          TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
			project_id  TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
			name        TEXT NOT NULL,
			parent_lsn  TEXT NOT NULL,
			pg_port     INT NOT NULL,
			pg_data_dir TEXT NOT NULL,
			slot_name   TEXT NOT NULL DEFAULT '',
			status      TEXT NOT NULL DEFAULT 'active',
			conflicts   JSONB,
			created_at  TIMESTAMPTZ DEFAULT now(),
			UNIQUE(project_id, name)
		);
		-- add slot_name column if upgrading from older schema
		ALTER TABLE branches ADD COLUMN IF NOT EXISTS slot_name TEXT NOT NULL DEFAULT '';
		ALTER TABLE branches ADD COLUMN IF NOT EXISTS conflicts JSONB;
		ALTER TABLE branches ADD COLUMN IF NOT EXISTS parent_branch TEXT;
		ALTER TABLE branches ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ;
	`)
	return err
}

type Project struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	ConnString string `json:"conn_string"`
}

type Branch struct {
	ID           string     `json:"id"`
	ProjectID    string     `json:"project_id"`
	Name         string     `json:"name"`
	ParentLSN    string     `json:"parent_lsn"`
	ParentBranch string     `json:"parent_branch"`
	PgPort       int        `json:"pg_port"`
	PgDataDir    string     `json:"pg_data_dir"`
	SlotName     string     `json:"slot_name"`
	Status       string     `json:"status"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
}

func (s *Store) ListProjects(ctx context.Context) ([]*Project, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, name, conn_string FROM projects`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var projects []*Project
	for rows.Next() {
		p := &Project{}
		rows.Scan(&p.ID, &p.Name, &p.ConnString)
		projects = append(projects, p)
	}
	return projects, nil
}

func (s *Store) CreateProject(ctx context.Context, name, connString string) (*Project, error) {
	p := &Project{Name: name, ConnString: connString}
	err := s.pool.QueryRow(ctx,
		`INSERT INTO projects (name, conn_string) VALUES ($1, $2) RETURNING id`,
		name, connString,
	).Scan(&p.ID)
	return p, err
}

func (s *Store) GetProject(ctx context.Context, id string) (*Project, error) {
	p := &Project{ID: id}
	err := s.pool.QueryRow(ctx,
		`SELECT name, conn_string FROM projects WHERE id = $1`,
		id,
	).Scan(&p.Name, &p.ConnString)
	return p, err
}

func (s *Store) CreateBranch(ctx context.Context, b *Branch) error {
	return s.pool.QueryRow(ctx,
		`INSERT INTO branches (project_id, name, parent_lsn, pg_port, pg_data_dir, slot_name, parent_branch)
		 VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		b.ProjectID, b.Name, b.ParentLSN, b.PgPort, b.PgDataDir, b.SlotName, b.ParentBranch,
	).Scan(&b.ID)
}

func (s *Store) ListBranches(ctx context.Context, projectID string) ([]*Branch, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, name, parent_lsn, pg_port, pg_data_dir, slot_name, status, COALESCE(parent_branch, '') FROM branches WHERE project_id = $1`,
		projectID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var branches []*Branch
	for rows.Next() {
		b := &Branch{ProjectID: projectID}
		rows.Scan(&b.ID, &b.Name, &b.ParentLSN, &b.PgPort, &b.PgDataDir, &b.SlotName, &b.Status, &b.ParentBranch)
		branches = append(branches, b)
	}
	return branches, nil
}

func (s *Store) GetBranch(ctx context.Context, projectID, name string) (*Branch, error) {
	b := &Branch{ProjectID: projectID, Name: name}
	err := s.pool.QueryRow(ctx,
		`SELECT id, parent_lsn, pg_port, pg_data_dir, slot_name, status, COALESCE(parent_branch, '') FROM branches WHERE project_id = $1 AND name = $2`,
		projectID, name,
	).Scan(&b.ID, &b.ParentLSN, &b.PgPort, &b.PgDataDir, &b.SlotName, &b.Status, &b.ParentBranch)
	return b, err
}

func (s *Store) UpdateBranchLSN(ctx context.Context, id, lsn string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE branches SET parent_lsn = $1 WHERE id = $2`,
		lsn, id,
	)
	return err
}

func (s *Store) SaveConflicts(ctx context.Context, id string, conflicts any) error {
	data, err := json.Marshal(conflicts)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx,
		`UPDATE branches SET conflicts = $1, status = 'conflict' WHERE id = $2`,
		string(data), id,
	)
	return err
}

func (s *Store) ClearConflicts(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE branches SET conflicts = NULL, status = 'active' WHERE id = $1`,
		id,
	)
	return err
}

func (s *Store) DeleteBranch(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM branches WHERE id = $1`, id)
	return err
}

func (s *Store) SetBranchTTL(ctx context.Context, id string, ttl time.Duration) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE branches SET expires_at = now() + $1 WHERE id = $2`,
		ttl, id,
	)
	return err
}

func (s *Store) ListExpiredBranches(ctx context.Context) ([]*Branch, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT b.id, b.name, b.project_id, b.parent_lsn, b.pg_port, b.pg_data_dir, b.slot_name, b.status, COALESCE(b.parent_branch, '')
		 FROM branches b
		 WHERE b.expires_at IS NOT NULL AND b.expires_at < now()`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var branches []*Branch
	for rows.Next() {
		b := &Branch{}
		rows.Scan(&b.ID, &b.Name, &b.ProjectID, &b.ParentLSN, &b.PgPort, &b.PgDataDir, &b.SlotName, &b.Status, &b.ParentBranch)
		branches = append(branches, b)
	}
	return branches, nil
}

func (s *Store) NextFreePort(ctx context.Context) (int, error) {
	var maxPort int
	err := s.pool.QueryRow(ctx, `SELECT COALESCE(MAX(pg_port), 5432) FROM branches`).Scan(&maxPort)
	return maxPort + 1, err
}
