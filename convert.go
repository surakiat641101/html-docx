// Package htmldocx converts HTML documents to Microsoft Word (.docx) files
// using only Go code (no Word, LibreOffice or Pandoc required).
package htmldocx

import (
	"bytes"
	"cmp"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/net/html"
)

// Convert reads UTF-8 HTML from r and writes a .docx document to w.
func Convert(r io.Reader, w io.Writer, opts *Options) error {
	doc, err := html.Parse(r)
	if err != nil {
		return fmt.Errorf("htmldocx: parse html: %w", err)
	}
	return ConvertNode(doc, w, opts)
}

// ConvertNode converts an already parsed HTML tree. The tree is not modified.
func ConvertNode(doc *html.Node, w io.Writer, opts *Options) error {
	var raw Options
	if opts != nil {
		raw = *opts
	}
	var sheet stylesheet
	sheet.collect(doc)
	sheet.add(raw.CSS)
	sheet.applyPage(&raw) // @page fills in page size/margins not set in Options
	c := newConverter(raw.resolve())
	c.sheet = sheet
	c.scanAnchors(doc)
	c.walk(doc, runStyle{}, blockStyle{})
	c.flush()
	return c.writePackage(w)
}

// ConvertBytes converts HTML bytes and returns the .docx bytes.
func ConvertBytes(src []byte, opts *Options) ([]byte, error) {
	var buf bytes.Buffer
	if err := Convert(bytes.NewReader(src), &buf, opts); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ConvertString converts an HTML string and returns the .docx bytes.
func ConvertString(src string, opts *Options) ([]byte, error) {
	return ConvertBytes([]byte(src), opts)
}

// ConvertFile converts the HTML file at inPath to a .docx file at outPath.
// If opts.ImageLoader is nil, images are loaded relative to the HTML file.
func ConvertFile(inPath, outPath string, opts *Options) error {
	in, err := os.Open(inPath)
	if err != nil {
		return err
	}
	defer in.Close()
	var o Options // not resolved yet, so @page can still supply size and margins
	if opts != nil {
		o = *opts
	}
	if o.ImageLoader == nil {
		o.ImageLoader = FileImageLoader(filepath.Dir(inPath))
	}
	var buf bytes.Buffer
	if err := Convert(in, &buf, &o); err != nil {
		return err
	}
	return os.WriteFile(outPath, buf.Bytes(), 0o644)
}

// tri is a tri-state toggle: inherit from the paragraph style, on, or off.
type tri int8

const (
	inherit tri = iota
	on
	off
)

type runStyle struct {
	bold, italic, underline, strike tri
	underlineStyle                  string // Word w:u value when underline is on; "" = single
	vertAlign                       string
	caps                            bool
	color, shading, highlight, font string
	size                            float64 // points; 0 = inherit
	link                            string  // attributes of <w:hyperlink>
	border                          string  // <w:bdr .../> for a boxed inline element
}

type blockStyle struct {
	style               string
	align               string
	indent              int // twips
	firstLine           int // twips; negative = hanging
	shading             string
	pre, inCell         bool
	item                *listItem
	listNum, listLvl    int // set inside <ul>/<ol> for their <li> children
	listDepth           int
	before, after       int  // paragraph spacing in twips
	beforeSet, afterSet bool // false = use the paragraph style's spacing
	line                int  // line spacing: 240ths of a line (auto) or twips (atLeast)
	lineRule            string
	borders             string // <w:pBdr> content
}

type listItem struct {
	numID, ilvl int
	used        bool // the numbered paragraph has been written
}

type listDef struct {
	format string // Word numFmt
	start  int
}

type paragraph struct {
	bs                    blockStyle
	runs                  bytes.Buffer
	bookmarks             []string
	hasContent, lastSpace bool
}

type relationship struct {
	id, typ, target string
	external        bool
}

type mediaFile struct {
	name string
	data []byte
}

type imageRef struct {
	rid           string
	width, height int
	err           error
}

type converter struct {
	opts      Options
	out       *bytes.Buffer
	para      *paragraph
	textWidth int // twips

	rels     []relationship
	linkRels map[string]string
	media    []mediaFile
	images   map[string]imageRef
	lists    []listDef

	anchors          map[string]bool
	usedBookmarks    map[string]bool
	pendingBookmarks []string
	nextBookmark     int
	nextDocPr        int

	sheet   stylesheet
	preTrim *html.Node
	title   string
}

func newConverter(o Options) *converter {
	w := o.PageSize.WidthMM
	if o.Landscape {
		w = max(w, o.PageSize.HeightMM)
	}
	tw := mmToTwips(w) - mmToTwips(o.Margins.Left) - mmToTwips(o.Margins.Right)
	if tw < 1440 {
		tw = 1440
	}
	return &converter{
		opts:          o,
		out:           &bytes.Buffer{},
		textWidth:     tw,
		linkRels:      map[string]string{},
		images:        map[string]imageRef{},
		anchors:       map[string]bool{},
		usedBookmarks: map[string]bool{},
	}
}

const (
	relStyles    = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles"
	relSettings  = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/settings"
	relNumbering = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/numbering"
	relHyperlink = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/hyperlink"
	relImage     = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/image"

	pageBreakXML = `<w:p><w:r><w:br w:type="page"/></w:r></w:p>`
	hrXML        = `<w:p><w:pPr><w:pBdr><w:bottom w:val="single" w:sz="6" w:space="1" w:color="auto"/></w:pBdr></w:pPr></w:p>`
	brRun        = `<w:r><w:br/></w:r>`
	emptyCellXML = `<w:p><w:pPr><w:spacing w:before="0" w:after="0"/></w:pPr></w:p>`
)

func (c *converter) addRel(typ, target string, external bool) string {
	id := "rId" + strconv.Itoa(len(c.rels)+10) // rId1..9 are reserved for fixed parts
	c.rels = append(c.rels, relationship{id, typ, target, external})
	return id
}

// ---------------------------------------------------------------- tree walk

var blockTags = setOf("address", "article", "aside", "blockquote", "body", "caption",
	"center", "dd", "details", "dialog", "div", "dl", "dt", "fieldset", "figcaption",
	"figure", "footer", "form", "h1", "h2", "h3", "h4", "h5", "h6", "header", "hgroup",
	"hr", "html", "legend", "li", "main", "nav", "ol", "p", "pre", "section", "summary",
	"table", "ul")

var skipTags = setOf("head", "script", "style", "noscript", "template", "iframe", "object",
	"embed", "svg", "math", "canvas", "video", "audio", "select", "option", "button",
	"textarea", "meta", "link", "base", "map", "area", "source", "track", "datalist")

func setOf(items ...string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, s := range items {
		m[s] = true
	}
	return m
}

func (c *converter) walkChildren(n *html.Node, rs runStyle, bs blockStyle) {
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		c.walk(ch, rs, bs)
	}
}

