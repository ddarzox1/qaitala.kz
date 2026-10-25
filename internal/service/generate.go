// Package service — фоновая генерация: очередь, отправка в Genjutsu Lite,
// разбор результата и подстраховка опросом.
package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/askhat/qaitala/internal/blob"
	"github.com/askhat/qaitala/internal/higgsfield"
	"github.com/askhat/qaitala/internal/media"
	"github.com/askhat/qaitala/internal/model"
	"github.com/askhat/qaitala/internal/repository"
)

const (
	pollInterval = time.Minute
	stalledAfter = 5 * time.Minute
	// С запасом над худшим submit: 8 фото × 60 с таймаута HTTP.
	unstickAfter   = 15 * time.Minute
	expireAfter    = 24 * time.Hour
	claimIdle      = 3 * time.Second
	presignTTL     = 6 * time.Hour
	failUserFacing = "генерация не удалась"
	// AAC/MP4 добавляет несколько сотых секунды служебного padding: исходник на
	// 15.00с получается 15.02с по ffprobe. Genjutsu Lite всё равно берёт первые
	// 15 секунд, поэтому такой файл не режем на две независимые генерации.
	lightMaxSec = 15.1
)

type Generator struct {
	orders    *repository.Orders
	templates *repository.Templates
	hf        *higgsfield.Client
	store     *blob.Store
	baseURL   string
	hookToken string
	fake      bool
	log       *slog.Logger
}

func NewGenerator(o *repository.Orders, t *repository.Templates, hf *higgsfield.Client,
	st *blob.Store, baseURL, hookToken string, fake bool, log *slog.Logger) *Generator {
	return &Generator{orders: o, templates: t, hf: hf, store: st,
		baseURL: baseURL, hookToken: hookToken, fake: fake, log: log}
}

// Run поднимает n воркеров и реконсилятор. n ограничивает конкурентность:
// Higgsfield отвечает 400 при превышении лимита аккаунта.
func (g *Generator) Run(ctx context.Context, n int) {
	for i := 0; i < n; i++ {
		go g.worker(ctx)
	}
	go g.reconcile(ctx)
}

func (g *Generator) worker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		order, err := g.orders.ClaimJob(ctx)
		if errors.Is(err, repository.ErrNotFound) {
			select {
			case <-ctx.Done():
				return
			case <-time.After(claimIdle):
			}
			continue
		}
		if err != nil {
			g.log.Error("не удалось взять заказ в работу", "err", err)
			time.Sleep(claimIdle)
			continue
		}
		if err := g.submit(ctx, order); err != nil {
			g.log.Error("отправка в Genjutsu Lite не удалась", "token", order.Token, "err", err)
			_ = g.orders.MarkFailed(ctx, order.Token, failUserFacing)
		}
	}
}

func (g *Generator) submit(ctx context.Context, o *model.Order) error {
	// GetAny, а не Get: заказ уже оплачен, и снятие ролика с продажи
	// не должно оставить клиента без видео.
	tpl, err := g.templates.GetAny(ctx, o.TemplateID)
	if err != nil {
		return fmt.Errorf("шаблон %s: %w", o.TemplateID, err)
	}

	if g.fake {
		// Прогон потока без трат. Заказ дозревает не только по таймеру, но и
		// через реконсилятор — иначе эту подстраховку локально не проверить.
		go func() {
			time.Sleep(6 * time.Second)
			_ = g.orders.MarkDone(context.WithoutCancel(ctx), o.Token, tpl.ClipURL)
		}()
		return g.orders.SetHFRequest(ctx, o.Token, "fake-"+o.Token, 1)
	}

	if tpl.DurationSec <= lightMaxSec {
		return g.submitPart(ctx, o, tpl, 1, g.store.PublicURL(tpl.ClipURL))
	}

	// Lite принимает максимум 15 секунд. Исходники у нас не длиннее 29.5,
	// поэтому достаточно двух равных частей: каждая гарантированно короче лимита.
	if err := g.prepareParts(ctx, o.Token, tpl); err != nil {
		return err
	}
	url, err := g.store.PresignGet(ctx, sourcePartKey(o.Token, 1), presignTTL)
	if err != nil {
		return err
	}
	return g.submitPart(ctx, o, tpl, 1, url)
}

