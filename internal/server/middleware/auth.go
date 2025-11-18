package middleware

import (
	"net/http"
	"strings"

	"github.com/alvindingpeng/go-backend-template/internal/server/auth"
	"github.com/alvindingpeng/go-backend-template/internal/server/resp"
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
		if !auth.VerifyToken(strings.TrimPrefix(token, "Bearer ")) {
			resp.Error(c, http.StatusUnauthorized, resp.ErrUnauthorized)
			c.Abort()
			return
		}
		c.Next()
	}
}
