package blob

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type Store struct {
	cl         *s3.Client
	bucket     string
	publicBase string
}

func New(endpoint, region, bucket, key, secret, publicBase string) *Store {
	cl := s3.New(s3.Options{
		Region:       region,
		BaseEndpoint: aws.String(endpoint),
		Credentials:  credentials.NewStaticCredentialsProvider(key, secret, ""),
		UsePathStyle: true, // большинство S3-совместимых хранилищ ждут path-style
	})
	return &Store{cl: cl, bucket: bucket, publicBase: strings.TrimRight(publicBase, "/")}
}

// Put кладёт объект. Всё под public/ читается анонимно: это витрина, её же
// скачивает Higgsfield как video_url. Остальное приватно и отдаётся только
// через PresignGet.
//
// ponytail: прав на объекте нет — в R2 их не существует, публична сразу вся
// корзина. Приватность orders/* держит WAF custom rule на зоне qaitala.kz:
// всё на cdn.qaitala.kz вне /public/ блокируется. Снесёшь правило — откроешь
// фото клиентов. Нужна жёсткая гарантия вместо правила — вторая корзина
// без публичного домена.
func (s *Store) Put(ctx context.Context, key, contentType string, body io.Reader) error {
	in := &s3.PutObjectInput{
		Bucket:      &s.bucket,
		Key:         &key,
		Body:        body,
		ContentType: &contentType,
	}
	_, err := s.cl.PutObject(ctx, in)
	if err != nil {
		return fmt.Errorf("s3 put %s: %w", key, err)
	}
	return nil
}

func (s *Store) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := s.cl.GetObject(ctx, &s3.GetObjectInput{Bucket: &s.bucket, Key: &key})
	if err != nil {
		return nil, fmt.Errorf("s3 get %s: %w", key, err)
	}
	return out.Body, nil
}

// PublicURL — прямая ссылка на объект под public/.
func (s *Store) PublicURL(key string) string {
	if key == "" {
		return ""
	}
	return s.publicBase + "/" + key
}

// PresignGet — временная ссылка на приватный объект (готовое видео клиенту).
func (s *Store) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	req, err := s3.NewPresignClient(s.cl).PresignGetObject(ctx,
		&s3.GetObjectInput{Bucket: &s.bucket, Key: &key},
		s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("s3 presign %s: %w", key, err)
	}
	return req.URL, nil
}
