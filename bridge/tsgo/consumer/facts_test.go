package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mahoo12138/tynel/compiler/frontend"
	"github.com/Mahoo12138/tynel/compiler/source"
	"github.com/microsoft/typescript-go/tyneladapter"
)

// loadFacts 走一遍宿主的真实路径：读快照、开会话、取事实、关会话、解码校验。
func loadFacts(t *testing.T, path string) *frontend.Facts {
	t.Helper()
	snapshot, err := source.Read(path, source.DefaultLimits())
	if err != nil {
		t.Fatalf("读取 %s 失败：%v", path, err)
	}
	session, err := tyneladapter.Open(tyneladapter.Options{FileName: snapshot.Path(), Source: snapshot.Bytes()})
	if err != nil {
		t.Fatalf("建立会话失败：%v", err)
	}
	raw, err := session.Facts()
	if err != nil {
		t.Fatalf("读取事实失败：%v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("关闭会话失败：%v", err)
	}
	facts, err := frontend.Decode(raw)
	if err != nil {
		t.Fatalf("解码事实失败：%v", err)
	}
	return facts
}

func fixturePath(t *testing.T, name string) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "testdata", name))
	if err != nil {
		t.Fatalf("解析夹具路径失败：%v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("夹具不存在：%v", err)
	}
	return path
}

func TestFactsResolveForwardCall(t *testing.T) {
	facts := loadFacts(t, fixturePath(t, "facts.ts"))
	if len(facts.Calls) == 0 {
		t.Fatal("应至少有一条调用事实")
	}

	// 第一条调用在 add 的声明之前：函数声明提升，调用仍然要指向那个声明。
	call := facts.Calls[0]
	callee, ok := facts.Declaration(call.Callee)
	if !ok {
		t.Fatalf("调用的被调用者 ID %d 不存在", call.Callee)
	}
	if callee.Kind != frontend.DeclFunction || callee.Name != "add" {
		t.Fatalf("被调用者应是函数 add，实际 %s %q", callee.Kind, callee.Name)
	}
	if callee.Start <= call.Start {
		t.Fatalf("夹具应让 add 声明在调用之后：调用 %d，声明 %d", call.Start, callee.Start)
	}
	if len(call.Arguments) != len(callee.Parameters) {
		t.Fatalf("实参 %d 个，形参 %d 个", len(call.Arguments), len(callee.Parameters))
	}
}

func TestFactsDistinguishSameNamedParameters(t *testing.T) {
	facts := loadFacts(t, fixturePath(t, "facts.ts"))

	// add 的 a 和 scale 的 a 是两个声明，必须拿到两个不同的 ID。
	var ids []int
	for _, d := range facts.Declarations {
		if d.Kind == frontend.DeclParameter && d.Name == "a" {
			ids = append(ids, d.ID)
		}
	}
	if len(ids) != 2 {
		t.Fatalf("应有两个名为 a 的参数，实际 %d 个", len(ids))
	}
	if ids[0] == ids[1] {
		t.Fatalf("同名参数不应共用 ID：%v", ids)
	}

	// 每个 a 的引用各指各的：两处引用分别落在两个 ID 上，且各自唯一。
	seen := map[int]int{}
	for _, ref := range facts.References {
		if ref.Name == "a" {
			seen[ref.Target]++
		}
	}
	for _, id := range ids {
		if seen[id] != 1 {
			t.Fatalf("参数 ID %d 应恰好被引用一次，实际 %d 次", id, seen[id])
		}
	}
}

func TestFactsRemainReadableAfterSessionClose(t *testing.T) {
	// loadFacts 在解码之前已经关闭会话；这里再确认数据完整，并且能通过校验。
	facts := loadFacts(t, fixturePath(t, "facts.ts"))
	if facts.Session == "" {
		t.Fatal("事实应带会话标识")
	}
	if len(facts.Declarations) == 0 || len(facts.References) == 0 {
		t.Fatal("关闭会话后事实不应为空")
	}
	if err := facts.Validate(); err != nil {
		t.Fatalf("关闭会话后事实仍应自洽：%v", err)
	}
}

