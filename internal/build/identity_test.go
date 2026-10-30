package build

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
	"github.com/Mahoo12138/tynel/compiler/graph"
	"github.com/Mahoo12138/tynel/internal/toolchain"
)

// identityFiles 写出入口和依赖，返回构建身份需要的相对路径与内容哈希。
func identityFiles(t *testing.T, root string, contents map[string]string) ([]File, *Source) {
	t.Helper()
	files := make([]File, 0, len(contents))
	for rel, content := range contents {
		writeFile(t, filepath.Join(root, filepath.FromSlash(rel)), content)
		files = append(files, File{Path: rel, SHA256: sha256Hex([]byte(content))})
	}
	entry := "src/entry.ts"
	if _, ok := contents[entry]; !ok {
		t.Fatalf("夹具缺少入口 %s", entry)
	}
	source := &Source{Path: entry, SHA256: sha256Hex([]byte(contents[entry])), Bytes: len(contents[entry])}
	return files, source
}

// replaceFile 返回替换某个路径内容哈希后的新列表，保持其余身份不变。
func replaceFile(files []File, path, sha string) []File {
	out := append([]File{}, files...)
	for i := range out {
		if out[i].Path == path {
			out[i].SHA256 = sha
			return out
		}
	}
	return append(out, File{Path: path, SHA256: sha})
}

func identityGraphHash(files []File) string {
	infos := make([]graph.FileInfo, 0, len(files))
	for _, file := range files {
		infos = append(infos, graph.FileInfo{Path: file.Path, SHA256: file.SHA256})
	}
	return graph.HashFiles(infos)
}

func identityOptions(root, output string, files []File, source *Source, data []byte) Options {
	return Options{
		Root:        root,
		ProjectRoot: root,
		InputData:   data,
		Output:      output,
		Source:      source,
		Entry:       source.Path,
		GraphHash:   identityGraphHash(files),
		Files:       files,
	}
}

func TestIdentityIsStableAndContentAddressed(t *testing.T) {
	root, runner := newProject(t)
	runner.writeOutput = true
	files, source := identityFiles(t, root, map[string]string{
		"src/entry.ts": "export function main(): void { helper(); }\n",
		"src/value.ts": "export function helper(): void {}\n",
		"src/types.ts": "export type T = number;\n",
	})
	output := filepath.Join(root, "out", "app")
	data := exampleNIR(t)

	first, err := Run(identityOptions(root, output, files, source, data), runner)
	if err != nil {
		t.Fatalf("首次构建失败：%v", err)
	}
	if len(first.Identity.Files) != 3 {
		t.Fatalf("身份里应有 3 个文件，实际 %d", len(first.Identity.Files))
	}
	if first.Tools.BuildTag != "tynel_"+first.Identity.ArtifactSHA256 {
		t.Errorf("build tag %q 没有使用产物输入身份 %s", first.Tools.BuildTag, first.Identity.ArtifactSHA256)
	}
	if first.Identity.ArtifactSHA256 == "" || first.Identity.ArtifactSHA256 == first.Identity.Hash {
		t.Errorf("完整身份与产物输入身份应分别记录：%+v", first.Identity)
	}

	second, err := Run(identityOptions(root, output, files, source, data), runner)
	if err != nil {
		t.Fatalf("相同输入再次构建失败：%v", err)
	}
	if second.Identity.Hash != first.Identity.Hash {
		t.Errorf("相同输入得到不同身份：%s != %s", second.Identity.Hash, first.Identity.Hash)
	}
	if second.Tools.BuildTag != first.Tools.BuildTag {
		t.Errorf("相同输入的 build tag 不同")
	}

	// 值依赖变化，NIR 内容故意保持不变：身份和缓存标签仍必须变化。
	valueContent := "export function helper(): void { console.log(1); }\n"
	writeFile(t, filepath.Join(root, "src", "value.ts"), valueContent)
	changedValue := replaceFile(files, "src/value.ts", sha256Hex([]byte(valueContent)))
	third, err := Run(identityOptions(root, output, changedValue, source, data), runner)
	if err != nil {
		t.Fatalf("依赖变化后构建失败：%v", err)
	}
	if third.Identity.Hash == first.Identity.Hash {
		t.Error("值依赖变化没有改变构建身份")
	}
	// 这里的 NIR 故意没变；类型/源码文件变化只改完整身份，产物输入身份保持
	// 不变。真正改变生成代码的是下面的 NIR 变化。
	if third.Identity.ArtifactSHA256 != first.Identity.ArtifactSHA256 {
		t.Error("NIR 未变时产物输入身份不应变化")
	}
	if third.Tools.BuildTag != first.Tools.BuildTag {
		t.Error("NIR 未变时不应换 build tag")
	}

	// 类型依赖变化，同样不应复用旧身份。
	typeContent := "export type T = boolean;\n"
	writeFile(t, filepath.Join(root, "src", "types.ts"), typeContent)
	changedType := replaceFile(changedValue, "src/types.ts", sha256Hex([]byte(typeContent)))
	fourth, err := Run(identityOptions(root, output, changedType, source, data), runner)
	if err != nil {
		t.Fatalf("类型依赖变化后构建失败：%v", err)
	}
	if fourth.Identity.Hash == third.Identity.Hash {
		t.Error("类型依赖变化没有改变构建身份")
	}

	// 配置变化也必须进入身份。
	report, err := toolchain.LoadReport(filepath.Join(root, filepath.FromSlash(toolchain.ReportPath)))
	if err != nil {
		t.Fatal(err)
	}
	report.Target = "darwin/amd64"
	writeJSON(t, filepath.Join(root, filepath.FromSlash(toolchain.ReportPath)), report)
	configChanged, err := Run(identityOptions(root, output, changedType, source, data), runner)
	if err != nil {
		t.Fatalf("配置变化后构建失败：%v", err)
	}
	if configChanged.Identity.Hash == fourth.Identity.Hash {
		t.Error("目标配置变化没有改变构建身份")
	}
	if configChanged.Identity.ArtifactSHA256 == fourth.Identity.ArtifactSHA256 {
		t.Error("目标配置变化没有改变产物输入身份")
	}

	// NIR 字节变化：即使源码列表不变，产物输入身份和 build tag 也必须换，
	// 否则 Go 缓存可能把旧对象当成这次输入。
	differentNIR := append(append([]byte{}, data...), '\n')
	fifth, err := Run(identityOptions(root, output, changedType, source, differentNIR), runner)
	if err != nil {
		t.Fatalf("NIR 变化后构建失败：%v", err)
	}
	if fifth.Identity.ArtifactSHA256 == configChanged.Identity.ArtifactSHA256 {
		t.Error("NIR 变化没有改变产物输入身份")
	}
	if fifth.Tools.BuildTag == configChanged.Tools.BuildTag {
		t.Error("NIR 变化没有改变 build tag")
	}
}

