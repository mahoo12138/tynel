package nir

import "strings"

// 调试段的作用域和局部变量种类。它们只用于描述源码结构，不参与执行。
const (
	DebugScopeFunction = "function"
	DebugScopeBranch   = "branch"
	DebugScopeLoop     = "loop"
	DebugScopePrelude  = "prelude"
)

const (
	DebugLocalParameter   = "parameter"
	DebugLocalLocal       = "local"
	DebugLocalModuleConst = "module-const"
	DebugLocalTemporary   = "temporary"
)

// DebugInfo 是可选的调试元数据。执行核心只读 Functions，不读这里。
type DebugInfo struct {
	Functions []DebugFunction `json:"functions,omitempty"`
}

// DebugFunction 描述一个 NIR 函数的作用域树和局部变量。
type DebugFunction struct {
	Function string       `json:"function"`
	Scopes   []DebugScope `json:"scopes,omitempty"`
	Locals   []DebugLocal `json:"locals,omitempty"`
}

// DebugScope 是函数内的一个词法作用域。
type DebugScope struct {
	ID     int    `json:"id"`
	Parent int    `json:"parent"`
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Start  *Pos   `json:"start,omitempty"`
	End    *Pos   `json:"end,omitempty"`
}

// DebugLocal 是一个参数、局部值、模块常量或编译器临时值。
type DebugLocal struct {
	Name       string `json:"name"`
	SourceName string `json:"sourceName,omitempty"`
	Kind       string `json:"kind"`
	Type       string `json:"type"`
	Scope      int    `json:"scope"`
	Pos        *Pos   `json:"pos,omitempty"`
	Mutable    bool   `json:"mutable,omitempty"`
}

func validScopeKind(kind string) bool {
	switch kind {
	case DebugScopeFunction, DebugScopeBranch, DebugScopeLoop, DebugScopePrelude:
		return true
	}
	return false
}

func validLocalKind(kind string) bool {
	switch kind {
	case DebugLocalParameter, DebugLocalLocal, DebugLocalModuleConst, DebugLocalTemporary:
		return true
	}
	return false
}

// debug 校验调试段。缺失时直接通过；存在时只检查自洽性，不要求覆盖每个
// NIR let——完整生成是降低器的责任，手写 NIR 可以省略部分调试信息。
func (c *checker) debug(info *DebugInfo) error {
	if info == nil {
		return nil
	}
	seenFunction := make(map[string]bool, len(info.Functions))
	for i := range info.Functions {
		fn := &info.Functions[i]
		if fn.Function == "" {
			return badf("nir.debug-shape", "debug.functions[%d] 缺少 function", i)
		}
		if _, ok := c.funcs[fn.Function]; !ok {
			return badf("nir.debug-shape", "debug 指向不存在的函数 %q", fn.Function)
		}
		if seenFunction[fn.Function] {
			return badf("nir.debug-shape", "函数 %q 的调试信息重复", fn.Function)
		}
		seenFunction[fn.Function] = true
		if err := debugFunction(fn); err != nil {
			return err
		}
	}
	return nil
}

