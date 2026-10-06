package htmldocx

// PageSize is a paper size in millimetres (portrait orientation).
type PageSize struct {
	WidthMM, HeightMM float64
}

// Common paper sizes.
var (
	PageA4     = PageSize{210, 297}
	PageA5     = PageSize{148, 210}
	PageLetter = PageSize{215.9, 279.4}
	PageLegal  = PageSize{215.9, 355.6}
)

// Margins are page margins in millimetres.
type Margins struct {
	Top, Right, Bottom, Left float64
}

// ImageLoader returns the raw bytes of an image referenced by <img src="...">.
// data: URIs are always decoded internally and never reach the loader.
// Supported image formats are PNG, JPEG and GIF.
type ImageLoader func(src string) ([]byte, error)

// Options controls the generated document. The zero value is usable.
type Options struct {
	// FontFamily is the default font for Latin and complex-script (e.g. Thai)
	// text. Default: "Tahoma".
	FontFamily string
	// FontSize is the default font size in points. Default: 11.
	FontSize float64

	// PageSize defaults to A4.
	PageSize PageSize
	// Landscape swaps the page width and height.
	Landscape bool
	// Margins defaults to 25.4mm (1 inch) on every side.
	Margins *Margins

	// Document properties. Title defaults to the HTML <title>.
	Title, Subject, Author, Keywords string

	// ImageLoader resolves non-data: image sources. When nil, only data: URIs
	// are embedded and other images are replaced with their alt text.
	// See FileImageLoader and HTTPImageLoader.
	ImageLoader ImageLoader
}

func (o *Options) resolve() Options {
	var r Options
	if o != nil {
		r = *o
	}
	if r.FontFamily == "" {
		r.FontFamily = "Tahoma"
	}
	if r.FontSize <= 0 {
		r.FontSize = 11
	}
	if r.PageSize.WidthMM <= 0 || r.PageSize.HeightMM <= 0 {
		r.PageSize = PageA4
	}
	if r.Margins == nil {
		r.Margins = &Margins{25.4, 25.4, 25.4, 25.4}
	}
	return r
}

func mmToTwips(mm float64) int {
	return int(mm*1440/25.4 + 0.5)
}