func (c *converter) walk(n *html.Node, rs runStyle, bs blockStyle) {
	switch n.Type {
	case html.DocumentNode:
		c.walkChildren(n, rs, bs)
	case html.TextNode:
		c.text(n, rs, bs)
	case html.ElementNode:
		c.element(n, rs, bs)
	}
}

func (c *converter) element(n *html.Node, rs runStyle, bs blockStyle) {
	tag := strings.ToLower(n.Data)
	if tag == "title" {
		if c.title == "" {
			c.title = strings.TrimSpace(collapseSpace(textContent(n)))
		}
		return
	}
	if tag == "head" {
		c.walkChildren(n, rs, bs) // only to find <title>; other head tags are skipped
		return
	}
	if hasAttr(n, "hidden") {
		return
	}
	css := c.computeStyle(n)
	if strings.EqualFold(css["display"], "none") || strings.EqualFold(css["visibility"], "hidden") {
		return
	}
	// data-docx-text replaces an element (e.g. an <svg> checkbox) with text in the .docx.
	if text, ok := attrValue(n, "data-docx-text"); ok {
		if text != "" {
			c.literal(text, c.applyRunCSS(rs, css), bs)
		}
		return
	}
	if skipTags[tag] {
		return
	}
	c.markBookmark(n, tag)

	block := isBlock(tag, css)
	if breakBefore(css) {
		c.flush()
		c.out.WriteString(pageBreakXML)
	}

	outerRS := rs
	rs, bs = c.tagStyle(tag, n, rs, bs)
	if block && !blockTags[tag] {
		bs = resetBlockBox(bs, true) // e.g. <span style="display:block"> behaves like a <div>
	}
	rs = c.applyRunCSS(rs, css)
	// A white background is invisible on paper and only adds noise in Word.
	if bg, ok := cssBackground(css); ok && bg != "FFFFFF" {
		switch {
		case !block:
			rs.shading = bg
		case tag != "table" && tag != "body" && tag != "html":
			bs.shading = bg
		}
	}
	if block {
		bs = c.applyBlockCSS(bs, css, n, c.curSize(rs))
	} else {
		if b, ok := cssBorderSide(css, ""); ok {
			rs.border = "<w:bdr " + b + "/>"
		} else if s, ok := bottomBorderUnderline(css); ok {
			// Word has no per-side character borders; a bottom border reads as an underline.
			rs.underline, rs.underlineStyle = on, s
		}
		if m, ok := boxSides(css, "margin", c.curSize(rs)); ok[3] && m[3] >= 1 {
			c.gap(outerRS)
		}
		defer func() {
			if m, ok := boxSides(css, "margin", c.curSize(rs)); ok[1] && m[1] >= 1 {
				c.gap(outerRS)
			}
		}()
	}

	switch tag {
	case "br":
		c.lineBreak(bs)
	case "hr":
		c.flush()
		c.out.WriteString(hrXML)
	case "img":
		c.image(n, rs, bs, css)
	case "table":
		c.table(n, rs, bs, css)
	case "ul", "ol":
		c.list(n, tag == "ol", rs, bs, css)
	case "pre":
		c.flush()
		bs.pre = true
		if bs.style == "" {
			bs.style = "HTMLPreformatted"
		}
		c.preTrim = lastTextNode(n)
		c.walkChildren(n, rs, bs)
		c.preTrim = nil
		c.flush()
	case "input":
		c.input(n, rs, bs)
	case "q":
		c.literal("“", rs, bs)
		c.walkChildren(n, rs, bs)
		c.literal("”", rs, bs)
	default:
		if block {
			c.flush()
			c.walkChildren(n, rs, bs)
			c.flush()
		} else {
			c.walkChildren(n, rs, bs)
		}
	}

	if breakAfter(css) {
		c.flush()
		c.out.WriteString(pageBreakXML)
	}
}

