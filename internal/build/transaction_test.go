package build

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
	"github.com/Mahoo12138/tynel/internal/toolchain"
)

func writePair(t *testing.T, output, content string) {
	t.Helper()
	writeFile(t, output, content)
	record := &Record{SchemaVersion: 1}
	record.Output.Path = output
	record.Output.SHA256 = sha256Hex([]byte(content))
	record.Output.Bytes = int64(len(content))
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, RecordPath(output), string(data))
}

func assertNoTransactionFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".tynel-build-") ||
			strings.HasPrefix(name, ".tynel-record-") ||
			strings.HasPrefix(name, ".tynel-tmp-") ||
			strings.HasSuffix(name, ".pending") ||
			strings.HasSuffix(name, ".artifact.bak") ||
			strings.HasSuffix(name, ".record.bak") {
			t.Errorf("留下了发布临时文件 %s", name)
		}
	}
}

func TestRunPublishesConsistentPair(t *testing.T) {
	root, runner := newProject(t)
	runner.writeOutput = true
	output := filepath.Join(root, "out", "app")

	record, err := Run(Options{Root: root, InputData: exampleNIR(t), Output: output}, runner)
	if err != nil {
		t.Fatalf("构建失败：%v", err)
	}
	if record.Output.SHA256 != sha256Hex([]byte("binary-content")) {
		t.Errorf("记录里的产物哈希不符：%s", record.Output.SHA256)
	}
	if err := VerifyPair(output); err != nil {
		t.Errorf("产物与记录应互相对应：%v", err)
	}
	assertNoTransactionFiles(t, filepath.Dir(output))
}

func TestVerifyPairRejectsMismatchedRecord(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "app")
	writePair(t, output, "old-binary")
	writeFile(t, output, "tampered")

	err := VerifyPair(output)
	if err == nil {
		t.Fatal("产物被改动后记录不应继续匹配")
	}
	var d *diagnostic.Diagnostic
	if !errors.As(err, &d) || d.Code != "build.record-mismatch" {
		t.Fatalf("错误码应为 build.record-mismatch：%v", err)
	}
}

func TestRunRejectsConcurrentOutput(t *testing.T) {
	root, runner := newProject(t)
	runner.writeOutput = true
	output := filepath.Join(root, "out", "app")
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		t.Fatal(err)
	}
	// 锁键按规范路径生成：Run 会先解析输出目录（macOS 的临时目录本身经过
	// /var -> /private/var 符号链接）。测试持有锁时也用同一条规范路径，
	// 才能模拟同一个输出上的并发构建。
	canonicalDir, err := filepath.EvalSymlinks(filepath.Dir(output))
	if err != nil {
		t.Fatal(err)
	}
	output = filepath.Join(canonicalDir, filepath.Base(output))
	lock, err := acquireOutputLock(output)
	if err != nil {
		t.Fatalf("无法取得测试锁：%v", err)
	}
	defer lock.release()

	_, err = Run(Options{Root: root, InputData: exampleNIR(t), Output: output}, runner)
	if err == nil {
		t.Fatal("同一输出已有构建时应当拒绝")
	}
	if got := diagnostic.ExitCode(err); got != diagnostic.ExitEnvironment {
		t.Errorf("退出状态为 %d，期望 %d（%v）", got, diagnostic.ExitEnvironment, err)
	}
	var d *diagnostic.Diagnostic
	if !errors.As(err, &d) || d.Code != "build.output-busy" {
		t.Errorf("错误码应为 build.output-busy：%v", err)
	}
	if runner.calls != 0 {
		t.Errorf("持有锁时不应启动后端，实际 %d 次", runner.calls)
	}
	if _, statErr := os.Stat(output); !os.IsNotExist(statErr) {
		t.Errorf("持有锁时不应产出文件")
	}
}

func TestRunReleasesLockAfterSuccessAndFailure(t *testing.T) {
	root, runner := newProject(t)
	runner.writeOutput = true
	output := filepath.Join(root, "out", "app")
	options := Options{Root: root, InputData: exampleNIR(t), Output: output}

	if _, err := Run(options, runner); err != nil {
		t.Fatalf("首次构建失败：%v", err)
	}
	runner.exit = 1
	runner.stderr = "link: failed"
	if _, err := Run(options, runner); err == nil {
		t.Fatal("后端失败应返回错误")
	}
	runner.exit = 0
	runner.stderr = ""
	if _, err := Run(options, runner); err != nil {
		t.Fatalf("失败后锁应已释放：%v", err)
	}
}

func TestCommitRestoresOldPairOnRecordFailure(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "app")
	writePair(t, output, "old-binary")

	p := newPublication(output)
	p.stagedOutput = filepath.Join(dir, "new-artifact")
	p.stagedRecord = filepath.Join(dir, "missing-record")
	writeFile(t, p.stagedOutput, "new-binary")

	if err := p.commit(); err == nil {
		t.Fatal("记录提交失败时应返回错误")
	}
	if data, _ := os.ReadFile(output); string(data) != "old-binary" {
		t.Errorf("发布失败后旧产物没有恢复：%q", data)
	}
	if err := VerifyPair(output); err != nil {
		t.Errorf("旧产物与旧记录应重新一致：%v", err)
	}
	assertNoTransactionFiles(t, dir)
}

