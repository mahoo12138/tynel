package lower

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Mahoo12138/tynel/compiler/frontend"
	"github.com/Mahoo12138/tynel/compiler/nir"
)

// typeTable 把事实里的结构化类型分配成 NIR 类型 ID。内置标量使用固定 ID；
// 对象按首次使用顺序分配 >= 8 的 ID，结构相同（忽略类型名）的对象复用同一
// 个条目。属性按名字排序，既让去重稳定，也让 bridge 的字段顺序可预测。
type typeTable struct {
	facts *frontend.Facts
	defs  []nir.TypeDef
	byKey map[string]int
	next  int
}

func newTypeTable(facts *frontend.Facts) *typeTable {
	return &typeTable{facts: facts, byKey: make(map[string]int), next: nir.FirstComposite}
}

// id 返回一个事实类型的 NIR 类型 ID。void 由调用方按位置决定是否允许。
func (t *typeTable) id(typ *frontend.Type) (int, error) {
	return t.idDepth(typ, 0)
}

func (t *typeTable) idDepth(typ *frontend.Type, depth int) (int, error) {
	if typ == nil {
		return 0, fmt.Errorf("缺少类型摘要")
	}
	if depth > maxDepth {
		return 0, fmt.Errorf("类型嵌套超过 %d 层", maxDepth)
	}
	resolved := t.facts.ResolveType(typ)
	switch resolved.Kind {
	case frontend.KindNumber:
		return nir.TypeNumber, nil
	case frontend.KindBoolean:
		return nir.TypeBool, nil
	case frontend.KindString:
		return nir.TypeString, nil
	case frontend.KindVoid:
		return nir.TypeVoid, nil
	case frontend.KindObject:
		return t.object(resolved, depth)
	case frontend.KindArray:
		return t.array(resolved, depth)
	case frontend.KindTuple:
		return t.tuple(resolved, depth)
	}
	return 0, fmt.Errorf("类型 %s 还没有 NIR 表示", resolved.Kind)
}

// comparable 报告严格相等是否支持这个类型：标量和对象引用都支持。
func (t *typeTable) comparable(id int) bool {
	switch id {
	case nir.TypeNumber, nir.TypeBool, nir.TypeString:
		return true
	}
	return t.isObject(id)
}

func (t *typeTable) isObject(id int) bool {
	def := t.objectDef(id)
	return def != nil
}

func (t *typeTable) objectDef(id int) *nir.TypeDef {
	return t.defOf(id, nir.TypeKindObject)
}

func (t *typeTable) arrayDef(id int) *nir.TypeDef {
	return t.defOf(id, nir.TypeKindArray)
}

func (t *typeTable) tupleDef(id int) *nir.TypeDef {
	return t.defOf(id, nir.TypeKindTuple)
}

func (t *typeTable) defOf(id int, kind string) *nir.TypeDef {
	for i := range t.defs {
		if t.defs[i].ID == id && t.defs[i].Kind == kind {
			return &t.defs[i]
		}
	}
	return nil
}

func (t *typeTable) object(typ *frontend.Type, depth int) (int, error) {
	props := append([]frontend.Property(nil), typ.Properties...)
	sort.SliceStable(props, func(i, j int) bool { return props[i].Name < props[j].Name })
	var key strings.Builder
	key.WriteString("object{")
	def := nir.TypeDef{Kind: nir.TypeKindObject, Name: typ.Name}
	for i := range props {
		prop := &props[i]
		if prop.Name == "" {
			return 0, fmt.Errorf("对象属性缺少名字")
		}
		if prop.Optional {
			return 0, fmt.Errorf("可选属性 %q 需要 undefined 表示", prop.Name)
		}
		fieldID, err := t.idDepth(&prop.Type, depth+1)
		if err != nil {
			return 0, err
		}
		if fieldID == nir.TypeVoid {
			return 0, fmt.Errorf("对象属性 %q 不能是 void", prop.Name)
		}
		fmt.Fprintf(&key, "%s:%d;", prop.Name, fieldID)
		def.Properties = append(def.Properties, nir.TypeProperty{Name: prop.Name, Type: fieldID})
	}
	key.WriteString("}")
	if id, ok := t.byKey[key.String()]; ok {
		return id, nil
	}
	def.ID = t.next
	t.next++
	t.byKey[key.String()] = def.ID
	t.defs = append(t.defs, def)
	return def.ID, nil
}

// array 为一个同质数组分配类型 ID。元素类型递归进入类型表，结构相同的数组
// 复用同一条目。
func (t *typeTable) array(typ *frontend.Type, depth int) (int, error) {
	if typ.Element == nil {
		return 0, fmt.Errorf("数组缺少元素类型")
	}
	element, err := t.idDepth(typ.Element, depth+1)
	if err != nil {
		return 0, fmt.Errorf("数组元素：%w", err)
	}
	if element == nir.TypeVoid {
		return 0, fmt.Errorf("数组元素不能是 void")
	}
	key := fmt.Sprintf("array{%d}", element)
	if id, ok := t.byKey[key]; ok {
		return id, nil
	}
	def := nir.TypeDef{ID: t.next, Kind: nir.TypeKindArray, Element: element}
	t.next++
	t.byKey[key] = def.ID
	t.defs = append(t.defs, def)
	return def.ID, nil
}

// tuple 为一个固定长度元组分配类型 ID。元素按位置保留类型，空元组没有
// 可执行布局，明确拒绝。
func (t *typeTable) tuple(typ *frontend.Type, depth int) (int, error) {
	if len(typ.Elements) == 0 {
		return 0, fmt.Errorf("空元组还没有 NIR 表示")
	}
	elements := make([]int, 0, len(typ.Elements))
	for i := range typ.Elements {
		id, err := t.idDepth(&typ.Elements[i], depth+1)
		if err != nil {
			return 0, fmt.Errorf("元组第 %d 个元素：%w", i+1, err)
		}
		if id == nir.TypeVoid {
			return 0, fmt.Errorf("元组第 %d 个元素不能是 void", i+1)
		}
		elements = append(elements, id)
	}
	var key strings.Builder
	key.WriteString("tuple{")
	for _, id := range elements {
		fmt.Fprintf(&key, "%d;", id)
	}
	key.WriteString("}")
	if id, ok := t.byKey[key.String()]; ok {
		return id, nil
	}
	def := nir.TypeDef{ID: t.next, Kind: nir.TypeKindTuple, Elements: elements}
	t.next++
	t.byKey[key.String()] = def.ID
	t.defs = append(t.defs, def)
	return def.ID, nil
}
