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
	"github.com/askhat/qaitala/internal/repository"
	"github.com/askhat/qaitala/internal/service"
)

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

	repo := repository.NewTemplates(pool)
	store := blob.New(cfg.R2Endpoint, "auto", cfg.R2Bucket, cfg.R2KeyID, cfg.R2Secret, cfg.R2PublicURL)

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
		if err := seed(ctx, repo, store, filepath.Join(*dir, e.Name()), e.Name()); err != nil {
			fmt.Printf("  ✗ %-17s %v\n", e.Name(), err)
			failed++
			continue
		}
	}
	if failed > 0 {
		os.Exit(1)
	}
}

func seed(ctx context.Context, repo *repository.Templates, store *blob.Store, dir, id string) error {
	video, err := findVideo(dir)
	if err != nil {
		return err
	}

	var in service.TemplateInput
	if err := readJSON(filepath.Join(dir, "meta.json"), &in); err != nil {
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
	if len(in.SlotLabelsRu) != refPhotos {
		return fmt.Errorf("слотов в request.json %d, а подписей ru %d", refPhotos, len(in.SlotLabelsRu))
	}
	in.ID, in.Prompt = id, req.Prompt
	in.Active = in.Price480 != nil || in.Price720 != nil || in.Price1080 != nil

	t, err := service.SaveTemplate(ctx, repo, store, in, video)
	if err != nil {
		return err
	}

	state := "в каталоге"
	if !t.Active {
		state = "скрыт — нет цен"
	}
	fmt.Printf("  ✓ %-17s %5.1fс  %d:%-3d  фото:%d  %s\n", id, t.DurationSec, t.AspectW, t.AspectH, t.RefPhotos, state)
	return nil
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
