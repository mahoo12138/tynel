package build

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
	"github.com/Mahoo12138/tynel/compiler/graph"
)

// IdentitySchemaVersion 是构建身份结构的版本。字段含义变化时递增，不把新旧
// 记录当成同一种输入。
const IdentitySchemaVersion = 1

// File 是一份参与构建的源码或依赖。Path 是项目内相对路径，SHA256 是建立依赖
// 图时固定下来的内容哈希。类型边和值边的文件都列进来：检查需要前者，运行
// 需要后者，两者变化都应让本次构建身份失效。
type File struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// Config 是不由源码内容表达、但会影响产物的构建配置。
type Config struct {
	Target     string `json:"target"`
	SupportDir string `json:"supportDir"`
}

// Toolchain 是这次真正执行构建的工具身份。路径不进入哈希，内容会。
type Toolchain struct {
	GoVersion      string `json:"goVersion"`
	GoSHA256       string `json:"goSha256"`
	CompilerSHA256 string `json:"compilerSha256"`
	ToolexecSHA256 string `json:"toolexecSha256"`
	SupportSHA256  string `json:"supportSha256"`
}

// Identity 是一次构建全部输入的内容身份。
//
// Hash 覆盖入口与全部依赖路径和内容、NIR 字节、构建配置、工具链内容。它不
// 包含输出路径、开始时间或耗时：同样的输入换个输出位置仍应得到同一个身份。
//
// ArtifactSHA256 只覆盖会改变生成代码的输入：NIR、配置与工具链。它用作 Go
// build tag，让底层缓存按完整内容而不是短前缀或时间区分。它刻意不含源码文件
// 列表：类型依赖变化需要重新检查并写进完整身份，但生成的 NIR 不变时，同一份
// NIR 回放应得到逐字节相同的产物。
type Identity struct {
	SchemaVersion  int       `json:"schemaVersion"`
	Hash           string    `json:"hash"`
	ArtifactSHA256 string    `json:"artifactSha256"`
	Entry          string    `json:"entry,omitempty"`
	Files          []File    `json:"files"`
	InputSHA256    string    `json:"inputSha256"`
	Config         Config    `json:"config"`
	Toolchain      Toolchain `json:"toolchain"`
}

