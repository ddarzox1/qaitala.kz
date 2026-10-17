package handler

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/askhat/qaitala/internal/model"
)

// ListTemplates отдаёт каталог. Цены приходят отсюда же — клиент их только
// показывает, считать сумму ему не доверяем.
func (h *Handler) ListTemplates(c echo.Context) error {
	lang := c.QueryParam("lang")
	list, err := h.tpls.ListActive(c.Request().Context())
	if err != nil {
		return h.oops(c, err, "каталог")
	}
	for _, t := range list {
		t.Localize(lang)
		t.ClipURL = h.store.PublicURL(t.ClipURL)
		t.PosterURL = h.store.PublicURL(t.PosterURL)
	}
	if list == nil {
		list = []*model.Template{} // пустой каталог — это [], а не null: фронт его перебирает
	}
	return c.JSON(http.StatusOK, list)
}
