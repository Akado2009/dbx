# dbx — Git-like branching for Postgres

Create isolated database branches for feature development. Make changes, see diffs, rebase, merge — just like git, but for your data.

```bash
dbx branch create feature-auth
# → Branch created, connection: postgresql://localhost:5433/myapp

psql postgresql://localhost:5433/myapp -c "ALTER TABLE users ADD COLUMN auth_token TEXT"

dbx branch diff feature-auth
# → ~ UPDATE users ...

dbx branch rebase feature-auth  # sync with main
dbx branch merge feature-auth   # ship it
```

## How it works

- Each branch is a real Postgres instance (via `pg_basebackup`)
- Changes are tracked via WAL logical replication slots
- Diff, rebase and merge use WAL-based change capture (no schema snapshots)

## Quickstart

```bash
# start dbx server + sample postgres
docker compose up -d

# connect your database
dbx project init myapp "postgresql://user:pass@main-db:5432/myapp"

# create a branch
dbx branch create feature-x -p <project-id>

# connect to branch and make changes
psql postgresql://localhost:5433/myapp

# see what changed
dbx branch diff feature-x -p <project-id>

# sync with main and merge
dbx branch rebase feature-x -p <project-id>
dbx branch merge feature-x -p <project-id>
```

## Install

```bash
# build CLI
make cli

# or run everything in docker
make up
```

## Commands

```
dbx project init <name> <conn-string>   Connect a Postgres database
dbx project ls                          List projects

dbx branch create <name>                Create a branch
dbx branch ls                           List branches with connection strings
dbx branch diff <name>                  Show changes vs main
dbx branch rebase <name>                Sync branch with latest main
dbx branch rebase <name> --continue     Continue after resolving conflicts
dbx branch merge <name>                 Merge branch into main
dbx branch delete <name>                Delete branch and stop its PG instance
```

## Requirements

- Docker (for server + branch PG instances)
- Postgres 14+ on main database (logical replication must be enabled)
- `wal_level = logical` on main DB

## Architecture

```
dbx CLI  →  dbx Server (Go + Gin)
               ├── meta DB (Postgres) — stores projects/branches metadata
               ├── main DB — your application database
               └── branch PG instances — one per branch, started by dbx server
```

WAL-based change tracking means:
- Branch creation is fast (pg_basebackup + slot, not full copy)
- Diff shows exact row-level changes with old/new values
- Merge applies only the delta, not a full overwrite
