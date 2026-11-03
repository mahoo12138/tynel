// Package diagnostic 定义 Tynel 的失败类别、稳定代码和退出状态。
//
// 类别是编译器与命令行之间唯一的退出码来源：调用方按类别取退出状态，
// 不再各自约定数字。四类失败必须分开，否则「编译器没准备好」会被误读成
// 「用户代码有错」，而「这个写法暂不支持」又会被误读成语法错误。
package diagnostic

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Category 是失败的类别。类别的顺序即优先级的语义顺序，不要随意调整。
type Category int

const (
	// Source 表示源码本身不合法：语法错误、类型错误、未定义名称。
	Source Category = iota + 1
	// Usage 表示命令用法不对：未知命令、缺少参数、多余的参数。
	Usage
	// Capability 表示写法合法，但当前原生后端尚未实现该能力。
	Capability
	// Environment 表示编译器自身可用，但依赖不满足：缺工具、版本不符、目录不可用。
	Environment
	// Internal 表示编译器或它调用的进程出现不该出现的故障。
	Internal
)

// 退出状态与类别一一对应。0 表示成功，其余值由类别决定。
const (
	ExitOK          = 0
	ExitSource      = 1
	ExitUsage       = 2
	ExitCapability  = 3
	ExitEnvironment = 4
	ExitInternal    = 5
)

// String 返回用于诊断文本的中文类别名。
func (c Category) String() string {
	switch c {
	case Source:
		return "源码错误"
	case Usage:
		return "用法错误"
	case Capability:
		return "能力限制"
	case Environment:
		return "环境缺失"
	case Internal:
		return "内部故障"
	default:
		return "未知类别"
	}
}

// ExitCode 返回该类别的进程退出状态。
func (c Category) ExitCode() int {
	switch c {
	case Source:
		return ExitSource
	case Usage:
		return ExitUsage
	case Capability:
		return ExitCapability
	case Environment:
		return ExitEnvironment
	default:
		return ExitInternal
	}
}

// Key 返回稳定的机器可读类别名。中文名只用于人类输出，不参与脚本匹配。
func (c Category) Key() string {
	switch c {
	case Source:
		return "source"
	case Usage:
		return "usage"
	case Capability:
		return "capability"
	case Environment:
		return "environment"
	case Internal:
		return "internal"
	default:
		return "internal"
	}
}

// 阶段名是稳定的机器字段，描述失败发生在编译流程的哪一段。它不等于类别：
// 同一个前端阶段可以产生源码错误、能力限制或环境缺失。
const (
	StageCLI        = "cli"
	StageProject    = "project"
	StageSource     = "source"
	StageGraph      = "graph"
	StageModules    = "modules"
	StageCapability = "capability"
	StageFrontend   = "frontend"
	StageLower      = "lower"
	StageNIR        = "nir"
	StageBuild      = "build"
	StageBackend    = "backend"
	StagePublish    = "publish"
	StageRun        = "run"
	StageToolchain  = "toolchain"
	StageTarget     = "target"
	StageInternal   = "internal"
)

// Diagnostic 是一条可展示的失败。Code 是稳定标识，供脚本匹配；Message 是
// 面向使用者的中文说明；Stage 说明发生在编译流程的哪一段；Detail 保存外部
// 进程输出等原始原因，不做改写。
type Diagnostic struct {
	Category Category
	Code     string
	Stage    string
	Message  string
	Severity string
	File     string
	Line     int
	Column   int
	Detail   string
}

// New 构造一条诊断。format 与 args 的用法同 fmt.Sprintf。阶段默认从错误码
// 前缀推导，调用方可以在跨进程边界用 WithStage 覆盖。
func New(category Category, code, format string, args ...any) *Diagnostic {
	return &Diagnostic{Category: category, Code: code, Stage: stageForCode(code), Message: fmt.Sprintf(format, args...)}
}

// WithStage 覆盖从错误码推导出的阶段。空值不覆盖。
func (d *Diagnostic) WithStage(stage string) *Diagnostic {
	if stage != "" {
		d.Stage = stage
	}
	return d
}

// WithSeverity 补充 TypeScript 的 error/warning 级别；环境与工具诊断没有
// 这个字段，保持为空。
func (d *Diagnostic) WithSeverity(severity string) *Diagnostic {
	d.Severity = severity
	return d
}

// stageForCode 让已有错误码不必逐条改写就能带上阶段。前缀是诊断协议的一部分；
// 新增错误码时沿用同一组前缀即可。
func stageForCode(code string) string {
	prefix, _, _ := strings.Cut(code, ".")
	switch prefix {
	case "cli":
		return StageCLI
	case "project":
		return StageProject
	case "source":
		return StageSource
	case "graph":
		return StageGraph
	case "module", "modules", "init", "merge":
		return StageModules
	case "cap":
		return StageCapability
	case "facts", "frontend", "host":
		return StageFrontend
	case "lower":
		return StageLower
	case "nir":
		return StageNIR
	case "build":
		return StageBuild
	case "tool":
		return StageToolchain
	case "lock", "toolchain":
		return StageToolchain
	case "run":
		return StageRun
	case "target":
		return StageTarget
	case "internal":
		return StageInternal
	default:
		return StageInternal
	}
}

