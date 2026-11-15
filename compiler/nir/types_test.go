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

func TestArrayAndTupleStatements(t *testing.T) {
	m := typedModule(
		TypeDef{ID: 8, Kind: TypeKindArray, Element: TypeNumber},
		TypeDef{ID: 9, Kind: TypeKindTuple, Elements: []int{TypeNumber, TypeBool}},
	)
	m.Functions[0].Body = []Stmt{
		{Op: OpArray, Name: "xs", Type: 8, Items: []Expr{num(1), num(2)}},
		{Op: OpIndex, Name: "first", Type: TypeNumber, Value: ptrExpr(local("xs")), Index: ptrExpr(num(0))},
		{Op: OpPrint, Value: ptrExpr(Expr{Op: OpLen, Operand: ptrExpr(local("xs"))})},
		{Op: OpTuple, Name: "pair", Type: 9, Items: []Expr{num(7), Bool(true)}},
		{Op: OpIndex, Name: "flag", Type: TypeBool, Value: ptrExpr(local("pair")), Index: ptrExpr(num(1))},
		{Op: OpPrint, Value: ptrExpr(local("flag"))},
		{Op: OpPrint, Value: ptrExpr(local("first"))},
		{Op: OpReturn},
	}
	if err := Verify(m); err != nil {
		t.Fatalf("数组与元组模块应通过验证：%v", err)
	}
	data, err := EncodeBytes(m)
	if err != nil {
		t.Fatal(err)
	}
	back, err := DecodeBytes(data, DefaultLimits())
	if err != nil {
		t.Fatalf("数组与元组模块应能解码：%v", err)
	}
	body := back.Functions[0].Body
	if body[0].Op != OpArray || len(body[0].Items) != 2 {
		t.Fatalf("array 语句回读不完整：%+v", body[0])
	}
	if body[1].Op != OpIndex || body[1].Index == nil {
		t.Fatalf("index 语句回读不完整：%+v", body[1])
	}
	if body[3].Op != OpTuple || len(body[3].Items) != 2 {
		t.Fatalf("tuple 语句回读不完整：%+v", body[3])
	}
}

func TestArrayAndTupleStatementsRejectBadItems(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Module)
		code   string
	}{
		{"数组元素类型不符", func(m *Module) {
			m.Functions[0].Body[0].Items[0] = String("a")
		}, "nir.array-item"},
		{"元组元素类型不符", func(m *Module) {
			m.Functions[0].Body[3].Items[1] = num(1)
		}, "nir.tuple-item"},
		{"元组索引不是字面量", func(m *Module) {
			m.Functions[0].Body[4].Index = ptrExpr(local("first"))
		}, "nir.index-type"},
		{"元组索引越界", func(m *Module) {
			m.Functions[0].Body[4].Index = ptrExpr(num(2))
		}, "nir.index-bounds"},
		{"len 操作数不是数组", func(m *Module) {
			m.Functions[0].Body[2].Value = ptrExpr(Expr{Op: OpLen, Operand: ptrExpr(String("a"))})
		}, "nir.operand-type"},
		{"索引结果类型不符", func(m *Module) {
			m.Functions[0].Body[1].Type = TypeBool
		}, "nir.index-type"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := typedModule(
				TypeDef{ID: 8, Kind: TypeKindArray, Element: TypeNumber},
				TypeDef{ID: 9, Kind: TypeKindTuple, Elements: []int{TypeNumber, TypeBool}},
			)
			m.Functions[0].Body = []Stmt{
				{Op: OpArray, Name: "xs", Type: 8, Items: []Expr{num(1), num(2)}},
				{Op: OpIndex, Name: "first", Type: TypeNumber, Value: ptrExpr(local("xs")), Index: ptrExpr(num(0))},
				{Op: OpPrint, Value: ptrExpr(Expr{Op: OpLen, Operand: ptrExpr(local("xs"))})},
				{Op: OpTuple, Name: "pair", Type: 9, Items: []Expr{num(7), Bool(true)}},
				{Op: OpIndex, Name: "flag", Type: TypeBool, Value: ptrExpr(local("pair")), Index: ptrExpr(num(1))},
				{Op: OpReturn},
			}
			c.mutate(m)
			err := Verify(m)
			if err == nil {
				t.Fatal("坏数组或元组语句应被拒绝")
			}
			if got := diagnosticCode(err); got != c.code {
				t.Fatalf("错误码应为 %s，实际 %s（%v）", c.code, got, err)
			}
		})
	}
}

func TestUnionWrapUnwrapAndTag(t *testing.T) {
	m := typedModule(TypeDef{ID: 8, Kind: TypeKindUnion, Members: []int{TypeNumber, TypeNull}})
	m.Functions[0].Body = []Stmt{
		{Op: OpWrap, Name: "a", Type: 8, Value: ptrExpr(num(5))},
		{Op: OpUnwrap, Name: "n", Type: TypeNumber, Value: ptrExpr(local("a"))},
		{Op: OpPrint, Value: ptrExpr(local("n"))},
		{Op: OpWrap, Name: "b", Type: 8, Value: ptrExpr(Expr{Op: OpNull})},
		{Op: OpPrint, Value: ptrExpr(Expr{Op: OpIs, Type: TypeNull, Operand: ptrExpr(local("b"))})},
		{Op: OpReturn},
	}
	if err := Verify(m); err != nil {
		t.Fatalf("联合模块应通过验证：%v", err)
	}
	data, err := EncodeBytes(m)
	if err != nil {
		t.Fatal(err)
	}
	back, err := DecodeBytes(data, DefaultLimits())
	if err != nil {
		t.Fatalf("联合模块应能解码：%v", err)
	}
	if back.Functions[0].Body[0].Op != OpWrap || back.Functions[0].Body[1].Op != OpUnwrap {
		t.Fatalf("联合操作回读不完整：%+v", back.Functions[0].Body[:2])
	}
	if back.Functions[0].Body[4].Value.Type != TypeNull {
		t.Fatalf("is 的目标类型丢失：%+v", back.Functions[0].Body[4].Value)
	}
}

