package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Mahoo12138/tynel/compiler/capability"
	"github.com/Mahoo12138/tynel/compiler/lower"
	"github.com/Mahoo12138/tynel/compiler/nir"
	"github.com/Mahoo12138/tynel/compiler/source"
)

// lowerFixture 走完整前半程：源码检查、能力检查、降低。
func lowerFixture(t *testing.T, path string) (*source.Snapshot, *nir.Module) {
	t.Helper()
	snapshot, err := source.Read(path, source.DefaultLimits())
	if err != nil {
		t.Fatalf("读取 %s 失败：%v", path, err)
	}
	diagnostics, err := check(snapshot)
	if err != nil {
		t.Fatalf("源码检查失败：%v", err)
	}
	for _, d := range diagnostics {
		if d.Severity == source.SeverityError {
			t.Fatalf("%s 应先通过类型检查：%s", path, d)
		}
	}
	facts, err := factsOf(snapshot)
	if err != nil {
		t.Fatalf("读取事实失败：%v", err)
	}
	findings, err := capability.Check(snapshot, facts)
	if err != nil {
		t.Fatalf("能力检查失败：%v", err)
	}
	if len(findings) > 0 {
		t.Fatalf("%s 应通过能力检查：%v", path, findings)
	}
	module, err := lower.Lower(snapshot, facts)
	if err != nil {
		t.Fatalf("降低失败：%v", err)
	}
	return snapshot, module
}

func lowerDebugFunction(t *testing.T, module *nir.Module, name string) *nir.DebugFunction {
	t.Helper()
	if module.Debug == nil {
		t.Fatal("降低结果应包含 debug 段")
	}
	for i := range module.Debug.Functions {
		if module.Debug.Functions[i].Function == name {
			return &module.Debug.Functions[i]
		}
	}
	t.Fatalf("缺少函数 %q 的调试信息：%+v", name, module.Debug.Functions)
	return nil
}

func debugScopeByPath(fn *nir.DebugFunction) map[string]nir.DebugScope {
	out := make(map[string]nir.DebugScope, len(fn.Scopes))
	for _, scope := range fn.Scopes {
		out[scope.Path] = scope
	}
	return out
}

func debugScopePathByID(fn *nir.DebugFunction, id int) string {
	for _, scope := range fn.Scopes {
		if scope.ID == id {
			return scope.Path
		}
	}
	return ""
}

func TestLowerAdd(t *testing.T) {
	_, module := lowerFixture(t, fixturePath(t, "add.ts"))
	if module.Entry != "main" || len(module.Functions) != 2 {
		t.Fatalf("模块形状不对：entry=%q functions=%d", module.Entry, len(module.Functions))
	}
	if module.Functions[0].Name != "add" || module.Functions[1].Name != "main" {
		t.Fatalf("函数顺序应为 add、main：%+v", module.Functions)
	}
	main := module.Functions[1]
	if len(main.Body) != 3 {
		t.Fatalf("main 应有 let、print、return 三条语句，实际 %d", len(main.Body))
	}
	if main.Body[0].Op != nir.OpLet || main.Body[0].Name != "answer" {
		t.Fatalf("第一条应为 let answer：%+v", main.Body[0])
	}
	if main.Body[1].Op != nir.OpPrint || main.Body[1].Value.Op != nir.OpLocal {
		t.Fatalf("第二条应为 print local：%+v", main.Body[1])
	}
}

func TestLowerNegatedZero(t *testing.T) {
	_, module := lowerFixture(t, fixturePath(t, "lower/negzero.ts"))
	print := module.Functions[0].Body[0]
	if print.Op != nir.OpPrint {
		t.Fatalf("第一条应为 print：%+v", print)
	}
	if print.Value.Bits != "0x8000000000000000" {
		t.Fatalf("负零应折成 0x8000000000000000，实际 %q", print.Value.Bits)
	}
}

