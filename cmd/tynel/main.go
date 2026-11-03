// Command tynel 是 Tynel 编译器的命令行入口。
//
// 它负责编排：找项目根、准备前端宿主、按需降低、调用后端、发布产物。它自己
// 不解析 TypeScript，也不生成机器码——那两件事分别在宿主进程和修改版 Go
// 编译器里。
package main

import (
	"context"
	"errors"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
	"github.com/Mahoo12138/tynel/compiler/pipeline"
	"github.com/Mahoo12138/tynel/internal/toolchain"
)

func main() {
	// Ctrl-C 取消正在运行的后端进程；发布阶段一旦开始就让它走完，避免把
	// 两个文件的事务停在中间。
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(runContext(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

// app 保存一次调用的输出目标和可替换依赖。测试通过替换 runner 和 root
// 来构造环境缺失、工具不存在等路径，不需要真的改动磁盘上的工具链。
type app struct {
	stdout          io.Writer
	stderr          io.Writer
	progress        io.Writer
	diag            io.Writer
	jsonDiagnostics bool
	runner          toolchain.Runner
	program         programRunner
	frontend        func(file, want string) (*pipeline.Result, error)
	root            string
	hasRoot         bool
	ctx             context.Context
}

func newApp(stdout, stderr io.Writer) *app {
	root, ok := findRepoRoot()
	return &app{stdout: stdout, stderr: stderr, diag: stderr, runner: toolchain.Exec{}, root: root, hasRoot: ok}
}

// progressWriter 接收宿主重建等进度；JSON 诊断模式把它丢弃，避免进度文本
// 混进同一路输出。真实进程的 stderr 仍由 a.stderr 保留给用户程序。
func (a *app) progressWriter() io.Writer {
	if a.progress != nil {
		return a.progress
	}
	return a.stderr
}

// diagnosticWriter 是诊断的唯一出口。默认与 stderr 相同；测试和 JSON 模式
// 可以单独指向别的写入端。
func (a *app) diagnosticWriter() io.Writer {
	if a.diag != nil {
		return a.diag
	}
	return a.stderr
}

func (a *app) context() context.Context {
	if a.ctx != nil {
		return a.ctx
	}
	return context.Background()
}

// writeError 是命令失败的统一出口：人类模式写多行诊断，JSON 模式写一个
// 信封。写入错误本身不再递归报告。
func (a *app) writeError(err error) error {
	if a.jsonDiagnostics {
		return diagnostic.WriteJSON(a.diagnosticWriter(), err)
	}
	return diagnostic.Write(a.diagnosticWriter(), err)
}

// takeDiagnostics 抽出全局 `--diagnostics=human|json` 选项，返回剩余参数。
// 选项可以出现在命令前后；模式不合法时按用法错误处理，而不是悄悄回退。
func takeDiagnostics(args []string) (string, []string, error) {
	mode := "human"
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--diagnostics":
			if i+1 >= len(args) {
				return "", nil, diagnostic.New(diagnostic.Usage, "cli.missing-argument", "--diagnostics 需要 human 或 json")
			}
			mode = args[i+1]
			i++
		case strings.HasPrefix(arg, "--diagnostics="):
			mode = strings.TrimPrefix(arg, "--diagnostics=")
		default:
			rest = append(rest, arg)
		}
	}
	switch mode {
	case "", "human":
		return "human", rest, nil
	case "json":
		return "json", rest, nil
	default:
		return "", nil, diagnostic.New(diagnostic.Usage, "cli.bad-diagnostics-mode", "--diagnostics 只接受 human 或 json，收到 %q", mode)
	}
}

// findRepoRoot 先按当前工作目录向上找锁文件；找不到时再从可执行文件所在
// 目录向上找。后一条让 bin/tynel 可以从任意工作目录启动，但项目根仍由
// --root 或入口路径决定，不会把工作目录误当成项目根。
func findRepoRoot() (string, bool) {
	if root, ok := toolchain.FindRoot("."); ok {
		return root, true
	}
	exe, err := os.Executable()
	if err != nil {
		return "", false
	}
	return toolchain.FindRoot(filepath.Dir(exe))
}

// run 执行一次命令行调用并返回进程退出状态。测试不安装信号处理器，
// 取消路径由 runContext 单独覆盖。
func run(args []string, stdout, stderr io.Writer) int {
	return runContext(context.Background(), args, stdout, stderr)
}

func runContext(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	mode, rest, err := takeDiagnostics(args)
	if err != nil {
		_ = diagnostic.Write(stderr, err)
		return diagnostic.ExitCode(err)
	}
	if len(rest) == 0 {
		if mode == "json" {
			_ = diagnostic.WriteJSON(stderr, diagnostic.New(diagnostic.Usage, "cli.missing-command", "没有命令；用 tynel help 查看可用命令"))
			return diagnostic.ExitUsage
		}
		writeUsage(stderr)
		return diagnostic.ExitUsage
	}
	a := newApp(stdout, stderr)
	a.ctx = ctx
	a.jsonDiagnostics = mode == "json"
	if a.jsonDiagnostics {
		a.progress = io.Discard
	}
	if err := a.dispatch(rest[0], rest[1:]); err != nil {
		var silent silentExit
		if errors.As(err, &silent) {
			return silent.code
		}
		_ = a.writeError(err)
		return diagnostic.ExitCode(err)
	}
	return diagnostic.ExitOK
}

func (a *app) dispatch(command string, args []string) error {
	switch command {
	case "version":
		return a.version(args)
	case "help", "-h", "--help":
		return a.help(args)
	case "toolchain":
		return a.toolchainCommand(args)
	case "check":
		return a.check(args)
	case "build":
		return a.build(args)
	case "run":
		return a.run(args)
	case "dump":
		return a.dump(args)
	case "build-ir":
		return a.buildIR(args)
	default:
		return diagnostic.New(diagnostic.Usage, "cli.unknown-command", "未知命令 %q；用 tynel help 查看可用命令", command)
	}
}
