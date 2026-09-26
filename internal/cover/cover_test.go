package cover

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerate(t *testing.T) {
	b := Generate()
	if len(b) < 200 {
		t.Fatalf("tiny png %d", len(b))
	}
	if !bytes.HasPrefix(b, []byte{137, 80, 78, 71, 13, 10, 26, 10}) {
		t.Fatal("missing png magic")
	}
}

func TestLoadFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.png")
	src := Generate()
	if err := os.WriteFile(p, src, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, src) {
		t.Fatal("mismatch")
	}
}
