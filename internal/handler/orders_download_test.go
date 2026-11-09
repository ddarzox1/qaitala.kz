package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

func TestStreamVideoDownloadForcesAttachment(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/orders/token/download", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	if err := streamVideoDownload(c, strings.NewReader("video bytes")); err != nil {
		t.Fatal(err)
	}

	if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="qaitala-video.mp4"` {
		t.Fatalf("Content-Disposition = %q", got)
	}
	if got := rec.Header().Get(echo.HeaderContentType); got != "application/octet-stream" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := rec.Body.String(); got != "video bytes" {
		t.Fatalf("body = %q", got)
	}
}
