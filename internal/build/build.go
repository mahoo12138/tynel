// Package build 编排一次「NIR 到原生程序」的构建。
//
// 它固定一次输入快照，把工具调用交给可替换的 Runner，构建产物先写到目标
// 目录的临时文件，成功后才改名发布；失败时不覆盖已有的成功产物。构建本身
// 不运行用户程序。
package build

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
	"github.com/Mahoo12138/tynel/compiler/nir"
	"github.com/Mahoo12138/tynel/internal/release"
	"github.com/Mahoo12138/tynel/internal/toolchain"
)

// 仓库内的固定位置。
const (
	SupportDir   = "bridge/go/support"
	ToolexecPath = "bridge/go/toolexec"
	CompilerPath = ".work/bridge/compile"
	StampPath    = ".work/bridge/stamp.json"
	SnapshotDir  = ".work/build/snapshots"
)

// Options 是一次构建的输入。
//
// Input 与 InputData 二选一：前者读文件，后者直接用内存里的 NIR 字节。从
// TypeScript 构建时降低结果在内存里，没必要先落一份用户可见的文件；快照仍会
// 写到 .work 下，编译器读的始终是快照。
type Options struct {
	Root        string          // 构建根：工具链、支持目录和快照都在它下面
	ProjectRoot string          // 源码根；Files 与 Source.Path 相对它，默认等于 Root
	Input       string          // NIR 文件
	InputData   []byte          // 内存中的 NIR；非空时忽略 Input
	Output      string          // 产物路径
	Source      *Source         // 可选：源码身份，写进构建记录
	Entry       string          // 可选：入口在项目内的相对路径，写进构建身份
	GraphHash   string          // 可选：依赖图内容哈希；给 Files 时必须同时给出
	Files       []File          // 可选：入口与全部类型/值依赖的身份；从 NIR 构建时没有
	Context     context.Context // 可选：取消后停止后端，未发布的临时文件会被清理
}

// Source 是产生这份 NIR 的源码。从 NIR 文件构建时没有这一项。
type Source struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int    `json:"bytes"`
}

// Record 是构建记录，成功后写到 <产物>.build.json。
type Record struct {
	SchemaVersion  int      `json:"schemaVersion"`
	StartedAt      string   `json:"startedAt"`
	DurationMillis int64    `json:"durationMillis"`
	Source         *Source  `json:"source,omitempty"`
	GraphHash      string   `json:"graphHash,omitempty"`
	Identity       Identity `json:"identity"`
	Input          struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
		Bytes  int    `json:"bytes"`
	} `json:"input"`
	Toolchain struct {
		Version string `json:"version"`
		Target  string `json:"target"`
	} `json:"toolchain"`
	Tools struct {
		Go             string `json:"go"`
		Compiler       string `json:"compiler"`
		CompilerSHA256 string `json:"compilerSHA256"`
		Toolexec       string `json:"toolexec"`
		BuildTag       string `json:"buildTag"`
		Snapshot       string `json:"snapshot"`
	} `json:"tools"`
	Output struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
		Bytes  int64  `json:"bytes"`
	} `json:"output"`
}

