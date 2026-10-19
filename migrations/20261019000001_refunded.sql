-- +goose Up
-- Возврат делается из админки: заказ, по которому деньги ушли обратно, — refunded.
ALTER TABLE orders
    DROP CONSTRAINT orders_status_check,
    ADD CONSTRAINT orders_status_check CHECK (status IN (
        'draft','awaiting_payment','paid','generating',
        'done','failed','expired','refund_requested','refunded')),
    ADD COLUMN refunded_at timestamptz;

-- +goose Down
UPDATE orders SET status = 'refund_requested' WHERE status = 'refunded';
ALTER TABLE orders
    DROP COLUMN refunded_at,
    DROP CONSTRAINT orders_status_check,
    ADD CONSTRAINT orders_status_check CHECK (status IN (
        'draft','awaiting_payment','paid','generating',
        'done','failed','expired','refund_requested'));