func debugFunction(fn *DebugFunction) error {
	if len(fn.Scopes) == 0 {
		return badf("nir.debug-shape", "函数 %q 的调试信息没有作用域", fn.Function)
	}
	scopes := make(map[int]*DebugScope, len(fn.Scopes))
	paths := make(map[string]bool, len(fn.Scopes))
	roots := 0
	for i := range fn.Scopes {
		s := &fn.Scopes[i]
		if s.ID <= 0 {
			return badf("nir.debug-shape", "函数 %q 的作用域 id 必须是正整数", fn.Function)
		}
		if _, exists := scopes[s.ID]; exists {
			return badf("nir.debug-shape", "函数 %q 的作用域 id %d 重复", fn.Function, s.ID)
		}
		scopes[s.ID] = s
		if s.Path == "" || paths[s.Path] {
			return badf("nir.debug-shape", "函数 %q 的作用域 path %q 为空或重复", fn.Function, s.Path)
		}
		paths[s.Path] = true
		if !validScopeKind(s.Kind) {
			return badf("nir.debug-shape", "函数 %q 的作用域 %d 种类 %q 不认识", fn.Function, s.ID, s.Kind)
		}
		if err := debugSpan(s); err != nil {
			return err
		}
		if s.Parent == 0 {
			roots++
			if s.ID != 1 || s.Kind != DebugScopeFunction || s.Path != "function" {
				return badf("nir.debug-shape", "函数 %q 的根作用域必须是 id 1、kind function、path function", fn.Function)
			}
		}
	}
	if roots != 1 {
		return badf("nir.debug-shape", "函数 %q 必须恰好有一个根作用域，实际 %d 个", fn.Function, roots)
	}
	for i := range fn.Scopes {
		s := &fn.Scopes[i]
		if s.Parent == 0 {
			continue
		}
		parent, ok := scopes[s.Parent]
		if !ok || s.Parent >= s.ID {
			return badf("nir.debug-shape", "函数 %q 的作用域 %d 指向无效父作用域 %d", fn.Function, s.ID, s.Parent)
		}
		if s.Kind == DebugScopeFunction {
			return badf("nir.debug-shape", "函数 %q 的非根作用域 %d 不能是 function", fn.Function, s.ID)
		}
		if !strings.HasPrefix(s.Path, parent.Path+"/") || len(s.Path) <= len(parent.Path)+1 {
			return badf("nir.debug-shape", "函数 %q 的作用域 path %q 不以父路径 %q 开头", fn.Function, s.Path, parent.Path)
		}
	}

	seenLocal := make(map[localID]bool, len(fn.Locals))
	for i := range fn.Locals {
		local := &fn.Locals[i]
		if local.Name == "" {
			return badf("nir.debug-local", "函数 %q 的局部变量缺少 name", fn.Function)
		}
		if _, ok := scopes[local.Scope]; !ok {
			return badf("nir.debug-local", "函数 %q 的局部变量 %q 指向不存在的作用域 %d", fn.Function, local.Name, local.Scope)
		}
		if !validLocalKind(local.Kind) {
			return badf("nir.debug-local", "函数 %q 的局部变量 %q 种类 %q 不认识", fn.Function, local.Name, local.Kind)
		}
		if local.Type != TypeNumber && local.Type != TypeBool {
			return badf("nir.debug-local", "函数 %q 的局部变量 %q 类型 %q 只接受 number 或 bool", fn.Function, local.Name, local.Type)
		}
		if local.Kind == DebugLocalTemporary && local.SourceName != "" {
			return badf("nir.debug-local", "函数 %q 的临时值 %q 不应带 sourceName", fn.Function, local.Name)
		}
		if local.Kind != DebugLocalTemporary && local.SourceName == "" {
			return badf("nir.debug-local", "函数 %q 的局部变量 %q 缺少 sourceName", fn.Function, local.Name)
		}
		if (local.Kind == DebugLocalParameter || local.Kind == DebugLocalModuleConst) && local.Scope != 1 {
			return badf("nir.debug-local", "函数 %q 的 %s %q 必须在根作用域", fn.Function, local.Kind, local.Name)
		}
		if local.Pos != nil && !validPos(local.Pos) {
			return badf("nir.debug-span", "函数 %q 的局部变量 %q 位置不合法", fn.Function, local.Name)
		}
		key := localID{scope: local.Scope, name: local.Name}
		if seenLocal[key] {
			return badf("nir.debug-local", "函数 %q 的作用域 %d 里 %q 重复", fn.Function, local.Scope, local.Name)
		}
		seenLocal[key] = true
	}
	return nil
}

type localID struct {
	scope int
	name  string
}

func debugSpan(s *DebugScope) error {
	if !validPos(s.Start) || !validPos(s.End) {
		return badf("nir.debug-span", "作用域 %d 的 start/end 不完整", s.ID)
	}
	if s.Start.File != s.End.File {
		return badf("nir.debug-span", "作用域 %d 的 start/end 不在同一个文件", s.ID)
	}
	if comparePos(s.Start, s.End) > 0 {
		return badf("nir.debug-span", "作用域 %d 的 end 早于 start", s.ID)
	}
	return nil
}

func validPos(p *Pos) bool {
	return p != nil && p.File != "" && p.Line > 0 && p.Column > 0
}

func comparePos(a, b *Pos) int {
	if a.Line != b.Line {
		if a.Line < b.Line {
			return -1
		}
		return 1
	}
	switch {
	case a.Column < b.Column:
		return -1
	case a.Column > b.Column:
		return 1
	}
	return 0
}
