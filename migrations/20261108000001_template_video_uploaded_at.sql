-- +goose Up
ALTER TABLE templates
    ADD COLUMN video_uploaded_at timestamptz NOT NULL DEFAULT now();

CREATE INDEX templates_active_latest
    ON templates (video_uploaded_at DESC)
    WHERE active;

-- +goose Down
DROP INDEX templates_active_latest;
ALTER TABLE templates DROP COLUMN video_uploaded_at;
