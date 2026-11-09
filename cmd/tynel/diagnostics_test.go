package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
	"github.com/Mahoo12138/tynel/compiler/pipeline"
)

func decodeEnvelope(t *testing.T, data []byte) diagnostic.Envelope {
	t.Helper()
	var envelope diagnostic.Envelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatalf("诊断信封无法解析：%v\n%s", err, data)
	}
	return envelope
}

func TestJSONDiagnosticsFromCLI(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run([]string{"--diagnostics=json", "check", "--root", t.TempDir(), "missing.ts"}, &out, &errOut)
	if code != diagnostic.ExitSource {
		t.Fatalf("退出状态为 %d，期望 %d", code, diagnostic.ExitSource)
	}
	if out.Len() != 0 {
		t.Errorf("失败时 stdout 应为空：%q", out.String())
	}
	envelope := decodeEnvelope(t, errOut.Bytes())
	if envelope.ExitCode != diagnostic.ExitSource || len(envelope.Diagnostics) != 1 {
		t.Fatalf("信封不对：%+v", envelope)
	}
	item := envelope.Diagnostics[0]
	if item.Category != "source" || item.Code != "project.missing" || item.Stage != diagnostic.StageProject {
		t.Errorf("诊断字段不对：%+v", item)
	}
}

func TestJSONDiagnosticsSourceResultMatchesHuman(t *testing.T) {
	jsonOut := &bytes.Buffer{}
	var machine bytes.Buffer
	jsonApp := &app{stdout: jsonOut, stderr: &bytes.Buffer{}, diag: &machine, runner: &fileRunner{}, jsonDiagnostics: true}
	jsonApp.frontend = func(file, want string) (*pipeline.Result, error) {
		return sourceErrorResult(file), nil
	}
	if code := exitOf(jsonApp.check([]string{"bad.ts"})); code != diagnostic.ExitSource {
		t.Fatalf("JSON 模式退出状态为 %d，期望 1", code)
	}
	if jsonOut.Len() != 0 {
		t.Errorf("失败时 stdout 应为空：%q", jsonOut.String())
	}
	envelope := decodeEnvelope(t, machine.Bytes())
	if envelope.ExitCode != diagnostic.ExitSource || len(envelope.Diagnostics) != 1 {
		t.Fatalf("信封不对：%+v", envelope)
	}
	item := envelope.Diagnostics[0]
	if item.Category != "source" || item.Code != "TS2322" || item.Stage != diagnostic.StageFrontend || item.Severity != "error" {
		t.Errorf("诊断字段不对：%+v", item)
	}
	if item.File != "bad.ts" || item.Line != 2 || item.Column != 7 {
		t.Errorf("位置不对：%+v", item)
	}

	var humanErr bytes.Buffer
	humanApp := &app{stdout: &bytes.Buffer{}, stderr: &humanErr, runner: &fileRunner{}}
	humanApp.frontend = jsonApp.frontend
	if code := exitOf(humanApp.check([]string{"bad.ts"})); code != diagnostic.ExitSource {
		t.Fatalf("人类模式退出状态为 %d，期望 1", code)
	}
	if !strings.Contains(humanErr.String(), "bad.ts:2:7: error TS2322") {
		t.Errorf("人类输出没有相同位置和错误码：%q", humanErr.String())
	}
}

// outputProgram 写两路输出，用来证明 run 不把程序自己的 stdout/stderr 当成
// 编译器诊断，也不在 JSON 模式里吞掉它们。
type outputProgram struct{}

func (outputProgram) Run(_ string, stdout, stderr io.Writer) (int, error) {
	fmt.Fprintln(stdout, "program-out")
	fmt.Fprintln(stderr, "program-err")
	return 0, nil
}

func TestJSONModeKeepsProgramOutputSeparate(t *testing.T) {
	root := newBackendProject(t)
	var stdout, stderr, diag bytes.Buffer
	a := &app{
		stdout:          &stdout,
		stderr:          &stderr,
		diag:            &diag,
		jsonDiagnostics: true,
		runner:          &fileRunner{},
		program:         outputProgram{},
		root:            root,
		hasRoot:         true,
	}
	a.frontend = func(file, want string) (*pipeline.Result, error) {
		return okResult(file), nil
	}
	if err := a.run([]string{"add.ts"}); err != nil {
		t.Fatalf("run 失败：%v", err)
	}
	if stdout.String() != "program-out\n" {
		t.Errorf("程序 stdout 被改动：%q", stdout.String())
	}
	if stderr.String() != "program-err\n" {
		t.Errorf("程序 stderr 被改动：%q", stderr.String())
	}
	if diag.Len() != 0 {
		t.Errorf("成功运行不应产生诊断：%q", diag.String())
	}
}

