package toolchain

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
)

func TestResolvePathAcceptsRelativeAndInternalAbsolute(t *testing.T) {
	root := t.TempDir()
	relative := ".work/toolchain/go/bin/go"
	got, err := ResolvePath(root, relative)
	if err != nil {
		t.Fatalf("相对路径应通过：%v", err)
	}
	if got != filepath.Join(root, filepath.FromSlash(relative)) {
		t.Errorf("相对路径解析为 %q", got)
	}

	absolute := filepath.Join(root, ".work", "bridge", "compile")
	got, err = ResolvePath(root, absolute)
	if err != nil || got != absolute {
		t.Fatalf("根内绝对路径应通过：%q %v", got, err)
	}
}

func TestResolvePathRejectsOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(string(filepath.Separator), "Users", "someone", ".work", "toolchain")
	_, err := ResolvePath(root, outside)
	if err == nil {
		t.Fatal("根外绝对路径应被拒绝")
	}
	if !strings.Contains(err.Error(), "toolchain.path-escape") {
		t.Fatalf("错误应为 toolchain.path-escape：%v", err)
	}
	if diagnostic.ExitCode(err) != diagnostic.ExitEnvironment {
		t.Errorf("退出状态应为 4，实际 %d", diagnostic.ExitCode(err))
	}
}

func TestResolvePathRejectsDotDot(t *testing.T) {
	root := t.TempDir()
	if _, err := ResolvePath(root, "../elsewhere/go"); err == nil {
		t.Fatal("../ 路径应被拒绝")
	}
}
