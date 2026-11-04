package release

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
)

func write(t *testing.T, root, rel, content string, mode os.FileMode) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func buildFixture(t *testing.T) (string, *Manifest) {
	t.Helper()
	root := t.TempDir()
	for _, rel := range RequiredFiles {
		mode := os.FileMode(0o644)
		if strings.HasPrefix(rel, "bin/") || strings.HasSuffix(rel, "compile") || strings.HasSuffix(rel, "consumer") || strings.HasSuffix(rel, "toolexec") {
			mode = 0o755
		}
		write(t, root, rel, "content of "+rel+"\n", mode)
	}
	write(t, root, "LICENSES/go.LICENSE", "go license\n", 0o644)
	m := Manifest{
		Name:        "tynel-test-darwin-arm64",
		Version:     "dev",
		Commit:      "abcdef0",
		Target:      "darwin/arm64",
		GeneratedAt: "2026-11-04T00:00:00+08:00",
	}
	collected, err := Collect(root, m)
	if err != nil {
		t.Fatal(err)
	}
	if err := Write(root, collected); err != nil {
		t.Fatal(err)
	}
	return root, collected
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	var d *diagnostic.Diagnostic
	if !errors.As(err, &d) {
		t.Fatalf("错误不是诊断：%v", err)
	}
	if d.Code != code {
		t.Fatalf("错误码为 %q，期望 %q：%v", d.Code, code, err)
	}
	if d.Category != diagnostic.Environment {
		t.Fatalf("类别为 %v，期望环境缺失", d.Category)
	}
}

func TestCollectAndVerify(t *testing.T) {
	root, m := buildFixture(t)
	if len(m.Files) != len(RequiredFiles)+1 {
		t.Fatalf("文件数为 %d，期望 %d", len(m.Files), len(RequiredFiles)+1)
	}
	if m.Files[0].Path != ".work/bridge/compile" {
		t.Errorf("清单应按路径排序，实际第一项 %q", m.Files[0].Path)
	}
	if _, err := os.Stat(filepath.Join(root, ManifestName)); err != nil {
		t.Fatalf("清单不存在：%v", err)
	}
	if err := Verify(root); err != nil {
		t.Fatalf("Verify 失败：%v", err)
	}
}

func TestVerifyRejectsMissingFile(t *testing.T) {
	root, _ := buildFixture(t)
	if err := os.Remove(filepath.Join(root, "bridge", "go", "support", "support.go")); err != nil {
		t.Fatal(err)
	}
	err := Verify(root)
	wantCode(t, err, "release.missing")
	if !strings.Contains(err.Error(), "support.go") {
		t.Errorf("错误应指出缺失文件：%v", err)
	}
}

func TestVerifyRejectsChangedFile(t *testing.T) {
	root, _ := buildFixture(t)
	write(t, root, "bridge/go/support/support.go", "tampered\n", 0o644)
	err := Verify(root)
	wantCode(t, err, "release.mismatch")
	if !strings.Contains(err.Error(), "support.go") {
		t.Errorf("错误应指出不匹配文件：%v", err)
	}
}

func TestVerifyRejectsModeChange(t *testing.T) {
	root, _ := buildFixture(t)
	if err := os.Chmod(filepath.Join(root, "bin", "tynel"), 0o644); err != nil {
		t.Fatal(err)
	}
	wantCode(t, Verify(root), "release.mismatch")
}

func TestManifestRejectsUnsafePaths(t *testing.T) {
	root, m := buildFixture(t)
	m.Files = append(m.Files, FileSpec{Path: "../escape", SHA256: strings.Repeat("a", 64), Bytes: 1, Mode: "0644"})
	if err := Write(root, m); err != nil {
		t.Fatal(err)
	}
	wantCode(t, Verify(root), "release.invalid")
}

func TestManifestRequiresEveryRequiredFile(t *testing.T) {
	root, m := buildFixture(t)
	filtered := m.Files[:0]
	for _, f := range m.Files {
		if f.Path != "go.mod" {
			filtered = append(filtered, f)
		}
	}
	m.Files = filtered
	if err := Write(root, m); err != nil {
		t.Fatal(err)
	}
	wantCode(t, Verify(root), "release.invalid")
}

func TestVerifyIfSealedIgnoresDevelopmentTree(t *testing.T) {
	if err := VerifyIfSealed(t.TempDir()); err != nil {
		t.Fatalf("没有 release.json 不应校验：%v", err)
	}
}

func TestMalformedManifestIsExplicit(t *testing.T) {
	root := t.TempDir()
	write(t, root, ManifestName, "{not json", 0o644)
	_, err := IsSealed(root)
	if err != nil {
		t.Fatal(err)
	}
	wantCode(t, Verify(root), "release.invalid")
}
