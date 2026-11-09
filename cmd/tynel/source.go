package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Mahoo12138/tynel/compiler/capability"
	"github.com/Mahoo12138/tynel/compiler/diagnostic"
	"github.com/Mahoo12138/tynel/compiler/frontend"
	"github.com/Mahoo12138/tynel/compiler/graph"
	"github.com/Mahoo12138/tynel/compiler/lower"
	"github.com/Mahoo12138/tynel/compiler/merge"
	"github.com/Mahoo12138/tynel/compiler/modules"
	"github.com/Mahoo12138/tynel/compiler/nir"
	"github.com/Mahoo12138/tynel/compiler/pipeline"
	"github.com/Mahoo12138/tynel/compiler/project"
	"github.com/Mahoo12138/tynel/compiler/source"
	"github.com/Mahoo12138/tynel/internal/build"
	"github.com/Mahoo12138/tynel/internal/host"
	"github.com/Mahoo12138/tynel/internal/toolchain"
)

// silentExit 表示「细节已经报告过，只需要设置退出状态」。
//
// check 与 run 会先把诊断或程序输出写完，再决定退出状态；这时再让上层打印
// 一条汇总诊断，会把同一件事说两遍。
type silentExit struct{ code int }

func (e silentExit) Error() string { return fmt.Sprintf("退出状态 %d", e.code) }

// programRunner 执行构建产物。抽成接口是为了让测试不真的启动子进程。
type programRunner interface {
	Run(path string, stdout, stderr io.Writer) (int, error)
}

type execProgram struct{}

func (execProgram) Run(path string, stdout, stderr io.Writer) (int, error) {
	cmd := exec.Command(path)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Stdin = os.Stdin
	err := cmd.Run()
	if err == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	return 0, err
}

func (a *app) programRunner() programRunner {
	if a.program != nil {
		return a.program
	}
	return execProgram{}
}

// sourceArgs 是 check、build、run、dump 和 build-ir 共用的参数形状。
// 每个命令只打开自己接受的选项，解析与帮助使用同一份语义。
type sourceArgs struct {
	file   string
	output string
	root   string
	target string
	json   bool
}

type optionSet struct {
	root   bool
	output bool
	json   bool
	target bool
}

func parseSourceArgs(command, inputLabel string, args []string, allow optionSet, requireOutput bool) (sourceArgs, error) {
	var parsed sourceArgs
	var seenRoot, seenOutput, seenJSON, seenTarget bool
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--root" || strings.HasPrefix(arg, "--root="):
			if !allow.root {
				return parsed, optionNotAllowed(command, "--root")
			}
			if seenRoot {
				return parsed, duplicateOption(command, "--root")
			}
			seenRoot = true
			value, used, err := optionValue(command, "--root", arg, args, i)
			if err != nil {
				return parsed, err
			}
			parsed.root = value
			i += used
		case arg == "-o" || strings.HasPrefix(arg, "-o="):
			if !allow.output {
				return parsed, optionNotAllowed(command, "-o")
			}
			if seenOutput {
				return parsed, duplicateOption(command, "-o")
			}
			seenOutput = true
			value, used, err := optionValue(command, "-o", arg, args, i)
			if err != nil {
				return parsed, err
			}
			parsed.output = value
			i += used
		case arg == "--json":
			if !allow.json {
				return parsed, optionNotAllowed(command, "--json")
			}
			if seenJSON {
				return parsed, duplicateOption(command, "--json")
			}
			seenJSON = true
			parsed.json = true
		case arg == "--target" || strings.HasPrefix(arg, "--target="):
			if !allow.target {
				return parsed, optionNotAllowed(command, "--target")
			}
			if seenTarget {
				return parsed, duplicateOption(command, "--target")
			}
			seenTarget = true
			value, used, err := optionValue(command, "--target", arg, args, i)
			if err != nil {
				return parsed, err
			}
			parsed.target = value
			i += used
		case strings.HasPrefix(arg, "-"):
			return parsed, diagnostic.New(diagnostic.Usage, "cli.bad-flag", "%s 不认识选项 %q；用 tynel help %s 查看参数", command, arg, command)
		case parsed.file == "":
			parsed.file = arg
		default:
			return parsed, diagnostic.New(diagnostic.Usage, "cli.unexpected-argument", "%s 只接受一份%s：%q", command, inputLabel, arg)
		}
	}
	if parsed.file == "" {
		return parsed, diagnostic.New(diagnostic.Usage, "cli.missing-argument", "%s 需要一份%s", command, inputLabel)
	}
	if requireOutput && parsed.output == "" {
		return parsed, diagnostic.New(diagnostic.Usage, "cli.missing-argument", "%s 需要 -o 指定输出路径", command)
	}
	return parsed, nil
}

