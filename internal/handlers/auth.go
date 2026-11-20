package handlers

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// RequireAPIKey rejects requests that do not send
// "Authorization: Bearer <apiKey>". The comparison is constant-time.
func RequireAPIKey(apiKey string) gin.HandlerFunc {
	expected := []byte(apiKey)

	return func(c *gin.Context) {
		token, ok := strings.CutPrefix(c.GetHeader("Authorization"), "Bearer ")
		if !ok || len(expected) == 0 ||
			subtle.ConstantTimeCompare([]byte(strings.TrimSpace(token)), expected) != 1 {
			c.Header("WWW-Authenticate", `Bearer realm="api"`)
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "Invalid or missing API key",
			})
			return
		}

		c.Next()
	}
}
