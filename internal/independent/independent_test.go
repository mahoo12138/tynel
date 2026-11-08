package independent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultProgramsHaveIndependentExpectations(t *testing.T) {
	programs := DefaultPrograms()
	if len(programs) != 4 {
		t.Fatalf("程序数为 %d，期望 4", len(programs))
	}
	want := map[string]string{
		"add": "42\n", "choices": "-1\n0\n1\n1\nfalse\n3\ntrue\n",
		"collatz": "111\n111\n8\n8\n0\n", "quote": "90\n10\n100\n",
	}
	for _, program := range programs {
		if program.Expected != want[program.Name] {
			t.Errorf("%s 的期望输出为 %q，期望 %q", program.Name, program.Expected, want[program.Name])
		}
	}
}

func TestReadBuildRecordRequiresIdentity(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "add.build.json")
	writeTestJSON(t, path, `{"output":{"sha256":"`+strings.Repeat("a", 64)+`","bytes":42}}`)
	record, err := readBuildRecord(path)
	if err != nil {
		t.Fatal(err)
	}
	if record.Output.Bytes != 42 {
		t.Fatalf("字节数为 %d，期望 42", record.Output.Bytes)
	}
	writeTestJSON(t, path, `{"output":{"sha256":"not-a-hash","bytes":42}}`)
	if _, err := readBuildRecord(path); err == nil || !strings.Contains(err.Error(), "非法") {
		t.Fatalf("坏哈希没有被拒绝：%v", err)
	}
}

func TestPrepareArtifactsRejectsChangedExecutable(t *testing.T) {
	root := t.TempDir()
	artifacts := filepath.Join(root, "artifacts")
	payload := filepath.Join(root, "payload")
	if err := os.MkdirAll(artifacts, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(payload, 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(artifacts, "add")
	if err := os.WriteFile(source, []byte("changed"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestJSON(t, source+".build.json", `{"output":{"sha256":"`+strings.Repeat("0", 64)+`","bytes":7}}`)
	_, err := prepareArtifacts(artifacts, payload, []Program{{Name: "add"}})
	if err == nil || !strings.Contains(err.Error(), "与构建记录不符") {
		t.Fatalf("修改后的 exe 没有被拒绝：%v", err)
	}
	if _, statErr := os.Stat(filepath.Join(payload, "add")); !os.IsNotExist(statErr) {
		t.Fatalf("身份不符时不应发布载荷：%v", statErr)
	}
}

func TestCopyRegularPreservesBytesAndMode(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	target := filepath.Join(root, "target")
	if err := os.WriteFile(source, []byte("native bytes\x00"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := copyRegular(source, target, 0o755); err != nil {
		t.Fatal(err)
	}
	sourceSum, _ := hashFile(source)
	targetSum, _ := hashFile(target)
	if sourceSum != targetSum {
		t.Fatalf("复制前后哈希不同：%s != %s", sourceSum, targetSum)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("目标权限为 %o，期望 755", info.Mode().Perm())
	}
	if err := copyRegular(source, target, 0o755); err == nil {
		t.Fatal("复制不应覆盖已经存在的载荷")
	}
}

func TestValidateLibraries(t *testing.T) {
	valid := []string{"/usr/lib/libSystem.B.dylib", "/usr/lib/libresolv.9.dylib"}
	if err := validateLibraries(valid); err != nil {
		t.Fatalf("系统库白名单不应失败：%v", err)
	}
	if err := validateLibraries([]string{"/opt/homebrew/lib/libnode.dylib"}); err == nil || !strings.Contains(err.Error(), "libnode") {
		t.Fatalf("未声明动态库没有被拒绝：%v", err)
	}
}

func TestSandboxProfileDeniesChildrenAndProtectsInputs(t *testing.T) {
	runner := sandboxRunner{
		payloadDir:     "/tmp/载荷 with space",
		protectedRoots: []string{"/Users/example", "/Users/example/project"},
		nodePaths:      []string{"/opt/node/bin/node"},
	}
	command := "/tmp/载荷 with space/add"
	profile, err := runner.profile(command)
	if err != nil {
		t.Fatal(err)
	}
	checks := []string{
		"(deny process-exec)",
		`(deny file-read* (subpath "/Users/example"))`,
		`(deny file-read* (literal "/opt/node/bin/node"))`,
		`(allow file-read* (subpath "/tmp/载荷 with space"))`,
		`(allow process-exec (literal "/tmp/载荷 with space/add"))`,
	}
	for _, check := range checks {
		if !strings.Contains(profile, check) {
			t.Errorf("沙箱配置缺少 %q：\n%s", check, profile)
		}
	}
	if strings.LastIndex(profile, "(allow file-read*") < strings.LastIndex(profile, "(deny file-read*") {
		t.Error("载荷允许规则必须位于所有读取拒绝规则之后")
	}
}

func TestSBPLQuoteAndEnvironment(t *testing.T) {
	quoted, err := sbplQuote(`/tmp/a\"b`)
	if err != nil {
		t.Fatal(err)
	}
	if quoted != `"/tmp/a\\\"b"` {
		t.Fatalf("引用结果为 %q", quoted)
	}
	if _, err := sbplQuote("bad\npath"); err == nil {
		t.Fatal("路径控制字符没有被拒绝")
	}
	env := isolatedEnvironment("/tmp/payload")
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "PATH=/nonexistent") || strings.Contains(joined, "NODE") {
		t.Fatalf("最小环境不符合预期：%v", env)
	}
}

func TestAccessDeniedDistinguishesSandboxFailure(t *testing.T) {
	if !accessDenied(RunResult{ExitCode: 1, Stderr: "cat: source.ts: Operation not permitted"}) {
		t.Fatal("真实的文件拒绝应通过探针")
	}
	if accessDenied(RunResult{ExitCode: 71, Stderr: "sandbox-exec: sandbox_apply: Operation not permitted"}) {
		t.Fatal("沙箱无法启动不能冒充文件拒绝成功")
	}
	if accessDenied(RunResult{ExitCode: 0}) {
		t.Fatal("成功读取不能通过拒绝探针")
	}
}

func TestCappedLogBoundsOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.log")
	file, writer, err := newCappedLog(path)
	if err != nil {
		t.Fatal(err)
	}
	input := make([]byte, maxLogBytes+100)
	n, err := writer.Write(input)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(input) || !writer.truncated {
		t.Fatalf("写入结果 n=%d truncated=%t", n, writer.truncated)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != maxLogBytes {
		t.Fatalf("日志大小为 %d，期望上限 %d", info.Size(), maxLogBytes)
	}
}

func writeTestJSON(t *testing.T, path, content string) {
	t.Helper()
	var value any
	if err := json.Unmarshal([]byte(content), &value); err != nil {
		t.Fatalf("测试 JSON 非法：%v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
