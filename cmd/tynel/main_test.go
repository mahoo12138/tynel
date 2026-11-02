package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
	"github.com/Mahoo12138/tynel/internal/toolchain"
)

// fakeRunner 用固定表代替真实进程，让错误路径不依赖磁盘上的工具链。
type fakeRunner struct {
	paths map[string]string
	err   error
}

func (f fakeRunner) LookPath(name string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	if path, ok := f.paths[name]; ok {
		return path, nil
	}
	return "", errors.New("不在工具表中")
}

func (f fakeRunner) Run(string, ...string) (toolchain.Output, error) {
	return toolchain.Output{}, nil
}

func (f fakeRunner) RunEnv([]string, string, ...string) (toolchain.Output, error) {
	return toolchain.Output{}, nil
}

// newFakeProject 造一个只有锁文件和准备报告的项目根。
func newFakeProject(t *testing.T) (string, map[string]string) {
	t.Helper()
	root := t.TempDir()
	sdkDir := filepath.Join(root, ".work", "toolchain", "go1.27.3-sdk-darwin-arm64")
	tools := map[string]string{
		"go":      filepath.Join(sdkDir, "bin", "go"),
		"compile": filepath.Join(sdkDir, "pkg", "tool", "darwin_arm64", "compile"),
		"link":    filepath.Join(sdkDir, "pkg", "tool", "darwin_arm64", "link"),
		"asm":     filepath.Join(sdkDir, "pkg", "tool", "darwin_arm64", "asm"),
	}
	lock := toolchain.Lock{SchemaVersion: 1}
	lock.Go.Version = "go1.27.3"
	lock.Go.SourceCommit = "2aae1000ea1d4b28923b96db28acf4e3b99d6aff"
	lock.Go.License = "BSD-3-Clause"
	lock.Targets.Host = "darwin/arm64"
	lock.Targets.Primary = "darwin/arm64"
	lock.Targets.Deferred = []string{"windows/amd64"}
	lock.Frontend.Name = "typescript-go"
	lock.Frontend.Version = "typescript/v7.0.2"
	lock.Frontend.Commit = "2bd066d87f5bafd315be9f40889d0a60b9e58e0b"
	lock.Frontend.License = "Apache-2.0"

	report := toolchain.Report{SchemaVersion: 1, LockedVersion: "go1.27.3", Target: "darwin/arm64"}
	report.Installs = []toolchain.Install{
		{Kind: "sdk", Name: "go1.27.3-sdk-darwin-arm64", Dir: sdkDir, Tools: tools},
		{Kind: "src", Name: "go1.27.3-src", Dir: filepath.Join(root, ".work", "toolchain", "go1.27.3-src")},
	}
	writeJSON(t, filepath.Join(root, filepath.FromSlash(toolchain.LockPath)), lock)
	writeJSON(t, filepath.Join(root, filepath.FromSlash(toolchain.ReportPath)), report)
	return root, tools
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("创建目录失败：%v", err)
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("序列化失败：%v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("写入 %s 失败：%v", path, err)
	}
}

func newTestApp(t *testing.T, runner toolchain.Runner) (*app, *bytes.Buffer) {
	t.Helper()
	root, _ := newFakeProject(t)
	out := &bytes.Buffer{}
	return &app{stdout: out, stderr: &bytes.Buffer{}, runner: runner, root: root, hasRoot: true}, out
}

func TestRunVersionShowsLockedIdentity(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"version"}, &out, &errOut); code != diagnostic.ExitOK {
		t.Fatalf("退出状态为 %d，期望 0；stderr：%s", code, errOut.String())
	}
	for _, want := range []string{"tynel dev", "darwin/arm64", "go1.27.3", "BSD-3-Clause", "typescript-go", "typescript/v7.0.2", "Apache-2.0", "tools/toolchain/go.lock.json"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("版本输出缺少 %q：\n%s", want, out.String())
		}
	}
}

