package tts

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeExecutable(t *testing.T, path, script string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestPiperEngineSynthesizeAndVoiceResolution(t *testing.T) {
	dir := t.TempDir()
	argsLog := filepath.Join(dir, "piper_args.log")
	piperBin := filepath.Join(dir, "fake-piper")
	ffmpegBin := filepath.Join(dir, "fake-ffmpeg")

	writeExecutable(t, piperBin, `#!/bin/sh
echo "$@" >> "`+argsLog+`"
out=""
while [ $# -gt 0 ]; do
  if [ "$1" = "--output_file" ]; then
    out="$2"
    shift 2
  else
    shift
  fi
done
cat >/dev/null
printf "RIFFfakewavdata" > "$out"
`)

	writeExecutable(t, ffmpegBin, `#!/bin/sh
for last; do true; done
printf "ID3fakemp3data" > "$last"
`)

	modelsDir := filepath.Join(dir, "models")
	if err := os.MkdirAll(modelsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	defaultModel := filepath.Join(modelsDir, "en_US-lessac-medium.onnx")
	defaultCfg := defaultModel + ".json"

	eng := &PiperEngine{
		Bin:          piperBin,
		Model:        defaultModel,
		Config:       defaultCfg,
		FFmpegBin:    ffmpegBin,
		DefaultVoice: "en_US-lessac-medium",
	}

	t.Run("voice basename resolution", func(t *testing.T) {
		_ = os.Remove(argsLog)
		res, err := eng.Synthesize(context.Background(), "Hello world. Second sentence.", "en_US-amy-medium")
		if err != nil {
			t.Fatalf("Synthesize failed: %v", err)
		}
		data, err := io.ReadAll(res.Reader)
		if err != nil {
			t.Fatal(err)
		}
		if err := res.Close(); err != nil {
			t.Fatal(err)
		}
		if string(data) != "ID3fakemp3data" {
			t.Fatalf("unexpected mp3 data: %q", string(data))
		}
		logged, err := os.ReadFile(argsLog)
		if err != nil {
			t.Fatal(err)
		}
		wantModel := filepath.Join(modelsDir, "en_US-amy-medium.onnx")
		wantCfg := wantModel + ".json"
		if !strings.Contains(string(logged), "--model "+wantModel) {
			t.Fatalf("expected --model %s in args, got %q", wantModel, string(logged))
		}
		if !strings.Contains(string(logged), "--config "+wantCfg) {
			t.Fatalf("expected --config %s in args, got %q", wantCfg, string(logged))
		}
	})

	t.Run("rejects path traversal in voiceID", func(t *testing.T) {
		if _, err := eng.Synthesize(context.Background(), "Hello world.", "../evil"); err == nil {
			t.Fatal("expected error for traversal voiceID")
		}
	})
}
