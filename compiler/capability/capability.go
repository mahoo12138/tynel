// Package capability 判断一份程序是否落在原生后端的当前子集内。
//
// 它回答的问题和类型检查不同：TypeScript 接受这段代码，不代表 Tynel 能把它
// 变成机器码。类型检查通过之后，这里再走一遍所有函数体——包括没有被调用的
// 函数——按白名单核对语法、类型和调用目标，把「暂时不支持」的写法挑出来。
//
// 它不做可达性分析。一个没人调用的函数里写了对象字面量，同样会被拒绝：今天
// 不可达不代表以后不会被调用，而「因为没调用所以没检查」是最难排查的一类漏。
package capability

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
	"github.com/Mahoo12138/tynel/compiler/frontend"
	"github.com/Mahoo12138/tynel/compiler/nir"
	"github.com/Mahoo12138/tynel/compiler/source"
)

// Check 遍历事实里的每一个声明与函数体，返回能力限制诊断。
//
// 诊断类别是 Capability（退出 3），与源码错误（退出 1）分开：前者是「写法合法
// 但后端还没实现」，后者是「代码本身有问题」。
func Check(snapshot *source.Snapshot, facts *frontend.Facts) ([]*diagnostic.Diagnostic, error) {
	// 先确认事实自洽。能力检查会按 ID 查声明，如果 ID 本身有问题，后面的
	// 结论都不可信。
	if err := facts.Validate(); err != nil {
		return nil, err
	}
	c := &checker{snapshot: snapshot, facts: facts, imports: valueImports(facts)}
	for _, alias := range facts.Aliases {
		switch alias.Kind {
		case frontend.KindNumber, frontend.KindBoolean, frontend.KindString:
			// 基础别名只活在类型空间，不产生运行时值。
		case frontend.KindObject:
			if reason := c.objectProblem(c.facts.ResolveType(alias.Type), 0); reason != "" {
				c.report(alias.Start, "cap.bad-type", "对象类型 %q 的%s", alias.Name, reason)
			}
		case frontend.KindArray:
			if reason := c.arrayProblem(c.facts.ResolveType(alias.Type), 0); reason != "" {
				c.report(alias.Start, "cap.bad-type", "数组类型 %q 的%s", alias.Name, reason)
			}
		case frontend.KindTuple:
			if reason := c.tupleProblem(c.facts.ResolveType(alias.Type), 0); reason != "" {
				c.report(alias.Start, "cap.bad-type", "元组类型 %q 的%s", alias.Name, reason)
			}
		case frontend.KindUnion:
			if reason := c.unionProblem(c.facts.ResolveType(alias.Type), 0); reason != "" {
				c.report(alias.Start, "cap.bad-type", "联合类型 %q 的%s", alias.Name, reason)
			}
		case "interface":
			if alias.Type == nil {
				c.report(alias.Start, "cap.interface", "接口 %q 没有可读取的结构摘要", alias.Name)
				continue
			}
			resolved := c.facts.ResolveType(alias.Type)
			if resolved.Kind != frontend.KindObject {
				c.report(alias.Start, "cap.interface", "接口 %q 最终是 %s，本阶段只支持对象形状", alias.Name, describeType(resolved))
				continue
			}
			if reason := c.objectProblem(resolved, 0); reason != "" {
				c.report(alias.Start, "cap.bad-type", "接口 %q 的%s", alias.Name, reason)
			}
		case frontend.KindAny:
			c.report(alias.Start, "cap.any", "类型别名 %q 是 any 或 unknown", alias.Name)
		default:
			description := string(alias.Kind)
			if alias.Type != nil {
				description = alias.Type.String()
			}
			c.report(alias.Start, "cap.bad-type", "类型别名 %q 最终是 %s，本阶段只支持标量与对象类型", alias.Name, description)
		}
	}
	for i := range facts.Declarations {
		decl := &facts.Declarations[i]
		c.declaration(decl)
		if decl.Body != nil {
			c.inFunction = decl.Kind == frontend.DeclFunction
			c.loopDepth = 0
			c.resultType = decl.Result
			c.node(decl.Body)
			c.inFunction = false
			c.resultType = nil
		}
		if c.err != nil {
			return nil, c.err
		}
	}
	if facts.Module != nil {
		c.inFunction = false
		c.loopDepth = 0
		c.node(facts.Module)
	}
	return c.findings, c.err
}

type checker struct {
	snapshot   *source.Snapshot
	facts      *frontend.Facts
	imports    map[importKey]bool
	findings   []*diagnostic.Diagnostic
	err        error
	inFunction bool
	loopDepth  int
	resultType *frontend.Type
}

// importKey 是一条值导入绑定的完整身份：目标模块的解析后路径、那个模块
// 导出表里的名字、以及本文件里写出来的本地名。少任何一段都会把不同来源的
// 符号混成一个。
type importKey struct {
	module   string
	exported string
	local    string
}

// valueImports 收集本文件的值导入。类型导入只在类型空间里存在，不能当作
// 运行时调用或值的来源；这里就把它排除掉。
func valueImports(facts *frontend.Facts) map[importKey]bool {
	out := make(map[importKey]bool, len(facts.Imports))
	for _, imp := range facts.Imports {
		if imp.TypeOnly {
			continue
		}
		out[importKey{module: imp.Module, exported: imp.Imported, local: imp.Local}] = true
	}
	return out
}

// EntryName 是固定宿主入口使用的名字。用户函数不能占用它；这不是 TypeScript
// 规则，因此必须在能力层明确拒绝，而不是让降低阶段报内部故障。
const EntryName = "main"