func TestVersionReportsPreparedToolchain(t *testing.T) {
	a, out := newTestApp(t, fakeRunner{})
	if err := a.version(nil); err != nil {
		t.Fatalf("version 失败：%v", err)
	}
	if !strings.Contains(out.String(), "准备状态    已准备（go1.27.3-sdk-darwin-arm64）") {
		t.Errorf("应报告已准备状态：\n%s", out.String())
	}
}

func TestVersionWithoutProjectStillWorks(t *testing.T) {
	out := &bytes.Buffer{}
	a := &app{stdout: out, stderr: &bytes.Buffer{}, runner: fakeRunner{}}
	if err := a.version(nil); err != nil {
		t.Fatalf("不在项目目录内时 version 不应失败：%v", err)
	}
	if !strings.Contains(out.String(), "未知") {
		t.Errorf("应标注目标平台未知：\n%s", out.String())
	}
}

func TestHelpListsCommandsAndExitCodes(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"help"}, &out, &errOut); code != diagnostic.ExitOK {
		t.Fatalf("退出状态为 %d，期望 0", code)
	}
	for _, want := range []string{"用户命令", "开发与调试命令", "version", "toolchain which", "--target", "退出状态", "环境缺失"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("帮助输出缺少 %q", want)
		}
	}
}

func TestHelpTopic(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"help", "toolchain"}, &out, &errOut); code != diagnostic.ExitOK {
		t.Fatalf("退出状态为 %d，期望 0", code)
	}
	if !strings.Contains(out.String(), "toolchain which") {
		t.Errorf("主题帮助应说明子命令：\n%s", out.String())
	}
}

func TestNoArgumentsIsUsageError(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run(nil, &out, &errOut); code != diagnostic.ExitUsage {
		t.Fatalf("退出状态为 %d，期望 %d", code, diagnostic.ExitUsage)
	}
	if !strings.Contains(errOut.String(), "用法") {
		t.Errorf("应把用法写到 stderr：%q", errOut.String())
	}
}

func TestUnknownCommandIsUsageError(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"frobnicate"}, &out, &errOut); code != diagnostic.ExitUsage {
		t.Fatalf("退出状态为 %d，期望 %d", code, diagnostic.ExitUsage)
	}
	if !strings.Contains(errOut.String(), "未知命令") {
		t.Errorf("应说明是未知命令：%q", errOut.String())
	}
}

func TestUsageErrors(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"version 多余参数", []string{"version", "extra"}},
		{"help 未知主题", []string{"help", "nope"}},
		{"help 多余参数", []string{"help", "version", "extra"}},
		{"toolchain 未知子命令", []string{"toolchain", "show"}},
		{"toolchain which 缺参数", []string{"toolchain", "which"}},
		{"toolchain which 未知工具", []string{"toolchain", "which", "nope"}},
		{"build-ir 缺参数", []string{"build-ir"}},
		{"build-ir 缺输出", []string{"build-ir", "x.nir.json"}},
		{"build-ir 未知选项", []string{"build-ir", "-x"}},
		{"check 缺文件", []string{"check"}},
		{"check 多余参数", []string{"check", "a.ts", "b.ts"}},
		{"check 未知选项", []string{"check", "-x", "a.ts"}},
		{"check 不接受 -o", []string{"check", "a.ts", "-o", "out"}},
		{"check 不接受 --target", []string{"check", "a.ts", "--target", "darwin/arm64"}},
		{"check 重复 --root", []string{"check", "a.ts", "--root", "a", "--root", "b"}},
		{"check 空 --root", []string{"check", "a.ts", "--root="}},
		{"build 不接受 --json", []string{"build", "a.ts", "-o", "out", "--json"}},
		{"build 重复 -o", []string{"build", "a.ts", "-o", "a", "-o", "b"}},
		{"build 重复 --target", []string{"build", "a.ts", "-o", "out", "--target", "darwin/arm64", "--target", "darwin/arm64"}},
		{"build --root 后缺值", []string{"build", "a.ts", "-o", "out", "--root", "--json"}},
		{"run 不接受 -o", []string{"run", "a.ts", "-o", "out"}},
		{"dump 不接受 --json", []string{"dump", "facts", "a.ts", "--json"}},
		{"build-ir 不接受 --root", []string{"build-ir", "x.nir.json", "-o", "out", "--root", "."}},
		{"build 缺文件", []string{"build"}},
		{"build 缺输出", []string{"build", "a.ts"}},
		{"build 缺 -o 的值", []string{"build", "a.ts", "-o"}},
		{"run 缺文件", []string{"run"}},
		{"run 多余参数", []string{"run", "a.ts", "b.ts"}},
		{"dump 缺参数", []string{"dump"}},
		{"dump 未知内容", []string{"dump", "ast", "a.ts"}},
		{"dump 多余参数", []string{"dump", "facts", "a.ts", "b.ts"}},
	}
	for _, c := range cases {
		var out, errOut bytes.Buffer
		if code := run(c.args, &out, &errOut); code != diagnostic.ExitUsage {
			t.Errorf("%s：退出状态为 %d，期望 %d", c.name, code, diagnostic.ExitUsage)
		}
	}
}

