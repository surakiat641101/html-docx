package htmldocx

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"strings"
	"time"
)

const (
	xmlHeader = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n"
	nsW       = `xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"`
	nsR       = `xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"`
)

func esc(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func (c *converter) writePackage(w io.Writer) error {
	now := time.Now().UTC()
	zw := zip.NewWriter(w)
	add := func(name string, data []byte) error {
		f, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: now})
		if err != nil {
			return err
		}
		_, err = f.Write(data)
		return err
	}

	parts := []struct {
		name string
		data []byte
	}{
		{"[Content_Types].xml", c.contentTypesXML()},
		{"_rels/.rels", []byte(rootRelsXML)},
		{"docProps/core.xml", c.coreXML(now)},
		{"docProps/app.xml", []byte(appXML)},
		{"word/document.xml", c.documentXML()},
		{"word/styles.xml", c.stylesXML()},
		{"word/settings.xml", []byte(settingsXML)},
		{"word/_rels/document.xml.rels", c.documentRelsXML()},
	}
	if len(c.lists) > 0 {
		parts = append(parts, struct {
			name string
			data []byte
		}{"word/numbering.xml", c.numberingXML()})
	}
	for _, p := range parts {
		if err := add(p.name, p.data); err != nil {
			return err
		}
	}
	for _, m := range c.media {
		if err := add("word/media/"+m.name, m.data); err != nil {
			return err
		}
	}
	return zw.Close()
}

func (c *converter) contentTypesXML() []byte {
	var b bytes.Buffer
	b.WriteString(xmlHeader + `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
		`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
		`<Default Extension="xml" ContentType="application/xml"/>` +
		`<Default Extension="png" ContentType="image/png"/>` +
		`<Default Extension="jpeg" ContentType="image/jpeg"/>` +
		`<Default Extension="gif" ContentType="image/gif"/>` +
		`<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>` +
		`<Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/>` +
		`<Override PartName="/word/settings.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.settings+xml"/>`)
	if len(c.lists) > 0 {
		b.WriteString(`<Override PartName="/word/numbering.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.numbering+xml"/>`)
	}
	b.WriteString(`<Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/>` +
		`<Override PartName="/docProps/app.xml" ContentType="application/vnd.openxmlformats-officedocument.extended-properties+xml"/>` +
		`</Types>`)
	return b.Bytes()
}

const rootRelsXML = xmlHeader + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
	`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>` +
	`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/>` +
	`<Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/extended-properties" Target="docProps/app.xml"/>` +
	`</Relationships>`

const appXML = xmlHeader + `<Properties xmlns="http://schemas.openxmlformats.org/officeDocument/2006/extended-properties" ` +
	`xmlns:vt="http://schemas.openxmlformats.org/officeDocument/2006/docPropsVTypes"><Application>htmldocx</Application></Properties>`

const settingsXML = xmlHeader + `<w:settings ` + nsW + `><w:defaultTabStop w:val="720"/>` +
	`<w:characterSpacingControl w:val="doNotCompress"/><w:compat>` +
	`<w:compatSetting w:name="compatibilityMode" w:uri="http://schemas.microsoft.com/office/word" w:val="15"/>` +
	`</w:compat></w:settings>`

func (c *converter) coreXML(now time.Time) []byte {
	title := c.opts.Title
	if title == "" {
		title = c.title
	}
	ts := now.Format("2006-01-02T15:04:05Z")
	return []byte(xmlHeader + `<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" ` +
		`xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/" ` +
		`xmlns:dcmitype="http://purl.org/dc/dcmitype/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">` +
		`<dc:title>` + esc(title) + `</dc:title><dc:subject>` + esc(c.opts.Subject) + `</dc:subject>` +
		`<dc:creator>` + esc(c.opts.Author) + `</dc:creator><cp:keywords>` + esc(c.opts.Keywords) + `</cp:keywords>` +
		`<dcterms:created xsi:type="dcterms:W3CDTF">` + ts + `</dcterms:created>` +
		`<dcterms:modified xsi:type="dcterms:W3CDTF">` + ts + `</dcterms:modified></cp:coreProperties>`)
}

