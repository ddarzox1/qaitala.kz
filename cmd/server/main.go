package main

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/pressly/goose/v3"

	"github.com/askhat/qaitala"
	"github.com/askhat/qaitala/internal/blob"
	"github.com/askhat/qaitala/internal/config"
	"github.com/askhat/qaitala/internal/handler"
	"github.com/askhat/qaitala/internal/higgsfield"
	"github.com/askhat/qaitala/internal/kaspi"
	"github.com/askhat/qaitala/internal/repository"
	"github.com/askhat/qaitala/internal/service"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg := config.Load()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error("подключение к базе", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := migrate(cfg.DatabaseURL); err != nil {
		log.Error("миграции", "err", err)
		os.Exit(1)
	}

	store := blob.New(cfg.R2Endpoint, "auto", cfg.R2Bucket, cfg.R2KeyID, cfg.R2Secret, cfg.R2PublicURL)
	orders := repository.NewOrders(pool)
	templates := repository.NewTemplates(pool)
	kaspiCl := kaspi.New(cfg.KaspiBaseURL, cfg.KaspiTokenSN, cfg.KaspiVtokenSecret, cfg.KaspiProfileID)
	hfCl := higgsfield.New(cfg.HFKeyID, cfg.HFKeySecret)

	gen := service.NewGenerator(orders, templates, hfCl, store,
		cfg.PublicBaseURL, cfg.HFWebhookSecret, cfg.FakeVideo, log)
	gen.Run(ctx, cfg.WorkerConcurrency)

	handler.IndexHTML = qaitala.IndexHTML

	e := echo.New()
	e.HideBanner, e.HidePort = true, true
	e.Use(middleware.Recover())
	e.Use(middleware.BodyLimit("64M"))
	handler.New(cfg, orders, templates, kaspiCl, gen, store, log).Register(e)

	srv := &http.Server{Addr: ":" + cfg.Port, Handler: e}
	go func() {
		log.Info("запущен", "порт", cfg.Port,
			"заглушка_оплаты", cfg.FakePayments, "заглушка_видео", cfg.FakeVideo)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("сервер", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdown)
	log.Info("остановлен")
}

func migrate(dsn string) error {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return err
	}
	defer db.Close()

	goose.SetBaseFS(qaitala.Migrations)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	return goose.Up(db, "migrations")
}
