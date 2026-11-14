// Package lower 把前端事实降低成 NIR。
//
// 它是编译流程里唯一同时认识「事实」和「NIR」的地方：前端的事实按源码顺序
// 被摊平成 NIR 语句，模块级 const 变成局部值，函数体变成 return 与 print。
// 降低发生在检查会话关闭之后，只依赖事实里的普通数据。
//
// 降低不重复做类型检查，但它不假设事实一定合法：类型不对、操作数个数不对、
// 引用了作用域外的名字、嵌套过深，都会在这里报错并返回 nil 模块。任何一步
// 失败都不会留下半份可用的 NIR。
package lower

import (
	"fmt"
	"strings"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
	"github.com/Mahoo12138/tynel/compiler/frontend"
	"github.com/Mahoo12138/tynel/compiler/nir"
	"github.com/Mahoo12138/tynel/compiler/source"
)

// EntryName 是模块入口的名字。用户函数不能占用它。
const EntryName = "main"

// mergedInitName 是合并降低里共享的初始化函数名。所有模块体按顺序降进它，
// 模块级 const 在其中求值一次，供后续模块读取。
const mergedInitName = "__init"

// maxDepth 是表达式嵌套的降低上限。事实校验已经限制过一次，这里再兜一层：
// 降低是递归的，深树会先把栈吃光。
const maxDepth = 100

// Lower 把事实降低成模块。失败时返回 nil，不返回半个模块。
func Lower(snapshot *source.Snapshot, facts *frontend.Facts) (*nir.Module, error) {
	if snapshot == nil || facts == nil {
		return nil, diagnostic.New(diagnostic.Internal, "lower.missing-input", "降低缺少源码快照或事实")
	}
	if err := facts.Validate(); err != nil {
		return nil, err
	}

	l := &lowerer{
		snapshot: snapshot,
		facts:    facts,
		names:    make(map[int]string, len(facts.Declarations)),
		debug:    &nir.DebugInfo{},
	}
	l.reserveNames(facts)
	module := &nir.Module{SchemaVersion: nir.SchemaVersion, Types: []nir.TypeDef{}, Entry: EntryName}

	for i := range facts.Declarations {
		decl := &facts.Declarations[i]
		if decl.Kind != frontend.DeclFunction {
			continue
		}
		if decl.Name == EntryName {
			return nil, l.fail(decl.Start, "lower.entry-conflict", "用户函数占用了入口名 %q", EntryName)
		}
		l.names[decl.ID] = decl.Name
	}

	for i := range facts.Declarations {
		decl := &facts.Declarations[i]
		if decl.Kind != frontend.DeclFunction {
			continue
		}
		fn, err := l.function(decl)
		if err != nil {
			return nil, err
		}
		module.Functions = append(module.Functions, *fn)
	}

	entry, err := l.entry()
	if err != nil {
		return nil, err
	}
	module.Functions = append(module.Functions, *entry)
	if len(l.debug.Functions) > 0 {
		module.Debug = l.debug
	}

	if err := nir.Verify(module); err != nil {
		// 走到这里说明降低产出了自己都不认的模块，属于内部故障。
		return nil, diagnostic.New(diagnostic.Internal, "lower.invalid-output", "降低产出的 NIR 没有通过验证").WithDetail(err.Error())
	}
	return module, nil
}

// LowerMerged 把一个或多个模块的事实降低成单个 NIR。所有模块体按 order 降进
// 同一个初始化函数 __init，共享一个作用域：模块级 const 求值一次并绑定成带
// 模块前缀的局部值，跨模块读取按声明 ID 命中同一个局部值，不会重新执行初始化
// 表达式。入口 main 调用 __init；函数名已经由 merge 加了模块前缀。
func LowerMerged(facts *frontend.Facts, snapshots map[string]*source.Snapshot, bodies map[string]*frontend.Node, order []string) (*nir.Module, error) {
	if facts == nil || len(order) == 0 {
		return nil, diagnostic.New(diagnostic.Internal, "lower.missing-input", "合并降低缺少事实或模块顺序")
	}
	if err := facts.Validate(); err != nil {
		return nil, err
	}
	l := &lowerer{
		snapshots: snapshots,
		facts:     facts,
		names:     make(map[int]string, len(facts.Declarations)),
		debug:     &nir.DebugInfo{},
	}
	l.reserveNames(facts)
	if len(snapshots) > 0 {
		l.snapshot = snapshots[order[0]]
	}
	module := &nir.Module{SchemaVersion: nir.SchemaVersion, Types: []nir.TypeDef{}, Entry: EntryName}

	for i := range facts.Declarations {
		decl := &facts.Declarations[i]
		if decl.Kind == frontend.DeclFunction {
			l.names[decl.ID] = decl.Name
		}
	}
	for i := range facts.Declarations {
		decl := &facts.Declarations[i]
		if decl.Kind != frontend.DeclFunction {
			continue
		}
		fn, err := l.function(decl)
		if err != nil {
			return nil, err
		}
		module.Functions = append(module.Functions, *fn)
	}

	initFn := &nir.Function{Name: mergedInitName, Result: nir.TypeVoid}
	// __init 的根作用域以第一个模块体为准；函数降低可能已经把 l.snapshot
	// 切到别的文件，必须先切回来，否则根作用域的行列会落到错误的快照上。
	if snapshot := snapshots[order[0]]; snapshot != nil {
		l.snapshot = snapshot
	}
	l.beginDebugFunction(mergedInitName)
	start, length := 0, 0
	if body := bodies[order[0]]; body != nil {
		start, length = body.Start, body.Length
	}
	sc := l.rootScope(start, length)
	sc.params = map[int]string{}
	l.inInit = true
	for i, path := range order {
		// 模块前缀让不同模块的同名常量在共享作用域里不冲突；声明 ID 仍是
		// 全局唯一身份，跨模块读取按 ID 命中同一个局部值。
		stmts, err := l.initBody(bodies[path], path, sc, fmt.Sprintf("m%d$", i))
		if err != nil {
			return nil, err
		}
		initFn.Body = append(initFn.Body, stmts...)
	}
	l.inInit = false
	l.finishDebugFunction()
	initFn.Body = append(initFn.Body, nir.Stmt{Op: nir.OpReturn})
	module.Functions = append(module.Functions, *initFn)

	mainFn := &nir.Function{Name: EntryName, Result: nir.TypeVoid}
	mainFn.Body = append(mainFn.Body, nir.Stmt{
		Op:    nir.OpDiscard,
		Value: &nir.Expr{Op: nir.OpCall, Function: mergedInitName},
	})
	mainFn.Body = append(mainFn.Body, nir.Stmt{Op: nir.OpReturn})
	module.Functions = append(module.Functions, *mainFn)
	if len(l.debug.Functions) > 0 {
		module.Debug = l.debug
	}

	if err := nir.Verify(module); err != nil {
		return nil, diagnostic.New(diagnostic.Internal, "lower.invalid-output", "合并降低产出的 NIR 没有通过验证").WithDetail(err.Error())
	}
	return module, nil
}

