package db

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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
		CREATE TABLE IF NOT EXISTS users (
			id         TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
			email      TEXT UNIQUE NOT NULL,
			created_at TIMESTAMPTZ DEFAULT now()
		);

		CREATE TABLE IF NOT EXISTS api_tokens (
			id         TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
			user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			token      TEXT UNIQUE NOT NULL,
			name       TEXT NOT NULL DEFAULT 'default',
			created_at TIMESTAMPTZ DEFAULT now()
		);

		CREATE TABLE IF NOT EXISTS projects (
			id          TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
			name        TEXT NOT NULL,
			conn_string TEXT NOT NULL,
			user_id     TEXT REFERENCES users(id) ON DELETE CASCADE,
			created_at  TIMESTAMPTZ DEFAULT now(),
			UNIQUE(user_id, name)
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
		-- migrations for existing schemas
		ALTER TABLE branches ADD COLUMN IF NOT EXISTS slot_name TEXT NOT NULL DEFAULT '';
		ALTER TABLE branches ADD COLUMN IF NOT EXISTS conflicts JSONB;
		ALTER TABLE branches ADD COLUMN IF NOT EXISTS parent_branch TEXT;
		ALTER TABLE branches ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ;
		ALTER TABLE projects ADD COLUMN IF NOT EXISTS user_id TEXT REFERENCES users(id) ON DELETE CASCADE;
	`)
	return err
}

type User struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

type APIToken struct {
	ID     string `json:"id"`
	UserID string `json:"user_id"`
	Token  string `json:"token"`
	Name   string `json:"name"`
}

type Project struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	ConnString string `json:"conn_string"`
	UserID     string `json:"user_id,omitempty"`
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

// --- User / Token methods ---

func (s *Store) CreateUser(ctx context.Context, email string) (*User, error) {
	u := &User{Email: email}
	err := s.pool.QueryRow(ctx,
		`INSERT INTO users (email) VALUES ($1) ON CONFLICT (email) DO UPDATE SET email=EXCLUDED.email RETURNING id`,
		email,
	).Scan(&u.ID)
	return u, err
}

func (s *Store) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	u := &User{Email: email}
	err := s.pool.QueryRow(ctx, `SELECT id FROM users WHERE email = $1`, email).Scan(&u.ID)
	return u, err
}

func (s *Store) CreateToken(ctx context.Context, userID, name string) (*APIToken, error) {
	raw := make([]byte, 32)
	rand.Read(raw)
	token := "dbx_" + hex.EncodeToString(raw)

	t := &APIToken{UserID: userID, Token: token, Name: name}
	err := s.pool.QueryRow(ctx,
		`INSERT INTO api_tokens (user_id, token, name) VALUES ($1, $2, $3) RETURNING id`,
		userID, token, name,
	).Scan(&t.ID)
	return t, err
}

func (s *Store) GetUserByToken(ctx context.Context, token string) (*User, error) {
	u := &User{}
	err := s.pool.QueryRow(ctx,
		`SELECT u.id, u.email FROM users u
		 JOIN api_tokens t ON t.user_id = u.id
		 WHERE t.token = $1`,
		token,
	).Scan(&u.ID, &u.Email)
	return u, err
}

func (s *Store) RevokeToken(ctx context.Context, token, userID string) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM api_tokens WHERE token = $1 AND user_id = $2`,
		token, userID,
	)
	return err
}

func (s *Store) ListTokens(ctx context.Context, userID string) ([]*APIToken, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, name, created_at FROM api_tokens WHERE user_id = $1 ORDER BY created_at`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tokens []*APIToken
	for rows.Next() {
		t := &APIToken{UserID: userID}
		var createdAt time.Time
		rows.Scan(&t.ID, &t.Name, &createdAt)
		tokens = append(tokens, t)
	}
	return tokens, nil
}

// --- Project methods ---

func (s *Store) ListProjects(ctx context.Context, userID string) ([]*Project, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, name, conn_string, COALESCE(user_id, '') FROM projects
		 WHERE user_id = $1 OR user_id IS NULL
		 ORDER BY created_at`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var projects []*Project
	for rows.Next() {
		p := &Project{}
		rows.Scan(&p.ID, &p.Name, &p.ConnString, &p.UserID)
		projects = append(projects, p)
	}
	return projects, nil
}

func (s *Store) CreateProject(ctx context.Context, name, connString, userID string) (*Project, error) {
	p := &Project{Name: name, ConnString: connString, UserID: userID}
	err := s.pool.QueryRow(ctx,
		`INSERT INTO projects (name, conn_string, user_id) VALUES ($1, $2, NULLIF($3, '')) RETURNING id`,
		name, connString, userID,
	).Scan(&p.ID)
	return p, err
}

func (s *Store) GetProject(ctx context.Context, id string) (*Project, error) {
	p := &Project{ID: id}
	err := s.pool.QueryRow(ctx,
		`SELECT name, conn_string, COALESCE(user_id, '') FROM projects WHERE id = $1`,
		id,
	).Scan(&p.Name, &p.ConnString, &p.UserID)
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
