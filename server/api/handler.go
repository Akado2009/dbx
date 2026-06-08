package api

import (
	"fmt"
	"log"
	"net/http"

	"github.com/akado2009/dbx/server/core"
	"github.com/gin-gonic/gin"
)

func (s *Server) health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (s *Server) listProjects(c *gin.Context) {
	projects, err := s.store.ListProjects(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, projects)
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

	p, err := s.store.CreateProject(c.Request.Context(), req.Name, req.ConnectionString)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"id": p.ID, "name": p.Name})
}

func (s *Server) listBranches(c *gin.Context) {
	branches, err := s.store.ListBranches(c.Request.Context(), c.Param("projectID"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, branches)
}

func (s *Server) createBranch(c *gin.Context) {
	var req struct {
		Name string `json:"name" binding:"required"`
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

	port, err := s.store.NextFreePort(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	dataDir := fmt.Sprintf("/var/lib/dbx/branches/%s/%s", projectID, req.Name)

	// generate a branch ID for slot naming before DB insert
	branchID := fmt.Sprintf("%s-%s", projectID, req.Name)
	branch, err := core.CreateBranch(ctx, project.ConnString, branchID, req.Name, port, dataDir)
	if err != nil {
		log.Printf("CreateBranch error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	branch.ProjectID = projectID

	if err := s.store.CreateBranch(ctx, branch); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"id":                branch.ID,
		"name":              branch.Name,
		"connection_string": fmt.Sprintf("postgresql://localhost:%d/dbx", port),
	})
}

func (s *Server) deleteBranch(c *gin.Context) {
	ctx := c.Request.Context()
	projectID := c.Param("projectID")
	name := c.Param("name")

	branch, err := s.store.GetBranch(ctx, projectID, name)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "branch not found"})
		return
	}

	if err := core.StopBranch(branch.PgPort, branch.PgDataDir); err != nil {
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
		c.JSON(http.StatusConflict, gin.H{"conflicts": result.Conflicts})
		return
	}

	s.store.UpdateBranchLSN(ctx, branch.ID, result.NewLSN)
	c.JSON(http.StatusOK, gin.H{"rebased": true, "new_lsn": result.NewLSN})
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

	core.StopBranch(branch.PgPort, branch.PgDataDir)
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

	changes, err := core.DiffBranch(ctx, project.ConnString, branch)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"changes": changes})
}
