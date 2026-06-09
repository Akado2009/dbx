package api

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/akado2009/dbx/server/core"
	"github.com/akado2009/dbx/server/db"
	"github.com/gin-gonic/gin"
)

func (s *Server) health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (s *Server) register(c *gin.Context) {
	var req struct {
		Email string `json:"email" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx := c.Request.Context()
	user, err := s.store.CreateUser(ctx, req.Email)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	token, err := s.store.CreateToken(ctx, user.ID, "default")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"token": token.Token,
		"email": user.Email,
		"user_id": user.ID,
	})
}

func (s *Server) login(c *gin.Context) {
	var req struct {
		Email string `json:"email" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx := c.Request.Context()
	user, err := s.store.GetUserByEmail(ctx, req.Email)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "user not found — run: dbx register"})
		return
	}
	token, err := s.store.CreateToken(ctx, user.ID, "cli")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"token": token.Token,
		"email": user.Email,
		"user_id": user.ID,
	})
}

func (s *Server) me(c *gin.Context) {
	ctx := c.Request.Context()
	userID := userIDFromCtx(c)
	tokens, _ := s.store.ListTokens(ctx, userID)
	email, _ := c.Get(ctxUserEmail)
	c.JSON(http.StatusOK, gin.H{
		"user_id": userID,
		"email":   email,
		"tokens":  len(tokens),
	})
}

func (s *Server) revokeToken(c *gin.Context) {
	token := extractKey(c)
	userID := userIDFromCtx(c)
	if err := s.store.RevokeToken(c.Request.Context(), token, userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "token revoked"})
}

