package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/akado2009/dbx/server/api"
	"github.com/akado2009/dbx/server/db"
)

func main() {
	ctx := context.Background()

	metaDSN := os.Getenv("META_DSN")
	if metaDSN == "" {
		metaDSN = "postgresql://dbx:dbx@localhost:5499/dbx?sslmode=disable"
	}

	store, err := db.New(ctx, metaDSN)
	if err != nil {
		log.Fatalf("failed to connect to meta db: %v", err)
	}
	defer store.Close()

	if err := store.Migrate(ctx); err != nil {
		log.Fatalf("migration failed: %v", err)
	}

	// restart branch PG instances in background — don't block HTTP server startup
	go recoverBranches(ctx, store)

	port := os.Getenv("PORT")
	if port == "" {
		port = "7070"
	}

	srv := api.NewServer(store)

	// start TTL reaper — checks every 5 minutes for expired branches
	srv.StartTTLReaper(ctx, 5*time.Minute)

	log.Printf("dbx server listening on :%s", port)
	srv.Run(":" + port)
}

// recoverBranches restarts all active branch PG instances on server startup.
func recoverBranches(ctx context.Context, store *db.Store) {
	projects, err := store.ListProjects(ctx, "") // "" = all projects (admin recovery)
	if err != nil {
		log.Printf("recover: list projects: %v", err)
		return
	}

	var wg sync.WaitGroup
	for _, p := range projects {
		branches, err := store.ListBranches(ctx, p.ID)
		if err != nil {
			log.Printf("recover: list branches for %s: %v", p.ID, err)
			continue
		}
		for _, b := range branches {
			if b.Status != "active" {
				continue
			}
			wg.Add(1)
			go func(b *db.Branch) {
				defer wg.Done()
				log.Printf("recover: starting branch %s (port %d)", b.Name, b.PgPort)
				if err := startBranchPG(b.PgDataDir, b.PgPort); err != nil {
					log.Printf("recover: failed to start branch %s: %v", b.Name, err)
				} else {
					log.Printf("recover: branch %s ready on port %d", b.Name, b.PgPort)
				}
			}(b)
		}
	}
	wg.Wait()
	log.Printf("recover: done")
}

func startBranchPG(dataDir string, port int) error {
	// remove stale postmaster.pid left by previous container run
	os.Remove(dataDir + "/postmaster.pid")

	var cmd *exec.Cmd
	if os.Getuid() == 0 {
		cmd = exec.Command("gosu", "postgres", "pg_ctl", "start", "-D", dataDir, "-w", "-t", "30")
	} else {
		cmd = exec.Command("pg_ctl", "start", "-D", dataDir, "-w", "-t", "30")
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		// verify PG is actually accepting connections — if yes, treat as success
		var ready *exec.Cmd
		if os.Getuid() == 0 {
			ready = exec.Command("gosu", "postgres", "pg_isready", "-p", fmt.Sprintf("%d", port), "-q")
		} else {
			ready = exec.Command("pg_isready", "-p", fmt.Sprintf("%d", port), "-q")
		}
		if ready.Run() == nil {
			return nil // PG is up despite pg_ctl error
		}
		return fmt.Errorf("%w: %s", err, string(out))
	}
	return nil
}
