// Package higgsfield — клиент Genjutsu motion transfer.
package higgsfield

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	apiBase     = "https://api.higgsfield.ai"
	genjutsuURL = apiBase + "/higgsfield/genjutsu/motion-transfer/v1.0"

	StatusQueued     = "queued"
	StatusInProgress = "in_progress"
	StatusCompleted  = "completed"
	StatusFailed     = "failed"
	StatusNSFW       = "nsfw"
	StatusCanceled   = "canceled"
)

// Terminal — дальше статус не изменится, опрашивать больше нечего.
func Terminal(s string) bool {
	switch s {
	case StatusCompleted, StatusFailed, StatusNSFW, StatusCanceled:
		return true
	}
	return false
}

type Client struct {
	keyID  string
	secret string
	http   *http.Client
}

func New(keyID, secret string) *Client {
	return &Client{keyID: keyID, secret: secret, http: &http.Client{Timeout: 60 * time.Second}}
}

func (c *Client) auth(req *http.Request) {
	req.Header.Set("Authorization", "Key "+c.keyID+":"+c.secret)
}

func (c *Client) doJSON(ctx context.Context, method, u string, body any, hdr map[string]string, out any) error {
	var rdr io.Reader = http.NoBody
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("hf marshal: %w", err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		return fmt.Errorf("hf request: %w", err)
	}
	c.auth(req)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("hf %s: %w", u, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("hf %s: %s: %s", u, resp.Status, strings.TrimSpace(buf.String()))
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("hf decode %s: %w", u, err)
	}
	return nil
}

// UploadImage кладёт фото клиента в хранилище Higgsfield и возвращает ссылку,
// пригодную для image_urls. Так наш бакет остаётся приватным.
func (c *Client) UploadImage(ctx context.Context, contentType string, data []byte) (string, error) {
	var up struct {
		UploadURL     string            `json:"upload_url"`
		PublicURL     string            `json:"public_url"`
		UploadHeaders map[string]string `json:"upload_headers"`
	}
	err := c.doJSON(ctx, http.MethodPost, apiBase+"/files/generate-upload-url",
		map[string]string{"content_type": contentType}, nil, &up)
	if err != nil {
		return "", err
	}
	if up.UploadURL == "" || up.PublicURL == "" {
		return "", fmt.Errorf("hf upload: пустой ответ generate-upload-url")
	}

	// PUT идёт в presigned-хранилище: наши ключи туда слать нельзя.
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, up.UploadURL, bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("hf upload request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
	// Подпись покрывает upload_headers (x-amz-tagging и др.): без любого из них S3
	// отвечает SignatureDoesNotMatch.
	for k, v := range up.UploadHeaders {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("hf upload put: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		// Причину (SignatureDoesNotMatch, AccessDenied…) хранилище пишет в теле.
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(io.LimitReader(resp.Body, 2048))
		return "", fmt.Errorf("hf upload put: %s: %s", resp.Status, strings.TrimSpace(buf.String()))
	}
	return up.PublicURL, nil
}

type Submitted struct {
	RequestID string `json:"request_id"`
	Status    string `json:"status"`
	StatusURL string `json:"status_url"`
}

// Submit ставит задачу в очередь Genjutsu. idempotencyKey — токен заказа:
// повторная отправка того же заказа не создаёт второй платной генерации.
func (c *Client) Submit(ctx context.Context, videoURL string, imageURLs []string,
	prompt, resolution, idempotencyKey, webhookURL string) (*Submitted, error) {

	u := genjutsuURL
	if webhookURL != "" {
		u += "?hf_webhook=" + url.QueryEscape(webhookURL)
	}
	body := map[string]any{
		"video_url":  videoURL,
		"image_urls": imageURLs,
		"prompt":     prompt,
		"resolution": resolution,
	}
	var out Submitted
	err := c.doJSON(ctx, http.MethodPost, u, body,
		map[string]string{"Idempotency-Key": idempotencyKey}, &out)
	if err != nil {
		return nil, err
	}
	if out.RequestID == "" {
		return nil, fmt.Errorf("hf submit: пустой request_id")
	}
	return &out, nil
}

type Result struct {
	Status string
	Error  string
	Video  string
}

// Status — единственный источник правды о задаче. Вебхуки Higgsfield не
// подписаны, поэтому любой из них проверяется этим вызовом.
func (c *Client) Status(ctx context.Context, requestID string) (*Result, error) {
	var out struct {
		Status string `json:"status"`
		Error  string `json:"error"`
		Video  struct {
			URL string `json:"url"`
		} `json:"video"`
	}
	err := c.doJSON(ctx, http.MethodGet,
		apiBase+"/requests/"+url.PathEscape(requestID)+"/status", nil, nil, &out)
	if err != nil {
		return nil, err
	}
	return &Result{Status: out.Status, Error: out.Error, Video: out.Video.URL}, nil
}

// Download тянет готовый ролик. Ссылки Higgsfield живут около недели, поэтому
// результат сразу перекладывается в наше хранилище.
func (c *Client) Download(ctx context.Context, videoURL string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, videoURL, nil)
	if err != nil {
		return nil, fmt.Errorf("hf download request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("hf download: %w", err)
	}
	if resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, fmt.Errorf("hf download: %s", resp.Status)
	}
	return resp.Body, nil
}

// Webhook — тело уведомления. Подписи нет: это лишь сигнал перепроверить Status.
type Webhook struct {
	RequestID string `json:"request_id"`
	Status    string `json:"status"`
	Error     string `json:"error"`
}