func (c *checker) declaration(d *frontend.Declaration) {
	switch d.Kind {
	case frontend.DeclFunction:
		if d.Name == EntryName {
			c.report(d.Start, "cap.entry-name", "用户函数不能占用固定入口名 %q", EntryName)
			return
		}
		if d.Body == nil {
			// ambient 声明与重载签名只有类型、没有实现。降低阶段需要函数体，
			// 提前在能力层拒绝，避免合法 TypeScript 写法变成内部故障。
			c.report(d.Start, "cap.unsupported-declaration", "函数 %q 只有声明没有实现（ambient 或重载签名），本阶段不支持", d.Name)
			return
		}
		c.declaredType(d.Result, d.Start, "函数 %q 的返回类型", d.Name, frontend.KindNumber, frontend.KindBoolean, frontend.KindString, frontend.KindObject, frontend.KindArray, frontend.KindTuple, frontend.KindUnion, frontend.KindVoid)
	case frontend.DeclParameter:
		// 默认参数与可选参数都需要后端合成缺省值，本阶段还没实现。先在这里
		// 明确拒绝，别让合法的 add(5) 一路走到降低阶段才报内部故障；事实层
		// 已经放宽了参数个数，所以这两条诊断是唯一挡住它们的关卡。
		if d.HasDefault {
			c.report(d.Start, "cap.unsupported-parameter", "参数 %q 带默认值，本阶段只支持必填参数", d.Name)
			return
		}
		if d.Optional {
			c.report(d.Start, "cap.unsupported-parameter", "参数 %q 是可选参数，本阶段只支持必填参数", d.Name)
			return
		}
		c.declaredType(d.Type, d.Start, "参数 %q ", d.Name, frontend.KindNumber, frontend.KindBoolean, frontend.KindString, frontend.KindObject, frontend.KindArray, frontend.KindTuple, frontend.KindUnion)
	case frontend.DeclConst:
		if d.Body == nil {
			// declare const 只有类型声明，没有初始化表达式；入口降低需要值。
			c.report(d.Start, "cap.unsupported-declaration", "常量 %q 没有初始化表达式（ambient 声明），本阶段不支持", d.Name)
			return
		}
		c.declaredType(d.Type, d.Start, "常量 %q ", d.Name, frontend.KindNumber, frontend.KindBoolean, frontend.KindString, frontend.KindObject, frontend.KindArray, frontend.KindTuple, frontend.KindUnion)
		if d.Body.Type != nil && d.Body.Type.Kind == frontend.KindVoid {
			c.report(d.Body.Start, "cap.void-value", "常量 %q 的初始值是 void 调用，没有可用的值", d.Name)
		}
	case frontend.DeclLocal, frontend.DeclLet:
		c.declaredType(d.Type, d.Start, "局部值 %q ", d.Name, frontend.KindNumber, frontend.KindBoolean, frontend.KindString, frontend.KindObject, frontend.KindArray, frontend.KindTuple, frontend.KindUnion)
	case frontend.DeclModuleLet:
		c.report(d.Start, "cap.module-let", "模块级 let %q 不在本阶段子集内；模块体只接受 const", d.Name)
	case frontend.DeclVar:
		c.report(d.Start, "cap.unsupported-syntax", "var %q 不在本阶段子集内；局部值只支持 const 与 let", d.Name)
	}
}

// declaredType 核对声明上的类型。允许的类别按阶段放开：参数与常量是 number
// 或 boolean，函数返回值再加 void。
//
// format 是带一个 %q 的主语，调用方决定「参数 X 」还是「函数 X 的返回类型」，
// 这样中文里不会出现「返回类型 是」这种多余的空格。
func (c *checker) declaredType(t *frontend.Type, offset int, format, name string, allowed ...frontend.Kind) {
	if t == nil {
		c.report(offset, "cap.bad-type", format+"缺少类型", name)
		return
	}
	if t.Kind == frontend.KindAny {
		c.report(offset, "cap.any", format+"是 any 或 unknown；本阶段只支持标量、对象、数组、元组和 void", name)
		return
	}
	resolved := c.facts.ResolveType(t)
	for _, k := range allowed {
		if resolved.Kind == k {
			// void 只表示「没有值」，不参与值类型的递归检查。
			if resolved.Kind == frontend.KindVoid {
				return
			}
			if reason := c.valueProblem(resolved, 0); reason != "" {
				c.report(offset, "cap.bad-type", format+"的%s", name, reason)
			}
			return
		}
	}
	c.report(offset, "cap.bad-type", format+"是 %s，本阶段不支持", name, describeType(resolved))
}

