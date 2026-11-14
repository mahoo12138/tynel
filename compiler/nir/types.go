package nir

// 类型表的种类名。intersection 目前只保留名称，不定义执行语义。
const (
	TypeKindObject    = "object"
	TypeKindArray     = "array"
	TypeKindTuple     = "tuple"
	TypeKindUnion     = "union"
	TypeKindIntersect = "intersection"
	TypeKindFunction  = "function"
)

// TypeDef 是模块类型表里的一个复合类型。字段按 Kind 取用，其余字段必须为空。
// 类型引用都是类型 ID：内置类型用 1..7，复合类型指向表中的另一个条目。
type TypeDef struct {
	ID         int            `json:"id"`
	Kind       string         `json:"kind"`
	Name       string         `json:"name,omitempty"`
	Properties []TypeProperty `json:"properties,omitempty"`
	Element    int            `json:"element,omitempty"`
	Elements   []int          `json:"elements,omitempty"`
	Members    []int          `json:"members,omitempty"`
	Params     []int          `json:"params,omitempty"`
	Result     int            `json:"result,omitempty"`
}

// TypeProperty 是对象类型的一个属性。
type TypeProperty struct {
	Name     string `json:"name"`
	Type     int    `json:"type"`
	Optional bool   `json:"optional,omitempty"`
}

// BuiltinTypeName 返回内置类型 ID 的名字；不是内置类型时返回空串。
func BuiltinTypeName(id int) string {
	switch id {
	case TypeNumber:
		return "number"
	case TypeBool:
		return "bool"
	case TypeVoid:
		return "void"
	case TypeString:
		return "string"
	case TypeNull:
		return "null"
	case TypeUndefined:
		return "undefined"
	case TypeNever:
		return "never"
	}
	return ""
}

// typeName 返回诊断里使用的类型描述。复合类型只写 ID，避免为了报错展开
// 整张类型图。
func typeName(id int) string {
	if name := BuiltinTypeName(id); name != "" {
		return name
	}
	return "type#" + itoa(id)
}

// validTypeKind 报告类型定义种类是否认识。
func validTypeKind(kind string) bool {
	switch kind {
	case TypeKindObject, TypeKindArray, TypeKindTuple, TypeKindUnion, TypeKindIntersect, TypeKindFunction:
		return true
	}
	return false
}

// typeIndex 把类型表收成 ID → 定义的表。调用前应确认 ID 唯一。
func typeIndex(defs []TypeDef) map[int]*TypeDef {
	index := make(map[int]*TypeDef, len(defs))
	for i := range defs {
		index[defs[i].ID] = &defs[i]
	}
	return index
}

// validTypeRef 报告一个类型 ID 是否指向内置类型或表中已定义的类型。allowVoid
// 决定 void 能否出现在这个位置：只有函数结果可以接受 void。
func validTypeRef(index map[int]*TypeDef, id int, allowVoid bool) bool {
	if id >= TypeNumber && id <= TypeNever {
		return id != TypeVoid || allowVoid
	}
	if id < FirstComposite {
		return false
	}
	_, ok := index[id]
	return ok
}

// checkTypes 校验类型表的自洽性，并返回 ID 索引供后续字段检查复用。
//
// 类型图允许通过 ID 表达递归对象，所以这里只核对每一条直接引用是否存在，
// 不要求图无环，也不做深度遍历。类型条目、属性和成员都计入节点预算。
func checkTypes(defs []TypeDef, limits Limits, nodes *int) (map[int]*TypeDef, error) {
	index := make(map[int]*TypeDef, len(defs))
	for i := range defs {
		def := &defs[i]
		path := "types[" + itoa(i) + "]"
		*nodes++
		if def.ID < FirstComposite {
			return nil, badf("nir.type-id", "%s 的 id 为 %d，复合类型必须是 >= %d 的正整数", path, def.ID, FirstComposite)
		}
		if _, exists := index[def.ID]; exists {
			return nil, badf("nir.type-id", "%s 的类型 id %d 重复", path, def.ID)
		}
		if !validTypeKind(def.Kind) {
			return nil, badf("nir.type-kind", "%s 的种类 %q 不认识", path, def.Kind)
		}
		index[def.ID] = def
	}
	for i := range defs {
		def := &defs[i]
		path := "types[" + itoa(i) + "]"
		if err := checkTypeFields(def, path, index, nodes); err != nil {
			return nil, err
		}
	}
	return index, nil
}

