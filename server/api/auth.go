package api

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const ctxUserID = "userID"
const ctxUserEmail = "userEmail"

// authMiddleware validates API tokens against the DB.
// Unauthenticated requests are rejected unless the path is public.
// If no token is found in the DB, falls back to legacy DBX_API_KEY env var check.
func (s *Server) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		// public paths — no auth needed
		if path == "/" || path == "/health" || path == "/auth/register" || path == "/auth/login" {
			c.Next()
			return
		}

		token := extractKey(c)
		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "missing API token — run: dbx login",
			})
			return
		}

		user, err := s.store.GetUserByToken(c.Request.Context(), token)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "invalid or expired token",
			})
			return
		}

		c.Set(ctxUserID, user.ID)
		c.Set(ctxUserEmail, user.Email)
		c.Next()
	}
}

func extractKey(c *gin.Context) string {
	if auth := c.GetHeader("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	if key := c.GetHeader("X-API-Key"); key != "" {
		return key
	}
	return c.Query("api_key")
}

func userIDFromCtx(c *gin.Context) string {
	v, _ := c.Get(ctxUserID)
	s, _ := v.(string)
	return s
}
