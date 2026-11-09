package frontend

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
)

// arityFacts 以样例事实为底，给两个参数设置语义标记，并把调用的实参个数
// 改成 got。参数类型都是 number，所以实参也一律填 number。
func arityFacts(got int, first, second Declaration) *Facts {
	f := sampleFacts()
	f.Declarations[1] = first
	f.Declarations[2] = second
	args := make([]Type, got)
	for i := range args {
		args[i] = Type{Kind: KindNumber}
	}
	f.Calls[0].Arguments = args
	return f
}

func param(id int, name string) Declaration {
	number := Type{Kind: KindNumber}
	return Declaration{ID: id, Kind: DeclParameter, Name: name, Start: id * 10, Length: 1, Type: &number}
}

func TestArityOf(t *testing.T) {
	required := func(id int, name string) Declaration { return param(id, name) }
	withDefault := func(id int, name string) Declaration {
		d := param(id, name)
		d.HasDefault = true
		return d
	}
	withQuestion := func(id int, name string) Declaration {
		d := param(id, name)
		d.Optional = true
		return d
	}
	cases := []struct {
		name   string
		first  Declaration
		second Declaration
		want   Arity
	}{
		{"两个必填参数", required(2, "a"), required(3, "b"), Arity{Required: 2, Total: 2}},
		{"尾部带默认值", required(2, "a"), withDefault(3, "b"), Arity{Required: 1, Total: 2}},
		{"尾部可选", required(2, "a"), withQuestion(3, "b"), Arity{Required: 1, Total: 2}},
		{"默认值后面还有必填参数", withDefault(2, "a"), required(3, "b"), Arity{Required: 2, Total: 2}},
		{"两个都可省略", withDefault(2, "a"), withQuestion(3, "b"), Arity{Required: 0, Total: 2}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := arityFacts(2, c.first, c.second)
			callee := &f.Declarations[0]
			if got := f.ArityOf(callee); got != c.want {
				t.Fatalf("ArityOf = %+v，期望 %+v", got, c.want)
			}
		})
	}
}

func TestArityAllows(t *testing.T) {
	a := Arity{Required: 1, Total: 2}
	for _, got := range []int{-1, 0, 3, 4} {
		if a.Allows(got) {
			t.Errorf("%d 个实参不应被允许", got)
		}
	}
	for _, got := range []int{1, 2} {
		if !a.Allows(got) {
			t.Errorf("%d 个实参应被允许", got)
		}
	}
}

// TestValidateAcceptsOmittedArguments 是这次修复的核心：add(5) 只给一个实参，
// 事实仍然自洽，不该被 facts.arg-count 当成内部故障。
func TestValidateAcceptsOmittedArguments(t *testing.T) {
	second := param(3, "b")
	second.HasDefault = true
	cases := []struct {
		got     int
		wantErr string
	}{
		{0, "facts.arg-count"},
		{1, ""},
		{2, ""},
		{3, "facts.arg-count"},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("%d 个实参", c.got), func(t *testing.T) {
			f := arityFacts(c.got, param(2, "a"), second)
			err := f.Validate()
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("合法事实被拒绝：%v", err)
				}
				return
			}
			var d *diagnostic.Diagnostic
			if !errors.As(err, &d) || d.Code != c.wantErr {
				t.Fatalf("应报 %s，实际 %v", c.wantErr, err)
			}
		})
	}
}

// TestValidateRejectsParameterSemanticsOnOtherDeclarations 确认参数语义字段
// 不能挂到别的声明上：事实校验不假设输入一定来自刚跑过的前端。
func TestValidateRejectsParameterSemanticsOnOtherDeclarations(t *testing.T) {
	for _, mutate := range []func(*Facts){
		func(f *Facts) { f.Declarations[0].HasDefault = true },
		func(f *Facts) { f.Declarations[3].Optional = true },
	} {
		f := sampleFacts()
		mutate(f)
		err := f.Validate()
		var d *diagnostic.Diagnostic
		if !errors.As(err, &d) || d.Code != "facts.bad-decl" {
			t.Fatalf("应报 facts.bad-decl，实际 %v", err)
		}
	}
}

// TestValidateAllowsUndefinedOnOmittableParameter 确认 f(undefined, 2) 这种
// 合法写法不会撞上 facts.arg-type。前端把 undefined 压成 other，只有可省略
// 的位置才允许这种粗粒度摘要。
func TestValidateAllowsUndefinedOnOmittableParameter(t *testing.T) {
	first := param(2, "a")
	first.HasDefault = true
	f := arityFacts(2, first, param(3, "b"))
	f.Calls[0].Arguments[0] = Type{Kind: KindOther}
	if err := f.Validate(); err != nil {
		t.Fatalf("可省略位置传 undefined 应通过：%v", err)
	}
	f.Calls[0].Arguments[0] = Type{Kind: KindOther}
	f.Declarations[1] = param(2, "a")
	err := f.Validate()
	var d *diagnostic.Diagnostic
	if !errors.As(err, &d) || d.Code != "facts.arg-type" {
		t.Fatalf("必填位置仍应报 facts.arg-type，实际 %v", err)
	}
}

// TestParameterSemanticsRoundTrip 确认新字段能穿过 JSON 编解码，契约两端
// 用的是同一组字段名。
func TestParameterSemanticsRoundTrip(t *testing.T) {
	f := sampleFacts()
	f.Declarations[2].HasDefault = true
	f.Declarations[1].Optional = true
	f.Calls[0].Arguments = f.Calls[0].Arguments[:1]
	data, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(data)
	if err != nil {
		t.Fatalf("往返失败：%v", err)
	}
	if !decoded.Declarations[1].Optional || !decoded.Declarations[2].HasDefault {
		t.Fatalf("参数语义在往返中丢失：%+v", decoded.Declarations)
	}
}
