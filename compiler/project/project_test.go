package project

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
	"github.com/Mahoo12138/tynel/compiler/source"
)

func fixture(t *testing.T) *Root {
	t.Helper()
	root, err := Open(filepath.Join("testdata", "project"))
	if err != nil {
		t.Fatalf("打开夹具根失败：%v", err)
	}
	return root
}

func codeOf(err error) string {
	var d *diagnostic.Diagnostic
	if errors.As(err, &d) {
		return d.Code
	}
	return ""
}

func TestResolveEntryAndImports(t *testing.T) {
	root := fixture(t)

	entry, err := root.ResolveEntry("入口 文件.ts")
	if err != nil {
		t.Fatalf("解析入口失败：%v", err)
	}
	if entry.Path != "入口 文件.ts" || entry.SHA256 == "" {
		t.Fatalf("入口身份不对：%+v", entry)
	}

	tool, err := root.ResolveImport(entry, "./子目录/工具 一.ts")
	if err != nil {
		t.Fatalf("解析中文空格导入失败：%v", err)
	}
	if tool.Path != "子目录/工具 一.ts" {
		t.Fatalf("导入路径为 %q", tool.Path)
	}

	first, err := root.ResolveEntry("src/同名.ts")
	if err != nil {
		t.Fatalf("解析同名文件失败：%v", err)
	}
	second, err := root.ResolveEntry("other/同名.ts")
	if err != nil {
		t.Fatalf("解析另一个同名文件失败：%v", err)
	}
	if first.Path == second.Path || first.SHA256 == second.SHA256 {
		t.Fatalf("同名文件没有区分开：%+v %+v", first, second)
	}

	absolute, err := root.ResolveEntry(filepath.Join(root.Path(), "入口 文件.ts"))
	if err != nil {
		t.Fatalf("绝对入口失败：%v", err)
	}
	if absolute.Identity() != entry.Identity() {
		t.Fatalf("绝对入口身份 %q，相对入口身份 %q", absolute.Identity(), entry.Identity())
	}
}

func TestResolveStableAcrossWorkingDirectories(t *testing.T) {
	abs, err := filepath.Abs(filepath.Join("testdata", "project"))
	if err != nil {
		t.Fatal(err)
	}
	before, err := Open(abs)
	if err != nil {
		t.Fatal(err)
	}
	want, err := before.ResolveEntry("src/同名.ts")
	if err != nil {
		t.Fatal(err)
	}

	t.Chdir(t.TempDir())
	after, err := Open(abs)
	if err != nil {
		t.Fatal(err)
	}
	got, err := after.ResolveEntry("src/同名.ts")
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != want.Path || got.SHA256 != want.SHA256 || got.Abs != want.Abs {
		t.Fatalf("换工作目录后身份变了：%+v -> %+v", want, got)
	}
}

func TestResolveRejects(t *testing.T) {
	root := fixture(t)
	entry, err := root.ResolveEntry("入口 文件.ts")
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		call func() (File, error)
		code string
	}{
		{"缺文件", func() (File, error) { return root.ResolveEntry("src/缺失.ts") }, "project.missing"},
		{"非 ts", func() (File, error) { return root.ResolveEntry("src/同名.txt") }, "project.non-ts"},
		{"目录遍历", func() (File, error) { return root.ResolveEntry("../outside.ts") }, "project.outside-root"},
		{"绝对路径越界", func() (File, error) { return root.ResolveEntry("/tmp/outside.ts") }, "project.outside-root"},
		{"裸模块", func() (File, error) { return root.ResolveImport(entry, "工具.ts") }, "project.bare-module"},
		{"绝对导入", func() (File, error) { return root.ResolveImport(entry, "/tmp/工具.ts") }, "project.absolute-path"},
		{"错误大小写", func() (File, error) { return root.ResolveEntry("SRC/同名.ts") }, "project.case-conflict"},
		{"导入非 ts", func() (File, error) { return root.ResolveImport(entry, "./子目录/工具 一.txt") }, "project.non-ts"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := c.call()
			if got := codeOf(err); got != c.code {
				t.Fatalf("错误码为 %q，期望 %q（err=%v）", got, c.code, err)
			}
		})
	}
}

