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
	price_480p, price_720p, price_1080p, popular, sort`

func scanTemplate(row pgx.Row) (*model.Template, error) {
	var t model.Template
	err := row.Scan(&t.ID, &t.Name, &t.DescRu, &t.DescKz, &t.ClipURL, &t.PosterURL,
		&t.DurationSec, &t.AspectW, &t.AspectH, &t.RefPhotos, &t.SlotLabelsRu,
		&t.SlotLabelsKz, &t.Prompt, &t.Price480, &t.Price720, &t.Price1080,
		&t.Popular, &t.Sort)
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
