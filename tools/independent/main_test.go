package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveRootFromNestedDirectory(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "tools", "independent")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module github.com/Mahoo12138/tynel\n\ngo 1.27\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(nested); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(old); err != nil {
			t.Errorf("恢复工作目录：%v", err)
		}
	}()
	got, err := resolveRoot()
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("项目根为 %q，期望 %q", got, want)
	}
}
