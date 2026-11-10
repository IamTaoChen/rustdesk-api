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
		{"login", "/api/login", true, true},
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

func TestClientPayloadRedaction(t *testing.T) {
	for _, tc := range []struct{ method, target, body, contentType string }{
		{"POST", "/api/login?access_token=query-secret&search=device", `{"username":"alice","password":"password-secret","nested":[{"access_token":"token-secret","privateKey":"key-secret"}]}`, "application/json"},
		{"GET", "/api/peers?token=query-secret&search=device", "", ""},
		{"POST", "/api/login", "password=password-secret&username=alice", "application/x-www-form-urlencoded"},
		{"POST", "/api/login", `{"password":"password-secret"`, "application/json"},
	} {
		t.Run(tc.method+tc.target+tc.body, func(t *testing.T) {
			old := global.Config.Logger.DebugClientPayload
			global.Config.Logger.DebugClientPayload = true
			defer func() { global.Config.Logger.DebugClientPayload = old }()
			var output bytes.Buffer
			writer := log.Writer()
			log.SetOutput(&output)
			defer log.SetOutput(writer)
			r := gin.New()
			r.Use(ClientPayload())
			r.Handle(tc.method, strings.Split(tc.target, "?")[0], func(c *gin.Context) {
				body, _ := io.ReadAll(c.Request.Body)
				if string(body) != tc.body {
					t.Fatalf("body changed: %q", body)
				}
				c.Status(http.StatusAccepted)
			})
			req := httptest.NewRequest(tc.method, tc.target, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", tc.contentType)
			r.ServeHTTP(httptest.NewRecorder(), req)
			got := output.String()
			for _, secret := range []string{"password-secret", "token-secret", "key-secret", "query-secret"} {
				if strings.Contains(got, secret) {
					t.Errorf("secret leaked: %s", got)
				}
			}
			for _, expected := range []string{"method=" + tc.method, "status=202"} {
				if !strings.Contains(got, expected) {
					t.Errorf("missing %s: %s", expected, got)
				}
			}
		})
	}
}