// submitPart загружает фотографии заново для каждой половины. Это сохраняет
// простой и восстанавливаемый поток: после рестарта часть можно безопасно
// отправить с тем же idempotency key.
func (g *Generator) submitPart(ctx context.Context, o *model.Order, tpl *model.Template,
	segment int, videoURL string) error {

	// Фото клиента уезжают в хранилище Higgsfield — наш бакет остаётся приватным.
	imageURLs := make([]string, 0, len(o.PhotoKeys))
	for _, key := range o.PhotoKeys {
		rc, err := g.store.Get(ctx, key)
		if err != nil {
			return err
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return fmt.Errorf("чтение %s: %w", key, err)
		}
		url, err := g.hf.UploadImage(ctx, "image/jpeg", data)
		if err != nil {
			return err
		}
		imageURLs = append(imageURLs, url)
	}

	idempotencyKey := fmt.Sprintf("%s-r%d-s%d", o.Token, o.RetriesUsed, segment)
	sub, err := g.hf.Submit(ctx, videoURL, imageURLs, tpl.Prompt, o.Resolution,
		idempotencyKey, g.baseURL+"/webhooks/higgsfield/"+g.hookToken)
	if err != nil {
		return err
	}
	g.log.Info("задача поставлена", "token", o.Token, "часть", segment, "request_id", sub.RequestID)
	return g.orders.SetHFRequest(ctx, o.Token, sub.RequestID, segment)
}

func sourcePartKey(token string, segment int) string {
	return fmt.Sprintf("orders/%s/source-%d.mp4", token, segment)
}

func resultPartKey(token string, segment int) string {
	return fmt.Sprintf("orders/%s/result-%d.mp4", token, segment)
}

// prepareParts один раз режет публичный шаблон и складывает обе половины рядом
// с заказом. Повтор после сбоя просто перезапишет те же ключи.
func (g *Generator) prepareParts(ctx context.Context, token string, tpl *model.Template) error {
	tmp, err := os.MkdirTemp("", "genjutsu-parts")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	source := filepath.Join(tmp, "source.mp4")
	if err := g.objectToFile(ctx, tpl.ClipURL, source); err != nil {
		return err
	}
	first, second := filepath.Join(tmp, "source-1.mp4"), filepath.Join(tmp, "source-2.mp4")
	if err := media.SplitVideo(ctx, source, first, second, tpl.DurationSec/2); err != nil {
		return err
	}
	if err := upload(ctx, g.store, first, sourcePartKey(token, 1), "video/mp4"); err != nil {
		return err
	}
	return upload(ctx, g.store, second, sourcePartKey(token, 2), "video/mp4")
}

