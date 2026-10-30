package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
	"github.com/Mahoo12138/tynel/internal/build"
	"github.com/Mahoo12138/tynel/internal/toolchain"
)

// 本文件是 05.07 的多文件回归：它不替换前端，而是走真实的 host、依赖图、
// 合并降低和 Go 后端。测试需要 ./tools/test 已经准备的工具链、前端源码和
// 修改版编译器。

func realRepoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("无法定位仓库根：%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(toolchain.LockPath))); err != nil {
		t.Fatalf("仓库根 %s 缺少锁文件：%v", root, err)
	}
	return root
}

func writeSource(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func hashOf(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s 失败：%v", path, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// runBuilt 运行一次构建产物，返回标准输出；超时按失败处理，不挂住测试。
func runBuilt(t *testing.T, path string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("程序 %s 超时", path)
	}
	if err != nil {
		t.Fatalf("运行 %s 失败：%v\n%s", path, err, stderr.String())
	}
	return stdout.String()
}

// cliBuild 调用真实的 app.build，并读回构建记录。失败时返回诊断文本。
func cliBuild(t *testing.T, a *app, entry, output string) (*build.Record, string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	a.stdout = &stdout
	a.stderr = &stderr
	err := a.build([]string{entry, "-o", output})
	if err != nil {
		return nil, stderr.String(), err
	}
	data, readErr := os.ReadFile(build.RecordPath(output))
	if readErr != nil {
		t.Fatalf("构建记录不存在：%v", readErr)
	}
	var record build.Record
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("构建记录无法解析：%v", err)
	}
	return &record, stderr.String(), nil
}

