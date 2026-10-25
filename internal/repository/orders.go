package repository

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/askhat/qaitala/internal/model"
)

type Orders struct{ db *pgxpool.Pool }

func NewOrders(db *pgxpool.Pool) *Orders { return &Orders{db: db} }

// NewToken — он же секрет ссылки /o/{token}. 16 байт энтропии: заказ находится
// только по ней, перебор должен быть безнадёжен.
func NewToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand не падает; если упал — продолжать нельзя
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

const orderCols = `token, template_id, resolution, lang, coalesce(email,''), status,
	photo_keys, amount_kzt, qr_operation_id, coalesce(kaspi_payment_id,''),
	coalesce(hf_request_id,''), hf_segment, coalesce(video_key,''), retries_used, attempts,
	coalesce(error,''), created_at, paid_at, done_at, coalesce(pay_url,''), qr_expires_at`

func scanOrder(row pgx.Row) (*model.Order, error) {
	var o model.Order
	err := row.Scan(&o.Token, &o.TemplateID, &o.Resolution, &o.Lang, &o.Email, &o.Status,
		&o.PhotoKeys, &o.AmountKZT, &o.QROperationID, &o.KaspiPaymentID, &o.HFRequestID,
		&o.HFSegment, &o.VideoKey, &o.RetriesUsed, &o.Attempts, &o.Error, &o.CreatedAt, &o.PaidAt, &o.DoneAt,
		&o.PayURL, &o.QRExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("orders scan: %w", err)
	}
	return &o, nil
}

func (r *Orders) Create(ctx context.Context, o *model.Order) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO orders (token, template_id, resolution, lang, status, photo_keys, amount_kzt)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		o.Token, o.TemplateID, o.Resolution, o.Lang, o.Status, o.PhotoKeys, o.AmountKZT)
	if err != nil {
		return fmt.Errorf("orders create: %w", err)
	}
	return nil
}

func (r *Orders) ByToken(ctx context.Context, token string) (*model.Order, error) {
	return scanOrder(r.db.QueryRow(ctx, `SELECT `+orderCols+` FROM orders WHERE token = $1`, token))
}

func (r *Orders) ByHFRequest(ctx context.Context, reqID string) (*model.Order, error) {
	return scanOrder(r.db.QueryRow(ctx, `SELECT `+orderCols+` FROM orders WHERE hf_request_id = $1`, reqID))
}

func (r *Orders) ByQROperation(ctx context.Context, qrID int64) (*model.Order, error) {
	return scanOrder(r.db.QueryRow(ctx, `SELECT `+orderCols+` FROM orders WHERE qr_operation_id = $1`, qrID))
}