func (c *converter) documentRelsXML() []byte {
	var b bytes.Buffer
	b.WriteString(xmlHeader + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
		`<Relationship Id="rId1" Type="` + relStyles + `" Target="styles.xml"/>` +
		`<Relationship Id="rId2" Type="` + relSettings + `" Target="settings.xml"/>`)
	if len(c.lists) > 0 {
		b.WriteString(`<Relationship Id="rId3" Type="` + relNumbering + `" Target="numbering.xml"/>`)
	}
	for _, r := range c.rels {
		b.WriteString(`<Relationship Id="` + r.id + `" Type="` + r.typ + `" Target="` + esc(r.target) + `"`)
		if r.external {
			b.WriteString(` TargetMode="External"`)
		}
		b.WriteString("/>")
	}
	b.WriteString(`</Relationships>`)
	return b.Bytes()
}

func (c *converter) documentXML() []byte {
	o := c.opts
	pw, ph := mmToTwips(o.PageSize.WidthMM), mmToTwips(o.PageSize.HeightMM)
	orient := ""
	if o.Landscape {
		pw, ph = max(pw, ph), min(pw, ph)
		orient = ` w:orient="landscape"`
	}
	m := o.Margins

	var b bytes.Buffer
	b.Grow(c.out.Len() + 1024)
	b.WriteString(xmlHeader + `<w:document ` + nsW + ` ` + nsR +
		` xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing"` +
		` xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"` +
		` xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture"><w:body>`)
	body := c.out.Bytes()
	b.Write(body)
	if len(body) == 0 || bytes.HasSuffix(body, []byte("</w:tbl>")) {
		b.WriteString("<w:p/>") // Word expects a paragraph after a final table
	}
	fmt.Fprintf(&b, `<w:sectPr><w:pgSz w:w="%d" w:h="%d"%s/>`+
		`<w:pgMar w:top="%d" w:right="%d" w:bottom="%d" w:left="%d" w:header="720" w:footer="720" w:gutter="0"/>`+
		`</w:sectPr></w:body></w:document>`,
		pw, ph, orient, mmToTwips(m.Top), mmToTwips(m.Right), mmToTwips(m.Bottom), mmToTwips(m.Left))
	return b.Bytes()
}

