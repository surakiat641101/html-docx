// Package fiberdocx integrates htmldocx with the Fiber web framework.
package fiberdocx

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/gofiber/fiber/v2"

	htmldocx "github.com/surakiat641101/html-docx"
)

// ContentType is the MIME type of .docx files.
const ContentType = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"

// Config configures the handler returned by New.
type Config struct {
	// Options are passed to the converter. Leave ImageLoader nil when the
	// HTML comes from untrusted clients (file/HTTP loaders enable LFI/SSRF).
	Options *htmldocx.Options
	// OptionsFunc, when set, builds Options per request and overrides Options.
	OptionsFunc func(c *fiber.Ctx) *htmldocx.Options
	// DefaultFilename is used when the request names no file. Default "document.docx".
	DefaultFilename string
	// FileField is the multipart field holding an uploaded .html file. Default "file".
	FileField string
	// HTMLField is the form/JSON field holding HTML text. Default "html".
	HTMLField string
	// Inline sends Content-Disposition: inline instead of attachment.
	Inline bool
}

// New returns a handler that converts the HTML in the request to a .docx
// download. Accepted inputs, in order:
//
//   - multipart/form-data: uploaded file in "file" or HTML text in "html"
//   - application/x-www-form-urlencoded: HTML text in "html"
//   - application/json: {"html": "...", "filename": "report.docx"}
//   - anything else: the raw request body is the HTML
//
// The output file name comes from ?filename=, a "filename" field, the
// uploaded file's name, or Config.DefaultFilename.
func New(config ...Config) fiber.Handler {
	var cfg Config
	if len(config) > 0 {
		cfg = config[0]
	}
	if cfg.DefaultFilename == "" {
		cfg.DefaultFilename = "document.docx"
	}
	if cfg.FileField == "" {
		cfg.FileField = "file"
	}
	if cfg.HTMLField == "" {
		cfg.HTMLField = "html"
	}
	return func(c *fiber.Ctx) error {
		src, name, err := readHTML(c, cfg)
		if err != nil {
			return err
		}
		if len(bytes.TrimSpace(src)) == 0 {
			return fiber.NewError(fiber.StatusBadRequest, "no HTML content provided")
		}
		if name == "" {
			name = cfg.DefaultFilename
		}
		opts := cfg.Options
		if cfg.OptionsFunc != nil {
			opts = cfg.OptionsFunc(c)
		}
		return send(c, src, name, opts, cfg.Inline)
	}
}

func readHTML(c *fiber.Ctx, cfg Config) (src []byte, filename string, err error) {
	filename = c.Query("filename")
	ct := strings.ToLower(c.Get(fiber.HeaderContentType))
	switch {
	case strings.HasPrefix(ct, fiber.MIMEMultipartForm):
		if fh, ferr := c.FormFile(cfg.FileField); ferr == nil {
			f, err := fh.Open()
			if err != nil {
				return nil, "", fiber.NewError(fiber.StatusBadRequest, "cannot read uploaded file")
			}
			defer f.Close()
			if src, err = io.ReadAll(f); err != nil {
				return nil, "", fiber.NewError(fiber.StatusBadRequest, "cannot read uploaded file")
			}
			if filename == "" {
				filename = fh.Filename
			}
		} else {
			src = []byte(c.FormValue(cfg.HTMLField))
		}
		if filename == "" {
			filename = c.FormValue("filename")
		}
	case strings.HasPrefix(ct, fiber.MIMEApplicationForm):
		src = []byte(c.FormValue(cfg.HTMLField))
		if filename == "" {
			filename = c.FormValue("filename")
		}
	case strings.HasPrefix(ct, fiber.MIMEApplicationJSON):
		var body map[string]any
		if err := json.Unmarshal(c.Body(), &body); err != nil {
			return nil, "", fiber.NewError(fiber.StatusBadRequest, "invalid JSON body")
		}
		s, _ := body[cfg.HTMLField].(string)
		src = []byte(s)
		if fn, _ := body["filename"].(string); filename == "" {
			filename = fn
		}
	default:
		src = c.Body()
	}
	return src, filename, nil
}

// Send converts html and writes it to the response as a .docx attachment.
func Send(c *fiber.Ctx, html, filename string, opts *htmldocx.Options) error {
	return send(c, []byte(html), filename, opts, false)
}

// SendBytes is like Send but takes the HTML as bytes.
func SendBytes(c *fiber.Ctx, html []byte, filename string, opts *htmldocx.Options) error {
	return send(c, html, filename, opts, false)
}

// Render renders a template with the app's configured view engine
// (fiber.Config.Views) and sends the result as a .docx attachment.
func Render(c *fiber.Ctx, name string, bind any, filename string, opts *htmldocx.Options, layouts ...string) error {
	views := c.App().Config().Views
	if views == nil {
		return errors.New("fiberdocx: no view engine configured (fiber.Config.Views)")
	}
	var buf bytes.Buffer
	if err := views.Render(&buf, name, bind, layouts...); err != nil {
		return fmt.Errorf("fiberdocx: render %q: %w", name, err)
	}
	return send(c, buf.Bytes(), filename, opts, false)
}

func send(c *fiber.Ctx, src []byte, filename string, opts *htmldocx.Options, inline bool) error {
	out, err := htmldocx.ConvertBytes(src, opts)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	disposition := "attachment"
	if inline {
		disposition = "inline"
	}
	c.Set(fiber.HeaderContentType, ContentType)
	c.Set(fiber.HeaderContentDisposition, contentDisposition(disposition, docxName(filename)))
	return c.Send(out)
}

// docxName strips directories and control characters and forces a .docx extension.
func docxName(name string) string {
	name = path.Base(strings.ReplaceAll(name, `\`, "/"))
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7F || r == '"' {
			return -1
		}
		return r
	}, strings.TrimSpace(name))
	if name == "" || name == "." || name == "/" {
		return "document.docx"
	}
	if ext := path.Ext(name); ext != "" && !strings.EqualFold(ext, ".docx") {
		name = strings.TrimSuffix(name, ext)
	}
	if !strings.HasSuffix(strings.ToLower(name), ".docx") {
		name += ".docx"
	}
	return name
}

// contentDisposition builds a header with an ASCII fallback and an RFC 5987
// UTF-8 filename so non-ASCII (e.g. Thai) names survive.
func contentDisposition(kind, name string) string {
	ascii := strings.Map(func(r rune) rune {
		if r > 0x7E || r == '\\' {
			return '_'
		}
		return r
	}, name)
	const attrChars = "!#$&+-.^_`|~"
	var enc strings.Builder
	for _, b := range []byte(name) {
		if b < 0x80 && (b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || strings.IndexByte(attrChars, b) >= 0) {
			enc.WriteByte(b)
		} else {
			fmt.Fprintf(&enc, "%%%02X", b)
		}
	}
	return fmt.Sprintf(`%s; filename="%s"; filename*=UTF-8''%s`, kind, ascii, enc.String())
}
