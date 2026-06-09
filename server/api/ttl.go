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

	// fire ttl_warning for branches expiring in < 24h
	s.fireTTLWarnings(ctx)
	// fire behind_main for branches that have fallen behind
	s.fireBehindMainWebhooks(ctx)
}

func (s *Server) fireBehindMainWebhooks(ctx context.Context) {
	projects, err := s.store.ListProjects(ctx, "")
	if err != nil {
		return
	}
	for _, project := range projects {
		branches, err := s.store.ListBranches(ctx, project.ID)
		if err != nil {
			continue
		}
		for _, branch := range branches {
			status, err := core.BranchStatus(ctx, project.ConnString, branch)
			if err != nil || !status.BehindMain {
				continue
			}
			detail := fmt.Sprintf("%d change(s) in main", status.PendingMain)
			s.fireWebhooks(ctx, project.ID, project.Name, branch.Name, "behind_main", detail)
		}
	}
}

func (s *Server) fireTTLWarnings(ctx context.Context) {
	branches, err := s.store.ListBranchesExpiringWithin(ctx, 24*time.Hour)
	if err != nil {
		return
	}
	for _, branch := range branches {
		project, err := s.store.GetProject(ctx, branch.ProjectID)
		if err != nil {
			continue
		}
		detail := ""
		if branch.ExpiresAt != nil {
			detail = fmt.Sprintf("expires in %s", time.Until(*branch.ExpiresAt).Round(time.Minute))
		}
		s.fireWebhooks(ctx, project.ID, project.Name, branch.Name, "ttl_warning", detail)
	}
}