func TestCommitLeavesNoArtifactWhenRecordFailsWithoutOldPair(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "app")
	p := newPublication(output)
	p.stagedOutput = filepath.Join(dir, "new-artifact")
	p.stagedRecord = filepath.Join(dir, "missing-record")
	writeFile(t, p.stagedOutput, "new-binary")

	if err := p.commit(); err == nil {
		t.Fatal("记录提交失败时应返回错误")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Errorf("没有记录的产物不应留下：%v", err)
	}
	assertNoTransactionFiles(t, dir)
}

func TestRecoverRollsBackInterruptedPublish(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "app")
	writePair(t, output, "old-binary")
	p := newPublication(output)
	if err := os.Rename(output, p.backupOutput); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(RecordPath(output), p.backupRecord); err != nil {
		t.Fatal(err)
	}
	writeFile(t, output, "new-binary-without-record")
	writeFile(t, p.pending, "1\n")

	if err := p.recover(); err != nil {
		t.Fatalf("恢复失败：%v", err)
	}
	if data, _ := os.ReadFile(output); string(data) != "old-binary" {
		t.Errorf("应恢复旧产物，实际 %q", data)
	}
	if err := VerifyPair(output); err != nil {
		t.Errorf("恢复后旧产物与旧记录应一致：%v", err)
	}
	assertNoTransactionFiles(t, dir)
}

func TestRecoverKeepsOldRecordWhenOnlyArtifactMoved(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "app")
	writePair(t, output, "old-binary")
	p := newPublication(output)
	if err := os.Rename(output, p.backupOutput); err != nil {
		t.Fatal(err)
	}
	writeFile(t, output, "new-binary-without-record")
	writeFile(t, p.pending, `{"pid":1,"hadOutput":true,"hadRecord":true}`+"\n")

	if err := p.recover(); err != nil {
		t.Fatalf("恢复失败：%v", err)
	}
	if data, _ := os.ReadFile(output); string(data) != "old-binary" {
		t.Errorf("应恢复旧产物，实际 %q", data)
	}
	if err := VerifyPair(output); err != nil {
		t.Errorf("旧记录不应被误删：%v", err)
	}
	assertNoTransactionFiles(t, dir)
}

func TestRecoverKeepsCompletedPair(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "app")
	writePair(t, output, "old-binary")
	p := newPublication(output)
	if err := os.Rename(output, p.backupOutput); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(RecordPath(output), p.backupRecord); err != nil {
		t.Fatal(err)
	}
	writePair(t, output, "new-binary")
	writeFile(t, p.pending, "1\n")

	if err := p.recover(); err != nil {
		t.Fatalf("恢复失败：%v", err)
	}
	if data, _ := os.ReadFile(output); string(data) != "new-binary" {
		t.Errorf("已经写完的一对应保留，实际 %q", data)
	}
	if err := VerifyPair(output); err != nil {
		t.Errorf("恢复后新产物与新记录应一致：%v", err)
	}
	assertNoTransactionFiles(t, dir)
}

type cancelRunner struct {
	fakeRunner
	started chan struct{}
	once    sync.Once
}

func (r *cancelRunner) RunEnvContext(ctx context.Context, _ []string, _ string, _ ...string) (toolchain.Output, error) {
	r.once.Do(func() { close(r.started) })
	<-ctx.Done()
	return toolchain.Output{}, ctx.Err()
}

