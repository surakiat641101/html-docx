// Command html2docx converts an HTML file to a .docx file.
//
//	html2docx [-font "TH Sarabun New"] [-size 16] [-landscape] input.html [output.docx]
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	htmldocx "github.com/surakiat641101/html-docx"
)

func main() {
	font := flag.String("font", "", "default font family (default Tahoma)")
	size := flag.Float64("size", 0, "default font size in points (default 11)")
	landscape := flag.Bool("landscape", false, "landscape orientation")
	letter := flag.Bool("letter", false, "US Letter paper instead of A4")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: html2docx [flags] input.html [output.docx]")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() < 1 || flag.NArg() > 2 {
		flag.Usage()
		os.Exit(2)
	}
	in := flag.Arg(0)
	out := flag.Arg(1)
	if out == "" {
		out = strings.TrimSuffix(strings.TrimSuffix(in, ".html"), ".htm") + ".docx"
	}
	opts := &htmldocx.Options{FontFamily: *font, FontSize: *size, Landscape: *landscape}
	if *letter {
		opts.PageSize = htmldocx.PageLetter
	}
	if err := htmldocx.ConvertFile(in, out, opts); err != nil {
		fmt.Fprintln(os.Stderr, "html2docx:", err)
		os.Exit(1)
	}
	fmt.Println("wrote", out)
}
