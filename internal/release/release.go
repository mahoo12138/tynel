// Package release 定义 Tynel 发行目录的清单与完整性检查。
//
// 发行目录不是仓库副本：它只带运行编译器所需的 CLI、前端宿主、修改版
// 编译器、工具包装、固定支持、Go SDK、锁文件、示例和许可证。清单记录每个
// 文件的内容哈希与权限；移动目录后，路径仍相对发行根解释。
package release

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
)

// ManifestName 是发行根下的清单文件名。
const ManifestName = "release.json"

// RequiredFiles 是发行目录必须带的文件。清单缺任何一项都不能启动构建。
var RequiredFiles = []string{
	"bin/tynel",
	"go.mod",
	"tools/toolchain/go.lock.json",
	".work/toolchain/toolchain.json",
	".work/bridge/compile",
	".work/bridge/stamp.json",
	".work/frontend/bin/consumer",
	".work/frontend/bin/consumer.stamp.json",
	"bridge/go/toolexec",
	"bridge/go/support/support.go",
	"bridge/go/gc/tynel_bridge.go",
	"bridge/go/patches/0001-inject.patch",
}

// Identity 是一份上游组件在发行时的身份。
type Identity struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Commit  string `json:"commit"`
	License string `json:"license"`
}

// Manifest 是发行目录的完整文件清单。
//
// Files 不含 release.json 自身，避免清单记录自己的哈希。目录里的运行时
// 状态（例如 .work/build/snapshots）不属于发行内容，可以额外存在。
type Manifest struct {
	SchemaVersion int        `json:"schemaVersion"`
	Name          string     `json:"name"`
	Version       string     `json:"version"`
	Commit        string     `json:"commit"`
	Target        string     `json:"target"`
	GeneratedAt   string     `json:"generatedAt"`
	Go            Identity   `json:"go"`
	Frontend      Identity   `json:"frontend"`
	Files         []FileSpec `json:"files"`
}

// FileSpec 是一个发行文件的内容和权限身份。
type FileSpec struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
	Mode   string `json:"mode"`
}

// IsSealed 判断目录是否带发行清单。
func IsSealed(root string) (bool, error) {
	info, err := os.Stat(filepath.Join(root, ManifestName))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, diagnostic.New(diagnostic.Environment, "release.unreadable",
			"无法读取发行清单 %s", filepath.Join(root, ManifestName)).WithDetail(err.Error())
	}
	if info.IsDir() {
		return false, diagnostic.New(diagnostic.Environment, "release.invalid",
			"发行清单 %s 是目录", filepath.Join(root, ManifestName))
	}
	return true, nil
}

// Load 读取并校验清单结构，但不读取清单列出的文件。
func Load(root string) (*Manifest, error) {
	path := filepath.Join(root, ManifestName)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, diagnostic.New(diagnostic.Environment, "release.missing",
			"发行目录缺少清单 %s", ManifestName).WithDetail(err.Error()).
			WithStage(diagnostic.StageToolchain)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, diagnostic.New(diagnostic.Environment, "release.invalid",
			"发行清单 %s 不是预期的 JSON 结构", ManifestName).WithDetail(err.Error()).
			WithStage(diagnostic.StageToolchain)
	}
	if err := validateManifest(&m); err != nil {
		return nil, err
	}
	return &m, nil
}

func validateManifest(m *Manifest) error {
	bad := func(format string, args ...any) error {
		return diagnostic.New(diagnostic.Environment, "release.invalid", format, args...).
			WithStage(diagnostic.StageToolchain)
	}
	if m.SchemaVersion != 1 {
		return bad("发行清单的 schemaVersion 是 %d，本版本只认识 1", m.SchemaVersion)
	}
	if m.Name == "" || m.Version == "" || m.Commit == "" || m.Target == "" {
		return bad("发行清单缺少 name、version、commit 或 target")
	}
	seen := make(map[string]bool, len(m.Files))
	for _, f := range m.Files {
		if err := checkPath(f.Path); err != nil {
			return bad("发行清单里的路径 %q 不安全：%v", f.Path, err)
		}
		if f.Path == ManifestName {
			return bad("发行清单不能登记自身")
		}
		if seen[f.Path] {
			return bad("发行清单重复登记 %s", f.Path)
		}
		seen[f.Path] = true
		if len(f.SHA256) != 64 || !isHex(f.SHA256) {
			return bad("发行清单里 %s 的 sha256 非法", f.Path)
		}
		if f.Bytes < 0 {
			return bad("发行清单里 %s 的字节数为负数", f.Path)
		}
		if len(f.Mode) != 4 || !isOctal(f.Mode) {
			return bad("发行清单里 %s 的 mode 不是四位八进制", f.Path)
		}
	}
	for _, path := range RequiredFiles {
		if !seen[path] {
			return bad("发行清单没有登记必需文件 %s", path)
		}
	}
	return nil
}

