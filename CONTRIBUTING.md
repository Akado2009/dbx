# Contributing to dbx

## Prerequisites

- Go 1.21+
- Docker + Docker Compose
- `psql` (postgres client)

## Local development

```bash
git clone https://github.com/akado2009/dbx
cd dbx

# start dependencies (main DB + meta DB)
make up

# build CLI
make cli
sudo cp dbx-cli /usr/local/bin/dbx

# init a project
dbx project init myapp "postgresql://myapp@localhost:5432/myapp"
dbx project use myapp

# create a branch and try it out
dbx branch create feature-x
dbx branch ls
```

## Project structure

```
dbx/
├── cli/cmd/          # CLI commands (cobra)
├── server/
│   ├── api/          # HTTP handlers (gin), web UI static files
│   ├── core/         # WAL branching logic (branch, merge, rebase, diff)
│   └── db/           # Meta DB store (pgx)
├── helm/dbx/         # Helm chart for k8s
├── initdb/           # Postgres init scripts
├── Dockerfile.server
└── docker-compose.yml
```

## Running tests

```bash
# unit tests only (fast, no Docker needed)
go test ./server/core/... -run "TestParse|TestDetect|TestApply|TestBuild|TestSlot" -v

# all tests including integration (requires Docker for testcontainers)
make test

# specific test
go test ./server/core/... -run TestRebaseBranch_WithConflicts -v
```

Integration tests use [testcontainers-go](https://testcontainers.com/guides/getting-started-with-testcontainers-for-go/) — they spin up real Postgres containers automatically.

## How WAL branching works

1. **Create branch**: create replication slot on main → `pg_basebackup` → start new PG instance → create slot on branch
2. **Diff**: peek WAL slot on branch → parse pgoutput binary protocol → return row-level changes
3. **Rebase**: get changes from main slot → detect conflicts → apply to branch → advance slot
4. **Merge**: get changes from branch slot → apply to main → stop branch PG

Key files:
- `server/core/wal.go` — slot management, change capture, conflict detection
- `server/core/pgoutput.go` — pgoutput binary protocol parser
- `server/core/branch.go` — pg_basebackup, pg_ctl wrappers
- `server/core/rebase.go` — rebase logic
- `server/core/merge.go` — merge + diff logic

## Adding a new command

1. Add handler in `server/api/handler.go`
2. Register route in `server/api/server.go`
3. Add CLI command in `cli/cmd/`
4. Write a test

## Pull requests

- Keep PRs focused — one feature or fix per PR
- Add tests for new functionality
- Run `go build ./...` and `go test ./...` before submitting
- Follow existing code style (no linter config yet — just match surrounding code)

## Environment variables

| Variable | Default | Description |
|----------|---------|-------------|
| `META_DSN` | `postgresql://dbx:dbx@localhost:5499/dbx` | Meta DB connection |
| `PORT` | `7070` | HTTP server port |
| `DBX_API_KEY` | _(empty = auth disabled)_ | API key for authentication |
