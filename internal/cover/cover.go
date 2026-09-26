package cover

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
)

// Load returns PNG bytes from path, or a generated 1400x1400 fallback cover.
func Load(path string) ([]byte, error) {
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		return b, nil
	}
	return Generate(), nil
}

// Generate produces a simple branded 1400×1400 PNG suitable as an iTunes artwork.
func Generate() []byte {
	const size = 1400
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	bg := color.RGBA{R: 18, G: 18, B: 28, A: 255}
	accent := color.RGBA{R: 88, G: 166, B: 255, A: 255}
	inner := color.RGBA{R: 32, G: 36, B: 52, A: 255}
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			img.Set(x, y, bg)
		}
	}
	margin := 80
	for y := margin; y < size-margin; y++ {
		for x := margin; x < size-margin; x++ {
			img.Set(x, y, inner)
		}
	}
	// Concentric rings as a speaker/waveform motif.
	cx, cy := size/2, size/2
	radii := []int{80, 160, 240, 320}
	for _, r := range radii {
		drawCircle(img, cx, cy, r, 18, accent)
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func drawCircle(img *image.RGBA, cx, cy, r, thickness int, c color.RGBA) {
	r2 := r * r
	inner := r - thickness
	if inner < 0 {
		inner = 0
	}
	inner2 := inner * inner
	minX, maxX := cx-r-1, cx+r+1
	minY, maxY := cy-r-1, cy+r+1
	b := img.Bounds()
	if minX < b.Min.X {
		minX = b.Min.X
	}
	if minY < b.Min.Y {
		minY = b.Min.Y
	}
	if maxX > b.Max.X {
		maxX = b.Max.X
	}
	if maxY > b.Max.Y {
		maxY = b.Max.Y
	}
	for y := minY; y < maxY; y++ {
		dy := y - cy
		for x := minX; x < maxX; x++ {
			dx := x - cx
			d := dx*dx + dy*dy
			if d <= r2 && d >= inner2 {
				img.Set(x, y, c)
			}
		}
	}
}
