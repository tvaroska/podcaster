package tts

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// PiperEngine shells out to the Piper TTS binary, then encodes WAV → MP3.
type PiperEngine struct {
	Bin          string
	Model        string
	Config       string
	FFmpegBin    string
	DefaultVoice string
}

func (p *PiperEngine) Synthesize(ctx context.Context, text, voiceID string) (*Result, error) {
	chunks := Preprocess(text)
	if len(chunks) == 0 {
		return nil, fmt.Errorf("empty text after preprocessing")
	}
	model := p.Model
	cfg := p.Config
	if voiceID != "" && voiceID != p.DefaultVoice {
		// Allow a voice_id to be an alternate model path or basename.
		if strings.Contains(voiceID, string(os.PathSeparator)) || strings.HasSuffix(voiceID, ".onnx") {
			model = voiceID
			cfg = strings.TrimSuffix(model, ".onnx") + ".onnx.json"
			if _, err := os.Stat(cfg); err != nil {
				cfg = ""
			}
		}
	}

	tmp, err := os.MkdirTemp("", "podcaster-piper-*")
	if err != nil {
		return nil, err
	}
	wavs := make([]string, 0, len(chunks))
	for i, chunk := range chunks {
		wav := filepath.Join(tmp, fmt.Sprintf("part-%04d.wav", i))
		if err := p.runPiper(ctx, chunk, model, cfg, wav); err != nil {
			_ = os.RemoveAll(tmp)
			return nil, err
		}
		wavs = append(wavs, wav)
	}
	combined := filepath.Join(tmp, "combined.wav")
	if err := ConcatWAV(ctx, p.FFmpegBin, wavs, combined); err != nil {
		_ = os.RemoveAll(tmp)
		return nil, err
	}
	mp3 := filepath.Join(tmp, "episode.mp3")
	if err := EncodeMP3(ctx, p.FFmpegBin, combined, mp3); err != nil {
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

func (p *PiperEngine) runPiper(ctx context.Context, text, model, cfg, wav string) error {
	args := []string{"--model", model, "--output_file", wav}
	if cfg != "" {
		args = append(args, "--config", cfg)
	}
	cmd := exec.CommandContext(ctx, p.Bin, args...)
	cmd.Stdin = strings.NewReader(text)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("piper: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}
	st, err := os.Stat(wav)
	if err != nil {
		return fmt.Errorf("piper produced no wav: %w", err)
	}
	if st.Size() == 0 {
		return fmt.Errorf("piper produced empty wav")
	}
	return nil
}

type tmpFile struct {
	*os.File
	dir string
}

func (t *tmpFile) Close() error {
	err := t.File.Close()
	_ = os.RemoveAll(t.dir)
	return err
}
