package tts

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKokoroEngineSynthesizeAndVoiceResolution(t *testing.T) {
	dir := t.TempDir()
	argsLog := filepath.Join(dir, "kokoro_args.log")
	kokoroBin := filepath.Join(dir, "fake-kokoro")
	ffmpegBin := filepath.Join(dir, "fake-ffmpeg")

	writeExecutable(t, kokoroBin, `#!/bin/sh
echo "$@" >> "`+argsLog+`"
out=""
for arg in "$@"; do
  case "$arg" in
    --output-filename=*)
      out="${arg#--output-filename=}"
      ;;
  esac
done
printf "RIFFfakewavdata" > "$out"
`)

	writeExecutable(t, ffmpegBin, `#!/bin/sh
for last; do true; done
printf "ID3fakemp3data" > "$last"
`)

	modelDir := filepath.Join(dir, "kokoro-multi-lang-v1_0")
	if err := os.MkdirAll(filepath.Join(modelDir, "espeak-ng-data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(modelDir, "dict"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "lexicon-us-en.txt"), []byte("hello h ə l ˈoʊ\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "lexicon-zh.txt"), []byte("你好 n i3 h ao3\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	eng := &KokoroEngine{
		Bin:          kokoroBin,
		ModelDir:     modelDir,
		Speed:        1.0,
		Threads:      2,
		FFmpegBin:    ffmpegBin,
		DefaultVoice: DefaultKokoroVoice,
	}

	t.Run("default voice af_heart and lexicon auto-detection", func(t *testing.T) {
		_ = os.Remove(argsLog)
		res, err := eng.Synthesize(context.Background(), "## Morning Update\n\n**Hello** [world](https://example.com)!", "")
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
		s := string(logged)
		for _, want := range []string{
			"--kokoro-model=" + filepath.Join(modelDir, "model.onnx"),
			"--kokoro-voices=" + filepath.Join(modelDir, "voices.bin"),
			"--kokoro-tokens=" + filepath.Join(modelDir, "tokens.txt"),
			"--kokoro-data-dir=" + filepath.Join(modelDir, "espeak-ng-data"),
			"--kokoro-dict-dir=" + filepath.Join(modelDir, "dict"),
			"--kokoro-lexicon=" + filepath.Join(modelDir, "lexicon-us-en.txt") + "," + filepath.Join(modelDir, "lexicon-zh.txt"),
			"--sid=3",
			"Morning Update.",
			"Hello world!",
		} {
			if !strings.Contains(s, want) {
				t.Fatalf("expected %q in kokoro args, got:\n%s", want, s)
			}
		}
	})

	t.Run("named and numeric speaker resolution", func(t *testing.T) {
		cases := map[string]int{
			"af_heart":            3,
			"af_bella":            2,
			"am_adam":             11,
			"am_fenrir":           14,
			"am_michael":          16,
			"bf_emma":             21,
			"bm_george":           26,
			"18":                  18,
			"en_US-lessac-medium": 3, // backwards-compatible fallback
		}
		for voice, wantSID := range cases {
			got, err := eng.ResolveSpeakerID(voice)
			if err != nil {
				t.Fatalf("ResolveSpeakerID(%q) error: %v", voice, err)
			}
			if got != wantSID {
				t.Fatalf("ResolveSpeakerID(%q) = %d, want %d", voice, got, wantSID)
			}
		}
	})

	t.Run("rejects invalid or traversal voiceID", func(t *testing.T) {
		for _, bad := range []string{"../evil", "dir/voice", "unknown_voice"} {
			if _, err := eng.Synthesize(context.Background(), "Hello world.", bad); err == nil {
				t.Fatalf("expected error for invalid voiceID %q", bad)
			}
		}
	})
}
