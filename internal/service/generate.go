// Package service — фоновая генерация: очередь, отправка в Genjutsu,
// разбор результата и подстраховка опросом.
package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/askhat/qaitala/internal/blob"
	"github.com/askhat/qaitala/internal/higgsfield"
	"github.com/askhat/qaitala/internal/model"
	"github.com/askhat/qaitala/internal/repository"
)

const (
	pollInterval   = time.Minute
	stalledAfter   = 5 * time.Minute
	claimIdle      = 3 * time.Second
	presignTTL     = 6 * time.Hour
	failUserFacing = "генерация не удалась"
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
			g.log.Error("отправка в Genjutsu не удалась", "token", order.Token, "err", err)
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
		return g.orders.SetHFRequest(ctx, o.Token, "fake-"+o.Token)
	}

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

	sub, err := g.hf.Submit(ctx,
		g.store.PublicURL(tpl.ClipURL), imageURLs, tpl.Prompt, o.Resolution,
		o.Token, g.baseURL+"/webhooks/higgsfield/"+g.hookToken)
	if err != nil {
		return err
	}
	g.log.Info("задача поставлена", "token", o.Token, "request_id", sub.RequestID)
	return g.orders.SetHFRequest(ctx, o.Token, sub.RequestID)
}

// Check — единственный путь, которым заказ уходит в done или failed.
// Вызывается и вебхуком, и реконсилятором; вебхуку самому по себе не верим.
func (g *Generator) Check(ctx context.Context, requestID string) error {
	order, err := g.orders.ByHFRequest(ctx, requestID)
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

	key := "orders/" + order.Token + "/result.mp4"
	if err := g.copyToStore(ctx, res.Video, key); err != nil {
		return err
	}
	g.log.Info("заказ готов", "token", order.Token)
	return g.orders.MarkDone(ctx, order.Token, key)
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
