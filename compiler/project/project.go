// Package project 把一份 TypeScript 项目固定在某个根目录下，并把入口和显式
// 相对导入解析成文件身份。
//
// 解析只做三件事：确认路径在许可根内、按大小写精确地找到文件、拒绝任何需要
// 猜测的写法。它不读 tsconfig、不解析裸模块、不尝试补 .ts 后缀，也不在失败
// 后到别处搜索。文件身份由项目内相对路径和内容哈希组成，所以同一个根从不同
// 工作目录打开时，解析结果一致。
package project

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
	"github.com/Mahoo12138/tynel/compiler/source"
)

// Root 是一个已经规范化的项目根。它只接受目录。
type Root struct {
	path string
}

// File 是解析结果。Path 是项目内相对路径（斜杠分隔），Abs 是规范化绝对路径，
// SHA256 是解析时读到的内容哈希。Bytes 只在本次解析内有效，调用方不应修改。
type File struct {
	Path   string
	Abs    string
	SHA256 string
	Bytes  []byte
}

// Identity 返回稳定的文件身份：相对路径加内容哈希。
func (f File) Identity() string { return f.Path + "@" + f.SHA256 }

// Snapshot 用解析时读到的字节建立源码快照。快照的 Path 使用绝对路径，
// 诊断因此不依赖当前工作目录。
func (f File) Snapshot(limits source.Limits) (*source.Snapshot, error) {
	return source.New(f.Abs, f.Bytes, limits)
}

// Open 把 dir 规范化为项目根。相对路径按当前工作目录解释，随后解析符号链接，
// 保证同一个目录从不同工作目录打开得到同一个根。
func Open(dir string) (*Root, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, diagnostic.New(diagnostic.Usage, "project.no-root", "项目根不能为空")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, diagnostic.New(diagnostic.Environment, "project.root-invalid", "无法解析项目根 %s", dir).WithDetail(err.Error())
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, diagnostic.New(diagnostic.Environment, "project.root-missing", "项目根 %s 不存在或不可访问", dir).WithDetail(err.Error())
	}
	info, err := os.Stat(real)
	if err != nil {
		return nil, diagnostic.New(diagnostic.Environment, "project.root-missing", "项目根 %s 不可访问", dir).WithDetail(err.Error())
	}
	if !info.IsDir() {
		return nil, diagnostic.New(diagnostic.Environment, "project.root-not-dir", "项目根 %s 不是目录", dir)
	}
	return &Root{path: filepath.Clean(real)}, nil
}

// Path 返回规范化的绝对根路径。
func (r *Root) Path() string { return r.path }

// ResolveEntry 解析入口。name 可以是项目内相对路径，也可以是许可根内的绝对
// 路径；它必须以 .ts 结尾，不补后缀，也不接受裸模块名。
func (r *Root) ResolveEntry(name string) (File, error) {
	if strings.TrimSpace(name) == "" {
		return File{}, diagnostic.New(diagnostic.Source, "project.missing-entry", "缺少入口文件")
	}
	if filepath.IsAbs(name) {
		clean := filepath.Clean(name)
		if real, err := filepath.EvalSymlinks(clean); err == nil {
			clean = real
		}
		rel, err := filepath.Rel(r.path, clean)
		if err != nil {
			return File{}, diagnostic.New(diagnostic.Source, "project.outside-root", "入口 %s 不在项目根 %s 内", name, r.path)
		}
		name = filepath.ToSlash(rel)
	}
	return r.resolve(name, "入口")
}

// ResolveImport 从 from 所在目录解析一条显式相对导入。request 必须以 ./ 或
// ../ 开头，并以 .ts 结尾；裸模块名、绝对路径和目录都不接受。
func (r *Root) ResolveImport(from File, request string) (File, error) {
	if from.Path == "" {
		return File{}, diagnostic.New(diagnostic.Source, "project.no-importer", "导入缺少来源文件")
	}
	if !within(r.path, from.Abs) {
		return File{}, diagnostic.New(diagnostic.Source, "project.outside-root", "来源文件 %s 不在项目根 %s 内", from.Path, r.path)
	}
	if strings.TrimSpace(request) == "" {
		return File{}, diagnostic.New(diagnostic.Source, "project.bad-request", "导入路径为空")
	}
	if filepath.IsAbs(request) || strings.HasPrefix(request, "/") {
		return File{}, diagnostic.New(diagnostic.Source, "project.absolute-path", "导入 %q 是绝对路径；只接受 ./ 或 ../ 开头的项目内路径", request)
	}
	if !strings.HasPrefix(request, "./") && !strings.HasPrefix(request, "../") {
		return File{}, diagnostic.New(diagnostic.Source, "project.bare-module", "导入 %q 是裸模块名；本阶段只接受显式相对 .ts 路径", request)
	}
	joined := path.Join(path.Dir(from.Path), request)
	return r.resolve(joined, "导入")
}

