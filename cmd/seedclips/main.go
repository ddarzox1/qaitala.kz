// Команда seedclips заливает исходные ролики в S3 и заводит шаблоны в базе.
// Идемпотентна: подменил видео или правил meta.json — прогони снова.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/askhat/qaitala/internal/blob"
	"github.com/askhat/qaitala/internal/config"
	"github.com/askhat/qaitala/internal/media"
)

// Genjutsu режет всё, что длиннее 30 секунд. Подрезаем сами: иначе граничные
// ролики обрежутся в непредсказуемом месте.
const maxClipSec = 29.5

// Первый кадр часто не отражает содержание ролика, поэтому постер берётся
// из середины. Переопределяется полем poster_at в meta.json.
const posterFraction = 0.35

type meta struct {
	Name         string   `json:"name"`
	DescRu       string   `json:"desc_ru"`
	DescKz       string   `json:"desc_kz"`
	SlotLabelsRu []string `json:"slot_labels_ru"`
	SlotLabelsKz []string `json:"slot_labels_kz"`
	PosterAt     *float64 `json:"poster_at"`
	Price480     *int     `json:"price_480p"`
	Price720     *int     `json:"price_720p"`
	Price1080    *int     `json:"price_1080p"`
	Popular      bool     `json:"popular"`
	Sort         int      `json:"sort"`
}

type request struct {
	Prompt string `json:"prompt"`
}

func main() {
	dir := flag.String("dir", "clips", "папка с роликами")
	only := flag.String("only", "", "залить только этот шаблон")
	flag.Parse()

	cfg := config.Load()
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("база: %v", err)
	}
	defer pool.Close()

	store := blob.New(cfg.S3Endpoint, cfg.S3Region, cfg.S3Bucket, cfg.S3Key, cfg.S3Secret, cfg.S3PublicBase)

	entries, err := os.ReadDir(*dir)
	if err != nil {
		log.Fatalf("%s: %v", *dir, err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	var failed int
	for _, e := range entries {
		if !e.IsDir() || (*only != "" && e.Name() != *only) {
			continue
		}
		if err := seed(ctx, pool, store, filepath.Join(*dir, e.Name()), e.Name()); err != nil {
			fmt.Printf("  ✗ %-17s %v\n", e.Name(), err)
			failed++
			continue
		}
	}
	if failed > 0 {
		os.Exit(1)
	}
}

func seed(ctx context.Context, pool *pgxpool.Pool, store *blob.Store, dir, id string) error {
	video, err := findVideo(dir)
	if err != nil {
		return err
	}

	var m meta
	if err := readJSON(filepath.Join(dir, "meta.json"), &m); err != nil {
		return err
	}
	var req request
	reqPath := filepath.Join(dir, "request.json")
	if err := readJSON(reqPath, &req); err != nil {
		return err
	}
	refPhotos, err := countImageSlots(reqPath)
	if err != nil {
		return err
	}

	// Кривой шаблон лучше не заводить вовсе, чем показать клиенту слоты без подписей.
	if len(m.SlotLabelsRu) != refPhotos || len(m.SlotLabelsKz) != refPhotos {
		return fmt.Errorf("слотов в request.json %d, а подписей ru/kz %d/%d",
			refPhotos, len(m.SlotLabelsRu), len(m.SlotLabelsKz))
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return fmt.Errorf("пустой prompt в request.json")
	}

	info, err := media.Probe(ctx, video)
	if err != nil {
		return err
	}
	if info.DurationSec < 4 {
		return fmt.Errorf("ролик короче 4 секунд (%.1f), Genjutsu такой не возьмёт", info.DurationSec)
	}

	tmp, err := os.MkdirTemp("", "seedclips")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	clip := filepath.Join(tmp, "clip.mp4")
	if err := media.NormalizeVideo(ctx, video, clip, maxClipSec); err != nil {
		return err
	}
	norm, err := media.Probe(ctx, clip)
	if err != nil {
		return err
	}

	at := norm.DurationSec * posterFraction
	if m.PosterAt != nil {
		at = *m.PosterAt
	}
	poster := filepath.Join(tmp, "poster.jpg")
	if err := media.Poster(ctx, clip, poster, at); err != nil {
		return err
	}

	clipKey := "public/clips/" + id + ".mp4"
	posterKey := "public/posters/" + id + ".jpg"
	if err := upload(ctx, store, clip, clipKey, "video/mp4"); err != nil {
		return err
	}
	if err := upload(ctx, store, poster, posterKey, "image/jpeg"); err != nil {
		return err
	}

	// Формат берём с оригинала: при масштабировании ширина округляется до чётной
	// (1080/16*9 = 607,5 → 608), и точное 9:16 превратилось бы в 76:135.
	aw, ah := media.Aspect(info.Width, info.Height)
	active := m.Price480 != nil || m.Price720 != nil || m.Price1080 != nil

	_, err = pool.Exec(ctx, `
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
		id, m.Name, m.DescRu, m.DescKz, clipKey, posterKey,
		norm.DurationSec, aw, ah, refPhotos, m.SlotLabelsRu, m.SlotLabelsKz,
		req.Prompt, m.Price480, m.Price720, m.Price1080, m.Popular, active, m.Sort)
	if err != nil {
		return fmt.Errorf("запись в базу: %w", err)
	}

	state := "в каталоге"
	if !active {
		state = "скрыт — нет цен"
	}
	fmt.Printf("  ✓ %-17s %5.1fс  %d:%-3d  фото:%d  %s\n", id, norm.DurationSec, aw, ah, refPhotos, state)
	return nil
}

func upload(ctx context.Context, store *blob.Store, path, key, contentType string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return store.Put(ctx, key, contentType, f)
}

func findVideo(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var found []string
	for _, e := range entries {
		switch strings.ToLower(filepath.Ext(e.Name())) {
		case ".mp4", ".mov", ".m4v":
			found = append(found, filepath.Join(dir, e.Name()))
		}
	}
	switch len(found) {
	case 0:
		return "", fmt.Errorf("нет видеофайла")
	case 1:
		return found[0], nil
	default:
		return "", fmt.Errorf("несколько видеофайлов, оставь один")
	}
}

// countImageSlots считает ключи image_N — столько фото и просим у клиента.
func countImageSlots(path string) (int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	var all map[string]any
	if err := json.Unmarshal(raw, &all); err != nil {
		return 0, fmt.Errorf("request.json: %w", err)
	}
	n := 0
	for k := range all {
		if strings.HasPrefix(k, "image_") {
			n++
		}
	}
	if n < 1 || n > 8 {
		return 0, fmt.Errorf("слотов image_N: %d, Genjutsu принимает 1–8", n)
	}
	return n, nil
}

func readJSON(path string, v any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return nil
}
