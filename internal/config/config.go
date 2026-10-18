package config

import (
	"log"

	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
)

type Config struct {
	Port          string `envconfig:"PORT" default:"8080"`
	PublicBaseURL string `envconfig:"PUBLIC_BASE_URL" required:"true"`
	DatabaseURL   string `envconfig:"DATABASE_URL" required:"true"`

	KaspiBaseURL       string `envconfig:"KASPI_BASE_URL" default:"http://kaspi:3000"`
	KaspiTokenSN       string `envconfig:"KASPI_TOKEN_SN"`
	KaspiVtokenSecret  string `envconfig:"KASPI_VTOKEN_SECRET"`
	KaspiProfileID     string `envconfig:"KASPI_PROFILE_ID"`
	KaspiWebhookSecret string `envconfig:"KASPI_WEBHOOK_SECRET"`

	HFKeyID         string `envconfig:"HF_API_KEY_ID"`
	HFKeySecret     string `envconfig:"HF_API_KEY_SECRET"`
	HFWebhookSecret string `envconfig:"HF_WEBHOOK_SECRET"`

	R2AccountID string `envconfig:"R2_ACCOUNT_ID"`
	R2Endpoint  string `envconfig:"R2_ENDPOINT"` // локально: SeaweedFS; в бою выводится из R2_ACCOUNT_ID
	R2KeyID     string `envconfig:"R2_ACCESS_KEY_ID" required:"true"`
	R2Secret    string `envconfig:"R2_SECRET_ACCESS_KEY" required:"true"`
	R2Bucket    string `envconfig:"R2_BUCKET" required:"true"`
	R2PublicURL string `envconfig:"R2_PUBLIC_URL" required:"true"`

	// Пароль для /admin. Пустой — админки нет вовсе.
	AdminPassword string `envconfig:"ADMIN_PASSWORD"`

	WorkerConcurrency int `envconfig:"WORKER_CONCURRENCY" default:"2"`

	// Прогон всего потока без обращения к Kaspi и Higgsfield — и без трат.
	FakePayments bool `envconfig:"FAKE_PAYMENTS"`
	FakeVideo    bool `envconfig:"FAKE_VIDEO"`
}

func Load() Config {
	_ = godotenv.Load()
	var c Config
	if err := envconfig.Process("", &c); err != nil {
		log.Fatalf("config: %v", err)
	}
	if c.R2Endpoint == "" {
		if c.R2AccountID == "" {
			log.Fatal("config: нужен R2_ACCOUNT_ID (или R2_ENDPOINT)")
		}
		c.R2Endpoint = "https://" + c.R2AccountID + ".r2.cloudflarestorage.com"
	}
	if !c.FakePayments && (c.KaspiTokenSN == "" || c.KaspiVtokenSecret == "") {
		log.Fatal("config: нужны KASPI_TOKEN_SN и KASPI_VTOKEN_SECRET (или FAKE_PAYMENTS=1)")
	}
	if !c.FakeVideo && (c.HFKeyID == "" || c.HFKeySecret == "") {
		log.Fatal("config: нужны HF_API_KEY_ID и HF_API_KEY_SECRET (или FAKE_VIDEO=1)")
	}
	return c
}
