// Command release 组装一个可搬移的 Tynel 发行目录。
//
// 它只从已准备的开发树读取文件，不改动 .work 里的工具。默认输出到
// out/release/tynel-<commit>-<goos>-<goarch>，并在发布前用目录里的 CLI 编译
// examples/add.ts、运行并检查输出 42。
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/Mahoo12138/tynel/internal/release"
	"github.com/Mahoo12138/tynel/internal/toolchain"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "打包失败：%v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `用法：go run ./tools/release [选项]

把已准备的 CLI、前端宿主、Go 后端、SDK、支持文件、示例和许可证组装成一个
可搬移目录，并在目录内编译、运行示例。

选项：
  --project DIR   项目根，默认按本文件位置推断
  --out DIR       输出目录，默认 out/release/tynel-<commit>-<goos>-<goarch>
  --verify DIR    只校验已有发行目录的 release.json，不打包
  --quiet         只输出最终目录
`)
}

func run() error {
	fs := flag.NewFlagSet("release", flag.ContinueOnError)
	project := fs.String("project", "", "项目根")
	out := fs.String("out", "", "输出目录")
	verify := fs.String("verify", "", "只校验已有发行目录")
	quiet := fs.Bool("quiet", false, "只输出最终目录")
	fs.SetOutput(os.Stderr)
	fs.Usage = usage
	if err := fs.Parse(os.Args[1:]); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("不接受位置参数：%s", strings.Join(fs.Args(), " "))
	}
	if *verify != "" {
		dir, err := filepath.Abs(*verify)
		if err != nil {
			return err
		}
		m, err := release.Load(dir)
		if err != nil {
			return err
		}
		if err := release.Verify(dir); err != nil {
			return err
		}
		if !*quiet {
			fmt.Printf("清单有效：%s（%d 个文件，%s）\n", dir, len(m.Files), m.Target)
		}
		return nil
	}

	root, err := resolveRoot(*project)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		return fmt.Errorf("%s 不是 Tynel 项目根：%v", root, err)
	}
	lock, err := toolchain.LoadLock(filepath.Join(root, filepath.FromSlash(toolchain.LockPath)))
	if err != nil {
		return err
	}
	target := runtime.GOOS + "/" + runtime.GOARCH
	if lock.Targets.Host != target {
		return fmt.Errorf("发行目标为 %s，本机是 %s；不在错误平台上组装", lock.Targets.Host, target)
	}
	report, err := toolchain.LoadReport(filepath.Join(root, filepath.FromSlash(toolchain.ReportPath)))
	if err != nil {
		return err
	}
	sdk, ok := report.SDK()
	if !ok {
		return errors.New("准备报告里没有 SDK；先运行 ./tools/toolchain/prep")
	}
	sdkDir, err := toolchain.ResolvePath(root, sdk.Dir)
	if err != nil {
		return err
	}
	commit, err := gitCommit(root)
	if err != nil {
		return err
	}
	version, binCommit, err := binaryVersion(root)
	if err != nil {
		return err
	}
	if binCommit != commit {
		return fmt.Errorf("bin/tynel 构建自 %s，当前提交是 %s；先运行 ./tools/build", binCommit, commit)
	}
	name := fmt.Sprintf("tynel-%s-%s-%s", commit, runtime.GOOS, runtime.GOARCH)
	if *out == "" {
		*out = filepath.Join(root, "out", "release", name)
	}
	outDir, err := filepath.Abs(*out)
	if err != nil {
		return err
	}
	if _, err := os.Stat(outDir); err == nil {
		return fmt.Errorf("输出目录已存在：%s；先移走或删除它", outDir)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(outDir), 0o755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Dir(outDir), ".tynel-package-")
	if err != nil {
		return err
	}
	if err := os.Chmod(stage, 0o755); err != nil {
		return err
	}
	published := false
	defer func() {
		if !published {
			os.RemoveAll(stage)
		}
	}()

	if err := copyOne(filepath.Join(root, "bin", "tynel"), filepath.Join(stage, "bin", "tynel")); err != nil {
		return err
	}
	files := []struct{ src, dst string }{
		{filepath.Join(root, "go.mod"), "go.mod"},
		{filepath.Join(root, filepath.FromSlash(toolchain.LockPath)), filepath.FromSlash(toolchain.LockPath)},
		{filepath.Join(root, "bridge", "go", "toolexec"), "bridge/go/toolexec"},
		{filepath.Join(root, "bridge", "go", "gc", "tynel_bridge.go"), "bridge/go/gc/tynel_bridge.go"},
		{filepath.Join(root, "bridge", "go", "patches", "0001-inject.patch"), "bridge/go/patches/0001-inject.patch"},
		{filepath.Join(root, filepath.FromSlash(compilerPath)), filepath.FromSlash(compilerPath)},
		{filepath.Join(root, filepath.FromSlash(stampPath)), filepath.FromSlash(stampPath)},
		{filepath.Join(root, filepath.FromSlash(hostBinary)), filepath.FromSlash(hostBinary)},
		{filepath.Join(root, filepath.FromSlash(hostStamp)), filepath.FromSlash(hostStamp)},
		{filepath.Join(root, "examples", "add.ts"), "examples/add.ts"},
		{filepath.Join(sdkDir, "LICENSE"), "LICENSES/go.LICENSE"},
		{filepath.Join(root, ".work", "frontend", "typescript-go", "LICENSE"), "LICENSES/typescript-go.LICENSE"},
	}
	for _, f := range files {
		if err := copyOne(f.src, filepath.Join(stage, filepath.FromSlash(f.dst))); err != nil {
			return err
		}
	}
	if err := copyTree(filepath.Join(root, "bridge", "go", "support"), filepath.Join(stage, "bridge", "go", "support")); err != nil {
		return err
	}
	if err := copyTree(sdkDir, filepath.Join(stage, ".work", "toolchain", sdk.Name)); err != nil {
		return err
	}

	tools := make(map[string]string, len(toolchain.ToolNames))
	for _, name := range toolchain.ToolNames {
		path, ok := report.Tool(name)
		if !ok {
			return fmt.Errorf("准备报告里没有工具 %s；重新运行 ./tools/toolchain/prep", name)
		}
		resolved, err := toolchain.ResolvePath(root, path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(sdkDir, resolved)
		if err != nil || strings.HasPrefix(rel, "..") {
			return fmt.Errorf("工具 %s（%s）不在 SDK 目录 %s 内", name, resolved, sdkDir)
		}
		tools[name] = filepath.ToSlash(filepath.Join(".work", "toolchain", sdk.Name, rel))
	}
	rep := toolchain.Report{
		SchemaVersion: 1,
		LockedVersion: lock.Go.Version,
		Target:        lock.Targets.Host,
		Installs: []toolchain.Install{{
			Kind:  "sdk",
			Name:  sdk.Name,
			Dir:   filepath.ToSlash(filepath.Join(".work", "toolchain", sdk.Name)),
			Tools: tools,
		}},
	}
	if err := writeJSON(filepath.Join(stage, filepath.FromSlash(toolchain.ReportPath)), rep); err != nil {
		return err
	}
	if err := writeText(filepath.Join(stage, "README.md"), releaseReadme(name, version, commit, lock)); err != nil {
		return err
	}
	if err := writeText(filepath.Join(stage, "LICENSES", "NOTICE.md"), noticeText()); err != nil {
		return err
	}

	manifest := release.Manifest{
		Name:        name,
		Version:     version,
		Commit:      commit,
		Target:      lock.Targets.Host,
		GeneratedAt: time.Now().Format(time.RFC3339),
		Go: release.Identity{
			Name: "go", Version: lock.Go.Version, Commit: lock.Go.SourceCommit, License: lock.Go.License,
		},
		Frontend: release.Identity{
			Name: lock.Frontend.Name, Version: lock.Frontend.Version, Commit: lock.Frontend.Commit, License: lock.Frontend.License,
		},
	}
	collected, err := release.Collect(stage, manifest)
	if err != nil {
		return err
	}
	if err := release.Write(stage, collected); err != nil {
		return err
	}
	if err := release.Verify(stage); err != nil {
		return err
	}
	if err := smoke(stage); err != nil {
		return err
	}
	// smoke 会在发行根写 NIR 快照；它不属于发行内容，清理后目录仍可直接搬移。
	os.RemoveAll(filepath.Join(stage, ".work", "build"))
	if err := os.Rename(stage, outDir); err != nil {
		return err
	}
	published = true
	if err := release.Verify(outDir); err != nil {
		return err
	}
	if *quiet {
		fmt.Println(outDir)
	} else {
		fmt.Printf("已生成 %s（%d 个文件，%s）\n", outDir, len(collected.Files), collected.Target)
		fmt.Printf("  版本       tynel %s（commit %s）\n", version, commit)
		fmt.Printf("  清单       %s\n", filepath.Join(outDir, release.ManifestName))
	}
	return nil
}

const (
	compilerPath = ".work/bridge/compile"
	stampPath    = ".work/bridge/stamp.json"
	hostBinary   = ".work/frontend/bin/consumer"
	hostStamp    = ".work/frontend/bin/consumer.stamp.json"
)

func resolveRoot(project string) (string, error) {
	if project != "" {
		return filepath.Abs(project)
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("无法定位打包入口；用 --project 指定项目根")
	}
	abs, err := filepath.Abs(file)
	if err != nil {
		return "", err
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(abs))), nil
}

func gitCommit(root string) (string, error) {
	cmd := exec.Command("git", "-C", root, "rev-parse", "--short", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("无法读取当前提交：%v", err)
	}
	return strings.TrimSpace(string(out)), nil
}

var versionLine = regexp.MustCompile(`^tynel (\S+) \(commit ([^)]+)\)$`)

func binaryVersion(root string) (string, string, error) {
	cmd := exec.Command(filepath.Join(root, "bin", "tynel"), "version")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return "", "", fmt.Errorf("无法运行 bin/tynel version：%v", err)
	}
	line := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
	m := versionLine.FindStringSubmatch(line)
	if m == nil {
		return "", "", fmt.Errorf("bin/tynel version 的第一行无法解析：%q", line)
	}
	return m[1], m[2], nil
}

func copyOne(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return fmt.Errorf("缺少打包输入 %s：%v", src, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("打包输入 %s 不是普通文件", src)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(dst, info.Mode().Perm())
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("打包输入包含符号链接：%s", path)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("打包输入不是普通文件：%s", path)
		}
		return copyOne(path, target)
	})
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return writeText(path, string(append(data, '\n')))
}

func writeText(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func releaseReadme(name, version, commit string, lock *toolchain.Lock) string {
	return fmt.Sprintf(`# Tynel 编译器发行目录

