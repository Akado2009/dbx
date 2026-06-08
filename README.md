# dbx — Git-like branching for Postgres

[![CI](https://github.com/akado2009/dbx/actions/workflows/ci.yml/badge.svg)](https://github.com/akado2009/dbx/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/Go-1.21+-blue)](https://go.dev)
[![License: MIT](https://img.shields.io/badge/License-MIT-green)](LICENSE)

Create isolated database branches for feature development, testing, and preview environments. Make changes, see diffs, rebase, merge — just like git, but for your Postgres data.

```bash
# create a branch, make changes, ship it
dbx branch create feature-payments
psql "$BRANCH_URL" -c "ALTER TABLE orders ADD COLUMN paid_at TIMESTAMPTZ"
dbx branch diff feature-payments    # see what changed
dbx branch rebase feature-payments  # sync with main
dbx branch merge feature-payments   # apply to production
```

## Why dbx?

| | dbx | Neon | PlanetScale |
|--|-----|------|-------------|
| Self-hosted | ✅ | ❌ | ❌ |
| Open source | ✅ | partial | ❌ |
| WAL-based branching | ✅ | ✅ | ❌ |
| Branch from branch | ✅ | ✅ | ❌ |
| Conflict detection | ✅ | ❌ | ❌ |
| Works with existing PG | ✅ | ❌ | ❌ |

## How it works

```
Your app
   │
   ▼
main-db (your Postgres)
   │
   ├── WAL slot created at branch point
   │
   ▼
pg_basebackup ──► branch-db (new PG instance, port 5433)
                     │
                     └── WAL slot tracks branch changes
```

- Each branch is a **real Postgres instance** (via `pg_basebackup`)
- Changes tracked via **WAL logical replication slots** (pgoutput protocol)
- Diff shows **exact row-level changes** with old/new values
- Rebase detects **column-level conflicts** between main and branch
- Merge applies **only the delta** — not a full overwrite

## Quickstart

```bash
# 1. start dbx
docker compose up -d

# 2. connect your database
dbx project init myapp "postgresql://user:pass@localhost:5432/myapp"
dbx project use myapp   # saves project ID locally — no -p needed

# 3. branch, change, ship
dbx branch create feature-x
psql "$(dbx branch ls | grep feature-x | awk '{print $NF}')"
# make your changes...
dbx branch diff feature-x
dbx branch rebase feature-x  # if main moved ahead
dbx branch merge feature-x
```

Or open the **Web UI** at [http://localhost:7070](http://localhost:7070).

## Install CLI

```bash
# build from source
git clone https://github.com/akado2009/dbx
cd dbx && make cli
sudo cp dbx-cli /usr/local/bin/dbx
```

## Commands

```
Project
  dbx project init <name> <conn>   Connect a Postgres database
  dbx project ls                   List projects (* = active)
  dbx project use <name>           Set active project (writes .dbx file)

Branch
  dbx branch create <name>         Create branch from main
  dbx branch create <name> \
    --from <parent>                Branch from another branch
    --ttl 24h                      Auto-delete after duration (e.g. 1h, 7d)
  dbx branch ls                    List branches with connection strings
  dbx branch diff <name>           Show changes vs main (row-level)
  dbx branch status <name>         Show sync status, pending changes, conflicts
  dbx branch rebase <name>         Apply main changes to branch
  dbx branch rebase <name> \
    --continue                     Continue after resolving conflicts
  dbx branch merge <name>          Merge branch into main
  dbx branch delete <name>         Delete branch and stop its PG instance
```

## Configuration

```yaml
# ~/.dbx/config.yaml
server: http://localhost:7070
api_key: your-secret-key   # optional, matches DBX_API_KEY on server
projects:
  myapp:
    id: <uuid>
    name: myapp
```

Or use a `.dbx` file in your project directory (written by `dbx project use`).

## Environment variables (server)

| Variable | Default | Description |
|----------|---------|-------------|
| `META_DSN` | `postgresql://dbx:dbx@localhost:5499/dbx` | Internal metadata DB |
| `PORT` | `7070` | HTTP port |
| `DBX_API_KEY` | _(empty)_ | API key — if set, all API calls require it |

## Deploy to Kubernetes

```bash
helm install dbx ./helm/dbx \
  --set mainDatabase.connString="postgresql://user:pass@your-pg:5432/myapp" \
  --set ingress.enabled=true \
  --set ingress.hosts[0].host=dbx.yourcompany.com
```

## Architecture

```
dbx CLI  ──HTTP──►  dbx Server (Go + Gin)
                        │
              ┌─────────┼─────────────┐
              ▼         ▼             ▼
           meta DB   main DB     branch PGs
         (projects/ (your app)  (one per branch,
          branches)              started by dbx)
```

## Requirements

- Docker (for server + branch PG instances)
- Postgres 14+ with `wal_level = logical`
- `max_replication_slots ≥ 10`

The `docker-compose.yml` sets these up automatically for a sample database.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

MIT
