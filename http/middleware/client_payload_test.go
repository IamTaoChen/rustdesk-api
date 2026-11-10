package middleware

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lejianwen/rustdesk-api/v2/global"
)

func TestClientPayload(t *testing.T) {
	const payload = `{"id":"test-device","unknown_client_field":{"tags":["test"]}}`
	for _, tc := range []struct {
		name, path string
		enabled    bool
		wantLog    bool
	}{
		{"heartbeat", "/api/heartbeat", true, true},
		{"legacy book", "/api/ab", true, true},
		{"peer upload", "/api/ab/peer/add/1", true, true},
		{"disabled", "/api/heartbeat", false, false},
		{"login excluded", "/api/login", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			previousEnabled := global.Config.Logger.DebugClientPayload
			global.Config.Logger.DebugClientPayload = tc.enabled
			t.Cleanup(func() { global.Config.Logger.DebugClientPayload = previousEnabled })
			var output bytes.Buffer
			previous := log.Writer()
			log.SetOutput(&output)
			defer log.SetOutput(previous)
			r := gin.New()
			r.Use(ClientPayload())
			r.POST(tc.path, func(c *gin.Context) {
				body, err := io.ReadAll(c.Request.Body)
				if err != nil || string(body) != payload {
					t.Errorf("downstream body changed: %q, %v", body, err)
				}
				c.Status(http.StatusNoContent)
			})
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(payload)))
			if w.Code != http.StatusNoContent || strings.Contains(output.String(), payload) != tc.wantLog {
				t.Fatalf("status=%d log=%q", w.Code, output.String())
			}
		})
	}
}
