package handler

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/askhat/qaitala/internal/config"
)

func newStaticTestServer(t *testing.T) *echo.Echo {
	t.Helper()

	IndexHTML = []byte("<!doctype html><title>qaitala.kz</title>")
	SocialImage = []byte("png")

	e := echo.New()
	h := New(config.Config{PublicBaseURL: "https://qaitala.kz/"}, nil, nil, nil, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.Register(e)
	return e
}

func TestStaticSEORoutes(t *testing.T) {
	e := newStaticTestServer(t)

	tests := []struct {
		path        string
		contentType string
		contains    string
	}{
		{path: "/robots.txt", contentType: "text/plain", contains: "Sitemap: https://qaitala.kz/sitemap.xml"},
		{path: "/sitemap.xml", contentType: "application/xml", contains: "<loc>https://qaitala.kz/</loc>"},
		{path: "/og-image.png", contentType: "image/png", contains: "png"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
			}
			if got := rec.Header().Get(echo.HeaderContentType); !strings.HasPrefix(got, tt.contentType) {
				t.Fatalf("Content-Type = %q, want prefix %q", got, tt.contentType)
			}
			if !strings.Contains(rec.Body.String(), tt.contains) {
				t.Fatalf("body %q does not contain %q", rec.Body.String(), tt.contains)
			}
		})
	}
}

func TestOrderPageIsNotIndexed(t *testing.T) {
	e := newStaticTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/o/private-token", nil)
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Robots-Tag"); got != "noindex, nofollow, noarchive" {
		t.Fatalf("X-Robots-Tag = %q", got)
	}
}