// At 补充源码位置。
func (d *Diagnostic) At(file string, line, column int) *Diagnostic {
	d.File = file
	d.Line = line
	d.Column = column
	return d
}

// WithDetail 附加原始原因，例如外部进程的 stderr。
func (d *Diagnostic) WithDetail(detail string) *Diagnostic {
	d.Detail = strings.TrimRight(detail, "\n")
	return d
}

// Error 让诊断满足 error 接口。它只用于传递，展示请用 WriteTo。
func (d *Diagnostic) Error() string {
	if d.Code == "" {
		return fmt.Sprintf("%s: %s", d.Category, d.Message)
	}
	return fmt.Sprintf("%s (%s): %s", d.Category, d.Code, d.Message)
}

// ExitCode 返回这条诊断对应的进程退出状态。
func (d *Diagnostic) ExitCode() int { return d.Category.ExitCode() }

// WriteTo 按统一格式输出诊断：首行给类别与代码，位置和原因各占一行。
func (d *Diagnostic) WriteTo(w io.Writer) (int64, error) {
	var b strings.Builder
	b.WriteString("tynel: ")
	b.WriteString(d.Category.String())
	if d.Code != "" {
		b.WriteString(" (")
		b.WriteString(d.Code)
		b.WriteString(")")
	}
	b.WriteString(": ")
	b.WriteString(d.Message)
	if d.Stage != "" {
		b.WriteString("\n  阶段: ")
		b.WriteString(d.Stage)
	}
	if d.File != "" {
		b.WriteString("\n  位置: ")
		b.WriteString(d.File)
		if d.Line > 0 {
			fmt.Fprintf(&b, ":%d", d.Line)
			if d.Column > 0 {
				fmt.Fprintf(&b, ":%d", d.Column)
			}
		}
	}
	if d.Detail != "" {
		b.WriteString("\n  原因: ")
		b.WriteString(strings.ReplaceAll(d.Detail, "\n", "\n    "))
	}
	b.WriteString("\n")
	n, err := io.WriteString(w, b.String())
	return int64(n), err
}

// ExitCode 把任意错误映射为退出状态：空错误为 0，诊断按类别，其余按内部故障。
func ExitCode(err error) int {
	if err == nil {
		return ExitOK
	}
	var d *Diagnostic
	if errors.As(err, &d) {
		return d.ExitCode()
	}
	return ExitInternal
}

// Write 输出错误。非诊断错误会被包成内部故障，避免静默吞掉原因。
func Write(w io.Writer, err error) error {
	if err == nil {
		return nil
	}
	d := asDiagnostic(err)
	_, writeErr := d.WriteTo(w)
	return writeErr
}

// asDiagnostic 把任意错误转换成可展示的诊断，避免两个输出路径各写一套
// 包装逻辑。
func asDiagnostic(err error) *Diagnostic {
	var d *Diagnostic
	if errors.As(err, &d) {
		return d
	}
	return New(Internal, "internal.unclassified", "%v", err)
}

// Envelope 是机器可读的诊断信封。一次失败只有一个信封，内部可以有多条
// 源码或能力项；人类输出按同样顺序逐条打印。
type Envelope struct {
	SchemaVersion int    `json:"schemaVersion"`
	OK            bool   `json:"ok"`
	ExitCode      int    `json:"exitCode"`
	Diagnostics   []Item `json:"diagnostics"`
}

// Item 是一条机器可读诊断。Category 和 Stage 是稳定英文键，CategoryName
// 与 Message 是面向人的中文说明；位置字段没有时不出现。
type Item struct {
	Category     string `json:"category"`
	CategoryName string `json:"categoryName"`
	Code         string `json:"code,omitempty"`
	Stage        string `json:"stage"`
	Message      string `json:"message"`
	Severity     string `json:"severity,omitempty"`
	File         string `json:"file,omitempty"`
	Line         int    `json:"line,omitempty"`
	Column       int    `json:"column,omitempty"`
	Detail       string `json:"detail,omitempty"`
}

// Item 把一条诊断投影成机器可读形状。
func (d *Diagnostic) Item() Item {
	return Item{
		Category:     d.Category.Key(),
		CategoryName: d.Category.String(),
		Code:         d.Code,
		Stage:        d.Stage,
		Message:      d.Message,
		Severity:     d.Severity,
		File:         d.File,
		Line:         d.Line,
		Column:       d.Column,
		Detail:       d.Detail,
	}
}

// WriteItems 输出一个 JSON 诊断信封。退出状态决定 ok：成功路径上的警告
// 也会被报告，但信封仍然是成功的，不能只凭「有条目」就判成失败。
func WriteItems(w io.Writer, items []*Diagnostic, exitCode int) error {
	envelope := Envelope{
		SchemaVersion: 1,
		OK:            exitCode == ExitOK,
		ExitCode:      exitCode,
		Diagnostics:   make([]Item, 0, len(items)),
	}
	for _, item := range items {
		envelope.Diagnostics = append(envelope.Diagnostics, item.Item())
	}
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(envelope)
}

// WriteJSON 把任意错误写成单条 JSON 诊断。非诊断错误先按内部故障包装，
// 与人类输出的 Write 使用同一套分类。
func WriteJSON(w io.Writer, err error) error {
	if err == nil {
		return nil
	}
	d := asDiagnostic(err)
	return WriteItems(w, []*Diagnostic{d}, d.ExitCode())
}
