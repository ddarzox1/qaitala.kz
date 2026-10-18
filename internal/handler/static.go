package handler

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// Index отдаёт одностраничный фронтенд. Он же обслуживает /o/{token}:
// страница сама достаёт токен из адреса и спрашивает статус у API.
func (h *Handler) Index(c echo.Context) error {
	return c.Blob(http.StatusOK, "text/html; charset=utf-8", IndexHTML)
}

func (h *Handler) AdminIndex(c echo.Context) error {
	return c.Blob(http.StatusOK, "text/html; charset=utf-8", AdminHTML)
}

// AdminHTML задаётся при старте, как и IndexHTML.
var AdminHTML []byte

// IndexHTML задаётся при старте — файл вшит в бинарник в cmd/server.
var IndexHTML []byte
