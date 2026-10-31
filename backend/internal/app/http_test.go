package app

import (
	"net/http/httptest"
	"testing"

	"github.com/gotsta1/tomas_blank_bot/backend/internal/config"
)

func TestOriginAcceptsConfiguredAndRequestHost(t *testing.T) {
	t.Parallel()
	a := &App{Config: config.Config{BaseURL: "https://club.example"}}
	tests := []struct {
		name   string
		host   string
		origin string
		want   bool
	}{
		{name: "configured public URL", host: "internal", origin: "https://club.example", want: true},
		{name: "local same origin", host: "127.0.0.1:8097", origin: "http://127.0.0.1:8097", want: true},
		{name: "foreign site", host: "127.0.0.1:8097", origin: "https://evil.example", want: false},
		{name: "deceptive user info", host: "club.example", origin: "https://club.example@evil.example", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "http://"+tc.host+"/api/login", nil)
			r.Host = tc.host
			r.Header.Set("Origin", tc.origin)
			if got := a.originOK(r); got != tc.want {
				t.Fatalf("originOK()=%v want=%v", got, tc.want)
			}
		})
	}
}
