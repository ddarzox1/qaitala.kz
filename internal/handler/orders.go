package handler

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"

	"github.com/labstack/echo/v4"

	"github.com/askhat/qaitala/internal/media"
	"github.com/askhat/qaitala/internal/model"
	"github.com/askhat/qaitala/internal/repository"
)

const maxPhotoBytes = 15 << 20 // 15 МБ: снимок с телефона укладывается с запасом

type orderView struct {
	Token      string `json:"token"`
	TemplateID string `json:"template_id"`
	Resolution string `json:"resolution"`
	Status     string `json:"status"`
	AmountKZT  int    `json:"amount_kzt"`
	CanRetry   bool   `json:"can_retry"`
	CanRefund  bool   `json:"can_refund"`
	Error      string `json:"error,omitempty"`
}

func view(o *model.Order) orderView {
	return orderView{
		Token: o.Token, TemplateID: o.TemplateID, Resolution: o.Resolution,
		Status: o.Status, AmountKZT: o.AmountKZT,
		CanRetry:  o.Status == model.StatusFailed && o.RetriesUsed == 0,
		CanRefund: o.Status == model.StatusFailed,
		Error:     o.Error,
	}
}

// readPhotos валидирует и нормализует фото. Их ровно столько, сколько ролей у
// шаблона: лишние или недостающие сломали бы раздачу персонажей в промпте.
func (h *Handler) readPhotos(c echo.Context, want int) ([][]byte, error) {
	form, err := c.MultipartForm()
	if err != nil {
		return nil, fmt.Errorf("ожидается multipart-форма")
	}
	files := form.File["photos"]
	if len(files) != want {
		return nil, fmt.Errorf("нужно фото: %d, получено: %d", want, len(files))
	}

	out := make([][]byte, 0, want)
	for _, fh := range files {
		if fh.Size > maxPhotoBytes {
			return nil, fmt.Errorf("файл %s больше 15 МБ", fh.Filename)
		}
		f, err := fh.Open()
		if err != nil {
			return nil, fmt.Errorf("не удалось прочитать %s", fh.Filename)
		}
		raw, err := io.ReadAll(io.LimitReader(f, maxPhotoBytes+1))
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("не удалось прочитать %s", fh.Filename)
		}
		// Айфоны присылают HEIC, Higgsfield его не принимает — конвертируем здесь,
		// чтобы ошибка не всплыла уже после оплаты.
		jpg, _, err := media.NormalizeImage(c.Request().Context(), raw,
			fh.Header.Get("Content-Type"), fh.Filename)
		if err != nil {
			return nil, fmt.Errorf("%s: подойдёт JPG, PNG, HEIC или WebP", fh.Filename)
		}
		out = append(out, jpg)
	}
	return out, nil
}

