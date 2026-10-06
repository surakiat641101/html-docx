package htmldocx

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	_ "image/gif" // register decoders for image.DecodeConfig
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
)

// MaxImageBytes is the largest image the built-in loaders will read.
const MaxImageBytes = 20 << 20

var errNoImageLoader = errors.New("htmldocx: no ImageLoader configured")

func decodeDataURI(src string) ([]byte, error) {
	meta, data, ok := strings.Cut(src[len("data:"):], ",")
	if !ok {
		return nil, errors.New("htmldocx: malformed data URI")
	}
	if !strings.HasSuffix(strings.ToLower(meta), ";base64") {
		s, err := url.PathUnescape(data)
		return []byte(s), err
	}
	data = strings.Map(func(r rune) rune {
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
			return -1
		}
		return r
	}, data)
	b, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		b, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(data, "="))
	}
	return b, err
}

// imageInfo detects the format and pixel size of PNG, JPEG and GIF images.
func imageInfo(b []byte) (ext string, w, h int, err error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return "", 0, 0, fmt.Errorf("htmldocx: unsupported image: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return "", 0, 0, errors.New("htmldocx: empty image")
	}
	return format, cfg.Width, cfg.Height, nil
}

// FileImageLoader loads images from the local file system. Paths are resolved
// relative to baseDir and cannot escape it ("/img/a.png" means
// baseDir/img/a.png). Remote URLs are rejected.
func FileImageLoader(baseDir string) ImageLoader {
	return func(src string) ([]byte, error) {
		if i := strings.Index(src, "://"); i >= 0 && !strings.HasPrefix(strings.ToLower(src), "file://") {
			return nil, fmt.Errorf("htmldocx: not a local file: %s", src)
		}
		p := strings.TrimPrefix(src, "file://")
		if i := strings.IndexAny(p, "?#"); i >= 0 {
			p = p[:i]
		}
		if u, err := url.PathUnescape(p); err == nil {
			p = u
		}
		p = strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(p, `\`, "/")), "/")
		root, err := os.OpenRoot(baseDir)
		if err != nil {
			return nil, err
		}
		defer root.Close()
		f, err := root.Open(p)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		return readLimited(f)
	}
}

// HTTPImageLoader downloads http(s) images with the given client
// (http.DefaultClient when nil). Only use it with trusted HTML: fetching
// arbitrary URLs from user input enables SSRF.
func HTTPImageLoader(client *http.Client) ImageLoader {
	if client == nil {
		client = http.DefaultClient
	}
	return func(src string) ([]byte, error) {
		u, err := url.Parse(src)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return nil, fmt.Errorf("htmldocx: not an http(s) URL: %s", src)
		}
		resp, err := client.Get(src)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("htmldocx: GET %s: %s", src, resp.Status)
		}
		return readLimited(resp.Body)
	}
}

// ChainImageLoaders tries each loader in order and returns the first success.
func ChainImageLoaders(loaders ...ImageLoader) ImageLoader {
	return func(src string) ([]byte, error) {
		errs := make([]error, 0, len(loaders))
		for _, l := range loaders {
			b, err := l(src)
			if err == nil {
				return b, nil
			}
			errs = append(errs, err)
		}
		return nil, errors.Join(errs...)
	}
}

func readLimited(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, MaxImageBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > MaxImageBytes {
		return nil, errors.New("htmldocx: image exceeds MaxImageBytes")
	}
	return b, nil
}
