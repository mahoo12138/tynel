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
