package tts

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// DefaultKokoroVoice is the flagship warm American English narrator in Kokoro-82M v1.0.
const DefaultKokoroVoice = "af_heart"

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
	FFmpegBin    string
	DefaultVoice string
}

// ResolveSpeakerID maps a Kokoro voice name (e.g. "af_heart", "am_adam", "bf_emma")
// or numeric speaker ID string (e.g. "3") to a sherpa-onnx --sid integer.
// Legacy Piper default voice IDs ("en_US-lessac-medium") fall back to the default Kokoro voice.
func (k *KokoroEngine) ResolveSpeakerID(voiceID string) (int, error) {
	v := strings.TrimSpace(voiceID)
	if strings.Contains(v, "..") || strings.ContainsAny(v, `/\`) {
		return 0, fmt.Errorf("invalid voice_id %q", voiceID)
	}
	if v == "" || v == "en_US-lessac-medium" {
		v = strings.TrimSpace(k.DefaultVoice)
		if v == "" || v == "en_US-lessac-medium" {
			v = DefaultKokoroVoice
		}
	}
	if sid, ok := kokoroMultiLangVoices[strings.ToLower(v)]; ok {
		return sid, nil
	}
	if sid, err := strconv.Atoi(v); err == nil && sid >= 0 && sid <= 100 {
		return sid, nil
	}
	return 0, fmt.Errorf("unknown kokoro voice_id %q", voiceID)
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
	chunks := Preprocess(text)
	if len(chunks) == 0 {
		return nil, fmt.Errorf("empty text after preprocessing")
	}
	sid, err := k.ResolveSpeakerID(voiceID)
	if err != nil {
		return nil, err
	}
	bin, model, voices, tokens, dataDir, dictDir, lexicon, err := k.resolvePaths()
	if err != nil {
		return nil, err
	}

	tmp, err := os.MkdirTemp("", "podcaster-kokoro-*")
	if err != nil {
		return nil, err
	}
	wavs := make([]string, 0, len(chunks))
	for i, chunk := range chunks {
		wav := filepath.Join(tmp, fmt.Sprintf("part-%04d.wav", i))
		if err := k.runKokoro(ctx, bin, model, voices, tokens, dataDir, dictDir, lexicon, sid, chunk, wav); err != nil {
			_ = os.RemoveAll(tmp)
			return nil, err
		}
		wavs = append(wavs, wav)
	}
	combined := filepath.Join(tmp, "combined.wav")
	if err := ConcatWAV(ctx, k.FFmpegBin, wavs, combined); err != nil {
		_ = os.RemoveAll(tmp)
		return nil, err
	}
	mp3 := filepath.Join(tmp, "episode.mp3")
	if err := EncodeMP3(ctx, k.FFmpegBin, combined, mp3); err != nil {
		_ = os.RemoveAll(tmp)
		return nil, err
	}
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

func (k *KokoroEngine) runKokoro(ctx context.Context, bin, model, voices, tokens, dataDir, dictDir, lexicon string, sid int, text, wav string) error {
	threads := k.Threads
	if threads <= 0 {
		threads = 2
	}
	lengthScale := 1.0
	if k.Speed > 0 {
		lengthScale = 1.0 / k.Speed
	}
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
		fmt.Sprintf("--num-threads=%d", threads),
		fmt.Sprintf("--sid=%d", sid),
		fmt.Sprintf("--kokoro-length-scale=%.2f", lengthScale),
		"--output-filename="+wav,
		text,
	)
	cmd := exec.CommandContext(ctx, bin, args...)
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
	return nil
}