func (c *checker) node(n *frontend.Node) {
	if c.err != nil || n == nil {
		return
	}
	switch n.Kind {
	case "TypeAliasDeclaration", "InterfaceDeclaration":
		// 类型声明由 Aliases 单独核对，这里不进入运行值。
		return
	case "ImportDeclaration":
		// 命名导入与副作用导入由模块符号表核对；默认导入和命名空间导入不在
		// 子集内，明确拒绝，而不是让它们绕过绑定检查冒充内置对象。
		switch n.ImportKind {
		case "default":
			c.report(n.Start, "cap.unsupported-syntax", "默认导入不在本阶段子集内；只支持命名导入与副作用导入")
		case "namespace":
			c.report(n.Start, "cap.unsupported-syntax", "命名空间导入不在本阶段子集内；只支持命名导入与副作用导入")
		}
		return
	case "Block":
		c.children(n)
	case "Const":
		// 模块级常量本身没问题；它的初始化表达式挂在声明上，已经单独检查过。
		return
	case "ReturnStatement":
		if len(n.Children) > 1 {
			c.report(n.Start, "cap.unsupported-syntax", "return 有 %d 个值，超出本阶段支持的形状", len(n.Children))
			return
		}
		if len(n.Children) == 1 {
			value := &n.Children[0]
			c.valueNode(value, "返回值")
			if value.Type != nil && c.resultType != nil {
				want := c.facts.ResolveType(c.resultType)
				got := c.facts.ResolveType(value.Type)
				if want.Kind == frontend.KindObject && got.Kind == frontend.KindObject && !frontend.SameType(want, got) {
					c.report(value.Start, "cap.object-shape", "返回值需要 %s，实际为 %s；结构性放宽需要对象转换，本阶段只支持相同形状", describeType(want), describeType(got))
				}
			}
		}
	case "ExpressionStatement":
		// 结果被丢弃：表达式本身可以是 number，也可以是 void 调用。值不会被
		// 打印，只有副作用保留。
		if len(n.Children) != 1 {
			c.report(n.Start, "cap.unsupported-syntax", "表达式语句有 %d 个子节点", len(n.Children))
			return
		}
		inner := &n.Children[0]
		if inner.Kind == "BinaryExpression" && inner.Op == "=" {
			c.assignment(inner)
			return
		}
		c.node(inner)
	case "IfStatement":
		if !c.inFunction {
			c.report(n.Start, "cap.unsupported-syntax", "模块级 if 不在本阶段子集内")
			return
		}
		if len(n.Children) < 2 || len(n.Children) > 3 {
			c.report(n.Start, "cap.unsupported-syntax", "if 有 %d 个子节点", len(n.Children))
			return
		}
		c.boolOperand(&n.Children[0], "if 的条件")
		for i := 1; i < len(n.Children); i++ {
			c.node(&n.Children[i])
		}
	case "WhileStatement":
		if !c.inFunction {
			c.report(n.Start, "cap.unsupported-syntax", "模块级 while 不在本阶段子集内")
			return
		}
		if len(n.Children) != 2 {
			c.report(n.Start, "cap.unsupported-syntax", "while 有 %d 个子节点", len(n.Children))
			return
		}
		c.boolOperand(&n.Children[0], "while 的条件")
		c.loopDepth++
		c.node(&n.Children[1])
		c.loopDepth--
	case "LabeledStatement":
		c.report(n.Start, "cap.labeled-jump", "带标签的语句不在本阶段子集内")
	case "BreakStatement", "ContinueStatement":
		if len(n.Children) > 0 {
			c.report(n.Start, "cap.labeled-jump", "带标签的 %s 不在本阶段子集内", n.Kind)
			return
		}
		if c.loopDepth == 0 {
			c.report(n.Start, "cap.loop-target", "%s 不在循环里", n.Kind)
		}
	case "VariableStatement":
		if n.Decl != 0 {
			if decl, ok := c.facts.Declaration(n.Decl); ok {
				// 模块级 let/var 已经在 declaration 里报过一次，这里只跳过
				// 模块体节点，避免同一个声明产生两条诊断。
				if decl.Kind == frontend.DeclModuleLet || decl.Kind == frontend.DeclVar {
					return
				}
			}
		}
		list := variableList(n)
		if list == nil {
			c.report(n.Start, "cap.unsupported-syntax", "局部变量语句缺少声明列表")
			return
		}
		if len(list.Children) != 1 {
			c.report(n.Start, "cap.unsupported-syntax", "本阶段一次只支持声明一个局部值")
			return
		}
		c.node(list)
	case "VariableDeclarationList":
		c.children(n)
	case "VariableDeclaration":
		if n.Decl == 0 {
			c.report(n.Start, "cap.unsupported-syntax", "局部 var 不在本阶段子集内；局部值只支持 const 与 let")
			return
		}
		decl, ok := c.facts.Declaration(n.Decl)
		if !ok || (decl.Kind != frontend.DeclLocal && decl.Kind != frontend.DeclLet) {
			c.report(n.Start, "cap.unknown-name", "局部变量没有对应的声明")
			return
		}
		if len(n.Children) != 1 {
			c.report(n.Start, "cap.unsupported-syntax", "局部值 %q 必须有且只有一个初始值", decl.Name)
			return
		}
		if decl.Type == nil {
			c.report(n.Start, "cap.bad-type", "局部值 %q 缺少类型摘要", decl.Name)
			return
		}
		c.checkSameShape(&n.Children[0], decl.Type, "局部值 "+decl.Name)
		c.typedValue(&n.Children[0], decl.Type, "局部值 "+decl.Name)
	case "PostfixUnaryExpression":
		c.report(n.Start, "cap.inc-dec", "自增自减 %q 不在本阶段子集内", n.Op)
	case "ConditionalExpression":
		if len(n.Children) != 3 {
			c.report(n.Start, "cap.bad-arity", "三元式有 %d 个子节点", len(n.Children))
			return
		}
		c.boolOperand(&n.Children[0], "三元式的条件")
		thenNode, elseNode := &n.Children[1], &n.Children[2]
		if thenNode.Type == nil || elseNode.Type == nil {
			c.report(n.Start, "cap.bad-type", "三元式的分支缺少类型摘要")
			return
		}
		thenType := c.facts.ResolveType(thenNode.Type)
		elseType := c.facts.ResolveType(elseNode.Type)
		if !frontend.SameType(thenType, elseType) {
			c.report(n.Start, "cap.bad-type", "三元式两条分支类型不同：%s 与 %s", describeType(thenType), describeType(elseType))
			return
		}
		if reason := c.valueProblem(thenType, 0); reason != "" {
			c.report(n.Start, "cap.bad-type", "三元式结果的%s", reason)
			return
		}
		for i := range n.Children {
			c.node(&n.Children[i])
		}
	case "BinaryExpression":
		if n.Op == "=" {
			c.report(n.Start, "cap.assignment-value", "赋值只能作为独立语句，不能出现在表达式里")
			return
		}
		if isCompoundAssignment(n.Op) {
			c.report(n.Start, "cap.compound-assignment", "复合赋值 %q 不在本阶段子集内", n.Op)
			return
		}
		if len(n.Children) != 2 {
			c.report(n.Start, "cap.unsupported-syntax", "二元运算有 %d 个操作数", len(n.Children))
			return
		}
		switch {
		case n.Op == "+" && (isStringOperand(&n.Children[0]) || isStringOperand(&n.Children[1])):
			// string + string 是拼接；其余组合（"a" + 1）在 TypeScript 里合法
			// 但会触发隐式转换，本阶段明确拒绝，不生成 toString 调用。
			if !isStringOperand(&n.Children[0]) || !isStringOperand(&n.Children[1]) {
				c.report(n.Start, "cap.bad-type", "运算符 \"+\" 只支持两个 number 或两个 string，实际为 %s 与 %s", describeType(n.Children[0].Type), describeType(n.Children[1].Type))
				return
			}
			c.node(&n.Children[0])
			c.node(&n.Children[1])
		case isArithmetic(n.Op):
			for i := range n.Children {
				c.numberOperand(&n.Children[i], "运算符 "+n.Op+" 的操作数")
			}
		case nir.IsOrderingOperator(n.Op):
			for i := range n.Children {
				c.numberOperand(&n.Children[i], "比较 "+n.Op+" 的操作数")
			}
		case nir.IsEqualityOperator(n.Op):
			left, right := &n.Children[0], &n.Children[1]
			if _, ok := nullishKind(left); ok {
				c.nullCompare(n, left, right)
				return
			}
			if _, ok := nullishKind(right); ok {
				c.nullCompare(n, left, right)
				return
			}
			if left.Type == nil || right.Type == nil {
				c.report(n.Start, "cap.bad-type", "严格相等 %q 需要两侧同类型", n.Op)
				return
			}
			leftType := c.facts.ResolveType(left.Type)
			rightType := c.facts.ResolveType(right.Type)
			if !frontend.SameType(leftType, rightType) {
				c.report(n.Start, "cap.bad-type", "严格相等 %q 需要两侧同类型，实际为 %s 与 %s", n.Op, describeType(leftType), describeType(rightType))
				return
			}
			if !isValueKind(leftType.Kind) && leftType.Kind != frontend.KindObject {
				c.report(n.Start, "cap.bad-type", "严格相等 %q 只支持标量或对象引用；数组和元组尚未实现身份比较，实际为 %s", n.Op, describeType(leftType))
				return
			}
			if leftType.Kind == frontend.KindObject {
				if reason := c.objectProblem(leftType, 0); reason != "" {
					c.report(n.Start, "cap.bad-type", "对象比较的%s", reason)
					return
				}
			}
			c.node(left)
			c.node(right)
		case n.Op == "&&" || n.Op == "||":
			for i := range n.Children {
				c.boolOperand(&n.Children[i], "运算符 "+n.Op+" 的操作数")
			}
		case n.Op == "==" || n.Op == "!=":
			c.report(n.Start, "cap.bad-operator", "宽松相等 %q 不在本阶段子集内，请使用 ===", n.Op)
		default:
			c.report(n.Start, "cap.bad-operator", "运算符 %q 不在本阶段支持的 + - * / 与比较之内", n.Op)
		}
	case "PrefixUnaryExpression":
		if n.Op == "++" || n.Op == "--" {
			c.report(n.Start, "cap.inc-dec", "自增自减 %q 不在本阶段子集内", n.Op)
			return
		}
		if n.Op == "!" {
			if len(n.Children) != 1 {
				c.report(n.Start, "cap.unsupported-syntax", "一元运算有 %d 个操作数", len(n.Children))
				return
			}
			c.boolOperand(&n.Children[0], "! 的操作数")
			return
		}
		if n.Op != "-" && n.Op != "+" {
			c.report(n.Start, "cap.bad-operator", "一元运算符 %q 不在本阶段支持的范围之内", n.Op)
			return
		}
		if len(n.Children) != 1 {
			c.report(n.Start, "cap.unsupported-syntax", "一元运算有 %d 个操作数", len(n.Children))
			return
		}
		operand := &n.Children[0]
		if operand.Kind != "NumericLiteral" || operand.Bits == "" {
			c.report(n.Start, "cap.bad-unary", "一元 %q 只支持数字字面量；变量取负需要额外的取负操作", n.Op)
			return
		}
	case "Identifier":
		c.identifier(n)
	case "NumericLiteral", "TrueKeyword", "FalseKeyword":
		return
	case "CallExpression":
		c.call(n)
	case "PropertyAccessExpression":
		base := baseNode(n)
		if n.Name == "length" && base != nil && base.Type != nil {
			switch c.facts.ResolveType(base.Type).Kind {
			case frontend.KindString:
				c.stringLength(n)
				return
			case frontend.KindArray:
				c.arrayLength(n)
				return
			}
		}
		c.objectField(n)
	case "AsExpression", "TypeAssertionExpression", "NonNullExpression", "SatisfiesExpression":
		c.report(n.Start, "cap.type-escape", "类型断言 %s 绕过了类型检查，本阶段不支持", n.Kind)
	case "ObjectLiteralExpression":
		c.objectLiteral(n)
	case "ArrayLiteralExpression":
		c.arrayLiteral(n)
	case "ElementAccessExpression":
		c.indexAccess(n)
	case "NullKeyword", "UndefinedKeyword":
		// 空值本身只有类型；它只能出现在联合赋值、返回和空值比较里。
		return
	case "StringLiteral", "NoSubstitutionTemplateLiteral":
		return
	case "TemplateExpression":
		c.report(n.Start, "cap.unsupported-syntax", "模板字符串插值不在本阶段子集内")
	case "DoStatement", "ForStatement", "ForInStatement", "ForOfStatement", "SwitchStatement", "TryStatement", "ThrowStatement":
		c.report(n.Start, "cap.unsupported-syntax", "控制流 %s 不在本阶段子集内", n.Kind)
	case "NewExpression":
		c.report(n.Start, "cap.unsupported-syntax", "new 表达式不在本阶段子集内")
	case "FunctionExpression", "ArrowFunction", "ClassExpression":
		c.report(n.Start, "cap.unsupported-syntax", "嵌套函数与类不在本阶段子集内")
	case "FunctionDeclaration":
		// 顶层具名函数已被前端提取成声明；骨架里的函数声明是匿名、默认导出或嵌套声明。
		c.report(n.Start, "cap.unsupported-syntax", "函数声明不在本阶段子集内；只支持顶层具名函数声明")
	default:
		c.report(n.Start, "cap.unsupported-syntax", "语法 %s 不在本阶段子集内", n.Kind)
	}
}