type lowerer struct {
	snapshot      *source.Snapshot
	snapshots     map[string]*source.Snapshot
	facts         *frontend.Facts
	currentModule string
	inInit        bool
	names         map[int]string  // 函数声明 ID → NIR 函数名
	result        int             // 当前正在降低的函数的返回类型 ID
	temp          int             // 短路结果与实参物化的临时名序号
	reserved      map[string]bool // 用户声明占用的名字，临时名必须绕开
	loops         []int           // 当前函数内由内到外的循环 id
	nextLoop      int             // 下一个循环 id
	debug         *nir.DebugInfo  // 整个模块的调试段
	debugFn       *nir.DebugFunction
	nextScope     int // 当前函数内下一个作用域 id
}

// scope 是当前可见的声明。用声明 ID 而不是名字做键：两个函数里同名的参数是
// 两个声明，按名字查会把它们混成一个。params 只在函数根作用域出现；locals
// 按语句块分层，子块可以遮蔽父块，退出子块后声明不可见。
type scope struct {
	parent  *scope
	id      int
	path    string
	params  map[int]string
	locals  map[int]string
	mutable map[int]bool
}

func (s *scope) lookup(id int) (string, bool) {
	for cur := s; cur != nil; cur = cur.parent {
		if name, ok := cur.locals[id]; ok {
			return name, true
		}
	}
	return "", false
}

func (s *scope) lookupParam(id int) (string, bool) {
	for cur := s; cur != nil; cur = cur.parent {
		if cur.params == nil {
			continue
		}
		if name, ok := cur.params[id]; ok {
			return name, true
		}
	}
	return "", false
}

func (s *scope) define(id int, name string, mutable bool) bool {
	if _, exists := s.locals[id]; exists {
		return false
	}
	s.locals[id] = name
	s.mutable[id] = mutable
	return true
}

func (s *scope) isMutable(id int) bool {
	for cur := s; cur != nil; cur = cur.parent {
		if _, ok := cur.locals[id]; ok {
			return cur.mutable[id]
		}
	}
	return false
}

// beginDebugFunction 开始记录一个函数的调试信息。根作用域由 rootScope 建立；
// 函数结束前必须调用 finishDebugFunction，否则调试段不会进入模块。
func (l *lowerer) beginDebugFunction(name string) {
	l.debugFn = &nir.DebugFunction{Function: name}
	l.nextScope = 0
}

func (l *lowerer) finishDebugFunction() {
	if l.debugFn == nil {
		return
	}
	l.debug.Functions = append(l.debug.Functions, *l.debugFn)
	l.debugFn = nil
}

// rootScope 建立函数根作用域，id 固定为 1。
func (l *lowerer) rootScope(start, length int) *scope {
	return l.newScope(nil, nir.DebugScopeFunction, "function", start, length)
}

// childScope 建立子作用域。node 提供源码范围；node 为空时退化为零长度范围，
// 只用于不来自源码的合成作用域。
func (l *lowerer) childScope(parent *scope, kind string, node *frontend.Node) *scope {
	if node == nil {
		return l.newScope(parent, kind, l.debugPath(parent, kind), 0, 0)
	}
	return l.newScope(parent, kind, l.debugPath(parent, kind), node.Start, node.Length)
}

func (l *lowerer) newScope(parent *scope, kind, path string, start, length int) *scope {
	l.nextScope++
	id := l.nextScope
	parentID := 0
	if parent != nil {
		parentID = parent.id
	}
	l.debugFn.Scopes = append(l.debugFn.Scopes, nir.DebugScope{
		ID:     id,
		Parent: parentID,
		Path:   path,
		Kind:   kind,
		Start:  l.pos(start),
		End:    l.pos(start + length),
	})
	return &scope{
		parent:  parent,
		id:      id,
		path:    path,
		locals:  make(map[int]string),
		mutable: make(map[int]bool),
	}
}

// debugPath 生成人类可读的作用域路径。序号只在同一父作用域、同一 kind 内
// 递增；它不是跨版本稳定标识，只用于同一次编译结果里的阅读和排查。
func (l *lowerer) debugPath(parent *scope, kind string) string {
	if parent == nil {
		return "function"
	}
	ordinal := 0
	for _, s := range l.debugFn.Scopes {
		if s.Parent == parent.id && s.Kind == kind {
			ordinal++
		}
	}
	return fmt.Sprintf("%s/%s#%d", parent.path, kind, ordinal+1)
}

func (l *lowerer) addDebugLocal(sc *scope, name, sourceName, kind string, typ int, pos *nir.Pos, mutable bool) {
	if l.debugFn == nil || sc == nil {
		return
	}
	l.debugFn.Locals = append(l.debugFn.Locals, nir.DebugLocal{
		Name:       name,
		SourceName: sourceName,
		Kind:       kind,
		Type:       typ,
		Scope:      sc.id,
		Pos:        pos,
		Mutable:    mutable,
	})
}

// emitLet 同时生成 NIR let 和调试局部值。所有 let 都应经过这里，避免执行
// 语句和调试信息两条路径各自维护。
func (l *lowerer) emitLet(sc *scope, name string, value *nir.Expr, pos *nir.Pos, sourceName, kind string, typ int, mutable bool) nir.Stmt {
	l.addDebugLocal(sc, name, sourceName, kind, typ, pos, mutable)
	return nir.Stmt{Op: nir.OpLet, Name: name, Value: value, Mutable: mutable, Pos: pos}
}

func (l *lowerer) emitTemp(sc *scope, name string, value *nir.Expr, pos *nir.Pos, typ int) nir.Stmt {
	return l.emitLet(sc, name, value, pos, "", nir.DebugLocalTemporary, typ, false)
}

func (l *lowerer) addTempBinding(sc *scope, name string, typ int, pos *nir.Pos) {
	l.addDebugLocal(sc, name, "", nir.DebugLocalTemporary, typ, pos, false)
}

func (l *lowerer) function(decl *frontend.Declaration) (*nir.Function, error) {
	l.loops = nil
	l.currentModule = decl.Module
	if snapshot := l.snapshots[decl.Module]; snapshot != nil {
		l.snapshot = snapshot
	}
	if decl.Result == nil {
		return nil, l.fail(decl.Start, "lower.bad-type", "函数 %q 缺少返回类型摘要", decl.Name)
	}
	if !isValueKind(decl.Result.Kind) && decl.Result.Kind != frontend.KindVoid {
		return nil, l.fail(decl.Start, "lower.bad-type", "函数 %q 的返回类型是 %s，本阶段只支持 number、boolean、string 与 void", decl.Name, decl.Result.Kind)
	}
	fn := &nir.Function{
		Name:   decl.Name,
		Result: nirType(decl.Result.Kind),
		Pos:    l.pos(decl.Start),
	}
	l.beginDebugFunction(decl.Name)
	sc := l.rootScope(decl.Start, decl.Length)
	sc.params = make(map[int]string, len(decl.Parameters))
	for _, id := range decl.Parameters {
		param, ok := l.facts.Declaration(id)
		if !ok || param.Kind != frontend.DeclParameter {
			return nil, l.fail(decl.Start, "lower.unknown-param", "函数 %q 的参数 ID %d 不存在", decl.Name, id)
		}
		if param.Type == nil || !isValueKind(param.Type.Kind) {
			return nil, l.fail(param.Start, "lower.bad-type", "参数 %q 不是 number、boolean 或 string，本阶段不支持", param.Name)
		}
		fn.Params = append(fn.Params, nir.Param{Name: param.Name, Type: nirType(param.Type.Kind), Pos: l.pos(param.Start)})
		sc.params[id] = param.Name
		l.addDebugLocal(sc, param.Name, param.Name, nir.DebugLocalParameter, nirType(param.Type.Kind), l.pos(param.Start), false)
	}
	if decl.Body == nil {
		return nil, l.fail(decl.Start, "lower.missing-body", "函数 %q 没有函数体", decl.Name)
	}
	l.result = fn.Result
	defer func() { l.result = 0 }()
	body, err := l.block(decl.Body, sc, fn.Result)
	if err != nil {
		return nil, err
	}
	fn.Body = body
	l.finishDebugFunction()
	return fn, nil
}