// Run 执行一次构建。失败时返回诊断，且不改动目标位置已有的文件。
func Run(opts Options, runner toolchain.Runner) (*Record, error) {
	started := time.Now()
	if opts.Root == "" || opts.Output == "" || (len(opts.InputData) == 0 && opts.Input == "") {
		return nil, diagnostic.New(diagnostic.Usage, "build.missing-argument", "构建需要项目根、NIR 输入和输出路径")
	}
	if err := checkContext(opts.Context); err != nil {
		return nil, err
	}
	// 发行目录带清单时先复核内容。清单只登记运行所需文件，构建产生的
	// 快照和输出不属于发行内容。
	if err := release.VerifyIfSealed(opts.Root); err != nil {
		return nil, err
	}

	// 1. 输入快照：一次读入，后续都用这份字节，构建期间改源文件不影响本次结果。
	data := opts.InputData
	inputPath := opts.Input
	if len(data) == 0 {
		read, err := os.ReadFile(opts.Input)
		if err != nil {
			return nil, diagnostic.New(diagnostic.Environment, "build.input-unreadable", "读取 NIR %s 失败", opts.Input).WithDetail(err.Error())
		}
		data = read
	}
	inputSHA := sha256Hex(data)
	module, err := nir.DecodeBytes(data, nir.DefaultLimits())
	if err != nil {
		return nil, err
	}
	if err := nir.Verify(module); err != nil {
		return nil, err
	}

	// 2. 工具：隔离 SDK 的 go、修改版 compiler、工具包装。
	report, err := toolchain.LoadReport(filepath.Join(opts.Root, filepath.FromSlash(toolchain.ReportPath)))
	if err != nil {
		return nil, err
	}
	sdk, ok := report.SDK()
	if !ok {
		return nil, diagnostic.New(diagnostic.Environment, "build.no-sdk", "准备报告里没有 SDK 记录；重新运行 ./tools/toolchain/prep")
	}
	sdkDir, err := toolchain.ResolvePath(opts.Root, sdk.Dir)
	if err != nil {
		return nil, err
	}
	goTool := filepath.Join(sdkDir, "bin", "go")
	compiler := filepath.Join(opts.Root, filepath.FromSlash(CompilerPath))
	compilerSHA, err := hashFile(compiler)
	if err != nil {
		return nil, diagnostic.New(diagnostic.Environment, "build.no-compiler", "找不到修改版编译器 %s；先运行 ./bridge/go/build", CompilerPath).WithDetail(err.Error())
	}
	if err := checkStamp(opts.Root, sdkDir, compilerSHA); err != nil {
		return nil, err
	}
	goSHA, err := hashFile(goTool)
	if err != nil {
		return nil, diagnostic.New(diagnostic.Environment, "build.no-go", "无法读取工具链里的 go：%s", goTool).WithDetail(err.Error())
	}
	toolexec := filepath.Join(opts.Root, filepath.FromSlash(ToolexecPath))
	toolexecSHA, err := hashFile(toolexec)
	if err != nil {
		return nil, diagnostic.New(diagnostic.Environment, "build.no-toolexec", "无法读取工具包装 %s", ToolexecPath).WithDetail(err.Error())
	}
	supportDir := filepath.Join(opts.Root, filepath.FromSlash(SupportDir))
	supportSHA, err := hashTree(supportDir)
	if err != nil {
		return nil, diagnostic.New(diagnostic.Environment, "build.no-support", "无法读取固定支持目录 %s", SupportDir).WithDetail(err.Error())
	}

	// 3. 构建身份：入口、全部依赖、NIR、配置和工具内容必须一起成立。
	// Files 为空表示这是一次 NIR 回放；有 Files 时必须有同一张图的聚合哈希。
	if len(opts.Files) == 0 && opts.GraphHash != "" {
		return nil, diagnostic.New(diagnostic.Internal, "build.identity-mismatch", "有依赖图哈希但没有逐文件身份")
	}
	if len(opts.Files) > 0 {
		if opts.GraphHash == "" {
			return nil, diagnostic.New(diagnostic.Internal, "build.identity-mismatch", "有逐文件身份但没有依赖图哈希")
		}
		if opts.Source == nil {
			return nil, diagnostic.New(diagnostic.Internal, "build.identity-mismatch", "有依赖文件但没有入口身份")
		}
	}
	projectRoot := opts.ProjectRoot
	if projectRoot == "" {
		projectRoot = opts.Root
	}
	files, err := normalizeFiles(opts.Files)
	if err != nil {
		return nil, err
	}
	if err := verifyFiles(projectRoot, files); err != nil {
		return nil, err
	}
	if err := checkGraphIdentity(files, opts.GraphHash); err != nil {
		return nil, err
	}
	entry := opts.Entry
	if entry == "" && opts.Source != nil {
		entry = opts.Source.Path
	}
	if len(files) > 0 {
		if opts.Source == nil || opts.Source.Path != entry {
			return nil, diagnostic.New(diagnostic.Internal, "build.identity-mismatch",
				"入口身份 %q 与源码记录不一致", entry)
		}
		found := false
		for _, file := range files {
			if file.Path == entry {
				found = true
				if file.SHA256 != opts.Source.SHA256 {
					return nil, diagnostic.New(diagnostic.Internal, "build.identity-mismatch",
						"入口 %s 的内容哈希与依赖图不一致", file.Path)
				}
			}
		}
		if !found {
			return nil, diagnostic.New(diagnostic.Internal, "build.identity-mismatch",
				"入口 %s 不在依赖文件列表里", entry)
		}
	}
	identity, err := newIdentity(entry, files, inputSHA, Config{
		Target:     report.Target,
		SupportDir: SupportDir,
	}, Toolchain{
		GoVersion:      report.LockedVersion,
		GoSHA256:       goSHA,
		CompilerSHA256: compilerSHA,
		ToolexecSHA256: toolexecSHA,
		SupportSHA256:  supportSHA,
	})
	if err != nil {
		return nil, err
	}

	// 4. 快照落盘：编译器读的是这份文件，不是用户路径。
	snapshotDir := filepath.Join(opts.Root, filepath.FromSlash(SnapshotDir))
	if err := os.MkdirAll(snapshotDir, 0o755); err != nil {
		return nil, diagnostic.New(diagnostic.Environment, "build.snapshot-failed", "无法创建快照目录").WithDetail(err.Error())
	}
	snapshot := filepath.Join(snapshotDir, inputSHA+".nir.json")
	if err := ensureSnapshot(snapshot, data, inputSHA); err != nil {
		return nil, err
	}
	if inputPath == "" {
		// 内存里的 NIR 没有用户可见的文件路径；记录里写实际被编译的快照，
		// 而不是一个不存在的名字。
		inputPath = snapshot
	}

	// 5. 输出先写到同目录临时文件。输出路径按调用时的工作目录解析；
	// 临时路径必须是绝对的，因为下面的 go build 会用 -C 切换目录，
	// 相对路径会在错误的位置解析。
	output, err := filepath.Abs(opts.Output)
	if err != nil {
		return nil, diagnostic.New(diagnostic.Environment, "build.output-unwritable", "无法解析输出路径 %s", opts.Output).WithDetail(err.Error())
	}
	outDir := filepath.Dir(output)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, diagnostic.New(diagnostic.Environment, "build.output-unwritable", "无法创建输出目录 %s", outDir).WithDetail(err.Error())
	}
	// 同一输出同时只允许一个构建。锁覆盖临时文件、发布和恢复；第二个构建
	// 立即被拒绝，而不是两个进程互相覆盖产物或记录。
	lock, err := acquireOutputLock(output)
	if err != nil {
		return nil, err
	}
	defer lock.release()
	pub := newPublication(output)
	if err := pub.recover(); err != nil {
		return nil, err
	}
	tmpFile, err := os.CreateTemp(outDir, ".tynel-build-*")
	if err != nil {
		return nil, diagnostic.New(diagnostic.Environment, "build.output-unwritable", "无法在 %s 创建临时文件", outDir).WithDetail(err.Error())
	}
	tmpPath := tmpFile.Name()
	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpPath)
		return nil, diagnostic.New(diagnostic.Environment, "build.output-unwritable", "无法关闭临时文件 %s", tmpPath).WithDetail(err.Error())
	}
	os.Remove(tmpPath)

	// 6. 构建：Go 缓存键不含 NIR、配置和 bridge 身份，用产物输入身份作为
	// build tag。NIR、配置或工具链变化都会换标签；源码文件清单只影响完整
	// 身份，不改变同一份 NIR 的产物，因此不参与标签。
	tag := "tynel_" + identity.ArtifactSHA256
	env := append(os.Environ(),
		"TYNEL_NIR="+snapshot,
		"TYNEL_COMPILE="+compiler,
	)
	toolexecValue, err := toolexecArgument(toolexec)
	if err != nil {
		os.Remove(tmpPath)
		return nil, err
	}
	args := []string{
		"-C", opts.Root,
		"build",
		"-toolexec=" + toolexecValue,
		"-tags", "tynelapp " + tag,
		"-o", tmpPath,
		"./" + SupportDir,
	}
	if err := checkContext(opts.Context); err != nil {
		os.Remove(tmpPath)
		return nil, err
	}
	out, err := runBackend(opts.Context, runner, env, goTool, args...)
	if err != nil {
		os.Remove(tmpPath)
		if canceled := checkContext(opts.Context); canceled != nil {
			return nil, canceled
		}
		return nil, diagnostic.New(diagnostic.Environment, "build.tool-missing", "无法启动 %s", goTool).WithDetail(err.Error())
	}
	if out.ExitCode != 0 {
		os.Remove(tmpPath)
		if canceled := checkContext(opts.Context); canceled != nil {
			return nil, canceled
		}
		return nil, out.Failure("go", args)
	}
	if err := checkContext(opts.Context); err != nil {
		os.Remove(tmpPath)
		return nil, err
	}

	// 7. 发布前复核关键输入没有被替换。
	if err := verifyPostBuildInputs(snapshot, inputSHA, goTool, goSHA, compiler, compilerSHA, toolexec, toolexecSHA, supportDir, supportSHA); err != nil {
		os.Remove(tmpPath)
		return nil, err
	}

	// 8. 产物和记录都先落成临时文件，再作为一个发布事务换到目标位置。
	if err := checkContext(opts.Context); err != nil {
		os.Remove(tmpPath)
		return nil, err
	}
	info, err := os.Stat(tmpPath)
	if err != nil {
		os.Remove(tmpPath)
		return nil, diagnostic.New(diagnostic.Internal, "build.no-output", "构建报告成功但没有产出文件").WithDetail(err.Error())
	}
	outputSHA, err := hashFile(tmpPath)
	if err != nil {
		os.Remove(tmpPath)
		return nil, diagnostic.New(diagnostic.Environment, "build.output-unreadable", "无法读取构建产物").WithDetail(err.Error())
	}
	if err := syncFile(tmpPath); err != nil {
		os.Remove(tmpPath)
		return nil, diagnostic.New(diagnostic.Environment, "build.output-unreadable", "无法刷新构建产物 %s", tmpPath).WithDetail(err.Error())
	}

	// 9. 构建记录。
	record := &Record{SchemaVersion: 1, StartedAt: started.Format(time.RFC3339)}
	record.DurationMillis = time.Since(started).Milliseconds()
	record.Source = opts.Source
	record.GraphHash = opts.GraphHash
	record.Identity = identity
	record.Input.Path = inputPath
	record.Input.SHA256 = inputSHA
	record.Input.Bytes = len(data)
	record.Toolchain.Version = report.LockedVersion
	record.Toolchain.Target = report.Target
	record.Tools.Go = goTool
	record.Tools.Compiler = compiler
	record.Tools.CompilerSHA256 = compilerSHA
	record.Tools.Toolexec = toolexec
	record.Tools.BuildTag = tag
	record.Tools.Snapshot = snapshot
	record.Output.Path = opts.Output
	record.Output.SHA256 = outputSHA
	record.Output.Bytes = info.Size()
	stagedRecord, err := stageRecord(RecordPath(output), record)
	if err != nil {
		os.Remove(tmpPath)
		return nil, err
	}
	pub.stagedOutput = tmpPath
	pub.stagedRecord = stagedRecord
	if err := pub.commit(); err != nil {
		os.Remove(tmpPath)
		os.Remove(stagedRecord)
		return nil, err
	}
	return record, nil
}