func TestIdentityCoversToolchainContents(t *testing.T) {
	root, runner := newProject(t)
	runner.writeOutput = true
	files, source := identityFiles(t, root, map[string]string{
		"src/entry.ts": "export function main(): void {}\n",
	})
	output := filepath.Join(root, "out", "app")
	options := identityOptions(root, output, files, source, exampleNIR(t))

	first, err := Run(options, runner)
	if err != nil {
		t.Fatalf("首次构建失败：%v", err)
	}

	// 工具包装变化：内容直接进入身份。
	writeFile(t, filepath.Join(root, filepath.FromSlash(ToolexecPath)), "#!/bin/sh\nexec \"$@\" # changed\n")
	second, err := Run(options, runner)
	if err != nil {
		t.Fatalf("工具包装变化后构建失败：%v", err)
	}
	if second.Identity.Hash == first.Identity.Hash {
		t.Error("工具包装变化没有改变构建身份")
	}

	// 固定支持目录变化：即使编译器没变，也要换身份。
	writeFile(t, filepath.Join(root, filepath.FromSlash(SupportDir), "support.go"), "package support\n// changed\n")
	third, err := Run(options, runner)
	if err != nil {
		t.Fatalf("支持目录变化后构建失败：%v", err)
	}
	if third.Identity.Hash == second.Identity.Hash {
		t.Error("固定支持变化没有改变构建身份")
	}

	// 修改版编译器变化；戳记同步更新，确保失败原因只是身份不同。
	writeFile(t, filepath.Join(root, filepath.FromSlash(CompilerPath)), "compiler-binary-v2")
	stampPath := filepath.Join(root, filepath.FromSlash(StampPath))
	data, err := os.ReadFile(stampPath)
	if err != nil {
		t.Fatal(err)
	}
	var st stamp
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatal(err)
	}
	st.CompilerSHA256 = sha256Hex([]byte("compiler-binary-v2"))
	writeJSON(t, stampPath, st)
	fourth, err := Run(options, runner)
	if err != nil {
		t.Fatalf("编译器变化后构建失败：%v", err)
	}
	if fourth.Identity.Hash == third.Identity.Hash {
		t.Error("编译器内容变化没有改变构建身份")
	}
}

