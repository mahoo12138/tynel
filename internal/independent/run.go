package independent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Run 准备只含 exe 的载荷，先验证隔离边界，再逐个执行程序。任何阶段失败
// 都会留下报告；沙箱无法启动时不会退回普通 exec 冒充独立运行。
func Run(ctx context.Context, cfg Config) (*Report, error) {
	cfg, err := normalizeConfig(cfg)
	if err != nil {
		return nil, err
	}
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return nil, fmt.Errorf("06.06 当前只在 darwin/arm64 验收，实际是 %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	exists, err := fileExists(cfg.OutDir)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, fmt.Errorf("证据目录已存在：%s；请换一个新的 --out，避免混入旧结果", cfg.OutDir)
	}
	payloadDir := filepath.Join(cfg.OutDir, "payload")
	for _, dir := range []string{
		payloadDir,
		filepath.Join(payloadDir, "home"),
		filepath.Join(payloadDir, "tmp"),
		filepath.Join(cfg.OutDir, "profiles"),
		filepath.Join(cfg.OutDir, "logs"),
		filepath.Join(cfg.OutDir, "forbidden"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("创建证据目录 %s：%w", dir, err)
		}
	}

	roots, err := protectedRoots(cfg.ProjectRoot)
	if err != nil {
		return nil, err
	}
	nodePath, nodeResolved, nodePaths := nodeIdentity(cfg.NodePath)
	report := &Report{
		SchemaVersion: SchemaVersion,
		Node:          Node,
		GeneratedAt:   time.Now().Format(time.RFC3339),
		Status:        "failed",
		Host: HostInfo{
			GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
			Uname:          commandText("uname", "-a"),
			ProductVersion: commandText("sw_vers", "-productVersion"),
			BuildVersion:   commandText("sw_vers", "-buildVersion"),
		},
		Isolation: IsolationInfo{
			SandboxPath: cfg.SandboxPath, ProjectRoot: cfg.ProjectRoot,
			HomeRoot: homeRoot(), PayloadDir: payloadDir,
			NodePath: nodePath, NodeResolvedPath: nodeResolved,
			Environment: isolatedEnvironment(payloadDir),
		},
	}
	finish := func(status string, runErr error) (*Report, error) {
		report.Status = status
		if runErr != nil {
			report.Failure = runErr.Error()
		}
		if err := writeReport(cfg.OutDir, report); err != nil {
			runErr = errors.Join(runErr, err)
		}
		return report, runErr
	}

	programs := DefaultPrograms()
	artifacts, err := prepareArtifacts(cfg.ArtifactsDir, payloadDir, programs)
	report.Artifacts = artifacts
	if err != nil {
		return finish("failed", err)
	}

	runner := &sandboxRunner{
		sandboxPath: cfg.SandboxPath, payloadDir: payloadDir,
		profilesDir: filepath.Join(cfg.OutDir, "profiles"), logsDir: filepath.Join(cfg.OutDir, "logs"),
		protectedRoots: roots, nodePaths: nodePaths, timeout: cfg.Timeout,
	}
	preflight := runner.run(ctx, "probe-00-preflight", "/usr/bin/true")
	preflight.Passed = preflight.ExitCode == 0 && preflight.StartError == "" && !preflight.TimedOut
	report.Probes = append(report.Probes, Probe{Name: "preflight", Kind: "sandbox-start", Target: "/usr/bin/true", Result: &preflight})
	if !preflight.Passed {
		return finish("blocked", &BlockedError{Reason: fmt.Sprintf(
			"限制性 sandbox-exec 无法启动（退出 %d）：%s；未执行任何载荷，也未降级为普通运行",
			preflight.ExitCode, oneLine(preflight.Stderr))})
	}

	canary := filepath.Join(cfg.OutDir, "forbidden", "source-canary.ts")
	if err := os.WriteFile(canary, []byte("console.log(0);\n"), 0o644); err != nil {
		return finish("failed", fmt.Errorf("写入隔离探针：%w", err))
	}
	// 宿主先确认探针文件可读；随后同一文件在沙箱内必须不可读，才能证明
	// 非零退出来自隔离规则，而不是文件本来不存在。
	if _, err := os.ReadFile(canary); err != nil {
		return finish("failed", fmt.Errorf("宿主无法读取隔离探针：%w", err))
	}
	denied := runner.run(ctx, "probe-01-source-denied", "/bin/cat", canary)
	denied.Passed = accessDenied(denied)
	report.Probes = append(report.Probes, Probe{Name: "source-read", Kind: "must-deny", Target: canary, Result: &denied})
	if !denied.Passed {
		return finish("failed", fmt.Errorf("沙箱内仍能读取源码探针，或拒绝原因不明确：%s", oneLine(denied.Stderr)))
	}

	if nodePath == "" {
		report.Probes = append(report.Probes, Probe{Name: "node-read", Kind: "must-deny", Skipped: true, Detail: "宿主 PATH 未找到 Node；最小环境和全局禁止子进程仍适用于载荷"})
	} else {
		nodeDenied := runner.run(ctx, "probe-02-node-denied", "/bin/cat", nodePath)
		nodeDenied.Passed = accessDenied(nodeDenied)
		report.Probes = append(report.Probes, Probe{Name: "node-read", Kind: "must-deny", Target: nodePath, Result: &nodeDenied})
		if !nodeDenied.Passed {
			return finish("failed", fmt.Errorf("沙箱内仍能读取 Node %s，或拒绝原因不明确：%s", nodePath, oneLine(nodeDenied.Stderr)))
		}
	}

	allPassed := true
	for i, program := range programs {
		result := runner.run(ctx, fmt.Sprintf("run-%02d-%s", i+1, program.Name), report.Artifacts[i].PayloadPath)
		result.Passed = result.ExitCode == 0 && result.StartError == "" && !result.TimedOut &&
			!result.OutputTruncated && result.Stdout == program.Expected && result.Stderr == ""
		report.Artifacts[i].Run = &result
		if !result.Passed {
			allPassed = false
		}
	}
	if !allPassed {
		return finish("failed", errors.New("至少一个载荷的输出、stderr、退出状态或超时检查不符合预期；详见各 artifacts.run"))
	}
	return finish("passed", nil)
}

