package nir

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
)

func diagnosticCode(err error) string {
	var d *diagnostic.Diagnostic
	if errors.As(err, &d) {
		return d.Code
	}
	return ""
}

func typedModule(defs ...TypeDef) *Module {
	m := minimalModule()
	m.Types = defs
	return m
}

// TestTypeTableRoundTrip 固定 schema 3 能描述对象、数组、元组、联合和函数
// 类型，即使对应的执行操作还没有实现。编码按 ID 升序，解码后结构不变。
func TestTypeTableRoundTrip(t *testing.T) {
	defs := []TypeDef{
		{ID: 12, Kind: TypeKindFunction, Params: []int{1, 4}, Result: 2},
		{ID: 8, Kind: TypeKindObject, Name: "Point", Properties: []TypeProperty{
			{Name: "x", Type: 1},
			{Name: "label", Type: 9, Optional: true},
		}},
		{ID: 9, Kind: TypeKindUnion, Members: []int{4, 6}},
		{ID: 10, Kind: TypeKindArray, Element: 1},
		{ID: 11, Kind: TypeKindTuple, Elements: []int{1, 2}},
	}
	m := typedModule(defs...)
	m.Functions[0].Body = []Stmt{{Op: OpPrint, Value: ptrExpr(String("hello"))}}

	data, err := EncodeBytes(m)
	if err != nil {
		t.Fatalf("编码类型表失败：%v", err)
	}
	if !bytes.Contains(data, []byte(`"id": 8`)) {
		t.Fatalf("编码结果缺少类型表：%s", data)
	}
	if bytes.Index(data, []byte(`"id": 8`)) > bytes.Index(data, []byte(`"id": 12`)) {
		t.Fatalf("类型表应按 ID 升序输出：%s", data)
	}

	decoded, err := DecodeBytes(data, DefaultLimits())
	if err != nil {
		t.Fatalf("解码类型表失败：%v", err)
	}
	if len(decoded.Types) != len(defs) {
		t.Fatalf("类型表条目数 %d，期望 %d", len(decoded.Types), len(defs))
	}
	if err := Verify(decoded); err != nil {
		t.Fatalf("类型表应通过验证：%v", err)
	}
	if decoded.Types[0].ID != 8 || decoded.Types[4].ID != 12 {
		t.Fatalf("解码后的类型表顺序不对：%+v", decoded.Types)
	}
	if decoded.Types[0].Properties[1].Type != 9 || !decoded.Types[0].Properties[1].Optional {
		t.Fatalf("对象属性的类型或可选性丢失：%+v", decoded.Types[0])
	}
}

// TestRecursiveObjectTypeAllowed 确认类型图允许自引用：对象属性的类型 ID
// 可以指向对象自己，验证器只核对引用存在，不做无环假设。
func TestRecursiveObjectTypeAllowed(t *testing.T) {
	m := typedModule(TypeDef{
		ID: 8, Kind: TypeKindObject, Name: "Node",
		Properties: []TypeProperty{{Name: "next", Type: 8}},
	})
	if err := Verify(m); err != nil {
		t.Fatalf("递归对象类型应通过验证：%v", err)
	}
}

func TestTypeTableRejectsInvalidDefinitions(t *testing.T) {
	cases := []struct {
		name string
		defs []TypeDef
		code string
	}{
		{"覆盖内置 ID", []TypeDef{{ID: TypeNumber, Kind: TypeKindArray, Element: 1}}, "nir.type-id"},
		{"重复 ID", []TypeDef{
			{ID: 8, Kind: TypeKindArray, Element: 1},
			{ID: 8, Kind: TypeKindArray, Element: 2},
		}, "nir.type-id"},
		{"未知种类", []TypeDef{{ID: 8, Kind: "record"}}, "nir.type-kind"},
		{"对象属性缺名", []TypeDef{{ID: 8, Kind: TypeKindObject, Properties: []TypeProperty{{Type: 1}}}}, "nir.type-shape"},
		{"对象属性重名", []TypeDef{{ID: 8, Kind: TypeKindObject, Properties: []TypeProperty{
			{Name: "x", Type: 1}, {Name: "x", Type: 2},
		}}}, "nir.type-shape"},
		{"数组缺元素", []TypeDef{{ID: 8, Kind: TypeKindArray}}, "nir.type-shape"},
		{"元组为空", []TypeDef{{ID: 8, Kind: TypeKindTuple}}, "nir.type-shape"},
		{"联合只有一个成员", []TypeDef{{ID: 8, Kind: TypeKindUnion, Members: []int{1}}}, "nir.type-shape"},
		{"函数缺结果", []TypeDef{{ID: 8, Kind: TypeKindFunction, Params: []int{1}}}, "nir.type-shape"},
		{"引用不存在的类型", []TypeDef{{ID: 8, Kind: TypeKindArray, Element: 99}}, "nir.type-ref"},
		{"void 作为数组元素", []TypeDef{{ID: 8, Kind: TypeKindArray, Element: TypeVoid}}, "nir.type-void"},
		{"void 作为参数", []TypeDef{{ID: 8, Kind: TypeKindFunction, Params: []int{TypeVoid}, Result: 1}}, "nir.type-void"},
		{"非对象带名字", []TypeDef{{ID: 8, Kind: TypeKindArray, Name: "List", Element: 1}}, "nir.type-kind"},
		{"数组带成员", []TypeDef{{ID: 8, Kind: TypeKindArray, Element: 1, Members: []int{1, 2}}}, "nir.type-kind"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := Verify(typedModule(c.defs...))
			if err == nil {
				t.Fatal("坏类型表应被拒绝")
			}
			if got := diagnosticCode(err); got != c.code {
				t.Fatalf("错误码应为 %s，实际 %s（%v）", c.code, got, err)
			}
		})
	}
}

