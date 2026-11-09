package http

import (
	"net"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lejianwen/rustdesk-api/v2/global"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestProxyTrustIsExplicit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct{ name, configured, remote, expected string }{
		{"default distrust", "", "198.51.100.10:1234", "198.51.100.10"},
		{"loopback trusted", "127.0.0.1, ::1", "127.0.0.1:1234", "192.0.2.7"},
		{"untrusted sender", "127.0.0.1", "198.51.100.10:1234", "198.51.100.10"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := gin.New()
			if err := configureTrustedProxies(g, tc.configured); err != nil {
				t.Fatal(err)
			}
			g.GET("/", func(c *gin.Context) { c.String(200, c.ClientIP()) })
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = tc.remote
			req.Header.Set("X-Forwarded-For", "192.0.2.7")
			response := httptest.NewRecorder()
			g.ServeHTTP(response, req)
			if response.Body.String() != tc.expected {
				t.Fatalf("unexpected source IP %s", response.Body.String())
			}
		})
	}
	for _, value := range []string{"garbage", "127.0.0.1,", ",127.0.0.1"} {
		if err := configureTrustedProxies(gin.New(), value); err == nil {
			t.Fatal("invalid trust list accepted")
		}
	}
}

func TestReadinessRequiresDatabaseAndBypassesBusinessMiddleware(t *testing.T) {
	old := global.DB
	t.Cleanup(func() { global.DB = old })
	g := gin.New()
	registerReadiness(g)
	g.Use(func(c *gin.Context) { c.AbortWithStatus(403) })
	request := func() int {
		response := httptest.NewRecorder()
		g.ServeHTTP(response, httptest.NewRequest("GET", "/api/health", nil))
		return response.Code
	}
	global.DB = nil
	if request() != 503 {
		t.Fatal("uninitialized DB reported ready")
	}
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "ready.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	global.DB = db
	if request() != 200 {
		t.Fatal("live database not ready")
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.Close()
	if request() != 503 {
		t.Fatal("closed database reported ready")
	}
}

func TestRunReturnsBindFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := Run(gin.New(), listener.Addr().String()); err == nil {
		t.Fatal("listener bind failure ignored")
	}
}
