package tts

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestEncodeAndProbe(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	src := testdataMP3(t)
	dir := t.TempDir()
	wav := filepath.Join(dir, "in.wav")
	cmd := exec.Command("ffmpeg", "-y", "-i", src, wav)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("decode fixture: %v (%s)", err, out)
	}
	mp3 := filepath.Join(dir, "out.mp3")
	if err := EncodeMP3(context.Background(), "ffmpeg", wav, mp3); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(mp3)
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() < 100 {
		t.Fatalf("tiny mp3: %d", st.Size())
	}
	d, err := ProbeDuration(context.Background(), "ffmpeg", mp3)
	if err != nil {
		t.Fatal(err)
	}
	if d < 0.5 || d > 5 {
		t.Fatalf("unexpected duration %v", d)
	}
}

func TestConcatWAV(t *testing.T) {
	dir := t.TempDir()
	w1 := filepath.Join(dir, "part1.wav")
	if err := os.WriteFile(w1, []byte("RIFFpart1"), 0o644); err != nil {
		t.Fatal(err)
	}
	singleDest := filepath.Join(dir, "single.wav")
	if err := ConcatWAV(context.Background(), "", []string{w1}, singleDest); err != nil {
		t.Fatalf("single ConcatWAV failed: %v", err)
	}
	got, err := os.ReadFile(singleDest)
	if err != nil || string(got) != "RIFFpart1" {
		t.Fatalf("unexpected single concat output: %q (%v)", string(got), err)
	}

	fakeFFmpeg := filepath.Join(dir, "ffmpeg")
	writeExecutable(t, fakeFFmpeg, `#!/bin/sh
for last; do true; done
printf "RIFFcombined" > "$last"
`)
	w2 := filepath.Join(dir, "part2.wav")
	if err := os.WriteFile(w2, []byte("RIFFpart2"), 0o644); err != nil {
		t.Fatal(err)
	}
	multiDest := filepath.Join(dir, "multi.wav")
	if err := ConcatWAV(context.Background(), fakeFFmpeg, []string{w1, w2}, multiDest); err != nil {
		t.Fatalf("multi ConcatWAV failed: %v", err)
	}
	gotMulti, err := os.ReadFile(multiDest)
	if err != nil || string(gotMulti) != "RIFFcombined" {
		t.Fatalf("unexpected multi concat output: %q (%v)", string(gotMulti), err)
	}
}

func testdataMP3(t *testing.T) string {
	t.Helper()
	candidates := []string{
		"testdata/beep.mp3",
		filepath.Join("testdata", "beep.mp3"),
	}
	// When tests run from this package, look relative to this file.
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates,
			filepath.Join(wd, "testdata", "beep.mp3"),
			filepath.Join(wd, "..", "..", "testdata", "beep.mp3"),
		)
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	t.Fatal("testdata/beep.mp3 not found")
	return ""
}
