// Package abi 固化 Tynel Runtime ABI v1 的可验证描述。
//
// 它不参与代码生成，也不是跨平台承诺：bridge 的布局选择必须与这份快照一致，
// 布局变化必须提升版本。布局快照是 ABI 的检查点，而不是从 Go 的偶然实现
// 反推出来的文档。
package abi

import (
	"encoding/json"
	"fmt"
)

const (
	// Name 是 ABI 的稳定名字，写进构建记录。
	Name = "tynel-runtime-abi"
	// Version 是当前 ABI 版本。改变值布局、指针可见性或错误边界时递增。
	Version = 1
	// Target 是 ABI v1 的验证目标；其他平台仍是计划，不是当前承诺。
	Target = "darwin/arm64"
	// DataErrorExit 是运行时数据错误的退出状态，与编译失败的 1–5 分开。
	DataErrorExit = 6
)

// VersionString 返回写进构建记录和布局快照的版本标识。
func VersionString() string { return fmt.Sprintf("%s/%d", Name, Version) }

// Field 是一个布局字段。Type 只描述 ABI，不暴露 Go 内部类型名。
type Field struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// Layout 是一类值在 bridge/运行时里的物理表示。
type Layout struct {
	Kind           string   `json:"kind"`
	Representation string   `json:"representation"`
	Fields         []Field  `json:"fields,omitempty"`
	Pointers       []string `json:"gcVisiblePointers,omitempty"`
	Notes          string   `json:"notes,omitempty"`
}

// GCContract 描述当前预览的 GC 边界。
type GCContract struct {
	Collector         string `json:"collector"`
	PointerVisibility string `json:"pointerVisibility"`
	HiddenPointers    string `json:"hiddenPointers"`
	Allocation        string `json:"allocation"`
}

// Exit 是一条进程退出状态。
type Exit struct {
	Code    int    `json:"code"`
	Meaning string `json:"meaning"`
}

// Snapshot 是 ABI v1 的机器可读布局快照。
type Snapshot struct {
	SchemaVersion int        `json:"schemaVersion"`
	Name          string     `json:"name"`
	Version       int        `json:"version"`
	Target        string     `json:"target"`
	Layouts       []Layout   `json:"valueLayouts"`
	GC            GCContract `json:"gc"`
	Exits         []Exit     `json:"errorExits"`
	CrossModule   string     `json:"crossModule"`
}

// Current 返回 ABI v1 的布局快照。字段顺序固定，便于逐字节比较。
func Current() Snapshot {
	return Snapshot{
		SchemaVersion: 1,
		Name:          Name,
		Version:       Version,
		Target:        Target,
		Layouts: []Layout{
			{
				Kind:           "number",
				Representation: "IEEE 754 binary64",
				Notes:          "NIR 保存 64 位位模式；bridge 使用 Go float64。",
			},
			{
				Kind:           "boolean",
				Representation: "8-bit boolean",
				Notes:          "不使用数字 0/1 冒充 boolean。",
			},
			{
				Kind:           "string",
				Representation: "struct { data *uint16; length int32 }",
				Fields: []Field{
					{Name: "data", Type: "*uint16"},
					{Name: "length", Type: "int32"},
				},
				Pointers: []string{"data"},
				Notes:    "UTF-16 code unit 存储；空字符串 data 为 nil、length 为 0。",
			},
			{
				Kind:           "object",
				Representation: "*struct，字段按属性名排序",
				Pointers:       []string{"指针类型的字段"},
				Notes:          "对象引用是 GC 可见指针，=== 比较分配地址。",
			},
			{
				Kind:           "array",
				Representation: "Go 切片 []T（预览后端表示）",
				Pointers:       []string{"底层数组 data"},
				Notes:          "切片头对 GC 可见；长期目标是 TynelSlice，不把 Go 切片头当公开 ABI。",
			},
			{
				Kind:           "tuple",
				Representation: "*struct，字段按元组位置排列",
				Pointers:       []string{"指针类型的字段"},
				Notes:          "元组值同样通过 GC 可见指针引用。",
			},
			{
				Kind:           "union",
				Representation: "*struct { tag int32; payload 字段 }",
				Fields: []Field{
					{Name: "tag", Type: "int32"},
					{Name: "payload", Type: "按成员类型分配"},
				},
				Pointers: []string{"payload 中的指针字段"},
				Notes:    "null / undefined 只占标签；拆箱前检查标签，不一致退出 6。",
			},
			{
				Kind:           "function",
				Representation: "Go func 值（code + environment）",
				Pointers:       []string{"闭包 environment"},
				Notes:          "当前切片无捕获，environment 为 nil；Go func 只是后端表示。",
			},
			{
				Kind:           "void",
				Representation: "没有值",
				Notes:          "只用于函数结果和调用语句。",
			},
			{
				Kind:           "null / undefined",
				Representation: "只作为联合标签，不单独分配",
				Notes:          "单独作为局部值或输出参数会被类型规则拒绝。",
			},
		},
		GC: GCContract{
			Collector:         "随产物分发的 Go GC（预览基座）",
			PointerVisibility: "对象、元组、联合 payload、数组元素和字符串 data 中的指针必须是 GC 可见指针。",
			HiddenPointers:    "不允许用 uintptr 或无类型整数保存需要保活的对象地址。",
			Allocation:        "对象、元组、联合和字符串存储由 Go 堆分配；bridge 不手写分配器。",
		},
		Exits: []Exit{
			{Code: 0, Meaning: "成功"},
			{Code: 1, Meaning: "源码错误"},
			{Code: 2, Meaning: "命令行用法错误"},
			{Code: 3, Meaning: "能力限制"},
			{Code: 4, Meaning: "环境缺失"},
			{Code: 5, Meaning: "内部故障"},
			{Code: DataErrorExit, Meaning: "运行时数据错误（索引越界、联合标签不符）"},
		},
		CrossModule: "所有模块合并成一份 NIR 和一张类型表；跨模块值使用同一布局，不按模块切换 ABI。",
	}
}

// Encode 返回稳定的缩进 JSON，末尾带一个换行。
func Encode() ([]byte, error) {
	data, err := json.MarshalIndent(Current(), "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}
