package tts

import (
	"html"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	ssmlTag    = regexp.MustCompile(`(?is)</?[a-zA-Z][a-zA-Z0-9:_-]*(?:\s+[^>]*)?/?>`)
	multiSpace = regexp.MustCompile(`[ \t]+`)
	multiNL    = regexp.MustCompile(`\n{3,}`)
)

const maxChunkRunes = 1800

// Preprocess strips SSML, normalizes whitespace, and splits into TTS-friendly chunks.
func Preprocess(text string) []string {
	text = ssmlTag.ReplaceAllString(text, " ")
	text = html.UnescapeString(text)
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = multiSpace.ReplaceAllString(text, " ")
	text = multiNL.ReplaceAllString(text, "\n\n")
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	return chunk(text, maxChunkRunes)
}

func chunk(text string, maxRunes int) []string {
	if utf8.RuneCountInString(text) <= maxRunes {
		return []string{text}
	}
	var out []string
	var buf strings.Builder
	runes := 0
	flush := func() {
		s := strings.TrimSpace(buf.String())
		if s != "" {
			out = append(out, s)
		}
		buf.Reset()
		runes = 0
	}
	for _, sentence := range splitSentences(text) {
		n := utf8.RuneCountInString(sentence)
		if runes > 0 && runes+1+n > maxRunes {
			flush()
		}
		if n > maxRunes {
			for _, part := range splitHard(sentence, maxRunes) {
				if runes > 0 && runes+1+utf8.RuneCountInString(part) > maxRunes {
					flush()
				}
				if buf.Len() > 0 {
					buf.WriteByte(' ')
					runes++
				}
				buf.WriteString(part)
				runes += utf8.RuneCountInString(part)
			}
			continue
		}
		if buf.Len() > 0 {
			buf.WriteByte(' ')
			runes++
		}
		buf.WriteString(sentence)
		runes += n
	}
	flush()
	return out
}

func splitSentences(text string) []string {
	var out []string
	var buf strings.Builder
	runes := []rune(text)
	for i := 0; i < len(runes); i++ {
		buf.WriteRune(runes[i])
		if isSentenceEnd(runes[i]) {
			if i+1 < len(runes) && (unicode.IsSpace(runes[i+1]) || runes[i+1] == '\n') {
				out = append(out, strings.TrimSpace(buf.String()))
				buf.Reset()
			}
		}
	}
	if s := strings.TrimSpace(buf.String()); s != "" {
		out = append(out, s)
	}
	if len(out) == 0 {
		return []string{text}
	}
	return out
}

func isSentenceEnd(r rune) bool {
	switch r {
	case '.', '!', '?', '。', '！', '？':
		return true
	}
	return false
}

func splitHard(s string, maxRunes int) []string {
	rs := []rune(s)
	var out []string
	for len(rs) > 0 {
		n := maxRunes
		if n > len(rs) {
			n = len(rs)
		}
		out = append(out, string(rs[:n]))
		rs = rs[n:]
	}
	return out
}