func TestLowerForwardCall(t *testing.T) {
	_, module := lowerFixture(t, fixturePath(t, "lower/forward.ts"))
	// 函数按声明顺序输出：add 在前、main 在后；main 里的调用指向 add。
	if module.Functions[0].Name != "add" {
		t.Fatalf("第一个函数应为 add：%+v", module.Functions[0])
	}
	call := module.Functions[1].Body[0].Value
	if call.Op != nir.OpCall || call.Function != "add" {
		t.Fatalf("调用应指向 add：%+v", call)
	}
}

func TestLowerEvaluationOrder(t *testing.T) {
	_, module := lowerFixture(t, fixturePath(t, "lower/order.ts"))
	call := module.Functions[len(module.Functions)-1].Body[0].Value
	if call.Op != nir.OpCall || call.Function != "add" || len(call.Args) != 2 {
		t.Fatalf("外层应为 add 调用：%+v", call)
	}
	for i, want := range []string{"log", "log"} {
		if call.Args[i].Op != nir.OpCall || call.Args[i].Function != want {
			t.Fatalf("第 %d 个实参应为 %s 调用：%+v", i+1, want, call.Args[i])
		}
	}
}

func TestLowerSameNamedParameters(t *testing.T) {
	_, module := lowerFixture(t, fixturePath(t, "lower/same-name.ts"))
	first, second := module.Functions[0], module.Functions[1]
	if first.Name != "first" || second.Name != "second" {
		t.Fatalf("函数顺序不对：%+v", module.Functions)
	}
	// 两个函数的参数都叫 a，但各自引用自己的参数：NIR 里是同一个名字，作用域
	// 按函数分开，所以不会串。
	for _, fn := range []nir.Function{first, second} {
		if len(fn.Params) != 1 || fn.Params[0].Name != "a" {
			t.Fatalf("函数 %q 的参数不对：%+v", fn.Name, fn.Params)
		}
		param := fn.Body[0].Value.Left
		if param.Op != nir.OpParam || param.Name != "a" {
			t.Fatalf("函数 %q 应引用自己的参数 a：%+v", fn.Name, param)
		}
	}
}

// TestLowerVoidFunction 覆盖 void 函数：函数体以空 return 结束，调用点变成
// 丢弃结果的语句。
func TestLowerVoidFunction(t *testing.T) {
	_, module := lowerFixture(t, fixturePath(t, "void.ts"))
	announce := module.Functions[0]
	if announce.Name != "announce" || announce.Result != nir.TypeVoid {
		t.Fatalf("announce 应为 void 函数：%+v", announce)
	}
	if got := announce.Body[len(announce.Body)-1]; got.Op != nir.OpReturn || got.Value != nil {
		t.Fatalf("void 函数应以空 return 结束：%+v", got)
	}
	main := module.Functions[1]
	if len(main.Body) != 3 {
		t.Fatalf("main 应有两条调用语句加一条 return，实际 %d：%+v", len(main.Body), main.Body)
	}
	for i := 0; i < 2; i++ {
		if got := main.Body[i]; got.Op != nir.OpDiscard || got.Value.Op != nir.OpCall || got.Value.Function != "announce" {
			t.Fatalf("第 %d 条应为 discard 调用 announce：%+v", i+1, got)
		}
	}
}

// TestLowerDiscardedExpressions 确认 `answer;` 与 `add(1, 2);` 都只是求值，
// 不会变成 print。
func TestLowerDiscardedExpressions(t *testing.T) {
	_, module := lowerFixture(t, fixturePath(t, "discard.ts"))
	main := module.Functions[len(module.Functions)-1]
	if len(main.Body) != 5 {
		t.Fatalf("main 应有 5 条语句，实际 %d：%+v", len(main.Body), main.Body)
	}
	if got := main.Body[1]; got.Op != nir.OpDiscard || got.Value.Op != nir.OpLocal || got.Value.Name != "answer" {
		t.Fatalf("第二条应为 discard 局部值 answer：%+v", got)
	}
	if got := main.Body[2]; got.Op != nir.OpDiscard || got.Value.Op != nir.OpCall || got.Value.Function != "add" {
		t.Fatalf("第三条应为 discard 调用 add：%+v", got)
	}
	for _, stmt := range main.Body {
		if stmt.Op == nir.OpPrint && stmt.Value.Op != nir.OpLocal {
			t.Fatalf("只有最后一行 console.log(answer) 应当打印：%+v", stmt)
		}
	}
}

