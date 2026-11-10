package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"mime"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lejianwen/rustdesk-api/v2/global"
)

// ClientPayload logs API requests with sensitive fields redacted when enabled.
func ClientPayload() gin.HandlerFunc {
	enabled := global.Config.Logger.DebugClientPayload
	return func(c *gin.Context) {
		if !enabled {
			c.Next()
			return
		}
		var body []byte
		var readErr error
		if c.Request.Body != nil {
			body, readErr = io.ReadAll(c.Request.Body)
			// Restore even partial reads for downstream handlers.
			c.Request.Body = io.NopCloser(io.MultiReader(bytes.NewReader(body), c.Request.Body))
		}
		uri := c.Request.URL.Path
		if query := c.Request.URL.RawQuery; query != "" {
			if values, err := url.ParseQuery(query); err == nil {
				redactPayloadValues(values)
				uri += "?" + values.Encode()
			} else {
				uri += "?[omitted]"
			}
		}
		safeBody := redactClientPayload(body, c.GetHeader("Content-Type"))
		c.Next()
		// Do not print read errors: custom readers may embed request data in errors.
		log.Printf("[client-payload] method=%s uri=%q ip=%q status=%d body=%s read_error=%t",
			c.Request.Method, uri, c.ClientIP(), c.Writer.Status(), safeBody, readErr != nil)
	}
}

func sensitivePayloadField(key string) bool {
	key = strings.ToLower(strings.NewReplacer("-", "", "_", "", ":", "").Replace(key))
	for _, part := range []string{"password", "passwd", "pwd", "token", "secret", "credential", "authorization", "cookie", "privatekey", "licensekey", "licencekey"} {
		if strings.Contains(key, part) {
			return true
		}
	}
	return key == "key" || key == "code"
}

func redactPayloadValues(values url.Values) {
	for key := range values {
		if sensitivePayloadField(key) {
			values[key] = []string{"[REDACTED]"}
		}
	}
}

func redactPayloadJSON(value interface{}) {
	switch v := value.(type) {
	case map[string]interface{}:
		for key, child := range v {
			if sensitivePayloadField(key) {
				v[key] = "[REDACTED]"
			} else {
				redactPayloadJSON(child)
			}
		}
	case []interface{}:
		for _, child := range v {
			redactPayloadJSON(child)
		}
	}
}

func redactClientPayload(body []byte, contentType string) string {
	if len(body) == 0 {
		return ""
	}
	mediaType, _, _ := mime.ParseMediaType(contentType)
	if mediaType == "application/x-www-form-urlencoded" {
		if values, err := url.ParseQuery(string(body)); err == nil {
			redactPayloadValues(values)
			return values.Encode()
		}
		return "[omitted: invalid form]"
	}
	// Clients sometimes omit Content-Type; accept valid JSON regardless.
	if json.Valid(body) {
		var value interface{}
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.UseNumber()
		if decoder.Decode(&value) == nil {
			switch value.(type) {
			case map[string]interface{}, []interface{}:
				redactPayloadJSON(value)
				if safe, err := json.Marshal(value); err == nil {
					return string(safe)
				}
			}
		}
	}
	// Unknown or malformed bodies cannot be safely redacted.
	return "[omitted: unsupported or invalid body]"
}
