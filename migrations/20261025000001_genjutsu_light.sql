-- +goose Up
-- Genjutsu Lite принимает не больше 15 секунд. Для длинного шаблона здесь
-- хранится номер активной половины; request_id по-прежнему только один, потому
-- что половины отправляются строго последовательно.
ALTER TABLE orders
    ADD COLUMN hf_segment smallint NOT NULL DEFAULT 0
        CHECK (hf_segment BETWEEN 0 AND 2);

-- +goose Down
ALTER TABLE orders DROP COLUMN hf_segment;
