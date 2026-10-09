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

	// structuralHeaderInline matches inline section intros such as
	// "Part 1 NASA In March 2026..." and adds colon + ellipsis pause cues:
	// "Part 1: NASA. ... In March 2026..."
	structuralHeaderInline = regexp.MustCompile(`(?m)(^|\n)[ \t]*((?:Part|Section|Chapter|Phase|Act)\s+(?:\d+|[IVXLCDM]+))[ \t]+([A-Z][A-Za-z0-9'’-]*(?:[ \t]+[A-Z][A-Za-z0-9'’-]*){0,2})[ \t]+((?:In|On|At|The|A|An|During|Following|After|Before|When|While|As|By|For|From|With|Since|Until|Under|Over|Between|Through|Across|Despite|Although)\b)`)
	// structuralHeaderTitle matches standalone "Part 1 NASA" titles lacking a colon.
	structuralHeaderTitle = regexp.MustCompile(`^((?:Part|Section|Chapter|Phase|Act)\s+(?:\d+|[IVXLCDM]+))\s+([A-Z].*)$`)

	// initialismToken matches standalone uppercase tokens (2-6 uppercase letters, optional trailing 's').
	initialismToken = regexp.MustCompile(`\b([A-Z]{2,6})(s?)\b`)

	// changERegex matches Chang'e / Chang’e / Chang-e with optional mission number.
	changERegex = regexp.MustCompile(`(?i)\bchang['’\-]e(?:\s*-\s*|\s+)?(\d+)?\b`)
)

// Chunk is a preprocessed text segment ready for TTS synthesis along with
// structural boundary metadata for inter-chunk silence injection.
type Chunk struct {
	Text           string
	ParagraphBreak bool
}

// maxChunkRunes keeps each TTS synthesis chunk comfortably within Kokoro-82M's
// ~510-phoneme context window while preserving natural sentence/paragraph breaks.
const maxChunkRunes = 500

// spellOutInitialisms lists uppercase acronyms containing vowels that must still
// be spelled out letter-by-letter rather than pronounced as words by G2P.
var spellOutInitialisms = map[string]bool{
	"ILRS":  true,
	"SLS":   true,
	"ESA":   true,
	"CNSA":  true,
	"CASC":  true,
	"CASIC": true,
	"ISRO":  true,
	"JAXA":  true,
	"CSA":   true,
	"UAESA": true,
	"LEO":   true,
	"MEO":   true,
	"GEO":   true,
	"HEO":   true,
	"SSO":   true,
	"GTO":   true,
	"TLI":   true,
	"LOI":   true,
	"TEI":   true,
	"NRHO":  true,
	"DRO":   true,
	"CLPS":  true,
	"HLS":   true,
	"EVA":   true,
	"IVA":   true,
	"ISS":   true,
	"CSS":   true,
	"ESM":   true,
	"ICPS":  true,
	"EUS":   true,
	"OMS":   true,
	"ISRU":  true,
	"ECLSS": true,
	"USSF":  true,
	"USAF":  true,
	"USN":   true,
	"NRO":   true,
	"NGA":   true,
	"NSA":   true,
	"CIA":   true,
	"FBI":   true,
	"FAA":   true,
	"FCC":   true,
	"SEC":   true,
	"EPA":   true,
	"DOE":   true,
	"DOD":   true,
	"API":   true,
	"CLI":   true,
	"GUI":   true,
	"UI":    true,
	"UX":    true,
	"URL":   true,
	"URI":   true,
	"CPU":   true,
	"GPU":   true,
	"TPU":   true,
	"NPU":   true,
	"APU":   true,
	"FPU":   true,
	"ALU":   true,
	"MMU":   true,
	"FPGA":  true,
	"ASIC":  true,
	"IDE":   true,
	"IAM":   true,
	"OIDC":  true,
	"EOF":   true,
	"SLA":   true,
	"SLO":   true,
	"SLI":   true,
	"SRE":   true,
	"AI":    true,
	"NLU":   true,
	"ASR":   true,
	"ION":   false,
	"EKS":   true,
	"AKS":   true,
	"GKE":   true,
}

// pronounceableAcronyms lists all-caps words that should be pronounced as words, never spelled out.
var pronounceableAcronyms = map[string]bool{
	"NASA":   true,
	"NOAA":   true,
	"NATO":   true,
	"DARPA":  true,
	"ARPA":   true,
	"LASER":  true,
	"RADAR":  true,
	"LIDAR":  true,
	"SONAR":  true,
	"RAM":    true,
	"ROM":    true,
	"REST":   true,
	"JSON":   true,
	"YAML":   true,
	"TOML":   true,
	"SIMD":   true,
	"CUDA":   true,
	"ONNX":   true,
	"CORS":   true,
	"WASM":   true,
	"GRPC":   false,
	"MIME":   true,
	"PING":   true,
	"CRON":   true,
	"FIFO":   true,
	"LIFO":   true,
	"GUID":   true,
	"UUID":   false,
	"JPEG":   true,
	"GIF":    true,
	"ORION":  true,
	"APOLLO": true,
}