func normalizeConfig(cfg Config) (Config, error) {
	var err error
	for name, path := range map[string]*string{
		"project": &cfg.ProjectRoot, "artifacts": &cfg.ArtifactsDir, "out": &cfg.OutDir,
	} {
		if *path == "" {
			return cfg, fmt.Errorf("--%s 不能为空", name)
		}
		*path, err = filepath.Abs(*path)
		if err != nil {
			return cfg, fmt.Errorf("解析 --%s：%w", name, err)
		}
		*path = filepath.Clean(*path)
	}
	if cfg.SandboxPath == "" {
		cfg.SandboxPath = "/usr/bin/sandbox-exec"
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	if cfg.NodePath == "" {
		cfg.NodePath, _ = exec.LookPath("node")
	}
	if cfg.SandboxPath != "/usr/bin/sandbox-exec" {
		cfg.SandboxPath, err = filepath.Abs(cfg.SandboxPath)
		if err != nil {
			return cfg, err
		}
	}
	info, err := os.Stat(cfg.SandboxPath)
	if err != nil || info.IsDir() || info.Mode().Perm()&0o111 == 0 {
		return cfg, fmt.Errorf("sandbox-exec 不可执行：%s", cfg.SandboxPath)
	}
	return cfg, nil
}

func nodeIdentity(path string) (string, string, []string) {
	if path == "" {
		return "", "", nil
	}
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		resolved = path
	}
	seen := make(map[string]bool, 2)
	paths := make([]string, 0, 2)
	for _, candidate := range []string{path, resolved} {
		if candidate != "" && !seen[candidate] {
			seen[candidate] = true
			paths = append(paths, candidate)
		}
	}
	return path, resolved, paths
}

func accessDenied(result RunResult) bool {
	if result.ExitCode == 0 || result.StartError != "" || result.TimedOut {
		return false
	}
	stderr := strings.ToLower(result.Stderr)
	if strings.Contains(stderr, "sandbox_apply") {
		return false
	}
	return strings.Contains(stderr, "operation not permitted") ||
		strings.Contains(stderr, "permission denied") || strings.Contains(stderr, "denied")
}

func writeReport(outDir string, report *Report) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化独立运行报告：%w", err)
	}
	data = append(data, '\n')
	path := filepath.Join(outDir, "report.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("写入独立运行报告：%w", err)
	}
	environment := fmt.Sprintf("%s\nmacOS %s (%s)\n%s/%s\nstatus=%s\n",
		report.Host.Uname, report.Host.ProductVersion, report.Host.BuildVersion,
		report.Host.GOOS, report.Host.GOARCH, report.Status)
	if err := os.WriteFile(filepath.Join(outDir, "environment.txt"), []byte(environment), 0o644); err != nil {
		return fmt.Errorf("写入环境清单：%w", err)
	}
	return nil
}

func commandText(name string, args ...string) string {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Sprintf("%s: %s", err, oneLine(string(out)))
	}
	return oneLine(string(out))
}

func oneLine(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func homeRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}
