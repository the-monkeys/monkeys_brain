package events

import (
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/the-monkeys/the_monkeys/config"
	"go.uber.org/zap"
)

// TestRegisterEventRouter guards against wildcard collisions. Gin panics while
// registering conflicting routes, so a plain registration is the whole test:
// static segments such as /attending and /user/:username sit alongside the
// /:slug parameter and are easy to break.
func TestRegisterEventRouter(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	RegisterEventRouter(router, &config.Config{}, nil, nil, zap.NewNop().Sugar())

	want := map[string]string{
		"GET /api/v1/events":                            "",
		"GET /api/v1/events/attending":                  "",
		"GET /api/v1/events/user/:username":             "",
		"GET /api/v1/events/:slug":                      "",
		"PUT /api/v1/events/:slug":                      "",
		"DELETE /api/v1/events/:slug":                   "",
		"POST /api/v1/events":                           "",
		"POST /api/v1/events/:slug/publish":             "",
		"POST /api/v1/events/:slug/cancel":              "",
		"GET /api/v1/events/:slug/calendar":             "",
		"GET /api/v1/events/:slug/comments":             "",
		"GET /api/v1/events/:slug/share":                "",
		"GET /api/v1/events/:slug/attendees":            "",
		"GET /api/v1/events/:slug/attendees/export":     "",
		"POST /api/v1/events/payment/webhook":           "",
		"POST /api/v1/events/:slug/tiers":               "",
		"PUT /api/v1/events/:slug/tiers/:id":            "",
		"DELETE /api/v1/events/:slug/tiers/:id":         "",
		"POST /api/v1/events/:slug/coupons":             "",
		"GET /api/v1/events/:slug/coupons":              "",
		"DELETE /api/v1/events/:slug/coupons/:id":       "",
		"POST /api/v1/events/:slug/coupons/validate":    "",
		"POST /api/v1/events/:slug/rsvp":                "",
		"DELETE /api/v1/events/:slug/rsvp":              "",
		"POST /api/v1/events/:slug/comments":            "",
		"DELETE /api/v1/events/:slug/comments/:id":      "",
		"POST /api/v1/events/:slug/react":               "",
		"DELETE /api/v1/events/:slug/react":             "",
		"POST /api/v1/events/:slug/report":              "",
		"POST /api/v1/events/:slug/cohosts":             "",
		"DELETE /api/v1/events/:slug/cohosts/:username": "",
	}
	for _, route := range router.Routes() {
		delete(want, route.Method+" "+route.Path)
	}
	for path := range want {
		t.Errorf("route not registered: %s", path)
	}
}