type phoneticRule struct {
	re   *regexp.Regexp
	repl string
}

// phoneticOverrides maps foreign or non-standard proper nouns to phonetic
// respellings tailored for the English Kokoro/espeak-ng G2P pipeline.
var phoneticOverrides = []phoneticRule{
	{regexp.MustCompile(`(?i)\blavochkin\b`), "Lah-votch-keen"},
	{regexp.MustCompile(`(?i)\bbaikonur\b`), "Bye-kuh-noor"},
	{regexp.MustCompile(`(?i)\bjiuquan\b`), "Jee-oh-chwahn"},
	{regexp.MustCompile(`(?i)\bwenchang\b`), "Wen-chahng"},
	{regexp.MustCompile(`(?i)\bxichang\b`), "Shee-chahng"},
	{regexp.MustCompile(`(?i)\btaiyuan\b`), "Tie-yoo-ahn"},
	{regexp.MustCompile(`(?i)\btiangong\b`), "Tyen-gong"},
	{regexp.MustCompile(`(?i)\btianwen\b`), "Tyen-wen"},
	{regexp.MustCompile(`(?i)\btianzhou\b`), "Tyen-joh"},
	{regexp.MustCompile(`(?i)\bshenzhou\b`), "Shen-joh"},
	{regexp.MustCompile(`(?i)\bqueqiao\b`), "Chweh-chyow"},
	{regexp.MustCompile(`(?i)\byutu\b`), "Yoo-too"},
	{regexp.MustCompile(`(?i)\bzhurong\b`), "Joo-rong"},
	{regexp.MustCompile(`(?i)\bmengtian\b`), "Mung-tyen"},
	{regexp.MustCompile(`(?i)\bwentian\b`), "Wen-tyen"},
	{regexp.MustCompile(`(?i)\btianhe\b`), "Tyen-huh"},
	{regexp.MustCompile(`(?i)\bxuntian\b`), "Shwin-tyen"},
	{regexp.MustCompile(`(?i)\blongjiang\b`), "Long-jyahng"},
	{regexp.MustCompile(`(?i)\broscosmos\b`), "Ross-koz-moss"},
	{regexp.MustCompile(`(?i)\bsoyuz\b`), "Soy-ooz"},
	{regexp.MustCompile(`(?i)\bnauka\b`), "Now-kuh"},
	{regexp.MustCompile(`(?i)\bprichal\b`), "Pree-chahl"},
	{regexp.MustCompile(`(?i)\bzvezda\b`), "Zvez-dah"},
	{regexp.MustCompile(`(?i)\bzarya\b`), "Zahr-yah"},
	{regexp.MustCompile(`(?i)\bfregat\b`), "Freh-gaht"},
	{regexp.MustCompile(`(?i)\bvostochny\b`), "Voss-toch-nee"},
	{regexp.MustCompile(`(?i)\bplesetsk\b`), "Pleh-setsk"},
	{regexp.MustCompile(`(?i)\bkourou\b`), "Koo-roo"},
	{regexp.MustCompile(`(?i)\barianespace\b`), "Ah-ree-ahn-spahss"},
	{regexp.MustCompile(`(?i)\bariane\b`), "Ah-ree-ahn"},
	{regexp.MustCompile(`(?i)\btanegashima\b`), "Tah-neh-gah-shee-mah"},
	{regexp.MustCompile(`(?i)\bsriharikota\b`), "Sree-hah-ree-koh-tah"},
	{regexp.MustCompile(`(?i)\bchandrayaan\b`), "Chuhn-druh-yahn"},
	{regexp.MustCompile(`(?i)\bgaganyaan\b`), "Guh-guhn-yahn"},
	{regexp.MustCompile(`(?i)\btsniimash\b`), "Tsnee-mahsh"},
	{regexp.MustCompile(`(?i)\bkhrunichev\b`), "Khroo-nee-chev"},
	{regexp.MustCompile(`(?i)\breshetnev\b`), "Reh-shet-nyev"},
}

