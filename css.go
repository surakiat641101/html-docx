package htmldocx

import (
	"math"
	"strconv"
	"strings"
)

// parseStyleAttr parses an inline style="" attribute into lower-cased
// property names mapped to their values.
func parseStyleAttr(s string) map[string]string {
	if s == "" {
		return nil
	}
	m := make(map[string]string)
	for _, decl := range strings.Split(s, ";") {
		k, v, ok := strings.Cut(decl, ":")
		if !ok {
			continue
		}
		k = strings.ToLower(strings.TrimSpace(k))
		v = strings.TrimSpace(v)
		if i := strings.Index(strings.ToLower(v), "!important"); i >= 0 {
			v = strings.TrimSpace(v[:i])
		}
		if k != "" && v != "" {
			m[k] = v
		}
	}
	return m
}

var namedColors = map[string]string{
	"black": "000000", "white": "FFFFFF", "red": "FF0000", "green": "008000",
	"blue": "0000FF", "yellow": "FFFF00", "orange": "FFA500", "purple": "800080",
	"gray": "808080", "grey": "808080", "silver": "C0C0C0", "maroon": "800000",
	"navy": "000080", "teal": "008080", "olive": "808000", "lime": "00FF00",
	"aqua": "00FFFF", "cyan": "00FFFF", "fuchsia": "FF00FF", "magenta": "FF00FF",
	"pink": "FFC0CB", "brown": "A52A2A", "gold": "FFD700", "darkred": "8B0000",
	"darkgreen": "006400", "darkblue": "00008B", "lightgray": "D3D3D3",
	"lightgrey": "D3D3D3", "darkgray": "A9A9A9", "darkgrey": "A9A9A9",
	"lightblue": "ADD8E6", "lightgreen": "90EE90", "lightyellow": "FFFFE0",
	"indigo": "4B0082", "violet": "EE82EE", "crimson": "DC143C", "coral": "FF7F50",
	"tomato": "FF6347", "salmon": "FA8072", "khaki": "F0E68C", "beige": "F5F5DC",
	"ivory": "FFFFF0", "skyblue": "87CEEB", "steelblue": "4682B4",
	"royalblue": "4169E1", "dodgerblue": "1E90FF", "whitesmoke": "F5F5F5",
	"gainsboro": "DCDCDC", "darkorange": "FF8C00", "orangered": "FF4500",
	"forestgreen": "228B22", "seagreen": "2E8B57", "slategray": "708090",
	"slategrey": "708090", "lavender": "E6E6FA", "turquoise": "40E0D0",
}

// parseColor converts a CSS/HTML color to an upper-case RRGGBB hex string.
func parseColor(v string) (string, bool) {
	v = strings.ToLower(strings.TrimSpace(v))
	switch v {
	case "", "transparent", "inherit", "initial", "unset", "currentcolor", "none", "auto":
		return "", false
	}
	if c, ok := namedColors[v]; ok {
		return c, true
	}
	if strings.HasPrefix(v, "rgb") {
		return parseRGB(v)
	}
	h := strings.TrimPrefix(v, "#")
	switch len(h) {
	case 3, 4:
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	case 6:
	case 8:
		h = h[:6]
	default:
		return "", false
	}
	if _, err := strconv.ParseUint(h, 16, 32); err != nil {
		return "", false
	}
	return strings.ToUpper(h), true
}

func parseRGB(v string) (string, bool) {
	open, end := strings.IndexByte(v, '('), strings.LastIndexByte(v, ')')
	if open < 0 || end < open {
		return "", false
	}
	inner := strings.NewReplacer(",", " ", "/", " ").Replace(v[open+1 : end])
	f := strings.Fields(inner)
	if len(f) < 3 {
		return "", false
	}
	if len(f) >= 4 {
		if a, ok := parseNumberOrPercent(f[3], 1); ok && a == 0 {
			return "", false
		}
	}
	var out [3]byte
	for i := 0; i < 3; i++ {
		n, ok := parseNumberOrPercent(f[i], 255)
		if !ok {
			return "", false
		}
		out[i] = byte(math.Max(0, math.Min(255, math.Round(n))))
	}
	return strings.ToUpper(hexByte(out[0]) + hexByte(out[1]) + hexByte(out[2])), true
}

