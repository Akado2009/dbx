package api

import (
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
)

// authMiddleware checks for a valid API key if DBX_API_KEY env var is set.
// If DBX_API_KEY is empty, auth is disabled (dev mode).
// Accepts key via:
//   - Header: Authorization: Bearer <key>
//   - Header: X-API-Key: <key>
//   - Query:  ?api_key=<key>
func authMiddleware() gin.HandlerFunc {
	apiKey := os.Getenv("DBX_API_KEY")
	if apiKey == "" {
		// auth disabled — allow all
		return func(c *gin.Context) { c.Next() }
	}

	return func(c *gin.Context) {
		// skip auth for UI and health
		path := c.Request.URL.Path
		if path == "/" || path == "/health" {
			c.Next()
			return
		}

		key := extractKey(c)
		if key != apiKey {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "unauthorized — set Authorization: Bearer <key> or X-API-Key header",
			})
			return
		}
		c.Next()
	}
}

func extractKey(c *gin.Context) string {
	// Authorization: Bearer <key>
	if auth := c.GetHeader("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	// X-API-Key: <key>
	if key := c.GetHeader("X-API-Key"); key != "" {
		return key
	}
	// ?api_key=<key>
	return c.Query("api_key")
}
