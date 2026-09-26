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
