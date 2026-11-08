package independent

import (
	"crypto/sha256"
	"debug/macho"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var allowedLibraries = map[string]bool{
	"/usr/lib/libSystem.B.dylib": true,
	"/usr/lib/libresolv.9.dylib": true,
}

type buildRecord struct {
	Output struct {
		SHA256 string `json:"sha256"`
		Bytes  int64  `json:"bytes"`
	} `json:"output"`
}

// prepareArtifacts 先核对原文件与构建记录，再复制到只含 exe 的载荷目录。
// 复制后的哈希必须再次一致，防止把源目录身份误当成传输副本身份。
func prepareArtifacts(artifactsDir, payloadDir string, programs []Program) ([]Artifact, error) {
	artifacts := make([]Artifact, 0, len(programs))
	for _, program := range programs {
		source := filepath.Join(artifactsDir, program.Name)
		recordPath := source + ".build.json"
		record, err := readBuildRecord(recordPath)
		if err != nil {
			return artifacts, err
		}
		info, err := os.Lstat(source)
		if err != nil {
			return artifacts, fmt.Errorf("读取产物 %s 失败：%w", program.Name, err)
		}
		if !info.Mode().IsRegular() {
			return artifacts, fmt.Errorf("产物 %s 不是普通文件", source)
		}
		if info.Mode().Perm()&0o111 == 0 {
			return artifacts, fmt.Errorf("产物 %s 没有执行权限", source)
		}
		sourceSum, err := hashFile(source)
		if err != nil {
			return artifacts, err
		}
		if sourceSum != strings.ToLower(record.Output.SHA256) || info.Size() != record.Output.Bytes {
			return artifacts, fmt.Errorf("产物 %s 与构建记录不符：实际 sha256=%s、字节=%d，记录 sha256=%s、字节=%d",
				program.Name, sourceSum, info.Size(), record.Output.SHA256, record.Output.Bytes)
		}
		libraries, err := inspectMachO(source)
		if err != nil {
			return artifacts, fmt.Errorf("检查产物 %s 失败：%w", program.Name, err)
		}
		recordSum, err := hashFile(recordPath)
		if err != nil {
			return artifacts, err
		}
		payload := filepath.Join(payloadDir, program.Name)
		if err := copyRegular(source, payload, info.Mode().Perm()); err != nil {
			return artifacts, fmt.Errorf("传输产物 %s 失败：%w", program.Name, err)
		}
		payloadSum, err := hashFile(payload)
		if err != nil {
			return artifacts, err
		}
		if payloadSum != sourceSum {
			return artifacts, fmt.Errorf("产物 %s 传输前后哈希不同：%s -> %s", program.Name, sourceSum, payloadSum)
		}
		artifacts = append(artifacts, Artifact{
			Name: program.Name, SourcePath: source, PayloadPath: payload,
			SHA256: sourceSum, Bytes: info.Size(), BuildRecordSHA256: recordSum,
			Libraries: libraries,
		})
	}
	return artifacts, nil
}

func readBuildRecord(path string) (buildRecord, error) {
	var record buildRecord
	data, err := os.ReadFile(path)
	if err != nil {
		return record, fmt.Errorf("读取构建记录 %s 失败：%w", path, err)
	}
	if err := json.Unmarshal(data, &record); err != nil {
		return record, fmt.Errorf("构建记录 %s 不是预期 JSON：%w", path, err)
	}
	if len(record.Output.SHA256) != 64 || !isLowerHex(record.Output.SHA256) || record.Output.Bytes < 0 {
		return record, fmt.Errorf("构建记录 %s 的 output.sha256 或 output.bytes 非法", path)
	}
	return record, nil
}

// inspectMachO 同时确认目标指令集和动态库白名单。Go 代码与运行时链入 exe，
// 不等于 macOS 的 libSystem 也静态链入；两项必须分别说明。
func inspectMachO(path string) ([]string, error) {
	f, err := macho.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if f.Cpu != macho.CpuArm64 || f.Type != macho.TypeExec {
		return nil, fmt.Errorf("不是 darwin/arm64 Mach-O 可执行文件")
	}
	libraries, err := f.ImportedLibraries()
	if err != nil {
		return nil, fmt.Errorf("读取动态库命令：%w", err)
	}
	if err := validateLibraries(libraries); err != nil {
		return nil, err
	}
	sort.Strings(libraries)
	return libraries, nil
}

func validateLibraries(libraries []string) error {
	for _, library := range libraries {
		if !allowedLibraries[library] {
			return fmt.Errorf("发现未声明动态库 %s", library)
		}
	}
	return nil
}

func copyRegular(source, target string, mode os.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
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
	return os.Chmod(target, mode)
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("打开 %s 计算哈希失败：%w", path, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("读取 %s 计算哈希失败：%w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func isLowerHex(value string) bool {
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func fileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}