// tagStyle applies the default formatting implied by an element's tag.
func (c *converter) tagStyle(tag string, n *html.Node, rs runStyle, bs blockStyle) (runStyle, blockStyle) {
	if blockTags[tag] {
		bs = resetBlockBox(bs, marginlessTags[tag])
	}
	switch tag {
	case "b", "strong", "dt":
		rs.bold = on
	case "i", "em", "cite", "var", "dfn", "address":
		rs.italic = on
	case "u", "ins":
		rs.underline = on
	case "s", "strike", "del":
		rs.strike = on
	case "sup":
		rs.vertAlign = "superscript"
	case "sub":
		rs.vertAlign = "subscript"
	case "code", "kbd", "samp", "tt":
		rs.font = "Courier New"
	case "mark":
		rs.highlight = "yellow"
	case "small":
		rs.size = c.curSize(rs) * 0.83
	case "big":
		rs.size = c.curSize(rs) * 1.2
	case "font":
		if col, ok := parseColor(getAttr(n, "color")); ok {
			rs.color = col
		}
		if face := firstFont(getAttr(n, "face")); face != "" {
			rs.font = face
		}
		if pt, ok := parseHTMLFontSize(getAttr(n, "size")); ok {
			rs.size = pt
		}
	case "a":
		if link := c.linkAttr(getAttr(n, "href")); link != "" {
			rs.link = link
		}
	case "h1", "h2", "h3", "h4", "h5", "h6":
		bs.style = "Heading" + tag[1:]
	case "blockquote", "dd":
		bs.indent += 720
	case "center":
		bs.align = "center"
	case "caption", "figcaption":
		bs.style = "Caption"
		bs.align = "center"
	case "li":
		if bs.listNum > 0 {
			bs.item = &listItem{numID: bs.listNum, ilvl: bs.listLvl}
			bs.listNum = 0
		}
	}
	return rs, bs
}

func (c *converter) curSize(rs runStyle) float64 {
	if rs.size > 0 {
		return rs.size
	}
	return c.opts.FontSize
}

func (c *converter) applyRunCSS(rs runStyle, css map[string]string) runStyle {
	if len(css) == 0 {
		return rs
	}
	if v, ok := css["color"]; ok {
		if col, ok := parseColor(v); ok {
			rs.color = col
		}
	}
	if v, ok := css["font-size"]; ok {
		if pt, ok := parseFontSize(v, c.curSize(rs)); ok {
			rs.size = pt
		}
	}
	if v, ok := css["font-weight"]; ok {
		v = strings.ToLower(v)
		switch v {
		case "bold", "bolder":
			rs.bold = on
		case "normal", "lighter":
			rs.bold = off
		default:
			if n, err := strconv.Atoi(v); err == nil {
				rs.bold = off
				if n >= c.opts.BoldWeight {
					rs.bold = on
				}
			}
		}
	}
	if v, ok := css["font-style"]; ok {
		switch strings.ToLower(v) {
		case "italic", "oblique":
			rs.italic = on
		case "normal":
			rs.italic = off
		}
	}
	for _, k := range []string{"text-decoration", "text-decoration-line", "text-decoration-style"} {
		v, ok := css[k]
		if !ok {
			continue
		}
		for _, f := range strings.Fields(strings.ToLower(v)) {
			switch f {
			case "none":
				rs.underline, rs.strike = off, off
			case "underline":
				rs.underline = on
			case "line-through":
				rs.strike = on
			default:
				if s, ok := wordLineStyles[f]; ok {
					rs.underlineStyle = s
				}
			}
		}
	}
	if v, ok := css["font-family"]; ok {
		if f := firstFont(v); f != "" {
			rs.font = f
		}
	}
	if v, ok := css["vertical-align"]; ok {
		switch strings.ToLower(v) {
		case "super":
			rs.vertAlign = "superscript"
		case "sub":
			rs.vertAlign = "subscript"
		case "baseline":
			rs.vertAlign = ""
		}
	}
	if v, ok := css["text-transform"]; ok {
		rs.caps = strings.EqualFold(v, "uppercase")
	}
	return rs
}

// Block elements whose browser default has no vertical margins. Their
// paragraphs get zero spacing instead of the Normal style's space after.
var marginlessTags = setOf("address", "article", "aside", "body", "caption", "center",
	"dd", "details", "dialog", "div", "dt", "fieldset", "figcaption", "footer", "form",
	"header", "hgroup", "html", "legend", "main", "nav", "section", "summary")

// specialTags have dedicated handling and ignore CSS display changes.
var specialTags = setOf("br", "hr", "img", "input", "table", "tr", "td", "th", "ul", "ol",
	"li", "pre", "html", "body", "q")

func isBlock(tag string, css map[string]string) bool {
	if !specialTags[tag] {
		switch strings.ToLower(strings.TrimSpace(css["display"])) {
		case "block", "flex", "grid", "list-item", "flow-root", "table":
			return true
		case "inline", "inline-block", "inline-flex", "inline-grid", "inline-table", "contents":
			return false
		}
	}
	return blockTags[tag]
}

// resetBlockBox drops the non-inherited box properties (margins, borders)
// of the parent block before a nested block applies its own.
func resetBlockBox(bs blockStyle, marginless bool) blockStyle {
	bs.before, bs.after = 0, 0
	bs.beforeSet, bs.afterSet = marginless, marginless
	bs.borders = ""
	return bs
}