// entry 把模块体降低成入口函数：顶层 const 按源码顺序求值并绑定成局部值。
func (l *lowerer) entry() (*nir.Function, error) {
	return l.entryWith(EntryName, l.facts.Module, l.facts.ModulePath)
}

func (l *lowerer) entryWith(name string, body *frontend.Node, module string) (*nir.Function, error) {
	fn := &nir.Function{Name: name, Result: nir.TypeVoid}
	l.beginDebugFunction(name)
	start, length := 0, 0
	if body != nil {
		start, length = body.Start, body.Length
	}
	sc := l.rootScope(start, length)
	sc.params = map[int]string{}
	l.inInit = true
	defer func() { l.inInit = false }()
	stmts, err := l.initBody(body, module, sc, "")
	if err != nil {
		return nil, err
	}
	fn.Body = append(stmts, nir.Stmt{Op: nir.OpReturn})
	l.finishDebugFunction()
	return fn, nil
}

// initBody 把一个模块体降低成初始化语句，写进共享作用域 sc。模块级 const
// 求值一次并绑定成局部值，声明 ID 是它的身份；prefix 非空时局部名加上模块
// 前缀，让不同模块的同名常量在共享作用域里不冲突。跨模块读取按同一个 ID
// 命中这个局部值，因此不会重新执行初始化表达式。
func (l *lowerer) initBody(body *frontend.Node, module string, sc *scope, prefix string) ([]nir.Stmt, error) {
	if snapshot := l.snapshots[module]; snapshot != nil {
		l.snapshot = snapshot
	}
	l.currentModule = module
	if body == nil {
		return nil, nil
	}
	var out []nir.Stmt
	for i := range body.Children {
		stmt := &body.Children[i]
		switch stmt.Kind {
		case "TypeAliasDeclaration", "InterfaceDeclaration":
			// 类型声明不产生运行时值；能力检查已经核对过类别。
			continue
		case "ImportDeclaration":
			// 导入绑定由模块符号表核对；初始化体本身不产生运行时语句。
			continue
		case "Const":
			decl, ok := l.facts.Declaration(stmt.Decl)
			if !ok || decl.Kind != frontend.DeclConst {
				return nil, l.fail(stmt.Start, "lower.unknown-const", "模块语句引用的常量 ID %d 不存在", stmt.Decl)
			}
			if _, exists := sc.locals[decl.ID]; exists {
				return nil, l.fail(stmt.Start, "lower.duplicate-const", "常量 %q 重复定义", decl.Name)
			}
			if decl.Body == nil {
				return nil, l.fail(stmt.Start, "lower.missing-value", "常量 %q 没有初始值", decl.Name)
			}
			if decl.Type == nil {
				return nil, l.fail(stmt.Start, "lower.bad-type", "常量 %q 缺少类型摘要", decl.Name)
			}
			if !isValueKind(decl.Type.Kind) {
				return nil, l.fail(stmt.Start, "lower.bad-type", "常量 %q 是 %s，本阶段只支持 number、boolean 与 string", decl.Name, decl.Type.Kind)
			}
			if err := l.typedOperand(decl.Body, decl.Type.Kind, "常量 "+decl.Name+" "); err != nil {
				return nil, err
			}
			value, pre, err := l.expr(decl.Body, sc, 1)
			if err != nil {
				return nil, err
			}
			name := prefix + decl.Name
			out = append(out, pre...)
			out = append(out, l.emitLet(sc, name, value, l.pos(stmt.Start), decl.Name, nir.DebugLocalModuleConst, nirType(decl.Type.Kind), false))
			sc.locals[decl.ID] = name
		case "ExpressionStatement":
			s, err := l.statement(stmt, sc)
			if err != nil {
				return nil, err
			}
			out = append(out, s...)
		default:
			return nil, l.fail(stmt.Start, "lower.unsupported-statement", "模块级语句 %s 不在本阶段子集内", stmt.Kind)
		}
	}
	return out, nil
}

func (l *lowerer) block(block *frontend.Node, sc *scope, result int) ([]nir.Stmt, error) {
	body, err := l.blockStmts(block, sc)
	if err != nil {
		return nil, err
	}
	if result == nir.TypeVoid {
		// void 函数正常结束等价于一条空 return；如果分支已经覆盖所有路径，
		// 不再补一条不可达的 return。
		if !nir.BlockReturns(body) {
			body = append(body, nir.Stmt{Op: nir.OpReturn})
		}
		return body, nil
	}
	if !nir.BlockReturns(body) {
		pos := nir.FirstMissingReturn(body)
		return nil, l.failNir(pos, block.Start, "lower.missing-return", "返回 %s 的函数有路径没有 return", nirTypeLabel(result))
	}
	return body, nil
}

// blockStmts 降低一个语句块，不负责补 return。if 的分支用它：分支里是否
// 返回由源码决定，补齐函数返回值是函数体自己的事。
func (l *lowerer) blockStmts(block *frontend.Node, sc *scope) ([]nir.Stmt, error) {
	if block.Kind != "Block" {
		return nil, l.fail(block.Start, "lower.bad-body", "语句块是 %s，应为 Block", block.Kind)
	}
	var body []nir.Stmt
	for i := range block.Children {
		stmt, err := l.statement(&block.Children[i], sc)
		if err != nil {
			return nil, err
		}
		body = append(body, stmt...)
	}
	return body, nil
}

