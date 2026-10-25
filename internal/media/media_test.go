package media

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSplitAndConcatVideo(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg не установлен")
	}
	ctx := context.Background()
	dir := t.TempDir()
	source := filepath.Join(dir, "source.mp4")
	if _, err := run(ctx, "ffmpeg", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=320x180:rate=12:duration=10",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=10",
		"-shortest", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", source); err != nil {
		// Например, локальный Homebrew ffmpeg может остаться без dylib после
		// обновления. В production-образе ffmpeg ставится вместе с зависимостями.
		t.Skipf("ffmpeg не запускается: %v", err)
	}

	first := filepath.Join(dir, "first.mp4")
	second := filepath.Join(dir, "second.mp4")
	if err := SplitVideo(ctx, source, first, second, 5); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{first, second} {
		info, err := Probe(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		if info.DurationSec < 4.8 || info.DurationSec > 5.2 {
			t.Fatalf("неожиданная длина части %s: %.3f", filepath.Base(path), info.DurationSec)
		}
	}

	joined := filepath.Join(dir, "joined.mp4")
	if err := ConcatVideos(ctx, first, second, joined); err != nil {
		t.Fatal(err)
	}
	info, err := Probe(ctx, joined)
	if err != nil {
		t.Fatal(err)
	}
	if info.DurationSec < 9.7 || info.DurationSec > 10.3 {
		t.Fatalf("неожиданная длина склейки: %.3f", info.DurationSec)
	}
}
