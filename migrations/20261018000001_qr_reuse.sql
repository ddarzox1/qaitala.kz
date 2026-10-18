-- +goose Up
-- Живой QR отдаётся повторно: новый QR перезаписал бы qr_operation_id,
-- и оплата по старому не нашла бы заказ.
ALTER TABLE orders
    ADD COLUMN pay_url       text,
    ADD COLUMN qr_expires_at timestamptz;

-- +goose Down
ALTER TABLE orders
    DROP COLUMN pay_url,
    DROP COLUMN qr_expires_at;