func (l *lowerer) statement(stmt *frontend.Node, sc *scope) ([]nir.Stmt, error) {
	switch stmt.Kind {
	case "ReturnStatement":
		if len(stmt.Children) == 0 {
			if l.result != nir.TypeVoid {
				return nil, l.fail(stmt.Start, "lower.missing-value", "返回 %s 的函数里 return 必须带值", nirTypeLabel(l.result))
			}
			return []nir.Stmt{{Op: nir.OpReturn, Pos: l.pos(stmt.Start)}}, nil
		}
		if len(stmt.Children) > 1 {
			return nil, l.fail(stmt.Start, "lower.bad-arity", "return 有 %d 个值", len(stmt.Children))
		}
		if l.result == nir.TypeVoid {
			return nil, l.fail(stmt.Start, "lower.void-value", "void 函数的 return 不能带返回值")
		}
		if err := l.typedOperand(&stmt.Children[0], kindOf(l.result), "返回值"); err != nil {
			return nil, err
		}
		value, pre, err := l.expr(&stmt.Children[0], sc, 1)
		if err != nil {
			return nil, err
		}
		return append(pre, nir.Stmt{Op: nir.OpReturn, Value: value, Pos: l.pos(stmt.Start)}), nil
	case "ExpressionStatement":
		if len(stmt.Children) != 1 {
			return nil, l.fail(stmt.Start, "lower.bad-arity", "表达式语句有 %d 个子节点", len(stmt.Children))
		}
		inner := &stmt.Children[0]
		if inner.Kind == "BinaryExpression" && inner.Op == "=" {
			return l.assignment(inner, sc)
		}
		arg, ok, err := l.consoleLog(inner)
		if err != nil {
			return nil, err
		}
		if ok {
			if arg.Type == nil || !isValueKind(arg.Type.Kind) {
				return nil, l.fail(arg.Start, "lower.bad-type", "console.log 的参数不是 number、boolean 或 string")
			}
			value, pre, err := l.expr(arg, sc, 1)
			if err != nil {
				return nil, err
			}
			return append(pre, nir.Stmt{Op: nir.OpPrint, Value: value, Pos: l.pos(stmt.Start)}), nil
		}
		// 其余表达式语句求值后丢弃结果：`f();` 的副作用要保留，`answer;`
		// 不打印任何东西。两条要求都落在 discard 上。
		value, pre, err := l.expr(inner, sc, 1)
		if err != nil {
			return nil, err
		}
		return append(pre, nir.Stmt{Op: nir.OpDiscard, Value: value, Pos: l.pos(stmt.Start)}), nil
	case "IfStatement":
		return l.ifStatement(stmt, sc)
	case "WhileStatement":
		return l.whileStatement(stmt, sc)
	case "BreakStatement", "ContinueStatement":
		return l.jumpStatement(stmt, sc)
	case "VariableStatement":
		return l.localDeclaration(stmt, sc)
	case "Block":
		return nil, l.fail(stmt.Start, "lower.unsupported-statement", "独立语句块不在本阶段子集内")
	default:
		return nil, l.fail(stmt.Start, "lower.unsupported-statement", "语句 %s 不在本阶段子集内", stmt.Kind)
	}
}

// ifStatement 降低 if / else / else if。条件在调用方的作用域里求值一次；
// 两条分支各自开一个子作用域，分支里的 let 不会泄漏到外面。
func (l *lowerer) ifStatement(stmt *frontend.Node, sc *scope) ([]nir.Stmt, error) {
	if len(stmt.Children) < 2 || len(stmt.Children) > 3 {
		return nil, l.fail(stmt.Start, "lower.bad-arity", "if 有 %d 个子节点", len(stmt.Children))
	}
	condNode := &stmt.Children[0]
	if err := l.typedOperand(condNode, frontend.KindBoolean, "if 的条件"); err != nil {
		return nil, err
	}
	cond, pre, err := l.expr(condNode, sc, 1)
	if err != nil {
		return nil, err
	}
	then, err := l.branchStmts(&stmt.Children[1], l.childScope(sc, nir.DebugScopeBranch, &stmt.Children[1]))
	if err != nil {
		return nil, err
	}
	var els []nir.Stmt
	if len(stmt.Children) == 3 {
		els, err = l.branchStmts(&stmt.Children[2], l.childScope(sc, nir.DebugScopeBranch, &stmt.Children[2]))
		if err != nil {
			return nil, err
		}
	}
	return append(pre, nir.Stmt{Op: nir.OpIf, Cond: cond, Then: then, Else: els, Pos: l.pos(stmt.Start)}), nil
}

// branchStmts 降低一条分支。带花括号的分支是 Block；单条语句（包括 else if）
// 也合法，包成只有一个元素的语句列表。
func (l *lowerer) branchStmts(node *frontend.Node, sc *scope) ([]nir.Stmt, error) {
	if node.Kind == "Block" {
		return l.blockStmts(node, sc)
	}
	return l.statement(node, sc)
}

// whileStatement 降低 `while (cond) { body }`。条件的准备语句放进 CondPre，
// 验证器和 bridge 会在每一轮重新执行它们；循环体单独开一个子作用域。
func (l *lowerer) whileStatement(stmt *frontend.Node, sc *scope) ([]nir.Stmt, error) {
	if len(stmt.Children) != 2 {
		return nil, l.fail(stmt.Start, "lower.bad-arity", "while 有 %d 个子节点", len(stmt.Children))
	}
	condNode, bodyNode := &stmt.Children[0], &stmt.Children[1]
	if err := l.typedOperand(condNode, frontend.KindBoolean, "while 的条件"); err != nil {
		return nil, err
	}
	condScope := l.childScope(sc, nir.DebugScopePrelude, condNode)
	cond, condPre, err := l.expr(condNode, condScope, 1)
	if err != nil {
		return nil, err
	}
	id := l.nextLoop + 1
	l.nextLoop = id
	l.loops = append(l.loops, id)
	body, err := l.branchStmts(bodyNode, l.childScope(sc, nir.DebugScopeLoop, bodyNode))
	l.loops = l.loops[:len(l.loops)-1]
	if err != nil {
		return nil, err
	}
	return []nir.Stmt{{
		Op:      nir.OpWhile,
		CondPre: condPre,
		Cond:    cond,
		Body:    body,
		ID:      id,
		Pos:     l.pos(stmt.Start),
	}}, nil
}

// jumpStatement 降低无标签 break / continue。目标永远是当前最内层循环；
// 带标签的写法直接拒绝，循环外的跳转也拒绝。
func (l *lowerer) jumpStatement(stmt *frontend.Node, sc *scope) ([]nir.Stmt, error) {
	if len(stmt.Children) > 0 {
		return nil, l.fail(stmt.Start, "lower.labeled-jump", "带标签的 %s 不在本阶段子集内", stmt.Kind)
	}
	if len(l.loops) == 0 {
		return nil, l.fail(stmt.Start, "lower.loop-target", "%s 不在循环里", stmt.Kind)
	}
	op := nir.OpBreak
	if stmt.Kind == "ContinueStatement" {
		op = nir.OpContinue
	}
	return []nir.Stmt{{
		Op:     op,
		Target: l.loops[len(l.loops)-1],
		Pos:    l.pos(stmt.Start),
	}}, nil
}

// assignment 降低 `x = value;`。目标必须是当前作用域里的可变 let；右值先
// 求值，写入放在右值的准备语句之后，因此 `x = x + 1` 读到的是写入前的 x。
func (l *lowerer) assignment(n *frontend.Node, sc *scope) ([]nir.Stmt, error) {
	if len(n.Children) != 2 {
		return nil, l.fail(n.Start, "lower.bad-arity", "赋值有 %d 个子节点", len(n.Children))
	}
	target, rhs := &n.Children[0], &n.Children[1]
	if target.Kind != "Identifier" {
		return nil, l.fail(target.Start, "lower.assign-target", "赋值目标必须是直接名字")
	}
	decl, ok := l.facts.Declaration(target.Target)
	if !ok || target.Target == 0 {
		return nil, l.fail(target.Start, "lower.unknown-name", "赋值目标 %q 不是本文件里的声明", target.Name)
	}
	if decl.Kind != frontend.DeclLet {
		return nil, l.fail(target.Start, "lower.assign-immutable", "赋值目标 %q 不是可变的 let", target.Name)
	}
	if decl.Type == nil || !isValueKind(decl.Type.Kind) {
		return nil, l.fail(decl.Start, "lower.bad-type", "赋值目标 %q 的类型不是 number、boolean 或 string", decl.Name)
	}
	name, ok := sc.lookup(decl.ID)
	if !ok {
		return nil, l.fail(target.Start, "lower.unknown-name", "赋值目标 %q 不在当前作用域", target.Name)
	}
	if !sc.isMutable(decl.ID) {
		return nil, l.fail(target.Start, "lower.assign-immutable", "赋值目标 %q 不是可变的 let", target.Name)
	}
	if err := l.typedOperand(rhs, decl.Type.Kind, "赋值右侧"); err != nil {
		return nil, err
	}
	value, pre, err := l.expr(rhs, sc, 1)
	if err != nil {
		return nil, err
	}
	return append(pre, nir.Stmt{Op: nir.OpSet, Name: name, Value: value, Pos: l.pos(n.Start)}), nil
}