// optionValue 读取 `--name value` 或 `--name=value`。空值和看起来像下一个
// 选项的值都按用法错误处理，避免把 --json 之类静默当成目录名。
func optionValue(command, name, arg string, args []string, i int) (string, int, error) {
	if eq := strings.IndexByte(arg, '='); eq >= 0 {
		value := arg[eq+1:]
		if value == "" {
			return "", 0, diagnostic.New(diagnostic.Usage, "cli.missing-argument", "%s 的 %s 需要一个非空值", command, name)
		}
		return value, 0, nil
	}
	if i+1 >= len(args) {
		return "", 0, diagnostic.New(diagnostic.Usage, "cli.missing-argument", "%s 的 %s 需要一个值", command, name)
	}
	value := args[i+1]
	if value == "" || strings.HasPrefix(value, "-") {
		return "", 0, diagnostic.New(diagnostic.Usage, "cli.missing-argument", "%s 的 %s 需要一个值，收到 %q", command, name, value)
	}
	return value, 1, nil
}

func duplicateOption(command, name string) error {
	return diagnostic.New(diagnostic.Usage, "cli.duplicate-option", "%s 的 %s 只能出现一次", command, name)
}

// optionNotAllowed 对「这个命令不接受这个选项」给出具体原因，而不是只说
// 选项名不认识；帮助和错误因此能指向正确的命令。
func optionNotAllowed(command, name string) error {
	reason := "这个命令不使用该选项"
	switch name {
	case "-o":
		reason = "只有 build 和 build-ir 接受输出路径"
	case "--root":
		reason = "只有 check、build、run 和 dump 接受用户项目根"
	case "--json":
		reason = "机器可读结果只由 check --json 和 dump 提供"
	case "--target":
		reason = "目标只影响 build、run 和 build-ir"
	}
	return diagnostic.New(diagnostic.Usage, "cli.bad-flag", "%s 不接受 %s；%s", command, name, reason)
}

// resolveSource 把入口固定到项目根下，并返回给前端宿主的绝对路径。
// 没有 --root 时沿用仓库根，保持单文件示例的旧行为；显式 --root 时，入口
// 按该项目根解释，而不是按当前工作目录解释。
func (a *app) resolveSource(parsed sourceArgs) (string, error) {
	if a.frontend != nil {
		// 测试用：假前端不读磁盘，路径原样传入。
		return parsed.file, nil
	}
	root := a.root
	if parsed.root != "" {
		root = parsed.root
	}
	if root == "" {
		return "", diagnostic.New(diagnostic.Environment, "project.not-found", "找不到项目根；用 --root 指定目录")
	}
	p, err := project.Open(root)
	if err != nil {
		return "", err
	}
	file, err := p.ResolveEntry(parsed.file)
	if err != nil {
		return "", err
	}
	return file.Abs, nil
}

// hostScanner 把前端宿主的导入列表适配成依赖图扫描器。
type hostScanner struct {
	host   *host.Host
	runner toolchain.Runner
}

func (s hostScanner) Imports(file project.File) ([]graph.Import, error) {
	items, err := s.host.Imports(file.Abs, s.runner)
	if err != nil {
		return nil, err
	}
	out := make([]graph.Import, 0, len(items))
	for _, item := range items {
		out = append(out, graph.Import{
			Specifier: item.Specifier,
			Start:     item.Start,
			Length:    item.Length,
			TypeOnly:  item.TypeOnly,
			Resolved:  item.Resolved,
		})
	}
	return out, nil
}