func (c *converter) applyBlockCSS(bs blockStyle, css map[string]string, n *html.Node, fontPt float64) blockStyle {
	if m, ok := boxSides(css, "margin", fontPt); ok[0] || ok[2] {
		if ok[0] {
			bs.before, bs.beforeSet = max(0, int(m[0]*20)), true
		}
		if ok[2] {
			bs.after, bs.afterSet = max(0, int(m[2]*20)), true
		}
	}
	if v, ok := css["line-height"]; ok {
		bs.line, bs.lineRule = parseLineHeight(v, fontPt)
	}
	var borders strings.Builder
	for _, side := range []string{"top", "left", "bottom", "right"} {
		if b, ok := cssBorderSide(css, side); ok {
			borders.WriteString("<w:" + side + " " + b + "/>")
		}
	}
	if borders.Len() > 0 {
		bs.borders = borders.String()
	}

	align, ok := css["text-align"]
	if !ok {
		align = getAttr(n, "align")
	}
	switch strings.ToLower(strings.TrimSpace(align)) {
	case "left", "start":
		bs.align = "left"
	case "center":
		bs.align = "center"
	case "right", "end":
		bs.align = "right"
	case "justify":
		bs.align = "both"
	}
	for _, prop := range []string{"margin", "padding"} {
		if m, ok := boxSides(css, prop, fontPt); ok[3] {
			bs.indent += int(m[3] * 20)
		}
	}
	bs.indent = max(0, min(bs.indent, c.textWidth*3/4))
	if v, ok := css["text-indent"]; ok {
		if pt, ok := parseLength(v, fontPt); ok {
			bs.firstLine = int(pt * 20)
		}
	}
	return bs
}

// firstFont returns the first usable family of a CSS font-family list.
func firstFont(v string) string {
	for _, f := range strings.Split(v, ",") {
		f = strings.Trim(strings.TrimSpace(f), `"'`)
		switch strings.ToLower(f) {
		case "":
			continue
		case "monospace":
			return "Courier New"
		case "serif", "sans-serif", "system-ui", "cursive", "fantasy", "inherit", "initial", "unset":
			return ""
		}
		return f
	}
	return ""
}

func breakBefore(css map[string]string) bool {
	return isPageBreak(css["page-break-before"]) || isPageBreak(css["break-before"])
}

func breakAfter(css map[string]string) bool {
	return isPageBreak(css["page-break-after"]) || isPageBreak(css["break-after"])
}

func isPageBreak(v string) bool {
	switch strings.ToLower(v) {
	case "always", "page", "left", "right":
		return true
	}
	return false
}

// ---------------------------------------------------------------- text

func (c *converter) text(n *html.Node, rs runStyle, bs blockStyle) {
	if bs.pre {
		c.preText(n, rs, bs)
		return
	}
	s := collapseSpace(n.Data)
	if s == "" || (s == " " && (c.para == nil || c.para.lastSpace || !c.para.hasContent)) {
		return
	}
	p := c.ensurePara(bs)
	if p.lastSpace || !p.hasContent {
		s = strings.TrimLeft(s, " ")
	}
	if s == "" {
		return
	}
	c.writeRun(textXML(s), rs)
	p.lastSpace = strings.HasSuffix(s, " ")
}

func (c *converter) preText(n *html.Node, rs runStyle, bs blockStyle) {
	s := strings.ReplaceAll(n.Data, "\r\n", "\n")
	if n == c.preTrim {
		s = strings.TrimSuffix(s, "\n")
	}
	if s == "" {
		return
	}
	c.ensurePara(bs)
	var b strings.Builder
	for i, line := range strings.Split(s, "\n") {
		if i > 0 {
			b.WriteString("<w:br/>")
		}
		for j, part := range strings.Split(line, "\t") {
			if j > 0 {
				b.WriteString("<w:tab/>")
			}
			if part != "" {
				b.WriteString(textXML(part))
			}
		}
	}
	c.writeRun(b.String(), rs)
	c.para.lastSpace = false
}

// literal writes text verbatim (no whitespace collapsing).
func (c *converter) literal(s string, rs runStyle, bs blockStyle) {
	p := c.ensurePara(bs)
	c.writeRun(textXML(s), rs)
	p.lastSpace = false
}

// gap writes one space for the horizontal margin of an inline element,
// unless the line is empty or already ends with a space.
func (c *converter) gap(rs runStyle) {
	p := c.para
	if p == nil || !p.hasContent || p.lastSpace {
		return
	}
	c.writeRun(textXML(" "), rs)
	p.lastSpace = true
}

func (c *converter) lineBreak(bs blockStyle) {
	p := c.ensurePara(bs)
	p.runs.WriteString(brRun)
	p.hasContent = true
	p.lastSpace = true
}

func (c *converter) input(n *html.Node, rs runStyle, bs blockStyle) {
	checked := hasAttr(n, "checked")
	switch strings.ToLower(getAttr(n, "type")) {
	case "checkbox":
		c.literal(pick(checked, "☒", "☐"), rs, bs)
	case "radio":
		c.literal(pick(checked, "◉", "○"), rs, bs)
	case "hidden", "submit", "reset", "button", "image", "file":
	default:
		if v := getAttr(n, "value"); v != "" {
			c.literal(v, rs, bs)
		}
	}
}

func pick(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}

func textXML(s string) string {
	return `<w:t xml:space="preserve">` + esc(s) + `</w:t>`
}

func collapseSpace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\f' {
			if !space {
				b.WriteByte(' ')
				space = true
			}
			continue
		}
		space = false
		b.WriteRune(r)
	}
	return b.String()
}

// ---------------------------------------------------------------- paragraphs & runs

func (c *converter) ensurePara(bs blockStyle) *paragraph {
	if c.para == nil {
		c.para = &paragraph{bs: bs}
	}
	if len(c.pendingBookmarks) > 0 {
		c.para.bookmarks = append(c.para.bookmarks, c.pendingBookmarks...)
		c.pendingBookmarks = nil
	}
	return c.para
}