// valueNode 检查一个处在「需要值」位置的表达式。void 调用只能单独成句或作为
// 丢弃结果的对象；出现在这里说明有人想用它的值，必须拒绝。
func (c *checker) valueNode(n *frontend.Node, what string) {
	if n == nil {
		return
	}
	if n.Type != nil && n.Type.Kind == frontend.KindVoid {
		c.report(n.Start, "cap.void-value", "%s需要一个值，但这里是 void 调用", what)
		return
	}
	c.node(n)
}

// boolOperand 要求一个表达式是 boolean。TypeScript 允许对任意值取反（!5 是
// false），但本阶段只把 boolean 的取反映射到原生指令，其余类型先拒绝。
func (c *checker) boolOperand(n *frontend.Node, what string) {
	c.typedOperand(n, frontend.KindBoolean, what)
}

// numberOperand 要求一个表达式是 number。算术与有序比较都靠它把「类型摘要
// 对不上」的伪造事实挡在降低之前。
func (c *checker) numberOperand(n *frontend.Node, what string) {
	c.typedOperand(n, frontend.KindNumber, what)
}

func (c *checker) typedOperand(n *frontend.Node, want frontend.Kind, what string) {
	if n == nil {
		return
	}
	if n.Type != nil && n.Type.Kind == frontend.KindVoid {
		c.report(n.Start, "cap.void-value", "%s需要一个值，但这里是 void 调用", what)
		return
	}
	if n.Type == nil || n.Type.Kind != want {
		c.report(n.Start, "cap.bad-type", "%s是 %s，需要 %s", what, describeType(n.Type), want)
		return
	}
	c.node(n)
}

// typedValue 检查一个值位置是否可以赋给目标类型。它比 typedOperand 多认识
// 联合成员到联合的赋值；对象、数组和元组的形状仍由 checkSameShape 负责。
func (c *checker) typedValue(n *frontend.Node, want *frontend.Type, what string) {
	if n == nil || want == nil {
		return
	}
	if n.Type != nil && n.Type.Kind == frontend.KindVoid {
		c.report(n.Start, "cap.void-value", "%s需要一个值，但这里是 void 调用", what)
		return
	}
	if n.Type == nil || !c.assignableTo(n.Type, want) {
		c.report(n.Start, "cap.bad-type", "%s是 %s，需要 %s", what, describeType(n.Type), describeType(want))
		return
	}
	c.node(n)
}

// assignableTo 判断当前子集是否允许把 got 放进 want。它只处理同一形状和
// 「成员 -> 可空联合」两种情况；其他兼容规则留给 TypeScript。
func (c *checker) assignableTo(got, want *frontend.Type) bool {
	if got == nil || want == nil {
		return false
	}
	g := c.facts.ResolveType(got)
	w := c.facts.ResolveType(want)
	if frontend.SameType(g, w) {
		return true
	}
	payload, nullish, ok := nullableUnion(w)
	if !ok {
		return false
	}
	if g.Kind == nullish {
		return true
	}
	return frontend.SameType(g, payload)
}

// nullableUnion 识别本阶段支持的两成员联合：一个可执行成员加 null 或
// undefined。成员顺序不敏感。
func nullableUnion(t *frontend.Type) (*frontend.Type, frontend.Kind, bool) {
	if t == nil || t.Kind != frontend.KindUnion || len(t.Members) != 2 {
		return nil, "", false
	}
	var payload *frontend.Type
	var nullish frontend.Kind
	for i := range t.Members {
		member := &t.Members[i]
		switch member.Kind {
		case frontend.KindNull, frontend.KindUndefined:
			if nullish != "" {
				return nil, "", false
			}
			nullish = member.Kind
		default:
			if payload != nil {
				return nil, "", false
			}
			payload = member
		}
	}
	if payload == nil || nullish == "" {
		return nil, "", false
	}
	return payload, nullish, true
}

// nullishKind 报告节点是不是 null / undefined 值。
func nullishKind(n *frontend.Node) (frontend.Kind, bool) {
	if n == nil {
		return "", false
	}
	switch n.Kind {
	case "NullKeyword":
		return frontend.KindNull, true
	case "UndefinedKeyword":
		return frontend.KindUndefined, true
	}
	if n.Type != nil {
		switch n.Type.Kind {
		case frontend.KindNull, frontend.KindUndefined:
			return n.Type.Kind, true
		}
	}
	return "", false
}

