// Example Fiber server:
//
//	go run ./examples/server
//	open http://localhost:3000
package main

import (
	"fmt"
	"log"
	"time"

	"github.com/gofiber/fiber/v2"

	htmldocx "github.com/surakiat641101/html-docx"
	"github.com/surakiat641101/html-docx/fiberdocx"
)

const formPage = `<!doctype html>
<html lang="th"><head><meta charset="utf-8"><title>HTML → DOCX</title>
<style>body{font-family:sans-serif;max-width:720px;margin:2rem auto;padding:0 1rem}textarea{width:100%;height:240px}</style>
</head><body>
<h1>HTML → DOCX</h1>
<form method="post" action="/convert" enctype="multipart/form-data">
  <p><label>ชื่อไฟล์ <input name="filename" value="เอกสาร.docx"></label></p>
  <p><textarea name="html"><h1>สวัสดี</h1><p>นี่คือ <b>ตัวหนา</b> และ <i>ตัวเอียง</i></p><ul><li>หนึ่ง</li><li>สอง</li></ul></textarea></p>
  <p>หรืออัปโหลดไฟล์ .html: <input type="file" name="file" accept=".html,.htm"></p>
  <button>แปลงเป็น .docx</button>
</form>
<p><a href="/report">ดาวน์โหลดรายงานตัวอย่าง</a></p>
</body></html>`

func main() {
	app := fiber.New(fiber.Config{BodyLimit: 20 << 20})

	opts := &htmldocx.Options{FontFamily: "TH Sarabun New", FontSize: 16}

	app.Get("/", func(c *fiber.Ctx) error {
		c.Type("html", "utf-8")
		return c.SendString(formPage)
	})

	// POST /convert accepts multipart (file or "html" field), form, JSON or a raw HTML body.
	app.Post("/convert", fiberdocx.New(fiberdocx.Config{Options: opts}))

	// Build HTML in code and send it as a Word file.
	app.Get("/report", func(c *fiber.Ctx) error {
		html := fmt.Sprintf(`<h1>รายงานยอดขาย</h1>
			<p>วันที่ออกรายงาน: %s</p>
			<table>
			  <thead><tr><th>สินค้า</th><th>จำนวน</th><th>ราคา (บาท)</th></tr></thead>
			  <tbody>
			    <tr><td>กาแฟ</td><td align="right">120</td><td align="right">6,000</td></tr>
			    <tr><td>ชาเขียว</td><td align="right">80</td><td align="right">3,600</td></tr>
			    <tr><td colspan="2"><b>รวม</b></td><td align="right"><b>9,600</b></td></tr>
			  </tbody>
			</table>`, time.Now().Format("02/01/2006"))
		return fiberdocx.Send(c, html, "รายงานยอดขาย.docx", opts)
	})

	log.Fatal(app.Listen(":3000"))
}
