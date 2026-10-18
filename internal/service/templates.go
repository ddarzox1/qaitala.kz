package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/askhat/qaitala/internal/blob"
	"github.com/askhat/qaitala/internal/media"
	"github.com/askhat/qaitala/internal/model"
	"github.com/askhat/qaitala/internal/repository"
)

// Genjutsu режет всё, что длиннее 30 секунд. Подрезаем сами: иначе граничные
// ролики обрежутся в непредсказуемом месте.
const maxClipSec = 29.5

// Первый кадр часто не отражает содержание ролика, поэтому постер берётся
// из середины. Переопределяется полем poster_at.
const posterFraction = 0.35

// id попадает в ключи S3 и адреса — только безопасные символы.
var templateID = regexp.MustCompile(`^[a-z0-9_-]{1,40}$`)

// TemplateInput — то, что задаёт человек. Теги совпадают с clips/*/meta.json,
// поэтому seedclips читает meta.json прямо в эту структуру.
type TemplateInput struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	DescRu       string   `json:"desc_ru"`
	DescKz       string   `json:"desc_kz"`
	Prompt       string   `json:"prompt"`
	SlotLabelsRu []string `json:"slot_labels_ru"`
	SlotLabelsKz []string `json:"slot_labels_kz"`
	PosterAt     *float64 `json:"poster_at"`
	Price480     *int     `json:"price_480p"`
	Price720     *int     `json:"price_720p"`
	Price1080    *int     `json:"price_1080p"`
	Popular      bool     `json:"popular"`
	Sort         int      `json:"sort"`
	Active       bool     `json:"active"`
}

// SaveTemplate заводит или обновляет шаблон. С video — перекодирует ролик,
// снимает постер и заливает оба в S3. Без video — правит только поля
// существующего шаблона, ролик остаётся прежним.
func SaveTemplate(ctx context.Context, repo *repository.Templates, store *blob.Store,
	in TemplateInput, video string) (*model.Template, error) {
	t, err := in.validate()
	if err != nil {
		return nil, err
	}
	if video == "" {
		return t, repo.UpdateMeta(ctx, t)
	}

	info, err := media.Probe(ctx, video)
	if err != nil {
		return nil, err
	}
	if info.DurationSec < 4 {
		return nil, fmt.Errorf("ролик короче 4 секунд (%.1f), Genjutsu такой не возьмёт", info.DurationSec)
	}

	tmp, err := os.MkdirTemp("", "template")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)

	clip := filepath.Join(tmp, "clip.mp4")
	if err := media.NormalizeVideo(ctx, video, clip, maxClipSec); err != nil {
		return nil, err
	}
	norm, err := media.Probe(ctx, clip)
	if err != nil {
		return nil, err
	}

	at := norm.DurationSec * posterFraction
	if in.PosterAt != nil {
		at = *in.PosterAt
	}
	poster := filepath.Join(tmp, "poster.jpg")
	if err := media.Poster(ctx, clip, poster, at); err != nil {
		return nil, err
	}

	// Версия в ключе: CDN закэшировал бы старый ролик под тем же адресом.
	// ponytail: старые версии в S3 не удаляем, копейки; чистить, если разрастётся.
	v := time.Now().Unix()
	t.ClipURL = fmt.Sprintf("public/clips/%s-%d.mp4", t.ID, v)
	t.PosterURL = fmt.Sprintf("public/posters/%s-%d.jpg", t.ID, v)
	if err := upload(ctx, store, clip, t.ClipURL, "video/mp4"); err != nil {
		return nil, err
	}
	if err := upload(ctx, store, poster, t.PosterURL, "image/jpeg"); err != nil {
		return nil, err
	}

	// Формат берём с оригинала: при масштабировании ширина округляется до чётной
	// (1080/16*9 = 607,5 → 608), и точное 9:16 превратилось бы в 76:135.
	t.AspectW, t.AspectH = media.Aspect(info.Width, info.Height)
	t.DurationSec = norm.DurationSec
	return t, repo.Upsert(ctx, t)
}

// Кривой шаблон лучше не заводить вовсе, чем показать клиенту слоты без подписей.
func (in TemplateInput) validate() (*model.Template, error) {
	if !templateID.MatchString(in.ID) {
		return nil, fmt.Errorf("id: только a-z, 0-9, _ и -, до 40 символов")
	}
	if strings.TrimSpace(in.Name) == "" {
		return nil, fmt.Errorf("пустое название")
	}
	if strings.TrimSpace(in.Prompt) == "" {
		return nil, fmt.Errorf("пустой prompt")
	}
	n := len(in.SlotLabelsRu)
	if n < 1 || n > 8 {
		return nil, fmt.Errorf("фото: %d, Genjutsu принимает 1–8", n)
	}
	if len(in.SlotLabelsKz) != n {
		return nil, fmt.Errorf("подписей ru %d, а kz %d", n, len(in.SlotLabelsKz))
	}
	for _, p := range []*int{in.Price480, in.Price720, in.Price1080} {
		if p != nil && *p <= 0 {
			return nil, fmt.Errorf("цена должна быть больше нуля")
		}
	}
	if in.Active && in.Price480 == nil && in.Price720 == nil && in.Price1080 == nil {
		return nil, fmt.Errorf("без цен в каталог не выставить")
	}
	return &model.Template{
		ID: in.ID, Name: in.Name, DescRu: in.DescRu, DescKz: in.DescKz,
		Prompt: in.Prompt, RefPhotos: n, SlotLabelsRu: in.SlotLabelsRu, SlotLabelsKz: in.SlotLabelsKz,
		Price480: in.Price480, Price720: in.Price720, Price1080: in.Price1080,
		Popular: in.Popular, Sort: in.Sort, Active: in.Active,
	}, nil
}

func upload(ctx context.Context, store *blob.Store, path, key, contentType string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return store.Put(ctx, key, contentType, f)
}