// TestLowerOutputProgram 确认输出格式夹具降低成五条 print。
func TestLowerOutputProgram(t *testing.T) {
	_, module := lowerFixture(t, fixturePath(t, "output.ts"))
	main := module.Functions[0]
	prints := 0
	for _, stmt := range main.Body {
		if stmt.Op == nir.OpPrint {
			prints++
		}
	}
	if prints != 5 {
		t.Fatalf("应有 5 条 print，实际 %d：%+v", prints, main.Body)
	}
}

func TestLoweredModulesVerify(t *testing.T) {
	for _, name := range []string{
		"add.ts", "void.ts", "discard.ts", "output.ts",
		"lower/order.ts", "lower/negzero.ts", "lower/forward.ts", "lower/same-name.ts", "lower/scopes.ts", "lower/scope-paths.ts",
	} {
		t.Run(name, func(t *testing.T) {
			_, module := lowerFixture(t, fixturePath(t, name))
			if err := nir.Verify(module); err != nil {
				t.Fatalf("降低结果应通过 NIR 验证：%v", err)
			}
			if _, err := nir.EncodeBytes(module); err != nil {
				t.Fatalf("降低结果应能编码：%v", err)
			}
		})
	}
}

// TestLowerEmitsDebugScopes 确认降低器把词法作用域写成稳定的作用域树：
// 函数根、while 条件准备、循环体、if 的 then / else 各有自己的 ID、path 和
// 源码范围，局部值归到正确的作用域。
func TestLowerEmitsDebugScopes(t *testing.T) {
	snapshot, module := lowerFixture(t, fixturePath(t, "lower/scopes.ts"))
	if module.Debug == nil {
		t.Fatal("降低结果应包含 debug 段")
	}
	var count *nir.DebugFunction
	for i := range module.Debug.Functions {
		if module.Debug.Functions[i].Function == "count" {
			count = &module.Debug.Functions[i]
		}
	}
	if count == nil {
		t.Fatalf("缺少 count 的调试信息：%+v", module.Debug.Functions)
	}

	byPath := map[string]nir.DebugScope{}
	byID := map[int]nir.DebugScope{}
	for _, scope := range count.Scopes {
		byPath[scope.Path] = scope
		byID[scope.ID] = scope
		if scope.Start == nil || scope.End == nil {
			t.Fatalf("作用域 %s 缺少范围", scope.Path)
		}
		for _, pos := range []*nir.Pos{scope.Start, scope.End} {
			offset, err := snapshot.Offset(source.Position{Line: pos.Line, Column: pos.Column})
			if err != nil {
				t.Fatalf("作用域 %s 的位置 %d:%d 无法回到源码：%v", scope.Path, pos.Line, pos.Column, err)
			}
			back, err := snapshot.Position(offset)
			if err != nil || back.Line != pos.Line || back.Column != pos.Column {
				t.Fatalf("作用域 %s 的位置往返失败：%v / %v", scope.Path, back, err)
			}
		}
	}
	root, ok := byPath["function"]
	if !ok || root.ID != 1 || root.Parent != 0 || root.Kind != nir.DebugScopeFunction {
		t.Fatalf("根作用域不对：%+v", root)
	}
	prelude, ok := byPath["function/prelude#1"]
	if !ok || prelude.Parent != root.ID || prelude.Kind != nir.DebugScopePrelude {
		t.Fatalf("while 条件准备作用域不对：%+v", prelude)
	}
	loop, ok := byPath["function/loop#1"]
	if !ok || loop.Parent != root.ID || loop.Kind != nir.DebugScopeLoop {
		t.Fatalf("循环体作用域不对：%+v", loop)
	}
	thenScope, ok := byPath["function/loop#1/branch#1"]
	if !ok || thenScope.Parent != loop.ID || thenScope.Kind != nir.DebugScopeBranch {
		t.Fatalf("then 作用域不对：%+v", thenScope)
	}
	elseScope, ok := byPath["function/loop#1/branch#2"]
	if !ok || elseScope.Parent != loop.ID || elseScope.Kind != nir.DebugScopeBranch {
		t.Fatalf("else 作用域不对：%+v", elseScope)
	}
	if len(count.Scopes) != 5 {
		t.Fatalf("count 应有 5 个作用域，实际 %d：%+v", len(count.Scopes), count.Scopes)
	}

	locals := map[string]nir.DebugLocal{}
	for _, local := range count.Locals {
		locals[local.SourceName] = local
		if _, ok := byID[local.Scope]; !ok {
			t.Fatalf("局部变量 %q 指向不存在的作用域 %d", local.Name, local.Scope)
		}
	}
	checks := []struct {
		name    string
		scope   int
		kind    string
		mutable bool
	}{
		{"n", root.ID, nir.DebugLocalParameter, false},
		{"total", root.ID, nir.DebugLocalLocal, true},
		{"i", root.ID, nir.DebugLocalLocal, true},
		{"x", thenScope.ID, nir.DebugLocalLocal, false},
		{"y", elseScope.ID, nir.DebugLocalLocal, false},
	}
	for _, want := range checks {
		local, ok := locals[want.name]
		if !ok {
			t.Fatalf("缺少局部变量 %q：%+v", want.name, count.Locals)
		}
		if local.Scope != want.scope || local.Kind != want.kind || local.Mutable != want.mutable {
			t.Errorf("局部变量 %q = %+v，期望 scope=%d kind=%s mutable=%t", want.name, local, want.scope, want.kind, want.mutable)
		}
		if local.Type != nir.TypeNumber {
			t.Errorf("局部变量 %q 类型应为 number，实际 %s", want.name, local.Type)
		}
	}
}