这是 %s，版本 tynel %s（commit %s），目标 %s。

## 使用

    # 在发行目录内编译示例，然后运行生成的程序。
    ./bin/tynel build examples/add.ts -o /tmp/tynel-add
    /tmp/tynel-add
    # 42

编译器会从这里带的 Go SDK、修改版编译器和前端宿主工作，不需要 Node、系统
Go、npm 或仓库源码。生成的原生程序也不需要本目录。

## 内容

- bin/tynel：命令行入口和 Tynel 编译器驱动。
- .work/frontend/bin/consumer：预构建的 TypeScript 前端宿主。
- .work/bridge/compile：修改版 Go 编译器；.work/bridge/stamp.json 是它的戳记。
- .work/toolchain/%s：Go SDK，包含 go、compile、link、asm 和标准库。
- bridge/go/support、bridge/go/toolexec、bridge/go/gc、bridge/go/patches：后端支持文件和工具包装。
- examples/add.ts：最小示例。
- release.json：每个文件的 sha256、大小和权限。
- LICENSES/：Go（%s）和 TypeScript 前端（%s）的许可证文本与来源说明。

## 校验

release.json 是发行内容的清单。仓库中的 tools/package --verify <目录> 会逐个复核文件；
也可以直接比较清单里的 sha256。清单不登记自身，也不是数字签名。