// loadGraph 在检查或降低之前把入口和全部显式相对依赖固定成快照。
func (a *app) loadGraph(parsed sourceArgs) (*graph.Graph, error) {
	if a.frontend != nil {
		return nil, nil
	}
	root := a.root
	if parsed.root != "" {
		root = parsed.root
	}
	if root == "" {
		return nil, diagnostic.New(diagnostic.Environment, "project.not-found", "找不到项目根；用 --root 指定目录")
	}
	p, err := project.Open(root)
	if err != nil {
		return nil, err
	}
	entry, err := p.ResolveEntry(parsed.file)
	if err != nil {
		return nil, err
	}
	if !a.hasRoot {
		return nil, diagnostic.New(diagnostic.Environment, "project.not-found", "不在项目目录内：向上找不到 %s", toolchain.LockPath)
	}
	front, err := host.Ensure(a.root, a.runner, a.progressWriter())
	if err != nil {
		return nil, err
	}
	return graph.Load(p, entry, hostScanner{host: front, runner: a.runner})
}

// loadModules 为图里的每个文件取事实，再合成模块符号表。
func (a *app) loadModules(parsed sourceArgs) (*modules.Table, error) {
	g, err := a.loadGraph(parsed)
	if err != nil {
		return nil, err
	}
	if g == nil {
		return nil, diagnostic.New(diagnostic.Internal, "modules.test-frontend", "假前端不能建立模块符号表")
	}
	front, err := host.Ensure(a.root, a.runner, a.progressWriter())
	if err != nil {
		return nil, err
	}
	facts := make([]*frontend.Facts, 0, len(g.Files))
	for _, file := range g.Files {
		deps := make([]string, 0, len(g.Files)-1)
		for _, other := range g.Files {
			if other.Abs != file.Abs {
				deps = append(deps, other.Abs)
			}
		}
		item, err := front.Facts(file.Abs, deps, a.runner)
		if err != nil {
			return nil, err
		}
		facts = append(facts, item)
	}
	return modules.Build(facts[0], facts[1:])
}

// loadMerged 把依赖图里的所有模块合并降低成一个 NIR。facts 与快照一一对应，
// 初始化顺序来自 modules.Plan。
func (a *app) loadMerged(g *graph.Graph) (*nir.Module, error) {
	if g == nil {
		return nil, diagnostic.New(diagnostic.Internal, "merge.test-frontend", "假前端不能合并模块")
	}
	front, err := host.Ensure(a.root, a.runner, a.progressWriter())
	if err != nil {
		return nil, err
	}
	facts := make([]*frontend.Facts, 0, len(g.Files))
	units := make([]merge.Unit, 0, len(g.Files))
	for _, file := range g.Files {
		deps := make([]string, 0, len(g.Files)-1)
		for _, other := range g.Files {
			if other.Abs != file.Abs {
				deps = append(deps, other.Abs)
			}
		}
		item, err := front.Facts(file.Abs, deps, a.runner)
		if err != nil {
			return nil, err
		}
		snapshot, err := file.Snapshot(source.DefaultLimits())
		if err != nil {
			return nil, err
		}
		// 前端宿主只对入口跑能力检查。依赖模块里的写法（比如带默认值的参数）
		// 同样要过这道闸，否则合并降低会拿到少一个实参的调用，报成
		// lower.bad-arity 内部故障，而不是能力限制。
		findings, err := capability.Check(snapshot, item)
		if err != nil {
			return nil, err
		}
		if len(findings) > 0 {
			return nil, findings[0]
		}
		facts = append(facts, item)
		units = append(units, merge.Unit{Snapshot: snapshot, Facts: item})
	}
	table, err := modules.Build(facts[0], facts[1:])
	if err != nil {
		return nil, err
	}
	plan, err := modules.Plan(table)
	if err != nil {
		return nil, err
	}
	merged, err := merge.Build(units, plan.Order)
	if err != nil {
		return nil, err
	}
	return lower.LowerMerged(merged.Facts, merged.Snapshots, merged.Bodies, plan.Order)
}

// analyze 准备前端宿主并检查一个文件。want 决定结果里附带什么。
func (a *app) analyze(file, want string, deps []string) (*pipeline.Result, error) {
	if a.frontend != nil {
		// 测试用：直接给出前端结果，不启动宿主进程。
		return a.frontend(file, want)
	}
	if !a.hasRoot {
		return nil, diagnostic.New(diagnostic.Environment, "project.not-found", "不在项目目录内：向上找不到 %s", toolchain.LockPath)
	}
	front, err := host.Ensure(a.root, a.runner, a.progressWriter())
	if err != nil {
		return nil, err
	}
	return front.AnalyzeFiles(file, deps, want, a.runner)
}