func TestRunRejectsStaleDependency(t *testing.T) {
	root, runner := newProject(t)
	runner.writeOutput = true
	files, source := identityFiles(t, root, map[string]string{
		"src/entry.ts": "export function main(): void {}\n",
	})
	output := filepath.Join(root, "out", "app")
	options := identityOptions(root, output, files, source, exampleNIR(t))

	if _, err := Run(options, runner); err != nil {
		t.Fatalf("首次构建失败：%v", err)
	}
	artifactBefore, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	recordBefore, err := os.ReadFile(RecordPath(output))
	if err != nil {
		t.Fatal(err)
	}
	callsBefore := runner.calls

	// 图建立后替换入口内容：不能带着旧身份继续编译。
	writeFile(t, filepath.Join(root, "src", "entry.ts"), "export function main(): void { console.log(1); }\n")
	_, err = Run(options, runner)
	if err == nil {
		t.Fatal("依赖变化后应拒绝构建")
	}
	if got := diagnostic.ExitCode(err); got != diagnostic.ExitEnvironment {
		t.Errorf("退出状态为 %d，期望 %d（%v）", got, diagnostic.ExitEnvironment, err)
	}
	if !strings.Contains(err.Error(), "build.file-changed") {
		t.Errorf("错误码不是 build.file-changed：%v", err)
	}
	if runner.calls != callsBefore {
		t.Errorf("依赖已变化时不应启动后端，实际多调用 %d 次", runner.calls-callsBefore)
	}
	if got, _ := os.ReadFile(output); string(got) != string(artifactBefore) {
		t.Error("拒绝构建时改动了旧产物")
	}
	if got, _ := os.ReadFile(RecordPath(output)); string(got) != string(recordBefore) {
		t.Error("拒绝构建时改动了旧记录")
	}
}

func TestRunRejectsGraphIdentityMismatch(t *testing.T) {
	root, runner := newProject(t)
	runner.writeOutput = true
	files, source := identityFiles(t, root, map[string]string{
		"src/entry.ts": "export function main(): void {}\n",
	})
	output := filepath.Join(root, "out", "app")
	options := identityOptions(root, output, files, source, exampleNIR(t))
	options.GraphHash = strings.Repeat("0", 64)

	_, err := Run(options, runner)
	if err == nil {
		t.Fatal("错误的图身份应被拒绝")
	}
	if !strings.Contains(err.Error(), "build.identity-mismatch") {
		t.Fatalf("错误码不是 build.identity-mismatch：%v", err)
	}
	if runner.calls != 0 {
		t.Errorf("图身份不一致时不应启动后端")
	}
	if _, statErr := os.Stat(output); !os.IsNotExist(statErr) {
		t.Error("图身份不一致时不应产出文件")
	}
}

func TestRunRejectsIdentityWithoutItsCompanion(t *testing.T) {
	root, runner := newProject(t)
	files, source := identityFiles(t, root, map[string]string{
		"src/entry.ts": "export function main(): void {}\n",
	})
	base := identityOptions(root, filepath.Join(root, "out", "app"), files, source, exampleNIR(t))

	cases := []Options{}
	noHash := base
	noHash.GraphHash = ""
	cases = append(cases, noHash)
	noFiles := base
	noFiles.Files = nil
	cases = append(cases, noFiles)
	for _, options := range cases {
		_, err := Run(options, runner)
		if err == nil || !strings.Contains(err.Error(), "build.identity-mismatch") {
			t.Errorf("不完整的身份应被拒绝：%v", err)
		}
	}
	if runner.calls != 0 {
		t.Errorf("身份不完整时不应启动后端，实际 %d 次", runner.calls)
	}
}

// mutateRunner 在后端运行期间改写 NIR 快照，模拟构建输入被别的进程替换。
type mutateRunner struct {
	fakeRunner
}

func (m *mutateRunner) RunEnv(env []string, name string, args ...string) (toolchain.Output, error) {
	for _, item := range env {
		if strings.HasPrefix(item, "TYNEL_NIR=") {
			path := strings.TrimPrefix(item, "TYNEL_NIR=")
			if err := os.WriteFile(path, []byte(`{"schemaVersion":1,"entry":"changed"}`), 0o644); err != nil {
				return toolchain.Output{}, err
			}
		}
	}
	return m.fakeRunner.RunEnv(env, name, args...)
}

func TestRunRejectsInputReplacedDuringBuild(t *testing.T) {
	root, normal := newProject(t)
	normal.writeOutput = true
	files, source := identityFiles(t, root, map[string]string{
		"src/entry.ts": "export function main(): void {}\n",
	})
	output := filepath.Join(root, "out", "app")
	options := identityOptions(root, output, files, source, exampleNIR(t))

	if _, err := Run(options, normal); err != nil {
		t.Fatalf("首次构建失败：%v", err)
	}
	artifactBefore, _ := os.ReadFile(output)
	recordBefore, _ := os.ReadFile(RecordPath(output))

	mutator := &mutateRunner{}
	mutator.writeOutput = true
	_, err := Run(options, mutator)
	if err == nil {
		t.Fatal("输入在构建期间被替换时应失败")
	}
	if !strings.Contains(err.Error(), "build.input-changed") {
		t.Fatalf("错误码不是 build.input-changed：%v", err)
	}
	if got, _ := os.ReadFile(output); string(got) != string(artifactBefore) {
		t.Error("输入变化时覆盖了旧产物")
	}
	if got, _ := os.ReadFile(RecordPath(output)); string(got) != string(recordBefore) {
		t.Error("输入变化时改动了旧记录")
	}
	entries, _ := os.ReadDir(filepath.Dir(output))
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".tynel-build-") {
			t.Errorf("留下了临时文件 %s", entry.Name())
		}
	}
}
