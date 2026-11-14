package frontend

import (
	"strings"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
)

// Kind 是类型摘要的类别。标量直接对应值类型；object、array、tuple、function、
// named 等结构化类别描述类型之间的关系，不代表后端已经能表示它们。尚未归类
// 的类型统一为 other。
type Kind string

const (
	KindNumber        Kind = "number"
	KindBoolean       Kind = "boolean"
	KindString        Kind = "string"
	KindVoid          Kind = "void"
	KindAny           Kind = "any"
	KindObject        Kind = "object"
	KindArray         Kind = "array"
	KindTuple         Kind = "tuple"
	KindFunction      Kind = "function"
	KindNamed         Kind = "named"
	KindUnion         Kind = "union"
	KindIntersection  Kind = "intersection"
	KindNull          Kind = "null"
	KindUndefined     Kind = "undefined"
	KindNever         Kind = "never"
	KindBigInt        Kind = "bigint"
	KindSymbol        Kind = "symbol"
	KindTypeParameter Kind = "type-parameter"
	KindInterface     Kind = "interface"
	KindOther         Kind = "other"
)

// valid 报告类别是否在允许集合内。校验按枚举比较，不做字符串规整。
func (k Kind) valid() bool {
	switch k {
	case KindNumber, KindBoolean, KindString, KindVoid, KindAny,
		KindObject, KindArray, KindTuple, KindFunction, KindNamed,
		KindUnion, KindIntersection, KindNull, KindUndefined, KindNever,
		KindBigInt, KindSymbol, KindTypeParameter, KindInterface, KindOther:
		return true
	}
	return false
}

// Type 是类型摘要。字段按 Kind 取用，未使用的字段保持零值。
//
// 它不是 TypeScript 类型系统的完整复刻：只保存能力检查、诊断和后续语义设计
// 需要的结构。例如 object 保存属性名与可选性，array 保存元素类型，function
// 保存参数与返回值，named 保存类型名与类型实参。上游 *checker.Type 不会出现
// 在这里，所以事实在会话关闭后仍然可读。
type Type struct {
	Kind       Kind       `json:"kind"`
	Name       string     `json:"name,omitempty"`
	TypeArgs   []Type     `json:"typeArgs,omitempty"`
	Members    []Type     `json:"members,omitempty"`
	Element    *Type      `json:"element,omitempty"`
	Elements   []Type     `json:"elements,omitempty"`
	Properties []Property `json:"properties,omitempty"`
	Params     []Field    `json:"params,omitempty"`
	Result     *Type      `json:"result,omitempty"`
}

// Property 是对象类型的一个属性。Optional 保留源码里的 `?` 或联合了
// undefined 的可选性；它不表示后端已经实现可选属性。
type Property struct {
	Name     string `json:"name"`
	Optional bool   `json:"optional,omitempty"`
	Type     Type   `json:"type"`
}

// Field 是函数类型的一个参数。Name 可能为空，例如 `(number) => string`。
type Field struct {
	Name     string `json:"name,omitempty"`
	Optional bool   `json:"optional,omitempty"`
	Type     Type   `json:"type"`
}

// String 返回用于诊断的紧凑类型描述。它只用于人类可读输出，不参与类型判定。
func (t Type) String() string { return typeString(&t, 0) }

func typeString(t *Type, depth int) string {
	if t == nil {
		return "missing"
	}
	if depth > 4 {
		return string(t.Kind)
	}
	switch t.Kind {
	case KindNamed:
		if len(t.TypeArgs) == 0 {
			if t.Name == "" {
				return "named"
			}
			return t.Name
		}
		return t.Name + "<" + joinTypes(t.TypeArgs, depth+1, ", ") + ">"
	case KindArray:
		if t.Element == nil {
			return "array"
		}
		return typeString(t.Element, depth+1) + "[]"
	case KindTuple:
		return "[" + joinTypes(t.Elements, depth+1, ", ") + "]"
	case KindUnion:
		return joinTypes(t.Members, depth+1, " | ")
	case KindIntersection:
		return joinTypes(t.Members, depth+1, " & ")
	case KindFunction:
		return "(" + joinFields(t.Params, depth+1) + ") => " + typeString(t.Result, depth+1)
	case KindObject:
		if t.Name != "" {
			return t.Name
		}
		return "{...}"
	case KindTypeParameter:
		if t.Name != "" {
			return t.Name
		}
		return "type-parameter"
	default:
		return string(t.Kind)
	}
}