func parseNumberOrPercent(s string, full float64) (float64, bool) {
	pct := strings.HasSuffix(s, "%")
	n, err := strconv.ParseFloat(strings.TrimSuffix(s, "%"), 64)
	if err != nil {
		return 0, false
	}
	if pct {
		n = n * full / 100
	}
	return n, true
}

func hexByte(b byte) string {
	const digits = "0123456789abcdef"
	return string([]byte{digits[b>>4], digits[b&0xF]})
}

// cssBackground extracts a color from background-color or background.
func cssBackground(css map[string]string) (string, bool) {
	if v, ok := css["background-color"]; ok {
		return parseColor(v)
	}
	if v, ok := css["background"]; ok {
		if c, ok := parseColor(v); ok {
			return c, true
		}
		for _, f := range strings.Fields(v) {
			if c, ok := parseColor(f); ok {
				return c, true
			}
		}
	}
	return "", false
}

// parseLength converts a CSS length to points. em, rem and % are relative to
// basePt.
func parseLength(v string, basePt float64) (float64, bool) {
	v = strings.ToLower(strings.TrimSpace(v))
	units := []struct {
		suffix string
		mul    float64
	}{
		{"px", 0.75}, {"pt", 1}, {"pc", 12}, {"in", 72}, {"cm", 72 / 2.54},
		{"mm", 72 / 25.4}, {"rem", basePt}, {"em", basePt}, {"%", basePt / 100},
	}
	for _, u := range units {
		if strings.HasSuffix(v, u.suffix) {
			n, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(v, u.suffix)), 64)
			if err != nil {
				return 0, false
			}
			return n * u.mul, true
		}
	}
	if n, err := strconv.ParseFloat(v, 64); err == nil && n == 0 {
		return 0, true
	}
	return 0, false
}

var fontSizeKeywords = map[string]float64{
	"xx-small": 7, "x-small": 7.5, "small": 10, "medium": 12,
	"large": 13.5, "x-large": 18, "xx-large": 24, "xxx-large": 36,
}

// parseFontSize converts a CSS font-size to points relative to curPt.
func parseFontSize(v string, curPt float64) (float64, bool) {
	v = strings.ToLower(strings.TrimSpace(v))
	if pt, ok := fontSizeKeywords[v]; ok {
		return pt, true
	}
	switch v {
	case "smaller":
		return curPt * 0.83, true
	case "larger":
		return curPt * 1.2, true
	}
	pt, ok := parseLength(v, curPt)
	return pt, ok && pt > 0
}

// parseDimPx converts an HTML attribute or CSS width/height to CSS pixels.
// Percentages are relative to refPx; unitless numbers are pixels.
func parseDimPx(v string, refPx float64) (float64, bool) {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" || v == "auto" {
		return 0, false
	}
	if strings.HasSuffix(v, "%") {
		n, err := strconv.ParseFloat(strings.TrimSuffix(v, "%"), 64)
		if err != nil || n <= 0 {
			return 0, false
		}
		return refPx * n / 100, true
	}
	if n, err := strconv.ParseFloat(v, 64); err == nil {
		return n, n > 0
	}
	pt, ok := parseLength(v, 12)
	return pt / 0.75, ok && pt > 0
}

// htmlFontSizes maps <font size="1..7"> to points.
var htmlFontSizes = [...]float64{8, 10, 12, 14, 18, 24, 36}

func parseHTMLFontSize(v string) (float64, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, false
	}
	rel := v[0] == '+' || v[0] == '-'
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, false
	}
	if rel {
		n += 3
	}
	n = max(1, min(7, n))
	return htmlFontSizes[n-1], true
}
