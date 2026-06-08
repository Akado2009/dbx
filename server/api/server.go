package api

import (
	"github.com/akado2009/dbx/server/db"
	"github.com/gin-gonic/gin"
)

type Server struct {
	store  *db.Store
	router *gin.Engine
}

func NewServer(store *db.Store) *Server {
	s := &Server{store: store, router: gin.Default()}
	s.routes()
	return s
}

func (s *Server) Run(addr string) error {
	return s.router.Run(addr)
}

func (s *Server) routes() {
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
