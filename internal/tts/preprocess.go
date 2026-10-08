package tts

import (
	"html"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	ssmlTag        = regexp.MustCompile(`(?is)</?[a-zA-Z][a-zA-Z0-9:_-]*(?:\s+[^>]*)?/?>`)
	codeFenceLine  = regexp.MustCompile(`(?m)^\s*` + "```" + `[^\n]*\n?`)
	inlineCode     = regexp.MustCompile("`([^`\n]+)`")
	mdImage        = regexp.MustCompile(`!\[([^\]]*)\]\([^)]+\)`)
	mdLink         = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`)
	bareURL        = regexp.MustCompile(`https?://[^\s)>\]]+`)
	mdHeading      = regexp.MustCompile(`(?m)^[ \t]*#{1,6}[ \t]+(.+?)[ \t]*$`)
	mdBullet       = regexp.MustCompile(`(?m)^[ \t]*(?:[-*+]|\d+\.)[ \t]+`)
	mdRule         = regexp.MustCompile(`(?m)^[ \t]*(?:[-*_][ \t]*){3,}$`)
	mdBoldItalic   = regexp.MustCompile(`(\*\*|__)(.+?)(\*\*|__)`)
	multiSpace     = regexp.MustCompile(`[ \t]+`)
	multiNL        = regexp.MustCompile(`\n{3,}`)
)

// maxChunkRunes keeps each TTS synthesis chunk comfortably within Kokoro-82M's
// ~510-phoneme context window while preserving natural sentence/paragraph breaks.
const maxChunkRunes = 500

// Preprocess strips SSML and Markdown formatting, normalizes whitespace, and
// splits text into paragraph- and sentence-aware TTS chunks.
func Preprocess(text string) []string {
	text = ssmlTag.ReplaceAllString(text, " ")
	text = html.UnescapeString(text)
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = cleanMarkdown(text)
	text = multiSpace.ReplaceAllString(text, " ")
	text = multiNL.ReplaceAllString(text, "\n\n")
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	return chunkParagraphs(text, maxChunkRunes)
}

func cleanMarkdown(text string) string {
	text = codeFenceLine.ReplaceAllString(text, "\n")
	text = mdImage.ReplaceAllString(text, "$1")
	text = mdLink.ReplaceAllString(text, "$1")
	text = bareURL.ReplaceAllString(text, "")
	text = inlineCode.ReplaceAllString(text, "$1")
	text = mdRule.ReplaceAllString(text, "\n\n")
	text = mdHeading.ReplaceAllStringFunc(text, func(line string) string {
		sub := mdHeading.FindStringSubmatch(line)
		if len(sub) < 2 {
			return line
		}
		h := strings.TrimSpace(sub[1])
		if h == "" {
			return ""
		}
		last, _ := utf8.DecodeLastRuneInString(h)
		if !isSentenceEnd(last) && last != ':' && last != ';' {
			h += "."
		}
		return h + "\n\n"
	})
	text = mdBullet.ReplaceAllString(text, "")
	for i := 0; i < 2; i++ {
		text = mdBoldItalic.ReplaceAllString(text, "$2")
	}
	return text
}

func chunkParagraphs(text string, maxRunes int) []string {
	paragraphs := strings.Split(text, "\n\n")
	var out []string
	for _, p := range paragraphs {
		p = strings.TrimSpace(strings.ReplaceAll(p, "\n", " "))
		p = multiSpace.ReplaceAllString(p, " ")
		if p == "" {
			continue
		}
		out = append(out, chunk(p, maxRunes)...)
	}
	return out
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