// localDeclaration 降低分支里的局部 const。骨架是
// VariableStatement → VariableDeclarationList → VariableDeclaration，声明本身
// 只带一个初始化表达式。
func (l *lowerer) localDeclaration(stmt *frontend.Node, sc *scope) ([]nir.Stmt, error) {
	list := variableList(stmt)
	if list == nil {
		return nil, l.fail(stmt.Start, "lower.bad-declaration", "局部变量语句缺少声明列表")
	}
	if len(list.Children) != 1 || list.Children[0].Kind != "VariableDeclaration" {
		return nil, l.fail(stmt.Start, "lower.bad-declaration", "本阶段只支持一次声明一个局部值")
	}
	declNode := &list.Children[0]
	if declNode.Decl == 0 {
		return nil, l.fail(declNode.Start, "lower.bad-declaration", "局部变量缺少声明身份；只支持 const")
	}
	decl, ok := l.facts.Declaration(declNode.Decl)
	if !ok || (decl.Kind != frontend.DeclLocal && decl.Kind != frontend.DeclLet) {
		return nil, l.fail(declNode.Start, "lower.bad-declaration", "局部变量的声明身份 %d 不对", declNode.Decl)
	}
	if decl.Type == nil || !isValueKind(decl.Type.Kind) {
		return nil, l.fail(decl.Start, "lower.bad-type", "局部值 %q 不是 number、boolean 或 string", decl.Name)
	}
	if len(declNode.Children) != 1 {
		return nil, l.fail(declNode.Start, "lower.missing-value", "局部值 %q 缺少初始值", decl.Name)
	}
	init := &declNode.Children[0]
	if err := l.typedOperand(init, decl.Type.Kind, "局部值 "+decl.Name); err != nil {
		return nil, err
	}
	value, pre, err := l.expr(init, sc, 1)
	if err != nil {
		return nil, err
	}
	if !sc.define(decl.ID, decl.Name, decl.Mutable) {
		return nil, l.fail(decl.Start, "lower.duplicate-name", "局部值 %q 在同一个块里重复定义", decl.Name)
	}
	return append(pre, l.emitLet(sc, decl.Name, value, l.pos(stmt.Start), decl.Name, nir.DebugLocalLocal, nirType(decl.Type.Kind), decl.Mutable)), nil
}

func variableList(stmt *frontend.Node) *frontend.Node {
	if stmt.Kind != "VariableStatement" {
		return nil
	}
	for i := range stmt.Children {
		if stmt.Children[i].Kind == "VariableDeclarationList" {
			return &stmt.Children[i]
		}
	}
	return nil
}

// consoleLog 识别 console.log(x) 并返回实参。ok=false 表示这不是一次受限输出。
func (l *lowerer) consoleLog(call *frontend.Node) (*frontend.Node, bool, error) {
	if call.Kind != "CallExpression" || len(call.Children) != 2 {
		return nil, false, nil
	}
	callee := &call.Children[0]
	if callee.Kind != "PropertyAccessExpression" || callee.Name != "log" || len(callee.Children) != 1 {
		return nil, false, nil
	}
	base := &callee.Children[0]
	if base.Kind != "Identifier" || base.Name != "console" || !l.builtinConsole(base) {
		return nil, false, nil
	}
	return &call.Children[1], true, nil
}

// builtinConsole 与能力检查同一判据：本地声明让 Target 非 0；内置 console 的
// 名称引用解析到声明库（bundled lib），导入绑定解析到项目模块或没有目标。
// 没有名称引用时按内置处理，兜底手写事实；真实事实总会记录引用。
func (l *lowerer) builtinConsole(base *frontend.Node) bool {
	if base.Target != 0 {
		return false
	}
	for _, ref := range l.facts.References {
		if ref.Start == base.Start && ref.Name == base.Name {
			return strings.HasPrefix(ref.TargetModule, "bundled:")
		}
	}
	return true
}

func (l *lowerer) expr(n *frontend.Node, sc *scope, depth int) (*nir.Expr, []nir.Stmt, error) {
	if n == nil {
		return nil, nil, l.fail(0, "lower.missing-expr", "表达式为空")
	}
	if depth > maxDepth {
		return nil, nil, l.fail(n.Start, "lower.too-deep", "表达式嵌套超过 %d 层", maxDepth)
	}
	switch n.Kind {
	case "Identifier":
		return l.identifier(n, sc, depth)
	case "NumericLiteral":
		if n.Bits == "" {
			return nil, nil, l.fail(n.Start, "lower.bad-literal", "数字字面量缺少位模式")
		}
		return &nir.Expr{Op: nir.OpNumber, Bits: n.Bits, Pos: l.pos(n.Start)}, nil, nil
	case "StringLiteral", "NoSubstitutionTemplateLiteral":
		if n.Type == nil || n.Type.Kind != frontend.KindString {
			return nil, nil, l.fail(n.Start, "lower.bad-type", "字符串字面量缺少 string 类型摘要")
		}
		literal := nir.String(n.Name)
		literal.Pos = l.pos(n.Start)
		return &literal, nil, nil
	case "TrueKeyword", "FalseKeyword":
		literal := nir.Bool(n.Kind == "TrueKeyword")
		literal.Pos = l.pos(n.Start)
		return &literal, nil, nil
	case "BinaryExpression":
		if n.Op == "=" {
			return nil, nil, l.fail(n.Start, "lower.assignment-value", "赋值只能作为独立语句，不能出现在表达式里")
		}
		if n.Op == "&&" || n.Op == "||" {
			return l.shortCircuit(n, sc, depth)
		}
		if isCompare(n.Op) {
			return l.compare(n, sc, depth)
		}
		if !isArithmetic(n.Op) {
			return nil, nil, l.fail(n.Start, "lower.bad-operator", "运算符 %q 不在 + - * / 与比较之内", n.Op)
		}
		if len(n.Children) != 2 {
			return nil, nil, l.fail(n.Start, "lower.bad-arity", "二元运算有 %d 个操作数", len(n.Children))
		}
		if n.Op == "+" && (isStringKind(&n.Children[0]) || isStringKind(&n.Children[1])) {
			// string + string 是拼接；混合类型属于能力限制，能力检查会先拒绝，
			// 这里再兜一层，避免生成既不像算术也不像拼接的节点。
			if !isStringKind(&n.Children[0]) || !isStringKind(&n.Children[1]) {
				return nil, nil, l.fail(n.Start, "lower.bad-type", "字符串拼接要求两侧都是 string")
			}
			left, right, pre, err := l.pairOperands(&n.Children[0], &n.Children[1], sc, depth)
			if err != nil {
				return nil, nil, err
			}
			return &nir.Expr{Op: nir.OpConcat, Left: left, Right: right, Pos: l.pos(n.Start)}, pre, nil
		}
		for i := range n.Children {
			if err := l.typedOperand(&n.Children[i], frontend.KindNumber, "运算符 "+n.Op+" 的操作数"); err != nil {
				return nil, nil, err
			}
		}
		left, right, pre, err := l.pairOperands(&n.Children[0], &n.Children[1], sc, depth)
		if err != nil {
			return nil, nil, err
		}
		return &nir.Expr{Op: nir.OpBinary, Operator: n.Op, Left: left, Right: right, Pos: l.pos(n.Start)}, pre, nil
	case "PrefixUnaryExpression":
		return l.unary(n, sc, depth)
	case "ConditionalExpression":
		return l.conditional(n, sc, depth)
	case "CallExpression":
		return l.call(n, sc, depth)
	case "PropertyAccessExpression":
		return l.stringLength(n, sc, depth)
	default:
		return nil, nil, l.fail(n.Start, "lower.unsupported-expr", "表达式 %s 不在本阶段子集内", n.Kind)
	}
}