// render 把诊断与能力结论写到诊断出口。stdout 只留给命令的结果；JSON
// 诊断模式写到同一个出口，但换成带阶段和位置的单个信封。
func (a *app) render(result *pipeline.Result) error {
	if a.jsonDiagnostics {
		items := make([]*diagnostic.Diagnostic, 0, len(result.Diagnostics)+len(result.Capability))
		for _, d := range result.Diagnostics {
			items = append(items, diagnostic.New(diagnostic.Source, fmt.Sprintf("TS%d", d.Code), "%s", d.Message).
				At(d.File, d.Line, d.Column).
				WithSeverity(d.Severity).
				WithStage(diagnostic.StageFrontend))
		}
		for _, f := range result.Capability {
			items = append(items, diagnostic.New(diagnostic.Capability, f.Code, "%s", f.Message).
				At(f.File, f.Line, f.Column).
				WithStage(diagnostic.StageCapability))
		}
		if len(items) == 0 {
			return nil
		}
		if err := diagnostic.WriteItems(a.diagnosticWriter(), items, result.ExitCode()); err != nil {
			return diagnostic.New(diagnostic.Internal, "cli.write-failed", "写出诊断失败").WithDetail(err.Error())
		}
		return nil
	}
	for _, d := range result.Diagnostics {
		fmt.Fprintf(a.diagnosticWriter(), "%s:%d:%d: %s TS%d: %s\n", d.File, d.Line, d.Column, d.Severity, d.Code, d.Message)
	}
	for _, f := range result.Capability {
		fmt.Fprintf(a.diagnosticWriter(), "%s:%d:%d: %s %s: %s\n", f.File, f.Line, f.Column, f.Category, f.Code, f.Message)
	}
	return nil
}

// moduleError 判断模块检查的错误是否比前端诊断更具体。只有模块或初始化
// 自己的源码诊断可以替换前端结论；工具、环境和内部故障一律留在原因链外，
// 避免一个语法错误被报告成「事实提取失败」。
func moduleError(err error) bool {
	var d *diagnostic.Diagnostic
	if !errors.As(err, &d) || d.Category != diagnostic.Source {
		return false
	}
	return strings.HasPrefix(d.Code, "module.") || strings.HasPrefix(d.Code, "init.")
}

// exitFor 把结果映射成「已经报告过」的退出状态。
func exitFor(result *pipeline.Result) error {
	if code := result.ExitCode(); code != 0 {
		return silentExit{code}
	}
	return nil
}

// check 只做源级检查与 NIR 验证，不生成机器码，也不要求后端工具可用。
func (a *app) check(args []string) error {
	parsed, err := parseSourceArgs("check", "TypeScript 文件", args, optionSet{root: true, json: true}, false)
	if err != nil {
		return err
	}
	file, err := a.resolveSource(parsed)
	if err != nil {
		return err
	}
	g, err := a.loadGraph(parsed)
	if err != nil {
		return err
	}
	result, err := a.analyze(file, "check", graphDeps(g))
	if err != nil {
		return err
	}
	// 合并降低之前必须先看前端结论。依赖里的类型错误如果被带进降低，就会
	// 变成另一个「内部故障」，真正的源码位置反而丢了。
	if g != nil && len(g.Files) > 1 {
		if result.ExitCode() != diagnostic.ExitOK {
			// 源码错误已经能让检查失败，不再降低；但模块链接问题（例如缺
			// 导出）比单文件诊断多带一个导出文件路径，仍值得优先报告。
			// 只接受模块自身的源码诊断：语法错误可能让事实提取失败，那种
			// 中间故障不能覆盖前端已经定位好的源码错误。
			if _, err := a.loadModules(parsed); err != nil && moduleError(err) {
				return err
			}
		} else {
			merged, err := a.loadMerged(g)
			if err != nil {
				return err
			}
			result.Summary.Functions = len(merged.Functions)
			result.Summary.Verified = true
		}
	}
	if parsed.json {
		if err := pipeline.Encode(a.stdout, result); err != nil {
			return diagnostic.New(diagnostic.Internal, "cli.write-failed", "写出检查结果失败").WithDetail(err.Error())
		}
		// --json 是命令自己的完整结果；全局诊断信封仍要独立报告失败，
		// 两路输出不能因为一个选项而互相吞掉。
		if a.jsonDiagnostics && result.ExitCode() != diagnostic.ExitOK {
			if err := a.render(result); err != nil {
				return err
			}
		}
		return exitFor(result)
	}
	if err := a.render(result); err != nil {
		return err
	}
	if err := exitFor(result); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "ok: %s（%d 个函数，NIR 已验证）\n", result.File, result.Summary.Functions)
	return nil
}

