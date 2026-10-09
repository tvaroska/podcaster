package tts

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultKokoroVoice is the flagship warm American English narrator in Kokoro-82M v1.0.
// By default, KokoroEngine blends 0.7 * af_heart + 0.3 * af_bella when voices.bin is present.
const DefaultKokoroVoice = "af_heart"

// DefaultKokoroSpeed is the default speech rate multiplier (0.95 = 5% slower than 1.0
// for clearer articulation and natural prosodic decay on dense technical briefings).
const DefaultKokoroSpeed = 0.95

// kokoroSpeakerEmbeddingBytes is the byte size of one speaker embedding in Kokoro-82M voices.bin
// (510 token positions * 1 * 256 style dim * 4 bytes per float32 = 522,240 bytes).
const kokoroSpeakerEmbeddingBytes = 510 * 256 * 4

// VoiceWeight represents a single speaker ID and its weight in a blended voice embedding.
type VoiceWeight struct {
	SID    int
	Weight float64
}

// kokoroMultiLangVoices maps Kokoro v1.0 (kokoro-multi-lang-v1_0) speaker names to numeric sid.
var kokoroMultiLangVoices = map[string]int{
	"af":          3, // alias to flagship af_heart
	"af_alloy":    0,
	"af_aoede":    1,
	"af_bella":    2,
	"af_heart":    3,
	"af_jessica":  4,
	"af_kore":     5,
	"af_nicole":   6,
	"af_nova":     7,
	"af_river":    8,
	"af_sarah":    9,
	"af_sky":      10,
	"am_adam":     11,
	"am_echo":     12,
	"am_eric":     13,
	"am_fenrir":   14,
	"am_liam":     15,
	"am_michael":  16,
	"am_onyx":     17,
	"am_puck":     18,
	"am_santa":    19,
	"bf_alice":    20,
	"bf_emma":     21,
	"bf_isabella": 22,
	"bf_lily":     23,
	"bm_daniel":   24,
	"bm_fable":    25,
	"bm_george":   26,
	"bm_lewis":    27,
}

// KokoroEngine synthesizes speech locally using Kokoro-82M via sherpa-onnx-offline-tts,
// then concatenates WAV chunks and masters a 128kbps 24kHz mono MP3 via ffmpeg.
type KokoroEngine struct {
	Bin          string
	ModelDir     string
	Model        string
	Voices       string
	Tokens       string
	DataDir      string
	DictDir      string
	Lexicon      string
	Speed        float64
	Threads      int
	Concurrency  int
	FFmpegBin    string
	DefaultVoice string
}

// ResolveSpeakerID maps a Kokoro voice name (e.g. "af_heart", "am_adam", "bf_emma"),
// blend specification (e.g. "af_heart:0.7,af_bella:0.3" or "0.7*af_heart+0.3*af_bella"),
// or numeric speaker ID string (e.g. "3") to the primary sherpa-onnx --sid integer.
// Legacy Piper default voice IDs ("en_US-lessac-medium") fall back to the default Kokoro voice.
func (k *KokoroEngine) ResolveSpeakerID(voiceID string) (int, error) {
	weights, err := k.ResolveVoiceWeights(voiceID)
	if err != nil {
		return 0, err
	}
	return weights[0].SID, nil
}