func TestBuildIdentityTracksDependencyChanges(t *testing.T) {
	root := realRepoRoot(t)
	dir, err := os.MkdirTemp(filepath.Join(root, ".work"), "identity-")
	if err != nil {
		t.Fatalf("无法创建夹具目录：%v", err)
	}
	defer os.RemoveAll(dir)

	entryRel, err := filepath.Rel(root, filepath.Join(dir, "main.ts"))
	if err != nil {
		t.Fatal(err)
	}
	entryRel = filepath.ToSlash(entryRel)
	output := filepath.Join(dir, "app")

	math := "export function sumTo(n: number): number {\n" +
		"  let sum = 0;\n" +
		"  let i = 1;\n" +
		"  while (i <= n) {\n" +
		"    sum = sum + i;\n" +
		"    i = i + 1;\n" +
		"  }\n" +
		"  return sum;\n" +
		"}\n"
	types := "export type Amount = number;\n"
	main := "import { sumTo } from \"./math.ts\";\n" +
		"import type { Amount } from \"./types.ts\";\n\n" +
		"const n: Amount = 10;\n" +
		"console.log(sumTo(n));\n"
	writeSource(t, filepath.Join(dir, "math.ts"), math)
	writeSource(t, filepath.Join(dir, "types.ts"), types)
	writeSource(t, filepath.Join(dir, "main.ts"), main)

	a := &app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, runner: toolchain.Exec{}, root: root, hasRoot: true}

	baseline, _, err := cliBuild(t, a, entryRel, output)
	if err != nil {
		t.Fatalf("基线构建失败：%v\n%s", err, a.stderr)
	}
	if got := runBuilt(t, output); got != "55\n" {
		t.Fatalf("基线输出为 %q，期望 55", got)
	}
	if baseline.Identity.Hash == "" || baseline.Identity.Entry != entryRel {
		t.Fatalf("构建身份不完整：%+v", baseline.Identity)
	}
	if len(baseline.Identity.Files) != 3 {
		t.Fatalf("身份里应有入口、值依赖和类型依赖 3 个文件，实际 %d", len(baseline.Identity.Files))
	}
	if baseline.Tools.BuildTag != "tynel_"+baseline.Identity.ArtifactSHA256 {
		t.Errorf("build tag 没有覆盖产物输入身份")
	}
	baselineArtifact := hashOf(t, output)

	// 相同完整输入：身份、标签和产物都应稳定。
	again, _, err := cliBuild(t, a, entryRel, output)
	if err != nil {
		t.Fatalf("重复构建失败：%v", err)
	}
	if again.Identity.Hash != baseline.Identity.Hash || again.Tools.BuildTag != baseline.Tools.BuildTag {
		t.Errorf("相同输入的构建身份不稳定")
	}
	if got := hashOf(t, output); got != baselineArtifact {
		t.Errorf("相同输入的产物不稳定：%s != %s", got, baselineArtifact)
	}

	// 改辅助函数：入口源码不变，运行结果必须更新，不能复用旧对象。
	mathChanged := strings.Replace(math, "  return sum;\n", "  return sum + 1;\n", 1)
	writeSource(t, filepath.Join(dir, "math.ts"), mathChanged)
	changed, _, err := cliBuild(t, a, entryRel, output)
	if err != nil {
		t.Fatalf("依赖变化后构建失败：%v\n%s", err, a.stderr)
	}
	if changed.Identity.Hash == baseline.Identity.Hash {
		t.Error("辅助函数变化没有改变构建身份")
	}
	if changed.Tools.BuildTag == baseline.Tools.BuildTag {
		t.Error("辅助函数变化没有改变 build tag")
	}
	if got := runBuilt(t, output); got != "56\n" {
		t.Fatalf("修改辅助函数后输出为 %q，期望 56；旧缓存可能被复用", got)
	}
	changedArtifact := hashOf(t, output)

	// 改纯类型依赖：检查必须重新发生并报 TS2322，旧成功产物保留。
	recordBefore, err := os.ReadFile(build.RecordPath(output))
	if err != nil {
		t.Fatal(err)
	}
	writeSource(t, filepath.Join(dir, "types.ts"), "export type Amount = boolean;\n")
	_, stderr, err := cliBuild(t, a, entryRel, output)
	if err == nil {
		t.Fatal("类型依赖变化后应报源码错误")
	}
	if code := exitOf(err); code != diagnostic.ExitSource {
		t.Fatalf("类型错误退出状态为 %d，期望 1（%v）", code, err)
	}
	if !strings.Contains(stderr, "TS2322") {
		t.Errorf("类型依赖变化应重新检查并报 TS2322：%s", stderr)
	}
	if got := hashOf(t, output); got != changedArtifact {
		t.Error("检查失败时改动了旧产物")
	}
	if got, _ := os.ReadFile(build.RecordPath(output)); string(got) != string(recordBefore) {
		t.Error("检查失败时改动了旧记录")
	}

	// 恢复类型后改导出名：入口的旧导入必须失败。
	writeSource(t, filepath.Join(dir, "types.ts"), types)
	renameExport := strings.Replace(mathChanged, "export function sumTo", "export function sum", 1)
	writeSource(t, filepath.Join(dir, "math.ts"), renameExport)
	_, stderr, err = cliBuild(t, a, entryRel, output)
	if err == nil {
		t.Fatal("导出改名后应报源码错误")
	}
	if code := exitOf(err); code != diagnostic.ExitSource {
		t.Fatalf("缺失导出的退出状态为 %d，期望 1（%v）", code, err)
	}
	if !strings.Contains(stderr, "sumTo") {
		t.Errorf("缺失导出诊断应提到旧名字：%s", stderr)
	}
	if got := hashOf(t, output); got != changedArtifact {
		t.Error("缺失导出时改动了旧产物")
	}

	// 更新导入并移动文件：结果仍正确，身份记录新路径。
	mainRenamed := strings.Replace(main, "sumTo", "sum", 2)
	writeSource(t, filepath.Join(dir, "main.ts"), mainRenamed)
	moved, _, err := cliBuild(t, a, entryRel, output)
	if err != nil {
		t.Fatalf("更新导出导入后构建失败：%v\n%s", err, a.stderr)
	}
	if got := runBuilt(t, output); got != "56\n" {
		t.Fatalf("更新导出后输出为 %q，期望 56", got)
	}
	if moved.Identity.Hash == changed.Identity.Hash {
		t.Error("导出变化没有改变构建身份")
	}

	if err := os.Rename(filepath.Join(dir, "math.ts"), filepath.Join(dir, "arithmetic.ts")); err != nil {
		t.Fatal(err)
	}
	mainMoved := strings.Replace(mainRenamed, "./math.ts", "./arithmetic.ts", 1)
	writeSource(t, filepath.Join(dir, "main.ts"), mainMoved)
	movedFile, _, err := cliBuild(t, a, entryRel, output)
	if err != nil {
		t.Fatalf("移动依赖后构建失败：%v\n%s", err, a.stderr)
	}
	if got := runBuilt(t, output); got != "56\n" {
		t.Fatalf("移动依赖后输出为 %q，期望 56", got)
	}
	if movedFile.Identity.Hash == moved.Identity.Hash {
		t.Error("移动依赖没有改变构建身份")
	}
	seen := map[string]bool{}
	for _, file := range movedFile.Identity.Files {
		seen[filepath.Base(file.Path)] = true
	}
	if !seen["arithmetic.ts"] || seen["math.ts"] {
		t.Errorf("移动后的身份文件不对：%+v", movedFile.Identity.Files)
	}

	// 纯类型依赖变化但 NIR 不变：检查重新发生、完整身份更新；产物输入身份
	// 与 build tag 不变，因为生成代码确实没变。
	writeSource(t, filepath.Join(dir, "types.ts"), types+"\n// type-only note\n")
	typeOnly, _, err := cliBuild(t, a, entryRel, output)
	if err != nil {
		t.Fatalf("纯类型注释变化后构建失败：%v\n%s", err, a.stderr)
	}
	if typeOnly.Identity.Hash == movedFile.Identity.Hash {
		t.Error("纯类型依赖变化没有改变完整身份")
	}
	if typeOnly.Identity.ArtifactSHA256 != movedFile.Identity.ArtifactSHA256 {
		t.Error("NIR 未变时产物输入身份不应变化")
	}
	if typeOnly.Tools.BuildTag != movedFile.Tools.BuildTag {
		t.Error("NIR 未变时不应换 build tag")
	}
	if got := runBuilt(t, output); got != "56\n" {
		t.Fatalf("纯类型变化后输出为 %q，期望 56", got)
	}
	recordBeforeMissing, err := os.ReadFile(build.RecordPath(output))
	if err != nil {
		t.Fatal(err)
	}
	artifactBeforeMissing := hashOf(t, output)

	// 缺依赖：在启动后端前失败，旧成功产物和记录保持原样。
	if err := os.Remove(filepath.Join(dir, "arithmetic.ts")); err != nil {
		t.Fatal(err)
	}
	_, _, err = cliBuild(t, a, entryRel, output)
	if err == nil {
		t.Fatal("缺失依赖应失败")
	}
	if code := exitOf(err); code != diagnostic.ExitSource {
		t.Fatalf("缺失依赖退出状态为 %d，期望 1（%v）", code, err)
	}
	if got := hashOf(t, output); got != artifactBeforeMissing {
		t.Error("缺失依赖时改动了旧产物")
	}
	if got, _ := os.ReadFile(build.RecordPath(output)); string(got) != string(recordBeforeMissing) {
		t.Error("缺失依赖时改动了旧记录")
	}
}
