package middleware

import (
	"net/http"
	"strings"

	"github.com/alvindingpeng/switchyard/internal/server/auth"
	"github.com/alvindingpeng/switchyard/internal/server/resp"
	"github.com/gin-gonic/gin"
)

func Auth() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := c.GetHeader("Authorization")
		if token == "" {
			resp.Error(c, http.StatusBadRequest, resp.ErrBadRequest)
			c.Abort()
			return
		}
		if !auth.VerifyJWTToken(strings.TrimPrefix(token, "Bearer ")) {
			resp.Error(c, http.StatusUnauthorized, resp.ErrUnauthorized)
			c.Abort()
			return
		}
		c.Next()
	}
}

func APIKeyAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		var apiKey string
		var requestType string

		if key := c.Request.Header.Get("x-api-key"); key != "" {
			apiKey = key
			requestType = "anthropic"
		} else if auth := c.Request.Header.Get("Authorization"); auth != "" {
			apiKey = strings.TrimPrefix(auth, "Bearer ")
			requestType = "openai"
		}

		if apiKey == "" {
			resp.Error(c, http.StatusUnauthorized, resp.ErrUnauthorized)
			c.Abort()
			return
		}

		if !auth.VerifyAPIKey(apiKey, c.Request.Context()) {
			resp.Error(c, http.StatusUnauthorized, resp.ErrUnauthorized)
			c.Abort()
			return
		}

		c.Set("request_type", requestType)
		c.Next()
	}
}