// resolve 把项目内相对路径解析成文件。它先按字面路径逐段精确查找，再把整条
// 路径展开成真实路径后检查许可根，最后从根目录文件描述符读取字节；任何一步
// 失败都返回明确错误，不做后备搜索。
func (r *Root) resolve(rel, what string) (File, error) {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return File{}, diagnostic.New(diagnostic.Source, "project.bad-request", "%s路径为空", what)
	}
	if strings.Contains(rel, "\\") {
		return File{}, diagnostic.New(diagnostic.Source, "project.bad-request", "%s路径 %q 含反斜杠；只接受斜杠分隔", what, rel)
	}
	clean := path.Clean(rel)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return File{}, diagnostic.New(diagnostic.Source, "project.outside-root", "%s %q 越出项目根 %s", what, rel, r.path)
	}
	if !strings.HasSuffix(clean, ".ts") {
		return File{}, diagnostic.New(diagnostic.Source, "project.non-ts", "%s %q 不是 .ts 文件", what, rel)
	}

	// 一次解析固定一个根目录文件描述符。后面的目录项读取和最终的读文件都从它
	// 出发，检查与读取之间即使有目录项被换成符号链接，也不会跟着走出许可根。
	handle, err := os.OpenRoot(r.path)
	if err != nil {
		return File{}, diagnostic.New(diagnostic.Environment, "project.io", "无法打开项目根 %s", r.path).WithDetail(err.Error())
	}
	defer handle.Close()

	abs, err := r.exactPath(handle, clean, what)
	if err != nil {
		return File{}, err
	}

	// 符号链接可能出现在任意一段，包括中间目录。只看最后一段是不是链接，会让
	// 字面路径留在根内、真实文件却在根外；因此整条路径都展开后再做边界检查。
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return File{}, diagnostic.New(diagnostic.Source, "project.missing", "%s %s 的符号链接目标不存在", what, clean).WithDetail(err.Error())
	}
	real = filepath.Clean(real)
	readRel, err := filepath.Rel(r.path, real)
	if err != nil || !within(r.path, real) {
		return File{}, diagnostic.New(diagnostic.Source, "project.outside-root", "%s %s 的符号链接指向项目根外：%s", what, clean, real)
	}
	readName := filepath.ToSlash(readRel)

	last, err := os.Lstat(abs)
	if err != nil {
		return File{}, diagnostic.New(diagnostic.Environment, "project.io", "无法读取%s %s", what, clean).WithDetail(err.Error())
	}
	info, err := os.Lstat(real)
	if err != nil {
		return File{}, diagnostic.New(diagnostic.Environment, "project.io", "无法读取%s %s", what, clean).WithDetail(err.Error())
	}
	if !info.Mode().IsRegular() {
		return File{}, diagnostic.New(diagnostic.Source, "project.not-file", "%s %s 不是普通文件", what, clean)
	}

	// 身份沿用原来的规则：最后一段是符号链接时落到真实目标，只有中间目录是
	// 链接时保持逻辑路径。前端宿主按逻辑路径解析导入，把 Abs 改成真实路径会
	// 让 tsgo 解析到的模块和项目解析出的文件对不上。
	identity := abs
	if last.Mode()&os.ModeSymlink != 0 {
		identity = real
	}
	relToRoot, err := filepath.Rel(r.path, identity)
	if err != nil || !within(r.path, identity) {
		return File{}, diagnostic.New(diagnostic.Source, "project.outside-root", "%s %s 不在项目根 %s 内", what, clean, r.path)
	}

	// 读字节用真实路径相对许可根的名字，经根目录文件描述符打开，而不是逐段
	// 跟随链接；打开后再对描述符做一次普通文件检查，收窄检查与读取之间的窗口。
	f, err := handle.Open(readName)
	if err != nil {
		// 检查之后、打开之前目录项被换掉时，根描述符会拒绝跟随越界链接；再
		// 展开一次把这种情况报成边界错误，而不是笼统的 I/O 失败。
		if now, e := filepath.EvalSymlinks(abs); e == nil && !within(r.path, now) {
			return File{}, diagnostic.New(diagnostic.Source, "project.outside-root", "%s %s 的符号链接指向项目根外：%s", what, clean, now)
		}
		return File{}, diagnostic.New(diagnostic.Environment, "project.io", "无法在许可根内读取%s %s", what, clean).WithDetail(err.Error())
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return File{}, diagnostic.New(diagnostic.Environment, "project.io", "无法读取%s %s", what, clean).WithDetail(err.Error())
	}
	if !opened.Mode().IsRegular() {
		return File{}, diagnostic.New(diagnostic.Source, "project.not-file", "%s %s 不是普通文件", what, clean)
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return File{}, diagnostic.New(diagnostic.Environment, "project.io", "无法读取%s %s", what, clean).WithDetail(err.Error())
	}
	sum := sha256.Sum256(data)
	return File{
		Path:   filepath.ToSlash(relToRoot),
		Abs:    identity,
		SHA256: hex.EncodeToString(sum[:]),
		Bytes:  data,
	}, nil
}