func TestToolchainWhichResolves(t *testing.T) {
	root, tools := newFakeProject(t)
	out := &bytes.Buffer{}
	a := &app{stdout: out, stderr: &bytes.Buffer{}, runner: fakeRunner{paths: map[string]string{tools["go"]: tools["go"]}}, root: root, hasRoot: true}
	if err := a.whichTool("go"); err != nil {
		t.Fatalf("whichTool 失败：%v", err)
	}
	if got := strings.TrimSpace(out.String()); got != tools["go"] {
		t.Errorf("输出为 %q，期望 %q", got, tools["go"])
	}
}

func TestToolchainWhichMissingToolIsEnvironmentError(t *testing.T) {
	a, _ := newTestApp(t, fakeRunner{err: errors.New("permission denied")})
	err := a.whichTool("go")
	if err == nil {
		t.Fatal("工具不可用时应报错")
	}
	if got := diagnostic.ExitCode(err); got != diagnostic.ExitEnvironment {
		t.Errorf("退出状态为 %d，期望 %d", got, diagnostic.ExitEnvironment)
	}
	if !strings.Contains(err.Error(), "go") {
		t.Errorf("错误应包含工具名：%v", err)
	}
}

func TestToolchainShowReportsMissingTools(t *testing.T) {
	a, _ := newTestApp(t, fakeRunner{err: errors.New("gone")})
	err := a.showToolchain()
	if got := diagnostic.ExitCode(err); got != diagnostic.ExitEnvironment {
		t.Fatalf("退出状态为 %d，期望 %d（%v）", got, diagnostic.ExitEnvironment, err)
	}
	if !strings.Contains(err.Error(), "go") || !strings.Contains(err.Error(), "link") {
		t.Errorf("应列出不可用的工具：%v", err)
	}
}

func TestToolchainNotPrepared(t *testing.T) {
	root := t.TempDir()
	out := &bytes.Buffer{}
	a := &app{stdout: out, stderr: &bytes.Buffer{}, runner: fakeRunner{}, root: root, hasRoot: true}
	err := a.showToolchain()
	if got := diagnostic.ExitCode(err); got != diagnostic.ExitEnvironment {
		t.Fatalf("未准备工具链时退出状态为 %d，期望 %d", got, diagnostic.ExitEnvironment)
	}
}

func TestToolchainOutsideProject(t *testing.T) {
	a := &app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, runner: fakeRunner{}}
	err := a.toolchainCommand(nil)
	if got := diagnostic.ExitCode(err); got != diagnostic.ExitEnvironment {
		t.Fatalf("不在项目目录内时退出状态为 %d，期望 %d", got, diagnostic.ExitEnvironment)
	}
}

