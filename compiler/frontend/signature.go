package frontend

// Arity 是一次调用允许的实参数量范围。Required 是必填参数个数，Total 是形参
// 总数。带默认值或 `?` 的参数可以省略，但省略只能发生在参数列表末尾：后面
// 还留着必填参数时，前面的位置仍必须传值。
//
// 这是事实校验与能力检查唯一的参数数量规则。两边都调用 ArityOf，不再各自
// 写一遍 len(args) != len(params)，免得一边放开、另一边还卡着。
type Arity struct {
	Required int
	Total    int
}

// Allows 报告 got 个实参是否落在允许范围内。
func (a Arity) Allows(got int) bool {
	return got >= a.Required && got <= a.Total
}

// ArityOf 按声明表算出函数的实参数量范围。
//
// 下界取「最后一个必填参数的下标 + 1」。这样 `f(a: number = 1, b: number)`
// 的第一个参数虽然带默认值，后面还有必填参数，调用时仍要传两个值——
// TypeScript 要求显式传 undefined，不能省略。全部参数都可省略时下界是 0。
// 查不到声明的参数按必填处理，事实校验会另外报声明缺失。
func (f *Facts) ArityOf(callee *Declaration) Arity {
	total := len(callee.Parameters)
	required := 0
	for i, id := range callee.Parameters {
		param, ok := f.Declaration(id)
		if ok && (param.Optional || param.HasDefault) {
			continue
		}
		required = i + 1
	}
	return Arity{Required: required, Total: total}
}