func (c *converter) stylesXML() []byte {
	font := esc(c.opts.FontFamily)
	hp := func(pt float64) int { return int(math.Round(pt * 2)) }
	base := c.opts.FontSize

	var b bytes.Buffer
	fmt.Fprintf(&b, xmlHeader+`<w:styles `+nsW+`><w:docDefaults><w:rPrDefault><w:rPr>`+
		`<w:rFonts w:ascii="%[1]s" w:hAnsi="%[1]s" w:eastAsia="%[1]s" w:cs="%[1]s"/>`+
		`<w:sz w:val="%[2]d"/><w:szCs w:val="%[2]d"/><w:lang w:val="en-US" w:eastAsia="en-US" w:bidi="th-TH"/>`+
		`</w:rPr></w:rPrDefault><w:pPrDefault><w:pPr><w:spacing w:after="160" w:line="259" w:lineRule="auto"/>`+
		`</w:pPr></w:pPrDefault></w:docDefaults>`, font, hp(base))

	b.WriteString(`<w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/><w:qFormat/></w:style>`)

	scale := [...]float64{2, 1.6, 1.35, 1.15, 1, 0.9}
	for i, s := range scale {
		fmt.Fprintf(&b, `<w:style w:type="paragraph" w:styleId="Heading%[1]d"><w:name w:val="heading %[1]d"/>`+
			`<w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:uiPriority w:val="9"/><w:qFormat/>`+
			`<w:pPr><w:keepNext/><w:keepLines/><w:spacing w:before="%[2]d" w:after="120"/><w:outlineLvl w:val="%[3]d"/></w:pPr>`+
			`<w:rPr><w:b/><w:bCs/><w:sz w:val="%[4]d"/><w:szCs w:val="%[4]d"/></w:rPr></w:style>`,
			i+1, 360-40*i, i, hp(base*s))
	}

	fmt.Fprintf(&b, `<w:style w:type="paragraph" w:styleId="ListParagraph"><w:name w:val="List Paragraph"/>`+
		`<w:basedOn w:val="Normal"/><w:uiPriority w:val="34"/><w:qFormat/>`+
		`<w:pPr><w:spacing w:after="60"/><w:contextualSpacing/></w:pPr></w:style>`+
		`<w:style w:type="paragraph" w:styleId="HTMLPreformatted"><w:name w:val="HTML Preformatted"/>`+
		`<w:basedOn w:val="Normal"/><w:uiPriority w:val="99"/>`+
		`<w:pPr><w:shd w:val="clear" w:color="auto" w:fill="F5F5F5"/><w:spacing w:after="160" w:line="240" w:lineRule="auto"/></w:pPr>`+
		`<w:rPr><w:rFonts w:ascii="Courier New" w:hAnsi="Courier New" w:eastAsia="Courier New" w:cs="Courier New"/>`+
		`<w:sz w:val="%[1]d"/><w:szCs w:val="%[1]d"/></w:rPr></w:style>`+
		`<w:style w:type="paragraph" w:styleId="Caption"><w:name w:val="caption"/><w:basedOn w:val="Normal"/>`+
		`<w:next w:val="Normal"/><w:uiPriority w:val="35"/><w:qFormat/><w:pPr><w:spacing w:after="120"/></w:pPr>`+
		`<w:rPr><w:i/><w:iCs/><w:color w:val="44546A"/><w:sz w:val="%[2]d"/><w:szCs w:val="%[2]d"/></w:rPr></w:style>`+
		`<w:style w:type="character" w:default="1" w:styleId="DefaultParagraphFont"><w:name w:val="Default Paragraph Font"/>`+
		`<w:uiPriority w:val="1"/><w:semiHidden/><w:unhideWhenUsed/></w:style>`+
		`<w:style w:type="character" w:styleId="Hyperlink"><w:name w:val="Hyperlink"/><w:uiPriority w:val="99"/>`+
		`<w:unhideWhenUsed/><w:rPr><w:color w:val="0563C1"/><w:u w:val="single"/></w:rPr></w:style>`+
		`<w:style w:type="table" w:default="1" w:styleId="TableNormal"><w:name w:val="Normal Table"/>`+
		`<w:uiPriority w:val="99"/><w:semiHidden/><w:unhideWhenUsed/><w:tblPr><w:tblInd w:w="0" w:type="dxa"/>`+
		`<w:tblCellMar><w:top w:w="0" w:type="dxa"/><w:left w:w="108" w:type="dxa"/><w:bottom w:w="0" w:type="dxa"/>`+
		`<w:right w:w="108" w:type="dxa"/></w:tblCellMar></w:tblPr></w:style>`+
		`<w:style w:type="numbering" w:default="1" w:styleId="NoList"><w:name w:val="No List"/>`+
		`<w:uiPriority w:val="99"/><w:semiHidden/><w:unhideWhenUsed/></w:style></w:styles>`,
		hp(base*0.9), hp(base*0.9))
	return b.Bytes()
}

var bulletChars = [...]string{"•", "◦", "▪"}

func (c *converter) numberingXML() []byte {
	var b bytes.Buffer
	b.WriteString(xmlHeader + `<w:numbering ` + nsW + `>`)
	for i, l := range c.lists {
		fmt.Fprintf(&b, `<w:abstractNum w:abstractNumId="%d"><w:multiLevelType w:val="hybridMultilevel"/>`, i)
		for lvl := 0; lvl < 9; lvl++ {
			format, text, start := l.format, fmt.Sprintf("%%%d.", lvl+1), l.start
			switch format {
			case "bullet":
				text, start = bulletChars[lvl%len(bulletChars)], 1
			case "none":
				text = ""
			}
			fmt.Fprintf(&b, `<w:lvl w:ilvl="%d"><w:start w:val="%d"/><w:numFmt w:val="%s"/><w:lvlText w:val="%s"/>`+
				`<w:lvlJc w:val="left"/><w:pPr><w:ind w:left="%d" w:hanging="360"/></w:pPr></w:lvl>`,
				lvl, start, format, text, 720*(lvl+1))
		}
		b.WriteString(`</w:abstractNum>`)
	}
	for i := range c.lists {
		fmt.Fprintf(&b, `<w:num w:numId="%d"><w:abstractNumId w:val="%d"/></w:num>`, i+1, i)
	}
	b.WriteString(`</w:numbering>`)
	return b.Bytes()
}