func (c *converter) writeRun(content string, rs runStyle) {
	p := c.para
	if rs.link != "" {
		p.runs.WriteString("<w:hyperlink " + rs.link + ">")
	}
	p.runs.WriteString("<w:r>")
	writeRPr(&p.runs, rs)
	p.runs.WriteString(content)
	p.runs.WriteString("</w:r>")
	if rs.link != "" {
		p.runs.WriteString("</w:hyperlink>")
	}
	p.hasContent = true
}

func (c *converter) flush() {
	p := c.para
	if p == nil {
		return
	}
	c.para = nil
	if !p.hasContent {
		c.pendingBookmarks = append(p.bookmarks, c.pendingBookmarks...)
		return
	}
	runs := p.runs.Bytes()
	// Browsers ignore a trailing <br> at the end of a block.
	if len(runs) > len(brRun) && bytes.HasSuffix(runs, []byte(brRun)) {
		runs = runs[:len(runs)-len(brRun)]
	}
	b := c.out
	b.WriteString("<w:p>")
	c.writePPr(b, p.bs)
	for _, name := range p.bookmarks {
		c.nextBookmark++
		id := strconv.Itoa(c.nextBookmark)
		b.WriteString(`<w:bookmarkStart w:id="` + id + `" w:name="` + esc(name) + `"/><w:bookmarkEnd w:id="` + id + `"/>`)
	}
	b.Write(runs)
	b.WriteString("</w:p>")
}

func (c *converter) writePPr(b *bytes.Buffer, bs blockStyle) {
	var pp strings.Builder
	style := bs.style
	numbered := false
	if bs.item != nil {
		numbered = !bs.item.used
		bs.item.used = true
		if style == "" {
			style = "ListParagraph"
		}
	}
	if style != "" {
		pp.WriteString(`<w:pStyle w:val="` + style + `"/>`)
	}
	if numbered {
		fmt.Fprintf(&pp, `<w:numPr><w:ilvl w:val="%d"/><w:numId w:val="%d"/></w:numPr>`, bs.item.ilvl, bs.item.numID)
	}
	if bs.borders != "" {
		pp.WriteString("<w:pBdr>" + bs.borders + "</w:pBdr>")
	}
	if bs.shading != "" {
		pp.WriteString(`<w:shd w:val="clear" w:color="auto" w:fill="` + bs.shading + `"/>`)
	}
	if bs.inCell && !strings.HasPrefix(style, "Heading") {
		if !bs.beforeSet {
			bs.before, bs.beforeSet = 0, true
		}
		if !bs.afterSet {
			bs.after, bs.afterSet = 0, true
		}
	}
	if bs.beforeSet || bs.afterSet || bs.line > 0 {
		pp.WriteString("<w:spacing")
		if bs.beforeSet {
			fmt.Fprintf(&pp, ` w:before="%d"`, bs.before)
		}
		if bs.afterSet {
			fmt.Fprintf(&pp, ` w:after="%d"`, bs.after)
		}
		if bs.line > 0 {
			fmt.Fprintf(&pp, ` w:line="%d" w:lineRule="%s"`, bs.line, bs.lineRule)
		}
		pp.WriteString("/>")
	}
	left, first := bs.indent, bs.firstLine
	switch {
	case bs.item != nil && numbered:
		if left > 0 || first != 0 {
			// Direct indentation replaces the numbering's, so restate the hanging indent.
			left += 720 * (bs.item.ilvl + 1)
			first = -360
		}
	case bs.item != nil:
		left += 720 * (bs.item.ilvl + 1)
	}
	if left > 0 || first != 0 {
		fmt.Fprintf(&pp, `<w:ind w:left="%d"`, left)
		if first > 0 {
			fmt.Fprintf(&pp, ` w:firstLine="%d"`, first)
		} else if first < 0 {
			fmt.Fprintf(&pp, ` w:hanging="%d"`, -first)
		}
		pp.WriteString("/>")
	}
	if bs.align != "" {
		pp.WriteString(`<w:jc w:val="` + bs.align + `"/>`)
	}
	if pp.Len() > 0 {
		b.WriteString("<w:pPr>" + pp.String() + "</w:pPr>")
	}
}

func writeRPr(b *bytes.Buffer, rs runStyle) {
	var r strings.Builder
	if rs.link != "" {
		r.WriteString(`<w:rStyle w:val="Hyperlink"/>`)
	}
	if rs.font != "" {
		f := esc(rs.font)
		r.WriteString(`<w:rFonts w:ascii="` + f + `" w:hAnsi="` + f + `" w:eastAsia="` + f + `" w:cs="` + f + `"/>`)
	}
	writeToggle(&r, "b", rs.bold)
	writeToggle(&r, "bCs", rs.bold)
	writeToggle(&r, "i", rs.italic)
	writeToggle(&r, "iCs", rs.italic)
	if rs.caps {
		r.WriteString("<w:caps/>")
	}
	writeToggle(&r, "strike", rs.strike)
	if rs.color != "" {
		r.WriteString(`<w:color w:val="` + rs.color + `"/>`)
	}
	if rs.size > 0 {
		hp := max(2, int(math.Round(rs.size*2)))
		fmt.Fprintf(&r, `<w:sz w:val="%d"/><w:szCs w:val="%d"/>`, hp, hp)
	}
	if rs.highlight != "" {
		r.WriteString(`<w:highlight w:val="` + rs.highlight + `"/>`)
	}
	switch rs.underline {
	case on:
		r.WriteString(`<w:u w:val="` + cmp.Or(rs.underlineStyle, "single") + `"/>`)
	case off:
		r.WriteString(`<w:u w:val="none"/>`)
	}
	r.WriteString(rs.border)
	if rs.shading != "" {
		r.WriteString(`<w:shd w:val="clear" w:color="auto" w:fill="` + rs.shading + `"/>`)
	}
	if rs.vertAlign != "" {
		r.WriteString(`<w:vertAlign w:val="` + rs.vertAlign + `"/>`)
	}
	if r.Len() > 0 {
		b.WriteString("<w:rPr>" + r.String() + "</w:rPr>")
	}
}

