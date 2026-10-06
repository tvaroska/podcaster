package cover

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"os"
	"strings"
)

// MaxInlineImageBytes is the maximum decoded byte size of an inline data:image/* cover (5 MiB).
const MaxInlineImageBytes = 5 << 20 // 5 MiB

// IsInlineDataURI reports whether s is a data:image/... URI.
func IsInlineDataURI(s string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(s)), "data:image/")
}

// DecodeInlineDataURI parses and validates a data:image/(png|jpeg);base64,<payload> URI.
func DecodeInlineDataURI(raw string) (data []byte, contentType string, err error) {
	s := strings.TrimSpace(raw)
	if !IsInlineDataURI(s) {
		return nil, "", errors.New("inline image must be a data:image/png;base64,... or data:image/jpeg;base64,... URI")
	}
	comma := strings.IndexByte(s, ',')
	if comma < 0 {
		return nil, "", errors.New("inline image data URI is missing comma separator")
	}
	header := strings.ToLower(strings.TrimSpace(s[len("data:"):comma]))
	payload := s[comma+1:]
	if !strings.HasSuffix(header, ";base64") {
		return nil, "", errors.New("inline image data URI must use ;base64 encoding")
	}
	mediaType := strings.TrimSpace(strings.TrimSuffix(header, ";base64"))
	switch mediaType {
	case "image/png", "image/jpeg", "image/jpg":
	default:
		return nil, "", errors.New("inline image media type must be image/png or image/jpeg")
	}
	cleaned := strings.Map(func(r rune) rune {
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
			return -1
		}
		return r
	}, payload)
	if cleaned == "" {
		return nil, "", errors.New("inline image base64 payload is empty")
	}
	if base64.StdEncoding.DecodedLen(len(cleaned)) > MaxInlineImageBytes+4 {
		return nil, "", fmt.Errorf("inline image exceeds maximum size of %d bytes", MaxInlineImageBytes)
	}
	decoded, err := base64.StdEncoding.DecodeString(cleaned)
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(cleaned, "="))
		if err != nil {
			return nil, "", errors.New("inline image base64 payload is invalid")
		}
	}
	if len(decoded) == 0 {
		return nil, "", errors.New("inline image is empty")
	}
	if len(decoded) > MaxInlineImageBytes {
		return nil, "", fmt.Errorf("inline image exceeds maximum size of %d bytes", MaxInlineImageBytes)
	}
	detected := http.DetectContentType(decoded)
	if detected != "image/png" && detected != "image/jpeg" {
		return nil, "", errors.New("inline image bytes must be a valid PNG or JPEG image")
	}
	return decoded, detected, nil
}

// PodcastCoverKey returns the storage object key for a custom podcast cover image.
func PodcastCoverKey(podcastID string) string {
	return "covers/podcasts/" + podcastID
}

// EpisodeCoverKey returns the storage object key for a custom episode cover image.
func EpisodeCoverKey(episodeID string) string {
	return "covers/episodes/" + episodeID
}

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
