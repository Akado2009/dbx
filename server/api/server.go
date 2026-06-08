package api

import (
	"embed"
	"net/http"

	"github.com/akado2009/dbx/server/db"
	"github.com/gin-gonic/gin"
)

//go:embed static
var staticFiles embed.FS

type Server struct {
	store  *db.Store
	router *gin.Engine
}

func NewServer(store *db.Store) *Server {
	r := gin.Default()
	r.RedirectTrailingSlash = false
	s := &Server{store: store, router: r}
	s.routes()
	return s
}

func (s *Server) Run(addr string) error {
	return s.router.Run(addr)
}

func (s *Server) routes() {
	// serve web UI — read index.html directly from embed
	s.router.GET("/", func(c *gin.Context) {
		data, err := staticFiles.ReadFile("static/index.html")
		if err != nil {
			c.String(http.StatusInternalServerError, "UI not found")
			return
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", data)
	})

	s.router.GET("/health", s.health)
	s.router.GET("/projects", s.listProjects)
	s.router.POST("/projects", s.createProject)
	s.router.GET("/projects/:projectID/branches", s.listBranches)
	s.router.POST("/projects/:projectID/branches", s.createBranch)
	s.router.DELETE("/projects/:projectID/branches/:name", s.deleteBranch)
	s.router.POST("/projects/:projectID/branches/:name/rebase", s.rebaseBranch)
	s.router.POST("/projects/:projectID/branches/:name/rebase/continue", s.rebaseContinue)
	s.router.POST("/projects/:projectID/branches/:name/merge", s.mergeBranch)
	s.router.GET("/projects/:projectID/branches/:name/diff", s.diffBranch)
	s.router.GET("/projects/:projectID/branches/:name/status", s.statusBranch)
}