// checkContext 把取消转换成环境类诊断，避免把它误报成源码或工具故障。
func checkContext(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return diagnostic.New(diagnostic.Environment, "build.canceled", "构建已取消").
			WithStage(diagnostic.StageBuild).
			WithDetail(err.Error())
	}
	return nil
}

// runBackend 在 Runner 支持取消时把 context 传给外部进程。
func runBackend(ctx context.Context, runner toolchain.Runner, env []string, name string, args ...string) (toolchain.Output, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if cancellable, ok := runner.(toolchain.ContextRunner); ok {
		return cancellable.RunEnvContext(ctx, env, name, args...)
	}
	return runner.RunEnv(env, name, args...)
}

// toolexecArgument 把工具包装路径编码成 go build 的 -toolexec 值。
//
// go 命令用 quoted.Split 解析这个值；含空格的路径如果不加引号，会在第一个
// 空格处被拆成程序名和额外参数。Go 的引号内没有转义，所以路径同时含单引号
// 和双引号时无法安全表达，直接拒绝而不是悄悄生成错误命令。
func toolexecArgument(path string) (string, error) {
	if !strings.ContainsAny(path, " \t\n\r") {
		return path, nil
	}
	if !strings.ContainsRune(path, '\'') {
		return "'" + path + "'", nil
	}
	if !strings.ContainsRune(path, '"') {
		return `"` + path + `"`, nil
	}
	return "", diagnostic.New(diagnostic.Environment, "build.toolexec-unquotable",
		"工具包装路径同时含单引号和双引号，无法安全传给 Go：%s", path)
}