func checkPath(path string) error {
	if path == "" {
		return errors.New("空路径")
	}
	if filepath.IsAbs(path) || strings.Contains(path, `\`) {
		return errors.New("必须是使用正斜杠的相对路径")
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	if clean != path || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return errors.New("不能离开发行根")
	}
	return nil
}

func isHex(s string) bool {
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func isOctal(s string) bool {
	for _, r := range s {
		if r < '0' || r > '7' {
			return false
		}
	}
	return true
}

// Collect 按目录当前内容生成清单。它不写文件，也不修改目录。
func Collect(root string, m Manifest) (*Manifest, error) {
	m.SchemaVersion = 1
	m.Files = nil
	for _, path := range RequiredFiles {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(path))); err != nil {
			return nil, diagnostic.New(diagnostic.Environment, "release.missing",
				"发行目录缺少必需文件 %s", path).WithDetail(err.Error()).
				WithStage(diagnostic.StageToolchain)
		}
	}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == ManifestName {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("发行目录不能包含符号链接：%s", rel)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("发行目录只能包含普通文件：%s", rel)
		}
		sum, err := hashFile(path)
		if err != nil {
			return err
		}
		m.Files = append(m.Files, FileSpec{
			Path:   rel,
			SHA256: sum,
			Bytes:  info.Size(),
			Mode:   fmt.Sprintf("%04o", info.Mode().Perm()),
		})
		return nil
	})
	if err != nil {
		return nil, diagnostic.New(diagnostic.Environment, "release.collect-failed",
			"无法生成发行清单").WithDetail(err.Error())
	}
	sort.Slice(m.Files, func(i, j int) bool { return m.Files[i].Path < m.Files[j].Path })
	if err := validateManifest(&m); err != nil {
		return nil, err
	}
	return &m, nil
}

// Write 把清单写进发行根。清单本身不在 Files 中。
func Write(root string, m *Manifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return diagnostic.New(diagnostic.Internal, "release.write-failed", "无法序列化发行清单").WithDetail(err.Error())
	}
	data = append(data, '\n')
	path := filepath.Join(root, ManifestName)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return diagnostic.New(diagnostic.Environment, "release.write-failed", "无法写入发行清单 %s", path).WithDetail(err.Error())
	}
	return nil
}

// Verify 读取清单并逐个复核文件的大小、哈希和权限。
func Verify(root string) error {
	m, err := Load(root)
	if err != nil {
		return err
	}
	for _, f := range m.Files {
		path := filepath.Join(root, filepath.FromSlash(f.Path))
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			return diagnostic.New(diagnostic.Environment, "release.missing",
				"发行包缺少 %s；目录不完整，请重新打包", f.Path).
				WithStage(diagnostic.StageToolchain)
		}
		if err != nil {
			return diagnostic.New(diagnostic.Environment, "release.unreadable",
				"无法读取发行文件 %s", f.Path).WithDetail(err.Error()).
				WithStage(diagnostic.StageToolchain)
		}
		if !info.Mode().IsRegular() {
			return mismatch(f.Path, "不是普通文件")
		}
		if info.Size() != f.Bytes {
			return mismatch(f.Path, fmt.Sprintf("大小是 %d，清单记录 %d", info.Size(), f.Bytes))
		}
		if mode := fmt.Sprintf("%04o", info.Mode().Perm()); mode != f.Mode {
			return mismatch(f.Path, fmt.Sprintf("权限是 %s，清单记录 %s", mode, f.Mode))
		}
		sum, err := hashFile(path)
		if err != nil {
			return diagnostic.New(diagnostic.Environment, "release.unreadable",
				"无法读取发行文件 %s", f.Path).WithDetail(err.Error()).
				WithStage(diagnostic.StageToolchain)
		}
		if sum != f.SHA256 {
			return mismatch(f.Path, fmt.Sprintf("sha256 是 %s，清单记录 %s", short(sum), short(f.SHA256)))
		}
	}
	return nil
}

func mismatch(path, reason string) error {
	return diagnostic.New(diagnostic.Environment, "release.mismatch",
		"发行文件 %s 与清单不符：%s", path, reason).
		WithStage(diagnostic.StageToolchain)
}

var (
	verified sync.Map // map[string]bool
	verifyMu sync.Mutex
)

// VerifyOnce 在进程内缓存同一发行根的校验成功结果。构建会多次经过这里，
// 不必对同一个几百 MB 的目录重复哈希；失败不缓存，修正后可以立即重试。
func VerifyOnce(root string) error {
	abs, err := filepath.Abs(root)
	if err != nil {
		return diagnostic.New(diagnostic.Environment, "release.invalid", "无法解析发行根 %s", root).WithDetail(err.Error())
	}
	if v, ok := verified.Load(abs); ok && v.(bool) {
		return nil
	}
	verifyMu.Lock()
	defer verifyMu.Unlock()
	if v, ok := verified.Load(abs); ok && v.(bool) {
		return nil
	}
	if err := Verify(abs); err != nil {
		return err
	}
	verified.Store(abs, true)
	return nil
}

// VerifyIfSealed 对发行目录做完整性检查；普通开发目录没有清单，直接通过。
func VerifyIfSealed(root string) error {
	sealed, err := IsSealed(root)
	if err != nil || !sealed {
		return err
	}
	return VerifyOnce(root)
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func short(sum string) string {
	if len(sum) > 12 {
		return sum[:12]
	}
	return sum
}
