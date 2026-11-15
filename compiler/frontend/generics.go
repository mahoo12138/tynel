package frontend

import "fmt"

// TypeParametersOf 返回一个声明签名里出现的类型参数名，按参数表再返回值的
// 首次出现顺序去重。泛型函数没有单独的类型参数表时，这是 NIR 单态化需要的
// 最小信息；约束不参与运行时布局。
func (f *Facts) TypeParametersOf(d *Declaration) []string {
	if d == nil {
		return nil
	}
	seen := make(map[string]bool)
	var names []string
	var collect func(*Type)
	collect = func(t *Type) {
		if t == nil {
			return
		}
		if t.Kind == KindTypeParameter {
			if t.Name != "" && !seen[t.Name] {
				seen[t.Name] = true
				names = append(names, t.Name)
			}
			return
		}
		collect(t.Element)
		for i := range t.Elements {
			collect(&t.Elements[i])
		}
		for i := range t.Members {
			collect(&t.Members[i])
		}
		for i := range t.TypeArgs {
			collect(&t.TypeArgs[i])
		}
		for i := range t.Properties {
			collect(&t.Properties[i].Type)
		}
		for i := range t.Params {
			collect(&t.Params[i].Type)
		}
		collect(t.Result)
	}
	for _, id := range d.Parameters {
		if param, ok := f.Declaration(id); ok {
			collect(param.Type)
		}
	}
	collect(d.Result)
	return names
}

// InferTypeParams 从实参类型推断类型参数。它只做结构直接匹配：类型参数可以
// 出现在顶层、数组元素、元组位置、对象属性和函数类型里；联合与泛型别名不
// 参与推断，调用点会按能力限制拒绝。
func InferTypeParams(params, args []Type) (map[string]Type, bool) {
	if len(params) != len(args) {
		return nil, false
	}
	subst := make(map[string]Type)
	for i := range params {
		if !unifyType(&params[i], &args[i], subst, 0) {
			return nil, false
		}
	}
	return subst, true
}

func unifyType(param, actual *Type, subst map[string]Type, depth int) bool {
	if param == nil || actual == nil {
		return param == actual
	}
	if depth > maxTypeDepth {
		return false
	}
	if param.Kind == KindTypeParameter {
		if param.Name == "" {
			return false
		}
		if bound, ok := subst[param.Name]; ok {
			return SameType(&bound, actual)
		}
		subst[param.Name] = *actual
		return true
	}
	if param.Kind != actual.Kind {
		return false
	}
	switch param.Kind {
	case KindArray:
		return unifyType(param.Element, actual.Element, subst, depth+1)
	case KindTuple:
		if len(param.Elements) != len(actual.Elements) {
			return false
		}
		for i := range param.Elements {
			if !unifyType(&param.Elements[i], &actual.Elements[i], subst, depth+1) {
				return false
			}
		}
		return true
	case KindObject:
		for i := range param.Properties {
			prop := &param.Properties[i]
			other, ok := propertyOf(actual, prop.Name)
			if !ok {
				return false
			}
			if !unifyType(&prop.Type, &other.Type, subst, depth+1) {
				return false
			}
		}
		return true
	case KindFunction:
		if len(param.Params) != len(actual.Params) {
			return false
		}
		for i := range param.Params {
			if !unifyType(&param.Params[i].Type, &actual.Params[i].Type, subst, depth+1) {
				return false
			}
		}
		return unifyType(param.Result, actual.Result, subst, depth+1)
	case KindUnion, KindIntersection:
		return false
	}
	return SameType(param, actual)
}

// SubstituteType 把类型参数替换成推断出的具体类型。遇到没有对应实参的类型
// 参数会返回错误，而不是留下一个 NIR 无法表示的类型。
func SubstituteType(t *Type, subst map[string]Type) (*Type, error) {
	return substituteType(t, subst, 0)
}

func substituteType(t *Type, subst map[string]Type, depth int) (*Type, error) {
	if t == nil {
		return nil, nil
	}
	if depth > maxTypeDepth {
		return nil, fmt.Errorf("类型替换超过 %d 层", maxTypeDepth)
	}
	if t.Kind == KindTypeParameter {
		bound, ok := subst[t.Name]
		if !ok {
			return nil, fmt.Errorf("类型参数 %q 没有类型实参", t.Name)
		}
		value := bound
		return &value, nil
	}
	out := *t
	var err error
	if t.Element != nil {
		if out.Element, err = substituteType(t.Element, subst, depth+1); err != nil {
			return nil, err
		}
	}
	if len(t.Elements) > 0 {
		out.Elements = make([]Type, len(t.Elements))
		for i := range t.Elements {
			value, err := substituteType(&t.Elements[i], subst, depth+1)
			if err != nil {
				return nil, err
			}
			out.Elements[i] = *value
		}
	}
	if len(t.Members) > 0 {
		out.Members = make([]Type, len(t.Members))
		for i := range t.Members {
			value, err := substituteType(&t.Members[i], subst, depth+1)
			if err != nil {
				return nil, err
			}
			out.Members[i] = *value
		}
	}
	if len(t.TypeArgs) > 0 {
		out.TypeArgs = make([]Type, len(t.TypeArgs))
		for i := range t.TypeArgs {
			value, err := substituteType(&t.TypeArgs[i], subst, depth+1)
			if err != nil {
				return nil, err
			}
			out.TypeArgs[i] = *value
		}
	}
	if len(t.Properties) > 0 {
		out.Properties = make([]Property, len(t.Properties))
		for i := range t.Properties {
			value, err := substituteType(&t.Properties[i].Type, subst, depth+1)
			if err != nil {
				return nil, err
			}
			out.Properties[i] = t.Properties[i]
			out.Properties[i].Type = *value
		}
	}
	if len(t.Params) > 0 {
		out.Params = make([]Field, len(t.Params))
		for i := range t.Params {
			value, err := substituteType(&t.Params[i].Type, subst, depth+1)
			if err != nil {
				return nil, err
			}
			out.Params[i] = t.Params[i]
			out.Params[i].Type = *value
		}
	}
	if t.Result != nil {
		if out.Result, err = substituteType(t.Result, subst, depth+1); err != nil {
			return nil, err
		}
	}
	return &out, nil
}

// ContainsTypeParameter 报告一个类型摘要是否含未实例化的类型参数。
func ContainsTypeParameter(t *Type) bool {
	if t == nil {
		return false
	}
	if t.Kind == KindTypeParameter {
		return true
	}
	if ContainsTypeParameter(t.Element) {
		return true
	}
	for i := range t.Elements {
		if ContainsTypeParameter(&t.Elements[i]) {
			return true
		}
	}
	for i := range t.Members {
		if ContainsTypeParameter(&t.Members[i]) {
			return true
		}
	}
	for i := range t.TypeArgs {
		if ContainsTypeParameter(&t.TypeArgs[i]) {
			return true
		}
	}
	for i := range t.Properties {
		if ContainsTypeParameter(&t.Properties[i].Type) {
			return true
		}
	}
	for i := range t.Params {
		if ContainsTypeParameter(&t.Params[i].Type) {
			return true
		}
	}
	return ContainsTypeParameter(t.Result)
}