func (l *lowerer) identifier(n *frontend.Node, sc *scope, depth int) (*nir.Expr, []nir.Stmt, error) {
	decl, ok := l.facts.Declaration(n.Target)
	if !ok || n.Target == 0 {
		return nil, nil, l.fail(n.Start, "lower.unknown-name", "名称 %q 不是本文件里的声明", n.Name)
	}
	switch decl.Kind {
	case frontend.DeclParameter:
		name, ok := sc.lookupParam(decl.ID)
		if !ok {
			return nil, nil, l.fail(n.Start, "lower.unknown-name", "参数 %q 不属于当前函数", n.Name)
		}
		return &nir.Expr{Op: nir.OpParam, Name: name, Pos: l.pos(n.Start)}, nil, nil
	case frontend.DeclConst:
		if name, ok := sc.lookup(decl.ID); ok {
			return &nir.Expr{Op: nir.OpLocal, Name: name, Pos: l.pos(n.Start)}, nil, nil
		}
		if l.inInit {
			// 初始化作用域共享所有模块的常量存储，跨模块读取按声明 ID 命中
			// 依赖模块已经算好的局部值。读不到说明依赖顺序或声明身份出了
			// 问题；不能重新展开初始化表达式，那会重复执行其中的副作用。
			return nil, nil, l.fail(n.Start, "lower.unknown-name", "模块常量 %q 在初始化作用域里不可见", n.Name)
		}
		return nil, nil, l.fail(n.Start, "lower.module-const-in-function", "模块常量 %q 在函数内不可见", n.Name)
	case frontend.DeclLocal, frontend.DeclLet:
		name, ok := sc.lookup(decl.ID)
		if !ok {
			return nil, nil, l.fail(n.Start, "lower.unknown-name", "局部值 %q 不在当前作用域内", n.Name)
		}
		return &nir.Expr{Op: nir.OpLocal, Name: name, Pos: l.pos(n.Start)}, nil, nil
	default:
		return nil, nil, l.fail(n.Start, "lower.function-value", "函数 %q 作为值使用", n.Name)
	}
}

// stringLength 降低 `s.length`。它只接受 string 上的 length 属性；结果按
// UTF-16 code unit 计数，类型是 number。
func (l *lowerer) stringLength(n *frontend.Node, sc *scope, depth int) (*nir.Expr, []nir.Stmt, error) {
	if n.Name != "length" || len(n.Children) != 1 {
		return nil, nil, l.fail(n.Start, "lower.unsupported-expr", "只支持 string 上的 length 属性")
	}
	base := &n.Children[0]
	if !isStringKind(base) {
		return nil, nil, l.fail(n.Start, "lower.bad-type", "length 的属性基不是 string")
	}
	if n.Type == nil || n.Type.Kind != frontend.KindNumber {
		return nil, nil, l.fail(n.Start, "lower.bad-type", "length 的结果不是 number")
	}
	value, pre, err := l.expr(base, sc, depth+1)
	if err != nil {
		return nil, nil, err
	}
	return &nir.Expr{Op: nir.OpStrlen, Operand: value, Pos: l.pos(n.Start)}, pre, nil
}

// isStringKind 报告一个表达式节点的类型摘要是不是 string。缺摘要时不按
// string 处理，让 typedOperand 给出明确的坏类型诊断。
func isStringKind(n *frontend.Node) bool {
	return n != nil && n.Type != nil && n.Type.Kind == frontend.KindString
}

// compare 降低有序比较与严格相等。有序比较只接受 number；严格相等要求两侧
// 是同一个标量类型。操作数顺序原样保留，求值顺序由后端负责。
func (l *lowerer) compare(n *frontend.Node, sc *scope, depth int) (*nir.Expr, []nir.Stmt, error) {
	if len(n.Children) != 2 {
		return nil, nil, l.fail(n.Start, "lower.bad-arity", "比较运算有 %d 个操作数", len(n.Children))
	}
	leftNode, rightNode := &n.Children[0], &n.Children[1]
	if nir.IsOrderingOperator(n.Op) {
		if err := l.typedOperand(leftNode, frontend.KindNumber, "比较的左操作数"); err != nil {
			return nil, nil, err
		}
		if err := l.typedOperand(rightNode, frontend.KindNumber, "比较的右操作数"); err != nil {
			return nil, nil, err
		}
	} else {
		if leftNode.Type == nil || rightNode.Type == nil {
			return nil, nil, l.fail(n.Start, "lower.bad-type", "严格相等的操作数缺少类型摘要")
		}
		if leftNode.Type.Kind != rightNode.Type.Kind {
			return nil, nil, l.fail(n.Start, "lower.bad-type", "严格相等需要两侧同类型，实际为 %s 与 %s", leftNode.Type.Kind, rightNode.Type.Kind)
		}
		if !isValueKind(leftNode.Type.Kind) {
			return nil, nil, l.fail(n.Start, "lower.bad-type", "严格相等只支持 number、boolean 或 string，实际为 %s", leftNode.Type.Kind)
		}
	}
	left, right, pre, err := l.pairOperands(leftNode, rightNode, sc, depth)
	if err != nil {
		return nil, nil, err
	}
	return &nir.Expr{Op: nir.OpCompare, Operator: n.Op, Left: left, Right: right, Pos: l.pos(n.Start)}, pre, nil
}

