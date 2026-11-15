package nir

import (
	"fmt"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
)

// Verify 检查模块的语义合法性，返回第一处错误。
//
// 它先按默认预算重做一次格式检查，再检查入口签名、声明唯一性、参数引用、
// 调用实参、返回要求和表达式类型。验证器不假设模块来自 JSON 解码：内存里
// 直接构造的结构同样要过这一关，因此坏引用和循环引用不能靠解码器兜住。
//
// 它不导入 tsgo 或 Go 内部对象，只看 NIR 自己的数据。
func Verify(module *Module) error {
	return VerifyWithLimits(module, DefaultLimits())
}

// VerifyWithLimits 与 Verify 相同，但使用调用方给出的预算。
func VerifyWithLimits(module *Module, limits Limits) error {
	if module == nil {
		return badf("nir.missing", "模块为空")
	}
	// 格式、字段、预算和「表达式必须是树」先过一遍。循环结构在这里被拒绝，
	// 后面的递归检查才不会卡住。
	if err := module.check(limits); err != nil {
		return err
	}

	c := &checker{
		funcs: make(map[string]*Function, len(module.Functions)),
		types: typeIndex(module.Types),
	}
	for i := range module.Functions {
		f := &module.Functions[i]
		if prev, exists := c.funcs[f.Name]; exists {
			return failAt("nir.duplicate-name", f.Pos,
				"函数 %q 重复声明，另一处在 %s", f.Name, describePos(prev.Pos))
		}
		c.funcs[f.Name] = f
	}

	entry, ok := c.funcs[module.Entry]
	if !ok {
		return badf("nir.entry-missing", "入口函数 %q 不存在", module.Entry)
	}
	if len(entry.Params) != 0 || entry.Result != TypeVoid {
		return failAt("nir.entry-signature", entry.Pos,
			"入口函数 %q 必须没有参数且返回 void，实际有 %d 个参数、返回 %s",
			entry.Name, len(entry.Params), typeName(entry.Result))
	}

	for i := range module.Functions {
		if err := c.function(&module.Functions[i]); err != nil {
			return err
		}
	}
	if err := c.debug(module.Debug); err != nil {
		return err
	}
	return nil
}

// checker 保存一次验证的函数表，供调用解析使用。
type checker struct {
	funcs map[string]*Function
	types map[int]*TypeDef
	loops []int
}

// function 检查一个函数体。返回值类型与参数表在这一层确定，表达式只做类型
// 推导，不生成任何东西。
func (c *checker) function(f *Function) error {
	c.loops = c.loops[:0]
	root := &scope{params: make(map[string]int, len(f.Params)), names: make(map[string]int), mutable: make(map[string]bool)}
	for i := range f.Params {
		p := &f.Params[i]
		if _, exists := root.params[p.Name]; exists {
			return failAt("nir.duplicate-name", p.Pos, "函数 %q 的参数 %q 重复", f.Name, p.Name)
		}
		root.params[p.Name] = p.Type
	}

	// 函数体与参数共用最外层作用域：函数体顶层再 let 一个同名值属于重复声明，
	// 嵌套块里的同名 let 才是遮蔽。
	if err := c.stmts(f.Body, root, f); err != nil {
		return err
	}

	// 有值函数必须覆盖所有返回路径；只有一条 return 不够，if 的两臂都结束
	// 才算完整。完整规则见 returns.go。
	if f.Result != TypeVoid && !BlockReturns(f.Body) {
		pos := FirstMissingReturn(f.Body)
		if pos == nil {
			pos = f.Pos
		}
		return failAt("nir.missing-return", pos, "函数 %q 返回 %s，但有路径没有 return", f.Name, typeName(f.Result))
	}
	return nil
}

