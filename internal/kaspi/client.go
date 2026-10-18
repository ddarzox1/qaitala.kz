// Package kaspi — клиент к самостоятельно размещённому kaspi-pos-automation.
// Сервис не торчит в интернет: ходим к нему только по внутренней сети Dokploy.
package kaspi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ErrUnauthorized — сессия Kaspi протухла. Её продлевают руками, пройдя
// SMS-авторизацию заново, поэтому ошибку нельзя глотать: оплата недоступна.
var ErrUnauthorized = errors.New("kaspi: сессия недействительна, нужна повторная SMS-авторизация")

// StatusProcessed — единственный статус QR-операции, означающий, что деньги пришли.
const StatusProcessed = "Processed"

// almaty — часовой пояс Kaspi: ExpireDate приходит без смещения.
var almaty = time.FixedZone("ALMT", 5*3600)

// parseExpire разбирает ExpireDate. Не разобралось — считаем, что QR живёт
// пять минут: лучше выдать новый раньше, чем держать мёртвый.
func parseExpire(s string, now time.Time) time.Time {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	if t, err := time.ParseInLocation("2006-01-02T15:04:05", s, almaty); err == nil {
		return t
	}
	return now.Add(5 * time.Minute) // ponytail: предположение о сроке жизни QR
}

type Client struct {
	base      string
	tokenSN   string
	vtoken    string
	profileID string
	http      *http.Client
}

func New(base, tokenSN, vtoken, profileID string) *Client {
	return &Client{
		base:      strings.TrimRight(base, "/"),
		tokenSN:   tokenSN,
		vtoken:    vtoken,
		profileID: profileID,
		http:      &http.Client{Timeout: 20 * time.Second},
	}
}

type QR struct {
	OperationID int64
	PayURL      string // QrToken — это ссылка на оплату, не картинка
	ReceiptURL  string
	ExpiresAt   time.Time
	Amount      int
}

type qrEnvelope struct {
	StatusCode int `json:"StatusCode"`
	Data       struct {
		QrOperationID int64  `json:"QrOperationId"`
		QrToken       string `json:"QrToken"`
		ExpireDate    string `json:"ExpireDate"`
		Amount        int    `json:"Amount"`
		ReceiptURL    string `json:"ReceiptUrl"`
		Status        string `json:"Status"`
	} `json:"Data"`
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var rdr *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("kaspi marshal: %w", err)
		}
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rdr)
	if err != nil {
		return fmt.Errorf("kaspi request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Token-SN", c.tokenSN)
	req.Header.Set("X-Vtoken-Secret", c.vtoken)
	if c.profileID != "" {
		req.Header.Set("X-Profile-Id", c.profileID)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("kaspi %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return ErrUnauthorized
	}
	if resp.StatusCode >= 300 {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(resp.Body)
		return fmt.Errorf("kaspi %s: %s: %s", path, resp.Status, strings.TrimSpace(buf.String()))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("kaspi decode %s: %w", path, err)
	}
	return nil
}

// CreateQR создаёт QR на сумму в тенге. Наш номер заказа сюда передать нельзя —
// связь «платёж ↔ заказ» держится на QrOperationId в нашей базе.
func (c *Client) CreateQR(ctx context.Context, amountKZT int) (*QR, error) {
	var env qrEnvelope
	if err := c.do(ctx, http.MethodPost, "/api/qr/create",
		map[string]any{"amount": amountKZT}, &env); err != nil {
		return nil, err
	}
	if env.Data.QrOperationID == 0 || env.Data.QrToken == "" {
		return nil, fmt.Errorf("kaspi qr/create: пустой ответ, StatusCode=%d", env.StatusCode)
	}
	qr := &QR{
		OperationID: env.Data.QrOperationID,
		PayURL:      env.Data.QrToken,
		ReceiptURL:  env.Data.ReceiptURL,
		Amount:      env.Data.Amount,
		ExpiresAt:   parseExpire(env.Data.ExpireDate, time.Now()),
	}
	return qr, nil
}

// QRStatus — независимая проверка платежа. Вызывается после вебхука: верить
// одному лишь уведомлению на денежном пути нельзя.
func (c *Client) QRStatus(ctx context.Context, operationID int64) (status string, amount int, err error) {
	var env qrEnvelope
	err = c.do(ctx, http.MethodGet,
		"/api/qr/status?qrOperationId="+strconv.FormatInt(operationID, 10), nil, &env)
	if err != nil {
		return "", 0, err
	}
	return env.Data.Status, env.Data.Amount, nil
}

// Webhook — тело уведомления от kaspi-pos-automation.
type Webhook struct {
	Event      string `json:"event"`
	PaymentID  string `json:"paymentId"`
	Type       string `json:"type"`
	Status     string `json:"status"`
	StatusDesc string `json:"statusDesc"`
	Amount     int    `json:"amount"`
	QrToken    string `json:"qrToken"`
	ReceiptURL string `json:"receiptUrl"`
	Timestamp  string `json:"timestamp"`
}

// VerifySignature проверяет заголовок X-Webhook-Signature вида "sha256=<hex>".
// Сравнение постоянного времени: иначе по задержке подбирается валидная подпись.
func VerifySignature(secret string, body []byte, header string) bool {
	if secret == "" || header == "" {
		return false
	}
	got, ok := strings.CutPrefix(header, "sha256=")
	if !ok {
		return false
	}
	sum, err := hex.DecodeString(strings.TrimSpace(got))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return subtle.ConstantTimeCompare(sum, mac.Sum(nil)) == 1
}