// checkTypeFields 按 Kind 核对字段形状和引用。
func checkTypeFields(def *TypeDef, path string, index map[int]*TypeDef, nodes *int) error {
	failField := func(field string) error {
		return badf("nir.type-kind", "%s（kind=%s）不接受字段 %s", path, def.Kind, field)
	}
	failRef := func(field string, id int, allowVoid bool) error {
		if validTypeRef(index, id, allowVoid) {
			return nil
		}
		if id == TypeVoid {
			return badf("nir.type-void", "%s.%s 引用了 void；void 只能作为函数结果", path, field)
		}
		return badf("nir.type-ref", "%s.%s 引用了不存在的类型 id %d", path, field, id)
	}

	if def.Kind != TypeKindObject && def.Name != "" {
		return failField("name")
	}
	if def.Kind != TypeKindObject && len(def.Properties) > 0 {
		return failField("properties")
	}
	if def.Kind != TypeKindArray && def.Element != 0 {
		return failField("element")
	}
	if def.Kind != TypeKindTuple && len(def.Elements) > 0 {
		return failField("elements")
	}
	if def.Kind != TypeKindUnion && def.Kind != TypeKindIntersect && len(def.Members) > 0 {
		return failField("members")
	}
	if def.Kind != TypeKindFunction && (len(def.Params) > 0 || def.Result != 0) {
		return failField("params/result")
	}

	switch def.Kind {
	case TypeKindObject:
		names := make(map[string]bool, len(def.Properties))
		for j := range def.Properties {
			prop := &def.Properties[j]
			ppath := path + ".properties[" + itoa(j) + "]"
			*nodes++
			if prop.Name == "" {
				return badf("nir.type-shape", "%s 缺少属性名", ppath)
			}
			if names[prop.Name] {
				return badf("nir.type-shape", "%s 的属性 %q 重复", path, prop.Name)
			}
			names[prop.Name] = true
			if err := failRef("properties."+prop.Name, prop.Type, false); err != nil {
				return err
			}
		}
	case TypeKindArray:
		if def.Element == 0 {
			return badf("nir.type-shape", "%s 的 array 缺少 element", path)
		}
		if err := failRef("element", def.Element, false); err != nil {
			return err
		}
	case TypeKindTuple:
		if len(def.Elements) == 0 {
			return badf("nir.type-shape", "%s 的 tuple 缺少 elements", path)
		}
		*nodes += len(def.Elements)
		for j, id := range def.Elements {
			if err := failRef("elements["+itoa(j)+"]", id, false); err != nil {
				return err
			}
		}
	case TypeKindUnion, TypeKindIntersect:
		if len(def.Members) < 2 {
			return badf("nir.type-shape", "%s 的 %s 至少需要两个成员", path, def.Kind)
		}
		*nodes += len(def.Members)
		for j, id := range def.Members {
			if err := failRef("members["+itoa(j)+"]", id, false); err != nil {
				return err
			}
		}
	case TypeKindFunction:
		if def.Result == 0 {
			return badf("nir.type-shape", "%s 的 function 缺少 result", path)
		}
		*nodes += len(def.Params)
		for j, id := range def.Params {
			if err := failRef("params["+itoa(j)+"]", id, false); err != nil {
				return err
			}
		}
		if err := failRef("result", def.Result, true); err != nil {
			return err
		}
	}
	return nil
}

// itoa 只用于格式路径；类型表规模很小，避免为每处路径引入 fmt。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