// TestLowerScopePathsComplex 用多层 else if、嵌套循环、短路/三元准备块和同名
// 遮蔽，验证作用域 path 的唯一性、父链前缀和局部变量归属。
func TestLowerScopePathsComplex(t *testing.T) {
	_, module := lowerFixture(t, fixturePath(t, "lower/scope-paths.ts"))
	if module.Debug == nil {
		t.Fatal("降低结果应包含 debug 段")
	}
	functions := map[string]*nir.DebugFunction{}
	for i := range module.Debug.Functions {
		fn := &module.Debug.Functions[i]
		functions[fn.Function] = fn
	}

	// 所有函数共同的不变量：ID 稠密、path 唯一、父链存在且以父 path 为前缀。
	for name, fn := range functions {
		if len(fn.Scopes) == 0 {
			t.Fatalf("%s 没有作用域", name)
		}
		byID := map[int]nir.DebugScope{}
		byPath := map[string]nir.DebugScope{}
		for i := range fn.Scopes {
			scope := fn.Scopes[i]
			if scope.ID != i+1 {
				t.Errorf("%s 的作用域 %d 的 id 应为 %d", name, scope.ID, i+1)
			}
			if _, ok := byID[scope.ID]; ok {
				t.Errorf("%s 的作用域 id %d 重复", name, scope.ID)
			}
			if _, ok := byPath[scope.Path]; ok {
				t.Errorf("%s 的作用域 path %q 重复", name, scope.Path)
			}
			byID[scope.ID] = scope
			byPath[scope.Path] = scope
		}
		root, ok := byPath["function"]
		if !ok || root.ID != 1 || root.Parent != 0 || root.Kind != nir.DebugScopeFunction {
			t.Fatalf("%s 的根作用域不对：%+v", name, root)
		}
		for _, scope := range fn.Scopes {
			if scope.Parent == 0 {
				continue
			}
			parent, ok := byID[scope.Parent]
			if !ok || parent.ID >= scope.ID {
				t.Errorf("%s 的作用域 %s 父链不对：parent=%d", name, scope.Path, scope.Parent)
				continue
			}
			if !strings.HasPrefix(scope.Path, parent.Path+"/") {
				t.Errorf("%s 的作用域 path %q 不以父路径 %q 开头", name, scope.Path, parent.Path)
			}
		}
	}

	checkPaths := func(name string, want map[string]string) {
		t.Helper()
		fn := functions[name]
		if fn == nil {
			t.Fatalf("缺少函数 %q 的调试信息", name)
		}
		if len(fn.Scopes) != len(want) {
			t.Fatalf("%s 应有 %d 个作用域，实际 %d：%+v", name, len(want), len(fn.Scopes), fn.Scopes)
		}
		byPath := map[string]nir.DebugScope{}
		for _, scope := range fn.Scopes {
			byPath[scope.Path] = scope
		}
		for path, parentPath := range want {
			scope, ok := byPath[path]
			if !ok {
				t.Fatalf("%s 缺少作用域 %q", name, path)
			}
			if parentPath == "" {
				if scope.Parent != 0 {
					t.Errorf("%s 的根作用域 %q 不应有父作用域", name, path)
				}
				continue
			}
			parent, ok := byPath[parentPath]
			if !ok || scope.Parent != parent.ID {
				t.Errorf("%s 的作用域 %q 父路径应为 %q，实际 parent=%d", name, path, parentPath, scope.Parent)
			}
		}
	}

	checkPaths("classify", map[string]string{
		"function":                                    "",
		"function/prelude#1":                          "function",
		"function/loop#1":                             "function",
		"function/loop#1/branch#1":                    "function/loop#1",
		"function/loop#1/branch#2":                    "function/loop#1",
		"function/loop#1/branch#2/branch#1":           "function/loop#1/branch#2",
		"function/loop#1/branch#2/branch#2":           "function/loop#1/branch#2",
		"function/loop#1/branch#2/branch#2/prelude#1": "function/loop#1/branch#2/branch#2",
		"function/loop#1/branch#2/branch#2/loop#1":    "function/loop#1/branch#2/branch#2",
		"function/loop#1/branch#2/branch#2/branch#1":  "function/loop#1/branch#2/branch#2",
	})

	choosePaths := map[string]string{"function": "", "function/branch#1": "function"}
	for i := 1; i <= 6; i++ {
		choosePaths[fmt.Sprintf("function/prelude#%d", i)] = "function"
	}
	checkPaths("choose", choosePaths)
	checkPaths("main", map[string]string{"function": ""})

	classify := functions["classify"]
	classifyLocals := map[string]nir.DebugLocal{}
	for _, local := range classify.Locals {
		classifyLocals[local.SourceName] = local
	}
	classifyWant := map[string]string{
		"n":     "function",
		"total": "function",
		"i":     "function",
		"a":     "function/loop#1/branch#1",
		"b":     "function/loop#1/branch#2/branch#1",
		"c":     "function/loop#1/branch#2/branch#2/loop#1",
		"d":     "function/loop#1/branch#2/branch#2/branch#1",
	}
	for name, path := range classifyWant {
		local, ok := classifyLocals[name]
		if !ok {
			t.Fatalf("classify 缺少局部变量 %q：%+v", name, classify.Locals)
		}
		if got := debugScopePathByID(classify, local.Scope); got != path {
			t.Errorf("classify 的 %q 应在 %q，实际 %q", name, path, got)
		}
	}
	if classifyLocals["n"].Kind != nir.DebugLocalParameter {
		t.Errorf("classify 的 n 应是参数：%+v", classifyLocals["n"])
	}
	for _, name := range []string{"total", "i"} {
		if !classifyLocals[name].Mutable {
			t.Errorf("classify 的 %q 应可变：%+v", name, classifyLocals[name])
		}
	}
	for _, name := range []string{"a", "b", "c", "d"} {
		if classifyLocals[name].Mutable {
			t.Errorf("classify 的 %q 不应可变：%+v", name, classifyLocals[name])
		}
	}

	choose := functions["choose"]
	if len(choose.Locals) == 0 {
		t.Fatal("choose 缺少局部变量")
	}
	// 同名 x 在两个作用域出现：根作用域和 if 分支；path 必须能区分它们。
	var xs []nir.DebugLocal
	temporaries := 0
	for _, local := range choose.Locals {
		if local.SourceName == "x" {
			xs = append(xs, local)
		}
		if local.Kind == nir.DebugLocalTemporary {
			temporaries++
			if local.SourceName != "" {
				t.Errorf("临时值 %q 不应有 sourceName", local.Name)
			}
		}
	}
	if len(xs) != 2 {
		t.Fatalf("choose 应有两个 x，实际 %d：%+v", len(xs), choose.Locals)
	}
	shadowPaths := map[string]bool{}
	for _, local := range xs {
		shadowPaths[debugScopePathByID(choose, local.Scope)] = true
	}
	if !shadowPaths["function"] || !shadowPaths["function/branch#1"] {
		t.Fatalf("choose 的两个 x 应分别位于根和分支：%v", shadowPaths)
	}
	if temporaries < 4 {
		t.Fatalf("choose 应至少有 4 个临时值，实际 %d：%+v", temporaries, choose.Locals)
	}
}

