package tts

import (
	"context"
	"encoding/binary"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
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
			"--tts-max-num-sentences=1",
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

func TestKokoroEngineConcurrency(t *testing.T) {
	dir := t.TempDir()
	modelDir := filepath.Join(dir, "kokoro-multi-lang-v1_0")
	if err := os.MkdirAll(filepath.Join(modelDir, "espeak-ng-data"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Fake ffmpeg that concatenates WAV files in the exact order listed in the
	// concat file, then copies combined.wav to episode.mp3.
	ffmpegBin := filepath.Join(dir, "fake-ffmpeg")
	writeExecutable(t, ffmpegBin, `#!/bin/sh
set -e
is_concat=0
list_file=""
prev=""
last=""
for arg in "$@"; do
  if [ "$arg" = "concat" ]; then
    is_concat=1
  fi
  if [ "$prev" = "-i" ]; then
    list_file="$arg"
  fi
  prev="$arg"
  last="$arg"
done
if [ "$is_concat" = "1" ]; then
  : > "$last"
  sed -n "s/^file '\(.*\)'$/\1/p" "$list_file" | while IFS= read -r wav; do
    cat "$wav" >> "$last"
  done
else
  cat "$list_file" > "$last"
fi
`)

	// padParagraph returns a paragraph (~290 runes) that fits in a single chunk
	// (<= maxChunkRunes=500) on its own, but exceeds maxChunkRunes when combined
	// with an adjacent paragraph so Preprocess keeps each paragraph separate.
	padParagraph := func(label string) string {
		return label + " " + strings.Repeat("word ", 55) + "end."
	}

	t.Run("output order matches chunk order and concurrency never exceeds limit", func(t *testing.T) {
		activeDir := filepath.Join(dir, "active-pool")
		if err := os.MkdirAll(activeDir, 0o755); err != nil {
			t.Fatal(err)
		}
		countsLog := filepath.Join(dir, "counts.log")
		kokoroBin := filepath.Join(dir, "fake-kokoro-pool")
		writeExecutable(t, kokoroBin, `#!/bin/sh
set -e
out=""
text=""
for arg in "$@"; do
  case "$arg" in
    --output-filename=*)
      out="${arg#--output-filename=}"
      ;;
  esac
  text="$arg"
done
token="`+activeDir+`/$$"
: > "$token"
count=$(ls -1 "`+activeDir+`" | wc -l | tr -d ' ')
echo "$count" >> "`+countsLog+`"
case "$out" in
  *part-0000.wav) sleep 0.18 ;;
  *part-0001.wav) sleep 0.14 ;;
  *part-0002.wav) sleep 0.04 ;;
  *) sleep 0.08 ;;
esac
rm -f "$token"
printf "[%s]" "$text" > "$out"
`)

		eng := &KokoroEngine{
			Bin:          kokoroBin,
			ModelDir:     modelDir,
			Speed:        1.0,
			Threads:      2,
			Concurrency:  3,
			FFmpegBin:    ffmpegBin,
			DefaultVoice: DefaultKokoroVoice,
		}

		p0 := padParagraph("Chunk zero.")
		p1 := padParagraph("Chunk one.")
		p2 := padParagraph("Chunk two.")
		p3 := padParagraph("Chunk three.")
		p4 := padParagraph("Chunk four.")
		p5 := padParagraph("Chunk five.")
		script := strings.Join([]string{p0, p1, p2, p3, p4, p5}, "\n\n")

		res, err := eng.Synthesize(context.Background(), script, "af_heart")
		if err != nil {
			t.Fatalf("Synthesize failed: %v", err)
		}
		// Verify intermediate WAV files were eagerly deleted before res.Close().
		if tf, ok := res.Reader.(*tmpFile); ok {
			wavLeftovers, _ := filepath.Glob(filepath.Join(tf.dir, "*.wav"))
			if len(wavLeftovers) > 0 {
				t.Fatalf("expected intermediate .wav files to be deleted before Close(), found: %v", wavLeftovers)
			}
		}
		gotBytes, err := io.ReadAll(res.Reader)
		if err != nil {
			t.Fatal(err)
		}
		if err := res.Close(); err != nil {
			t.Fatal(err)
		}

		want := "[" + p0 + "][" + p1 + "][" + p2 + "][" + p3 + "][" + p4 + "][" + p5 + "]"
		if string(gotBytes) != want {
			t.Fatalf("output order mismatch:\n got: %q\nwant: %q", string(gotBytes), want)
		}

		rawCounts, err := os.ReadFile(countsLog)
		if err != nil {
			t.Fatal(err)
		}
		maxActive := 0
		for _, line := range strings.Split(strings.TrimSpace(string(rawCounts)), "\n") {
			n, err := strconv.Atoi(strings.TrimSpace(line))
			if err != nil {
				t.Fatalf("parse active count %q: %v", line, err)
			}
			if n > maxActive {
				maxActive = n
			}
		}
		if maxActive > 3 {
			t.Fatalf("concurrency exceeded limit 3: maxActive=%d", maxActive)
		}
		if maxActive < 2 {
			t.Fatalf("expected concurrent execution (maxActive >= 2), got %d", maxActive)
		}
	})

	t.Run("concurrency 1 runs sequentially in order", func(t *testing.T) {
		activeDir := filepath.Join(dir, "active-seq")
		if err := os.MkdirAll(activeDir, 0o755); err != nil {
			t.Fatal(err)
		}
		countsLog := filepath.Join(dir, "counts-seq.log")
		orderLog := filepath.Join(dir, "order-seq.log")
		kokoroBin := filepath.Join(dir, "fake-kokoro-seq")
		writeExecutable(t, kokoroBin, `#!/bin/sh
set -e
out=""
text=""
for arg in "$@"; do
  case "$arg" in
    --output-filename=*)
      out="${arg#--output-filename=}"
      ;;
  esac
  text="$arg"
done
token="`+activeDir+`/$$"
: > "$token"
count=$(ls -1 "`+activeDir+`" | wc -l | tr -d ' ')
echo "$count" >> "`+countsLog+`"
echo "$text" >> "`+orderLog+`"
sleep 0.03
rm -f "$token"
printf "[%s]" "$text" > "$out"
`)

		eng := &KokoroEngine{
			Bin:          kokoroBin,
			ModelDir:     modelDir,
			Speed:        1.0,
			Threads:      2,
			Concurrency:  1,
			FFmpegBin:    ffmpegBin,
			DefaultVoice: DefaultKokoroVoice,
		}

		s1 := padParagraph("First paragraph.")
		s2 := padParagraph("Second paragraph.")
		s3 := padParagraph("Third paragraph.")
		script := strings.Join([]string{s1, s2, s3}, "\n\n")
		res, err := eng.Synthesize(context.Background(), script, "af_heart")
		if err != nil {
			t.Fatalf("Synthesize failed: %v", err)
		}
		gotBytes, err := io.ReadAll(res.Reader)
		if err != nil {
			t.Fatal(err)
		}
		if err := res.Close(); err != nil {
			t.Fatal(err)
		}
		wantOut := "[" + s1 + "][" + s2 + "][" + s3 + "]"
		if string(gotBytes) != wantOut {
			t.Fatalf("unexpected sequential output: %q", string(gotBytes))
		}

		rawCounts, err := os.ReadFile(countsLog)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(strings.TrimSpace(string(rawCounts)), "\n") {
			if strings.TrimSpace(line) != "1" {
				t.Fatalf("expected active count 1 under Concurrency=1, got %q", line)
			}
		}
		orderBytes, err := os.ReadFile(orderLog)
		if err != nil {
			t.Fatal(err)
		}
		wantOrder := strings.Join([]string{s1, s2, s3}, "\n")
		if strings.TrimSpace(string(orderBytes)) != wantOrder {
			t.Fatalf("unexpected sequential execution order:\n%s", string(orderBytes))
		}
	})

	t.Run("one failing chunk cancels the rest and removes temp directory", func(t *testing.T) {
		customTmp := filepath.Join(dir, "tmp-fail")
		if err := os.MkdirAll(customTmp, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("TMPDIR", customTmp)

		startedLog := filepath.Join(dir, "started-fail.log")
		kokoroBin := filepath.Join(dir, "fake-kokoro-fail")
		writeExecutable(t, kokoroBin, `#!/bin/sh
out=""
text=""
for arg in "$@"; do
  case "$arg" in
    --output-filename=*)
      out="${arg#--output-filename=}"
      ;;
  esac
  text="$arg"
done
echo "$text" >> "`+startedLog+`"
case "$text" in
  *FAIL*)
    echo "simulated synthesis failure" >&2
    exit 1
    ;;
  *)
    exec sleep 5
    ;;
esac
`)

		eng := &KokoroEngine{
			Bin:          kokoroBin,
			ModelDir:     modelDir,
			Speed:        1.0,
			Threads:      2,
			Concurrency:  2,
			FFmpegBin:    ffmpegBin,
			DefaultVoice: DefaultKokoroVoice,
		}

		script := strings.Join([]string{
			padParagraph("Slow chunk one."),
			padParagraph("Chunk two FAIL."),
			padParagraph("Slow chunk three."),
			padParagraph("Slow chunk four."),
			padParagraph("Slow chunk five."),
		}, "\n\n")
		start := time.Now()
		_, err := eng.Synthesize(context.Background(), script, "af_heart")
		elapsed := time.Since(start)
		if err == nil {
			t.Fatal("expected error from failing chunk")
		}
		if !strings.Contains(err.Error(), "simulated synthesis failure") {
			t.Fatalf("expected simulated synthesis failure error, got: %v", err)
		}
		if elapsed >= 2*time.Second {
			t.Fatalf("expected failing chunk to cancel in-flight chunks quickly, took %v", elapsed)
		}
		startedBytes, err := os.ReadFile(startedLog)
		if err != nil {
			t.Fatal(err)
		}
		startedLines := strings.Split(strings.TrimSpace(string(startedBytes)), "\n")
		if len(startedLines) >= 5 {
			t.Fatalf("expected queued chunks to be canceled before starting, but all %d started", len(startedLines))
		}

		leftover, err := filepath.Glob(filepath.Join(customTmp, "podcaster-kokoro-*"))
		if err != nil {
			t.Fatal(err)
		}
		if len(leftover) > 0 {
			t.Fatalf("expected temp directory to be removed on error, found: %v", leftover)
		}
	})
}

func TestKokoroVoiceBlending(t *testing.T) {
	dir := t.TempDir()
	// Create a synthetic voices.bin with 28 speakers, 4 float32s per speaker.
	const numSpeakers = 28
	const floatsPerSpeaker = 4
	raw := make([]byte, numSpeakers*floatsPerSpeaker*4)
	for s := 0; s < numSpeakers; s++ {
		for j := 0; j < floatsPerSpeaker; j++ {
			val := float32(s*10 + j)
			off := (s*floatsPerSpeaker + j) * 4
			binary.LittleEndian.PutUint32(raw[off:off+4], math.Float32bits(val))
		}
	}
	voicesPath := filepath.Join(dir, "voices.bin")
	if err := os.WriteFile(voicesPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	eng := &KokoroEngine{DefaultVoice: DefaultKokoroVoice}

	// Default "af_heart" should resolve to 0.7 * af_heart (sid=3) + 0.3 * af_bella (sid=2).
	weights, err := eng.ResolveVoiceWeights("af_heart")
	if err != nil {
		t.Fatalf("ResolveVoiceWeights(af_heart) error: %v", err)
	}
	if len(weights) != 2 || weights[0].SID != 3 || math.Abs(weights[0].Weight-0.7) > 1e-6 || weights[1].SID != 2 || math.Abs(weights[1].Weight-0.3) > 1e-6 {
		t.Fatalf("unexpected default af_heart blend weights: %+v", weights)
	}

	blendedPath, sid, cleanup, err := prepareBlendedVoices(voicesPath, dir, weights)
	if err != nil {
		t.Fatalf("prepareBlendedVoices error: %v", err)
	}
	defer cleanup()
	if sid != 3 || blendedPath == voicesPath {
		t.Fatalf("expected blended voices file at sid 3, got path=%q sid=%d", blendedPath, sid)
	}

	blendedBytes, err := os.ReadFile(blendedPath)
	if err != nil {
		t.Fatal(err)
	}
	for j := 0; j < floatsPerSpeaker; j++ {
		off := (3*floatsPerSpeaker + j) * 4
		got := math.Float32frombits(binary.LittleEndian.Uint32(blendedBytes[off : off+4]))
		want := float32(0.7*float64(30+j) + 0.3*float64(20+j))
		if math.Abs(float64(got-want)) > 1e-4 {
			t.Fatalf("blended float[%d] = %v, want %v", j, got, want)
		}
	}

	// Verify expression syntax "0.7*af_heart+0.3*af_bella" and "am_adam:0.6,am_michael:0.4".
	exprWeights, err := eng.ResolveVoiceWeights("0.7 * af_heart + 0.3 * af_bella")
	if err != nil || len(exprWeights) != 2 || exprWeights[0].SID != 3 || exprWeights[1].SID != 2 {
		t.Fatalf("ResolveVoiceWeights expression failed: %+v (%v)", exprWeights, err)
	}
	colonWeights, err := eng.ResolveVoiceWeights("am_adam:0.6,am_michael:0.4")
	if err != nil || len(colonWeights) != 2 || colonWeights[0].SID != 11 || colonWeights[1].SID != 16 {
		t.Fatalf("ResolveVoiceWeights colon syntax failed: %+v (%v)", colonWeights, err)
	}
}
