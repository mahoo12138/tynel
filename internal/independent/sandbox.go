package independent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const maxLogBytes = 1 << 20

type sandboxRunner struct {
	sandboxPath    string
	payloadDir     string
	profilesDir    string
	logsDir        string
	protectedRoots []string
	nodePaths      []string
	timeout        time.Duration
}

func (r *sandboxRunner) run(ctx context.Context, name, command string, args ...string) RunResult {
	started := time.Now()
	result := RunResult{ExitCode: -1}
	profile, err := r.profile(command)
	if err != nil {
		result.StartError = err.Error()
		return result
	}
	profilePath := filepath.Join(r.profilesDir, name+".sb")
	if err := os.WriteFile(profilePath, []byte(profile), 0o644); err != nil {
		result.StartError = fmt.Sprintf("写入沙箱配置：%v", err)
		return result
	}
	sum := sha256.Sum256([]byte(profile))
	result.ProfileSHA256 = hex.EncodeToString(sum[:])

	stdout, stdoutWriter, err := newCappedLog(filepath.Join(r.logsDir, name+".stdout"))
	if err != nil {
		result.StartError = err.Error()
		return result
	}
	defer stdout.Close()
	stderr, stderrWriter, err := newCappedLog(filepath.Join(r.logsDir, name+".stderr"))
	if err != nil {
		result.StartError = err.Error()
		return result
	}
	defer stderr.Close()

	commandCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	cmd := exec.CommandContext(commandCtx, r.sandboxPath, append([]string{"-f", profilePath, command}, args...)...)
	cmd.Dir = r.payloadDir
	cmd.Env = isolatedEnvironment(r.payloadDir)
	cmd.Stdout = stdoutWriter
	cmd.Stderr = stderrWriter
	runErr := cmd.Run()
	result.DurationMillis = time.Since(started).Milliseconds()
	result.TimedOut = errors.Is(commandCtx.Err(), context.DeadlineExceeded)
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}
	if runErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(runErr, &exitErr) {
			result.StartError = runErr.Error()
		}
	}
	stdoutWriter.Sync()
	stderrWriter.Sync()
	stdoutData, _ := os.ReadFile(stdout.Name())
	stderrData, _ := os.ReadFile(stderr.Name())
	result.Stdout = string(stdoutData)
	result.Stderr = string(stderrData)
	result.OutputTruncated = stdoutWriter.truncated || stderrWriter.truncated
	return result
}

func (r *sandboxRunner) profile(command string) (string, error) {
	var b strings.Builder
	b.WriteString("(version 1)\n(allow default)\n")
	// 被测程序不能启动 Node、shell 或其他辅助进程。sandbox-exec 本身在应用
	// 配置后才 exec 目标，因此最后只放行当前目标的路径。
	b.WriteString("(deny process-exec)\n")
	for _, root := range r.protectedRoots {
		quoted, err := sbplQuote(root)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "(deny file-read* (subpath %s))\n", quoted)
		fmt.Fprintf(&b, "(deny file-write* (subpath %s))\n", quoted)
	}
	for _, path := range r.nodePaths {
		quoted, err := sbplQuote(path)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "(deny file-read* (literal %s))\n", quoted)
	}
	payload, err := sbplQuote(r.payloadDir)
	if err != nil {
		return "", err
	}
	// 载荷例外必须位于 home/project 的拒绝规则之后。它只放开 exe 所在的新
	// 目录，不放开源码、构建记录、NIR 快照或编译器目录。
	fmt.Fprintf(&b, "(allow file-read* (subpath %s))\n", payload)
	fmt.Fprintf(&b, "(allow file-write* (subpath %s))\n", payload)
	target, err := sbplQuote(command)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(&b, "(allow process-exec (literal %s))\n", target)
	return b.String(), nil
}

func isolatedEnvironment(payloadDir string) []string {
	return []string{
		"HOME=" + filepath.Join(payloadDir, "home"),
		"TMPDIR=" + filepath.Join(payloadDir, "tmp"),
		"PATH=/nonexistent",
		"LC_ALL=C",
	}
}

func protectedRoots(projectRoot string) ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("无法确定用户目录：%w", err)
	}
	roots := []string{home, projectRoot, "/opt/homebrew", "/usr/local"}
	seen := make(map[string]bool, len(roots))
	unique := roots[:0]
	for _, root := range roots {
		if root == "" || root == "/" || seen[root] {
			continue
		}
		abs, err := filepath.Abs(root)
		if err != nil {
			return nil, err
		}
		abs = filepath.Clean(abs)
		if !seen[abs] {
			seen[abs] = true
			unique = append(unique, abs)
		}
	}
	sort.Strings(unique)
	return unique, nil
}

func sbplQuote(value string) (string, error) {
	if value == "" {
		return "", errors.New("沙箱路径不能为空")
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("沙箱路径包含控制字符：%q", value)
		}
	}
	value = strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value)
	return `"` + value + `"`, nil
}

type cappedLogWriter struct {
	file      *os.File
	remaining int
	truncated bool
}

func newCappedLog(path string) (*os.File, *cappedLogWriter, error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return nil, nil, fmt.Errorf("创建日志 %s：%w", path, err)
	}
	return file, &cappedLogWriter{file: file, remaining: maxLogBytes}, nil
}

func (w *cappedLogWriter) Write(p []byte) (int, error) {
	original := len(p)
	if len(p) > w.remaining {
		p = p[:w.remaining]
		w.truncated = true
	}
	if len(p) > 0 {
		if _, err := w.file.Write(p); err != nil {
			return 0, err
		}
		w.remaining -= len(p)
	}
	// 丢弃超过上限的部分，但向子进程报告完整接收，避免无限输出因短写
	// 改变被测程序行为；报告中的 outputTruncated 会使验收失败。
	return original, nil
}

func (w *cappedLogWriter) Sync() {
	_ = w.file.Sync()
}