// TestLowerScopePathsArePerFunction 确认 path 只在函数内唯一：不同函数可以
// 各自拥有 `function`、`function/prelude#1`，但父链和 ID 必须独立。
func TestLowerScopePathsArePerFunction(t *testing.T) {
	_, module := lowerFixture(t, fixturePath(t, "lower/scope-paths.ts"))
	for _, name := range []string{"classify", "choose", "main"} {
		fn := lowerDebugFunction(t, module, name)
		for i, scope := range fn.Scopes {
			if scope.ID != i+1 {
				t.Errorf("%s 的作用域 %d 的 id 应为 %d", name, scope.ID, i+1)
			}
		}
		if fn.Scopes[0].Parent != 0 || fn.Scopes[0].Path != "function" {
			t.Errorf("%s 的根作用域不对：%+v", name, fn.Scopes[0])
		}
	}
	classify := debugScopeByPath(lowerDebugFunction(t, module, "classify"))
	choose := debugScopeByPath(lowerDebugFunction(t, module, "choose"))
	if _, ok := classify["function/loop#1"]; !ok {
		t.Error("classify 应有循环作用域")
	}
	if _, ok := classify["function/branch#1"]; ok {
		t.Error("classify 的分支应挂在循环下，不应直接挂在根下")
	}
	if _, ok := choose["function/branch#1"]; !ok {
		t.Error("choose 应有根下的 if 分支")
	}
	if _, ok := choose["function/loop#1"]; ok {
		t.Error("choose 不应有循环作用域")
	}
}

