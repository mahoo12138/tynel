package nir

import (
	"errors"
	"strings"
	"testing"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
)

func debugPos(line, column int) *Pos {
	return &Pos{File: "debug.ts", Line: line, Column: column}
}

func debugModule() *Module {
	m := module("main",
		fn("add", TypeNumber, []string{"a", "b"}, ret(binary("+", param("a"), param("b")))),
		mainWith(printStmt(call("add", num(1), num(2)))),
	)
	m.Debug = &DebugInfo{Functions: []DebugFunction{{
		Function: "add",
		Scopes: []DebugScope{{
			ID: 1, Parent: 0, Path: "function", Kind: DebugScopeFunction,
			Start: debugPos(1, 1), End: debugPos(3, 2),
		}},
		Locals: []DebugLocal{
			{Name: "a", SourceName: "a", Kind: DebugLocalParameter, Type: TypeNumber, Scope: 1, Pos: debugPos(1, 15)},
			{Name: "b", SourceName: "b", Kind: DebugLocalParameter, Type: TypeNumber, Scope: 1, Pos: debugPos(1, 28)},
		},
	}}}
	return m
}

func debugCode(err error) string {
	var d *diagnostic.Diagnostic
	if errors.As(err, &d) {
		return d.Code
	}
	return ""
}

func TestVerifyAcceptsDebugInfo(t *testing.T) {
	if err := Verify(debugModule()); err != nil {
		t.Fatalf("合法调试段应通过验证：%v", err)
	}
}

func TestVerifyRejectsInvalidDebugInfo(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Module)
		code   string
	}{
		{"未知函数", func(m *Module) { m.Debug.Functions[0].Function = "missing" }, "nir.debug-shape"},
		{"重复函数", func(m *Module) { m.Debug.Functions = append(m.Debug.Functions, m.Debug.Functions[0]) }, "nir.debug-shape"},
		{"没有作用域", func(m *Module) { m.Debug.Functions[0].Scopes = nil }, "nir.debug-shape"},
		{"重复作用域 ID", func(m *Module) {
			m.Debug.Functions[0].Scopes = append(m.Debug.Functions[0].Scopes, m.Debug.Functions[0].Scopes[0])
		}, "nir.debug-shape"},
		{"根作用域 ID 不是 1", func(m *Module) { m.Debug.Functions[0].Scopes[0].ID = 2 }, "nir.debug-shape"},
		{"根作用域 path 不对", func(m *Module) { m.Debug.Functions[0].Scopes[0].Path = "root" }, "nir.debug-shape"},
		{"缺少根作用域", func(m *Module) { m.Debug.Functions[0].Scopes[0].Parent = 1 }, "nir.debug-shape"},
		{"父作用域不存在", func(m *Module) {
			m.Debug.Functions[0].Scopes = append(m.Debug.Functions[0].Scopes, DebugScope{
				ID: 2, Parent: 9, Path: "function/branch#1", Kind: DebugScopeBranch,
				Start: debugPos(2, 3), End: debugPos(2, 4),
			})
		}, "nir.debug-shape"},
		{"父作用域 ID 更大", func(m *Module) {
			m.Debug.Functions[0].Scopes = append(m.Debug.Functions[0].Scopes, DebugScope{
				ID: 2, Parent: 2, Path: "function/branch#1", Kind: DebugScopeBranch,
				Start: debugPos(2, 3), End: debugPos(2, 4),
			})
		}, "nir.debug-shape"},
		{"子作用域 path 不接父路径", func(m *Module) {
			m.Debug.Functions[0].Scopes = append(m.Debug.Functions[0].Scopes, DebugScope{
				ID: 2, Parent: 1, Path: "other/branch#1", Kind: DebugScopeBranch,
				Start: debugPos(2, 3), End: debugPos(2, 4),
			})
		}, "nir.debug-shape"},
		{"重复 path", func(m *Module) {
			m.Debug.Functions[0].Scopes = append(m.Debug.Functions[0].Scopes, DebugScope{
				ID: 2, Parent: 1, Path: "function", Kind: DebugScopeBranch,
				Start: debugPos(2, 3), End: debugPos(2, 4),
			})
		}, "nir.debug-shape"},
		{"未知作用域种类", func(m *Module) { m.Debug.Functions[0].Scopes[0].Kind = "block" }, "nir.debug-shape"},
		{"缺少范围", func(m *Module) { m.Debug.Functions[0].Scopes[0].End = nil }, "nir.debug-span"},
		{"范围跨文件", func(m *Module) { m.Debug.Functions[0].Scopes[0].End.File = "other.ts" }, "nir.debug-span"},
		{"范围倒置", func(m *Module) { m.Debug.Functions[0].Scopes[0].End = debugPos(0, 1) }, "nir.debug-span"},
		{"局部变量缺少名字", func(m *Module) { m.Debug.Functions[0].Locals[0].Name = "" }, "nir.debug-local"},
		{"局部变量作用域不存在", func(m *Module) { m.Debug.Functions[0].Locals[0].Scope = 9 }, "nir.debug-local"},
		{"局部变量种类未知", func(m *Module) { m.Debug.Functions[0].Locals[0].Kind = "field" }, "nir.debug-local"},
		{"局部变量类型未知", func(m *Module) { m.Debug.Functions[0].Locals[0].Type = TypeVoid }, "nir.debug-local"},
		{"临时值带源码名", func(m *Module) {
			m.Debug.Functions[0].Locals[0].Kind = DebugLocalTemporary
		}, "nir.debug-local"},
		{"非临时值缺少源码名", func(m *Module) { m.Debug.Functions[0].Locals[0].SourceName = "" }, "nir.debug-local"},
		{"参数不在根作用域", func(m *Module) {
			m.Debug.Functions[0].Scopes = append(m.Debug.Functions[0].Scopes, DebugScope{
				ID: 2, Parent: 1, Path: "function/branch#1", Kind: DebugScopeBranch,
				Start: debugPos(2, 3), End: debugPos(2, 4),
			})
			m.Debug.Functions[0].Locals[0].Scope = 2
		}, "nir.debug-local"},
		{"同作用域重名", func(m *Module) { m.Debug.Functions[0].Locals[1].Name = "a" }, "nir.debug-local"},
		{"局部变量位置非法", func(m *Module) { m.Debug.Functions[0].Locals[0].Pos.Line = 0 }, "nir.debug-span"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := debugModule()
			c.mutate(m)
			err := Verify(m)
			if err == nil {
				t.Fatal("坏调试段应被拒绝")
			}
			if got := debugCode(err); got != c.code {
				t.Fatalf("错误码应为 %s，实际 %s（%v）", c.code, got, err)
			}
		})
	}
}

// TestDebugInfoRoundTrip 确认调试段能穿过 JSON 编解码，字段名由同一组结构
// 定义约束。
func TestDebugInfoRoundTrip(t *testing.T) {
	m := debugModule()
	data, err := EncodeBytes(m)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"debug"`) {
		t.Fatal("编码结果应包含 debug 段")
	}
	decoded, err := DecodeBytes(data, DefaultLimits())
	if err != nil {
		t.Fatalf("解码调试段失败：%v", err)
	}
	if !equalModules(m, decoded) {
		t.Fatal("调试段编解码后结构不一致")
	}
	if err := Verify(decoded); err != nil {
		t.Fatalf("回读的调试段应通过验证：%v", err)
	}
}