// nonSplittingAbbreviations lists lowercase tokens (without trailing period)
// after which a period does NOT end a sentence.
var nonSplittingAbbreviations = map[string]bool{
	"mr": true, "mrs": true, "ms": true, "dr": true, "prof": true,
	"sr": true, "jr": true, "st": true, "mt": true, "ft": true,
	"gen": true, "col": true, "maj": true, "capt": true, "lt": true, "sgt": true,
	"vs": true, "etc": true, "eg": true, "e.g": true, "ie": true, "i.e": true,
	"cf": true, "al": true, "fig": true, "figs": true, "no": true, "nos": true,
	"vol": true, "vols": true, "pt": true, "pts": true, "ch": true, "sec": true,
	"inc": true, "ltd": true, "corp": true, "co": true,
	"jan": true, "feb": true, "mar": true, "apr": true, "jun": true, "jul": true,
	"aug": true, "sep": true, "sept": true, "oct": true, "nov": true, "dec": true,
	"approx": true, "est": true, "dept": true, "gov": true, "u.s": true, "u.k": true, "e.u": true,
}

// Preprocess strips SSML and Markdown formatting, normalizes acronyms, proper
// nouns, and structural headers, and splits text into paragraph- and
// sentence-aware TTS chunks.
func Preprocess(text string) []string {
	chunks := PreprocessWithBreaks(text)
	if len(chunks) == 0 {
		return nil
	}
	out := make([]string, len(chunks))
	for i, c := range chunks {
		out[i] = c.Text
	}
	return out
}

// PreprocessWithBreaks performs full text normalization and returns chunks annotated
// with paragraph/section break boundaries for silence injection.
func PreprocessWithBreaks(text string) []Chunk {
	text = ssmlTag.ReplaceAllString(text, " ")
	text = html.UnescapeString(text)
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = cleanMarkdown(text)
	text = normalizeStructuralHeaders(text)
	text = applyPhoneticOverrides(text)
	text = normalizeInitialisms(text)
	text = multiSpace.ReplaceAllString(text, " ")
	text = multiNL.ReplaceAllString(text, "\n\n")
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	return chunkParagraphsWithBreaks(text, maxChunkRunes)
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
		if m := structuralHeaderTitle.FindStringSubmatch(h); len(m) == 3 {
			h = m[1] + ": " + m[2]
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

func normalizeStructuralHeaders(text string) string {
	return structuralHeaderInline.ReplaceAllStringFunc(text, func(match string) string {
		sub := structuralHeaderInline.FindStringSubmatch(match)
		if len(sub) < 5 {
			return match
		}
		prefix := sub[1]
		partLabel := strings.TrimSpace(sub[2])
		topic := strings.TrimSpace(sub[3])
		nextWord := sub[4]
		return prefix + partLabel + ": " + topic + ". ... " + nextWord
	})
}

func applyPhoneticOverrides(text string) string {
	text = changERegex.ReplaceAllStringFunc(text, func(m string) string {
		sub := changERegex.FindStringSubmatch(m)
		if len(sub) >= 2 && sub[1] != "" {
			return "Chahng-uh " + sub[1]
		}
		return "Chahng-uh"
	})
	for _, rule := range phoneticOverrides {
		text = rule.re.ReplaceAllString(text, rule.repl)
	}
	return text
}

func normalizeInitialisms(text string) string {
	return initialismToken.ReplaceAllStringFunc(text, func(tok string) string {
		sub := initialismToken.FindStringSubmatch(tok)
		if len(sub) < 3 {
			return tok
		}
		base := sub[1]
		plural := sub[2]
		if pronounceableAcronyms[base] {
			return tok
		}
		if spellOutInitialisms[base] || isConsonantInitialism(base) {
			letters := strings.Join(strings.Split(base, ""), " ")
			if plural != "" {
				return letters + " s"
			}
			return letters
		}
		return tok
	})
}

func isConsonantInitialism(s string) bool {
	if len(s) < 2 || len(s) > 5 {
		return false
	}
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case 'A', 'E', 'I', 'O', 'U', 'Y':
			return false
		}
	}
	return true
}

func chunkParagraphsWithBreaks(text string, maxRunes int) []Chunk {
	paragraphs := strings.Split(text, "\n\n")
	var raw []Chunk
	for _, p := range paragraphs {
		p = strings.TrimSpace(strings.ReplaceAll(p, "\n", " "))
		p = multiSpace.ReplaceAllString(p, " ")
		if p == "" {
			continue
		}
		parts := chunk(p, maxRunes)
		for i, part := range parts {
			raw = append(raw, Chunk{
				Text:           part,
				ParagraphBreak: i == len(parts)-1,
			})
		}
	}
	return coalesceChunksWithBreaks(raw, maxRunes)
}