// stmts 检查一个语句块。sc 是该块的可见名字；if 的两条分支各自开一个子作用域。
func (c *checker) stmts(stmts []Stmt, sc *scope, f *Function) error {
	for i := range stmts {
		s := &stmts[i]
		switch s.Op {
		case OpReturn:
			if f.Result == TypeVoid {
				if s.Value != nil {
					return failAt("nir.result-type", s.Pos, "void 函数 %q 的 return 不能带返回值", f.Name)
				}
				continue
			}
			if s.Value == nil {
				return failAt("nir.result-type", s.Pos, "函数 %q 返回 %s，return 必须带值", f.Name, typeName(f.Result))
			}
			got, err := c.exprType(s.Value, sc)
			if err != nil {
				return err
			}
			if got != f.Result {
				return failAt("nir.result-type", s.Pos, "函数 %q 声明返回 %s，实际返回 %s", f.Name, typeName(f.Result), typeName(got))
			}
		case OpPrint:
			got, err := c.exprType(s.Value, sc)
			if err != nil {
				return err
			}
			if got != TypeNumber && got != TypeBool && got != TypeString {
				return failAt("nir.print-type", s.Pos, "print 只接受 number、bool 或 string，实际为 %s", typeName(got))
			}
		case OpDiscard:
			// 结果被丢弃，类型只用来确认表达式本身成立：number、bool 与 void 都行。
			if _, err := c.exprType(s.Value, sc); err != nil {
				return err
			}
		case OpLet:
			got, err := c.exprType(s.Value, sc)
			if err != nil {
				return err
			}
			if !c.valueType(got) {
				return failAt("nir.local-type", s.Pos, "let %q 的值必须是值类型，实际为 %s", s.Name, typeName(got))
			}
			if _, exists := sc.names[s.Name]; exists {
				return failAt("nir.duplicate-name", s.Pos, "函数 %q 里 %q 重复定义", f.Name, s.Name)
			}
			if _, exists := sc.params[s.Name]; exists {
				return failAt("nir.duplicate-name", s.Pos, "函数 %q 里 %q 与参数重复", f.Name, s.Name)
			}
			sc.names[s.Name] = got
			sc.mutable[s.Name] = s.Mutable
		case OpObject:
			def, ok := c.types[s.Type]
			if !ok || def.Kind != TypeKindObject {
				return failAt("nir.type-kind", s.Pos, "object 的 type 不是 object 类型：%s", typeName(s.Type))
			}
			for i := range def.Properties {
				if def.Properties[i].Optional {
					return failAt("nir.object-field", s.Pos, "类型 %s 的可选属性 %q 需要 undefined 表示", typeName(s.Type), def.Properties[i].Name)
				}
			}
			if len(s.Fields) != len(def.Properties) {
				return failAt("nir.missing", s.Pos, "对象字面量有 %d 个字段，类型 %s 需要 %d 个", len(s.Fields), typeName(s.Type), len(def.Properties))
			}
			seenField := make(map[string]bool, len(s.Fields))
			for i := range s.Fields {
				field := &s.Fields[i]
				prop, ok := objectProperty(c.types, s.Type, field.Name)
				if !ok {
					return failAt("nir.unknown-field", s.Pos, "类型 %s 没有属性 %q", typeName(s.Type), field.Name)
				}
				if seenField[field.Name] {
					return failAt("nir.duplicate-name", s.Pos, "对象字面量的字段 %q 重复", field.Name)
				}
				seenField[field.Name] = true
				got, err := c.exprType(&field.Value, sc)
				if err != nil {
					return err
				}
				if got != prop.Type {
					return failAt("nir.object-field", s.Pos, "字段 %q 需要 %s，实际为 %s", field.Name, typeName(prop.Type), typeName(got))
				}
			}
			if _, exists := sc.names[s.Name]; exists {
				return failAt("nir.duplicate-name", s.Pos, "函数 %q 里 %q 重复定义", f.Name, s.Name)
			}
			if _, exists := sc.params[s.Name]; exists {
				return failAt("nir.duplicate-name", s.Pos, "函数 %q 里 %q 与参数重复", f.Name, s.Name)
			}
			sc.names[s.Name] = s.Type
		case OpSet:
			got, err := c.exprType(s.Value, sc)
			if err != nil {
				return err
			}
			want, ok := sc.lookupLocal(s.Name)
			if !ok {
				return failAt("nir.unknown-local", s.Pos, "set 的目标 %q 不是已定义的局部值", s.Name)
			}
			if !sc.lookupMutable(s.Name) {
				return failAt("nir.immutable-local", s.Pos, "set 的目标 %q 不是可变的 let", s.Name)
			}
			if got != want {
				return failAt("nir.assign-type", s.Pos, "set 的目标 %q 是 %s，右值是 %s", s.Name, typeName(want), typeName(got))
			}
		case OpWhile:
			for _, id := range c.loops {
				if id == s.ID {
					return failAt("nir.duplicate-loop", s.Pos, "循环 id %d 在同一函数里重复", s.ID)
				}
			}
			condScope := sc.child()
			if err := c.stmts(s.CondPre, condScope, f); err != nil {
				return err
			}
			cond, err := c.exprType(s.Cond, condScope)
			if err != nil {
				return err
			}
			if cond != TypeBool {
				return failAt("nir.condition-type", s.Pos, "while 的条件必须是 bool，实际为 %s", typeName(cond))
			}
			c.loops = append(c.loops, s.ID)
			err = c.stmts(s.Body, sc.child(), f)
			c.loops = c.loops[:len(c.loops)-1]
			if err != nil {
				return err
			}
		case OpBreak, OpContinue:
			if len(c.loops) == 0 || c.loops[len(c.loops)-1] != s.Target {
				return failAt("nir.loop-target", s.Pos, "%s 的目标 %d 不是当前循环", s.Op, s.Target)
			}
		case OpIf:
			got, err := c.exprType(s.Cond, sc)
			if err != nil {
				return err
			}
			if got != TypeBool {
				return failAt("nir.condition-type", s.Pos, "if 的条件必须是 bool，实际为 %s", typeName(got))
			}
			if err := c.stmts(s.Then, sc.child(), f); err != nil {
				return err
			}
			if len(s.Else) > 0 {
				if err := c.stmts(s.Else, sc.child(), f); err != nil {
					return err
				}
			}
		case OpShort:
			left, err := c.exprType(s.Left, sc)
			if err != nil {
				return err
			}
			if left != TypeBool {
				return failAt("nir.operand-type", s.Pos, "short %q 的左侧必须是 bool，实际为 %s", s.Operator, typeName(left))
			}
			// 右侧的准备工作只在这里执行，局部值也只在这里可见；value 在同一
			// 个子作用域里求值，随后赋给外层的 name。
			right := sc.child()
			if err := c.stmts(s.Right, right, f); err != nil {
				return err
			}
			value, err := c.exprType(s.Value, right)
			if err != nil {
				return err
			}
			if value != TypeBool {
				return failAt("nir.operand-type", s.Pos, "short %q 的右侧必须是 bool，实际为 %s", s.Operator, typeName(value))
			}
			if _, exists := sc.names[s.Name]; exists {
				return failAt("nir.duplicate-name", s.Pos, "函数 %q 里 %q 重复定义", f.Name, s.Name)
			}
			if _, exists := sc.params[s.Name]; exists {
				return failAt("nir.duplicate-name", s.Pos, "函数 %q 里 %q 与参数重复", f.Name, s.Name)
			}
			sc.names[s.Name] = TypeBool
		case OpSelect:
			cond, err := c.exprType(s.Cond, sc)
			if err != nil {
				return err
			}
			if cond != TypeBool {
				return failAt("nir.condition-type", s.Pos, "select 的条件必须是 bool，实际为 %s", typeName(cond))
			}
			thenScope := sc.child()
			if err := c.stmts(s.Then, thenScope, f); err != nil {
				return err
			}
			thenType, err := c.exprType(s.ThenValue, thenScope)
			if err != nil {
				return err
			}
			elseScope := sc.child()
			if err := c.stmts(s.Else, elseScope, f); err != nil {
				return err
			}
			elseType, err := c.exprType(s.ElseValue, elseScope)
			if err != nil {
				return err
			}
			if thenType != elseType {
				return failAt("nir.result-type", s.Pos, "select 的两条分支类型不同：%s 与 %s", typeName(thenType), typeName(elseType))
			}
			if thenType != s.Type {
				return failAt("nir.result-type", s.Pos, "select 声明结果 %s，两条分支是 %s", typeName(s.Type), typeName(thenType))
			}
			if !c.valueType(thenType) {
				return failAt("nir.result-type", s.Pos, "select 的结果不能是 %s", typeName(thenType))
			}
			if _, exists := sc.names[s.Name]; exists {
				return failAt("nir.duplicate-name", s.Pos, "函数 %q 里 %q 重复定义", f.Name, s.Name)
			}
			if _, exists := sc.params[s.Name]; exists {
				return failAt("nir.duplicate-name", s.Pos, "函数 %q 里 %q 与参数重复", f.Name, s.Name)
			}
			sc.names[s.Name] = thenType
		}
	}
	return nil
}

