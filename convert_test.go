package htmldocx

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// unpack converts html and returns every part of the package, checking that
// all XML parts are well-formed.
func unpack(t *testing.T, src string, opts *Options) map[string]string {
	t.Helper()
	out, err := ConvertString(src, opts)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatal(err)
	}
	parts := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		parts[f.Name] = string(b)
		if strings.HasSuffix(f.Name, ".xml") || strings.HasSuffix(f.Name, ".rels") {
			dec := xml.NewDecoder(bytes.NewReader(b))
			for {
				if _, err := dec.Token(); err == io.EOF {
					break
				} else if err != nil {
					t.Fatalf("%s is not well-formed: %v\n%s", f.Name, err, b)
				}
			}
		}
	}
	for _, name := range []string{"[Content_Types].xml", "_rels/.rels", "word/document.xml", "word/styles.xml", "word/_rels/document.xml.rels"} {
		if _, ok := parts[name]; !ok {
			t.Fatalf("missing part %s", name)
		}
	}
	return parts
}

func mustContain(t *testing.T, s string, subs ...string) {
	t.Helper()
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			t.Errorf("expected to find %q in:\n%s", sub, s)
		}
	}
}

func TestBasicFormatting(t *testing.T) {
	parts := unpack(t, `<html><head><title>รายงาน</title><style>p{color:red}</style></head><body>
		<h1>หัวข้อหลัก</h1>
		<p style="text-align:center">สวัสดี <b>ตัวหนา</b> <i>เอียง</i> <u>ขีดเส้น</u> <s>ขีดฆ่า</s>
		H<sub>2</sub>O x<sup>2</sup> <span style="color:#f00;font-size:16pt;background-color:yellow">สี</span></p>
		<p>line1<br>line2</p>
		<pre>  code
	tabbed
</pre>
		<hr>
		<p style="page-break-before:always">หน้าใหม่ &amp; &lt;escaped&gt;</p>
		<script>alert(1)</script>
	</body></html>`, nil)
	doc := parts["word/document.xml"]
	mustContain(t, doc,
		`<w:pStyle w:val="Heading1"/>`, `หัวข้อหลัก`, `<w:jc w:val="center"/>`,
		`<w:b/><w:bCs/>`, `<w:i/><w:iCs/>`, `<w:u w:val="single"/>`, `<w:strike/>`,
		`<w:vertAlign w:val="subscript"/>`, `<w:vertAlign w:val="superscript"/>`,
		`<w:color w:val="FF0000"/>`, `<w:sz w:val="32"/>`, `<w:shd w:val="clear" w:color="auto" w:fill="FFFF00"/>`,
		`<w:br/>`, `<w:pStyle w:val="HTMLPreformatted"/>`, `<w:tab/>`, `xml:space="preserve">  code</w:t>`,
		`<w:pBdr>`, `<w:br w:type="page"/>`, `&amp; &lt;escaped&gt;`)
	if strings.Contains(doc, "alert") {
		t.Error("script content leaked into document")
	}
	if strings.Contains(doc, "p{color") {
		t.Error("style content leaked into document")
	}
	mustContain(t, parts["docProps/core.xml"], "<dc:title>รายงาน</dc:title>")
	mustContain(t, parts["word/styles.xml"], `w:cs="Tahoma"`, `w:bidi="th-TH"`)
}

func TestWhitespaceCollapsing(t *testing.T) {
	doc := unpack(t, "<p>\n   hello    \n  <b>world</b>  !</p>\n\n<p>   </p>", nil)["word/document.xml"]
	mustContain(t, doc, `>hello </w:t>`, `>world</w:t>`, `> !</w:t>`)
	if n := strings.Count(doc, "<w:p>"); n != 1 {
		t.Errorf("expected 1 paragraph, got %d", n)
	}
}

func TestLists(t *testing.T) {
	parts := unpack(t, `<ul><li>หนึ่ง<ul><li>ซ้อน</li></ul>ต่อ</li><li>สอง</li></ul>
		<ol start="3" type="a"><li>three</li></ol>`, nil)
	doc, num := parts["word/document.xml"], parts["word/numbering.xml"]
	mustContain(t, doc,
		`<w:numPr><w:ilvl w:val="0"/><w:numId w:val="1"/></w:numPr>`,
		`<w:numPr><w:ilvl w:val="1"/><w:numId w:val="2"/></w:numPr>`,
		`<w:numPr><w:ilvl w:val="0"/><w:numId w:val="3"/></w:numPr>`,
		`<w:ind w:left="720"/>`) // continuation text "ต่อ"
	mustContain(t, num, `<w:numFmt w:val="bullet"/>`, `<w:numFmt w:val="lowerLetter"/>`, `<w:start w:val="3"/>`)
	if strings.Count(doc, "<w:numPr>") != 4 {
		t.Errorf("expected 4 numbered paragraphs:\n%s", doc)
	}
	mustContain(t, parts["[Content_Types].xml"], "numbering+xml")
}

