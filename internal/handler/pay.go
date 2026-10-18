package handler

import (
	"errors"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/askhat/qaitala/internal/kaspi"
	"github.com/askhat/qaitala/internal/model"
	"github.com/askhat/qaitala/internal/repository"
)

type payRequest struct {
	Email string `json:"email"`
}

type payResponse struct {
	PayURL    string `json:"pay_url"`
	AmountKZT int    `json:"amount_kzt"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

// Pay создаёт QR на сумму из базы. Присланная клиентом сумма игнорируется:
// цену назначает сервер, иначе её можно было бы занизить из браузера.
func (h *Handler) Pay(c echo.Context) error {
	ctx := c.Request().Context()

	var req payRequest
	_ = c.Bind(&req)

	o, err := h.orders.ByToken(ctx, c.Param("token"))
	if errors.Is(err, repository.ErrNotFound) {
		return fail(c, http.StatusNotFound, "заказ не найден")
	}
	if err != nil {
		return h.oops(c, err, "оплата: заказ")
	}
	if o.Status != model.StatusDraft && o.Status != model.StatusAwaitPay {
		return fail(c, http.StatusConflict, "заказ уже оплачен")
	}

	var qr *kaspi.QR
	switch {
	case o.Status == model.StatusAwaitPay && o.PayURL != "" && o.QROperationID != nil &&
		o.QRExpiresAt != nil && time.Until(*o.QRExpiresAt) > qrReuseMargin:
		// Прежний QR ещё можно оплатить. Новый перезаписал бы qr_operation_id,
		// и оплата по старому не нашла бы заказ.
		qr = &kaspi.QR{OperationID: *o.QROperationID, PayURL: o.PayURL, ExpiresAt: *o.QRExpiresAt}
	case h.cfg.FakePayments:
		qr = &kaspi.QR{
			OperationID: fakeQROperationID(o.Token),
			PayURL:      h.cfg.PublicBaseURL + "/fake-pay/" + o.Token,
			ExpiresAt:   time.Now().Add(5 * time.Minute),
		}
	default:
		qr, err = h.kaspi.CreateQR(ctx, o.AmountKZT)
		if errors.Is(err, kaspi.ErrUnauthorized) {
			// Сессия Kaspi протухла. Молча ронять оплату нельзя — её продлевают руками.
			h.log.Error("СЕССИЯ KASPI НЕДЕЙСТВИТЕЛЬНА, оплата недоступна", "token", o.Token)
			return fail(c, http.StatusServiceUnavailable, "оплата временно недоступна, попробуй позже")
		}
		if err != nil {
			return h.oops(c, err, "оплата: создание QR")
		}
	}

	if err := h.orders.AwaitPayment(ctx, o.Token, qr.OperationID, qr.PayURL, qr.ExpiresAt, req.Email); err != nil {
		return h.oops(c, err, "оплата: привязка QR")
	}

	return c.JSON(http.StatusOK, payResponse{
		PayURL:    qr.PayURL,
		AmountKZT: o.AmountKZT,
		ExpiresAt: qr.ExpiresAt.Format(time.RFC3339),
	})
}

// qrReuseMargin — QR, которому осталось жить меньше, не отдаём: клиент не успеет.
const qrReuseMargin = 15 * time.Second

// FakePay заменяет приложение Kaspi при локальном прогоне.
func (h *Handler) FakePay(c echo.Context) error {
	ctx := c.Request().Context()
	token := c.Param("token")
	if _, err := h.orders.MarkPaid(ctx, token, "fake"); err != nil {
		return h.oops(c, err, "заглушка оплаты")
	}
	return c.Redirect(http.StatusFound, "/o/"+token)
}

// fakeQROperationID — стабильный псевдо-id, чтобы сработало ограничение UNIQUE.
func fakeQROperationID(token string) int64 {
	var h int64 = 1469598103934665603
	for _, b := range []byte(token) {
		h = (h ^ int64(b)) * 1099511628211
	}
	if h < 0 {
		h = -h
	}
	return h
}