// build 从一份 TypeScript 源文件生成原生程序。构建成功也不运行它。
func (a *app) build(args []string) error {
	parsed, err := parseSourceArgs("build", "TypeScript 文件", args, optionSet{root: true, output: true, target: true}, true)
	if err != nil {
		return err
	}
	if err := a.checkTarget(parsed.target); err != nil {
		return err
	}
	file, err := a.resolveSource(parsed)
	if err != nil {
		return err
	}
	projectRoot, err := a.projectRoot(parsed)
	if err != nil {
		return err
	}
	g, err := a.loadGraph(parsed)
	if err != nil {
		return err
	}
	result, err := a.analyze(file, "check", graphDeps(g))
	if err != nil {
		return err
	}
	if err := a.render(result); err != nil {
		return err
	}
	if err := exitFor(result); err != nil {
		return err
	}
	var data []byte
	if a.frontend != nil {
		if result.NIR == nil {
			return diagnostic.New(diagnostic.Internal, "cli.no-nir", "前端没有返回 NIR")
		}
		data, err = nir.EncodeBytes(result.NIR)
	} else {
		merged, err := a.loadMerged(g)
		if err != nil {
			return err
		}
		data, err = nir.EncodeBytes(merged)
	}
	if err != nil {
		return diagnostic.New(diagnostic.Internal, "cli.encode-failed", "无法序列化降低结果").WithDetail(err.Error())
	}
	record, err := build.Run(build.Options{
		Root:        a.root,
		ProjectRoot: projectRoot,
		InputData:   data,
		Output:      parsed.output,
		Source:      sourceOf(projectRoot, result),
		Entry:       entryPath(g),
		GraphHash:   graphHash(g),
		Files:       graphFiles(g),
		Context:     a.context(),
	}, a.runner)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "已生成 %s（%d 字节）\n", record.Output.Path, record.Output.Bytes)
	if record.Source != nil {
		fmt.Fprintf(a.stdout, "  源码     %s（%s）\n", record.Source.Path, shortSHA(record.Source.SHA256))
	}
	fmt.Fprintf(a.stdout, "  输入     %s\n", shortSHA(record.Input.SHA256))
	fmt.Fprintf(a.stdout, "  身份     %s\n", shortSHA(record.Identity.Hash))
	fmt.Fprintf(a.stdout, "  编译器   %s\n", shortSHA(record.Tools.CompilerSHA256))
	fmt.Fprintf(a.stdout, "  构建记录 %s\n", build.RecordPath(record.Output.Path))
	return nil
}

// run 编译并立即执行本次产物。它不执行任何先前存在的文件：构建失败就直接
// 返回，目标位置即使留着上一次的产物也不会被运行。
func (a *app) run(args []string) error {
	parsed, err := parseSourceArgs("run", "TypeScript 文件", args, optionSet{root: true, target: true}, false)
	if err != nil {
		return err
	}
	if err := a.checkTarget(parsed.target); err != nil {
		return err
	}
	file, err := a.resolveSource(parsed)
	if err != nil {
		return err
	}
	projectRoot, err := a.projectRoot(parsed)
	if err != nil {
		return err
	}
	g, err := a.loadGraph(parsed)
	if err != nil {
		return err
	}
	result, err := a.analyze(file, "check", graphDeps(g))
	if err != nil {
		return err
	}
	if err := a.render(result); err != nil {
		return err
	}
	if err := exitFor(result); err != nil {
		return err
	}
	var data []byte
	if a.frontend != nil {
		if result.NIR == nil {
			return diagnostic.New(diagnostic.Internal, "cli.no-nir", "前端没有返回 NIR")
		}
		data, err = nir.EncodeBytes(result.NIR)
	} else {
		merged, err := a.loadMerged(g)
		if err != nil {
			return err
		}
		data, err = nir.EncodeBytes(merged)
	}
	if err != nil {
		return diagnostic.New(diagnostic.Internal, "cli.encode-failed", "无法序列化降低结果").WithDetail(err.Error())
	}
	output := filepath.Join(a.root, ".work", "run", "app")
	record, err := build.Run(build.Options{
		Root:        a.root,
		ProjectRoot: projectRoot,
		InputData:   data,
		Output:      output,
		Source:      sourceOf(projectRoot, result),
		Entry:       entryPath(g),
		GraphHash:   graphHash(g),
		Files:       graphFiles(g),
		Context:     a.context(),
	}, a.runner)
	if err != nil {
		return err
	}
	code, err := a.programRunner().Run(record.Output.Path, a.stdout, a.stderr)
	if err != nil {
		return diagnostic.New(diagnostic.Environment, "run.exec-failed", "无法执行 %s", record.Output.Path).WithDetail(err.Error())
	}
	if code != 0 {
		// 被运行程序的退出状态原样传出去，与 go run 一致；编译失败仍用
		// 1、3、4、5 这几类。
		return silentExit{code}
	}
	return nil
}