func (h *Handler) storePhotos(c echo.Context, token string, photos [][]byte) ([]string, error) {
	keys := make([]string, 0, len(photos))
	for i, p := range photos {
		key := fmt.Sprintf("orders/%s/photo-%d.jpg", token, i)
		if err := h.store.Put(c.Request().Context(), key, "image/jpeg", bytesReader(p)); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, nil
}

func (h *Handler) CreateOrder(c echo.Context) error {
	ctx := c.Request().Context()

	tpl, err := h.tpls.Get(ctx, c.FormValue("template_id"))
	if errors.Is(err, repository.ErrNotFound) {
		return fail(c, http.StatusNotFound, "шаблон не найден")
	}
	if err != nil {
		return h.oops(c, err, "создание заказа: шаблон")
	}

	resolution := c.FormValue("resolution")
	if !slices.Contains(model.Resolutions, resolution) {
		return fail(c, http.StatusBadRequest, "неизвестное качество")
	}
	price := tpl.PriceFor(resolution)
	if price == nil {
		return fail(c, http.StatusBadRequest, "это качество не продаётся")
	}

	photos, err := h.readPhotos(c, tpl.RefPhotos)
	if err != nil {
		return fail(c, http.StatusBadRequest, err.Error())
	}

	lang := c.FormValue("lang")
	if lang != "kz" {
		lang = "ru"
	}

	order := &model.Order{
		Token: repository.NewToken(), TemplateID: tpl.ID, Resolution: resolution,
		Lang: lang, Status: model.StatusDraft, AmountKZT: *price,
	}
	keys, err := h.storePhotos(c, order.Token, photos)
	if err != nil {
		return h.oops(c, err, "сохранение фото")
	}
	order.PhotoKeys = keys

	if err := h.orders.Create(ctx, order); err != nil {
		return h.oops(c, err, "создание заказа")
	}
	return c.JSON(http.StatusOK, view(order))
}

func (h *Handler) GetOrder(c echo.Context) error {
	o, err := h.orders.ByToken(c.Request().Context(), c.Param("token"))
	if errors.Is(err, repository.ErrNotFound) {
		return fail(c, http.StatusNotFound, "заказ не найден")
	}
	if err != nil {
		return h.oops(c, err, "чтение заказа")
	}
	return c.JSON(http.StatusOK, view(o))
}

func (h *Handler) Retry(c echo.Context) error {
	ctx := c.Request().Context()
	o, err := h.orders.ByToken(ctx, c.Param("token"))
	if errors.Is(err, repository.ErrNotFound) {
		return fail(c, http.StatusNotFound, "заказ не найден")
	}
	if err != nil {
		return h.oops(c, err, "повтор: заказ")
	}
	if o.Status != model.StatusFailed || o.RetriesUsed != 0 {
		return fail(c, http.StatusConflict, "бесплатный повтор недоступен")
	}

	tpl, err := h.tpls.Get(ctx, o.TemplateID)
	if err != nil {
		return h.oops(c, err, "повтор: шаблон")
	}
	photos, err := h.readPhotos(c, tpl.RefPhotos)
	if err != nil {
		return fail(c, http.StatusBadRequest, err.Error())
	}
	keys, err := h.storePhotos(c, o.Token+"-retry", photos)
	if err != nil {
		return h.oops(c, err, "повтор: сохранение фото")
	}
	if err := h.orders.Requeue(ctx, o.Token, keys); err != nil {
		return fail(c, http.StatusConflict, "бесплатный повтор недоступен")
	}
	o.Status, o.PhotoKeys, o.RetriesUsed = model.StatusPaid, keys, 1
	return c.JSON(http.StatusOK, view(o))
}

// Refund только регистрирует заявку. Перевод денег делается руками — ровно то,
// что обещает интерфейс: «вернутся на карту в течение 1–3 дней».
func (h *Handler) Refund(c echo.Context) error {
	ctx := c.Request().Context()
	if err := h.orders.RequestRefund(ctx, c.Param("token")); err != nil {
		return fail(c, http.StatusConflict, "возврат по этому заказу недоступен")
	}
	h.log.Warn("ЗАПРОШЕН ВОЗВРАТ", "token", c.Param("token"))
	o, err := h.orders.ByToken(ctx, c.Param("token"))
	if err != nil {
		return h.oops(c, err, "возврат: заказ")
	}
	return c.JSON(http.StatusOK, view(o))
}

func (h *Handler) Video(c echo.Context) error {
	ctx := c.Request().Context()
	o, err := h.orders.ByToken(ctx, c.Param("token"))
	if errors.Is(err, repository.ErrNotFound) {
		return fail(c, http.StatusNotFound, "заказ не найден")
	}
	if err != nil {
		return h.oops(c, err, "видео: заказ")
	}
	if o.Status != model.StatusDone || o.VideoKey == "" {
		return fail(c, http.StatusConflict, "видео ещё не готово")
	}
	// В режиме заглушки результат — сам исходный ролик, он лежит в public/.
	if h.cfg.FakeVideo {
		return c.Redirect(http.StatusFound, h.store.PublicURL(o.VideoKey))
	}
	url, err := h.gen.VideoURL(ctx, o.VideoKey)
	if err != nil {
		return h.oops(c, err, "видео: ссылка")
	}
	return c.Redirect(http.StatusFound, url)
}