func TestResolveRejectsSymlinkOutside(t *testing.T) {
	rootDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootDir, "inside.ts"), []byte("console.log(1);\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "outside.ts")
	if err := os.WriteFile(outsideFile, []byte("console.log(2);\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideFile, filepath.Join(rootDir, "link.ts")); err != nil {
		t.Fatal(err)
	}

	root, err := Open(rootDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := root.ResolveEntry("link.ts"); codeOf(err) != "project.outside-root" {
		t.Fatalf("越界符号链接应拒绝，实际 %v", err)
	}
	if _, err := root.ResolveEntry("inside.ts"); err != nil {
		t.Fatalf("普通文件应通过：%v", err)
	}
}

func TestResolveAllowsSymlinkInside(t *testing.T) {
	rootDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootDir, "inside.ts"), []byte("console.log(1);\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(rootDir, "inside.ts"), filepath.Join(rootDir, "alias.ts")); err != nil {
		t.Fatal(err)
	}
	root, err := Open(rootDir)
	if err != nil {
		t.Fatal(err)
	}
	file, err := root.ResolveEntry("alias.ts")
	if err != nil {
		t.Fatalf("根内符号链接应通过：%v", err)
	}
	if file.Path != "inside.ts" {
		t.Fatalf("符号链接身份应落到目标，实际 %q", file.Path)
	}
}

func TestSnapshotUsesResolvedBytes(t *testing.T) {
	root := fixture(t)
	file, err := root.ResolveEntry("入口 文件.ts")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := file.Snapshot(source.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(snapshot.Text(), "入口") || snapshot.Path() != file.Abs {
		t.Fatalf("快照不对：path=%q text=%q", snapshot.Path(), snapshot.Text())
	}
}

func TestOpenRejectsMissingRoot(t *testing.T) {
	_, err := Open(filepath.Join(t.TempDir(), "missing"))
	if got := codeOf(err); got != "project.root-missing" {
		t.Fatalf("错误码为 %q，期望 project.root-missing（err=%v）", got, err)
	}
}

// 中间目录是符号链接时，字面路径看上去仍在许可根内，最终读到的却可能是根外
// 文件。入口、导入、绝对入口、链接链和相对链接目标都要在同一处被拒绝。
func TestResolveRejectsSymlinkedDirectoryOutside(t *testing.T) {
	base := t.TempDir()
	rootDir := filepath.Join(base, "project")
	outside := filepath.Join(base, "outside")
	for _, dir := range []string{
		rootDir,
		filepath.Join(rootDir, "src"),
		outside,
		filepath.Join(outside, "deep"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(rootDir, "main.ts"), []byte("import './external/secret.ts';\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.ts"), []byte("export const secret = 42;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "deep", "nested.ts"), []byte("export const nested = 1;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 绝对目标、相对目标、两级链接，以及「先出根、再指回根内」的链接。
	if err := os.Symlink(outside, filepath.Join(rootDir, "external")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../outside", filepath.Join(rootDir, "src", "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(rootDir, "src", "escape"), filepath.Join(rootDir, "chain")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(rootDir, "main.ts"), filepath.Join(outside, "back.ts")); err != nil {
		t.Fatal(err)
	}

	root, err := Open(rootDir)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := root.ResolveEntry("main.ts")
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		call func() (File, error)
	}{
		{"入口经过绝对目标的目录链接", func() (File, error) { return root.ResolveEntry("external/secret.ts") }},
		{"入口经过相对目标的目录链接", func() (File, error) { return root.ResolveEntry("src/escape/deep/nested.ts") }},
		{"入口经过两级目录链接", func() (File, error) { return root.ResolveEntry("chain/secret.ts") }},
		{"导入经过目录链接", func() (File, error) { return root.ResolveImport(entry, "./external/secret.ts") }},
		{"绝对入口经过目录链接", func() (File, error) {
			return root.ResolveEntry(filepath.Join(rootDir, "external", "secret.ts"))
		}},
		{"根外目录的条目不被枚举", func() (File, error) { return root.ResolveEntry("external/缺失.ts") }},
		{"中间目录越界后即使最终指回根内也拒绝", func() (File, error) { return root.ResolveEntry("external/back.ts") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := c.call(); codeOf(err) != "project.outside-root" {
				t.Fatalf("中间目录越界应拒绝，实际 %v", err)
			}
		})
	}
	if _, err := root.ResolveEntry("main.ts"); err != nil {
		t.Fatalf("普通文件应通过：%v", err)
	}
}

// 目标仍在许可根内时，中间目录的符号链接继续可用。只有最后一段是文件链接时
// 身份才落到真实目标；目录链接保留逻辑路径，前端宿主靠这条路径解析导入。
func TestResolveAllowsSymlinkedDirectoryInside(t *testing.T) {
	rootDir := t.TempDir()
	for _, dir := range []string{filepath.Join(rootDir, "src"), filepath.Join(rootDir, "shared")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	want := []byte("export const value = 1;\n")
	if err := os.WriteFile(filepath.Join(rootDir, "shared", "value.ts"), want, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootDir, "main.ts"), []byte("import './src/link/value.ts';\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../shared", filepath.Join(rootDir, "src", "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(rootDir, "shared"), filepath.Join(rootDir, "src", "absolute")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("value.ts", filepath.Join(rootDir, "shared", "alias.ts")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(".", filepath.Join(rootDir, "self")); err != nil {
		t.Fatal(err)
	}

	root, err := Open(rootDir)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := root.ResolveEntry("main.ts")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		call func() (File, error)
		path string
	}{
		{"相对目标的目录链接", func() (File, error) { return root.ResolveEntry("src/link/value.ts") }, "src/link/value.ts"},
		{"绝对目标的目录链接", func() (File, error) { return root.ResolveEntry("src/absolute/value.ts") }, "src/absolute/value.ts"},
		{"目录链接加文件链接", func() (File, error) { return root.ResolveEntry("src/link/alias.ts") }, "shared/value.ts"},
		{"链接指向根自身", func() (File, error) { return root.ResolveEntry("self/shared/value.ts") }, "self/shared/value.ts"},
		{"导入经过目录链接", func() (File, error) { return root.ResolveImport(entry, "./src/link/value.ts") }, "src/link/value.ts"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			file, err := c.call()
			if err != nil {
				t.Fatalf("根内符号链接应通过：%v", err)
			}
			wantAbs := filepath.Join(root.Path(), filepath.FromSlash(c.path))
			if file.Path != c.path || file.Abs != wantAbs || string(file.Bytes) != string(want) {
				t.Fatalf("身份或内容不对，期望 %s / %s，实际 %+v", c.path, wantAbs, file)
			}
		})
	}
}