func TestUnionOperationsRejectBadMembers(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Module)
		code   string
	}{
		{"wrap 值不是成员", func(m *Module) {
			m.Functions[0].Body[0].Value = ptrExpr(String("a"))
		}, "nir.union-member"},
		{"unwrap 目标不是成员", func(m *Module) {
			m.Functions[0].Body[1].Type = TypeBool
		}, "nir.union-member"},
		{"is 目标不是成员", func(m *Module) {
			m.Functions[0].Body[4].Value.Type = TypeBool
		}, "nir.union-member"},
		{"unwrap 值不是联合", func(m *Module) {
			m.Functions[0].Body[1].Value = ptrExpr(num(1))
		}, "nir.type-kind"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := typedModule(TypeDef{ID: 8, Kind: TypeKindUnion, Members: []int{TypeNumber, TypeNull}})
			m.Functions[0].Body = []Stmt{
				{Op: OpWrap, Name: "a", Type: 8, Value: ptrExpr(num(5))},
				{Op: OpUnwrap, Name: "n", Type: TypeNumber, Value: ptrExpr(local("a"))},
				{Op: OpPrint, Value: ptrExpr(local("n"))},
				{Op: OpWrap, Name: "b", Type: 8, Value: ptrExpr(Expr{Op: OpNull})},
				{Op: OpPrint, Value: ptrExpr(Expr{Op: OpIs, Type: TypeNull, Operand: ptrExpr(local("b"))})},
				{Op: OpReturn},
			}
			c.mutate(m)
			err := Verify(m)
			if err == nil {
				t.Fatal("坏联合操作应被拒绝")
			}
			if got := diagnosticCode(err); got != c.code {
				t.Fatalf("错误码应为 %s，实际 %s（%v）", c.code, got, err)
			}
		})
	}
}

func ptrExpr(e Expr) *Expr { return &e }

// functionValueModule 构造一个引用具名函数并通过函数值调用的模块。
func functionValueModule() *Module {
	m := typedModule(TypeDef{ID: 8, Kind: TypeKindFunction, Params: []int{TypeNumber}, Result: TypeNumber})
	m.Functions = append(m.Functions, Function{
		Name:   "inc",
		Params: []Param{{Name: "x", Type: TypeNumber}},
		Result: TypeNumber,
		Body: []Stmt{{Op: OpReturn, Value: ptrExpr(Expr{
			Op:       OpBinary,
			Operator: "+",
			Left:     ptrExpr(Expr{Op: OpParam, Name: "x"}),
			Right:    ptrExpr(num(1)),
		})}},
	})
	m.Functions[0].Body = []Stmt{
		{Op: OpLet, Name: "f", Value: ptrExpr(Expr{Op: OpFunc, Function: "inc", Type: 8})},
		{Op: OpPrint, Value: ptrExpr(Expr{
			Op:     OpInvoke,
			Callee: ptrExpr(local("f")),
			Args:   []Expr{num(4)},
			Type:   TypeNumber,
		})},
		{Op: OpReturn},
	}
	return m
}

func TestFunctionValueAndInvoke(t *testing.T) {
	m := functionValueModule()
	if err := Verify(m); err != nil {
		t.Fatalf("函数值模块应通过验证：%v", err)
	}
	data, err := EncodeBytes(m)
	if err != nil {
		t.Fatal(err)
	}
	back, err := DecodeBytes(data, DefaultLimits())
	if err != nil {
		t.Fatalf("函数值模块应能解码：%v", err)
	}
	body := back.Functions[0].Body
	if body[0].Value == nil || body[0].Value.Op != OpFunc || body[0].Value.Function != "inc" || body[0].Value.Type != 8 {
		t.Fatalf("func 表达式回读不完整：%+v", body[0].Value)
	}
	if body[1].Value == nil || body[1].Value.Op != OpInvoke || body[1].Value.Callee == nil || len(body[1].Value.Args) != 1 {
		t.Fatalf("invoke 表达式回读不完整：%+v", body[1].Value)
	}
}

func TestFunctionValueRejectsBadCalls(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Module)
		code   string
	}{
		{"func 签名不符", func(m *Module) {
			m.Types[0].Params = []int{TypeNumber, TypeNumber}
		}, "nir.function-signature"},
		{"invoke 实参个数不符", func(m *Module) {
			m.Functions[0].Body[1].Value.Args = nil
		}, "nir.arg-count"},
		{"invoke 实参类型不符", func(m *Module) {
			m.Functions[0].Body[1].Value.Args[0] = String("a")
		}, "nir.arg-type"},
		{"invoke 结果类型不符", func(m *Module) {
			m.Functions[0].Body[1].Value.Type = TypeBool
		}, "nir.result-type"},
		{"invoke 目标不是函数", func(m *Module) {
			m.Functions[0].Body[1].Value.Callee = ptrExpr(num(1))
		}, "nir.type-kind"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := functionValueModule()
			c.mutate(m)
			err := Verify(m)
			if err == nil {
				t.Fatal("坏函数值调用应被拒绝")
			}
			if got := diagnosticCode(err); got != c.code {
				t.Fatalf("错误码应为 %s，实际 %s（%v）", c.code, got, err)
			}
		})
	}
}