func (s *Server) listProjects(c *gin.Context) {
	userID := userIDFromCtx(c)
	projects, err := s.store.ListProjects(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	type projectView struct {
		ID               string `json:"id"`
		Name             string `json:"name"`
		ConnectionString string `json:"connection_string"`
	}
	views := make([]projectView, len(projects))
	for i, p := range projects {
		views[i] = projectView{ID: p.ID, Name: p.Name, ConnectionString: p.ConnString}
	}
	c.JSON(http.StatusOK, views)
}

func (s *Server) createProject(c *gin.Context) {
	var req struct {
		Name             string `json:"name" binding:"required"`
		ConnectionString string `json:"connection_string" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID := userIDFromCtx(c)
	p, err := s.store.CreateProject(c.Request.Context(), req.Name, req.ConnectionString, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"id": p.ID, "name": p.Name})
}

func (s *Server) listBranches(c *gin.Context) {
	ctx := c.Request.Context()
	projectID := c.Param("projectID")

	project, err := s.store.GetProject(ctx, projectID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "project not found"})
		return
	}

	branches, err := s.store.ListBranches(ctx, projectID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// enrich each branch with a ready-to-use connection string
	type branchView struct {
		ID               string     `json:"id"`
		Name             string     `json:"name"`
		PgPort           int        `json:"pg_port"`
		Status           string     `json:"status"`
		ParentLSN        string     `json:"parent_lsn"`
		ParentBranch     string     `json:"parent_branch"`
		ConnectionString string     `json:"connection_string"`
		ExpiresAt        *time.Time `json:"expires_at,omitempty"`
	}
	views := make([]branchView, len(branches))
	for i, b := range branches {
		views[i] = branchView{
			ID:               b.ID,
			Name:             b.Name,
			PgPort:           b.PgPort,
			Status:           b.Status,
			ParentLSN:        b.ParentLSN,
			ParentBranch:     b.ParentBranch,
			ConnectionString: core.BranchConnString(project.ConnString, b.PgPort),
			ExpiresAt:        b.ExpiresAt,
		}
	}
	c.JSON(http.StatusOK, views)
}

func (s *Server) createBranch(c *gin.Context) {
	var req struct {
		Name string `json:"name" binding:"required"`
		From string `json:"from"` // optional: branch name to branch from (default: main)
		TTL  string `json:"ttl"` // optional: e.g. "24h", "7d"
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()
	projectID := c.Param("projectID")

	project, err := s.store.GetProject(ctx, projectID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "project not found"})
		return
	}

	// determine source PG — main or another branch
	sourceConnStr := project.ConnString
	parentBranch := ""
	if req.From != "" {
		fromBranch, err := s.store.GetBranch(ctx, projectID, req.From)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "source branch not found: " + req.From})
			return
		}
		sourceConnStr = core.BranchConnString(project.ConnString, fromBranch.PgPort)
		parentBranch = req.From
	}

	port, err := s.store.NextFreePort(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	dataDir := fmt.Sprintf("/var/lib/dbx/branches/%s/%s", projectID, req.Name)

	// generate a branch ID for slot naming before DB insert
	branchID := fmt.Sprintf("%s-%s", projectID, req.Name)
	branch, err := core.CreateBranch(ctx, project.ConnString, sourceConnStr, branchID, req.Name, port, dataDir)
	if err != nil {
		log.Printf("CreateBranch error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	branch.ProjectID = projectID
	branch.ParentBranch = parentBranch

	if err := s.store.CreateBranch(ctx, branch); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	detail := "from main"
	if parentBranch != "" {
		detail = "from branch " + parentBranch
	}
	s.store.LogEvent(ctx, branch.ID, "created", detail)

	// set TTL if requested (support "7d" shorthand in addition to Go durations)
	if req.TTL != "" {
		if ttl, err := parseTTL(req.TTL); err == nil {
			s.store.SetBranchTTL(ctx, branch.ID, ttl)
		}
	}

	c.JSON(http.StatusCreated, gin.H{
		"id":                branch.ID,
		"name":              branch.Name,
		"connection_string": core.BranchConnString(project.ConnString, port),
	})
}

func (s *Server) deleteBranch(c *gin.Context) {
	ctx := c.Request.Context()
	projectID := c.Param("projectID")
	name := c.Param("name")

	project, err := s.store.GetProject(ctx, projectID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "project not found"})
		return
	}

	branch, err := s.store.GetBranch(ctx, projectID, name)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "branch not found"})
		return
	}

	if err := core.DeleteBranch(ctx, project.ConnString, branch); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	s.store.DeleteBranch(ctx, branch.ID)
	c.JSON(http.StatusOK, gin.H{"deleted": true})
}

func (s *Server) rebaseBranch(c *gin.Context) {
	ctx := c.Request.Context()
	projectID := c.Param("projectID")
	name := c.Param("name")

	project, err := s.store.GetProject(ctx, projectID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "project not found"})
		return
	}

	branch, err := s.store.GetBranch(ctx, projectID, name)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "branch not found"})
		return
	}

	result, err := core.RebaseBranch(ctx, project.ConnString, branch)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if len(result.Conflicts) > 0 {
		s.store.SaveConflicts(ctx, branch.ID, result.Conflicts)
		s.store.LogEvent(ctx, branch.ID, "conflict_detected", fmt.Sprintf("%d conflict(s)", len(result.Conflicts)))
		c.JSON(http.StatusConflict, gin.H{"conflicts": result.Conflicts})
		return
	}

	s.store.UpdateBranchLSN(ctx, branch.ID, result.NewLSN)
	s.store.LogEvent(ctx, branch.ID, "rebased", "")
	c.JSON(http.StatusOK, gin.H{"rebased": true, "new_lsn": result.NewLSN})
}

func (s *Server) rebaseContinue(c *gin.Context) {
	ctx := c.Request.Context()
	projectID := c.Param("projectID")
	name := c.Param("name")

	branch, err := s.store.GetBranch(ctx, projectID, name)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "branch not found"})
		return
	}
	if branch.Status != "conflict" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "branch has no pending conflicts"})
		return
	}

	project, err := s.store.GetProject(ctx, projectID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "project not found"})
		return
	}

	// User resolved conflicts manually in the branch DB.
	// Accept their resolution: advance main slot to current LSN and update ParentLSN.
	newLSN, err := core.AcceptRebase(ctx, project.ConnString, branch)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	s.store.ClearConflicts(ctx, branch.ID)
	s.store.UpdateBranchLSN(ctx, branch.ID, newLSN)
	s.store.LogEvent(ctx, branch.ID, "conflict_resolved", "")
	c.JSON(http.StatusOK, gin.H{"rebased": true, "new_lsn": newLSN})
}

func (s *Server) mergeBranch(c *gin.Context) {
	ctx := c.Request.Context()
	projectID := c.Param("projectID")
	name := c.Param("name")

	project, err := s.store.GetProject(ctx, projectID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "project not found"})
		return
	}

	branch, err := s.store.GetBranch(ctx, projectID, name)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "branch not found"})
		return
	}

	if err := core.MergeBranch(ctx, project.ConnString, branch); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	s.store.LogEvent(ctx, branch.ID, "merged", "into main")
	// stop branch PG so its port is freed for future branches
	core.StopBranch(branch)
	s.store.DeleteBranch(ctx, branch.ID)
	c.JSON(http.StatusOK, gin.H{"merged": true})
}

func (s *Server) diffBranch(c *gin.Context) {
	ctx := c.Request.Context()
	projectID := c.Param("projectID")
	name := c.Param("name")

	project, err := s.store.GetProject(ctx, projectID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "project not found"})
		return
	}

	branch, err := s.store.GetBranch(ctx, projectID, name)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "branch not found"})
		return
	}

	changes, err := s.diffRecursive(ctx, project.ConnString, projectID, branch)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"changes": changes})
}


// diffRecursive computes diff = parent's diff merged with own changes.
// For direct-from-main branches: just own changes.
// For branch-from-branch: parent changes + own changes (own wins per PK).
func (s *Server) diffRecursive(ctx context.Context, mainConnStr, projectID string, branch *db.Branch) ([]*core.Change, error) {
	ownChanges, err := core.DiffBranch(ctx, mainConnStr, branch)
	if err != nil {
		return nil, err
	}

	if branch.ParentBranch == "" {
		return ownChanges, nil
	}

	// get parent branch and its changes recursively
	parentBranch, err := s.store.GetBranch(ctx, projectID, branch.ParentBranch)
	if err != nil {
		// parent deleted — just return own changes
		return ownChanges, nil
	}

	parentChanges, err := s.diffRecursive(ctx, mainConnStr, projectID, parentBranch)
	if err != nil {
		return ownChanges, nil
	}

	return core.MergeChangeSets(parentChanges, ownChanges), nil
}

func (s *Server) statusBranch(c *gin.Context) {
	ctx := c.Request.Context()
	projectID := c.Param("projectID")
	name := c.Param("name")

	project, err := s.store.GetProject(ctx, projectID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "project not found"})
		return
	}

	branch, err := s.store.GetBranch(ctx, projectID, name)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "branch not found"})
		return
	}

	status, err := core.BranchStatus(ctx, project.ConnString, branch)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, status)
}

func (s *Server) branchLog(c *gin.Context) {
	ctx := c.Request.Context()
	projectID := c.Param("projectID")
	name := c.Param("name")

	branch, err := s.store.GetBranch(ctx, projectID, name)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "branch not found"})
		return
	}

	events, err := s.store.GetBranchLog(ctx, branch.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if events == nil {
		events = []*db.BranchEvent{}
	}
	c.JSON(http.StatusOK, gin.H{"branch": name, "events": events})
}