func coalesceChunksWithBreaks(chunks []Chunk, maxRunes int) []Chunk {
	if len(chunks) <= 1 {
		return chunks
	}
	var out []Chunk
	var cur Chunk
	for _, c := range chunks {
		c.Text = strings.TrimSpace(c.Text)
		if c.Text == "" {
			continue
		}
		n := utf8.RuneCountInString(c.Text)
		if cur.Text == "" {
			cur = c
			continue
		}
		withPeriod := ensureSentenceEnd(cur.Text)
		withPeriodRunes := utf8.RuneCountInString(withPeriod)
		if withPeriodRunes+1+n <= maxRunes {
			cur.Text = withPeriod + " " + c.Text
			cur.ParagraphBreak = c.ParagraphBreak
		} else {
			out = append(out, cur)
			cur = c
		}
	}
	if cur.Text != "" {
		out = append(out, cur)
	}
	return out
}

func ensureSentenceEnd(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	last, _ := utf8.DecodeLastRuneInString(s)
	if !isSentenceEnd(last) && last != ':' && last != ';' {
		return s + "."
	}
	return s
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
			for _, part := range splitClausesOrHard(sentence, maxRunes) {
				partLen := utf8.RuneCountInString(part)
				if runes > 0 && runes+1+partLen > maxRunes {
					flush()
				}
				if buf.Len() > 0 {
					buf.WriteByte(' ')
					runes++
				}
				buf.WriteString(part)
				runes += partLen
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
		r := runes[i]
		buf.WriteRune(r)
		if !isSentenceEnd(r) {
			continue
		}
		// Keep consecutive sentence punctuation (e.g. "..." or "?!") attached.
		if i+1 < len(runes) && isSentenceEnd(runes[i+1]) {
			continue
		}
		if i+1 >= len(runes) || !(unicode.IsSpace(runes[i+1]) || runes[i+1] == '\n') {
			continue
		}
		current := strings.TrimSpace(buf.String())
		if r == '.' && shouldNotSplitAtPeriod(current) {
			continue
		}
		out = append(out, current)
		buf.Reset()
	}
	if s := strings.TrimSpace(buf.String()); s != "" {
		out = append(out, s)
	}
	if len(out) == 0 {
		return []string{text}
	}
	return out
}

func shouldNotSplitAtPeriod(segmentWithTrailingDot string) bool {
	if strings.HasSuffix(segmentWithTrailingDot, "...") {
		return true
	}
	withoutDot := strings.TrimSuffix(segmentWithTrailingDot, ".")
	fields := strings.Fields(withoutDot)
	if len(fields) == 0 {
		return false
	}
	lastWord := strings.TrimLeft(fields[len(fields)-1], `"'([{`)
	lower := strings.ToLower(lastWord)
	if nonSplittingAbbreviations[lower] {
		return true
	}
	// Single-letter initial (e.g. "J." or "U.S.")
	if utf8.RuneCountInString(lastWord) == 1 && unicode.IsUpper([]rune(lastWord)[0]) {
		return true
	}
	return false
}

func isSentenceEnd(r rune) bool {
	switch r {
	case '.', '!', '?', '。', '！', '？':
		return true
	}
	return false
}

// splitClausesOrHard splits an oversized sentence at natural clause delimiters
// ("; ", " — ", ": ", ", ") or word boundaries before falling back to splitHard.
func splitClausesOrHard(s string, maxRunes int) []string {
	if utf8.RuneCountInString(s) <= maxRunes {
		return []string{s}
	}
	for _, sep := range []string{"; ", " — ", ": ", ", ", " "} {
		if !strings.Contains(s, sep) {
			continue
		}
		tokens := strings.Split(s, sep)
		var out []string
		var cur string
		for i, tok := range tokens {
			piece := tok
			if i < len(tokens)-1 && sep != " " {
				piece += strings.TrimRight(sep, " ")
			}
			piece = strings.TrimSpace(piece)
			if piece == "" {
				continue
			}
			if utf8.RuneCountInString(piece) > maxRunes {
				if cur != "" {
					out = append(out, cur)
					cur = ""
				}
				if sep == " " {
					out = append(out, splitHard(piece, maxRunes)...)
				} else {
					out = append(out, splitClausesOrHard(piece, maxRunes)...)
				}
				continue
			}
			if cur == "" {
				cur = piece
			} else if utf8.RuneCountInString(cur)+1+utf8.RuneCountInString(piece) <= maxRunes {
				cur += " " + piece
			} else {
				out = append(out, cur)
				cur = piece
			}
		}
		if cur != "" {
			out = append(out, cur)
		}
		if len(out) > 1 {
			return out
		}
	}
	return splitHard(s, maxRunes)
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