// dump 输出前端事实或 NIR，只写 JSON。
func (a *app) dump(args []string) error {
	if len(args) < 2 {
		return diagnostic.New(diagnostic.Usage, "cli.missing-argument", "dump 需要内容名和一份源文件：dump facts|nir|graph|modules|init <file.ts>")
	}
	what := args[0]
	switch what {
	case "facts", "nir", "graph", "modules", "init":
	default:
		return diagnostic.New(diagnostic.Usage, "cli.unknown-topic", "dump 只接受 facts、nir、graph、modules 或 init，收到 %q", what)
	}
	parsed, err := parseSourceArgs("dump", "TypeScript 文件", args[1:], optionSet{root: true}, false)
	if err != nil {
		return err
	}
	if what == "nir" {
		g, err := a.loadGraph(parsed)
		if err != nil {
			return err
		}
		if g != nil && len(g.Files) > 1 {
			merged, err := a.loadMerged(g)
			if err != nil {
				return err
			}
			data, err := json.MarshalIndent(merged, "", "  ")
			if err != nil {
				return diagnostic.New(diagnostic.Internal, "cli.encode-failed", "无法序列化合并 NIR").WithDetail(err.Error())
			}
			if _, err := a.stdout.Write(append(data, '\n')); err != nil {
				return diagnostic.New(diagnostic.Internal, "cli.write-failed", "写出合并 NIR 失败").WithDetail(err.Error())
			}
			return nil
		}
	}
	if what == "init" {
		table, err := a.loadModules(parsed)
		if err != nil {
			return err
		}
		plan, err := modules.Plan(table)
		if err != nil {
			return err
		}
		data, err := json.MarshalIndent(plan, "", "  ")
		if err != nil {
			return diagnostic.New(diagnostic.Internal, "cli.encode-failed", "无法序列化初始化计划").WithDetail(err.Error())
		}
		if _, err := a.stdout.Write(append(data, '\n')); err != nil {
			return diagnostic.New(diagnostic.Internal, "cli.write-failed", "写出初始化计划失败").WithDetail(err.Error())
		}
		return nil
	}
	if what == "modules" {
		table, err := a.loadModules(parsed)
		if err != nil {
			return err
		}
		data, err := json.MarshalIndent(table.Report(), "", "  ")
		if err != nil {
			return diagnostic.New(diagnostic.Internal, "cli.encode-failed", "无法序列化模块符号表").WithDetail(err.Error())
		}
		if _, err := a.stdout.Write(append(data, '\n')); err != nil {
			return diagnostic.New(diagnostic.Internal, "cli.write-failed", "写出模块符号表失败").WithDetail(err.Error())
		}
		return nil
	}
	if what == "graph" {
		g, err := a.loadGraph(parsed)
		if err != nil {
			return err
		}
		data, err := json.MarshalIndent(g.Report(), "", "  ")
		if err != nil {
			return diagnostic.New(diagnostic.Internal, "cli.encode-failed", "无法序列化依赖图").WithDetail(err.Error())
		}
		if _, err := a.stdout.Write(append(data, '\n')); err != nil {
			return diagnostic.New(diagnostic.Internal, "cli.write-failed", "写出依赖图失败").WithDetail(err.Error())
		}
		return nil
	}
	file, err := a.resolveSource(parsed)
	if err != nil {
		return err
	}
	g, err := a.loadGraph(parsed)
	if err != nil {
		return err
	}
	result, err := a.analyze(file, what, graphDeps(g))
	if err != nil {
		return err
	}
	if err := exitFor(result); err != nil {
		if renderErr := a.render(result); renderErr != nil {
			return renderErr
		}
		return err
	}
	var value any
	if what == "facts" {
		value = result.Facts
	} else {
		value = result.NIR
	}
	if value == nil {
		return diagnostic.New(diagnostic.Internal, "cli.no-dump", "前端没有返回 %s", what)
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return diagnostic.New(diagnostic.Internal, "cli.encode-failed", "无法序列化 %s", what).WithDetail(err.Error())
	}
	if _, err := a.stdout.Write(append(data, '\n')); err != nil {
		return diagnostic.New(diagnostic.Internal, "cli.write-failed", "写出 %s 失败", what).WithDetail(err.Error())
	}
	return nil
}

