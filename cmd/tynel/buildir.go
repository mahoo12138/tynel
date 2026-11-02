package main

import (
	"fmt"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
	"github.com/Mahoo12138/tynel/internal/build"
	"github.com/Mahoo12138/tynel/internal/toolchain"
)

// buildIR 实现开发用的 build-ir：把一份 NIR 编成原生程序。
// 它是调试接口，不读取用户项目，也不接受 --root；输入和输出按当前工作
// 目录解释，工具链根由编译器自动定位。
func (a *app) buildIR(args []string) error {
	parsed, err := parseSourceArgs("build-ir", "NIR 文件", args, optionSet{output: true, target: true}, true)
	if err != nil {
		return err
	}
	if err := a.checkTarget(parsed.target); err != nil {
		return err
	}
	if !a.hasRoot {
		return diagnostic.New(diagnostic.Environment, "project.not-found", "不在项目目录内：向上找不到 %s", toolchain.LockPath)
	}

	record, err := build.Run(build.Options{Root: a.root, Input: parsed.file, Output: parsed.output}, a.runner)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "已生成 %s（%d 字节）\n", record.Output.Path, record.Output.Bytes)
	fmt.Fprintf(a.stdout, "  输入     %s\n", shortSHA(record.Input.SHA256))
	fmt.Fprintf(a.stdout, "  身份     %s\n", shortSHA(record.Identity.Hash))
	fmt.Fprintf(a.stdout, "  编译器   %s\n", shortSHA(record.Tools.CompilerSHA256))
	fmt.Fprintf(a.stdout, "  构建记录 %s\n", build.RecordPath(record.Output.Path))
	return nil
}
