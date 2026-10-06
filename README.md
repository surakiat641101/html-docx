# html-docx

Library ภาษา Go สำหรับแปลง **HTML → Word (.docx)** พร้อม integration กับ **Fiber**

- เขียนด้วย Go ล้วน ไม่ต้องติดตั้ง Word / LibreOffice / Pandoc
- รองรับภาษาไทย (ตั้งฟอนต์ complex script และ `w:bidi="th-TH"` ให้อัตโนมัติ)
- ต้องใช้ Go 1.24 ขึ้นไป

```bash
go get github.com/surakiat641101/html-docx
```

## ใช้งานพื้นฐาน

```go
import htmldocx "github.com/surakiat641101/html-docx"

data, err := htmldocx.ConvertString(`<h1>สวัสดี</h1><p>นี่คือ <b>ตัวหนา</b></p>`, &htmldocx.Options{
    FontFamily: "TH Sarabun New",
    FontSize:   16,
})
os.WriteFile("out.docx", data, 0o644)

// หรือแปลงจากไฟล์ (รูปภาพ path relative จะถูกโหลดจากโฟลเดอร์เดียวกับไฟล์ HTML)
err = htmldocx.ConvertFile("page.html", "page.docx", nil)

// หรือแบบ stream
err = htmldocx.Convert(reader, writer, opts)
```

## ใช้กับ Fiber

```go
import (
    "github.com/gofiber/fiber/v2"
    htmldocx "github.com/surakiat641101/html-docx"
    "github.com/surakiat641101/html-docx/fiberdocx"
)

app := fiber.New()
opts := &htmldocx.Options{FontFamily: "TH Sarabun New", FontSize: 16}

// 1) Endpoint สำเร็จรูป: รับ HTML แล้วตอบกลับเป็นไฟล์ .docx
app.Post("/convert", fiberdocx.New(fiberdocx.Config{Options: opts}))

// 2) สร้าง HTML เองแล้วส่งเป็นไฟล์ Word
app.Get("/report", func(c *fiber.Ctx) error {
    return fiberdocx.Send(c, "<h1>รายงาน</h1>", "รายงาน.docx", opts)
})

// 3) Render template ของ Fiber (fiber.Config{Views: ...}) แล้วส่งเป็น .docx
app.Get("/invoice/:id", func(c *fiber.Ctx) error {
    return fiberdocx.Render(c, "invoice", fiber.Map{"ID": c.Params("id")}, "invoice.docx", opts)
})
```

`fiberdocx.New` รับ input ได้หลายแบบ:

| Content-Type | ที่มาของ HTML |
|---|---|
| `multipart/form-data` | ไฟล์อัปโหลดใน field `file` หรือข้อความใน field `html` |
| `application/x-www-form-urlencoded` | field `html` |
| `application/json` | `{"html": "...", "filename": "report.docx"}` |
| อื่น ๆ (เช่น `text/html`) | request body ทั้งก้อน |

ชื่อไฟล์ผลลัพธ์มาจาก `?filename=`, field `filename`, ชื่อไฟล์ที่อัปโหลด หรือ `Config.DefaultFilename` ตามลำดับ (ชื่อภาษาไทยใช้ได้ ส่งผ่าน `filename*=UTF-8''…`)

```bash
curl -X POST localhost:3000/convert?filename=test -H "Content-Type: text/html" \
     --data "<h1>ทดสอบ</h1>" -o test.docx
```

ตัวอย่าง server เต็ม ๆ: `go run ./examples/server` แล้วเปิด http://localhost:3000

## CLI

```bash
go install github.com/surakiat641101/html-docx/cmd/html2docx@latest
html2docx -font "TH Sarabun New" -size 16 input.html output.docx
```

## Options

| Field | ค่าเริ่มต้น | คำอธิบาย |
|---|---|---|
| `FontFamily` | `Tahoma` | ฟอนต์หลัก (ใช้ทั้งอักษรละตินและไทย) |
| `FontSize` | `11` | ขนาดฟอนต์ (pt) |
| `BoldWeight` | `600` | `font-weight` ตัวเลขตั้งแต่ค่านี้ขึ้นไปเป็นตัวหนา (Word มีแค่ปกติ/หนา) — ถ้า HTML ใช้ 500 เป็นตัวเน้น ให้ตั้งเป็น `500` |
| `PageSize` | `PageA4` | `PageA4`, `PageA5`, `PageLetter`, `PageLegal` หรือกำหนดเอง (mm) |
| `Landscape` | `false` | แนวนอน |
| `Margins` | 25.4mm ทุกด้าน | ขอบกระดาษ (mm) |
| `Title`, `Subject`, `Author`, `Keywords` | — | Document properties (`Title` ใช้ `<title>` ถ้าไม่กำหนด) |
| `ImageLoader` | `nil` | วิธีโหลดรูปที่ไม่ใช่ `data:` URI |
| `CSS` | — | stylesheet เพิ่มเติม (เช่นเนื้อหาไฟล์ `.css` ที่อ้างด้วย `<link>`) ใช้หลัง `<style>` ในเอกสาร |