// TestLowerScopePathsSiblingNumbering 确认同级作用域按 kind 连续编号，且
// 嵌套 else if 的分支路径逐层带上父路径。
func TestLowerScopePathsSiblingNumbering(t *testing.T) {
	_, module := lowerFixture(t, fixturePath(t, "lower/scope-paths.ts"))
	classify := debugScopeByPath(lowerDebugFunction(t, module, "classify"))
	for _, path := range []string{
		"function/loop#1/branch#1",
		"function/loop#1/branch#2",
		"function/loop#1/branch#2/branch#1",
		"function/loop#1/branch#2/branch#2",
	} {
		if _, ok := classify[path]; !ok {
			t.Errorf("classify 缺少嵌套分支 %q：%+v", path, classify)
		}
	}
	choose := debugScopeByPath(lowerDebugFunction(t, module, "choose"))
	for i := 1; i <= 6; i++ {
		path := fmt.Sprintf("function/prelude#%d", i)
		scope, ok := choose[path]
		if !ok {
			t.Fatalf("choose 缺少 %q：%+v", path, choose)
		}
		if scope.Parent != 1 {
			t.Errorf("%q 应挂在根作用域，实际 parent=%d", path, scope.Parent)
		}
	}
	if _, ok := choose["function/prelude#7"]; ok {
		t.Error("choose 不应有 prelude#7")
	}
}