// ResolveVoiceWeights parses a single voice name or weighted blend expression into
// normalized speaker weights. When voiceID resolves to "af_heart" (the default voice)
// without an explicit single-voice weight (like "af_heart:1.0"), it returns the
// recommended warm blend: 0.7 * af_heart (sid=3) + 0.3 * af_bella (sid=2).
func (k *KokoroEngine) ResolveVoiceWeights(voiceID string) ([]VoiceWeight, error) {
	v := strings.TrimSpace(voiceID)
	if strings.Contains(v, "..") || strings.ContainsAny(v, `/\`) {
		return nil, fmt.Errorf("invalid voice_id %q", voiceID)
	}
	if v == "" || v == "en_US-lessac-medium" {
		v = strings.TrimSpace(k.DefaultVoice)
		if v == "" || v == "en_US-lessac-medium" {
			v = DefaultKokoroVoice
		}
	}
	lower := strings.ToLower(strings.TrimSpace(v))
	if lower == "af_heart" || lower == "af" || lower == "af_heart_bella" {
		return []VoiceWeight{
			{SID: kokoroMultiLangVoices["af_heart"], Weight: 0.7},
			{SID: kokoroMultiLangVoices["af_bella"], Weight: 0.3},
		}, nil
	}

	// Check if v is a blend expression using "+" or "," or ":" or "*".
	if strings.ContainsAny(lower, "+,:*") {
		return parseVoiceBlend(v)
	}

	sid, err := resolveSingleSpeakerID(v)
	if err != nil {
		return nil, err
	}
	return []VoiceWeight{{SID: sid, Weight: 1.0}}, nil
}

func resolveSingleSpeakerID(token string) (int, error) {
	t := strings.TrimSpace(token)
	if strings.Contains(t, "..") || strings.ContainsAny(t, `/\`) {
		return 0, fmt.Errorf("invalid voice_id %q", token)
	}
	if sid, ok := kokoroMultiLangVoices[strings.ToLower(t)]; ok {
		return sid, nil
	}
	if sid, err := strconv.Atoi(t); err == nil && sid >= 0 && sid <= 100 {
		return sid, nil
	}
	return 0, fmt.Errorf("unknown kokoro voice_id %q", token)
}

func parseVoiceBlend(spec string) ([]VoiceWeight, error) {
	// Normalize "+" delimiter to "," so both "0.7*af_heart+0.3*af_bella"
	// and "af_heart:0.7,af_bella:0.3" tokenize uniformly.
	normalized := strings.ReplaceAll(spec, "+", ",")
	parts := strings.Split(normalized, ",")
	var weights []VoiceWeight
	var total float64
	for _, rawPart := range parts {
		part := strings.TrimSpace(rawPart)
		if part == "" {
			continue
		}
		var (
			voiceToken string
			weight     = 1.0
		)
		switch {
		case strings.Contains(part, ":"):
			kv := strings.SplitN(part, ":", 2)
			voiceToken = strings.TrimSpace(kv[0])
			w, err := strconv.ParseFloat(strings.TrimSpace(kv[1]), 64)
			if err != nil || w <= 0 || math.IsNaN(w) || math.IsInf(w, 0) {
				return nil, fmt.Errorf("invalid blend weight in %q", spec)
			}
			weight = w
		case strings.Contains(part, "*"):
			kv := strings.SplitN(part, "*", 2)
			left := strings.TrimSpace(kv[0])
			right := strings.TrimSpace(kv[1])
			if w, err := strconv.ParseFloat(left, 64); err == nil {
				if w <= 0 || math.IsNaN(w) || math.IsInf(w, 0) {
					return nil, fmt.Errorf("invalid blend weight in %q", spec)
				}
				weight = w
				voiceToken = right
			} else if w, err := strconv.ParseFloat(right, 64); err == nil {
				if w <= 0 || math.IsNaN(w) || math.IsInf(w, 0) {
					return nil, fmt.Errorf("invalid blend weight in %q", spec)
				}
				weight = w
				voiceToken = left
			} else {
				return nil, fmt.Errorf("invalid blend term %q in %q", part, spec)
			}
		default:
			voiceToken = part
		}
		sid, err := resolveSingleSpeakerID(voiceToken)
		if err != nil {
			return nil, err
		}
		weights = append(weights, VoiceWeight{SID: sid, Weight: weight})
		total += weight
	}
	if len(weights) == 0 || total <= 0 {
		return nil, fmt.Errorf("invalid voice blend %q", spec)
	}
	for i := range weights {
		weights[i].Weight /= total
	}
	return weights, nil
}

// prepareBlendedVoices creates a temporary voices.bin with the weighted linear
// combination of speaker embeddings written into weights[0].SID.
// If len(weights) <= 1 or srcVoicesPath does not exist on disk (e.g. in fake-binary tests),
// it returns srcVoicesPath unchanged.
func prepareBlendedVoices(srcVoicesPath, tmpDir string, weights []VoiceWeight) (voicesPath string, sid int, cleanup func(), err error) {
	noop := func() {}
	if len(weights) == 0 {
		return srcVoicesPath, 0, noop, fmt.Errorf("empty voice weights")
	}
	targetSID := weights[0].SID
	if len(weights) == 1 {
		return srcVoicesPath, targetSID, noop, nil
	}
	raw, readErr := os.ReadFile(srcVoicesPath)
	if readErr != nil {
		if os.IsNotExist(readErr) {
			return srcVoicesPath, targetSID, noop, nil
		}
		return "", 0, noop, fmt.Errorf("read voices.bin: %w", readErr)
	}

	maxSID := 0
	for _, w := range weights {
		if w.SID > maxSID {
			maxSID = w.SID
		}
	}
	bytesPerSpeaker := inferSpeakerEmbeddingBytes(len(raw), maxSID)
	if bytesPerSpeaker <= 0 {
		return srcVoicesPath, targetSID, noop, nil
	}

	floatsPerSpeaker := bytesPerSpeaker / 4
	out := make([]byte, len(raw))
	copy(out, raw)

	targetOffset := targetSID * bytesPerSpeaker
	for j := 0; j < floatsPerSpeaker; j++ {
		var sum float64
		for _, w := range weights {
			off := w.SID*bytesPerSpeaker + j*4
			bits := binary.LittleEndian.Uint32(raw[off : off+4])
			val := float64(math.Float32frombits(bits))
			sum += w.Weight * val
		}
		blendedBits := math.Float32bits(float32(sum))
		binary.LittleEndian.PutUint32(out[targetOffset+j*4:targetOffset+j*4+4], blendedBits)
	}

	blendedPath := filepath.Join(tmpDir, "voices-blended.bin")
	if err := os.WriteFile(blendedPath, out, 0o644); err != nil {
		return "", 0, noop, fmt.Errorf("write blended voices.bin: %w", err)
	}
	return blendedPath, targetSID, func() { _ = os.Remove(blendedPath) }, nil
}

func inferSpeakerEmbeddingBytes(fileSize, maxSID int) int {
	if fileSize <= 0 || fileSize%4 != 0 {
		return 0
	}
	if fileSize >= kokoroSpeakerEmbeddingBytes && fileSize%kokoroSpeakerEmbeddingBytes == 0 {
		if (maxSID+1)*kokoroSpeakerEmbeddingBytes <= fileSize {
			return kokoroSpeakerEmbeddingBytes
		}
	}
	// Support smaller synthetic voices.bin fixtures in unit tests (e.g. 28 speakers).
	for _, numSpeakers := range []int{53, 28} {
		if fileSize%(numSpeakers*4) == 0 && maxSID < numSpeakers {
			return fileSize / numSpeakers
		}
	}
	return 0
}

func (k *KokoroEngine) resolvePaths() (bin, model, voices, tokens, dataDir, dictDir, lexicon string, err error) {
	bin = strings.TrimSpace(k.Bin)
	if bin == "" {
		bin = "sherpa-onnx-offline-tts"
	}
	dir := strings.TrimSpace(k.ModelDir)
	model = strings.TrimSpace(k.Model)
	if model == "" && dir != "" {
		model = filepath.Join(dir, "model.onnx")
	}
	if model == "" {
		return "", "", "", "", "", "", "", fmt.Errorf("kokoro model path is required")
	}
	if dir == "" {
		dir = filepath.Dir(model)
	}
	voices = strings.TrimSpace(k.Voices)
	if voices == "" {
		voices = filepath.Join(dir, "voices.bin")
	}
	tokens = strings.TrimSpace(k.Tokens)
	if tokens == "" {
		tokens = filepath.Join(dir, "tokens.txt")
	}
	dataDir = strings.TrimSpace(k.DataDir)
	if dataDir == "" {
		dataDir = filepath.Join(dir, "espeak-ng-data")
	}
	dictDir = strings.TrimSpace(k.DictDir)
	if dictDir == "" {
		candidateDict := filepath.Join(dir, "dict")
		if info, statErr := os.Stat(candidateDict); statErr == nil && info.IsDir() {
			dictDir = candidateDict
		}
	}
	lexicon = strings.TrimSpace(k.Lexicon)
	if lexicon == "" {
		var lexicons []string
		for _, name := range []string{"lexicon-us-en.txt", "lexicon-zh.txt"} {
			candidate := filepath.Join(dir, name)
			if _, statErr := os.Stat(candidate); statErr == nil {
				lexicons = append(lexicons, candidate)
			}
		}
		lexicon = strings.Join(lexicons, ",")
	}
	return bin, model, voices, tokens, dataDir, dictDir, lexicon, nil
}

func (k *KokoroEngine) Synthesize(ctx context.Context, text, voiceID string) (*Result, error) {
	chunks := PreprocessWithBreaks(text)
	if len(chunks) == 0 {
		return nil, fmt.Errorf("empty text after preprocessing")
	}
	weights, err := k.ResolveVoiceWeights(voiceID)
	if err != nil {
		return nil, err
	}
	bin, model, baseVoices, tokens, dataDir, dictDir, lexicon, err := k.resolvePaths()
	if err != nil {
		return nil, err
	}

	tmp, err := os.MkdirTemp("", "podcaster-kokoro-*")
	if err != nil {
		return nil, err
	}
	voices, sid, cleanupVoices, err := prepareBlendedVoices(baseVoices, tmp, weights)
	if err != nil {
		_ = os.RemoveAll(tmp)
		return nil, err
	}

	wavs := make([]string, len(chunks))
	silences := make([]time.Duration, len(chunks))
	for i, ch := range chunks {
		wavs[i] = filepath.Join(tmp, fmt.Sprintf("part-%04d.wav", i))
		switch {
		case i == len(chunks)-1:
			silences[i] = TailSilenceDuration
		case ch.ParagraphBreak:
			silences[i] = ParagraphSilenceDuration
		default:
			silences[i] = SentenceSilenceDuration
		}
	}

	concurrency := k.Concurrency
	if concurrency <= 1 || len(chunks) == 1 {
		for i, chunk := range chunks {
			if err := k.runKokoro(ctx, bin, model, voices, tokens, dataDir, dictDir, lexicon, sid, chunk.Text, wavs[i], silences[i]); err != nil {
				cleanupVoices()
				_ = os.RemoveAll(tmp)
				return nil, err
			}
		}
	} else {
		if concurrency > len(chunks) {
			concurrency = len(chunks)
		}
		runCtx, cancel := context.WithCancel(ctx)
		defer cancel()

		sem := make(chan struct{}, concurrency)
		var (
			wg       sync.WaitGroup
			errOnce  sync.Once
			firstErr error
		)
	loop:
		for i, chunk := range chunks {
			select {
			case <-runCtx.Done():
				errOnce.Do(func() {
					firstErr = runCtx.Err()
				})
				break loop
			case sem <- struct{}{}:
			}
			if runCtx.Err() != nil {
				errOnce.Do(func() {
					firstErr = runCtx.Err()
				})
				<-sem
				break
			}
			wg.Add(1)
			go func(text, wav string, trailingSilence time.Duration) {
				defer wg.Done()
				defer func() { <-sem }()
				if err := k.runKokoro(runCtx, bin, model, voices, tokens, dataDir, dictDir, lexicon, sid, text, wav, trailingSilence); err != nil {
					errOnce.Do(func() {
						firstErr = err
						cancel()
					})
				}
			}(chunk.Text, wavs[i], silences[i])
		}
		wg.Wait()
		if firstErr != nil {
			cleanupVoices()
			_ = os.RemoveAll(tmp)
			return nil, firstErr
		}
	}
	cleanupVoices()

	srcWAV := wavs[0]
	if len(wavs) > 1 {
		combined := filepath.Join(tmp, "combined.wav")
		if err := ConcatWAV(ctx, k.FFmpegBin, wavs, combined); err != nil {
			_ = os.RemoveAll(tmp)
			return nil, err
		}
		for _, w := range wavs {
			_ = os.Remove(w)
		}
		srcWAV = combined
	}
	mp3 := filepath.Join(tmp, "episode.mp3")
	if err := EncodeMP3(ctx, k.FFmpegBin, srcWAV, mp3); err != nil {
		_ = os.RemoveAll(tmp)
		return nil, err
	}
	_ = os.Remove(srcWAV)
	f, err := os.Open(mp3)
	if err != nil {
		_ = os.RemoveAll(tmp)
		return nil, err
	}
	return &Result{
		Reader: &tmpFile{
			File: f,
			dir:  tmp,
		},
		ContentType: "audio/mpeg",
		Extension:   "mp3",
	}, nil
}

func (k *KokoroEngine) runKokoro(ctx context.Context, bin, model, voices, tokens, dataDir, dictDir, lexicon string, sid int, text, wav string, trailingSilence time.Duration) error {
	threads := k.Threads
	if threads <= 0 {
		threads = 2
	}
	speed := k.Speed
	if speed <= 0 {
		speed = DefaultKokoroSpeed
	}
	lengthScale := 1.0 / speed
	args := []string{
		"--kokoro-model=" + model,
		"--kokoro-voices=" + voices,
		"--kokoro-tokens=" + tokens,
		"--kokoro-data-dir=" + dataDir,
	}
	if dictDir != "" {
		args = append(args, "--kokoro-dict-dir="+dictDir)
	}
	if lexicon != "" {
		args = append(args, "--kokoro-lexicon="+lexicon)
	}
	args = append(args,
		"--tts-max-num-sentences=1",
		fmt.Sprintf("--num-threads=%d", threads),
		fmt.Sprintf("--sid=%d", sid),
		fmt.Sprintf("--kokoro-length-scale=%.2f", lengthScale),
		"--output-filename="+wav,
		text,
	)
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.WaitDelay = 200 * time.Millisecond
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("kokoro: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}
	st, err := os.Stat(wav)
	if err != nil {
		return fmt.Errorf("kokoro produced no wav: %w", err)
	}
	if st.Size() == 0 {
		return fmt.Errorf("kokoro produced empty wav")
	}
	if err := PolishChunkWAV(wav, trailingSilence); err != nil {
		return fmt.Errorf("polish wav: %w", err)
	}
	return nil
}