// nullCompare 检查联合值与 null / undefined 的严格相等。两侧都是空值时由
// TypeScript 自行判定；一侧是联合时，联合必须恰好包含对应的空值成员。
func (c *checker) nullCompare(n, left, right *frontend.Node) {
	leftKind, leftNull := nullishKind(left)
	rightKind, rightNull := nullishKind(right)
	if leftNull && rightNull {
		c.node(left)
		c.node(right)
		return
	}
	unionNode := left
	kind := rightKind
	if rightNull {
		unionNode, kind = left, rightKind
	} else if leftNull {
		unionNode, kind = right, leftKind
	}
	if unionNode == nil || unionNode.Type == nil {
		c.report(n.Start, "cap.bad-type", "空值比较缺少联合值类型")
		return
	}
	unionType := c.facts.ResolveType(unionNode.Type)
	_, nullish, ok := nullableUnion(unionType)
	if !ok {
		if unionType.Kind == frontend.KindUnion {
			c.report(n.Start, "cap.bad-type", "空值比较需要可空联合，实际为 %s", describeType(unionType))
			return
		}
		// 已经缩窄成非联合类型时，比较结果是常量；TypeScript 已经允许。
		c.node(unionNode)
		return
	}
	if nullish != kind {
		c.report(n.Start, "cap.bad-type", "联合类型 %s 不包含 %s", describeType(unionType), kind)
		return
	}
	c.node(unionNode)
}

// describeType 把类型摘要转成诊断里的短描述。nil 不实现 String 的判空由这里
// 兜住，避免格式化和诊断本身因为缺字段而崩溃。
func describeType(t *frontend.Type) string {
	if t == nil {
		return "缺少类型"
	}
	return t.String()
}

func (c *checker) children(n *frontend.Node) {
	for i := range n.Children {
		c.node(&n.Children[i])
	}
}

// assignment 检查一条独立的 `x = value;`。赋值只支持这种语句形式；目标必须
// 是已初始化的 let，右值与目标同类型。
func (c *checker) assignment(n *frontend.Node) {
	if len(n.Children) != 2 {
		c.report(n.Start, "cap.bad-arity", "赋值有 %d 个子节点", len(n.Children))
		return
	}
	target, rhs := &n.Children[0], &n.Children[1]
	if target.Kind != "Identifier" {
		c.report(target.Start, "cap.assign-target", "赋值目标必须是直接名字")
		return
	}
	decl, ok := c.facts.Declaration(target.Target)
	if !ok || target.Target == 0 {
		c.report(target.Start, "cap.unknown-name", "赋值目标 %q 不是本文件里的声明", target.Name)
		return
	}
	if decl.Kind != frontend.DeclLet {
		c.report(target.Start, "cap.assign-immutable", "赋值目标 %q 是%s，不是可变的 let", target.Name, decl.Kind)
		return
	}
	if decl.Type == nil {
		c.report(target.Start, "cap.bad-type", "赋值目标 %q 缺少类型摘要", target.Name)
		return
	}
	if rhs.Type == nil || !c.assignableTo(rhs.Type, decl.Type) {
		c.report(rhs.Start, "cap.bad-type", "赋值目标 %q 是 %s，右值不是同一类型", target.Name, describeType(decl.Type))
		return
	}
	c.valueNode(rhs, "赋值右侧")
	c.node(rhs)
}

func (c *checker) identifier(n *frontend.Node) {
	if n.Name == "undefined" && n.Target == 0 && n.Type != nil && n.Type.Kind == frontend.KindUndefined {
		return
	}
	if n.Name == "null" && n.Target == 0 && n.Type != nil && n.Type.Kind == frontend.KindNull {
		return
	}
	decl, ok := c.facts.Declaration(n.Target)
	// 跨模块值引用只允许在模块初始化阶段；函数捕获模块值仍由
	// module.captured-variable 拒绝。跨模块函数调用不受这条限制，它会被
	// 重映射成 NIR 里的全局函数 ID，不需要闭包环境。
	if (!ok || n.Target == 0) && !c.inFunction && c.crossModuleUse(n, n.Name) {
		return
	}
	if !ok || n.Target == 0 {
		c.report(n.Start, "cap.unknown-name", "名称 %q 不是本文件里的参数或常量", n.Name)
		return
	}
	switch decl.Kind {
	case frontend.DeclFunction:
		c.report(n.Start, "cap.unsupported-syntax", "函数 %q 作为值使用不在本阶段子集内", n.Name)
	case frontend.DeclConst:
		if c.inFunction {
			c.report(n.Start, "cap.module-const-in-function", "函数内引用模块常量 %q 需要全局变量，本阶段不支持", n.Name)
		}
	case frontend.DeclLocal, frontend.DeclLet:
		// 局部 const 与已初始化的 let 可以在当前块及其子块里读取；越界读取
		// 由 TypeScript 报「找不到名称」，能力检查不重复做作用域分析。
	}
}

// crossModuleUse 判断一次引用能不能落到项目模块里真实导出的值上。
//
// 前端的 target() 从符号声明所在的文件取模块名，标准库声明文件也一样会给
// 出这个字段：`isFinite` 的 CalleeModule 是 bundled:///libs/lib.es5.d.ts，
// `Infinity` 的 TargetModule 也是它。所以「模块名非空」根本不是跨模块调用
// 的证据，只凭它放行会让降低阶段拿到一个 Target 为 0 的调用，最终报成
// lower.unknown-callee 这种内部故障。
//
// 能对上本文件的一条值导入，才说明目标模块是依赖图里解析过的真实模块、
// 名字在那个模块的导出表里解析到了声明（modules.Build 之后还会拿目标模块
// 的事实再复核一次）。Identifier 与 CallExpression 共用这套判定：前者查
// References，后者查 Calls，两者取出的 (模块, 导出名) 连源码里的本地名
// 一起交给 resolvesImport，不会出现两套不同的放行规则。
func (c *checker) crossModuleUse(n *frontend.Node, local string) bool {
	switch n.Kind {
	case "Identifier":
		for _, ref := range c.facts.References {
			if ref.Start == n.Start && ref.Name == local {
				return c.resolvesImport(ref.TargetModule, ref.TargetName, local)
			}
		}
	case "CallExpression":
		for _, call := range c.facts.Calls {
			if call.Start == n.Start {
				return c.resolvesImport(call.CalleeModule, call.CalleeName, local)
			}
		}
	}
	return false
}

// resolvesImport 核对一份前端解析结果是否对应本文件的一条值导入。
// 模块名等于当前模块时不是跨模块引用：那种调用应该由本文件的声明接住，
// 落到这里说明声明身份缺失，必须拒绝。
func (c *checker) resolvesImport(module, exported, local string) bool {
	if module == "" || exported == "" || local == "" || module == c.facts.ModulePath {
		return false
	}
	return c.imports[importKey{module: module, exported: exported, local: local}]
}