func TestTable(t *testing.T) {
	doc := unpack(t, `<table>
		<thead><tr><th>A</th><th colspan="2">B</th></tr></thead>
		<tbody>
			<tr><td rowspan="2">r</td><td style="background:#eee">1</td><td></td></tr>
			<tr><td>2</td><td align="right">3</td></tr>
		</tbody></table>`, nil)["word/document.xml"]
	mustContain(t, doc, `<w:tblHeader/>`, `<w:gridSpan w:val="2"/>`, `<w:vMerge w:val="restart"/>`,
		`<w:vMerge/>`, `w:fill="EEEEEE"`, `w:fill="F2F2F2"`, `<w:jc w:val="right"/>`)
	if n := strings.Count(doc, "<w:gridCol "); n != 3 {
		t.Errorf("expected 3 grid columns, got %d", n)
	}
	// Each row must cover all 3 grid columns.
	for _, row := range strings.Split(doc, "<w:tr>")[1:] {
		row = row[:strings.Index(row, "</w:tr>")]
		cols := strings.Count(row, "<w:tc>") + strings.Count(row, `<w:gridSpan w:val="2"/>`)
		if cols != 3 {
			t.Errorf("row covers %d columns: %s", cols, row)
		}
	}
	if !strings.Contains(doc, "</w:tbl><w:p/>") {
		t.Error("document should end with a paragraph after the table")
	}
}

func TestLinksAndBookmarks(t *testing.T) {
	parts := unpack(t, `<p><a href="https://example.com/?a=1&b=2">ext</a> <a href="#sec">jump</a>
		<a href="javascript:alert(1)">bad</a></p><h2 id="sec">Section</h2>`, nil)
	doc, rels := parts["word/document.xml"], parts["word/_rels/document.xml.rels"]
	mustContain(t, doc, `<w:hyperlink r:id="rId10" w:history="1">`, `<w:hyperlink w:anchor="sec">`,
		`<w:bookmarkStart w:id="1" w:name="sec"/>`, `<w:rStyle w:val="Hyperlink"/>`, `>bad</w:t>`)
	mustContain(t, rels, `Target="https://example.com/?a=1&amp;b=2" TargetMode="External"`)
	if strings.Contains(rels, "javascript") {
		t.Error("javascript: link should be dropped")
	}
}

func TestImages(t *testing.T) {
	data := base64.StdEncoding.EncodeToString(pngBytes(t, 200, 100))
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pic.png"), pngBytes(t, 4000, 1000), 0o644); err != nil {
		t.Fatal(err)
	}
	parts := unpack(t, `<p><img src="data:image/png;base64,`+data+`" width="100" alt="logo">
		<img src="data:image/png;base64,`+data+`">
		<img src="/pic.png"> <img src="../../etc/passwd" alt="nope"> <img src="missing.png" alt="ไม่มีรูป"></p>`,
		&Options{ImageLoader: FileImageLoader(dir)})
	doc := parts["word/document.xml"]
	mustContain(t, doc, `<wp:extent cx="952500" cy="476250"/>`, `descr="logo"`, `[ไม่มีรูป]`, `[nope]`)
	if _, ok := parts["word/media/image1.png"]; !ok {
		t.Error("missing image1.png")
	}
	if _, ok := parts["word/media/image2.png"]; !ok {
		t.Error("missing image2.png from FileImageLoader")
	}
	if _, ok := parts["word/media/image3.png"]; ok {
		t.Error("identical data URIs should be embedded once")
	}
	// The 4000px image must be scaled down to the A4 text width (~6.27in).
	if strings.Contains(doc, `cx="38100000"`) {
		t.Error("large image was not scaled to page width")
	}
}

func TestImageWithoutLoader(t *testing.T) {
	doc := unpack(t, `<p><img src="https://example.com/a.png" alt="remote"></p>`, nil)["word/document.xml"]
	mustContain(t, doc, "[remote]")
}

func TestOptions(t *testing.T) {
	parts := unpack(t, `<p>x</p>`, &Options{
		FontFamily: "TH Sarabun New", FontSize: 16, Landscape: true,
		PageSize: PageA4, Margins: &Margins{10, 10, 10, 10}, Author: "ทดสอบ",
	})
	mustContain(t, parts["word/styles.xml"], `w:ascii="TH Sarabun New"`, `<w:sz w:val="32"/>`)
	mustContain(t, parts["word/document.xml"], `<w:pgSz w:w="16838" w:h="11906" w:orient="landscape"/>`, `w:top="567"`)
	mustContain(t, parts["docProps/core.xml"], "<dc:creator>ทดสอบ</dc:creator>")
}

func TestEmptyInput(t *testing.T) {
	doc := unpack(t, ``, nil)["word/document.xml"]
	mustContain(t, doc, "<w:body><w:p/><w:sectPr>")
}

func TestConvertFile(t *testing.T) {
	dir := t.TempDir()
	in, out := filepath.Join(dir, "in.html"), filepath.Join(dir, "out.docx")
	if err := os.WriteFile(in, []byte(`<p>file</p>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ConvertFile(in, out, nil); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(out); err != nil || fi.Size() == 0 {
		t.Fatalf("output not written: %v", err)
	}
}

func TestParseColor(t *testing.T) {
	cases := map[string]string{
		"#abc": "AABBCC", "#A1B2C3": "A1B2C3", "red": "FF0000", "rgb(255, 0, 128)": "FF0080",
		"rgba(0 0 0 / 50%)": "000000", "ff8800": "FF8800", "transparent": "", "rgba(1,2,3,0)": "", "nope": "",
	}
	for in, want := range cases {
		if got, _ := parseColor(in); got != want {
			t.Errorf("parseColor(%q) = %q, want %q", in, got, want)
		}
	}
}