func TestFactsFromDifferentSessionsCannotCombine(t *testing.T) {
	path := fixturePath(t, "facts.ts")
	first := loadFacts(t, path)
	second := loadFacts(t, path)
	if first.Session == second.Session {
		t.Fatal("两次检查应有不同的会话标识")
	}
	if _, err := frontend.Combine(first, second); err == nil {
		t.Fatal("不同会话的事实不能合并")
	}
}

// TestFactsRecordParameterSemantics 确认适配层把 `?` 与初始化表达式分别记进
// 参数声明。少一个实参的 add(1) 因此能通过事实校验，而不是撞上
// facts.arg-count 内部故障。
func TestFactsRecordParameterSemantics(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "params.ts")
	src := `function add(a: number, b: number = 10, c?: number): number {
  return a;
}
console.log(add(1));
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	facts := loadFacts(t, path)

	byName := map[string]frontend.Declaration{}
	for _, d := range facts.Declarations {
		if d.Kind == frontend.DeclParameter {
			byName[d.Name] = d
		}
	}
	for _, name := range []string{"a", "b", "c"} {
		if _, ok := byName[name]; !ok {
			t.Fatalf("缺少参数 %q 的声明：%+v", name, facts.Declarations)
		}
	}
	if byName["a"].Optional || byName["a"].HasDefault {
		t.Errorf("必填参数 a 不应带语义标记：%+v", byName["a"])
	}
	if !byName["b"].HasDefault || byName["b"].Optional {
		t.Errorf("b 应只有 hasDefault：%+v", byName["b"])
	}
	if !byName["c"].Optional || byName["c"].HasDefault {
		t.Errorf("c 应只有 optional：%+v", byName["c"])
	}

	var callee *frontend.Declaration
	for i := range facts.Declarations {
		if facts.Declarations[i].Kind == frontend.DeclFunction && facts.Declarations[i].Name == "add" {
			callee = &facts.Declarations[i]
		}
	}
	if callee == nil {
		t.Fatal("没有 add 的声明")
	}
	if got, want := facts.ArityOf(callee), (frontend.Arity{Required: 1, Total: 3}); got != want {
		t.Errorf("ArityOf = %+v，期望 %+v", got, want)
	}
	for _, call := range facts.Calls {
		if call.Callee == callee.ID && !facts.ArityOf(callee).Allows(len(call.Arguments)) {
			t.Errorf("add 的 %d 个实参应被允许：%+v", len(call.Arguments), call)
		}
	}
}

// TestFactsNumericLiteralBits 确认真实前端给大数字面量产出正确的 binary64
// 位模式：超出 uint64 的非十进制、2^53 以上的舍入、上溢与下溢都不丢值。
func TestFactsNumericLiteralBits(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "numbers.ts")
	src := `const hex: number = 0x10000000000000000;
const bin: number = 0b1_0000_0000_0000_0000_0000_0000_0000_0000_0000_0000_0000_0000_0000_0000_0000_0000;
const oct: number = 0o2000000000000000000000;
const tie: number = 0x20000000000001;
const even: number = 0x20000000000003;
const over: number = 1e400;
const huge: number = 0x1` + strings.Repeat("0", 300) + `;
const under: number = 1e-400;
console.log(hex);
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	facts := loadFacts(t, path)

	want := map[string]string{
		"hex":   "0x43f0000000000000", // 2^64，超出 uint64
		"bin":   "0x43f0000000000000", // 二进制的 2^64
		"oct":   "0x43f0000000000000", // 八进制的 2^64
		"tie":   "0x4340000000000000", // 2^53+1 舍入到 2^53
		"even":  "0x4340000000000002", // 2^53+3 按最近偶数舍入到 2^53+4
		"over":  "0x7ff0000000000000", // 1e400 上溢为 +Inf
		"huge":  "0x7ff0000000000000", // 2^1200 上溢为 +Inf
		"under": "0x0000000000000000", // 1e-400 下溢为 +0
	}
	seen := map[string]bool{}
	for _, d := range facts.Declarations {
		if d.Kind != frontend.DeclConst {
			continue
		}
		bits, ok := want[d.Name]
		if !ok {
			continue
		}
		seen[d.Name] = true
		if d.Body == nil {
			t.Fatalf("%s 缺少初始化表达式", d.Name)
		}
		if d.Body.Bits != bits {
			t.Errorf("%s 的位模式 = %s，期望 %s", d.Name, d.Body.Bits, bits)
		}
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("缺少 %s 的声明事实", name)
		}
	}
}