// normalizeFiles 复制并排序文件身份，同时拒绝不能参与稳定哈希的路径。
// 构建只接受项目内相对、已规范化的斜杠路径；绝对路径、空路径和含 .. 的路径
// 说明调用方给出了错误身份，不能带着它继续。
func normalizeFiles(files []File) ([]File, error) {
	out := make([]File, 0, len(files))
	seen := make(map[string]bool, len(files))
	for _, file := range files {
		if file.Path == "" || path.IsAbs(file.Path) || path.Clean(file.Path) != file.Path ||
			file.Path == "." || file.Path == ".." || strings.HasPrefix(file.Path, "../") {
			return nil, diagnostic.New(diagnostic.Internal, "build.bad-file-identity",
				"构建身份里的文件路径 %q 不是规范的项目内相对路径", file.Path)
		}
		if seen[file.Path] {
			return nil, diagnostic.New(diagnostic.Internal, "build.bad-file-identity",
				"构建身份里的文件路径 %q 重复", file.Path)
		}
		if len(file.SHA256) != 64 {
			return nil, diagnostic.New(diagnostic.Internal, "build.bad-file-identity",
				"构建身份里的文件 %s 缺少完整的内容哈希", file.Path)
		}
		seen[file.Path] = true
		out = append(out, file)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// verifyFiles 在启动后端之前重新读取每个文件，确认它仍是依赖图固定下来的
// 那一版。图建立后、构建开始前换掉文件，会让「记录里写的输入」和「实际被
// 检查的内容」不一致；这时直接失败，不生成一个看似成功的新产物。
func verifyFiles(root string, files []File) error {
	for _, file := range files {
		got, err := hashFile(filepath.Join(root, filepath.FromSlash(file.Path)))
		if err != nil {
			return diagnostic.New(diagnostic.Environment, "build.file-changed",
				"构建开始前无法读取依赖 %s", file.Path).WithDetail(err.Error())
		}
		if got != file.SHA256 {
			return diagnostic.New(diagnostic.Environment, "build.file-changed",
				"构建开始前依赖 %s 已变化（%s，构建身份记录的是 %s）", file.Path, short(got), short(file.SHA256))
		}
	}
	return nil
}

// checkGraphIdentity 确认逐文件身份与调用方给出的图哈希一致。两个值由同一个
// 依赖图产生时必然相同；不同则说明文件名、内容或图哈希在传递中被替换，
// 不能用一个错误的聚合身份写进构建记录。
func checkGraphIdentity(files []File, graphHash string) error {
	if graphHash == "" {
		return nil
	}
	infos := make([]graph.FileInfo, 0, len(files))
	for _, file := range files {
		infos = append(infos, graph.FileInfo{Path: file.Path, SHA256: file.SHA256})
	}
	if got := graph.HashFiles(infos); got != graphHash {
		return diagnostic.New(diagnostic.Internal, "build.identity-mismatch",
			"依赖图身份与逐文件身份不一致：图记录 %s，文件列表得到 %s", short(graphHash), short(got))
	}
	return nil
}

// newIdentity 计算完整身份。files 必须已经规范化；这里不再读磁盘，哈希只由
// 调用方固定的数据决定。
func newIdentity(entry string, files []File, inputSHA string, config Config, toolchain Toolchain) (Identity, error) {
	normalized, err := normalizeFiles(files)
	if err != nil {
		return Identity{}, err
	}
	identity := Identity{
		SchemaVersion: IdentitySchemaVersion,
		Entry:         entry,
		Files:         normalized,
		InputSHA256:   inputSHA,
		Config:        config,
		Toolchain:     toolchain,
	}
	document := struct {
		SchemaVersion int       `json:"schemaVersion"`
		Entry         string    `json:"entry,omitempty"`
		Files         []File    `json:"files"`
		InputSHA256   string    `json:"inputSha256"`
		Config        Config    `json:"config"`
		Toolchain     Toolchain `json:"toolchain"`
	}{
		SchemaVersion: identity.SchemaVersion,
		Entry:         identity.Entry,
		Files:         identity.Files,
		InputSHA256:   identity.InputSHA256,
		Config:        identity.Config,
		Toolchain:     identity.Toolchain,
	}
	data, err := json.Marshal(document)
	if err != nil {
		return Identity{}, diagnostic.New(diagnostic.Internal, "build.identity-failed",
			"无法序列化构建身份").WithDetail(err.Error())
	}
	sum := sha256.Sum256(data)
	identity.Hash = hex.EncodeToString(sum[:])

	artifactDocument := struct {
		SchemaVersion int       `json:"schemaVersion"`
		InputSHA256   string    `json:"inputSha256"`
		Config        Config    `json:"config"`
		Toolchain     Toolchain `json:"toolchain"`
	}{
		SchemaVersion: identity.SchemaVersion,
		InputSHA256:   identity.InputSHA256,
		Config:        identity.Config,
		Toolchain:     identity.Toolchain,
	}
	artifactData, err := json.Marshal(artifactDocument)
	if err != nil {
		return Identity{}, diagnostic.New(diagnostic.Internal, "build.identity-failed",
			"无法序列化产物输入身份").WithDetail(err.Error())
	}
	artifactSum := sha256.Sum256(artifactData)
	identity.ArtifactSHA256 = hex.EncodeToString(artifactSum[:])
	return identity, nil
}

// verifyPostBuildInputs 在产物发布之前再核对一次关键输入。编译器读的是快照
// 文件；如果快照在读取前后被别的程序替换，产物就不再对应记录里的 NIR。
// 编译器、工具包装和固定支持文件同样复核，避免「构建成功但用的是另一份
// 工具」这种无法从记录解释的结果。
func verifyPostBuildInputs(snapshot, inputSHA, compiler, compilerSHA, toolexec, toolexecSHA, supportDir, supportSHA string) error {
	checks := []struct {
		name string
		path string
		want string
	}{
		{"NIR 快照", snapshot, inputSHA},
		{"修改版编译器", compiler, compilerSHA},
		{"工具包装", toolexec, toolexecSHA},
	}
	for _, check := range checks {
		got, err := hashFile(check.path)
		if err != nil || got != check.want {
			return diagnostic.New(diagnostic.Environment, "build.input-changed",
				"构建期间%s被替换，产物不发布", check.name)
		}
	}
	got, err := hashTree(supportDir)
	if err != nil || got != supportSHA {
		return diagnostic.New(diagnostic.Environment, "build.input-changed",
			"构建期间固定支持目录被替换，产物不发布")
	}
	return nil
}

// hashTree 计算一个目录的稳定哈希：排序后的相对路径加内容哈希。支持目录当前
// 只有一个文件，按目录计算可以覆盖以后增加的文件，不依赖文件名清单。
func hashTree(dir string) (string, error) {
	var lines []string
	err := filepath.WalkDir(dir, func(filePath string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, filePath)
		if err != nil {
			return err
		}
		sum, err := hashFile(filePath)
		if err != nil {
			return err
		}
		lines = append(lines, filepath.ToSlash(rel)+"\x00"+sum)
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(lines)
	h := sha256.New()
	for _, line := range lines {
		h.Write([]byte(line))
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func short(hash string) string {
	if len(hash) <= 12 {
		return hash
	}
	return hash[:12]
}