// exactPath 逐段检查目录项，要求大小写完全匹配。它同时识别两种情况：
// 请求的大小写与磁盘不一致，以及同一目录里存在仅大小写不同的多个条目。
// 每一段都先展开当前目录上的符号链接并检查边界，目录项则通过根目录文件
// 描述符读取，避免中途顺着目录链接列出许可根之外的内容。
func (r *Root) exactPath(handle *os.Root, rel, what string) (string, error) {
	fsys := handle.FS()
	cur := "."
	for _, part := range strings.Split(rel, "/") {
		dirAbs := filepath.Join(r.path, filepath.FromSlash(cur))
		realDir, err := filepath.EvalSymlinks(dirAbs)
		if err != nil {
			if os.IsNotExist(err) {
				return "", diagnostic.New(diagnostic.Source, "project.missing", "%s %s 不存在", what, rel)
			}
			return "", diagnostic.New(diagnostic.Environment, "project.io", "无法读取目录 %s", dirAbs).WithDetail(err.Error())
		}
		if !within(r.path, realDir) {
			return "", diagnostic.New(diagnostic.Source, "project.outside-root", "%s %s 经过的目录符号链接指向项目根外：%s", what, rel, realDir)
		}
		relDir, err := filepath.Rel(r.path, realDir)
		if err != nil {
			return "", diagnostic.New(diagnostic.Source, "project.outside-root", "%s %s 不在项目根 %s 内", what, rel, r.path)
		}
		entries, err := fs.ReadDir(fsys, filepath.ToSlash(relDir))
		if err != nil {
			if os.IsNotExist(err) {
				return "", diagnostic.New(diagnostic.Source, "project.missing", "%s %s 不存在", what, rel)
			}
			return "", diagnostic.New(diagnostic.Environment, "project.io", "无法读取目录 %s", dirAbs).WithDetail(err.Error())
		}
		var exact string
		var folded []string
		for _, entry := range entries {
			if entry.Name() == part {
				exact = entry.Name()
			}
			if strings.EqualFold(entry.Name(), part) {
				folded = append(folded, entry.Name())
			}
		}
		if exact == "" {
			if len(folded) > 0 {
				return "", diagnostic.New(diagnostic.Source, "project.case-conflict", "%s %s 的大小写与磁盘不一致：磁盘上是 %s", what, rel, strings.Join(folded, "、"))
			}
			return "", diagnostic.New(diagnostic.Source, "project.missing", "%s %s 不存在", what, rel)
		}
		if len(folded) > 1 {
			return "", diagnostic.New(diagnostic.Source, "project.case-conflict", "%s %s 存在仅大小写不同的条目：%s", what, rel, strings.Join(folded, "、"))
		}
		cur = path.Join(cur, exact)
	}
	return filepath.Join(r.path, filepath.FromSlash(cur)), nil
}

// within 报告 target 是否在 root 内（含 root 本身）。两侧都应是规范化绝对路径。
func within(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func (f File) String() string {
	short := f.SHA256
	if len(short) > 12 {
		short = short[:12]
	}
	return fmt.Sprintf("%s (%s)", f.Path, short)
}
