package fiberdocx

import (
	"archive/zip"
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func newApp() *fiber.App {
	app := fiber.New()
	app.Post("/convert", New())
	app.Get("/report", func(c *fiber.Ctx) error {
		return Send(c, "<h1>รายงาน</h1>", "รายงาน.html", nil)
	})
	return app
}

func checkDocx(t *testing.T, app *fiber.App, req *http.Request, wantName string) {
	t.Helper()
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get(fiber.HeaderContentType); ct != ContentType {
		t.Errorf("content type %q", ct)
	}
	if cd := resp.Header.Get(fiber.HeaderContentDisposition); !strings.Contains(cd, wantName) {
		t.Errorf("content disposition %q does not contain %q", cd, wantName)
	}
	if _, err := zip.NewReader(bytes.NewReader(body), int64(len(body))); err != nil {
		t.Errorf("response is not a zip: %v", err)
	}
}

func TestHandlerRawBody(t *testing.T) {
	req := httptest.NewRequest("POST", "/convert?filename=out.html", strings.NewReader("<p>hello</p>"))
	req.Header.Set("Content-Type", "text/html; charset=utf-8")
	checkDocx(t, newApp(), req, `filename="out.docx"`)
}

func TestHandlerJSON(t *testing.T) {
	req := httptest.NewRequest("POST", "/convert", strings.NewReader(`{"html":"<p>สวัสดี</p>","filename":"ไฟล์"}`))
	req.Header.Set("Content-Type", "application/json")
	checkDocx(t, newApp(), req, "filename*=UTF-8''%E0%B9%84%E0%B8%9F%E0%B8%A5%E0%B9%8C.docx")
}

func TestHandlerMultipartFile(t *testing.T) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "page.html")
	fw.Write([]byte("<p>uploaded</p>"))
	mw.Close()
	req := httptest.NewRequest("POST", "/convert", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	checkDocx(t, newApp(), req, `filename="page.docx"`)
}

func TestHandlerEmpty(t *testing.T) {
	req := httptest.NewRequest("POST", "/convert", strings.NewReader("   "))
	resp, err := newApp().Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != fiber.StatusBadRequest {
		t.Errorf("status %d, want 400", resp.StatusCode)
	}
}

func TestSend(t *testing.T) {
	req := httptest.NewRequest("GET", "/report", nil)
	checkDocx(t, newApp(), req, "filename*=UTF-8''%E0%B8%A3")
}

func TestDocxName(t *testing.T) {
	cases := map[string]string{
		"report.html": "report.docx", `..\..\evil.docx`: "evil.docx", "a/b/c": "c.docx",
		"": "document.docx", "x.DOCX": "x.DOCX", "q\"uote": "quote.docx",
	}
	for in, want := range cases {
		if got := docxName(in); got != want {
			t.Errorf("docxName(%q) = %q, want %q", in, got, want)
		}
	}
}