func TestStringOperations(t *testing.T) {
	greet := Function{
		Name:   "greet",
		Result: TypeString,
		Params: []Param{{Name: "name", Type: TypeString}},
		Body:   []Stmt{ret(Expr{Op: OpConcat, Left: ptrExpr(String("hello, ")), Right: ptrExpr(param("name"))})},
	}
	m := module("main", greet, mainWith(
		printStmt(call("greet", String("世界"))),
		printStmt(Expr{Op: OpStrlen, Operand: ptrExpr(String("😀"))}),
		printStmt(compare("===", String("a"), String("a"))),
		printStmt(compare("!==", String("a"), String("b"))),
		letStmt("empty", String("")),
		printStmt(local("empty")),
	))
	if err := Verify(m); err != nil {
		t.Fatalf("字符串模块应通过验证：%v", err)
	}

	data, err := EncodeBytes(m)
	if err != nil {
		t.Fatal(err)
	}
	back, err := DecodeBytes(data, DefaultLimits())
	if err != nil {
		t.Fatalf("字符串模块应能解码：%v", err)
	}
	if err := Verify(back); err != nil {
		t.Fatalf("回读的字符串模块应通过验证：%v", err)
	}
	value, ok := back.Functions[1].Body[0].Value.Args[0].StringValue()
	if !ok || value != "世界" {
		t.Fatalf("字符串字面量回读为 %q（ok=%v）", value, ok)
	}
	empty, ok := back.Functions[1].Body[4].Value.StringValue()
	if !ok || empty != "" {
		t.Fatalf("空字符串回读为 %q（ok=%v）", empty, ok)
	}
}

func TestStringOperationsRejectWrongOperands(t *testing.T) {
	cases := []struct {
		name string
		expr Expr
		code string
	}{
		{"concat 左操作数是 number", Expr{Op: OpConcat, Left: ptrExpr(num(1)), Right: ptrExpr(String("a"))}, "nir.operand-type"},
		{"concat 右操作数是 bool", Expr{Op: OpConcat, Left: ptrExpr(String("a")), Right: ptrExpr(Bool(true))}, "nir.operand-type"},
		{"strlen 操作数是 number", Expr{Op: OpStrlen, Operand: ptrExpr(num(1))}, "nir.operand-type"},
		{"字符串与数字严格相等", compare("===", String("a"), num(1)), "nir.operand-type"},
		{"字符串有序比较", compare("<", String("a"), String("b")), "nir.operand-type"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := minimalModule()
			m.Functions[0].Body = []Stmt{printStmt(c.expr)}
			err := Verify(m)
			if err == nil {
				t.Fatal("错误的字符串操作数应被拒绝")
			}
			if got := diagnosticCode(err); got != c.code {
				t.Fatalf("错误码应为 %s，实际 %s（%v）", c.code, got, err)
			}
		})
	}
}