// TestHelpTopicsMatchCommandContract 确认每个可调用命令都有帮助，且帮助里
// 写出的选项与解析器实际接受的选项一致。
func TestHelpTopicsMatchCommandContract(t *testing.T) {
	cases := []struct {
		topic string
		want  []string
	}{
		{"version", []string{"不接受参数", "TypeScript", "准备状态"}},
		{"check", []string{"--root", "--json", "不接受 -o", "不接受"}},
		{"build", []string{"-o", "--root", "--target", "build.json"}},
		{"run", []string{"--root", "--target", "不接受 -o", ".work/run/app"}},
		{"dump", []string{"facts", "nir", "graph", "modules", "init", "--root", "不接受 -o"}},
		{"build-ir", []string{"-o", "--target", "不接受 --root"}},
		{"toolchain", []string{"which", "go", "compile", "link", "asm"}},
		{"help", []string{"命令"}},
	}
	for _, c := range cases {
		t.Run(c.topic, func(t *testing.T) {
			var out, errOut bytes.Buffer
			if code := run([]string{"help", c.topic}, &out, &errOut); code != diagnostic.ExitOK {
				t.Fatalf("help %s 退出状态为 %d，期望 0；stderr：%s", c.topic, code, errOut.String())
			}
			for _, want := range c.want {
				if !strings.Contains(out.String(), want) {
					t.Errorf("help %s 缺少 %q：\n%s", c.topic, want, out.String())
				}
			}
		})
	}
	var out, errOut bytes.Buffer
	if code := run([]string{"help"}, &out, &errOut); code != diagnostic.ExitOK {
		t.Fatalf("总体帮助退出状态为 %d，期望 0", code)
	}
	for _, want := range []string{"用户命令", "开发与调试命令", "--target", "源文件相对 --root"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("总体帮助缺少 %q：\n%s", want, out.String())
		}
	}
}

func TestSourceOptionForms(t *testing.T) {
	parsed, err := parseSourceArgs("build", "TypeScript 文件", []string{
		"add.ts", "--root=examples/quote", "-o=out/app", "--target=darwin/arm64",
	}, optionSet{root: true, output: true, target: true}, true)
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if parsed.file != "add.ts" || parsed.root != "examples/quote" || parsed.output != "out/app" || parsed.target != "darwin/arm64" {
		t.Errorf("解析结果不对：%+v", parsed)
	}
}

func TestCheckTargetOnlyAcceptsPrimary(t *testing.T) {
	a, _ := newTestApp(t, fakeRunner{})
	if err := a.checkTarget("darwin/arm64"); err != nil {
		t.Fatalf("主目标应被接受：%v", err)
	}
	err := a.checkTarget("windows/amd64")
	if err == nil {
		t.Fatal("未实现的目标应被拒绝")
	}
	if got := diagnostic.ExitCode(err); got != diagnostic.ExitCapability {
		t.Errorf("退出状态为 %d，期望 %d", got, diagnostic.ExitCapability)
	}
	if !strings.Contains(err.Error(), "darwin/arm64") || !strings.Contains(err.Error(), "windows/amd64") {
		t.Errorf("错误应同时说明支持目标和请求目标：%v", err)
	}
}

func TestUnsupportedTargetStopsBeforeBuild(t *testing.T) {
	a, _ := newTestApp(t, fakeRunner{})
	runner := &fileRunner{}
	a.runner = runner
	calls := []struct {
		name string
		fn   func() error
	}{
		{"build", func() error {
			return a.build([]string{"a.ts", "-o", filepath.Join(t.TempDir(), "app"), "--target", "windows/amd64"})
		}},
		{"run", func() error { return a.run([]string{"a.ts", "--target", "windows/amd64"}) }},
		{"build-ir", func() error {
			return a.buildIR([]string{"x.nir.json", "-o", filepath.Join(t.TempDir(), "app"), "--target", "windows/amd64"})
		}},
	}
	for _, c := range calls {
		t.Run(c.name, func(t *testing.T) {
			err := c.fn()
			if got := diagnostic.ExitCode(err); got != diagnostic.ExitCapability {
				t.Fatalf("退出状态为 %d，期望 %d（%v）", got, diagnostic.ExitCapability, err)
			}
		})
	}
	if runner.calls != 0 {
		t.Fatalf("不支持的目标不应启动后端，实际调用 %d 次", runner.calls)
	}
}