// TestFactsRecordDependencyEdgesWithoutBindings 确认每条导入声明都产生依赖边，
// 与本地绑定分开建模：副作用导入没有绑定，但目标模块仍要先执行；纯类型导入
// 只产生类型边。
func TestFactsRecordDependencyEdgesWithoutBindings(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "main.ts")
	registerPath := filepath.Join(dir, "register.ts")
	typesPath := filepath.Join(dir, "types.ts")
	mainSource := `import "./register.ts";
import type { T } from "./types.ts";

const value: T = 1;
console.log(value);
`
	registerSource := "console.log(2);\n"
	typesSource := "export type T = number;\n"
	for path, source := range map[string]string{mainPath: mainSource, registerPath: registerSource, typesPath: typesSource} {
		if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	session, err := tyneladapter.Open(tyneladapter.Options{
		FileName: mainPath,
		Source:   []byte(mainSource),
		Files: []tyneladapter.Input{
			{FileName: mainPath, Source: []byte(mainSource)},
			{FileName: registerPath, Source: []byte(registerSource)},
			{FileName: typesPath, Source: []byte(typesSource)},
		},
	})
	if err != nil {
		t.Fatalf("建立会话失败：%v", err)
	}
	raw, err := session.Facts()
	if err != nil {
		t.Fatalf("读取事实失败：%v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("关闭会话失败：%v", err)
	}
	facts, err := frontend.Decode(raw)
	if err != nil {
		t.Fatalf("解码事实失败：%v", err)
	}

	deps := map[string]frontend.Dependency{}
	for _, dep := range facts.Dependencies {
		deps[dep.Module] = dep
	}
	register, ok := deps[registerPath]
	if !ok || register.TypeOnly {
		t.Fatalf("副作用导入应产生运行时依赖边：%+v", facts.Dependencies)
	}
	types, ok := deps[typesPath]
	if !ok || !types.TypeOnly {
		t.Fatalf("纯类型导入应产生类型依赖边：%+v", facts.Dependencies)
	}
	for _, imp := range facts.Imports {
		if imp.Module == registerPath {
			t.Fatalf("副作用导入不应产生本地绑定：%+v", imp)
		}
	}
}

// TestFactsSkipTypeAnnotationNames 确认 walk 不把类型注解里的名字记成运行时
// 引用：`const q: Quantity = 1` 只在类型空间使用 Quantity，References 里
// 不应出现它，否则 import type 会被模块阶段误判成当值使用。
func TestFactsSkipTypeAnnotationNames(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "main.ts")
	typesPath := filepath.Join(dir, "types.ts")
	mainSource := `import type { Quantity } from "./types.ts";

function display(): void {
  const q: Quantity = 1;
  console.log(q);
}

display();
`
	typesSource := "export type Quantity = number;\n"
	for path, source := range map[string]string{mainPath: mainSource, typesPath: typesSource} {
		if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	session, err := tyneladapter.Open(tyneladapter.Options{
		FileName: mainPath,
		Source:   []byte(mainSource),
		Files: []tyneladapter.Input{
			{FileName: mainPath, Source: []byte(mainSource)},
			{FileName: typesPath, Source: []byte(typesSource)},
		},
	})
	if err != nil {
		t.Fatalf("建立会话失败：%v", err)
	}
	raw, err := session.Facts()
	if err != nil {
		t.Fatalf("读取事实失败：%v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("关闭会话失败：%v", err)
	}
	facts, err := frontend.Decode(raw)
	if err != nil {
		t.Fatalf("解码事实失败：%v", err)
	}

	for _, ref := range facts.References {
		if ref.Name == "Quantity" || ref.TargetModule == typesPath {
			t.Fatalf("类型注解不应产生运行时引用：%+v", ref)
		}
	}
	typeOnly := false
	for _, imp := range facts.Imports {
		if imp.Local == "Quantity" && imp.TypeOnly {
			typeOnly = true
		}
	}
	if !typeOnly {
		t.Fatalf("import type 绑定应被记录为 type-only：%+v", facts.Imports)
	}
}

// TestFactsDescribeStructuredTypes 确认适配层不再把对象、数组、元组、函数和
// 联合类型统一压成 other。它只检查类型摘要的形状，不要求后端支持这些写法。
func TestFactsDescribeStructuredTypes(t *testing.T) {
	facts := loadFacts(t, fixturePath(t, "structured-types.ts"))
	alias := func(name string) frontend.Alias {
		t.Helper()
		for _, item := range facts.Aliases {
			if item.Name == name {
				return item
			}
		}
		t.Fatalf("缺少类型别名 %q", name)
		return frontend.Alias{}
	}

	point := alias("Point")
	if point.Type == nil || point.Type.Kind != frontend.KindObject {
		t.Fatalf("Point 应是对象类型，实际 %+v", point.Type)
	}
	properties := map[string]frontend.Property{}
	for _, prop := range point.Type.Properties {
		properties[prop.Name] = prop
	}
	for _, name := range []string{"x", "y", "label"} {
		if _, ok := properties[name]; !ok {
			t.Fatalf("Point 缺少属性 %q：%+v", name, point.Type.Properties)
		}
	}
	if properties["x"].Type.Kind != frontend.KindNumber {
		t.Errorf("Point.x 应是 number，实际 %+v", properties["x"].Type)
	}
	if !properties["label"].Optional {
		t.Errorf("Point.label 应标记为可选：%+v", properties["label"])
	}

	names := alias("Names")
	if names.Type == nil || names.Type.Kind != frontend.KindArray || names.Type.Element == nil {
		t.Fatalf("Names 应是元素类型已知的数组，实际 %+v", names.Type)
	}
	if names.Type.Element.Kind != frontend.KindString {
		t.Errorf("Names 的元素应是 string，实际 %+v", names.Type.Element)
	}

	pair := alias("Pair")
	if pair.Type == nil || pair.Type.Kind != frontend.KindTuple || len(pair.Type.Elements) != 2 {
		t.Fatalf("Pair 应是二元组，实际 %+v", pair.Type)
	}
	if pair.Type.Elements[1].Kind != frontend.KindBoolean {
		t.Errorf("Pair 的第二个元素应是 boolean，实际 %+v", pair.Type.Elements[1])
	}

	handler := alias("Handler")
	if handler.Type == nil || handler.Type.Kind != frontend.KindFunction {
		t.Fatalf("Handler 应是函数类型，实际 %+v", handler.Type)
	}
	if len(handler.Type.Params) != 2 || handler.Type.Result == nil || handler.Type.Result.Kind != frontend.KindBoolean {
		t.Errorf("Handler 的签名不完整：%+v", handler.Type)
	}

	mixed := alias("Mixed")
	if mixed.Type == nil || mixed.Type.Kind != frontend.KindUnion || len(mixed.Type.Members) != 2 {
		t.Fatalf("Mixed 应是二元联合，实际 %+v", mixed.Type)
	}
}