func joinTypes(items []Type, depth int, sep string) string {
	parts := make([]string, 0, len(items))
	for i := range items {
		parts = append(parts, typeString(&items[i], depth))
	}
	return strings.Join(parts, sep)
}

func joinFields(items []Field, depth int) string {
	parts := make([]string, 0, len(items))
	for i := range items {
		field := &items[i]
		prefix := ""
		if field.Name != "" {
			prefix = field.Name + ": "
		}
		suffix := ""
		if field.Optional {
			suffix = "?"
		}
		parts = append(parts, prefix+typeString(&field.Type, depth)+suffix)
	}
	return strings.Join(parts, ", ")
}

// 类型摘要的递归预算。事实来自前端，深度和节点数必须由调用方兜底，避免一段
// 异常深的类型让校验或诊断爆栈。
const (
	maxTypeDepth = 16
	maxTypeNodes = 512
)

// validateType 检查类型摘要的结构完整性。它不做类型检查，只回答「这份摘要
// 自己讲得通吗」：类别是否认识、结构化类别要求的关键字段是否存在、子类型是否
// 也在预算内。
func validateType(t *Type, path string, depth int, nodes *int) error {
	if t == nil {
		return diagnostic.New(diagnostic.Internal, "facts.bad-type", "%s 缺少类型摘要", path)
	}
	if depth > maxTypeDepth {
		return diagnostic.New(diagnostic.Internal, "facts.bad-type", "%s 的类型摘要超过 %d 层", path, maxTypeDepth)
	}
	*nodes++
	if *nodes > maxTypeNodes {
		return diagnostic.New(diagnostic.Internal, "facts.bad-type", "%s 的类型摘要超过 %d 个节点", path, maxTypeNodes)
	}
	if !t.Kind.valid() {
		return diagnostic.New(diagnostic.Internal, "facts.bad-type", "%s 的类型类别 %q 不认识", path, t.Kind)
	}

	switch t.Kind {
	case KindNamed, KindTypeParameter:
		if t.Name == "" {
			return diagnostic.New(diagnostic.Internal, "facts.bad-type", "%s 的 %s 类型缺少名字", path, t.Kind)
		}
	case KindArray:
		if t.Element == nil {
			return diagnostic.New(diagnostic.Internal, "facts.bad-type", "%s 的数组类型缺少元素类型", path)
		}
	case KindTuple:
		if len(t.Elements) == 0 {
			return diagnostic.New(diagnostic.Internal, "facts.bad-type", "%s 的元组类型缺少元素", path)
		}
	case KindUnion, KindIntersection:
		if len(t.Members) < 2 {
			return diagnostic.New(diagnostic.Internal, "facts.bad-type", "%s 的 %s 类型至少需要两个成员", path, t.Kind)
		}
	case KindFunction:
		if t.Result == nil {
			return diagnostic.New(diagnostic.Internal, "facts.bad-type", "%s 的函数类型缺少返回类型", path)
		}
	}

	if t.Element != nil {
		if err := validateType(t.Element, path+".element", depth+1, nodes); err != nil {
			return err
		}
	}
	for i := range t.TypeArgs {
		if err := validateType(&t.TypeArgs[i], path+".typeArgs[]", depth+1, nodes); err != nil {
			return err
		}
	}
	for i := range t.Members {
		if err := validateType(&t.Members[i], path+".members[]", depth+1, nodes); err != nil {
			return err
		}
	}
	for i := range t.Elements {
		if err := validateType(&t.Elements[i], path+".elements[]", depth+1, nodes); err != nil {
			return err
		}
	}
	for i := range t.Properties {
		prop := &t.Properties[i]
		if prop.Name == "" {
			return diagnostic.New(diagnostic.Internal, "facts.bad-type", "%s.properties[] 缺少属性名", path)
		}
		if err := validateType(&prop.Type, path+".properties."+prop.Name, depth+1, nodes); err != nil {
			return err
		}
	}
	for i := range t.Params {
		if err := validateType(&t.Params[i].Type, path+".params[]", depth+1, nodes); err != nil {
			return err
		}
	}
	if t.Result != nil {
		if err := validateType(t.Result, path+".result", depth+1, nodes); err != nil {
			return err
		}
	}
	return nil
}
