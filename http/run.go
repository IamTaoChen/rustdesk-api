//go:build !windows

package http

import (
	"errors"
	"net/http"
	"github.com/fvbock/endless"
	"github.com/gin-gonic/gin"
)

func Run(g *gin.Engine, addr string) error {
	err := endless.ListenAndServe(addr, g)
	if errors.Is(err, http.ErrServerClosed) { return nil }
	return err
}