func TestRunStopsOnCancellation(t *testing.T) {
	root, _ := newProject(t)
	output := filepath.Join(root, "out", "app")
	writeFile(t, output, "old")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner := &cancelRunner{started: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		_, err := Run(Options{Root: root, InputData: exampleNIR(t), Output: output, Context: ctx}, runner)
		done <- err
	}()
	select {
	case <-runner.started:
	case <-time.After(5 * time.Second):
		t.Fatal("后端没有启动")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("取消后应返回错误")
		}
		var d *diagnostic.Diagnostic
		if !errors.As(err, &d) || d.Code != "build.canceled" {
			t.Fatalf("错误码应为 build.canceled：%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("取消后构建没有结束")
	}
	if data, _ := os.ReadFile(output); string(data) != "old" {
		t.Errorf("取消时改动了旧产物：%q", data)
	}
	assertNoTransactionFiles(t, filepath.Dir(output))
}

type mutateGoRunner struct {
	fakeRunner
	goPath string
}

func (r *mutateGoRunner) RunEnv(env []string, name string, args ...string) (toolchain.Output, error) {
	out, err := r.fakeRunner.RunEnv(env, name, args...)
	if err == nil && out.ExitCode == 0 {
		_ = os.WriteFile(r.goPath, []byte("#!/bin/sh\n# replaced\n"), 0o755)
	}
	return out, err
}

func TestRunRechecksGoTool(t *testing.T) {
	root, _ := newProject(t)
	runner := &mutateGoRunner{goPath: filepath.Join(root, ".work", "toolchain", "go1.27.3-sdk-darwin-arm64", "bin", "go")}
	runner.writeOutput = true
	output := filepath.Join(root, "out", "app")
	writeFile(t, output, "old")

	_, err := Run(Options{Root: root, InputData: exampleNIR(t), Output: output}, runner)
	if err == nil {
		t.Fatal("工具在构建期间变化时应拒绝发布")
	}
	if !strings.Contains(err.Error(), "build.input-changed") {
		t.Fatalf("错误码应为 build.input-changed：%v", err)
	}
	if data, _ := os.ReadFile(output); string(data) != "old" {
		t.Errorf("工具变化时覆盖了旧产物：%q", data)
	}
}

func TestRunIgnoresLegacyArchiveDirectories(t *testing.T) {
	root, runner := newProject(t)
	runner.writeOutput = true
	writeFile(t, filepath.Join(root, ".tynel", "bridge", "compile"), "legacy compiler")
	writeFile(t, filepath.Join(root, ".upstream", "bridge", "compile"), "upstream compiler")

	record, err := Run(Options{Root: root, InputData: exampleNIR(t), Output: filepath.Join(root, "out", "app")}, runner)
	if err != nil {
		t.Fatalf("构建失败：%v", err)
	}
	if !strings.Contains(record.Tools.Compiler, filepath.FromSlash(".work/bridge/compile")) {
		t.Errorf("编译器应来自 .work：%q", record.Tools.Compiler)
	}
	if strings.Contains(record.Tools.Compiler, ".tynel") || strings.Contains(record.Tools.Compiler, ".upstream") {
		t.Errorf("不应读取旧归档目录：%q", record.Tools.Compiler)
	}
	if !strings.Contains(record.Input.Path, filepath.FromSlash(".work/build/snapshots")) {
		t.Errorf("快照应来自 .work：%q", record.Input.Path)
	}
}

// TestRunRejectsConcurrentOutputThroughDirSymlink 确认目录符号链接不能绕过
// 构建锁：真实目录与指向它的 symlink 解析到同一个目录项，锁键必须一致，
// 第二个构建无法并发进入发布阶段。filepath.Abs 不解析目录 symlink，旧实现
// 用未解析的路径字符串生成锁键，两条路径各拿一把锁，会同时替换同一产物与
// 记录，失败回滚还可能删掉另一个进程刚发布的文件。
func TestRunRejectsConcurrentOutputThroughDirSymlink(t *testing.T) {
	root, runner := newProject(t)
	runner.writeOutput = true
	realDir := filepath.Join(root, "dist")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	linkDir := filepath.Join(root, "output-link")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Fatal(err)
	}
	// 锁键按规范路径生成（生产里 Run 会先解析目录）；测试持有锁时也用它，
	// 模拟「真实目录上已有一个构建在发布」。
	canonicalReal, err := filepath.EvalSymlinks(realDir)
	if err != nil {
		t.Fatal(err)
	}
	realOutput := filepath.Join(canonicalReal, "app")
	linkOutput := filepath.Join(linkDir, "app")

	lock, err := acquireOutputLock(realOutput)
	if err != nil {
		t.Fatalf("无法取得测试锁：%v", err)
	}
	defer lock.release()

	_, err = Run(Options{Root: root, InputData: exampleNIR(t), Output: linkOutput}, runner)
	if err == nil {
		t.Fatal("经目录 symlink 指向同一输出时应当拒绝并发构建")
	}
	var d *diagnostic.Diagnostic
	if !errors.As(err, &d) || d.Code != "build.output-busy" {
		t.Fatalf("错误码应为 build.output-busy：%v", err)
	}
	if runner.calls != 0 {
		t.Errorf("持有锁时不应启动后端，实际 %d 次", runner.calls)
	}
	if _, statErr := os.Stat(realOutput); !os.IsNotExist(statErr) {
		t.Errorf("持有锁时不应产出文件")
	}
}

// TestRunPublishesThroughDirSymlink 确认经目录 symlink 的构建仍把产物与记录
// 发布到真实目录，且两者配对一致。
func TestRunPublishesThroughDirSymlink(t *testing.T) {
	root, runner := newProject(t)
	runner.writeOutput = true
	realDir := filepath.Join(root, "dist")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	linkDir := filepath.Join(root, "output-link")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Fatal(err)
	}
	linkOutput := filepath.Join(linkDir, "app")

	if _, err := Run(Options{Root: root, InputData: exampleNIR(t), Output: linkOutput}, runner); err != nil {
		t.Fatalf("经 symlink 构建失败：%v", err)
	}
	canonicalReal, err := filepath.EvalSymlinks(realDir)
	if err != nil {
		t.Fatal(err)
	}
	realOutput := filepath.Join(canonicalReal, "app")
	if err := VerifyPair(realOutput); err != nil {
		t.Errorf("真实目录上的产物与记录应配对一致：%v", err)
	}
	assertNoTransactionFiles(t, canonicalReal)
}
