package handler

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
)

// Index отдаёт одностраничный фронтенд. Он же обслуживает /o/{token}:
// страница сама достаёт токен из адреса и спрашивает статус у API.
func (h *Handler) Index(c echo.Context) error {
	if strings.HasPrefix(c.Request().URL.Path, "/o/") {
		// Ссылка содержит токен доступа к заказу и не должна попадать в поиск.
		c.Response().Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	}
	return c.Blob(http.StatusOK, "text/html; charset=utf-8", IndexHTML)
}

func (h *Handler) OGImage(c echo.Context) error {
	c.Response().Header().Set("Cache-Control", "public, max-age=86400")
	return c.Blob(http.StatusOK, "image/png", SocialImage)
}

func (h *Handler) Robots(c echo.Context) error {
	baseURL := strings.TrimRight(h.cfg.PublicBaseURL, "/")
	body := fmt.Sprintf("User-agent: *\nAllow: /\nDisallow: /admin/\nDisallow: /api/\nDisallow: /fake-pay/\nDisallow: /o/\nDisallow: /webhooks/\n\nSitemap: %s/sitemap.xml\n", baseURL)
	return c.String(http.StatusOK, body)
}

func (h *Handler) Sitemap(c echo.Context) error {
	baseURL := strings.TrimRight(h.cfg.PublicBaseURL, "/")
	body := fmt.Sprintf("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<urlset xmlns=\"http://www.sitemaps.org/schemas/sitemap/0.9\">\n  <url>\n    <loc>%s/</loc>\n    <changefreq>daily</changefreq>\n    <priority>1.0</priority>\n  </url>\n</urlset>\n", baseURL)
	return c.XMLBlob(http.StatusOK, []byte(body))
}

func (h *Handler) AdminIndex(c echo.Context) error {
	return c.Blob(http.StatusOK, "text/html; charset=utf-8", AdminHTML)
}

// AdminHTML задаётся при старте, как и IndexHTML.
var AdminHTML []byte

// IndexHTML задаётся при старте — файл вшит в бинарник в cmd/server.
var IndexHTML []byte

// SocialImage — брендовая картинка для Open Graph и Twitter Cards.
var SocialImage []byte
