package handler

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"

	"github.com/askhat/qaitala/internal/higgsfield"
	"github.com/askhat/qaitala/internal/kaspi"
)

const maxWebhookBytes = 1 << 20

// KaspiWebhook — деньги. Подпись проверяется обязательно, но и подписанному
// уведомлению верим не до конца: статус и сумма перепроверяются у Kaspi.
func (h *Handler) KaspiWebhook(c echo.Context) error {
	ctx := c.Request().Context()

	body, err := io.ReadAll(io.LimitReader(c.Request().Body, maxWebhookBytes))
	if err != nil {
		return fail(c, http.StatusBadRequest, "тело не прочитано")
	}
	if !kaspi.VerifySignature(h.cfg.KaspiWebhookSecret, body, c.Request().Header.Get("X-Webhook-Signature")) {
		h.log.Warn("вебхук Kaspi с неверной подписью отклонён")
		return fail(c, http.StatusUnauthorized, "подпись не совпала")
	}

	var wh kaspi.Webhook
	if err := json.Unmarshal(body, &wh); err != nil {
		return fail(c, http.StatusBadRequest, "тело не разобрано")
	}
	if wh.Event != "payment.success" {
		h.log.Info("платёж не состоялся", "event", wh.Event, "payment_id", wh.PaymentID)
		return c.NoContent(http.StatusOK)
	}

	// Связь «платёж ↔ заказ» держится на QrOperationId: наш номер заказа
	// Kaspi не принимает, поэтому сопоставляем по нему.
	qrID, err := strconv.ParseInt(wh.PaymentID, 10, 64)
	if err != nil {
		h.log.Error("нечисловой paymentId в вебхуке Kaspi", "payment_id", wh.PaymentID)
		return c.NoContent(http.StatusOK)
	}
	order, err := h.orders.ByQROperation(ctx, qrID)
	if err != nil {
		h.log.Warn("вебхук Kaspi без заказа", "qr_operation_id", qrID)
		return c.NoContent(http.StatusOK)
	}

	// Сумма зашита в QR и сверена при создании, заказ найден по нашему
	// QrOperationId — подтверждения статуса достаточно.
	status, err := h.kaspi.QRStatus(ctx, qrID)
	if err != nil {
		// 5xx — Kaspi повторит доставку, так что заказ не потеряется.
		return h.oops(c, err, "вебхук Kaspi: перепроверка статуса")
	}
	if status != kaspi.StatusProcessed {
		h.log.Warn("платёж не подтверждён Kaspi", "qr_operation_id", qrID, "status", status)
		return c.NoContent(http.StatusOK)
	}

	// Повтор доставки не должен заводить вторую генерацию.
	ok, err := h.orders.MarkPaid(ctx, order.Token, wh.PaymentID)
	if err != nil {
		return h.oops(c, err, "вебхук Kaspi: отметка оплаты")
	}
	if ok {
		h.log.Info("заказ оплачен", "token", order.Token, "сумма", order.AmountKZT)
	}
	return c.NoContent(http.StatusOK)
}

// HiggsfieldWebhook. Эти уведомления не подписаны, поэтому тело здесь ни на что
// не влияет: из него берётся только request_id, а решение принимается по
// ответу status_url. Секрет в пути отсекает посторонний шум.
func (h *Handler) HiggsfieldWebhook(c echo.Context) error {
	if subtle.ConstantTimeCompare([]byte(c.Param("secret")), []byte(h.cfg.HFWebhookSecret)) != 1 {
		return fail(c, http.StatusNotFound, "не найдено")
	}

	body, err := io.ReadAll(io.LimitReader(c.Request().Body, maxWebhookBytes))
	if err != nil {
		return fail(c, http.StatusBadRequest, "тело не прочитано")
	}
	var wh higgsfield.Webhook
	if err := json.Unmarshal(body, &wh); err != nil || wh.RequestID == "" {
		return fail(c, http.StatusBadRequest, "тело не разобрано")
	}

	if err := h.gen.Check(c.Request().Context(), wh.RequestID); err != nil {
		// 5xx — Higgsfield будет повторять до двух часов; плюс есть реконсилятор.
		return h.oops(c, err, "вебхук Higgsfield")
	}
	return c.NoContent(http.StatusOK)
}
