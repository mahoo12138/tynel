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