// AwaitPayment привязывает заказ к QR-операции Kaspi. qr_operation_id под UNIQUE:
// создать два заказа на один платёж нельзя. Пустой email не стирает прежний.
func (r *Orders) AwaitPayment(ctx context.Context, token string, qrID int64,
	payURL string, expiresAt time.Time, email string) error {
	ct, err := r.db.Exec(ctx, `
		UPDATE orders SET status = $1, qr_operation_id = $2, email = coalesce(nullif($3,''), email),
			pay_url = $6, qr_expires_at = $7
		WHERE token = $4 AND status IN ($5, $1)`,
		model.StatusAwaitPay, qrID, email, token, model.StatusDraft, payURL, expiresAt)
	if err != nil {
		return fmt.Errorf("orders await payment: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkPaid идемпотентен: Kaspi шлёт вебхук до трёх раз, и повтор не должен
// заводить вторую генерацию.
func (r *Orders) MarkPaid(ctx context.Context, token, paymentID string) (bool, error) {
	ct, err := r.db.Exec(ctx, `
		UPDATE orders SET status = $1, kaspi_payment_id = $2, paid_at = now()
		WHERE token = $3 AND status = $4`,
		model.StatusPaid, paymentID, token, model.StatusAwaitPay)
	if err != nil {
		return false, fmt.Errorf("orders mark paid: %w", err)
	}
	return ct.RowsAffected() == 1, nil
}

// ClaimJob забирает один оплаченный заказ. SKIP LOCKED — несколько воркеров
// разбирают очередь, не мешая друг другу и не блокируясь.
func (r *Orders) ClaimJob(ctx context.Context) (*model.Order, error) {
	return scanOrder(r.db.QueryRow(ctx, `
		UPDATE orders SET status = $1, attempts = attempts + 1, locked_at = now()
		WHERE token = (
			SELECT token FROM orders WHERE status = $2
			ORDER BY paid_at FOR UPDATE SKIP LOCKED LIMIT 1
		)
		RETURNING `+orderCols, model.StatusGenerating, model.StatusPaid))
}

func (r *Orders) SetHFRequest(ctx context.Context, token, reqID string, segment int) error {
	_, err := r.db.Exec(ctx, `UPDATE orders SET hf_request_id = $1, hf_segment = $2 WHERE token = $3`,
		reqID, segment, token)
	if err != nil {
		return fmt.Errorf("orders set hf request: %w", err)
	}
	return nil
}

func (r *Orders) MarkDone(ctx context.Context, token, videoKey string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE orders SET status = $1, video_key = $2, done_at = now(), error = NULL
		WHERE token = $3`, model.StatusDone, videoKey, token)
	if err != nil {
		return fmt.Errorf("orders mark done: %w", err)
	}
	return nil
}

func (r *Orders) MarkFailed(ctx context.Context, token, reason string) error {
	_, err := r.db.Exec(ctx, `UPDATE orders SET status = $1, error = $2 WHERE token = $3`,
		model.StatusFailed, reason, token)
	if err != nil {
		return fmt.Errorf("orders mark failed: %w", err)
	}
	return nil
}

// Requeue — бесплатный повтор с новыми фото. Деньги уже получены, поэтому
// заказ возвращается сразу в очередь, минуя оплату.
func (r *Orders) Requeue(ctx context.Context, token string, photoKeys []string) error {
	ct, err := r.db.Exec(ctx, `
		UPDATE orders SET status = $1, photo_keys = $2, retries_used = retries_used + 1,
			hf_request_id = NULL, hf_segment = 0, error = NULL
		WHERE token = $3 AND status = $4 AND retries_used = 0`,
		model.StatusPaid, photoKeys, token, model.StatusFailed)
	if err != nil {
		return fmt.Errorf("orders requeue: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Orders) RequestRefund(ctx context.Context, token string) error {
	ct, err := r.db.Exec(ctx, `UPDATE orders SET status = $1 WHERE token = $2 AND status = $3`,
		model.StatusRefundReq, token, model.StatusFailed)
	if err != nil {
		return fmt.Errorf("orders request refund: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Refundable — заказы, по которым админ может вернуть деньги.
func (r *Orders) Refundable(ctx context.Context) ([]*model.Order, error) {
	rows, err := r.db.Query(ctx, `SELECT `+orderCols+` FROM orders
		WHERE status IN ($1, $2) AND qr_operation_id IS NOT NULL
		ORDER BY (status = $2) DESC, created_at DESC
		LIMIT 100`, model.StatusFailed, model.StatusRefundReq)
	if err != nil {
		return nil, fmt.Errorf("orders refundable: %w", err)
	}
	defer rows.Close()
	var out []*model.Order
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// ClaimRefund помечает заказ возвращённым ДО запроса в Kaspi: повторный клик
// уже не найдёт заказ, и деньги не уйдут дважды. Отдаёт прежний статус для отката.
func (r *Orders) ClaimRefund(ctx context.Context, token string) (prevStatus string, qrID int64, amountKZT int, err error) {
	err = r.db.QueryRow(ctx, `
		UPDATE orders o SET status = $1, refunded_at = now()
		FROM (SELECT token, status FROM orders WHERE token = $2 FOR UPDATE) prev
		WHERE o.token = prev.token AND prev.status IN ($3, $4) AND o.qr_operation_id IS NOT NULL
		RETURNING prev.status, o.qr_operation_id, o.amount_kzt`,
		model.StatusRefunded, token, model.StatusFailed, model.StatusRefundReq).
		Scan(&prevStatus, &qrID, &amountKZT)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", 0, 0, ErrNotFound
	}
	if err != nil {
		return "", 0, 0, fmt.Errorf("orders claim refund: %w", err)
	}
	return prevStatus, qrID, amountKZT, nil
}

// UnclaimRefund откатывает ClaimRefund, если Kaspi возврат не провёл.
func (r *Orders) UnclaimRefund(ctx context.Context, token, prevStatus string) error {
	_, err := r.db.Exec(ctx, `UPDATE orders SET status = $1, refunded_at = NULL
		WHERE token = $2 AND status = $3`, prevStatus, token, model.StatusRefunded)
	if err != nil {
		return fmt.Errorf("orders unclaim refund: %w", err)
	}
	return nil
}

// Stalled — заказы в генерации дольше older. Вебхук Higgsfield могли не
// доставить, поэтому их статус перепроверяется опросом.
func (r *Orders) Stalled(ctx context.Context, older time.Duration) ([]*model.Order, error) {
	rows, err := r.db.Query(ctx, `
		SELECT `+orderCols+` FROM orders
		WHERE status = $1 AND hf_request_id IS NOT NULL
		  AND locked_at < now() - make_interval(secs => $2)
		LIMIT 20`, model.StatusGenerating, older.Seconds())
	if err != nil {
		return nil, fmt.Errorf("orders stalled: %w", err)
	}
	defer rows.Close()

	var out []*model.Order
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// Unstick возвращает в очередь заказы, взятые воркером, но так и не
// отправленные в Higgsfield: процесс упал между ClaimJob и SetHFRequest.
// Повторная отправка безопасна — Idempotency-Key равен токену заказа.
// После трёх попыток заказ считается проваленным, чтобы не крутиться вечно.
func (r *Orders) Unstick(ctx context.Context, older time.Duration, reason string) (int64, error) {
	ct, err := r.db.Exec(ctx, `
		UPDATE orders SET
			status = CASE WHEN attempts >= 3 THEN $1 ELSE $2 END,
			error  = CASE WHEN attempts >= 3 THEN $3 ELSE NULL END
		WHERE status = $4 AND hf_request_id IS NULL
		  AND locked_at < now() - make_interval(secs => $5)`,
		model.StatusFailed, model.StatusPaid, reason, model.StatusGenerating, older.Seconds())
	if err != nil {
		return 0, fmt.Errorf("orders unstick: %w", err)
	}
	return ct.RowsAffected(), nil
}

// ExpireStale закрывает брошенные заказы. Неоплаченный QR к этому моменту
// давно истёк, так что поздняя оплата по нему невозможна.
func (r *Orders) ExpireStale(ctx context.Context, older time.Duration) (int64, error) {
	ct, err := r.db.Exec(ctx, `
		UPDATE orders SET status = $1
		WHERE (status = $2 AND created_at < now() - make_interval(secs => $4))
		   OR (status = $3 AND coalesce(qr_expires_at, created_at) < now() - make_interval(secs => $4))`,
		model.StatusExpired, model.StatusDraft, model.StatusAwaitPay, older.Seconds())
	if err != nil {
		return 0, fmt.Errorf("orders expire: %w", err)
	}
	return ct.RowsAffected(), nil
}