// valueType 报告类型 ID 是否是当前可执行的局部值类型：标量或对象引用。
func (c *checker) valueType(id int) bool {
	switch id {
	case TypeNumber, TypeBool, TypeString:
		return true
	}
	return isObjectType(c.types, id)
}

// scope 是语句块内的可见名字。params 只在函数根作用域非空；names 是当前块
// 的局部值。子块可以遮蔽父块，但同名声明不能出现在同一个块里。
type scope struct {
	parent  *scope
	params  map[string]int
	names   map[string]int
	mutable map[string]bool
}

func (s *scope) child() *scope {
	return &scope{parent: s, names: make(map[string]int), mutable: make(map[string]bool)}
}

func (s *scope) lookupLocal(name string) (int, bool) {
	for cur := s; cur != nil; cur = cur.parent {
		if t, ok := cur.names[name]; ok {
			return t, true
		}
	}
	return 0, false
}

func (s *scope) lookupParam(name string) (int, bool) {
	for cur := s; cur != nil; cur = cur.parent {
		if cur.params == nil {
			continue
		}
		if t, ok := cur.params[name]; ok {
			return t, true
		}
	}
	return 0, false
}

func (s *scope) lookupMutable(name string) bool {
	for cur := s; cur != nil; cur = cur.parent {
		if _, ok := cur.names[name]; ok {
			return cur.mutable[name]
		}
	}
	return false
}