func TestJSONToolFailureIsInternalNotSource(t *testing.T) {
	root := newBackendProject(t)
	var stdout, stderr, diag bytes.Buffer
	runner := &fileRunner{exit: 1, stderr: "signal: killed"}
	a := &app{
		stdout:          &stdout,
		stderr:          &stderr,
		diag:            &diag,
		jsonDiagnostics: true,
		runner:          runner,
		root:            root,
		hasRoot:         true,
	}
	a.frontend = func(file, want string) (*pipeline.Result, error) {
		return okResult(file), nil
	}
	err := a.build([]string{"add.ts", "-o", filepath.Join(t.TempDir(), "app")})
	if err == nil {
		t.Fatal("工具崩溃应返回错误")
	}
	if got := diagnostic.ExitCode(err); got != diagnostic.ExitInternal {
		t.Fatalf("退出状态为 %d，期望 %d（%v）", got, diagnostic.ExitInternal, err)
	}
	if err := a.writeError(err); err != nil {
		t.Fatalf("写诊断失败：%v", err)
	}
	envelope := decodeEnvelope(t, diag.Bytes())
	if envelope.ExitCode != diagnostic.ExitInternal || len(envelope.Diagnostics) != 1 {
		t.Fatalf("信封不对：%+v", envelope)
	}
	item := envelope.Diagnostics[0]
	if item.Category != "internal" || item.Stage != diagnostic.StageBackend || !strings.Contains(item.Detail, "signal: killed") {
		t.Errorf("工具故障不应伪装成源码错误：%+v", item)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Errorf("JSON 模式不应把工具失败写到程序输出：stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestJSONOutputFailureIsEnvironment(t *testing.T) {
	root := newBackendProject(t)
	blocked := filepath.Join(root, "blocked")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr, diag bytes.Buffer
	a := &app{
		stdout:          &stdout,
		stderr:          &stderr,
		diag:            &diag,
		jsonDiagnostics: true,
		runner:          &fileRunner{},
		root:            root,
		hasRoot:         true,
	}
	a.frontend = func(file, want string) (*pipeline.Result, error) {
		return okResult(file), nil
	}
	err := a.build([]string{"add.ts", "-o", filepath.Join(blocked, "app")})
	if err == nil {
		t.Fatal("不可写输出应返回错误")
	}
	if got := diagnostic.ExitCode(err); got != diagnostic.ExitEnvironment {
		t.Fatalf("退出状态为 %d，期望 %d（%v）", got, diagnostic.ExitEnvironment, err)
	}
	if err := a.writeError(err); err != nil {
		t.Fatalf("写诊断失败：%v", err)
	}
	item := decodeEnvelope(t, diag.Bytes()).Diagnostics[0]
	if item.Category != "environment" || item.Stage != diagnostic.StageBuild {
		t.Errorf("输出失败应按环境缺失报告：%+v", item)
	}
}

func TestJSONModeUnknownCommand(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run([]string{"--diagnostics=json", "frobnicate"}, &out, &errOut)
	if code != diagnostic.ExitUsage {
		t.Fatalf("退出状态为 %d，期望 %d", code, diagnostic.ExitUsage)
	}
	envelope := decodeEnvelope(t, errOut.Bytes())
	item := envelope.Diagnostics[0]
	if item.Category != "usage" || item.Stage != diagnostic.StageCLI || !strings.Contains(item.Message, "未知命令") {
		t.Errorf("未知命令诊断不对：%+v", item)
	}
}

func TestJSONModeMissingCommand(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run([]string{"--diagnostics=json"}, &out, &errOut)
	if code != diagnostic.ExitUsage {
		t.Fatalf("退出状态为 %d，期望 %d", code, diagnostic.ExitUsage)
	}
	if out.Len() != 0 {
		t.Errorf("失败时 stdout 应为空：%q", out.String())
	}
	envelope := decodeEnvelope(t, errOut.Bytes())
	item := envelope.Diagnostics[0]
	if item.Category != "usage" || item.Code != "cli.missing-command" || item.Stage != diagnostic.StageCLI {
		t.Errorf("无命令诊断不对：%+v", item)
	}
}

// TestDependencyTypeErrorKeepsSourcePosition 走真实前端：依赖里的类型错误
// 必须在检查阶段按源码错误返回，并带上依赖文件自己的行列；它不能溜进合并
// 降低，最后被报成 lower.bad-type。
func TestDependencyTypeErrorKeepsSourcePosition(t *testing.T) {
	root := realRepoRoot(t)
	dir, err := os.MkdirTemp(filepath.Join(root, ".work"), "cross-file-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	writeSource(t, filepath.Join(dir, "main.ts"), "import { value } from \"./math.ts\";\n\nconsole.log(value);\n")
	mathPath := filepath.Join(dir, "math.ts")
	writeSource(t, mathPath, "export const value: number = \"not a number\";\n")

	var stdout, stderr bytes.Buffer
	code := run([]string{"check", "--root", dir, "main.ts"}, &stdout, &stderr)
	if code != diagnostic.ExitSource {
		t.Fatalf("退出状态为 %d，期望 %d；stderr=%s", code, diagnostic.ExitSource, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("失败时 stdout 应为空：%q", stdout.String())
	}
	if !strings.Contains(stderr.String(), mathPath+":1:14: error TS2322") {
		t.Errorf("应给出依赖文件的源码位置：%s", stderr.String())
	}
	if strings.Contains(stderr.String(), "lower.bad-type") {
		t.Errorf("源码错误被降级成了内部故障：%s", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"--diagnostics=json", "check", "--root", dir, "main.ts"}, &stdout, &stderr)
	if code != diagnostic.ExitSource {
		t.Fatalf("JSON 模式退出状态为 %d，期望 %d", code, diagnostic.ExitSource)
	}
	item := decodeEnvelope(t, stderr.Bytes()).Diagnostics[0]
	if item.Category != "source" || item.Code != "TS2322" || item.Stage != diagnostic.StageFrontend {
		t.Errorf("跨文件诊断字段不对：%+v", item)
	}
	if item.File != mathPath || item.Line != 1 || item.Column != 14 {
		t.Errorf("跨文件诊断位置不对：%+v", item)
	}
}

// TestDependencySyntaxErrorNotMaskedByModuleProbe 确认失败路径上的模块探测
// 不会把已经定位好的语法错误替换成事实提取失败或内部故障。
func TestDependencySyntaxErrorNotMaskedByModuleProbe(t *testing.T) {
	root := realRepoRoot(t)
	dir, err := os.MkdirTemp(filepath.Join(root, ".work"), "syntax-file-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	writeSource(t, filepath.Join(dir, "main.ts"), "import { value } from \"./math.ts\";\n\nconsole.log(value);\n")
	mathPath := filepath.Join(dir, "math.ts")
	writeSource(t, mathPath, "export const value: number = ;\n")

	var stdout, stderr bytes.Buffer
	code := run([]string{"check", "--root", dir, "main.ts"}, &stdout, &stderr)
	if code != diagnostic.ExitSource {
		t.Fatalf("退出状态为 %d，期望 %d；stderr=%s", code, diagnostic.ExitSource, stderr.String())
	}
	if !strings.Contains(stderr.String(), mathPath+":") {
		t.Errorf("应保留依赖文件的语法错误位置：%s", stderr.String())
	}
	for _, unwanted := range []string{"host.facts-failed", "lower.bad-type", "facts.bad-position"} {
		if strings.Contains(stderr.String(), unwanted) {
			t.Errorf("语法错误被中间故障覆盖（%s）：%s", unwanted, stderr.String())
		}
	}
}

// TestBuiltinGlobalCallIsCapabilityError 走真实前端确认：标准库全局函数的
// 声明文件也会被 target() 算成 CalleeModule，但项目里没有导入能接住它。
// 它必须在能力检查阶段按退出 3 拒绝，不能溜到降低阶段变成内部故障。
func TestBuiltinGlobalCallIsCapabilityError(t *testing.T) {
	root := realRepoRoot(t)
	dir, err := os.MkdirTemp(filepath.Join(root, ".work"), "global-call-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	writeSource(t, filepath.Join(dir, "main.ts"), "console.log(isFinite(1));\n")

	var stdout, stderr bytes.Buffer
	code := run([]string{"check", "--root", dir, "main.ts"}, &stdout, &stderr)
	if code != diagnostic.ExitCapability {
		t.Fatalf("退出状态为 %d，期望 %d；stderr=%s", code, diagnostic.ExitCapability, stderr.String())
	}
	if !strings.Contains(stderr.String(), "cap.unknown-callee") {
		t.Errorf("应报 cap.unknown-callee：%s", stderr.String())
	}
	for _, unwanted := range []string{"lower.unknown-callee", "host.failed"} {
		if strings.Contains(stderr.String(), unwanted) {
			t.Errorf("不应出现 %s：%s", unwanted, stderr.String())
		}
	}
}

// TestProjectImportsStillResolve 确认这次收紧只针对「凭模块名放行」这一条：
// 真实项目模块的跨模块调用与顶层 const 引用仍然能一路走到原生产物。
// 重命名导入在这里很关键：CalleeName 是导出名，源码里写的却是本地名。
func TestProjectImportsStillResolve(t *testing.T) {
	root := realRepoRoot(t)
	dir, err := os.MkdirTemp(filepath.Join(root, ".work"), "project-import-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	writeSource(t, filepath.Join(dir, "lib.ts"),
		"export function helper(n: number): number { return n + 1; }\n"+
			"export const offset: number = 5;\n")
	writeSource(t, filepath.Join(dir, "main.ts"),
		"import { helper as h, offset as o } from \"./lib.ts\";\n"+
			"console.log(h(1) + o);\n")

	output := filepath.Join(dir, "app")
	var stdout, stderr bytes.Buffer
	code := run([]string{"build", "main.ts", "--root", dir, "-o", output}, &stdout, &stderr)
	if code != diagnostic.ExitOK {
		t.Fatalf("构建退出状态为 %d，期望 0；stderr=%s", code, stderr.String())
	}
	if got := runBuilt(t, output); got != "7\n" {
		t.Fatalf("产物输出 %q，期望 %q", got, "7\n")
	}
}

// TestDefaultParameterIsCapabilityError 确认默认参数不管出现在入口还是依赖
// 模块，都按能力限制拒绝。少一个实参的 add(5) 是合法 TypeScript，不能落到
// facts.arg-count 或 lower.bad-arity 这种内部故障上。
func TestDefaultParameterIsCapabilityError(t *testing.T) {
	root := realRepoRoot(t)
	cases := []struct {
		name string
		lib  bool
	}{
		{"入口模块", false},
		{"依赖模块", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir, err := os.MkdirTemp(filepath.Join(root, ".work"), "default-param-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(dir) })
			if c.lib {
				writeSource(t, filepath.Join(dir, "lib.ts"),
					"export function add(a: number, b: number = 10): number { return a + b; }\n")
				writeSource(t, filepath.Join(dir, "main.ts"),
					"import { add } from \"./lib.ts\";\nconsole.log(add(5));\n")
			} else {
				writeSource(t, filepath.Join(dir, "main.ts"),
					"function add(a: number, b: number = 10): number { return a + b; }\nconsole.log(add(5));\n")
			}

			var stdout, stderr bytes.Buffer
			code := run([]string{"check", "--root", dir, "main.ts"}, &stdout, &stderr)
			if code != diagnostic.ExitCapability {
				t.Fatalf("退出状态为 %d，期望 %d；stderr=%s", code, diagnostic.ExitCapability, stderr.String())
			}
			if !strings.Contains(stderr.String(), "cap.unsupported-parameter") {
				t.Errorf("应报 cap.unsupported-parameter：%s", stderr.String())
			}
			for _, unwanted := range []string{"facts.arg-count", "lower.bad-arity", "host.failed"} {
				if strings.Contains(stderr.String(), unwanted) {
					t.Errorf("不应出现 %s：%s", unwanted, stderr.String())
				}
			}
		})
	}
}

func TestBadDiagnosticsMode(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run([]string{"--diagnostics=xml", "version"}, &out, &errOut)
	if code != diagnostic.ExitUsage {
		t.Fatalf("退出状态为 %d，期望 %d", code, diagnostic.ExitUsage)
	}
	if !strings.Contains(errOut.String(), "只接受 human 或 json") {
		t.Errorf("应说明可接受的模式：%q", errOut.String())
	}
}
