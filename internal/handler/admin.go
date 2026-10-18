package handler

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"github.com/askhat/qaitala/internal/repository"
	"github.com/askhat/qaitala/internal/service"
)

// ponytail: один пароль на всех, логин не проверяется. Роли — когда админов станет больше одного.
func (h *Handler) registerAdmin(e *echo.Echo) {
	g := e.Group("/admin",
		middleware.BodyLimit("300M"),
		middleware.BasicAuth(func(_, pass string, _ echo.Context) (bool, error) {
			return subtle.ConstantTimeCompare([]byte(pass), []byte(h.cfg.AdminPassword)) == 1, nil
		}))
	g.GET("", h.AdminIndex)
	g.GET("/api/templates", h.AdminListTemplates)
	g.POST("/api/templates", h.AdminSaveTemplate)
	g.POST("/api/templates/:id/active", h.AdminSetActive)
}

type adminTemplate struct {
	service.TemplateInput
	ClipURL     string  `json:"clip_url"`
	PosterURL   string  `json:"poster_url"`
	DurationSec float64 `json:"duration_sec"`
}

func (h *Handler) AdminListTemplates(c echo.Context) error {
	list, err := h.tpls.ListAll(c.Request().Context())
	if err != nil {
		return h.oops(c, err, "админка: каталог")
	}
	out := make([]adminTemplate, 0, len(list))
	for _, t := range list {
		out = append(out, adminTemplate{
			TemplateInput: service.TemplateInput{
				ID: t.ID, Name: t.Name, DescRu: t.DescRu, DescKz: t.DescKz, Prompt: t.Prompt,
				SlotLabelsRu: t.SlotLabelsRu, SlotLabelsKz: t.SlotLabelsKz,
				Price480: t.Price480, Price720: t.Price720, Price1080: t.Price1080,
				Popular: t.Popular, Sort: t.Sort, Active: t.Active,
			},
			ClipURL:     h.store.PublicURL(t.ClipURL),
			PosterURL:   h.store.PublicURL(t.PosterURL),
			DurationSec: t.DurationSec,
		})
	}
	return c.JSON(http.StatusOK, out)
}

// AdminSaveTemplate принимает multipart: поле data с JSON шаблона и
// необязательный файл video. Без файла правятся только поля.
func (h *Handler) AdminSaveTemplate(c echo.Context) error {
	var in service.TemplateInput
	if err := json.Unmarshal([]byte(c.FormValue("data")), &in); err != nil {
		return fail(c, http.StatusBadRequest, "data: "+err.Error())
	}

	var video string
	if fh, err := c.FormFile("video"); err == nil {
		src, err := fh.Open()
		if err != nil {
			return h.oops(c, err, "админка: видео")
		}
		defer src.Close()
		tmp, err := os.CreateTemp("", "upload-*")
		if err != nil {
			return h.oops(c, err, "админка: видео")
		}
		defer os.Remove(tmp.Name())
		_, err = io.Copy(tmp, src)
		tmp.Close()
		if err != nil {
			return h.oops(c, err, "админка: видео")
		}
		video = tmp.Name()
	} else if !errors.Is(err, http.ErrMissingFile) {
		return fail(c, http.StatusBadRequest, "video: "+err.Error())
	}

	t, err := service.SaveTemplate(c.Request().Context(), h.tpls, h.store, in, video)
	if errors.Is(err, repository.ErrNotFound) {
		return fail(c, http.StatusBadRequest, "нового шаблона без видео не бывает")
	}
	if err != nil {
		// Ошибки здесь — проверки и ffmpeg; админу нужен текст, а не «внутренняя ошибка».
		h.log.Error("админка: сохранение", "id", in.ID, "err", err)
		return fail(c, http.StatusBadRequest, err.Error())
	}
	return c.JSON(http.StatusOK, map[string]string{"id": t.ID})
}

func (h *Handler) AdminSetActive(c echo.Context) error {
	var body struct {
		Active bool `json:"active"`
	}
	if err := c.Bind(&body); err != nil {
		return fail(c, http.StatusBadRequest, "неверный запрос")
	}
	err := h.tpls.SetActive(c.Request().Context(), c.Param("id"), body.Active)
	if errors.Is(err, repository.ErrNotFound) {
		return fail(c, http.StatusNotFound, "шаблон не найден")
	}
	if err != nil {
		return h.oops(c, err, "админка: active")
	}
	return c.NoContent(http.StatusNoContent)
}