func writeToggle(b *strings.Builder, tag string, t tri) {
	switch t {
	case on:
		b.WriteString("<w:" + tag + "/>")
	case off:
		b.WriteString("<w:" + tag + ` w:val="0"/>`)
	}
}

// ---------------------------------------------------------------- links & bookmarks

func (c *converter) scanAnchors(n *html.Node) {
	if n.Type == html.ElementNode && strings.EqualFold(n.Data, "a") {
		if href := strings.TrimSpace(getAttr(n, "href")); strings.HasPrefix(href, "#") && len(href) > 1 {
			c.anchors[anchorName(href[1:])] = true
		}
	}
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		c.scanAnchors(ch)
	}
}

// markBookmark queues a bookmark for elements that are internal link targets.
func (c *converter) markBookmark(n *html.Node, tag string) {
	ids := []string{getAttr(n, "id")}
	if tag == "a" {
		ids = append(ids, getAttr(n, "name"))
	}
	for _, id := range ids {
		if id == "" {
			continue
		}
		name := anchorName(id)
		if c.anchors[name] && !c.usedBookmarks[name] {
			c.usedBookmarks[name] = true
			c.pendingBookmarks = append(c.pendingBookmarks, name)
		}
	}
}

// anchorName converts an HTML id into a valid Word bookmark name.
func anchorName(id string) string {
	if u, err := url.PathUnescape(id); err == nil {
		id = u
	}
	var b strings.Builder
	for _, r := range id {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	name := b.String()
	if r := []rune(name); len(r) == 0 || !unicode.IsLetter(r[0]) {
		name = "a" + name
	}
	if r := []rune(name); len(r) > 40 {
		name = string(r[:40])
	}
	return name
}

func (c *converter) linkAttr(href string) string {
	href = strings.TrimSpace(href)
	if href == "" {
		return ""
	}
	if strings.HasPrefix(href, "#") {
		if len(href) == 1 {
			return ""
		}
		return `w:anchor="` + esc(anchorName(href[1:])) + `"`
	}
	u, err := url.Parse(href)
	if err != nil {
		return ""
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "mailto", "tel", "ftp":
	default:
		return ""
	}
	id, ok := c.linkRels[href]
	if !ok {
		id = c.addRel(relHyperlink, href, true)
		c.linkRels[href] = id
	}
	return `r:id="` + id + `" w:history="1"`
}

// ---------------------------------------------------------------- lists

func (c *converter) list(n *html.Node, ordered bool, rs runStyle, bs blockStyle, css map[string]string) {
	c.flush()
	ilvl := min(bs.listDepth, 8)
	start := 1
	if ordered {
		if v, err := strconv.Atoi(strings.TrimSpace(getAttr(n, "start"))); err == nil {
			start = max(0, v)
		}
	}
	c.lists = append(c.lists, listDef{format: listFormat(ordered, getAttr(n, "type"), css), start: start})
	bs.item = nil
	bs.listNum = len(c.lists)
	bs.listLvl = ilvl
	bs.listDepth = ilvl + 1
	c.walkChildren(n, rs, bs)
	c.flush()
}

func listFormat(ordered bool, typ string, css map[string]string) string {
	v := css["list-style-type"]
	if v == "" {
		v = css["list-style"]
	}
	for _, f := range strings.Fields(strings.ToLower(v)) {
		switch f {
		case "none":
			return "none"
		case "disc", "circle", "square":
			return "bullet"
		case "decimal":
			return "decimal"
		case "decimal-leading-zero":
			return "decimalZero"
		case "lower-alpha", "lower-latin":
			return "lowerLetter"
		case "upper-alpha", "upper-latin":
			return "upperLetter"
		case "lower-roman":
			return "lowerRoman"
		case "upper-roman":
			return "upperRoman"
		case "thai":
			return "thaiNumbers"
		}
	}
	if !ordered {
		return "bullet"
	}
	switch strings.TrimSpace(typ) {
	case "a":
		return "lowerLetter"
	case "A":
		return "upperLetter"
	case "i":
		return "lowerRoman"
	case "I":
		return "upperRoman"
	}
	return "decimal"
}

// ---------------------------------------------------------------- images

func (c *converter) image(n *html.Node, rs runStyle, bs blockStyle, css map[string]string) {
	src := strings.TrimSpace(getAttr(n, "src"))
	ref, ok := c.images[src]
	if !ok {
		ref = c.loadImage(src)
		c.images[src] = ref
	}
	if ref.err != nil {
		if alt := strings.TrimSpace(getAttr(n, "alt")); alt != "" {
			c.literal("["+alt+"]", rs, bs)
		}
		return
	}

	maxPx := float64(c.textWidth) / 15 // twips -> CSS px at 96 dpi
	natW, natH := float64(ref.width), float64(ref.height)
	w, wok := dimension(n, css, "width", maxPx)
	h, hok := dimension(n, css, "height", natH)
	switch {
	case wok && hok:
	case wok:
		h = w * natH / natW
	case hok:
		w = h * natW / natH
	default:
		w, h = natW, natH
	}
	if w > maxPx {
		h = h * maxPx / w
		w = maxPx
	}
	cx, cy := int64(math.Max(1, w)*9525), int64(math.Max(1, h)*9525)

	c.nextDocPr++
	alt := esc(getAttr(n, "alt"))
	rs.link = "" // keep drawings outside hyperlinks for compatibility
	p := c.ensurePara(bs)
	c.writeRun(fmt.Sprintf(`<w:drawing><wp:inline distT="0" distB="0" distL="0" distR="0">`+
		`<wp:extent cx="%[1]d" cy="%[2]d"/><wp:docPr id="%[3]d" name="Picture %[3]d" descr="%[4]s"/>`+
		`<wp:cNvGraphicFramePr><a:graphicFrameLocks noChangeAspect="1"/></wp:cNvGraphicFramePr>`+
		`<a:graphic><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/picture">`+
		`<pic:pic><pic:nvPicPr><pic:cNvPr id="%[3]d" name="Picture %[3]d" descr="%[4]s"/><pic:cNvPicPr/></pic:nvPicPr>`+
		`<pic:blipFill><a:blip r:embed="%[5]s"/><a:stretch><a:fillRect/></a:stretch></pic:blipFill>`+
		`<pic:spPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="%[1]d" cy="%[2]d"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom></pic:spPr>`+
		`</pic:pic></a:graphicData></a:graphic></wp:inline></w:drawing>`,
		cx, cy, c.nextDocPr, alt, ref.rid), rs)
	p.lastSpace = false
}

func (c *converter) loadImage(src string) imageRef {
	if src == "" {
		return imageRef{err: fmt.Errorf("htmldocx: empty image src")}
	}
	var data []byte
	var err error
	switch {
	case strings.HasPrefix(strings.ToLower(src), "data:"):
		data, err = decodeDataURI(src)
	case c.opts.ImageLoader != nil:
		data, err = c.opts.ImageLoader(src)
	default:
		err = errNoImageLoader
	}
	if err != nil {
		return imageRef{err: err}
	}
	ext, w, h, err := imageInfo(data)
	if err != nil {
		return imageRef{err: err}
	}
	name := fmt.Sprintf("image%d.%s", len(c.media)+1, ext)
	c.media = append(c.media, mediaFile{name: name, data: data})
	return imageRef{rid: c.addRel(relImage, "media/"+name, false), width: w, height: h}
}

func dimension(n *html.Node, css map[string]string, prop string, refPx float64) (float64, bool) {
	if v, ok := css[prop]; ok {
		if px, ok := parseDimPx(v, refPx); ok {
			return px, true
		}
	}
	return parseDimPx(getAttr(n, prop), refPx)
}

// ---------------------------------------------------------------- tables

type gridCell struct {
	n                          *html.Node
	row, col, colspan, rowspan int
}

func (c *converter) table(n *html.Node, rs runStyle, bs blockStyle, css map[string]string) {
	c.flush()

	var rows []*html.Node
	var caption *html.Node
	var collect func(p *html.Node)
	collect = func(p *html.Node) {
		for ch := p.FirstChild; ch != nil; ch = ch.NextSibling {
			if ch.Type != html.ElementNode {
				continue
			}
			switch strings.ToLower(ch.Data) {
			case "tr":
				rows = append(rows, ch)
			case "thead", "tbody", "tfoot":
				collect(ch)
			case "caption":
				if caption == nil {
					caption = ch
				}
			}
		}
	}
	collect(n)

	if caption != nil {
		c.element(caption, rs, bs)
	}

	// Lay out cells on a grid so rowspan/colspan become vMerge/gridSpan.
	grid := make([][]*gridCell, len(rows))
	ncols := 0
	for r, tr := range rows {
		col := 0
		for td := tr.FirstChild; td != nil; td = td.NextSibling {
			if td.Type != html.ElementNode {
				continue
			}
			if t := strings.ToLower(td.Data); t != "td" && t != "th" {
				continue
			}
			for col < len(grid[r]) && grid[r][col] != nil {
				col++
			}
			cs := spanAttr(td, "colspan", 63)
			for k := 1; k < cs; k++ { // shrink spans that collide with rowspans from above
				if col+k < len(grid[r]) && grid[r][col+k] != nil {
					cs = k
					break
				}
			}
			rsp := min(spanAttr(td, "rowspan", 1<<16), len(rows)-r)
			cell := &gridCell{n: td, row: r, col: col, colspan: cs, rowspan: rsp}
			for rr := r; rr < r+rsp; rr++ {
				for len(grid[rr]) < col+cs {
					grid[rr] = append(grid[rr], nil)
				}
				for cc := col; cc < col+cs; cc++ {
					if grid[rr][cc] == nil {
						grid[rr][cc] = cell
					}
				}
			}
			col += cs
			ncols = max(ncols, col)
		}
	}
	if ncols == 0 {
		return
	}

	colW := max(100, c.textWidth/ncols)
	b := c.out
	b.WriteString(`<w:tbl><w:tblPr><w:tblW w:w="5000" w:type="pct"/>`)
	if hasBorders(n, css) {
		b.WriteString(`<w:tblBorders>`)
		for _, side := range []string{"top", "left", "bottom", "right", "insideH", "insideV"} {
			b.WriteString(`<w:` + side + ` w:val="single" w:sz="4" w:space="0" w:color="auto"/>`)
		}
		b.WriteString(`</w:tblBorders>`)
	}
	b.WriteString(`<w:tblLayout w:type="autofit"/><w:tblCellMar><w:top w:w="40" w:type="dxa"/>` +
		`<w:left w:w="100" w:type="dxa"/><w:bottom w:w="40" w:type="dxa"/><w:right w:w="100" w:type="dxa"/>` +
		`</w:tblCellMar></w:tblPr><w:tblGrid>`)
	for i := 0; i < ncols; i++ {
		fmt.Fprintf(b, `<w:gridCol w:w="%d"/>`, colW)
	}
	b.WriteString(`</w:tblGrid>`)

	for r, tr := range rows {
		trCSS := c.computeStyle(tr)
		rowBg, _ := cssBackground(trCSS)
		if rowBg == "" {
			rowBg, _ = parseColor(getAttr(tr, "bgcolor"))
		}
		rowRS := c.applyRunCSS(rs, trCSS)
		rowBS := c.applyBlockCSS(blockStyle{inCell: true}, trCSS, tr, c.curSize(rowRS))

		b.WriteString("<w:tr>")
		if tr.Parent != nil && strings.EqualFold(tr.Parent.Data, "thead") {
			b.WriteString("<w:trPr><w:tblHeader/></w:trPr>")
		}
		for col := 0; col < ncols; {
			var cell *gridCell
			if col < len(grid[r]) {
				cell = grid[r][col]
			}
			switch {
			case cell == nil || cell.col != col:
				fmt.Fprintf(b, `<w:tc><w:tcPr><w:tcW w:w="%d" w:type="dxa"/></w:tcPr>%s</w:tc>`, colW, emptyCellXML)
				col++
			case cell.row == r:
				c.cell(cell, rowRS, rowBS, rowBg, colW)
				col += cell.colspan
			default: // covered by a rowspan from above
				fmt.Fprintf(b, `<w:tc><w:tcPr><w:tcW w:w="%d" w:type="dxa"/>%s<w:vMerge/></w:tcPr>%s</w:tc>`,
					colW*cell.colspan, gridSpanXML(cell.colspan), emptyCellXML)
				col += cell.colspan
			}
		}
		b.WriteString("</w:tr>")
	}
	b.WriteString("</w:tbl>")
}

func (c *converter) cell(cell *gridCell, rs runStyle, bs blockStyle, rowBg string, colW int) {
	n := cell.n
	header := strings.EqualFold(n.Data, "th")
	css := c.computeStyle(n)
	if header {
		rs.bold = on
		bs.align = "center"
	}
	rs = c.applyRunCSS(rs, css)
	bs = c.applyBlockCSS(bs, css, n, c.curSize(rs))

	fill, ok := cssBackground(css)
	if !ok {
		fill, ok = parseColor(getAttr(n, "bgcolor"))
	}
	switch {
	case ok:
	case rowBg != "":
		fill = rowBg
	case header:
		fill = "F2F2F2"
	}
	valign := css["vertical-align"]
	if valign == "" {
		valign = getAttr(n, "valign")
	}

	// Render the cell content into its own buffer.
	savedOut, savedPara := c.out, c.para
	c.out, c.para = &bytes.Buffer{}, nil
	c.walkChildren(n, rs, bs)
	c.flush()
	content := c.out.Bytes()
	c.out, c.para = savedOut, savedPara

	b := c.out
	fmt.Fprintf(b, `<w:tc><w:tcPr><w:tcW w:w="%d" w:type="dxa"/>%s`, colW*cell.colspan, gridSpanXML(cell.colspan))
	if cell.rowspan > 1 {
		b.WriteString(`<w:vMerge w:val="restart"/>`)
	}
	if fill != "" {
		b.WriteString(`<w:shd w:val="clear" w:color="auto" w:fill="` + fill + `"/>`)
	}
	switch strings.ToLower(strings.TrimSpace(valign)) {
	case "middle", "center":
		b.WriteString(`<w:vAlign w:val="center"/>`)
	case "bottom":
		b.WriteString(`<w:vAlign w:val="bottom"/>`)
	}
	b.WriteString("</w:tcPr>")
	b.Write(content)
	if !bytes.HasSuffix(content, []byte("</w:p>")) { // a cell must end with a paragraph
		b.WriteString(emptyCellXML)
	}
	b.WriteString("</w:tc>")
}

func gridSpanXML(n int) string {
	if n <= 1 {
		return ""
	}
	return fmt.Sprintf(`<w:gridSpan w:val="%d"/>`, n)
}

func spanAttr(n *html.Node, name string, limit int) int {
	v, err := strconv.Atoi(strings.TrimSpace(getAttr(n, name)))
	if err != nil || v < 1 {
		return 1
	}
	return min(v, limit)
}

func hasBorders(n *html.Node, css map[string]string) bool {
	if strings.TrimSpace(getAttr(n, "border")) == "0" {
		return false
	}
	for _, k := range []string{"border", "border-style"} {
		switch strings.ToLower(strings.TrimSpace(css[k])) {
		case "none", "0", "hidden":
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------- helpers

func getAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Namespace == "" && strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}

func attrValue(n *html.Node, key string) (string, bool) {
	for _, a := range n.Attr {
		if a.Namespace == "" && strings.EqualFold(a.Key, key) {
			return a.Val, true
		}
	}
	return "", false
}

func hasAttr(n *html.Node, key string) bool {
	for _, a := range n.Attr {
		if a.Namespace == "" && strings.EqualFold(a.Key, key) {
			return true
		}
	}
	return false
}

func textContent(n *html.Node) string {
	var b strings.Builder
	var rec func(*html.Node)
	rec = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			rec(ch)
		}
	}
	rec(n)
	return b.String()
}

func lastTextNode(n *html.Node) *html.Node {
	for ch := n.LastChild; ch != nil; ch = ch.PrevSibling {
		if ch.Type == html.TextNode {
			return ch
		}
		if ch.Type == html.ElementNode {
			if t := lastTextNode(ch); t != nil {
				return t
			}
		}
	}
	return nil
}
