package http

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lejianwen/rustdesk-api/v2/global"
)

func configureTrustedProxies(g *gin.Engine, configured string) error {
	if err := g.SetTrustedProxies(nil); err != nil {
		return err
	}
	if strings.TrimSpace(configured) == "" {
		return nil
	}
	values := strings.Split(configured, ",")
	for i, value := range values {
		values[i] = strings.TrimSpace(value)
		if values[i] == "" {
			return errors.New("empty entry in trusted proxy list")
		}
	}
	return g.SetTrustedProxies(values)
}

// Register before business middleware: login/IP bans must not hide readiness.
func registerReadiness(g *gin.Engine) {
	g.GET("/api/health", func(c *gin.Context) {
		if global.DB == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable"})
			return
		}
		db, err := global.DB.DB()
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), time.Second)
		defer cancel()
		if err := db.PingContext(ctx); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ready"})
	})
}