// unary 处理一元运算。`!` 直接降成 not；一元正负号只接受数字字面量，并在
// 降低时折成位模式。
//
// 这样 -0 会被写成 0x8000000000000000，而不是「0 再取负」：后者依赖运行时的
// 取负语义，而常量折叠在编译期就可能把符号位抹掉。
func (l *lowerer) unary(n *frontend.Node, sc *scope, depth int) (*nir.Expr, []nir.Stmt, error) {
	if len(n.Children) != 1 {
		return nil, nil, l.fail(n.Start, "lower.bad-arity", "一元运算有 %d 个操作数", len(n.Children))
	}
	operand := &n.Children[0]
	if n.Op == "!" {
		if err := l.typedOperand(operand, frontend.KindBoolean, "! 的操作数"); err != nil {
			return nil, nil, err
		}
		value, pre, err := l.expr(operand, sc, depth+1)
		if err != nil {
			return nil, nil, err
		}
		return &nir.Expr{Op: nir.OpNot, Operand: value, Pos: l.pos(n.Start)}, pre, nil
	}
	if n.Op != "-" && n.Op != "+" {
		return nil, nil, l.fail(n.Start, "lower.bad-operator", "一元运算符 %q 不在本阶段子集内", n.Op)
	}
	if operand.Kind != "NumericLiteral" || operand.Bits == "" {
		return nil, nil, l.fail(n.Start, "lower.bad-unary", "一元 %q 只支持数字字面量", n.Op)
	}
	bits, err := parseBits(operand.Bits)
	if err != nil {
		return nil, nil, l.fail(n.Start, "lower.bad-literal", "位模式 %q 不合法", operand.Bits)
	}
	if n.Op == "-" {
		bits ^= 1 << 63
	}
	return &nir.Expr{Op: nir.OpNumber, Bits: formatBits(bits), Pos: l.pos(n.Start)}, nil, nil
}

func (l *lowerer) call(n *frontend.Node, sc *scope, depth int) (*nir.Expr, []nir.Stmt, error) {
	if len(n.Children) == 0 {
		return nil, nil, l.fail(n.Start, "lower.bad-arity", "调用没有实参列表")
	}
	callee := &n.Children[0]
	if callee.Kind != "Identifier" {
		return nil, nil, l.fail(n.Start, "lower.unknown-callee", "调用目标不是直接函数名")
	}
	decl, ok := l.facts.Declaration(callee.Target)
	if !ok || decl.Kind != frontend.DeclFunction {
		return nil, nil, l.fail(n.Start, "lower.unknown-callee", "调用目标 %q 不是本文件里的函数", callee.Name)
	}
	args := n.Children[1:]
	// 这里要求实参与形参一一对应：后端不会合成缺省值，少一个实参就凑不出
	// 目标函数的调用。可省略的参数在能力检查阶段已按 cap.unsupported-parameter
	// 拒绝，所以合法程序不会走到这里；事实验证用的 frontend.Arity 允许一个
	// 区间，是给「先判自洽、再判能力」那两步用的，不是降低器的契约。
	if len(args) != len(decl.Parameters) {
		return nil, nil, l.fail(n.Start, "lower.bad-arity", "调用 %q 给了 %d 个实参，声明有 %d 个参数", decl.Name, len(args), len(decl.Parameters))
	}
	exprs := make([]nir.Expr, len(args))
	var pre []nir.Stmt
	var pending []int
	for i := range args {
		param, ok := l.facts.Declaration(decl.Parameters[i])
		if !ok || param.Type == nil {
			return nil, nil, l.fail(n.Start, "lower.unknown-param", "函数 %q 的第 %d 个参数没有类型摘要", decl.Name, i+1)
		}
		if err := l.typedOperand(&args[i], param.Type.Kind, "调用的实参"); err != nil {
			return nil, nil, err
		}
		arg, argPre, err := l.expr(&args[i], sc, depth+1)
		if err != nil {
			return nil, nil, err
		}
		exprs[i] = *arg
		if len(argPre) == 0 {
			pending = append(pending, i)
			continue
		}
		// 这个实参带准备工作。先把它之前还没物化的实参按顺序算出来，
		// 再执行准备工作，最后物化它自己——否则后面的副作用会跑到前面。
		for _, j := range pending {
			name := l.tempName()
			value := exprs[j]
			pre = append(pre, l.emitTemp(sc, name, &value, l.pos(args[j].Start), nodeNirType(&args[j])))
			exprs[j] = nir.Expr{Op: nir.OpLocal, Name: name}
		}
		pending = nil
		pre = append(pre, argPre...)
		name := l.tempName()
		value := exprs[i]
		pre = append(pre, l.emitTemp(sc, name, &value, l.pos(args[i].Start), nodeNirType(&args[i])))
		exprs[i] = nir.Expr{Op: nir.OpLocal, Name: name}
	}
	return &nir.Expr{Op: nir.OpCall, Function: decl.Name, Args: exprs, Pos: l.pos(n.Start)}, pre, nil
}

// shortCircuit 降低 && 与 ||。左侧的准备工作在外层执行；右侧的准备工作与
// 右值一起放进 short 的 right 列表，只有真正需要求值右侧时才执行。结果绑定
// 到一个临时值，后续表达式用 local 读取它。
func (l *lowerer) shortCircuit(n *frontend.Node, sc *scope, depth int) (*nir.Expr, []nir.Stmt, error) {
	if len(n.Children) != 2 {
		return nil, nil, l.fail(n.Start, "lower.bad-arity", "短路运算有 %d 个操作数", len(n.Children))
	}
	leftNode, rightNode := &n.Children[0], &n.Children[1]
	if err := l.typedOperand(leftNode, frontend.KindBoolean, "运算符 "+n.Op+" 的左操作数"); err != nil {
		return nil, nil, err
	}
	if err := l.typedOperand(rightNode, frontend.KindBoolean, "运算符 "+n.Op+" 的右操作数"); err != nil {
		return nil, nil, err
	}
	left, leftPre, err := l.expr(leftNode, sc, depth+1)
	if err != nil {
		return nil, nil, err
	}
	rightScope := l.childScope(sc, nir.DebugScopePrelude, rightNode)
	right, rightPre, err := l.expr(rightNode, rightScope, depth+1)
	if err != nil {
		return nil, nil, err
	}
	name := l.tempName()
	l.addTempBinding(sc, name, nir.TypeBool, l.pos(n.Start))
	pre := append(leftPre, nir.Stmt{
		Op:       nir.OpShort,
		Name:     name,
		Operator: n.Op,
		Left:     left,
		Right:    rightPre,
		Value:    right,
		Pos:      l.pos(n.Start),
	})
	return &nir.Expr{Op: nir.OpLocal, Name: name, Pos: l.pos(n.Start)}, pre, nil
}

// reserveNames 登记这份事实里所有声明名。NIR 的局部值按名字寻址，参数、
// 局部值和模块常量都可能叫 _tmp_1；_tmp_ 不是语言保留字，编译器不能占用
// 用户的标识符空间。登记整个降低单元的名字（而不是当前作用域）是安全的：
// 多绕开几个名字只改变临时名序号，不会改变任何用户代码的含义。
func (l *lowerer) reserveNames(facts *frontend.Facts) {
	l.reserved = make(map[string]bool, len(facts.Declarations))
	for _, decl := range facts.Declarations {
		l.reserved[decl.Name] = true
	}
}

// tempName 分配一个不与用户名字冲突的临时名。序号在整个降低单元里单调
// 递增，所以临时值之间也不会重名。
func (l *lowerer) tempName() string {
	for {
		l.temp++
		name := fmt.Sprintf("_tmp_%d", l.temp)
		if !l.reserved[name] {
			return name
		}
	}
}

