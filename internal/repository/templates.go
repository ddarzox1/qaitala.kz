package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/askhat/qaitala/internal/model"
)

var ErrNotFound = errors.New("не найдено")

type Templates struct{ db *pgxpool.Pool }

func NewTemplates(db *pgxpool.Pool) *Templates { return &Templates{db: db} }

const tplCols = `id, name, desc_ru, desc_kz, clip_key, poster_key, duration_sec,
	aspect_w, aspect_h, ref_photos, slot_labels_ru, slot_labels_kz, hf_prompt,
	price_480p, price_720p, price_1080p, popular, sort, active`

func scanTemplate(row pgx.Row) (*model.Template, error) {
	var t model.Template
	err := row.Scan(&t.ID, &t.Name, &t.DescRu, &t.DescKz, &t.ClipURL, &t.PosterURL,
		&t.DurationSec, &t.AspectW, &t.AspectH, &t.RefPhotos, &t.SlotLabelsRu,
		&t.SlotLabelsKz, &t.Prompt, &t.Price480, &t.Price720, &t.Price1080,
		&t.Popular, &t.Sort, &t.Active)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("templates scan: %w", err)
	}
	return &t, nil
}

// ListActive отдаёт каталог. ClipURL/PosterURL здесь ещё ключи S3 — в публичные
// ссылки их превращает хендлер, он один знает базовый адрес хранилища.
func (r *Templates) ListActive(ctx context.Context) ([]*model.Template, error) {
	rows, err := r.db.Query(ctx, `SELECT `+tplCols+` FROM templates WHERE active ORDER BY sort, id`)
	if err != nil {
		return nil, fmt.Errorf("templates list: %w", err)
	}
	defer rows.Close()

	var out []*model.Template
	for rows.Next() {
		t, err := scanTemplate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Get возвращает активный шаблон — для покупки. Неактивный недоступен и по
// прямой ссылке, иначе снятый с продажи ролик можно было бы заказать мимо каталога.
func (r *Templates) Get(ctx context.Context, id string) (*model.Template, error) {
	return scanTemplate(r.db.QueryRow(ctx,
		`SELECT `+tplCols+` FROM templates WHERE id = $1 AND active`, id))
}

// GetAny возвращает шаблон независимо от active — для уже оплаченных заказов.
// Снятие ролика с продажи не должно ломать генерацию тем, кто успел заплатить.
func (r *Templates) GetAny(ctx context.Context, id string) (*model.Template, error) {
	return scanTemplate(r.db.QueryRow(ctx,
		`SELECT `+tplCols+` FROM templates WHERE id = $1`, id))
}

// ListAll — все шаблоны, включая скрытые. Для админки.
func (r *Templates) ListAll(ctx context.Context) ([]*model.Template, error) {
	rows, err := r.db.Query(ctx, `SELECT `+tplCols+` FROM templates ORDER BY sort, id`)
	if err != nil {
		return nil, fmt.Errorf("templates list all: %w", err)
	}
	defer rows.Close()

	var out []*model.Template
	for rows.Next() {
		t, err := scanTemplate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Upsert заводит шаблон целиком, вместе с роликом. ClipURL/PosterURL — ключи S3.
func (r *Templates) Upsert(ctx context.Context, t *model.Template) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO templates (id, name, desc_ru, desc_kz, clip_key, poster_key,
			duration_sec, aspect_w, aspect_h, ref_photos, slot_labels_ru, slot_labels_kz,
			hf_prompt, price_480p, price_720p, price_1080p, popular, active, sort)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)
		ON CONFLICT (id) DO UPDATE SET
			name=excluded.name, desc_ru=excluded.desc_ru, desc_kz=excluded.desc_kz,
			clip_key=excluded.clip_key, poster_key=excluded.poster_key,
			duration_sec=excluded.duration_sec, aspect_w=excluded.aspect_w,
			aspect_h=excluded.aspect_h, ref_photos=excluded.ref_photos,
			slot_labels_ru=excluded.slot_labels_ru, slot_labels_kz=excluded.slot_labels_kz,
			hf_prompt=excluded.hf_prompt, price_480p=excluded.price_480p,
			price_720p=excluded.price_720p, price_1080p=excluded.price_1080p,
			popular=excluded.popular, active=excluded.active, sort=excluded.sort`,
		t.ID, t.Name, t.DescRu, t.DescKz, t.ClipURL, t.PosterURL,
		t.DurationSec, t.AspectW, t.AspectH, t.RefPhotos, t.SlotLabelsRu, t.SlotLabelsKz,
		t.Prompt, t.Price480, t.Price720, t.Price1080, t.Popular, t.Active, t.Sort)
	if err != nil {
		return fmt.Errorf("templates upsert: %w", err)
	}
	return nil
}

// UpdateMeta правит всё, кроме ролика и того, что из него вычислено.
func (r *Templates) UpdateMeta(ctx context.Context, t *model.Template) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE templates SET name=$2, desc_ru=$3, desc_kz=$4, ref_photos=$5,
			slot_labels_ru=$6, slot_labels_kz=$7, hf_prompt=$8, price_480p=$9,
			price_720p=$10, price_1080p=$11, popular=$12, active=$13, sort=$14
		WHERE id = $1`,
		t.ID, t.Name, t.DescRu, t.DescKz, t.RefPhotos, t.SlotLabelsRu, t.SlotLabelsKz,
		t.Prompt, t.Price480, t.Price720, t.Price1080, t.Popular, t.Active, t.Sort)
	if err != nil {
		return fmt.Errorf("templates update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Templates) SetActive(ctx context.Context, id string, active bool) error {
	tag, err := r.db.Exec(ctx, `UPDATE templates SET active = $2 WHERE id = $1`, id, active)
	if err != nil {
		return fmt.Errorf("templates set active: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