// graphDeps 返回除入口外的依赖绝对路径，供前端宿主一起载入。
func graphDeps(g *graph.Graph) []string {
	if g == nil {
		return nil
	}
	var out []string
	for _, file := range g.Files {
		if file.Abs != g.Entry.Abs {
			out = append(out, file.Abs)
		}
	}
	return out
}

func graphHash(g *graph.Graph) string {
	if g == nil {
		return ""
	}
	return g.Hash
}

// graphFiles 把依赖图里的固定快照转成构建身份。入口和类型/值依赖都在里面；
// 构建会在启动后端前重新核对磁盘内容。路径保持相对项目根（--root），与图
// 哈希使用同一套身份。
func graphFiles(g *graph.Graph) []build.File {
	if g == nil {
		return nil
	}
	files := make([]build.File, 0, len(g.Files))
	for _, file := range g.Files {
		files = append(files, build.File{Path: file.Path, SHA256: file.SHA256})
	}
	return files
}

// entryPath 返回依赖图里的入口身份；从 NIR 回放时没有入口。
func entryPath(g *graph.Graph) string {
	if g == nil {
		return ""
	}
	return g.Entry.Path
}

// projectRoot 返回源码根。显式 --root 时按那个目录的规范化路径复核文件；
// 省略时沿用仓库根。构建身份里的路径都相对它。
func (a *app) projectRoot(parsed sourceArgs) (string, error) {
	root := a.root
	if parsed.root != "" {
		root = parsed.root
	}
	if root == "" {
		return "", diagnostic.New(diagnostic.Environment, "project.not-found", "找不到项目根；用 --root 指定目录")
	}
	p, err := project.Open(root)
	if err != nil {
		return "", err
	}
	return p.Path(), nil
}

// checkTarget 只接受锁文件的主目标。显式请求其他平台时直接失败，不尝试
// 交叉编译，也不退回宿主默认后端或解释器；省略 --target 时使用锁定的目标。
func (a *app) checkTarget(target string) error {
	if target == "" {
		return nil
	}
	if !a.hasRoot {
		return diagnostic.New(diagnostic.Environment, "project.not-found", "不在项目目录内：向上找不到 %s", toolchain.LockPath)
	}
	lock, err := toolchain.LoadLock(filepath.Join(a.root, filepath.FromSlash(toolchain.LockPath)))
	if err != nil {
		return err
	}
	supported := lock.Targets.Primary
	if supported == "" {
		supported = lock.Targets.Host
	}
	if target != supported {
		return diagnostic.New(diagnostic.Capability, "target.unsupported", "首版只支持 %s；请求的目标是 %q，不交叉编译，也不会改用其他后端或解释器", supported, target)
	}
	return nil
}

// sourceOf 把结果里的源码身份转成构建记录的一项。路径尽量写成项目内相对
// 路径：绝对路径换个工作目录就失效了。
func sourceOf(root string, result *pipeline.Result) *build.Source {
	path := result.File
	if rel, err := filepath.Rel(root, path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		path = rel
	}
	return &build.Source{Path: path, SHA256: result.SourceSHA256, Bytes: result.SourceBytes}
}
