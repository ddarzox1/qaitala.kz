-- +goose Up
CREATE TABLE templates (
    id             text PRIMARY KEY,
    name           text        NOT NULL,
    desc_ru        text        NOT NULL DEFAULT '',
    desc_kz        text        NOT NULL DEFAULT '',
    clip_key       text        NOT NULL,
    poster_key     text        NOT NULL,
    duration_sec   numeric(5,2) NOT NULL,
    aspect_w       int         NOT NULL,
    aspect_h       int         NOT NULL,
    ref_photos     int         NOT NULL CHECK (ref_photos BETWEEN 1 AND 8),
    slot_labels_ru text[]      NOT NULL,
    slot_labels_kz text[]      NOT NULL,
    hf_prompt      text        NOT NULL,
    price_480p     int         CHECK (price_480p  > 0),
    price_720p     int         CHECK (price_720p  > 0),
    price_1080p    int         CHECK (price_1080p > 0),
    popular        bool        NOT NULL DEFAULT false,
    active         bool        NOT NULL DEFAULT false,
    sort           int         NOT NULL DEFAULT 0,
    -- подписи ролей обязаны покрывать все слоты: кривой шаблон в каталог не попадёт
    CONSTRAINT slot_labels_match CHECK (
        cardinality(slot_labels_ru) = ref_photos AND
        cardinality(slot_labels_kz) = ref_photos
    ),
    -- активен только с хотя бы одной ценой, иначе купить его нельзя
    CONSTRAINT active_needs_price CHECK (
        NOT active OR coalesce(price_480p, price_720p, price_1080p) IS NOT NULL
    )
);

CREATE TABLE orders (
    token            text PRIMARY KEY,
    template_id      text        NOT NULL REFERENCES templates(id),
    resolution       text        NOT NULL CHECK (resolution IN ('480p','720p','1080p')),
    lang             text        NOT NULL DEFAULT 'ru',
    email            text,
    status           text        NOT NULL CHECK (status IN (
                         'draft','awaiting_payment','paid','generating',
                         'done','failed','expired','refund_requested')),
    photo_keys       text[]      NOT NULL DEFAULT '{}',
    amount_kzt       int         NOT NULL CHECK (amount_kzt > 0),
    qr_operation_id  bigint      UNIQUE,
    kaspi_payment_id text,
    hf_request_id    text        UNIQUE,
    video_key        text,
    retries_used     int         NOT NULL DEFAULT 0,
    attempts         int         NOT NULL DEFAULT 0,
    locked_at        timestamptz,
    error            text,
    created_at       timestamptz NOT NULL DEFAULT now(),
    paid_at          timestamptz,
    done_at          timestamptz
);

-- очередь: воркер берёт оплаченные заказы через FOR UPDATE SKIP LOCKED
CREATE INDEX orders_queue   ON orders (paid_at) WHERE status = 'paid';
-- реконсилятор: добирает зависшие, когда вебхук Higgsfield не дошёл
CREATE INDEX orders_pending ON orders (locked_at) WHERE status = 'generating';

-- +goose Down
DROP TABLE orders;
DROP TABLE templates;