// TestStringLiteralKeepsEmptyValue 确认空字符串和 false 一样不会被 omitempty
// 吃掉：value 字段存在与否是格式错误，不是零值判断。
func TestStringLiteralKeepsEmptyValue(t *testing.T) {
	m := minimalModule()
	m.Functions[0].Body = []Stmt{printStmt(String(""))}
	data, err := EncodeBytes(m)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"value": ""`) {
		t.Fatalf("空字符串字面量缺少 value 字段：%s", data)
	}
	back, err := DecodeBytes(data, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := back.Functions[0].Body[0].Value.StringValue(); !ok {
		t.Fatal("空字符串回读后不应被当成缺字段")
	}
}

func objectPoint() TypeDef {
	return TypeDef{
		ID: 8, Kind: TypeKindObject, Name: "Point",
		Properties: []TypeProperty{{Name: "x", Type: TypeNumber}, {Name: "y", Type: TypeNumber}},
	}
}

func objectModule(fields ...ObjectField) *Module {
	m := typedModule(objectPoint())
	m.Functions[0].Body = []Stmt{
		{Op: OpObject, Name: "p", Type: 8, Fields: fields},
		{Op: OpPrint, Value: &Expr{Op: OpField, Name: "x", Operand: &Expr{Op: OpLocal, Name: "p"}}},
		{Op: OpReturn},
	}
	return m
}

func TestObjectStatementAndField(t *testing.T) {
	m := objectModule(
		ObjectField{Name: "x", Value: num(1)},
		ObjectField{Name: "y", Value: num(2)},
	)
	if err := Verify(m); err != nil {
		t.Fatalf("对象模块应通过验证：%v", err)
	}
	data, err := EncodeBytes(m)
	if err != nil {
		t.Fatal(err)
	}
	back, err := DecodeBytes(data, DefaultLimits())
	if err != nil {
		t.Fatalf("对象模块应能解码：%v", err)
	}
	if back.Functions[0].Body[0].Type != 8 || len(back.Functions[0].Body[0].Fields) != 2 {
		t.Fatalf("对象语句回读不完整：%+v", back.Functions[0].Body[0])
	}
	if back.Functions[0].Body[1].Value.Op != OpField || back.Functions[0].Body[1].Value.Name != "x" {
		t.Fatalf("字段读取回读不完整：%+v", back.Functions[0].Body[1].Value)
	}
}

func TestObjectStatementRejectsBadFields(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Module)
		code   string
	}{
		{"缺字段", func(m *Module) {
			m.Functions[0].Body[0].Fields = []ObjectField{{Name: "x", Value: num(1)}}
		}, "nir.missing"},
		{"未知字段", func(m *Module) {
			m.Functions[0].Body[0].Fields[0].Name = "z"
		}, "nir.unknown-field"},
		{"字段类型不符", func(m *Module) {
			m.Functions[0].Body[0].Fields[0].Value = String("a")
		}, "nir.object-field"},
		{"字段重复", func(m *Module) {
			m.Functions[0].Body[0].Fields[1].Name = "x"
		}, "nir.duplicate-name"},
		{"读取未知字段", func(m *Module) {
			m.Functions[0].Body[1].Value.Name = "z"
		}, "nir.unknown-field"},
		{"对象不能打印", func(m *Module) {
			m.Functions[0].Body[1].Value = &Expr{Op: OpLocal, Name: "p"}
		}, "nir.print-type"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := objectModule(
				ObjectField{Name: "x", Value: num(1)},
				ObjectField{Name: "y", Value: num(2)},
			)
			c.mutate(m)
			err := Verify(m)
			if err == nil {
				t.Fatal("坏对象语句应被拒绝")
			}
			if got := diagnosticCode(err); got != c.code {
				t.Fatalf("错误码应为 %s，实际 %s（%v）", c.code, got, err)
			}
		})
	}
}

func TestObjectOptionalPropertyDeferred(t *testing.T) {
	m := typedModule(TypeDef{
		ID: 8, Kind: TypeKindObject, Name: "Option",
		Properties: []TypeProperty{{Name: "x", Type: TypeNumber}, {Name: "y", Type: TypeNumber, Optional: true}},
	})
	m.Functions[0].Body = []Stmt{
		{Op: OpObject, Name: "value", Type: 8, Fields: []ObjectField{{Name: "x", Value: num(1)}}},
	}
	err := Verify(m)
	if got := diagnosticCode(err); got != "nir.object-field" {
		t.Fatalf("可选属性应按 nir.object-field 拒绝，实际 %s（%v）", got, err)
	}
}

func TestObjectIdentityEquality(t *testing.T) {
	m := objectModule(
		ObjectField{Name: "x", Value: num(1)},
		ObjectField{Name: "y", Value: num(2)},
	)
	m.Functions[0].Body = append(m.Functions[0].Body[:2],
		Stmt{Op: OpPrint, Value: &Expr{Op: OpCompare, Operator: "===",
			Left:  &Expr{Op: OpLocal, Name: "p"},
			Right: &Expr{Op: OpLocal, Name: "p"}}},
		Stmt{Op: OpReturn},
	)
	if err := Verify(m); err != nil {
		t.Fatalf("对象引用比较应通过验证：%v", err)
	}
}

func ptrExpr(e Expr) *Expr { return &e }