func (c *checker) call(n *frontend.Node) {
	if len(n.Children) == 0 {
		c.report(n.Start, "cap.unsupported-syntax", "调用缺少被调用者")
		return
	}
	callee := &n.Children[0]
	args := n.Children[1:]

	switch callee.Kind {
	case "Identifier":
		if callee.Name == "eval" && callee.Target == 0 {
			c.report(n.Start, "cap.forbidden-global", "eval 不在本阶段子集内")
			return
		}
		decl, ok := c.facts.Declaration(callee.Target)
		if (!ok || callee.Target == 0) && c.crossModuleUse(n, callee.Name) {
			for i := range args {
				c.valueNode(&args[i], "调用 "+callee.Name+" 的实参")
			}
			return
		}
		if !ok || callee.Target == 0 || decl.Kind != frontend.DeclFunction {
			c.report(n.Start, "cap.unknown-callee", "调用目标 %q 不是本文件里声明的函数", callee.Name)
			return
		}
		// 与事实校验共用同一套参数数量规则；能力检查拿到的事实可能不是刚从
		// 前端出来的那一份，不能假设上游一定检查过。可省略的尾部参数会先被
		// declaration 里的 cap.unsupported-parameter 拒绝，这里只核对范围。
		arity := c.facts.ArityOf(decl)
		if !arity.Allows(len(args)) {
			c.report(n.Start, "cap.arg-count", "调用 %q 给了 %d 个实参，函数声明需要 %d 到 %d 个", decl.Name, len(args), arity.Required, arity.Total)
			return
		}
		for i := range args {
			c.valueNode(&args[i], "调用 "+decl.Name+" 的实参")
			if i < len(decl.Parameters) {
				if param, ok := c.facts.Declaration(decl.Parameters[i]); ok {
					c.checkSameShape(&args[i], param.Type, "调用 "+decl.Name+" 的实参")
				}
			}
		}
	case "PropertyAccessExpression":
		c.consoleCall(n, callee, args)
	default:
		c.report(n.Start, "cap.unknown-callee", "调用目标不是直接函数名")
	}
}

// consoleCall 处理受限输出。名字叫 console.log 不代表它就是内置的那个：
// 如果 console 解析到了本文件里的声明，说明它被遮蔽了，必须拒绝。
func (c *checker) consoleCall(n *frontend.Node, callee *frontend.Node, args []frontend.Node) {
	if callee.Name != "log" || len(callee.Children) != 1 {
		c.report(n.Start, "cap.unknown-callee", "只支持 console.log，收到的是 .%s", callee.Name)
		return
	}
	base := &callee.Children[0]
	if base.Kind != "Identifier" || base.Name != "console" {
		c.report(n.Start, "cap.unknown-callee", "只支持 console.log，收到的是其他对象的方法")
		return
	}
	if !c.builtinConsole(base) {
		c.report(n.Start, "cap.shadowed-console", "console 被本地声明或导入绑定遮蔽，不是内置全局对象")
		return
	}
	if len(args) != 1 {
		c.report(n.Start, "cap.bad-console-call", "console.log 只接受一个参数，收到 %d 个", len(args))
		return
	}
	if args[0].Type != nil && args[0].Type.Kind == frontend.KindVoid {
		c.report(args[0].Start, "cap.void-value", "console.log 需要一个值，但这里是 void 调用")
		return
	}
	if args[0].Type == nil || !isValueKind(args[0].Type.Kind) {
		c.report(args[0].Start, "cap.bad-console-call", "console.log 只接受 number、boolean 或 string，实际为 %s", describeType(args[0].Type))
		return
	}
	c.node(&args[0])
}

// builtinConsole 报告 console 标识符是否解析到内置全局 console。仅凭名字和
// Target==0 不够：命名导入、命名空间导入的 console 也没有本地 Target。可靠
// 的判据是符号身份——本地声明会让 Target 非 0；内置 console 的名称引用解析
// 到声明库（bundled lib），而导入绑定解析到项目模块（命名空间导入甚至没有
// 目标）。没有名称引用时按内置处理：真实事实总会记录引用，这条只兜底手写
// 事实，且导入形状已在 ImportDeclaration 处单独把关。
func (c *checker) builtinConsole(base *frontend.Node) bool {
	if base.Target != 0 {
		return false
	}
	for _, ref := range c.facts.References {
		if ref.Start == base.Start && ref.Name == base.Name {
			return strings.HasPrefix(ref.TargetModule, "bundled:")
		}
	}
	return true
}

// checkSameShape 拒绝形状不同但 TypeScript 允许的结构性放宽。当前切片只
// 支持相同形状，避免生成需要字段复制、截断或元素转换的隐式转换。
func (c *checker) checkSameShape(value *frontend.Node, want *frontend.Type, what string) {
	if value == nil || value.Type == nil || want == nil {
		return
	}
	gotType := c.facts.ResolveType(value.Type)
	wantType := c.facts.ResolveType(want)
	if expectComposite(gotType.Kind) == "" && expectComposite(wantType.Kind) == "" {
		return
	}
	if frontend.SameType(gotType, wantType) {
		return
	}
	if gotType.Kind == frontend.KindObject || wantType.Kind == frontend.KindObject {
		c.report(value.Start, "cap.object-shape", "%s需要 %s，实际为 %s；结构性放宽需要对象转换，本阶段只支持相同形状", what, describeType(wantType), describeType(gotType))
		return
	}
	c.report(value.Start, "cap.bad-type", "%s需要 %s，实际为 %s；数组和元组当前只支持相同形状", what, describeType(wantType), describeType(gotType))
}

// expectComposite 报告哪些类别需要走形状检查。标量由 TypeScript 和降低器
// 的类别核对负责，这里不重复。
func expectComposite(k frontend.Kind) frontend.Kind {
	switch k {
	case frontend.KindObject, frontend.KindArray, frontend.KindTuple:
		return k
	}
	return ""
}

// objectLiteral 检查静态形状对象字面量。字段必须是 name: value，顺序保留；
// 简写、方法和展开不属于第一个对象切片。
func (c *checker) objectLiteral(n *frontend.Node) {
	if n.Type == nil {
		c.report(n.Start, "cap.bad-type", "对象字面量缺少类型摘要")
		return
	}
	typ := c.facts.ResolveType(n.Type)
	if typ.Kind != frontend.KindObject {
		c.report(n.Start, "cap.bad-type", "对象字面量的类型是 %s，不是静态对象", describeType(typ))
		return
	}
	if reason := c.objectProblem(typ, 0); reason != "" {
		c.report(n.Start, "cap.bad-type", "对象字面量的%s", reason)
		return
	}
	if len(n.Children) != len(typ.Properties) {
		c.report(n.Start, "cap.unsupported-syntax", "对象字面量有 %d 个可读字段，类型需要 %d 个；方法、访问器与展开不在本阶段子集内", len(n.Children), len(typ.Properties))
		return
	}
	seen := make(map[string]bool, len(n.Children))
	for i := range n.Children {
		field := &n.Children[i]
		if field.Kind != "PropertyAssignment" || field.Name == "" || len(field.Children) != 1 {
			c.report(field.Start, "cap.unsupported-syntax", "对象字段必须是 name: value 形式")
			continue
		}
		if seen[field.Name] {
			c.report(field.Start, "cap.duplicate-field", "对象字段 %q 重复", field.Name)
			continue
		}
		seen[field.Name] = true
		value := &field.Children[0]
		if prop := propertyNamed(typ, field.Name); prop != nil {
			c.checkSameShape(value, &prop.Type, "对象字段 "+field.Name)
		}
		c.valueNode(value, "对象字段 "+field.Name)
	}
}

// propertyNamed 按名字找对象属性；找不到返回 nil，由 TypeScript 或降低器
// 报告未知字段。
func propertyNamed(t *frontend.Type, name string) *frontend.Property {
	if t == nil {
		return nil
	}
	for i := range t.Properties {
		if t.Properties[i].Name == name {
			return &t.Properties[i]
		}
	}
	return nil
}