// exprType 推导表达式类型。返回 TypeNumber、TypeBool 或 TypeVoid（void 调用）。
func (c *checker) exprType(e *Expr, sc *scope) (int, error) {
	if e == nil {
		return 0, badf("nir.missing", "表达式为空")
	}
	switch e.Op {
	case OpNumber:
		return TypeNumber, nil
	case OpBool:
		if _, ok := e.BoolValue(); !ok {
			return 0, badf("nir.missing", "bool 缺少布尔 value")
		}
		return TypeBool, nil
	case OpString:
		if _, ok := e.StringValue(); !ok {
			return 0, badf("nir.missing", "string 缺少字符串 value")
		}
		return TypeString, nil
	case OpParam:
		t, ok := sc.lookupParam(e.Name)
		if !ok {
			return 0, failAt("nir.unknown-param", e.Pos, "引用了不存在的参数 %q", e.Name)
		}
		return t, nil
	case OpLocal:
		t, ok := sc.lookupLocal(e.Name)
		if !ok {
			return 0, failAt("nir.unknown-local", e.Pos, "引用了尚未定义的局部值 %q", e.Name)
		}
		return t, nil
	case OpNot:
		if e.Operand == nil {
			return 0, failAt("nir.missing", e.Pos, "not 缺少操作数")
		}
		got, err := c.exprType(e.Operand, sc)
		if err != nil {
			return 0, err
		}
		if got != TypeBool {
			return 0, failAt("nir.operand-type", e.Operand.Pos, "not 的操作数必须是 bool，实际为 %s", typeName(got))
		}
		return TypeBool, nil
	case OpCall:
		target, ok := c.funcs[e.Function]
		if !ok {
			return 0, failAt("nir.unknown-function", e.Pos, "调用了不存在的函数 %q", e.Function)
		}
		if len(e.Args) != len(target.Params) {
			return 0, failAt("nir.arg-count", e.Pos,
				"调用 %q 需要 %d 个实参，实际 %d 个", e.Function, len(target.Params), len(e.Args))
		}
		for i := range e.Args {
			got, err := c.exprType(&e.Args[i], sc)
			if err != nil {
				return 0, err
			}
			if want := target.Params[i].Type; got != want {
				return 0, failAt("nir.arg-type", argPos(&e.Args[i], e.Pos),
					"调用 %q 的第 %d 个实参需要 %s，实际为 %s", e.Function, i+1, typeName(want), typeName(got))
			}
		}
		return target.Result, nil
	case OpField:
		if e.Name == "" || e.Operand == nil {
			return 0, failAt("nir.missing", e.Pos, "field 缺少 name 或 operand")
		}
		base, err := c.exprType(e.Operand, sc)
		if err != nil {
			return 0, err
		}
		prop, ok := objectProperty(c.types, base, e.Name)
		if !ok {
			return 0, failAt("nir.unknown-field", e.Pos, "类型 %s 没有属性 %q", typeName(base), e.Name)
		}
		if prop.Optional {
			return 0, failAt("nir.object-field", e.Pos, "可选属性 %q 需要 undefined 表示", e.Name)
		}
		return prop.Type, nil
	case OpConcat:
		if e.Left == nil || e.Right == nil {
			return 0, failAt("nir.missing", e.Pos, "concat 缺少左或右操作数")
		}
		for _, side := range []*Expr{e.Left, e.Right} {
			got, err := c.exprType(side, sc)
			if err != nil {
				return 0, err
			}
			if got != TypeString {
				return 0, failAt("nir.operand-type", side.Pos, "concat 的操作数必须是 string，实际为 %s", typeName(got))
			}
		}
		return TypeString, nil
	case OpStrlen:
		if e.Operand == nil {
			return 0, failAt("nir.missing", e.Pos, "strlen 缺少操作数")
		}
		got, err := c.exprType(e.Operand, sc)
		if err != nil {
			return 0, err
		}
		if got != TypeString {
			return 0, failAt("nir.operand-type", e.Operand.Pos, "strlen 的操作数必须是 string，实际为 %s", typeName(got))
		}
		return TypeNumber, nil
	case OpBinary:
		for _, side := range []struct {
			name string
			expr *Expr
		}{{"左", e.Left}, {"右", e.Right}} {
			if side.expr == nil {
				return 0, failAt("nir.missing", e.Pos, "binary 缺少%s操作数", side.name)
			}
			got, err := c.exprType(side.expr, sc)
			if err != nil {
				return 0, err
			}
			if got != TypeNumber {
				return 0, failAt("nir.operand-type", side.expr.Pos,
					"运算符 %q 的%s操作数必须是 number，实际为 %s", e.Operator, side.name, typeName(got))
			}
		}
		return TypeNumber, nil
	case OpCompare:
		if e.Left == nil || e.Right == nil {
			return 0, failAt("nir.missing", e.Pos, "compare 缺少左或右操作数")
		}
		left, err := c.exprType(e.Left, sc)
		if err != nil {
			return 0, err
		}
		right, err := c.exprType(e.Right, sc)
		if err != nil {
			return 0, err
		}
		if IsOrderingOperator(e.Operator) {
			if left != TypeNumber || right != TypeNumber {
				return 0, failAt("nir.operand-type", e.Pos,
					"有序比较 %q 需要两个 number，实际为 %s 与 %s", e.Operator, typeName(left), typeName(right))
			}
			return TypeBool, nil
		}
		if left != right {
			return 0, failAt("nir.operand-type", e.Pos,
				"严格相等 %q 需要两侧同类型，实际为 %s 与 %s", e.Operator, typeName(left), typeName(right))
		}
		if left != TypeNumber && left != TypeBool && left != TypeString && !isObjectType(c.types, left) {
			return 0, failAt("nir.operand-type", e.Pos,
				"严格相等 %q 只接受标量或对象引用，实际为 %s", e.Operator, typeName(left))
		}
		return TypeBool, nil
	default:
		return 0, failAt("nir.bad-op", e.Pos, "未知表达式 op %q", e.Op)
	}
}

// argPos 优先用实参自己的位置，没有就退回调用节点的位置。
func argPos(arg *Expr, fallback *Pos) *Pos {
	if arg.Pos != nil {
		return arg.Pos
	}
	return fallback
}

func failAt(code string, pos *Pos, format string, args ...any) *diagnostic.Diagnostic {
	d := badf(code, format, args...)
	if pos != nil {
		d = d.At(pos.File, pos.Line, pos.Column)
	}
	return d
}

func describePos(pos *Pos) string {
	if pos == nil {
		return "未记录位置"
	}
	return fmt.Sprintf("%s:%d:%d", pos.File, pos.Line, pos.Column)
}