### รูปภาพ

- `data:image/...;base64,...` ฝังได้เสมอ
- รูปจากไฟล์/URL ต้องตั้ง `ImageLoader`:
  - `htmldocx.FileImageLoader(dir)` — โหลดจากโฟลเดอร์ (ไม่สามารถออกนอก `dir` ได้)
  - `htmldocx.HTTPImageLoader(client)` — ดาวน์โหลดผ่าน http(s)
  - `htmldocx.ChainImageLoaders(a, b)` — ลองทีละตัว
- รองรับ PNG, JPEG, GIF; รูปที่โหลดไม่ได้จะแสดงเป็นข้อความ `[alt]`
- ขนาดใช้ `width`/`height` (attribute หรือ CSS) และย่อให้ไม่เกินความกว้างหน้ากระดาษ

> ⚠️ ถ้า HTML มาจากผู้ใช้ภายนอก **อย่า** เปิด `FileImageLoader`/`HTTPImageLoader` กับ input นั้นโดยไม่กรอง เพราะอาจถูกใช้อ่านไฟล์ในเครื่องหรือยิง request ไปยัง network ภายใน (SSRF)

## HTML/CSS ที่รองรับ

- **Block:** `h1`–`h6`, `p`, `div`, `section`/`article`/…, `blockquote`, `pre`, `hr`, `br`, `center`, `dl`/`dt`/`dd`, `figure`/`figcaption`
- **Inline:** `b`/`strong`, `i`/`em`, `u`/`ins`, `s`/`del`/`strike`, `sup`, `sub`, `code`/`kbd`, `mark`, `small`, `big`, `font`, `span`, `q`, `a` (ลิงก์ภายนอก + ลิงก์ภายใน `#id`)
- **List:** `ul`, `ol` (ซ้อนได้ 9 ระดับ, `start`, `type`, `list-style-type` รวมถึง `thai`)
- **Table:** `thead` (ทำซ้ำหัวตารางทุกหน้า), `th`, `colspan`, `rowspan`, `caption`, `bgcolor`, `align`, `valign`, `border="0"`
- **Image:** `img`
- **Form:** `input type="checkbox|radio"` แสดงเป็น ☐ ☒ ○ ◉
- **CSS:** ทั้ง inline `style="..."` และ `<style>` ในเอกสาร
  - Selector: `p`, `*`, `.class`, `#id`, `[attr]`, `[attr="v"]` (`^= $= *= ~= |=`), `div p`, `ul > li`, `h2 + p`, `h2 ~ p`, `:first-child`, `:last-child`, `:nth-child(odd|even|2n+1)`, `:root`
  - Cascade ตาม specificity, ลำดับใน source และ `!important`; ใช้ `@media print` / `all` / `screen` (ไม่สนใจ `max-width` ฯลฯ)
  - `@page { size; margin }` → ขนาดกระดาษ/แนวกระดาษ/ขอบกระดาษ (ถ้าไม่ได้ตั้งใน Options)
  - ตัวอักษร: `color`, `background-color`, `font-size`, `font-weight`, `font-style`, `font-family`, `text-decoration` (รวม `dotted`/`dashed`/`double`/`wavy`), `vertical-align`, `text-transform: uppercase`
  - ย่อหน้า: `text-align`, `text-indent`, `line-height`, `margin` (บน/ล่าง → ระยะห่างย่อหน้า, ซ้าย → เยื้อง), `padding-left`, `border` → เส้นขอบย่อหน้า
  - Inline: `border` → กรอบรอบตัวอักษร, `border-bottom` → ขีดเส้นใต้, `margin-left/right` → เว้นวรรค
  - `display: none`, `display: block|flex` (span กลายเป็นย่อหน้า), `display: inline|inline-block` (div ไม่ขึ้นย่อหน้าใหม่), `page-break-*`, `break-*`
  - `div` และ element ที่ไม่มี margin ใน browser จะไม่มีระยะห่างระหว่างย่อหน้า ส่วน `p` ใช้ระยะห่างปกติ
- **`data-docx-text="..."`:** ใส่ที่ element ใดก็ได้ (เช่น `<svg>`) เพื่อแทนที่ด้วยข้อความใน .docx เช่น checkbox `<svg data-docx-text="☑">`

**ข้อจำกัด:** ไม่โหลด `<link rel="stylesheet">` เอง (ส่งผ่าน `Options.CSS` แทน), ไม่รองรับ `:hover`/`:not()`/`::before`, `position`/flex/grid/float layout (เนื้อหาเรียงเป็นย่อหน้าตามลำดับ), SVG/WebP (ใช้ `data-docx-text` แทน), และ input ต้องเป็น UTF-8

## Tests

```bash
go test ./...
```
