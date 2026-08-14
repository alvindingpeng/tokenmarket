package middleware

import (
	"io/fs"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// StaticEmbed 返回用于嵌入式静态文件系统的 HTTP 中间件。
func StaticEmbed(urlPrefix string, embedFS fs.FS) gin.HandlerFunc {
	fs := http.FS(embedFS)
	return static(urlPrefix, fs)
}

// StaticLocal 返回用于本地静态文件目录的 HTTP 中间件。
func StaticLocal(urlPrefix string, localPath string) gin.HandlerFunc {
	fs := http.Dir(localPath)
	return static(urlPrefix, fs)
}

// static 根据文件类型设置缓存策略并响应已存在的静态文件。
func static(urlPrefix string, fileSystem http.FileSystem) gin.HandlerFunc {
	fileserver := http.FileServer(fileSystem)
	if urlPrefix != "" {
		fileserver = http.StripPrefix(urlPrefix, fileserver)
	}
	return func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api") {
			c.Next()
			return
		}
		if _, err := fileSystem.Open(c.Request.URL.Path); err == nil {
			if strings.HasPrefix(c.Request.URL.Path, "/assets/") {
				c.Header("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				c.Header("Cache-Control", "no-cache")
			}
			fileserver.ServeHTTP(c.Writer, c.Request)
			c.Abort()
		}
	}
}
