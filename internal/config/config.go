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

	S3Endpoint   string `envconfig:"S3_ENDPOINT" required:"true"`
	S3Region     string `envconfig:"S3_REGION" default:"auto"`
	S3Bucket     string `envconfig:"S3_BUCKET" required:"true"`
	S3Key        string `envconfig:"S3_KEY" required:"true"`
	S3Secret     string `envconfig:"S3_SECRET" required:"true"`
	S3PublicBase string `envconfig:"S3_PUBLIC_BASE" required:"true"`

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
	if !c.FakePayments && (c.KaspiTokenSN == "" || c.KaspiVtokenSecret == "") {
		log.Fatal("config: нужны KASPI_TOKEN_SN и KASPI_VTOKEN_SECRET (или FAKE_PAYMENTS=1)")
	}
	if !c.FakeVideo && (c.HFKeyID == "" || c.HFKeySecret == "") {
		log.Fatal("config: нужны HF_API_KEY_ID и HF_API_KEY_SECRET (или FAKE_VIDEO=1)")
	}
	return c
}
