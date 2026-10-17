// Package handler — HTTP-слой.
package handler

import (
	"log/slog"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/askhat/qaitala/internal/blob"
	"github.com/askhat/qaitala/internal/config"
	"github.com/askhat/qaitala/internal/kaspi"
	"github.com/askhat/qaitala/internal/repository"
	"github.com/askhat/qaitala/internal/service"
)

type Handler struct {
	cfg    config.Config
	orders *repository.Orders
	tpls   *repository.Templates
	kaspi  *kaspi.Client
	gen    *service.Generator
	store  *blob.Store
	log    *slog.Logger
}

func New(cfg config.Config, o *repository.Orders, t *repository.Templates,
	k *kaspi.Client, g *service.Generator, s *blob.Store, log *slog.Logger) *Handler {
	return &Handler{cfg: cfg, orders: o, tpls: t, kaspi: k, gen: g, store: s, log: log}
}

func (h *Handler) Register(e *echo.Echo) {
	e.GET("/api/templates", h.ListTemplates)

	e.POST("/api/orders", h.CreateOrder)
	e.GET("/api/orders/:token", h.GetOrder)
	e.POST("/api/orders/:token/pay", h.Pay)
	e.POST("/api/orders/:token/retry", h.Retry)
	e.POST("/api/orders/:token/refund", h.Refund)
	e.GET("/api/orders/:token/video", h.Video)

	e.POST("/webhooks/kaspi", h.KaspiWebhook)
	e.POST("/webhooks/higgsfield/:secret", h.HiggsfieldWebhook)

	if h.cfg.FakePayments {
		// Локальная заглушка вместо приложения Kaspi: открыл ссылку — «оплатил».
		e.GET("/fake-pay/:token", h.FakePay)
	}

	e.GET("/", h.Index)
	e.GET("/o/:token", h.Index)
}

func fail(c echo.Context, code int, msg string) error {
	return c.JSON(code, map[string]string{"error": msg})
}

func (h *Handler) oops(c echo.Context, err error, what string) error {
	h.log.Error(what, "err", err, "path", c.Path())
	return fail(c, http.StatusInternalServerError, "внутренняя ошибка")
}