// Check — единственный путь, которым заказ уходит в done или failed.
// Вызывается и вебхуком, и реконсилятором; вебхуку самому по себе не верим.
func (g *Generator) Check(ctx context.Context, requestID string) error {
	order, err := g.orders.ByHFRequest(ctx, requestID)
	if errors.Is(err, repository.ErrNotFound) {
		// Повторный вебхук от уже завершённой первой половины приходит после того,
		// как hf_request_id переключился на вторую. Это уже обработанный сигнал.
		return nil
	}
	if err != nil {
		return err
	}
	if order.Status != model.StatusGenerating {
		return nil // уже разобран
	}

	if g.fake {
		tpl, err := g.templates.GetAny(ctx, order.TemplateID)
		if err != nil {
			return err
		}
		return g.orders.MarkDone(ctx, order.Token, tpl.ClipURL)
	}

	res, err := g.hf.Status(ctx, requestID)
	if err != nil {
		return err
	}
	if !higgsfield.Terminal(res.Status) {
		return nil
	}

	if res.Status != higgsfield.StatusCompleted || res.Video == "" {
		reason := res.Error
		if reason == "" {
			reason = res.Status
		}
		g.log.Info("генерация не удалась", "token", order.Token, "status", res.Status, "err", reason)
		return g.orders.MarkFailed(ctx, order.Token, reason)
	}

	tpl, err := g.templates.GetAny(ctx, order.TemplateID)
	if err != nil {
		return err
	}
	if tpl.DurationSec > lightMaxSec && order.HFSegment > 0 {
		key := resultPartKey(order.Token, order.HFSegment)
		if err := g.copyToStore(ctx, res.Video, key); err != nil {
			return err
		}
		if order.HFSegment == 1 {
			url, err := g.store.PresignGet(ctx, sourcePartKey(order.Token, 2), presignTTL)
			if err != nil {
				return err
			}
			return g.submitPart(ctx, order, tpl, 2, url)
		}
		if err := g.joinParts(ctx, order.Token); err != nil {
			return err
		}
	} else {
		key := "orders/" + order.Token + "/result.mp4"
		if err := g.copyToStore(ctx, res.Video, key); err != nil {
			return err
		}
	}
	g.log.Info("заказ готов", "token", order.Token)
	return g.orders.MarkDone(ctx, order.Token, "orders/"+order.Token+"/result.mp4")
}

func (g *Generator) objectToFile(ctx context.Context, key, path string) error {
	rc, err := g.store.Get(ctx, key)
	if err != nil {
		return err
	}
	defer rc.Close()
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(f, rc)
	closeErr := f.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func (g *Generator) joinParts(ctx context.Context, token string) error {
	tmp, err := os.MkdirTemp("", "genjutsu-join")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	first, second := filepath.Join(tmp, "result-1.mp4"), filepath.Join(tmp, "result-2.mp4")
	if err := g.objectToFile(ctx, resultPartKey(token, 1), first); err != nil {
		return err
	}
	if err := g.objectToFile(ctx, resultPartKey(token, 2), second); err != nil {
		return err
	}
	joined := filepath.Join(tmp, "result.mp4")
	if err := media.ConcatVideos(ctx, first, second, joined); err != nil {
		return err
	}
	return upload(ctx, g.store, joined, "orders/"+token+"/result.mp4", "video/mp4")
}

// copyToStore перекладывает результат к нам: ссылки Higgsfield живут около
// недели, а клиенту мы обещаем тридцать дней.
func (g *Generator) copyToStore(ctx context.Context, videoURL, key string) error {
	rc, err := g.hf.Download(ctx, videoURL)
	if err != nil {
		return err
	}
	defer rc.Close()

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, rc); err != nil {
		return fmt.Errorf("скачивание результата: %w", err)
	}
	return g.store.Put(ctx, key, "video/mp4", bytes.NewReader(buf.Bytes()))
}

// reconcile добирает заказы, по которым вебхук не дошёл. Без него зависшая
// генерация осталась бы в этом статусе навсегда.
func (g *Generator) reconcile(ctx context.Context) {
	t := time.NewTicker(pollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if n, err := g.orders.Unstick(ctx, unstickAfter, failUserFacing); err != nil {
			g.log.Error("возврат неотправленных заказов", "err", err)
		} else if n > 0 {
			g.log.Warn("неотправленные заказы возвращены в очередь", "count", n)
		}
		if _, err := g.orders.ExpireStale(ctx, expireAfter); err != nil {
			g.log.Error("закрытие брошенных заказов", "err", err)
		}
		stalled, err := g.orders.Stalled(ctx, stalledAfter)
		if err != nil {
			g.log.Error("опрос зависших заказов", "err", err)
			continue
		}
		for _, o := range stalled {
			if err := g.Check(ctx, o.HFRequestID); err != nil {
				g.log.Error("перепроверка заказа", "token", o.Token, "err", err)
			}
		}
	}
}

// VideoURL — временная ссылка на готовый ролик.
func (g *Generator) VideoURL(ctx context.Context, key string) (string, error) {
	return g.store.PresignGet(ctx, key, presignTTL)
}
