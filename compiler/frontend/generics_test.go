package frontend

import "testing"

func typeParam(name string) Type { return Type{Kind: KindTypeParameter, Name: name} }

func TestTypeParametersOf(t *testing.T) {
	number := Type{Kind: KindNumber}
	tp := typeParam("T")
	facts := &Facts{
		Declarations: []Declaration{
			{ID: 1, Kind: DeclFunction, Name: "pick", Parameters: []int{2, 3}, Result: &tp},
			{ID: 2, Kind: DeclParameter, Name: "value", Type: &tp},
			{ID: 3, Kind: DeclParameter, Name: "fallback", Type: &number},
		},
	}
	got := facts.TypeParametersOf(&facts.Declarations[0])
	if len(got) != 1 || got[0] != "T" {
		t.Fatalf("类型参数应为 [T]，实际 %v", got)
	}
}

func TestInferTypeParamsFromArrayAndObject(t *testing.T) {
	tp := typeParam("T")
	number := Type{Kind: KindNumber}
	stringType := Type{Kind: KindString}

	params := []Type{{Kind: KindArray, Element: &tp}}
	args := []Type{{Kind: KindArray, Element: &number}}
	subst, ok := InferTypeParams(params, args)
	if !ok || subst["T"].Kind != KindNumber {
		t.Fatalf("数组元素推断失败：%v %v", ok, subst)
	}

	box := Type{Kind: KindObject, Properties: []Property{{Name: "value", Type: tp}}}
	actual := Type{Kind: KindObject, Properties: []Property{{Name: "value", Type: stringType}}}
	subst, ok = InferTypeParams([]Type{box}, []Type{actual})
	if !ok || subst["T"].Kind != KindString {
		t.Fatalf("对象属性推断失败：%v %v", ok, subst)
	}
}

func TestInferTypeParamsRejectsUnionAndMismatch(t *testing.T) {
	tp := typeParam("T")
	number := Type{Kind: KindNumber}
	if _, ok := InferTypeParams([]Type{{Kind: KindUnion, Members: []Type{tp, {Kind: KindNull}}}}, []Type{number}); ok {
		t.Fatal("联合推断应被拒绝")
	}
	if _, ok := InferTypeParams([]Type{tp}, []Type{{Kind: KindNumber}}); !ok {
		t.Fatal("直接类型参数推断应成功")
	}
	if _, ok := InferTypeParams([]Type{{Kind: KindArray, Element: &tp}}, []Type{{Kind: KindArray, Element: &number}, number}); ok {
		t.Fatal("参数个数不一致应被拒绝")
	}
}

func TestSubstituteType(t *testing.T) {
	tp := typeParam("T")
	number := Type{Kind: KindNumber}
	input := Type{
		Kind: KindTuple,
		Elements: []Type{
			tp,
			{Kind: KindObject, Properties: []Property{{Name: "value", Type: tp}}},
		},
	}
	got, err := SubstituteType(&input, map[string]Type{"T": number})
	if err != nil {
		t.Fatal(err)
	}
	if got.Elements[0].Kind != KindNumber {
		t.Fatalf("第一个元组元素应为 number：%+v", got.Elements[0])
	}
	if got.Elements[1].Properties[0].Type.Kind != KindNumber {
		t.Fatalf("对象属性应被替换：%+v", got.Elements[1])
	}
	if !ContainsTypeParameter(&input) || ContainsTypeParameter(got) {
		t.Fatal("输入应含类型参数，替换结果不应含")
	}
	if _, err := SubstituteType(&tp, map[string]Type{}); err == nil {
		t.Fatal("缺少类型实参应报错")
	}
}
