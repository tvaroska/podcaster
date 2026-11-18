package podcaster_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestPortableRepoHygieneAndLinks(t *testing.T) {
	repoRoot := findRepoRoot(t)

	if _, err := os.Stat(filepath.Join(repoRoot, ".github", "workflows")); err == nil {
		t.Fatal("CI/CD must use cloudbuild.yaml only (zero .github/workflows/)")
	}

	for _, req := range []string{"AGENTS.md", "CRITICAL.md", "DECISIONS.md", "README.md"} {
		if _, err := os.Stat(filepath.Join(repoRoot, req)); err != nil {
			t.Fatalf("missing required root doc %s: %v", req, err)
		}
	}

	agentsInfo, err := os.Stat(filepath.Join(repoRoot, "AGENTS.md"))
	if err != nil {
		t.Fatalf("missing AGENTS.md: %v", err)
	}
	if agentsInfo.Size() >= 24_000 {
		t.Fatalf("AGENTS.md exceeds 24 KB cap (%d bytes)", agentsInfo.Size())
	}

	decisionsInfo, err := os.Stat(filepath.Join(repoRoot, "DECISIONS.md"))
	if err != nil {
		t.Fatalf("missing DECISIONS.md: %v", err)
	}
	if decisionsInfo.Size() > 25_000 {
		t.Fatalf("DECISIONS.md exceeds 25 KB cap (%d bytes)", decisionsInfo.Size())
	}

	claudePath := filepath.Join(repoRoot, "CLAUDE.md")
	linkTarget, err := os.Readlink(claudePath)
	if err != nil {
		t.Fatalf("CLAUDE.md must be a symlink to AGENTS.md: %v", err)
	}
	if filepath.ToSlash(linkTarget) != "AGENTS.md" {
		t.Fatalf("CLAUDE.md symlink target = %q, want %q", linkTarget, "AGENTS.md")
	}

	forbidden := []*regexp.Regexp{
		regexp.MustCompile("/usr/local/" + "google/home|\\.c\\.googlers\\.com"),
		regexp.MustCompile("file" + ":///"),
		regexp.MustCompile(`\b` + "b" + `/\d{6,}\b`),
		regexp.MustCompile("//" + "depot/"),
		regexp.MustCompile("~/" + "customers|~/" + "notes|~/" + "ideas|~/" + "robotics"),
	}

	mdLinkRe := regexp.MustCompile(`\[[^\]]+\]\(([^)\s]+)\)`)

	err = filepath.WalkDir(repoRoot, func(path string, d os.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		name := d.Name()
		if d.IsDir() {
			if strings.HasPrefix(name, ".") || name == "bin" || name == "dist" || name == "bucket" || name == "data" {
				return filepath.SkipDir
			}
			return nil
		}
		if name == "hygiene_test.go" || strings.HasSuffix(name, ".exe") || strings.HasSuffix(name, ".zip") || strings.HasSuffix(name, ".mp3") || strings.HasSuffix(name, ".wav") || strings.HasSuffix(name, ".png") || strings.HasSuffix(name, ".db") {
			return nil
		}

		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(raw)
		rel, _ := filepath.Rel(repoRoot, path)

		for _, re := range forbidden {
			if m := re.FindString(text); m != "" {
				t.Errorf("%s: forbidden non-portable pattern %q", rel, m)
			}
		}

		if strings.HasSuffix(name, ".md") {
			for _, m := range mdLinkRe.FindAllStringSubmatch(text, -1) {
				target := strings.SplitN(m[1], "#", 2)[0]
				if target == "" || strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") || strings.HasPrefix(target, "mailto:") {
					continue
				}
				resolved := filepath.Clean(filepath.Join(filepath.Dir(path), filepath.FromSlash(target)))
				relToRepo, rerr := filepath.Rel(repoRoot, resolved)
				if rerr != nil || strings.HasPrefix(relToRepo, "..") {
					t.Errorf("%s: escaping relative markdown link %q", rel, target)
					continue
				}
				if _, serr := os.Stat(resolved); serr != nil {
					t.Errorf("%s: broken relative markdown link %q", rel, target)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find go.mod root")
		}
		dir = parent
	}
}