// TestLowerScopePathsShadowingAndTemporaries 确认同名局部值靠作用域 path 区分，
// 编译器临时值标记为 temporary 且 sourceName 为空。
func TestLowerScopePathsShadowingAndTemporaries(t *testing.T) {
	_, module := lowerFixture(t, fixturePath(t, "lower/scope-paths.ts"))
	choose := lowerDebugFunction(t, module, "choose")
	var xs []nir.DebugLocal
	temporaries := 0
	for _, local := range choose.Locals {
		if local.SourceName == "x" {
			xs = append(xs, local)
		}
		if local.Kind == nir.DebugLocalTemporary {
			temporaries++
			if local.SourceName != "" {
				t.Errorf("临时值 %q 不应有 sourceName", local.Name)
			}
			if !strings.HasPrefix(local.Name, "_tmp_") {
				t.Errorf("临时值名 %q 应以 _tmp_ 开头", local.Name)
			}
		}
	}
	if len(xs) != 2 {
		t.Fatalf("choose 应有两个 x，实际 %d：%+v", len(xs), choose.Locals)
	}
	paths := map[string]bool{}
	for _, local := range xs {
		if local.Name != "x" {
			t.Errorf("遮蔽的 NIR 名应为 x，实际 %q", local.Name)
		}
		paths[debugScopePathByID(choose, local.Scope)] = true
	}
	if !paths["function"] || !paths["function/branch#1"] {
		t.Fatalf("两个 x 应分别位于根和分支：%v", paths)
	}
	if temporaries < 4 {
		t.Fatalf("choose 应至少有 4 个临时值，实际 %d", temporaries)
	}
}

// TestLoweredPositionsMapBackToSource 确认降低器写进 NIR 的每个 pos 都能用
// source.Snapshot.Offset 回到快照里的字节偏移，再由 Position 得到同一行列。
// 这是调试信息和诊断定位依赖的稳定映射：NIR 不需要保存原始字节，但行列不能
// 指向快照之外的位置。
func TestLoweredPositionsMapBackToSource(t *testing.T) {
	snapshot, module := lowerFixture(t, fixturePath(t, "add.ts"))
	seen := 0
	check := func(pos *nir.Pos, what string) {
		if pos == nil {
			return
		}
		if pos.File != snapshot.Path() {
			t.Errorf("%s 的位置指向 %s，快照是 %s", what, pos.File, snapshot.Path())
			return
		}
		offset, err := snapshot.Offset(source.Position{Line: pos.Line, Column: pos.Column})
		if err != nil {
			t.Errorf("%s 的 %d:%d 无法回到字节偏移：%v", what, pos.Line, pos.Column, err)
			return
		}
		back, err := snapshot.Position(offset)
		if err != nil {
			t.Errorf("%s 的字节偏移 %d 无法换算回行列：%v", what, offset, err)
			return
		}
		if back.Line != pos.Line || back.Column != pos.Column {
			t.Errorf("%s 的 %d:%d 往返后变成 %d:%d", what, pos.Line, pos.Column, back.Line, back.Column)
		}
		seen++
	}

	var walkExpr func(*nir.Expr)
	var walkStmts func([]nir.Stmt)
	var walkStmt func(nir.Stmt)
	walkExpr = func(e *nir.Expr) {
		if e == nil {
			return
		}
		check(e.Pos, "表达式")
		walkExpr(e.Left)
		walkExpr(e.Right)
		walkExpr(e.Operand)
		for i := range e.Args {
			walkExpr(&e.Args[i])
		}
	}
	walkStmts = func(stmts []nir.Stmt) {
		for i := range stmts {
			walkStmt(stmts[i])
		}
	}
	walkStmt = func(s nir.Stmt) {
		check(s.Pos, "语句")
		walkExpr(s.Value)
		walkExpr(s.Cond)
		walkExpr(s.ThenValue)
		walkExpr(s.ElseValue)
		walkExpr(s.Left)
		walkStmts(s.Then)
		walkStmts(s.Else)
		walkStmts(s.Right)
		walkStmts(s.CondPre)
		walkStmts(s.Body)
	}

	for i := range module.Functions {
		fn := &module.Functions[i]
		check(fn.Pos, "函数")
		for j := range fn.Params {
			check(fn.Params[j].Pos, "参数")
		}
		walkStmts(fn.Body)
	}
	if seen == 0 {
		t.Fatal("降低结果里没有可核对的位置")
	}
}
