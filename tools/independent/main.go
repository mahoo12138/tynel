// Command independent 执行 06.06 的 darwin/arm64 生成物独立运行验收。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Mahoo12138/tynel/internal/independent"
)

func main() {
	os.Exit(run())
}

func run() int {
	fs := flag.NewFlagSet("independent", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	project := fs.String("project", "", "项目根；默认从当前目录向上查找 go.mod")
	artifacts := fs.String("artifacts", "", "06.05 的 outputs；默认 <project>/out/clean/06-05/outputs")
	out := fs.String("out", "", "新的证据目录；默认 <project>/out/independent/06-06")
	sandbox := fs.String("sandbox", "/usr/bin/sandbox-exec", "macOS sandbox-exec 路径")
	node := fs.String("node", "", "需要拒绝访问的 Node；默认从宿主 PATH 查找")
	timeout := fs.Duration("timeout", 10*time.Second, "每个探针或载荷的超时时间")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "用法：./tools/independent-run [选项]")
		fmt.Fprintln(os.Stderr, "只携带 add、choices、collatz、quote 四个 exe，在限制性 macOS 沙箱中运行。")
		fs.PrintDefaults()
	}
	if err := fs.Parse(os.Args[1:]); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "不接受位置参数：%v\n", fs.Args())
		return 2
	}
	root := *project
	if root == "" {
		var err error
		root, err = resolveRoot()
		if err != nil {
			fmt.Fprintf(os.Stderr, "无法定位项目根：%v；请用 --project 指定\n", err)
			return 2
		}
	}
	if *artifacts == "" {
		*artifacts = filepath.Join(root, "out", "clean", "06-05", "outputs")
	}
	if *out == "" {
		*out = filepath.Join(root, "out", "independent", "06-06")
	}

	report, err := independent.Run(context.Background(), independent.Config{
		ProjectRoot: root, ArtifactsDir: *artifacts, OutDir: *out,
		SandboxPath: *sandbox, NodePath: *node, Timeout: *timeout,
	})
	if report != nil {
		for _, artifact := range report.Artifacts {
			if artifact.Run != nil {
				fmt.Printf("%s：退出 %d，passed=%t\n", artifact.Name, artifact.Run.ExitCode, artifact.Run.Passed)
			} else {
				fmt.Printf("%s：已核对并传输 sha256 %s，未运行\n", artifact.Name, artifact.SHA256)
			}
		}
		fmt.Printf("报告：%s\n", filepath.Join(*out, "report.json"))
	}
	if err == nil {
		fmt.Println("独立运行验收通过：4 个程序")
		return 0
	}
	var blocked *independent.BlockedError
	if errors.As(err, &blocked) {
		fmt.Fprintf(os.Stderr, "独立运行验收受阻：%v\n", err)
		return 3
	}
	fmt.Fprintf(os.Stderr, "独立运行验收失败：%v\n", err)
	return 1
}

func resolveRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		data, readErr := os.ReadFile(filepath.Join(dir, "go.mod"))
		if readErr == nil && strings.Contains(string(data), "module github.com/Mahoo12138/tynel\n") {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("当前目录及父目录都不是 Tynel 项目")
		}
		dir = parent
	}
}