## 限制

只支持 %s。发行包没有项目级许可证声明；Tynel 仓库本身目前没有 LICENSE 文件，
这里不替它补一份。前端源码和适配包没有随包分发，发行包使用预构建宿主，不能
在这个目录里重建前端。
`, name, version, commit, lock.Targets.Host, lock.Go.Version, lock.Go.License, lock.Frontend.License, lock.Targets.Host)
}

func noticeText() string {
	return `# 第三方许可证与来源

本目录分发以下上游组件：

- Go 工具链与运行时：BSD-3-Clause，许可证见 ` + "`go.LICENSE`" + `。版本与源码提交见
  ` + "`../release.json`" + ` 的 ` + "`go`" + ` 字段。
- TypeScript 前端 typescript-go：Apache-2.0，许可证见 ` + "`typescript-go.LICENSE`" + `。
  版本与提交见 ` + "`../release.json`" + ` 的 ` + "`frontend`" + ` 字段。

Tynel 仓库当前没有项目级 LICENSE 文件；本目录不额外授予 Tynel 代码的许可证。
`
}

func smoke(root string) error {
	tmp, err := os.MkdirTemp("", "tynel-release-smoke-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	app := filepath.Join(tmp, "app")
	cmd := exec.Command(filepath.Join(root, "bin", "tynel"), "build", "examples/add.ts", "-o", app)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("发行目录内构建示例失败：%v\n%s", err, out)
	}
	run := exec.Command(app)
	run.Dir = tmp
	got, err := run.Output()
	if err != nil {
		return fmt.Errorf("运行发行示例失败：%v", err)
	}
	if string(got) != "42\n" {
		return fmt.Errorf("发行示例输出 %q，期望 42", got)
	}
	return nil
}