// RecordPath 返回某个产物对应的构建记录路径。
func RecordPath(output string) string { return output + ".build.json" }

type stamp struct {
	SchemaVersion  int               `json:"schemaVersion"`
	CompilerSHA256 string            `json:"compilerSHA256"`
	SDKVersion     string            `json:"sdkVersion"`
	BuiltAt        string            `json:"builtAt"`
	Sources        map[string]string `json:"sources"`
}

// checkStamp 确认修改版编译器与当前 bridge 源码、SDK 源码一致。过期编译器
// 直接拒绝，避免用旧 bridge 生成产物。
func checkStamp(root, sdkDir, compilerSHA string) error {
	path := filepath.Join(root, filepath.FromSlash(StampPath))
	data, err := os.ReadFile(path)
	if err != nil {
		return diagnostic.New(diagnostic.Environment, "build.no-stamp", "找不到编译器戳记 %s；先运行 ./bridge/go/build", StampPath).WithDetail(err.Error())
	}
	var st stamp
	if err := json.Unmarshal(data, &st); err != nil {
		return diagnostic.New(diagnostic.Environment, "build.bad-stamp", "编译器戳记无法解析").WithDetail(err.Error())
	}
	if st.CompilerSHA256 != compilerSHA {
		return staleCompiler("编译器二进制与戳记不符")
	}
	for rel, want := range st.Sources {
		var got string
		var err error
		if rel == "sdk/cmd/compile/internal/gc/main.go" {
			got, err = hashFile(filepath.Join(sdkDir, "src", "cmd", "compile", "internal", "gc", "main.go"))
		} else {
			got, err = hashFile(filepath.Join(root, filepath.FromSlash(rel)))
		}
		if err != nil {
			return staleCompiler(fmt.Sprintf("无法读取 %s", rel))
		}
		if got != want {
			return staleCompiler(fmt.Sprintf("%s 已变化", rel))
		}
	}
	return nil
}

func staleCompiler(reason string) error {
	return diagnostic.New(diagnostic.Environment, "build.stale-compiler", "修改版编译器已过期（%s）；重新运行 ./bridge/go/build", reason)
}

func hashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return sha256Hex(data), nil
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
