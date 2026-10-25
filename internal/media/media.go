// Package media — тонкая обёртка над ffmpeg/ffprobe.
package media

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Higgsfield принимает только эти типы. HEIC с айфонов в список не входит.
var allowedImage = map[string]bool{
	"image/jpeg": true, "image/png": true, "image/webp": true, "image/gif": true,
}

func run(ctx context.Context, name string, args ...string) ([]byte, error) {
	var out, errb bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if len(msg) > 400 {
			msg = msg[len(msg)-400:]
		}
		return nil, fmt.Errorf("%s: %w: %s", name, err, msg)
	}
	return out.Bytes(), nil
}

type Info struct {
	DurationSec   float64
	Width, Height int
}

func Probe(ctx context.Context, path string) (*Info, error) {
	out, err := run(ctx, "ffprobe", "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=width,height", "-show_entries", "format=duration",
		"-of", "json", path)
	if err != nil {
		return nil, err
	}
	var raw struct {
		Streams []struct{ Width, Height int } `json:"streams"`
		Format  struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("ffprobe decode: %w", err)
	}
	if len(raw.Streams) == 0 {
		return nil, fmt.Errorf("ffprobe: в %s нет видеодорожки", filepath.Base(path))
	}
	d, _ := strconv.ParseFloat(raw.Format.Duration, 64)
	return &Info{DurationSec: d, Width: raw.Streams[0].Width, Height: raw.Streams[0].Height}, nil
}

// Aspect — сокращённое соотношение сторон: 1920x1080 → 16:9.
func Aspect(w, h int) (int, int) {
	g := gcd(w, h)
	if g == 0 {
		return w, h
	}
	return w / g, h / g
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// NormalizeVideo готовит один файл, который служит и превью на сайте, и
// video_url для Genjutsu Lite: H.264, длинная сторона не больше 1080, обрезка
// по maxSec. На вход 4K бессмысленны — на выходе максимум 1080p.
func NormalizeVideo(ctx context.Context, src, dst string, maxSec float64) error {
	_, err := run(ctx, "ffmpeg", "-y", "-i", src,
		"-t", strconv.FormatFloat(maxSec, 'f', 2, 64),
		"-vf", "scale='if(gt(iw,ih),min(1080,iw),-2)':'if(gt(iw,ih),-2,min(1080,ih))'",
		"-c:v", "libx264", "-preset", "medium", "-crf", "23",
		"-pix_fmt", "yuv420p", "-movflags", "+faststart",
		"-c:a", "aac", "-b:a", "128k", dst)
	return err
}

// SplitVideo делит нормализованный ролик точно в указанной точке. Перекодировка
// нужна для покадрового разреза: stream copy привязал бы стык к ближайшему
// ключевому кадру и мог потерять либо повторить несколько секунд.
func SplitVideo(ctx context.Context, src, first, second string, atSec float64) error {
	info, err := Probe(ctx, src)
	if err != nil {
		return err
	}
	if atSec < 4 || info.DurationSec-atSec < 4 {
		return fmt.Errorf("точка разреза %.2fс оставляет часть короче 4 секунд", atSec)
	}

	encode := func(dst string, args ...string) error {
		args = append(args,
			"-map", "0:v:0", "-map", "0:a?",
			"-c:v", "libx264", "-preset", "medium", "-crf", "18",
			"-pix_fmt", "yuv420p", "-movflags", "+faststart",
			"-c:a", "aac", "-b:a", "128k", dst)
		_, err := run(ctx, "ffmpeg", args...)
		return err
	}
	at := strconv.FormatFloat(atSec, 'f', 3, 64)
	if err := encode(first, "-y", "-i", src, "-t", at); err != nil {
		return fmt.Errorf("первая часть: %w", err)
	}
	if err := encode(second, "-y", "-i", src, "-ss", at); err != nil {
		return fmt.Errorf("вторая часть: %w", err)
	}
	return nil
}

// ConcatVideos соединяет результаты двух последовательных генераций без
// повторного пережатия. Оба файла пришли от одной модели с одинаковым quality,
// поэтому их кодеки и геометрия должны совпадать.
func ConcatVideos(ctx context.Context, first, second, dst string) error {
	dir := filepath.Dir(dst)
	list := filepath.Join(dir, "concat.txt")
	content := "file '" + strings.ReplaceAll(first, "'", "'\\''") + "'\n" +
		"file '" + strings.ReplaceAll(second, "'", "'\\''") + "'\n"
	if err := os.WriteFile(list, []byte(content), 0o600); err != nil {
		return err
	}
	_, err := run(ctx, "ffmpeg", "-y", "-f", "concat", "-safe", "0", "-i", list,
		"-c", "copy", "-movflags", "+faststart", dst)
	if err == nil {
		return nil
	}

	// Если провайдер вернул части с разными параметрами кодека, stream copy не
	// сработает. Один раз пережимаем склейку вместо того, чтобы навсегда оставить
	// оплаченный заказ в generating.
	_, retryErr := run(ctx, "ffmpeg", "-y", "-f", "concat", "-safe", "0", "-i", list,
		"-c:v", "libx264", "-preset", "medium", "-crf", "18", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-b:a", "128k", "-movflags", "+faststart", dst)
	if retryErr != nil {
		return fmt.Errorf("склейка частей: copy: %v; reencode: %w", err, retryErr)
	}
	return nil
}

// Poster вынимает кадр для карточки каталога. Первый кадр часто не отражает
// содержание ролика, поэтому момент задаётся явно.
func Poster(ctx context.Context, src, dst string, atSec float64) error {
	_, err := run(ctx, "ffmpeg", "-y", "-ss", strconv.FormatFloat(atSec, 'f', 2, 64),
		"-i", src, "-frames:v", "1", "-q:v", "3", dst)
	return err
}

// NormalizeImage приводит фото клиента к типу, который примет Higgsfield.
// HEIC перекодируется в JPEG, остальное проходит как есть.
func NormalizeImage(ctx context.Context, data []byte, contentType, filename string) ([]byte, string, error) {
	if allowedImage[contentType] {
		return data, contentType, nil
	}
	ext := strings.ToLower(filepath.Ext(filename))
	if contentType != "image/heic" && contentType != "image/heif" && ext != ".heic" && ext != ".heif" {
		return nil, "", fmt.Errorf("неподдерживаемый формат %q", contentType)
	}

	dir, err := os.MkdirTemp("", "heic")
	if err != nil {
		return nil, "", err
	}
	defer os.RemoveAll(dir)

	src, dst := filepath.Join(dir, "in.heic"), filepath.Join(dir, "out.jpg")
	if err := os.WriteFile(src, data, 0o600); err != nil {
		return nil, "", err
	}
	if _, err := run(ctx, "ffmpeg", "-y", "-i", src, "-q:v", "3", dst); err != nil {
		return nil, "", fmt.Errorf("конвертация HEIC: %w", err)
	}
	jpg, err := os.ReadFile(dst)
	if err != nil {
		return nil, "", err
	}
	return jpg, "image/jpeg", nil
}
