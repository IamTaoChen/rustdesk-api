package http

import (
	"github.com/gin-gonic/gin"
	"github.com/lejianwen/rustdesk-api/v2/global"
	"github.com/lejianwen/rustdesk-api/v2/http/middleware"
	"github.com/lejianwen/rustdesk-api/v2/http/router"
	"github.com/sirupsen/logrus"
	"net/http"
)

func ApiInit() error {
	gin.SetMode(global.Config.Gin.Mode)
	g := gin.New()

	if err := configureTrustedProxies(g, global.Config.Gin.TrustProxy); err != nil { return err }
	registerReadiness(g)

	if global.Config.Gin.Mode == gin.ReleaseMode {
		//修改gin Recovery日志 输出为logger的输出点
		if global.Logger != nil {
			gin.DefaultErrorWriter = global.Logger.WriterLevel(logrus.ErrorLevel)
		}
	}
	g.NoRoute(func(c *gin.Context) {
		c.String(http.StatusNotFound, "404 not found")
	})
	g.Use(middleware.Logger(), middleware.Limiter(), gin.Recovery())
	router.WebInit(g)
	router.Init(g)
	router.ApiInit(g)
	return Run(g, global.Config.Gin.ApiAddr)
}
