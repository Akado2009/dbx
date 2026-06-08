package api

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/akado2009/dbx/server/core"
)

// parseTTL parses duration strings like "24h", "7d", "30m".
// Extends standard Go time.ParseDuration with "d" for days.
func parseTTL(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil {
			return 0, fmt.Errorf("invalid ttl: %s", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

// StartTTLReaper launches a background goroutine that periodically
// deletes branches whose expires_at has passed.
func (s *Server) StartTTLReaper(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		log.Printf("[ttl] reaper started, interval=%s", interval)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.reapExpiredBranches(ctx)
			}
		}
	}()
}

func (s *Server) reapExpiredBranches(ctx context.Context) {
	branches, err := s.store.ListExpiredBranches(ctx)
	if err != nil {
		log.Printf("[ttl] list expired branches: %v", err)
		return
	}
	for _, branch := range branches {
		project, err := s.store.GetProject(ctx, branch.ProjectID)
		if err != nil {
			log.Printf("[ttl] get project for branch %s: %v", branch.Name, err)
			s.store.DeleteBranch(ctx, branch.ID)
			continue
		}
		log.Printf("[ttl] deleting expired branch %s (project %s)", branch.Name, project.Name)
		if err := core.DeleteBranch(ctx, project.ConnString, branch); err != nil {
			log.Printf("[ttl] delete branch PG %s: %v", branch.Name, err)
		}
		s.store.DeleteBranch(ctx, branch.ID)
		log.Printf("[ttl] deleted branch %s", branch.Name)
	}
}
