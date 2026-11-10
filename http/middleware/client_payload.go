package middleware

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lejianwen/rustdesk-api/v2/global"
)

// ClientPayload logs complete client payloads only when explicitly enabled for diagnosis.
func ClientPayload() gin.HandlerFunc {
	enabled := global.Config.Logger.DebugClientPayload
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		if enabled && c.Request.Method != http.MethodGet &&
			(path == "/api/heartbeat" || path == "/api/ab" || strings.HasPrefix(path, "/api/ab/")) {
			body, err := io.ReadAll(c.Request.Body)
			// Restore even partial reads so downstream binding keeps its normal behavior.
			c.Request.Body = io.NopCloser(io.MultiReader(bytes.NewReader(body), c.Request.Body))
			log.Printf("[client-payload] method=%s uri=%q ip=%q body=%s read_error=%v",
				c.Request.Method, c.Request.URL.RequestURI(), c.ClientIP(), body, err)
		}
		c.Next()
	}
}
