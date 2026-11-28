package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"
)

// PrivateNoStore prevents storage of account/session responses, including errors.
// Register it before authentication so denied requests receive the same policy.
func PrivateNoStore() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "private, no-store")
		header := c.Writer.Header()
		for _, field := range []string{"Cookie", "Authorization"} {
			present := false
			for _, value := range header.Values("Vary") {
				for _, token := range strings.Split(value, ",") {
					if strings.EqualFold(strings.TrimSpace(token), field) {
						present = true
					}
				}
			}
			if !present {
				header.Add("Vary", field)
			}
		}
		c.Next()
	}
}
