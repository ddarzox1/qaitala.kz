package model

import "time"

// Статусы заказа. Переходы:
// draft → awaiting_payment → paid → generating → done | failed
// failed → generating (бесплатный повтор) | refund_requested
const (
	StatusDraft      = "draft"
	StatusAwaitPay   = "awaiting_payment"
	StatusPaid       = "paid"
	StatusGenerating = "generating"
	StatusDone       = "done"
	StatusFailed     = "failed"
	StatusExpired    = "expired"
	StatusRefundReq  = "refund_requested"
)

var Resolutions = []string{"480p", "720p", "1080p"}

type Template struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	DescRu       string   `json:"-"`
	DescKz       string   `json:"-"`
	Desc         string   `json:"desc"`
	ClipURL      string   `json:"clip_url"`
	PosterURL    string   `json:"poster_url"`
	DurationSec  float64  `json:"duration_sec"`
	AspectW      int      `json:"aspect_w"`
	AspectH      int      `json:"aspect_h"`
	RefPhotos    int      `json:"ref_photos"`
	SlotLabelsRu []string `json:"-"`
	SlotLabelsKz []string `json:"-"`
	SlotLabels   []string `json:"slot_labels"`
	Prompt       string   `json:"-"`
	Price480     *int     `json:"price_480p"`
	Price720     *int     `json:"price_720p"`
	Price1080    *int     `json:"price_1080p"`
	Popular      bool     `json:"popular"`
	Sort         int      `json:"-"`
}

// Localize заполняет поля, зависящие от языка. Клиенту уходит только нужный язык.
func (t *Template) Localize(lang string) {
	if lang == "kz" {
		t.Desc, t.SlotLabels = t.DescKz, t.SlotLabelsKz
		return
	}
	t.Desc, t.SlotLabels = t.DescRu, t.SlotLabelsRu
}

// PriceFor возвращает цену качества или nil, если оно не продаётся.
func (t *Template) PriceFor(resolution string) *int {
	switch resolution {
	case "480p":
		return t.Price480
	case "720p":
		return t.Price720
	case "1080p":
		return t.Price1080
	}
	return nil
}

type Order struct {
	Token          string
	TemplateID     string
	Resolution     string
	Lang           string
	Email          string
	Status         string
	PhotoKeys      []string
	AmountKZT      int
	QROperationID  *int64
	PayURL         string
	QRExpiresAt    *time.Time
	KaspiPaymentID string
	HFRequestID    string
	VideoKey       string
	RetriesUsed    int
	Attempts       int
	Error          string
	CreatedAt      time.Time
	PaidAt         *time.Time
	DoneAt         *time.Time
}