// objectField 检查对象属性读取。只接受静态形状里存在的必填属性；字符串
// length 由 stringLength 单独处理。
func (c *checker) objectField(n *frontend.Node) {
	if n.Name == "" || len(n.Children) != 1 {
		c.report(n.Start, "cap.unsupported-syntax", "属性访问 %q 缺少对象或名字", n.Name)
		return
	}
	base := &n.Children[0]
	if base.Type == nil {
		c.report(n.Start, "cap.bad-type", "属性访问的对象缺少类型摘要")
		return
	}
	baseType := c.facts.ResolveType(base.Type)
	if baseType.Kind != frontend.KindObject {
		c.report(n.Start, "cap.unsupported-syntax", "属性访问 %q 不在本阶段子集内；只支持 string 的 length 或对象字段", n.Name)
		return
	}
	if reason := c.objectProblem(baseType, 0); reason != "" {
		c.report(n.Start, "cap.bad-type", "属性访问对象的%s", reason)
		return
	}
	var prop *frontend.Property
	for i := range baseType.Properties {
		if baseType.Properties[i].Name == n.Name {
			prop = &baseType.Properties[i]
			break
		}
	}
	if prop == nil {
		c.report(n.Start, "cap.unknown-field", "类型 %s 没有属性 %q", describeType(baseType), n.Name)
		return
	}
	if prop.Optional {
		c.report(n.Start, "cap.unsupported-optional-property", "可选属性 %q 需要 undefined 表示，本阶段不支持", n.Name)
		return
	}
	if n.Type == nil || !frontend.SameType(c.facts.ResolveType(n.Type), c.facts.ResolveType(&prop.Type)) {
		c.report(n.Start, "cap.bad-type", "属性 %q 的类型与静态布局不一致", n.Name)
		return
	}
	c.node(base)
}

// objectProblem 返回对象类型不能进入当前切片的原因；空串表示可以。
func (c *checker) objectProblem(t *frontend.Type, depth int) string {
	if t == nil {
		return "缺少类型摘要"
	}
	if depth > 16 {
		return "超过类型嵌套预算"
	}
	if t.Kind != frontend.KindObject {
		return "不是 object 类型"
	}
	seen := make(map[string]bool, len(t.Properties))
	for i := range t.Properties {
		prop := &t.Properties[i]
		if prop.Name == "" {
			return "有属性缺少名字"
		}
		if seen[prop.Name] {
			return "有重复属性 " + prop.Name
		}
		seen[prop.Name] = true
		if prop.Optional {
			return "可选属性 " + prop.Name + " 需要 undefined 表示"
		}
		if reason := c.valueProblem(&prop.Type, depth+1); reason != "" {
			return "属性 " + prop.Name + " 的" + reason
		}
	}
	return ""
}

// valueProblem 返回一个类型不能进入当前执行子集的原因；空串表示可以。
// 标量、静态对象、同质数组和定长元组递归检查，其余类型仍然拒绝。
func (c *checker) valueProblem(t *frontend.Type, depth int) string {
	if t == nil {
		return "缺少类型摘要"
	}
	if depth > 16 {
		return "超过类型嵌套预算"
	}
	resolved := c.facts.ResolveType(t)
	switch resolved.Kind {
	case frontend.KindNumber, frontend.KindBoolean, frontend.KindString:
		return ""
	case frontend.KindObject:
		return c.objectProblem(resolved, depth)
	case frontend.KindArray:
		return c.arrayProblem(resolved, depth)
	case frontend.KindTuple:
		return c.tupleProblem(resolved, depth)
	case frontend.KindUnion:
		return c.unionProblem(resolved, depth)
	case frontend.KindAny:
		return "是 any 或 unknown 类型"
	}
	return "类型 " + string(resolved.Kind) + " 不在本阶段子集内"
}

// arrayProblem 检查同质数组。元素必须是能执行的值类型；数组本身在 bridge
// 里是 GC 可见的切片，元素中的指针保持可见。
func (c *checker) arrayProblem(t *frontend.Type, depth int) string {
	if t.Kind != frontend.KindArray {
		return "不是 array 类型"
	}
	if t.Element == nil {
		return "缺少元素类型"
	}
	if reason := c.valueProblem(t.Element, depth+1); reason != "" {
		return "元素" + reason
	}
	return ""
}

// tupleProblem 检查定长元组。每个位置都要有可执行的元素类型；空元组没有
// 布局，当前子集明确拒绝。
func (c *checker) tupleProblem(t *frontend.Type, depth int) string {
	if t.Kind != frontend.KindTuple {
		return "不是 tuple 类型"
	}
	if len(t.Elements) == 0 {
		return "是空元组，没有可执行布局"
	}
	for i := range t.Elements {
		if reason := c.valueProblem(&t.Elements[i], depth+1); reason != "" {
			return fmt.Sprintf("第 %d 个元素%s", i+1, reason)
		}
	}
	return ""
}

// unionProblem 检查首版联合：恰好一个可执行成员和一个 null/undefined。
func (c *checker) unionProblem(t *frontend.Type, depth int) string {
	payload, _, ok := nullableUnion(t)
	if !ok {
		return "不在本阶段子集内；只支持一个成员加 null 或 undefined 的两成员联合"
	}
	if payload.Kind == frontend.KindUnion {
		return "的成员仍是联合类型，本阶段只支持一层联合"
	}
	if reason := c.valueProblem(payload, depth+1); reason != "" {
		return "的可执行成员" + reason
	}
	return ""
}

// arrayLiteral 检查同质数组和定长元组的字面量。数组允许任意个数，元组必须
// 与类型位置一一对应；展开、空槽和异质元素仍拒绝。
func (c *checker) arrayLiteral(n *frontend.Node) {
	if n.Type == nil {
		c.report(n.Start, "cap.bad-type", "数组字面量缺少类型摘要")
		return
	}
	typ := c.facts.ResolveType(n.Type)
	switch typ.Kind {
	case frontend.KindArray:
		if reason := c.arrayProblem(typ, 0); reason != "" {
			c.report(n.Start, "cap.bad-type", "数组字面量的%s", reason)
			return
		}
		for i := range n.Children {
			child := &n.Children[i]
			if child.Kind == "SpreadElement" || child.Kind == "OmittedExpression" {
				c.report(child.Start, "cap.unsupported-syntax", "数组字面量只接受逐个列出的元素；展开与空槽不在本阶段子集内")
				continue
			}
			c.checkSameShape(child, typ.Element, "数组元素")
			c.valueNode(child, "数组元素")
		}
	case frontend.KindTuple:
		if reason := c.tupleProblem(typ, 0); reason != "" {
			c.report(n.Start, "cap.bad-type", "元组字面量的%s", reason)
			return
		}
		if len(n.Children) != len(typ.Elements) {
			c.report(n.Start, "cap.bad-type", "元组字面量有 %d 个元素，类型需要 %d 个", len(n.Children), len(typ.Elements))
			return
		}
		for i := range n.Children {
			child := &n.Children[i]
			if child.Kind == "SpreadElement" || child.Kind == "OmittedExpression" {
				c.report(child.Start, "cap.unsupported-syntax", "元组字面量只接受逐个列出的元素")
				continue
			}
			c.checkSameShape(child, &typ.Elements[i], "元组元素")
			c.valueNode(child, "元组元素")
		}
	default:
		c.report(n.Start, "cap.bad-type", "数组字面量的类型是 %s，不是数组或元组", describeType(typ))
	}
}

