package db

import (
	"context"

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
			status      TEXT NOT NULL DEFAULT 'active',
			created_at  TIMESTAMPTZ DEFAULT now(),
			UNIQUE(project_id, name)
		);
	`)
	return err
}

type Project struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	ConnString string `json:"conn_string"`
}

type Branch struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	Name      string `json:"name"`
	ParentLSN string `json:"parent_lsn"`
	PgPort    int    `json:"pg_port"`
	PgDataDir string `json:"pg_data_dir"`
	Status    string `json:"status"`
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
		`INSERT INTO branches (project_id, name, parent_lsn, pg_port, pg_data_dir)
		 VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		b.ProjectID, b.Name, b.ParentLSN, b.PgPort, b.PgDataDir,
	).Scan(&b.ID)
}

func (s *Store) ListBranches(ctx context.Context, projectID string) ([]*Branch, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, name, parent_lsn, pg_port, pg_data_dir, status FROM branches WHERE project_id = $1`,
		projectID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var branches []*Branch
	for rows.Next() {
		b := &Branch{ProjectID: projectID}
		rows.Scan(&b.ID, &b.Name, &b.ParentLSN, &b.PgPort, &b.PgDataDir, &b.Status)
		branches = append(branches, b)
	}
	return branches, nil
}

func (s *Store) GetBranch(ctx context.Context, projectID, name string) (*Branch, error) {
	b := &Branch{ProjectID: projectID, Name: name}
	err := s.pool.QueryRow(ctx,
		`SELECT id, parent_lsn, pg_port, pg_data_dir, status FROM branches WHERE project_id = $1 AND name = $2`,
		projectID, name,
	).Scan(&b.ID, &b.ParentLSN, &b.PgPort, &b.PgDataDir, &b.Status)
	return b, err
}

func (s *Store) UpdateBranchLSN(ctx context.Context, id, lsn string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE branches SET parent_lsn = $1 WHERE id = $2`,
		lsn, id,
	)
	return err
}

func (s *Store) DeleteBranch(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM branches WHERE id = $1`, id)
	return err
}

func (s *Store) NextFreePort(ctx context.Context) (int, error) {
	var maxPort int
	err := s.pool.QueryRow(ctx, `SELECT COALESCE(MAX(pg_port), 5432) FROM branches`).Scan(&maxPort)
	return maxPort + 1, err
}
