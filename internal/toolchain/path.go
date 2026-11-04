package toolchain

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
)

// ResolvePath 把一个工具路径固定到项目根内。
//
// 开发准备报告写的是绝对路径；发行包写相对路径，移动目录后仍然有效。
// 两种写法都必须落在 root 内：绝对路径指向开发机的旧目录时，按环境缺失
// 拒绝，而不是悄悄去读发行目录之外的工具。
func ResolvePath(root, path string) (string, error) {
	if root == "" || path == "" {
		return "", diagnostic.New(diagnostic.Environment, "toolchain.path-invalid", "工具路径不能为空").
			WithStage(diagnostic.StageToolchain)
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", diagnostic.New(diagnostic.Environment, "toolchain.path-invalid", "无法解析项目根 %s", root).WithDetail(err.Error()).
			WithStage(diagnostic.StageToolchain)
	}
	candidate := path
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(absRoot, filepath.FromSlash(path))
	}
	abs, err := filepath.Abs(candidate)
	if err != nil {
		return "", diagnostic.New(diagnostic.Environment, "toolchain.path-invalid", "无法解析工具路径 %s", path).WithDetail(err.Error()).
			WithStage(diagnostic.StageToolchain)
	}
	rel, err := filepath.Rel(absRoot, abs)
	if err != nil {
		return "", diagnostic.New(diagnostic.Environment, "toolchain.path-invalid", "无法比较工具路径 %s", path).WithDetail(err.Error()).
			WithStage(diagnostic.StageToolchain)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", diagnostic.New(diagnostic.Environment, "toolchain.path-escape",
			"工具路径 %s 在项目根 %s 之外；发行包只能使用相对路径或根内路径", path, root).
			WithStage(diagnostic.StageToolchain)
	}
	return abs, nil
}