// arrayLength 检查数组的 length。结果必须是 number。
func (c *checker) arrayLength(n *frontend.Node) {
	if n.Name != "length" || len(n.Children) != 1 {
		c.report(n.Start, "cap.unsupported-syntax", "属性访问 %q 不在本阶段子集内；只支持 string 或数组的 length", n.Name)
		return
	}
	base := &n.Children[0]
	if base.Type == nil {
		c.report(n.Start, "cap.bad-type", "数组 length 缺少对象类型")
		return
	}
	baseType := c.facts.ResolveType(base.Type)
	if baseType.Kind != frontend.KindArray {
		c.report(n.Start, "cap.bad-type", "length 只支持 string 或数组，实际为 %s", describeType(baseType))
		return
	}
	if reason := c.arrayProblem(baseType, 0); reason != "" {
		c.report(n.Start, "cap.bad-type", "数组 length 的%s", reason)
		return
	}
	if n.Type == nil || n.Type.Kind != frontend.KindNumber {
		c.report(n.Start, "cap.bad-type", "数组 length 的结果应为 number")
		return
	}
	c.node(base)
}

// indexAccess 检查数组和元组的索引。数组下标必须是 number，运行时做边界
// 检查；元组下标必须是编译期整数，越界在能力检查阶段拒绝。
func (c *checker) indexAccess(n *frontend.Node) {
	if len(n.Children) != 2 || n.Type == nil {
		c.report(n.Start, "cap.unsupported-syntax", "索引访问缺少对象、下标或结果类型")
		return
	}
	base, index := &n.Children[0], &n.Children[1]
	if base.Type == nil {
		c.report(n.Start, "cap.bad-type", "索引访问的对象缺少类型摘要")
		return
	}
	baseType := c.facts.ResolveType(base.Type)
	switch baseType.Kind {
	case frontend.KindArray:
		if reason := c.arrayProblem(baseType, 0); reason != "" {
			c.report(n.Start, "cap.bad-type", "数组索引的%s", reason)
			return
		}
		c.numberOperand(index, "数组索引")
		if !frontend.SameType(c.facts.ResolveType(n.Type), c.facts.ResolveType(baseType.Element)) {
			c.report(n.Start, "cap.bad-type", "数组索引结果是 %s，元素类型是 %s", describeType(n.Type), describeType(baseType.Element))
			return
		}
		c.node(base)
		c.node(index)
	case frontend.KindTuple:
		if reason := c.tupleProblem(baseType, 0); reason != "" {
			c.report(n.Start, "cap.bad-type", "元组索引的%s", reason)
			return
		}
		slot, ok := literalIndex(index)
		if !ok {
			c.report(index.Start, "cap.unsupported-syntax", "元组索引必须是非负整数数字字面量")
			return
		}
		if slot >= len(baseType.Elements) {
			c.report(index.Start, "cap.index-bounds", "元组索引 %d 越界；长度是 %d", slot, len(baseType.Elements))
			return
		}
		if !frontend.SameType(c.facts.ResolveType(n.Type), c.facts.ResolveType(&baseType.Elements[slot])) {
			c.report(n.Start, "cap.bad-type", "元组索引结果是 %s，第 %d 个元素是 %s", describeType(n.Type), slot, describeType(&baseType.Elements[slot]))
			return
		}
		c.node(base)
	default:
		c.report(n.Start, "cap.unsupported-syntax", "索引访问只支持数组和元组；字符串索引等待 S2.4 的空值表示")
	}
}

// literalIndex 取出元组索引要求的非负整数数字字面量。
func literalIndex(n *frontend.Node) (int, bool) {
	if n == nil || n.Kind != "NumericLiteral" || n.Bits == "" {
		return 0, false
	}
	text := strings.TrimPrefix(strings.TrimPrefix(n.Bits, "0x"), "0X")
	bits, err := strconv.ParseUint(text, 16, 64)
	if err != nil {
		return 0, false
	}
	v := math.Float64frombits(bits)
	if math.IsNaN(v) || math.IsInf(v, 0) || v != math.Trunc(v) || v < 0 || v > 1<<31-1 {
		return 0, false
	}
	return int(v), true
}

// baseNode 返回属性访问的对象侧。
func baseNode(n *frontend.Node) *frontend.Node {
	if n == nil || len(n.Children) == 0 {
		return nil
	}
	return &n.Children[0]
}

// stringLength 只接受 string 上的 length 属性。其他属性访问、其他基类型
// 仍然按未实现语法拒绝。结果类型必须能交给 number 运算，否则说明事实摘要
// 与类型检查对不上。
func (c *checker) stringLength(n *frontend.Node) {
	if n.Name != "length" || len(n.Children) != 1 {
		c.report(n.Start, "cap.unsupported-syntax", "属性访问 %q 不在本阶段子集内；只支持 string 的 length", n.Name)
		return
	}
	base := &n.Children[0]
	if base.Type == nil || base.Type.Kind != frontend.KindString {
		c.report(n.Start, "cap.bad-type", "length 只支持 string，实际为 %s", describeType(base.Type))
		return
	}
	if n.Type == nil || n.Type.Kind != frontend.KindNumber {
		c.report(n.Start, "cap.bad-type", "string.length 的结果应为 number")
		return
	}
	c.node(base)
}

// isStringOperand 报告一个表达式的类型摘要是不是 string。缺摘要时不按
// string 处理，让后续的类型诊断给出原因。
func isStringOperand(n *frontend.Node) bool {
	return n != nil && n.Type != nil && n.Type.Kind == frontend.KindString
}

// isValueKind 报告一个事实类型是否属于当前阶段可执行的标量值。
func isValueKind(k frontend.Kind) bool {
	switch k {
	case frontend.KindNumber, frontend.KindBoolean, frontend.KindString:
		return true
	}
	return false
}

func (c *checker) report(offset int, code, format string, args ...any) {
	if c.err != nil {
		return
	}
	pos, err := c.snapshot.Position(offset)
	if err != nil {
		c.err = err
		return
	}
	c.findings = append(c.findings, diagnostic.New(diagnostic.Capability, code, format, args...).At(c.snapshot.Path(), pos.Line, pos.Column))
}

func isArithmetic(op string) bool {
	switch op {
	case "+", "-", "*", "/":
		return true
	}
	return false
}

func isCompoundAssignment(op string) bool {
	switch op {
	case "+=", "-=", "*=", "/=", "%=", "**=", "&=", "|=", "^=", "<<=", ">>=", ">>>=", "&&=", "||=", "??=":
		return true
	}
	return false
}

// variableList 找到 VariableStatement 下的声明列表。骨架可能来自手写事实，
// 不假设层级一定完整。
func variableList(n *frontend.Node) *frontend.Node {
	if n.Kind != "VariableStatement" {
		return nil
	}
	for i := range n.Children {
		if n.Children[i].Kind == "VariableDeclarationList" {
			return &n.Children[i]
		}
	}
	return nil
}