// pairOperands 降低二元运算的两个操作数。只要右侧带准备语句，左侧最终值
// 就必须在左侧准备之后、右侧准备之前物化：左侧若还是一次没求值的调用，
// 它的副作用会被推迟到整个二元表达式求值时，跑到右侧准备的后面。左侧自己
// 带准备语句时同样要物化——准备语句只算出了中间量，最终调用仍未执行。
func (l *lowerer) pairOperands(leftNode, rightNode *frontend.Node, sc *scope, depth int) (*nir.Expr, *nir.Expr, []nir.Stmt, error) {
	left, leftPre, err := l.expr(leftNode, sc, depth+1)
	if err != nil {
		return nil, nil, nil, err
	}
	right, rightPre, err := l.expr(rightNode, sc, depth+1)
	if err != nil {
		return nil, nil, nil, err
	}
	pre := leftPre
	if len(rightPre) > 0 {
		name := l.tempName()
		value := *left
		pre = append(pre, l.emitTemp(sc, name, &value, l.pos(leftNode.Start), nodeNirType(leftNode)))
		left = &nir.Expr{Op: nir.OpLocal, Name: name, Pos: l.pos(leftNode.Start)}
	}
	pre = append(pre, rightPre...)
	return left, right, pre, nil
}

// conditional 降低 cond ? then : else。条件在外层求值一次；两条分支各自带
// 准备语句，只有被选中的一条执行，结果绑定到临时值。
func (l *lowerer) conditional(n *frontend.Node, sc *scope, depth int) (*nir.Expr, []nir.Stmt, error) {
	if len(n.Children) != 3 {
		return nil, nil, l.fail(n.Start, "lower.bad-arity", "三元式有 %d 个子节点", len(n.Children))
	}
	condNode, thenNode, elseNode := &n.Children[0], &n.Children[1], &n.Children[2]
	if err := l.typedOperand(condNode, frontend.KindBoolean, "三元式的条件"); err != nil {
		return nil, nil, err
	}
	if thenNode.Type == nil || elseNode.Type == nil {
		return nil, nil, l.fail(n.Start, "lower.bad-type", "三元式的分支缺少类型摘要")
	}
	if thenNode.Type.Kind != elseNode.Type.Kind {
		return nil, nil, l.fail(n.Start, "lower.bad-type", "三元式两条分支类型不同：%s 与 %s", thenNode.Type.Kind, elseNode.Type.Kind)
	}
	if !isValueKind(thenNode.Type.Kind) {
		return nil, nil, l.fail(n.Start, "lower.bad-type", "三元式的结果不能是 %s", thenNode.Type.Kind)
	}
	cond, condPre, err := l.expr(condNode, sc, depth+1)
	if err != nil {
		return nil, nil, err
	}
	thenScope := l.childScope(sc, nir.DebugScopePrelude, thenNode)
	elseScope := l.childScope(sc, nir.DebugScopePrelude, elseNode)
	then, thenPre, err := l.expr(thenNode, thenScope, depth+1)
	if err != nil {
		return nil, nil, err
	}
	els, elsePre, err := l.expr(elseNode, elseScope, depth+1)
	if err != nil {
		return nil, nil, err
	}
	name := l.tempName()
	resultType := nirType(thenNode.Type.Kind)
	l.addTempBinding(sc, name, resultType, l.pos(n.Start))
	pre := append(condPre, nir.Stmt{
		Op:        nir.OpSelect,
		Name:      name,
		Cond:      cond,
		Then:      thenPre,
		ThenValue: then,
		Else:      elsePre,
		ElseValue: els,
		Type:      resultType,
		Pos:       l.pos(n.Start),
	})
	return &nir.Expr{Op: nir.OpLocal, Name: name, Pos: l.pos(n.Start)}, pre, nil
}

// typedOperand 检查一个表达式的类型摘要是不是 want。降低不重新推导类型，
// 但也不能盲信：事实里的类型摘要与结构对不上时，生成的 NIR 会通不过验证，
// 到那时再报错已经离原因很远了。
func (l *lowerer) typedOperand(n *frontend.Node, want frontend.Kind, what string) error {
	if n == nil || n.Type == nil {
		return l.fail(nodeStart(n), "lower.bad-type", "%s 缺少类型摘要", what)
	}
	if n.Type.Kind != want {
		return l.fail(n.Start, "lower.bad-type", "%s是 %s，需要 %s", what, n.Type.Kind, want)
	}
	return nil
}

func nodeStart(n *frontend.Node) int {
	if n == nil {
		return 0
	}
	return n.Start
}

func (l *lowerer) pos(offset int) *nir.Pos {
	pos, err := l.snapshot.Position(offset)
	if err != nil {
		return nil
	}
	return &nir.Pos{File: l.snapshot.Path(), Line: pos.Line, Column: pos.Column}
}

func (l *lowerer) fail(offset int, code, format string, args ...any) error {
	d := diagnostic.New(diagnostic.Internal, code, format, args...)
	if l.snapshot != nil {
		if pos, err := l.snapshot.Position(offset); err == nil {
			d.At(l.snapshot.Path(), pos.Line, pos.Column)
		}
	}
	return d
}

// failNir 用 NIR 位置报错。返回路径的缺口来自 NIR 语句，位置已经由降低填好；
// 没有位置时退回源码偏移。
func (l *lowerer) failNir(pos *nir.Pos, offset int, code, format string, args ...any) error {
	if pos != nil {
		return diagnostic.New(diagnostic.Internal, code, format, args...).At(pos.File, pos.Line, pos.Column)
	}
	return l.fail(offset, code, format, args...)
}

func nirType(k frontend.Kind) int {
	switch k {
	case frontend.KindVoid:
		return nir.TypeVoid
	case frontend.KindBoolean:
		return nir.TypeBool
	case frontend.KindString:
		return nir.TypeString
	}
	return nir.TypeNumber
}

// nodeNirType 返回表达式节点在 NIR 里的类型 ID。调用方已经用 typedOperand
// 核对过类型，这里只是把类别翻译成 ID；缺类型时退回 number，避免调试段
// 因为一条不完整事实而崩溃。
func nodeNirType(n *frontend.Node) int {
	if n != nil && n.Type != nil {
		return nirType(n.Type.Kind)
	}
	return nir.TypeNumber
}

// kindOf 把 NIR 类型换回事实里的类型类别，用于核对返回值。
func kindOf(t int) frontend.Kind {
	switch t {
	case nir.TypeBool:
		return frontend.KindBoolean
	case nir.TypeString:
		return frontend.KindString
	}
	return frontend.KindNumber
}

// nirTypeLabel 把类型 ID 变成诊断里的短名字。降低阶段只会遇到内置类型。
func nirTypeLabel(t int) string {
	if name := nir.BuiltinTypeName(t); name != "" {
		return name
	}
	return "type"
}

// isValueKind 报告一个事实类型能否作为当前阶段的标量值：number、boolean
// 或 string。void 只用于函数结果。
func isValueKind(k frontend.Kind) bool {
	switch k {
	case frontend.KindNumber, frontend.KindBoolean, frontend.KindString:
		return true
	}
	return false
}

func isArithmetic(op string) bool {
	switch op {
	case "+", "-", "*", "/":
		return true
	}
	return false
}

func isCompare(op string) bool {
	return nir.IsCompareOperator(op)
}

func parseBits(s string) (uint64, error) {
	var bits uint64
	if _, err := fmt.Sscanf(s, "0x%016x", &bits); err != nil {
		return 0, err
	}
	return bits, nil
}

func formatBits(bits uint64) string {
	return fmt.Sprintf("0x%016x", bits)
}
