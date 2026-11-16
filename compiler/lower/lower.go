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
	"math"
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
		snapshot:         snapshot,
		facts:            facts,
		names:            make(map[int]string, len(facts.Declarations)),
		types:            newTypeTable(facts),
		debug:            &nir.DebugInfo{},
		genericInstances: make(map[string]*genericInstance),
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
		if len(facts.TypeParametersOf(decl)) > 0 {
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
	module.Functions = append(module.Functions, l.lifted...)
	module.Functions = append(module.Functions, *entry)
	if len(l.debug.Functions) > 0 {
		module.Debug = l.debug
	}
	module.Types = l.types.defs

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
		snapshots:        snapshots,
		facts:            facts,
		names:            make(map[int]string, len(facts.Declarations)),
		types:            newTypeTable(facts),
		debug:            &nir.DebugInfo{},
		genericInstances: make(map[string]*genericInstance),
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
		if len(facts.TypeParametersOf(decl)) > 0 {
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
	module.Functions = append(module.Functions, l.lifted...)
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
	module.Types = l.types.defs

	if err := nir.Verify(module); err != nil {
		return nil, diagnostic.New(diagnostic.Internal, "lower.invalid-output", "合并降低产出的 NIR 没有通过验证").WithDetail(err.Error())
	}
	return module, nil
}

type lowerer struct {
	snapshot         *source.Snapshot
	snapshots        map[string]*source.Snapshot
	facts            *frontend.Facts
	currentModule    string
	inInit           bool
	names            map[int]string  // 函数声明 ID → NIR 函数名
	types            *typeTable      // 事实类型 → NIR 类型 ID
	result           int             // 当前正在降低的函数的返回类型 ID
	temp             int             // 短路结果与实参物化的临时名序号
	reserved         map[string]bool // 用户声明占用的名字，临时名必须绕开
	loops            []loopFrame     // 当前函数内由内到外的循环
	nextLoop         int             // 下一个循环 id
	nextFor          int             // 下一个 for 循环变量的 NIR 名序号
	debug            *nir.DebugInfo  // 整个模块的调试段
	debugFn          *nir.DebugFunction
	nextScope        int                         // 当前函数内下一个作用域 id
	lifted           []nir.Function              // 由箭头函数提升出的合成函数
	nextArrow        int                         // 下一个箭头函数名序号
	arrowParams      []map[string]bool           // 正在降低的箭头函数的参数名栈
	genericInstances map[string]*genericInstance // 单态化实例缓存
}

// loopFrame 是当前函数里的一个循环。while 没有更新表达式；for 记录更新，
// 以便在 continue 跳转前补执行一次。
type loopFrame struct {
	id     int
	update *frontend.Node
}

// genericInstance 是一次泛型函数的单态化结果。函数体可能正在降低，缓存项
// 先登记名字，递归调用才能复用同一个实例。
type genericInstance struct {
	name string
	fn   *nir.Function
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
	resultID, err := l.types.id(decl.Result)
	if err != nil {
		return nil, l.fail(decl.Start, "lower.bad-type", "函数 %q 的返回类型：%v", decl.Name, err)
	}
	fn := &nir.Function{
		Name:   decl.Name,
		Result: resultID,
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
		paramID, err := l.types.id(param.Type)
		if err != nil {
			return nil, l.fail(param.Start, "lower.bad-type", "参数 %q：%v", param.Name, err)
		}
		if paramID == nir.TypeVoid {
			return nil, l.fail(param.Start, "lower.bad-type", "参数 %q 不能是 void", param.Name)
		}
		fn.Params = append(fn.Params, nir.Param{Name: param.Name, Type: paramID, Pos: l.pos(param.Start)})
		sc.params[id] = param.Name
		l.addDebugLocal(sc, param.Name, param.Name, nir.DebugLocalParameter, paramID, l.pos(param.Start), false)
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
			wantID, err := l.valueTypeID(decl.Type)
			if err != nil {
				return nil, l.fail(stmt.Start, "lower.bad-type", "常量 %q：%v", decl.Name, err)
			}
			value, pre, err := l.valueAs(decl.Body, wantID, "常量 "+decl.Name+" ", sc, 1)
			if err != nil {
				return nil, err
			}
			name := prefix + decl.Name
			out = append(out, pre...)
			out = append(out, l.emitLet(sc, name, value, l.pos(stmt.Start), decl.Name, nir.DebugLocalModuleConst, wantID, false))
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
		return nil, l.failNir(pos, block.Start, "lower.missing-return", "返回 %s 的函数有路径没有 return", l.typeLabel(result))
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
				return nil, l.fail(stmt.Start, "lower.missing-value", "返回 %s 的函数里 return 必须带值", l.typeLabel(l.result))
			}
			return []nir.Stmt{{Op: nir.OpReturn, Pos: l.pos(stmt.Start)}}, nil
		}
		if len(stmt.Children) > 1 {
			return nil, l.fail(stmt.Start, "lower.bad-arity", "return 有 %d 个值", len(stmt.Children))
		}
		if l.result == nir.TypeVoid {
			return nil, l.fail(stmt.Start, "lower.void-value", "void 函数的 return 不能带返回值")
		}
		value, pre, err := l.valueAs(&stmt.Children[0], l.result, "返回值", sc, 1)
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
	case "ForStatement":
		return l.forStatement(stmt, sc)
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
	l.loops = append(l.loops, loopFrame{id: id})
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

// forStatement 降低经典 for。初始化先执行一次，条件和准备语句进入 while 的
// CondPre，循环体末尾追加更新；continue 的位置由 jumpStatement 补更新。
func (l *lowerer) forStatement(stmt *frontend.Node, sc *scope) ([]nir.Stmt, error) {
	if len(stmt.Children) != 3 && len(stmt.Children) != 4 {
		return nil, l.fail(stmt.Start, "lower.bad-arity", "for 有 %d 个子节点；需要初始化和条件", len(stmt.Children))
	}
	initNode, condNode := &stmt.Children[0], &stmt.Children[1]
	bodyNode := &stmt.Children[len(stmt.Children)-1]
	var update *frontend.Node
	if len(stmt.Children) == 4 {
		update = &stmt.Children[2]
		if update.Kind != "BinaryExpression" || update.Op != "=" {
			return nil, l.fail(update.Start, "lower.bad-operator", "for 的更新表达式只支持简单赋值")
		}
	}
	if err := l.typedOperand(condNode, frontend.KindBoolean, "for 的条件"); err != nil {
		return nil, err
	}
	headerScope := l.childScope(sc, nir.DebugScopeBranch, stmt)
	init, err := l.forInit(initNode, headerScope)
	if err != nil {
		return nil, err
	}
	condScope := l.childScope(headerScope, nir.DebugScopePrelude, condNode)
	cond, condPre, err := l.expr(condNode, condScope, 1)
	if err != nil {
		return nil, err
	}
	id := l.nextLoop + 1
	l.nextLoop = id
	l.loops = append(l.loops, loopFrame{id: id, update: update})
	bodyScope := l.childScope(headerScope, nir.DebugScopeLoop, bodyNode)
	body, err := l.branchStmts(bodyNode, bodyScope)
	if err == nil && update != nil {
		var updateStmts []nir.Stmt
		updateStmts, err = l.forUpdate(update, bodyScope)
		body = append(body, updateStmts...)
	}
	l.loops = l.loops[:len(l.loops)-1]
	if err != nil {
		return nil, err
	}
	return append(init, nir.Stmt{
		Op:      nir.OpWhile,
		CondPre: condPre,
		Cond:    cond,
		Body:    body,
		ID:      id,
		Pos:     l.pos(stmt.Start),
	}), nil
}

// forInit 降低 for 的初始化声明。循环变量只在 for 头部和循环体内可见；NIR
// 没有语句级的块作用域包住初始化，所以使用带 $forN 后缀的唯一名字，循环
// 结束后同名局部值不会与它冲突。
func (l *lowerer) forInit(list *frontend.Node, sc *scope) ([]nir.Stmt, error) {
	if list.Kind != "VariableDeclarationList" || len(list.Children) != 1 {
		return nil, l.fail(list.Start, "lower.bad-declaration", "for 的初始化只支持一个 let 声明")
	}
	declNode := &list.Children[0]
	if declNode.Kind != "VariableDeclaration" || declNode.Decl == 0 {
		return nil, l.fail(declNode.Start, "lower.bad-declaration", "for 的初始化缺少声明身份")
	}
	decl, ok := l.facts.Declaration(declNode.Decl)
	if !ok || decl.Kind != frontend.DeclLet {
		return nil, l.fail(declNode.Start, "lower.bad-declaration", "for 的循环变量必须是 let")
	}
	wantID, err := l.valueTypeID(decl.Type)
	if err != nil {
		return nil, l.fail(decl.Start, "lower.bad-type", "for 的循环变量 %q：%v", decl.Name, err)
	}
	if len(declNode.Children) != 1 {
		return nil, l.fail(declNode.Start, "lower.missing-value", "for 的循环变量 %q 缺少初始值", decl.Name)
	}
	value, pre, err := l.valueAs(&declNode.Children[0], wantID, "for 的循环变量 "+decl.Name, sc, 1)
	if err != nil {
		return nil, err
	}
	l.nextFor++
	name := fmt.Sprintf("%s$for%d", decl.Name, l.nextFor)
	if !sc.define(decl.ID, name, true) {
		return nil, l.fail(decl.Start, "lower.duplicate-name", "for 的循环变量 %q 重复定义", decl.Name)
	}
	return append(pre, l.emitLet(sc, name, value, l.pos(declNode.Start), decl.Name, nir.DebugLocalLocal, wantID, true)), nil
}

// forUpdate 降低 for 的更新表达式。当前只接受简单赋值，与独立赋值语句共用
// 同一套目标、可变性和类型检查。
func (l *lowerer) forUpdate(n *frontend.Node, sc *scope) ([]nir.Stmt, error) {
	if n.Kind != "BinaryExpression" || n.Op != "=" {
		return nil, l.fail(n.Start, "lower.bad-operator", "for 的更新表达式只支持简单赋值")
	}
	return l.assignment(n, sc)
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
	frame := l.loops[len(l.loops)-1]
	var out []nir.Stmt
	if stmt.Kind == "ContinueStatement" {
		op = nir.OpContinue
		// JavaScript 的 continue 在跳到条件之前执行 for 的更新表达式。NIR 的
		// continue 只回到条件检查，所以更新必须在这里补一次；每次 continue
		// 独立降低，临时名不会和正常路径的更新冲突。
		if frame.update != nil {
			update, err := l.forUpdate(frame.update, sc)
			if err != nil {
				return nil, err
			}
			out = append(out, update...)
		}
	}
	return append(out, nir.Stmt{
		Op:     op,
		Target: frame.id,
		Pos:    l.pos(stmt.Start),
	}), nil
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
	wantID, err := l.valueTypeID(decl.Type)
	if err != nil {
		return nil, l.fail(decl.Start, "lower.bad-type", "赋值目标 %q：%v", decl.Name, err)
	}
	name, ok := sc.lookup(decl.ID)
	if !ok {
		return nil, l.fail(target.Start, "lower.unknown-name", "赋值目标 %q 不在当前作用域", target.Name)
	}
	if !sc.isMutable(decl.ID) {
		return nil, l.fail(target.Start, "lower.assign-immutable", "赋值目标 %q 不是可变的 let", target.Name)
	}
	value, pre, err := l.valueAs(rhs, wantID, "赋值右侧", sc, 1)
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
	wantID, err := l.valueTypeID(decl.Type)
	if err != nil {
		return nil, l.fail(decl.Start, "lower.bad-type", "局部值 %q：%v", decl.Name, err)
	}
	if len(declNode.Children) != 1 {
		return nil, l.fail(declNode.Start, "lower.missing-value", "局部值 %q 缺少初始值", decl.Name)
	}
	init := &declNode.Children[0]
	value, pre, err := l.valueAs(init, wantID, "局部值 "+decl.Name, sc, 1)
	if err != nil {
		return nil, err
	}
	if !sc.define(decl.ID, decl.Name, decl.Mutable) {
		return nil, l.fail(decl.Start, "lower.duplicate-name", "局部值 %q 在同一个块里重复定义", decl.Name)
	}
	return append(pre, l.emitLet(sc, decl.Name, value, l.pos(stmt.Start), decl.Name, nir.DebugLocalLocal, wantID, decl.Mutable)), nil
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
	case "NullKeyword":
		return &nir.Expr{Op: nir.OpNull, Pos: l.pos(n.Start)}, nil, nil
	case "UndefinedKeyword":
		return &nir.Expr{Op: nir.OpUndefined, Pos: l.pos(n.Start)}, nil, nil
	case "ArrowFunction":
		return l.liftArrow(n, depth)
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
		if n.Op == "??" {
			return l.nullish(n, sc, depth)
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
	case "ObjectLiteralExpression":
		return l.objectLiteral(n, sc, depth)
	case "ArrayLiteralExpression":
		return l.arrayLiteral(n, sc, depth)
	case "ElementAccessExpression":
		return l.indexAccess(n, sc, depth)
	case "PropertyAccessExpression":
		if n.Name == "length" && baseNode(n) != nil && baseNode(n).Type != nil {
			switch l.facts.ResolveType(baseNode(n).Type).Kind {
			case frontend.KindString:
				return l.stringLength(n, sc, depth)
			case frontend.KindArray:
				return l.arrayLength(n, sc, depth)
			}
		}
		return l.objectField(n, sc, depth)
	default:
		return nil, nil, l.fail(n.Start, "lower.unsupported-expr", "表达式 %s 不在本阶段子集内", n.Kind)
	}
}

func (l *lowerer) identifier(n *frontend.Node, sc *scope, depth int) (*nir.Expr, []nir.Stmt, error) {
	if n.Target == 0 {
		if len(l.arrowParams) > 0 && l.arrowParams[len(l.arrowParams)-1][n.Name] {
			return &nir.Expr{Op: nir.OpParam, Name: n.Name, Pos: l.pos(n.Start)}, nil, nil
		}
	}
	if n.Target == 0 && n.Type != nil {
		switch n.Type.Kind {
		case frontend.KindUndefined:
			if n.Name == "undefined" {
				return &nir.Expr{Op: nir.OpUndefined, Pos: l.pos(n.Start)}, nil, nil
			}
		case frontend.KindNull:
			if n.Name == "null" {
				return &nir.Expr{Op: nir.OpNull, Pos: l.pos(n.Start)}, nil, nil
			}
		}
	}
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
		return l.narrowedRead(n, decl, &nir.Expr{Op: nir.OpParam, Name: name, Pos: l.pos(n.Start)}, sc)
	case frontend.DeclConst:
		if name, ok := sc.lookup(decl.ID); ok {
			return l.narrowedRead(n, decl, &nir.Expr{Op: nir.OpLocal, Name: name, Pos: l.pos(n.Start)}, sc)
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
		return l.narrowedRead(n, decl, &nir.Expr{Op: nir.OpLocal, Name: name, Pos: l.pos(n.Start)}, sc)
	case frontend.DeclFunction:
		if len(l.facts.TypeParametersOf(decl)) > 0 {
			return nil, nil, l.fail(n.Start, "lower.function-value", "泛型函数 %q 必须先实例化后才能作为值", n.Name)
		}
		if n.Type == nil || n.Type.Kind != frontend.KindFunction {
			return nil, nil, l.fail(n.Start, "lower.function-value", "函数 %q 作为值使用时缺少函数类型摘要", n.Name)
		}
		id, err := l.types.id(n.Type)
		if err != nil {
			return nil, nil, l.fail(n.Start, "lower.bad-type", "函数 %q 作为值：%v", n.Name, err)
		}
		return &nir.Expr{Op: nir.OpFunc, Function: decl.Name, Type: id, Pos: l.pos(n.Start)}, nil, nil
	default:
		return nil, nil, l.fail(n.Start, "lower.function-value", "函数 %q 作为值使用", n.Name)
	}
}

// narrowedRead 在读取联合类型存储时按当前表达式类型拆箱。前端已经完成控制
// 流缩窄，`n.Type` 是缩窄后的类型；这里只把标签检查后的值取出来。
func (l *lowerer) narrowedRead(n *frontend.Node, decl *frontend.Declaration, value *nir.Expr, sc *scope) (*nir.Expr, []nir.Stmt, error) {
	if decl.Type == nil || n.Type == nil {
		return value, nil, nil
	}
	storage, err := l.types.id(decl.Type)
	if err != nil {
		return nil, nil, l.fail(n.Start, "lower.bad-type", "读取 %q：%v", n.Name, err)
	}
	got, err := l.types.id(n.Type)
	if err != nil {
		return nil, nil, l.fail(n.Start, "lower.bad-type", "读取 %q：%v", n.Name, err)
	}
	if storage == got || got == nir.TypeNull || got == nir.TypeUndefined {
		return value, nil, nil
	}
	if !l.types.isUnionMember(storage, got) {
		return value, nil, nil
	}
	name := l.tempName()
	l.addTempBinding(sc, name, got, l.pos(n.Start))
	return localExpr(name, l.pos(n.Start)), []nir.Stmt{{
		Op:    nir.OpUnwrap,
		Name:  name,
		Type:  got,
		Value: value,
		Pos:   l.pos(n.Start),
	}}, nil
}

// objectLiteral 降低静态形状对象字面量。字段按源码顺序求值并各自物化成
// 临时值，随后由 object 语句一次性构造对象；这样后续字段的准备语句不会
// 提前到前面字段之前。字段名和类型由 NIR 验证器按目标类型复核。
func (l *lowerer) objectLiteral(n *frontend.Node, sc *scope, depth int) (*nir.Expr, []nir.Stmt, error) {
	if n.Type == nil {
		return nil, nil, l.fail(n.Start, "lower.bad-type", "对象字面量缺少类型摘要")
	}
	id, err := l.types.id(n.Type)
	if err != nil {
		return nil, nil, l.fail(n.Start, "lower.bad-type", "对象字面量：%v", err)
	}
	def := l.types.objectDef(id)
	if def == nil {
		return nil, nil, l.fail(n.Start, "lower.bad-type", "对象字面量的类型不是 object")
	}
	if len(n.Children) != len(def.Properties) {
		return nil, nil, l.fail(n.Start, "lower.bad-type", "对象字面量有 %d 个字段，类型需要 %d 个", len(n.Children), len(def.Properties))
	}
	var pre []nir.Stmt
	fields := make([]nir.ObjectField, 0, len(n.Children))
	for i := range n.Children {
		child := &n.Children[i]
		if child.Kind != "PropertyAssignment" || child.Name == "" || len(child.Children) != 1 {
			return nil, nil, l.fail(child.Start, "lower.unsupported-object", "对象字段必须是 name: value 形式")
		}
		valueNode := &child.Children[0]
		value, valuePre, err := l.expr(valueNode, sc, depth+1)
		if err != nil {
			return nil, nil, err
		}
		pre = append(pre, valuePre...)
		name := l.tempName()
		pre = append(pre, l.emitTemp(sc, name, value, l.pos(valueNode.Start), l.nodeNirType(valueNode)))
		fields = append(fields, nir.ObjectField{Name: child.Name, Value: *localExpr(name, l.pos(valueNode.Start))})
	}
	name := l.tempName()
	l.addTempBinding(sc, name, id, l.pos(n.Start))
	pre = append(pre, nir.Stmt{Op: nir.OpObject, Name: name, Type: id, Fields: fields, Pos: l.pos(n.Start)})
	return localExpr(name, l.pos(n.Start)), pre, nil
}

// objectField 降低对象属性读取。字段必须存在于静态形状里，且当前切片只
// 支持必填属性；可选属性要等 undefined 表示。
func (l *lowerer) objectField(n *frontend.Node, sc *scope, depth int) (*nir.Expr, []nir.Stmt, error) {
	if n.Name == "" || len(n.Children) != 1 {
		return nil, nil, l.fail(n.Start, "lower.unsupported-expr", "属性访问缺少名字或对象")
	}
	base := &n.Children[0]
	if base.Type == nil {
		return nil, nil, l.fail(n.Start, "lower.bad-type", "属性访问的对象缺少类型摘要")
	}
	baseType := l.facts.ResolveType(base.Type)
	if baseType.Kind != frontend.KindObject {
		return nil, nil, l.fail(n.Start, "lower.bad-type", "属性访问只支持对象，实际为 %s", baseType.Kind)
	}
	baseID, err := l.types.id(baseType)
	if err != nil {
		return nil, nil, l.fail(n.Start, "lower.bad-type", "属性访问的对象：%v", err)
	}
	def := l.types.objectDef(baseID)
	if def == nil {
		return nil, nil, l.fail(n.Start, "lower.bad-type", "属性访问的对象没有静态布局")
	}
	var prop *nir.TypeProperty
	for i := range def.Properties {
		if def.Properties[i].Name == n.Name {
			prop = &def.Properties[i]
			break
		}
	}
	if prop == nil {
		return nil, nil, l.fail(n.Start, "lower.unknown-field", "类型 %s 没有属性 %q", l.typeLabel(baseID), n.Name)
	}
	if l.nodeNirType(n) != prop.Type {
		return nil, nil, l.fail(n.Start, "lower.bad-type", "属性 %q 的类型与静态布局不一致", n.Name)
	}
	value, pre, err := l.expr(base, sc, depth+1)
	if err != nil {
		return nil, nil, err
	}
	return &nir.Expr{Op: nir.OpField, Name: n.Name, Operand: value, Pos: l.pos(n.Start)}, pre, nil
}

// arrayLiteral 降低数组或元组字面量。每个元素先按源码顺序求值并物化成临时
// 值，再生成一条 array/tuple 语句；这样后面的元素不会因为准备语句提前而跑到
// 前面元素之前。
func (l *lowerer) arrayLiteral(n *frontend.Node, sc *scope, depth int) (*nir.Expr, []nir.Stmt, error) {
	if n.Type == nil {
		return nil, nil, l.fail(n.Start, "lower.bad-type", "数组字面量缺少类型摘要")
	}
	typ := l.facts.ResolveType(n.Type)
	id, err := l.types.id(typ)
	if err != nil {
		return nil, nil, l.fail(n.Start, "lower.bad-type", "数组字面量：%v", err)
	}
	var wants []int
	op := nir.OpArray
	switch typ.Kind {
	case frontend.KindArray:
		def := l.types.arrayDef(id)
		if def == nil {
			return nil, nil, l.fail(n.Start, "lower.bad-type", "数组字面量的类型不是 array")
		}
		for range n.Children {
			wants = append(wants, def.Element)
		}
	case frontend.KindTuple:
		def := l.types.tupleDef(id)
		if def == nil {
			return nil, nil, l.fail(n.Start, "lower.bad-type", "元组字面量的类型不是 tuple")
		}
		if len(n.Children) != len(def.Elements) {
			return nil, nil, l.fail(n.Start, "lower.bad-type", "元组字面量有 %d 个元素，类型需要 %d 个", len(n.Children), len(def.Elements))
		}
		wants = def.Elements
		op = nir.OpTuple
	default:
		return nil, nil, l.fail(n.Start, "lower.bad-type", "数组字面量的类型是 %s", typ.Kind)
	}
	var pre []nir.Stmt
	items := make([]nir.Expr, 0, len(n.Children))
	for i := range n.Children {
		child := &n.Children[i]
		if child.Kind == "SpreadElement" || child.Kind == "OmittedExpression" {
			return nil, nil, l.fail(child.Start, "lower.unsupported-array", "数组字面量只接受逐个列出的元素")
		}
		if err := l.typedValue(child, wants[i], "数组元素"); err != nil {
			return nil, nil, err
		}
		value, valuePre, err := l.expr(child, sc, depth+1)
		if err != nil {
			return nil, nil, err
		}
		pre = append(pre, valuePre...)
		name := l.tempName()
		pre = append(pre, l.emitTemp(sc, name, value, l.pos(child.Start), wants[i]))
		items = append(items, *localExpr(name, l.pos(child.Start)))
	}
	name := l.tempName()
	l.addTempBinding(sc, name, id, l.pos(n.Start))
	pre = append(pre, nir.Stmt{Op: op, Name: name, Type: id, Items: items, Pos: l.pos(n.Start)})
	return localExpr(name, l.pos(n.Start)), pre, nil
}

// arrayLength 降低数组的 length。结果按当前元素个数计数，类型是 number。
func (l *lowerer) arrayLength(n *frontend.Node, sc *scope, depth int) (*nir.Expr, []nir.Stmt, error) {
	base := baseNode(n)
	if base == nil || base.Type == nil {
		return nil, nil, l.fail(n.Start, "lower.bad-type", "数组 length 缺少对象")
	}
	baseType := l.facts.ResolveType(base.Type)
	if baseType.Kind != frontend.KindArray {
		return nil, nil, l.fail(n.Start, "lower.bad-type", "length 只支持 string 或数组，实际为 %s", baseType.Kind)
	}
	value, pre, err := l.expr(base, sc, depth+1)
	if err != nil {
		return nil, nil, err
	}
	return &nir.Expr{Op: nir.OpLen, Operand: value, Pos: l.pos(n.Start)}, pre, nil
}

// indexAccess 降低数组和元组的索引读取。数组索引先求值对象再求值下标，随后
// 由 index 语句做运行时边界检查；元组索引必须是编译期整数，越界在降低前拒绝。
func (l *lowerer) indexAccess(n *frontend.Node, sc *scope, depth int) (*nir.Expr, []nir.Stmt, error) {
	if len(n.Children) != 2 {
		return nil, nil, l.fail(n.Start, "lower.unsupported-array", "索引访问有 %d 个子节点", len(n.Children))
	}
	base, index := &n.Children[0], &n.Children[1]
	if base.Type == nil {
		return nil, nil, l.fail(n.Start, "lower.bad-type", "索引访问的对象缺少类型摘要")
	}
	baseType := l.facts.ResolveType(base.Type)
	baseID, err := l.types.id(baseType)
	if err != nil {
		return nil, nil, l.fail(n.Start, "lower.bad-type", "索引访问的对象：%v", err)
	}
	var resultID int
	var indexNode nir.Expr
	isArray := false
	switch baseType.Kind {
	case frontend.KindArray:
		def := l.types.arrayDef(baseID)
		if def == nil {
			return nil, nil, l.fail(n.Start, "lower.bad-type", "索引访问的对象不是数组")
		}
		if err := l.typedValue(index, nir.TypeNumber, "数组索引"); err != nil {
			return nil, nil, err
		}
		if err := l.typedValue(n, def.Element, "数组索引结果"); err != nil {
			return nil, nil, err
		}
		isArray = true
		resultID = def.Element
	case frontend.KindTuple:
		def := l.types.tupleDef(baseID)
		if def == nil {
			return nil, nil, l.fail(n.Start, "lower.bad-type", "索引访问的对象不是元组")
		}
		slot, ok := integerLiteral(index)
		if !ok {
			return nil, nil, l.fail(index.Start, "lower.unsupported-array", "元组索引必须是非负整数数字字面量")
		}
		if slot >= len(def.Elements) {
			return nil, nil, l.fail(index.Start, "lower.index-bounds", "元组索引 %d 越界；长度是 %d", slot, len(def.Elements))
		}
		if err := l.typedValue(n, def.Elements[slot], "元组索引结果"); err != nil {
			return nil, nil, err
		}
		indexNode = nir.Number(float64(slot))
		resultID = def.Elements[slot]
	default:
		return nil, nil, l.fail(n.Start, "lower.bad-type", "索引访问只支持数组和元组，实际为 %s", baseType.Kind)
	}

	baseValue, pre, err := l.expr(base, sc, depth+1)
	if err != nil {
		return nil, nil, err
	}
	if isArray {
		// 数组下标可能带准备语句；先把对象物化，保证对象在源码里先于下标求值。
		baseName := l.tempName()
		pre = append(pre, l.emitTemp(sc, baseName, baseValue, l.pos(base.Start), baseID))
		baseValue = localExpr(baseName, l.pos(base.Start))

		indexValue, indexPre, err := l.expr(index, sc, depth+1)
		if err != nil {
			return nil, nil, err
		}
		pre = append(pre, indexPre...)
		indexName := l.tempName()
		pre = append(pre, l.emitTemp(sc, indexName, indexValue, l.pos(index.Start), nir.TypeNumber))
		indexNode = *localExpr(indexName, l.pos(index.Start))
	}
	name := l.tempName()
	l.addTempBinding(sc, name, resultID, l.pos(n.Start))
	pre = append(pre, nir.Stmt{
		Op:    nir.OpIndex,
		Name:  name,
		Type:  resultID,
		Value: baseValue,
		Index: &indexNode,
		Pos:   l.pos(n.Start),
	})
	return localExpr(name, l.pos(n.Start)), pre, nil
}

// integerLiteral 取出元组索引要求的非负整数数字字面量。
func integerLiteral(n *frontend.Node) (int, bool) {
	if n == nil || n.Kind != "NumericLiteral" || n.Bits == "" {
		return 0, false
	}
	bits, err := parseBits(n.Bits)
	if err != nil {
		return 0, false
	}
	v := math.Float64frombits(bits)
	if math.IsNaN(v) || math.IsInf(v, 0) || v != math.Trunc(v) || v < 0 || v > 1<<31-1 {
		return 0, false
	}
	return int(v), true
}

// baseNode 返回属性访问的对象侧；手写事实可能缺子节点。
func baseNode(n *frontend.Node) *frontend.Node {
	if n == nil || len(n.Children) == 0 {
		return nil
	}
	return &n.Children[0]
}

// localExpr 构造一个引用局部值的表达式。
func localExpr(name string, pos *nir.Pos) *nir.Expr {
	return &nir.Expr{Op: nir.OpLocal, Name: name, Pos: pos}
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
		if isNullishNode(leftNode) || isNullishNode(rightNode) {
			return l.nullCompare(n, leftNode, rightNode, sc, depth)
		}
		if leftNode.Type == nil || rightNode.Type == nil {
			return nil, nil, l.fail(n.Start, "lower.bad-type", "严格相等的操作数缺少类型摘要")
		}
		leftID, err := l.types.id(leftNode.Type)
		if err != nil {
			return nil, nil, l.fail(n.Start, "lower.bad-type", "严格相等的左操作数：%v", err)
		}
		rightID, err := l.types.id(rightNode.Type)
		if err != nil {
			return nil, nil, l.fail(n.Start, "lower.bad-type", "严格相等的右操作数：%v", err)
		}
		if leftID != rightID {
			return nil, nil, l.fail(n.Start, "lower.bad-type", "严格相等需要两侧同类型，实际为 %s 与 %s", l.typeLabel(leftID), l.typeLabel(rightID))
		}
		if !l.types.comparable(leftID) {
			return nil, nil, l.fail(n.Start, "lower.bad-type", "严格相等不支持类型 %s", l.typeLabel(leftID))
		}
	}
	left, right, pre, err := l.pairOperands(leftNode, rightNode, sc, depth)
	if err != nil {
		return nil, nil, err
	}
	return &nir.Expr{Op: nir.OpCompare, Operator: n.Op, Left: left, Right: right, Pos: l.pos(n.Start)}, pre, nil
}

// isNullishNode 报告节点是不是 null / undefined 字面量。类型摘要也参与判断，
// 手写事实可能缺 Kind 细节。
func isNullishNode(n *frontend.Node) bool {
	if n == nil {
		return false
	}
	if n.Kind == "NullKeyword" || n.Kind == "UndefinedKeyword" {
		return true
	}
	return n.Type != nil && (n.Type.Kind == frontend.KindNull || n.Type.Kind == frontend.KindUndefined)
}

// nullCompare 降低联合值与 null / undefined 的严格相等。联合值不拆箱，只读取
// 标签；`!==` 在结果上取反。两侧同时是空值时折叠成常量。
func (l *lowerer) nullCompare(n *frontend.Node, leftNode, rightNode *frontend.Node, sc *scope, depth int) (*nir.Expr, []nir.Stmt, error) {
	leftNull, rightNull := isNullishNode(leftNode), isNullishNode(rightNode)
	if leftNull && rightNull {
		value := nir.Bool(n.Op == "===")
		value.Pos = l.pos(n.Start)
		return &value, nil, nil
	}
	unionNode, nullNode := leftNode, rightNode
	if rightNull {
		unionNode, nullNode = leftNode, rightNode
	} else if leftNull {
		unionNode, nullNode = rightNode, leftNode
	}
	if unionNode.Type == nil {
		return nil, nil, l.fail(n.Start, "lower.bad-type", "空值比较的联合值缺少类型摘要")
	}
	unionID, err := l.types.id(unionNode.Type)
	if err != nil {
		return nil, nil, l.fail(n.Start, "lower.bad-type", "空值比较的联合值：%v", err)
	}
	if l.types.unionDef(unionID) == nil {
		// 前端已经把变量缩窄成非联合类型；比较结果固定，但仍然要求值。
		_, pre, err := l.expr(unionNode, sc, depth+1)
		if err != nil {
			return nil, nil, err
		}
		result := nir.Bool(n.Op == "!==")
		result.Pos = l.pos(n.Start)
		return &result, pre, nil
	}
	member, ok := nullishID(nullNode)
	if !ok || !l.types.isUnionMember(unionID, member) {
		return nil, nil, l.fail(n.Start, "lower.bad-type", "联合类型 %s 不包含 %s", l.typeLabel(unionID), nullNode.Kind)
	}
	value, pre, err := l.expr(unionNode, sc, depth+1)
	if err != nil {
		return nil, nil, err
	}
	test := &nir.Expr{Op: nir.OpIs, Type: member, Operand: value, Pos: l.pos(n.Start)}
	if n.Op == "!==" {
		return &nir.Expr{Op: nir.OpNot, Operand: test, Pos: l.pos(n.Start)}, pre, nil
	}
	return test, pre, nil
}

// nullish 降低 value ?? fallback。左侧只求值一次并物化成临时值；select 的
// 条件读取联合标签，只有标签是空值时才执行右侧准备语句。NIR 不需要新操作：
// is 判断空值，unwrap 取出可执行成员，select 选择结果。
func (l *lowerer) nullish(n *frontend.Node, sc *scope, depth int) (*nir.Expr, []nir.Stmt, error) {
	if len(n.Children) != 2 {
		return nil, nil, l.fail(n.Start, "lower.bad-arity", "?? 有 %d 个操作数", len(n.Children))
	}
	leftNode, rightNode := &n.Children[0], &n.Children[1]
	if leftNode.Type == nil || rightNode.Type == nil {
		return nil, nil, l.fail(n.Start, "lower.bad-type", "?? 的操作数缺少类型摘要")
	}
	readID, err := l.valueReadType(leftNode)
	if err != nil {
		return nil, nil, l.fail(n.Start, "lower.bad-type", "?? 的左操作数：%v", err)
	}
	def := l.types.unionDef(readID)
	if def == nil {
		if readID == nir.TypeNull || readID == nir.TypeUndefined {
			return nil, nil, l.fail(n.Start, "lower.bad-type", "?? 的左侧已经是空值")
		}
		// 左侧已缩窄成非空具体类型，右侧不会执行。
		return l.expr(leftNode, sc, depth+1)
	}
	payload, nullish, ok := nullishMembers(def)
	if !ok {
		return nil, nil, l.fail(n.Start, "lower.bad-type", "?? 的左侧需要两成员可空联合，实际为 %s", l.typeLabel(readID))
	}
	left, leftPre, err := l.storageValue(leftNode, sc, depth+1)
	if err != nil {
		return nil, nil, err
	}
	name := l.tempName()
	pre := append(leftPre, l.emitTemp(sc, name, left, l.pos(leftNode.Start), readID))
	leftRef := localExpr(name, l.pos(leftNode.Start))

	thenScope := l.childScope(sc, nir.DebugScopePrelude, leftNode)
	elseScope := l.childScope(sc, nir.DebugScopePrelude, rightNode)
	valueName := l.tempName()
	l.addTempBinding(thenScope, valueName, payload, l.pos(n.Start))
	thenPre := []nir.Stmt{{
		Op:    nir.OpUnwrap,
		Name:  valueName,
		Type:  payload,
		Value: leftRef,
		Pos:   l.pos(n.Start),
	}}
	right, rightPre, err := l.valueAs(rightNode, payload, "?? 的右操作数", elseScope, depth+1)
	if err != nil {
		return nil, nil, err
	}
	resultName := l.tempName()
	l.addTempBinding(sc, resultName, payload, l.pos(n.Start))
	test := &nir.Expr{Op: nir.OpIs, Type: nullish, Operand: leftRef, Pos: l.pos(n.Start)}
	pre = append(pre, nir.Stmt{
		Op:        nir.OpSelect,
		Name:      resultName,
		Cond:      &nir.Expr{Op: nir.OpNot, Operand: test, Pos: l.pos(n.Start)},
		Then:      thenPre,
		ThenValue: localExpr(valueName, l.pos(n.Start)),
		Else:      rightPre,
		ElseValue: right,
		Type:      payload,
		Pos:       l.pos(n.Start),
	})
	return localExpr(resultName, l.pos(n.Start)), pre, nil
}

// storageValue 读取一个值的存储形态，不按前端缩窄拆箱。`??` 必须先看联合
// 标签再决定是否拆箱，不能走 narrowedRead：那会在检查标签之前就把值取成
// 成员类型，空值路径无法生成 select。
func (l *lowerer) storageValue(n *frontend.Node, sc *scope, depth int) (*nir.Expr, []nir.Stmt, error) {
	if n.Kind != "Identifier" || n.Target == 0 {
		return l.expr(n, sc, depth)
	}
	decl, ok := l.facts.Declaration(n.Target)
	if !ok {
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
			return localExpr(name, l.pos(n.Start)), nil, nil
		}
		if l.inInit {
			return nil, nil, l.fail(n.Start, "lower.unknown-name", "模块常量 %q 在初始化作用域里不可见", n.Name)
		}
		return nil, nil, l.fail(n.Start, "lower.module-const-in-function", "模块常量 %q 在函数内不可见", n.Name)
	case frontend.DeclLocal, frontend.DeclLet:
		name, ok := sc.lookup(decl.ID)
		if !ok {
			return nil, nil, l.fail(n.Start, "lower.unknown-name", "局部值 %q 不在当前作用域内", n.Name)
		}
		return localExpr(name, l.pos(n.Start)), nil, nil
	default:
		return nil, nil, l.fail(n.Start, "lower.unsupported-expr", "?? 的左侧存储不支持声明种类 %s", decl.Kind)
	}
}

// nullishMembers 拆出两成员可空联合的空值成员和可执行成员。
func nullishMembers(def *nir.TypeDef) (payload, nullish int, ok bool) {
	for _, member := range def.Members {
		if member == nir.TypeNull || member == nir.TypeUndefined {
			if nullish != 0 {
				return 0, 0, false
			}
			nullish = member
			continue
		}
		if payload != 0 {
			return 0, 0, false
		}
		payload = member
	}
	return payload, nullish, payload != 0 && nullish != 0
}

// nullishID 返回空值节点对应的内置类型 ID。
func nullishID(n *frontend.Node) (int, bool) {
	if n == nil {
		return 0, false
	}
	switch n.Kind {
	case "NullKeyword":
		return nir.TypeNull, true
	case "UndefinedKeyword":
		return nir.TypeUndefined, true
	}
	if n.Type != nil {
		switch n.Type.Kind {
		case frontend.KindNull:
			return nir.TypeNull, true
		case frontend.KindUndefined:
			return nir.TypeUndefined, true
		}
	}
	return 0, false
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
	if callee.Kind == "Identifier" {
		if decl, ok := l.facts.Declaration(callee.Target); ok && decl.Kind == frontend.DeclFunction {
			return l.directCall(n, callee, decl, sc, depth)
		}
	}
	if callee.Type != nil && callee.Type.Kind == frontend.KindFunction {
		return l.indirectCall(n, callee, sc, depth)
	}
	return nil, nil, l.fail(n.Start, "lower.unknown-callee", "调用目标 %q 不是函数值", callee.Name)
}

// directCall 降低对具名函数的直接调用。
func (l *lowerer) directCall(n, callee *frontend.Node, decl *frontend.Declaration, sc *scope, depth int) (*nir.Expr, []nir.Stmt, error) {
	if len(l.facts.TypeParametersOf(decl)) > 0 {
		return l.genericDirectCall(n, decl, sc, depth)
	}
	args := n.Children[1:]
	if arity := l.facts.ArityOf(decl); !arity.Allows(len(args)) {
		return nil, nil, l.fail(n.Start, "lower.bad-arity", "调用 %q 给了 %d 个实参，声明需要 %d 到 %d 个", decl.Name, len(args), arity.Required, arity.Total)
	}
	paramIDs := make([]int, 0, len(decl.Parameters))
	for i := range decl.Parameters {
		param, ok := l.facts.Declaration(decl.Parameters[i])
		if !ok || param.Type == nil {
			return nil, nil, l.fail(n.Start, "lower.unknown-param", "函数 %q 的第 %d 个参数没有类型摘要", decl.Name, i+1)
		}
		paramID, err := l.types.id(param.Type)
		if err != nil {
			return nil, nil, l.fail(n.Start, "lower.bad-type", "函数 %q 的参数：%v", decl.Name, err)
		}
		paramIDs = append(paramIDs, paramID)
	}
	exprs, pre, err := l.callArguments(args, paramIDs, sc, depth)
	if err != nil {
		return nil, nil, err
	}
	exprs, pre, err = l.fillOptionalArgs(decl, len(args), paramIDs, exprs, pre, sc, n.Start)
	if err != nil {
		return nil, nil, err
	}
	return &nir.Expr{Op: nir.OpCall, Function: decl.Name, Args: exprs, Pos: l.pos(n.Start)}, pre, nil
}

// fillOptionalArgs 为省略的尾部可选参数补一个 undefined，并包成参数声明的
// 可空联合。省略的参数没有求值顺序问题，补值放在所有已给实参之后即可。
func (l *lowerer) fillOptionalArgs(decl *frontend.Declaration, provided int, paramIDs []int, exprs []nir.Expr, pre []nir.Stmt, sc *scope, offset int) ([]nir.Expr, []nir.Stmt, error) {
	pos := l.pos(offset)
	for i := provided; i < len(decl.Parameters); i++ {
		param, ok := l.facts.Declaration(decl.Parameters[i])
		if !ok || param.Type == nil {
			return nil, nil, l.fail(offset, "lower.unknown-param", "函数 %q 的第 %d 个参数没有类型摘要", decl.Name, i+1)
		}
		if !param.Optional {
			return nil, nil, l.fail(offset, "lower.bad-arity", "调用 %q 缺少必填参数 %q", decl.Name, param.Name)
		}
		if !l.types.isUnionMember(paramIDs[i], nir.TypeUndefined) {
			return nil, nil, l.fail(offset, "lower.bad-type", "可选参数 %q 的类型不是包含 undefined 的联合", param.Name)
		}
		name := l.tempName()
		l.addTempBinding(sc, name, paramIDs[i], pos)
		pre = append(pre, nir.Stmt{
			Op:    nir.OpWrap,
			Name:  name,
			Type:  paramIDs[i],
			Value: &nir.Expr{Op: nir.OpUndefined, Pos: pos},
			Pos:   pos,
		})
		exprs = append(exprs, *localExpr(name, pos))
	}
	return exprs, pre, nil
}

// genericDirectCall 在调用点推断类型实参，取得（或创建）对应的单态化实例，
// 再按实例的具体参数类型降低实参。
func (l *lowerer) genericDirectCall(n *frontend.Node, decl *frontend.Declaration, sc *scope, depth int) (*nir.Expr, []nir.Stmt, error) {
	args := n.Children[1:]
	if len(args) != len(decl.Parameters) {
		return nil, nil, l.fail(n.Start, "lower.bad-arity", "调用 %q 给了 %d 个实参，声明有 %d 个参数", decl.Name, len(args), len(decl.Parameters))
	}
	params := make([]frontend.Type, 0, len(decl.Parameters))
	names := l.facts.TypeParametersOf(decl)
	// 被调用函数自己的类型参数不能被调用方的实参替换掉，否则递归调用
	// down<T> 会把形参 T 提前变成外层实例的 number，推断就失去了变量。
	paramSubst := make(map[string]frontend.Type, len(l.types.subst))
	for name, value := range l.types.subst {
		paramSubst[name] = value
	}
	for _, name := range names {
		delete(paramSubst, name)
	}
	for _, id := range decl.Parameters {
		param, ok := l.facts.Declaration(id)
		if !ok || param.Type == nil {
			return nil, nil, l.fail(n.Start, "lower.unknown-param", "泛型函数 %q 的参数没有类型摘要", decl.Name)
		}
		resolved, err := l.substituteWith(l.facts.ResolveType(param.Type), paramSubst)
		if err != nil {
			return nil, nil, l.fail(n.Start, "lower.bad-type", "泛型函数 %q 的参数：%v", decl.Name, err)
		}
		params = append(params, *resolved)
	}
	actuals := make([]frontend.Type, len(args))
	for i := range args {
		if args[i].Type == nil {
			return nil, nil, l.fail(args[i].Start, "lower.bad-type", "泛型调用 %q 的实参缺少类型摘要", decl.Name)
		}
		resolved, err := l.substituteCurrent(l.facts.ResolveType(args[i].Type))
		if err != nil {
			return nil, nil, l.fail(args[i].Start, "lower.bad-type", "泛型调用 %q 的实参：%v", decl.Name, err)
		}
		actuals[i] = *resolved
	}
	subst, ok := frontend.InferTypeParams(params, actuals)
	if !ok {
		return nil, nil, l.fail(n.Start, "lower.bad-type", "无法从实参推断泛型函数 %q 的类型实参", decl.Name)
	}
	typeIDs := make([]int, 0, len(names))
	for _, name := range names {
		value, ok := subst[name]
		if !ok {
			return nil, nil, l.fail(n.Start, "lower.bad-type", "泛型函数 %q 的类型参数 %q 无法推断", decl.Name, name)
		}
		id, err := l.types.id(&value)
		if err != nil {
			return nil, nil, l.fail(n.Start, "lower.bad-type", "泛型函数 %q 的类型实参：%v", decl.Name, err)
		}
		typeIDs = append(typeIDs, id)
	}
	instance, err := l.instantiate(decl, subst, typeIDs, depth)
	if err != nil {
		return nil, nil, err
	}
	paramIDs := make([]int, len(instance.fn.Params))
	for i := range instance.fn.Params {
		paramIDs[i] = instance.fn.Params[i].Type
	}
	exprs, pre, err := l.callArguments(args, paramIDs, sc, depth)
	if err != nil {
		return nil, nil, err
	}
	return &nir.Expr{Op: nir.OpCall, Function: instance.name, Args: exprs, Pos: l.pos(n.Start)}, pre, nil
}

// instantiate 为一次泛型调用生成具体函数。缓存项先登记名字和参数表，再降低
// 函数体，递归或相互递归的泛型调用因此不会无限展开。
func (l *lowerer) instantiate(decl *frontend.Declaration, subst map[string]frontend.Type, typeIDs []int, depth int) (*genericInstance, error) {
	key := fmt.Sprintf("%d", decl.ID)
	for _, id := range typeIDs {
		key += fmt.Sprintf(":%d", id)
	}
	if instance, ok := l.genericInstances[key]; ok {
		return instance, nil
	}
	name := l.instanceName(decl.Name, typeIDs)
	fn := &nir.Function{Name: name, Pos: l.pos(decl.Start)}
	instance := &genericInstance{name: name, fn: fn}
	l.genericInstances[key] = instance

	savedResult, savedDebug, savedScope, savedLoops := l.result, l.debugFn, l.nextScope, l.loops
	savedSnapshot, savedModule, savedInit := l.snapshot, l.currentModule, l.inInit
	savedSubst, savedArrows := l.types.subst, l.arrowParams
	restore := func() {
		l.result, l.debugFn, l.nextScope, l.loops = savedResult, savedDebug, savedScope, savedLoops
		l.snapshot, l.currentModule, l.inInit = savedSnapshot, savedModule, savedInit
		l.types.subst, l.arrowParams = savedSubst, savedArrows
	}
	l.loops = nil
	l.inInit = false
	l.arrowParams = nil
	if snapshot := l.snapshots[decl.Module]; snapshot != nil {
		l.snapshot = snapshot
	}
	l.currentModule = decl.Module
	l.types.subst = subst
	l.beginDebugFunction(name)
	if decl.Body == nil {
		l.finishDebugFunction()
		restore()
		return nil, l.fail(decl.Start, "lower.missing-body", "泛型函数 %q 没有函数体", decl.Name)
	}
	if decl.Result == nil {
		l.finishDebugFunction()
		restore()
		return nil, l.fail(decl.Start, "lower.bad-type", "泛型函数 %q 缺少返回类型摘要", decl.Name)
	}
	resultID, err := l.types.id(decl.Result)
	if err != nil {
		l.finishDebugFunction()
		restore()
		return nil, l.fail(decl.Start, "lower.bad-type", "泛型函数 %q 的返回类型：%v", decl.Name, err)
	}
	fn.Result = resultID
	sc := l.rootScope(decl.Start, decl.Length)
	sc.params = make(map[int]string, len(decl.Parameters))
	for _, id := range decl.Parameters {
		param, ok := l.facts.Declaration(id)
		if !ok || param.Type == nil {
			l.finishDebugFunction()
			restore()
			return nil, l.fail(decl.Start, "lower.unknown-param", "泛型函数 %q 的参数 ID %d 不存在", decl.Name, id)
		}
		paramID, err := l.types.id(param.Type)
		if err != nil {
			l.finishDebugFunction()
			restore()
			return nil, l.fail(param.Start, "lower.bad-type", "泛型函数 %q 的参数 %q：%v", decl.Name, param.Name, err)
		}
		fn.Params = append(fn.Params, nir.Param{Name: param.Name, Type: paramID, Pos: l.pos(param.Start)})
		sc.params[id] = param.Name
		l.addDebugLocal(sc, param.Name, param.Name, nir.DebugLocalParameter, paramID, l.pos(param.Start), false)
	}
	l.result = resultID
	body, err := l.block(decl.Body, sc, resultID)
	if err != nil {
		l.finishDebugFunction()
		restore()
		return nil, err
	}
	fn.Body = body
	l.finishDebugFunction()
	restore()
	l.lifted = append(l.lifted, *fn)
	return instance, nil
}

// substituteCurrent 把当前实例的类型实参应用到类型摘要上；没有实例时原样
// 返回。它用于在推断前把外层泛型函数的 T 变成具体类型。
func (l *lowerer) substituteCurrent(t *frontend.Type) (*frontend.Type, error) {
	return l.substituteWith(t, l.types.subst)
}

func (l *lowerer) substituteWith(t *frontend.Type, subst map[string]frontend.Type) (*frontend.Type, error) {
	if t == nil {
		return nil, fmt.Errorf("缺少类型摘要")
	}
	if len(subst) == 0 {
		return t, nil
	}
	return frontend.SubstituteType(t, subst)
}

// instanceName 用具体类型 ID 生成稳定的实例名。类型 ID 在同一个模块内稳定；
// 名字冲突时追加序号，避免用户声明占用同名。
func (l *lowerer) instanceName(base string, typeIDs []int) string {
	var b strings.Builder
	b.WriteString(base)
	b.WriteString("$t")
	for i, id := range typeIDs {
		if i > 0 {
			b.WriteByte('_')
		}
		fmt.Fprintf(&b, "%d", id)
	}
	name := b.String()
	if !l.reserved[name] {
		l.reserved[name] = true
		return name
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s$%d", name, i)
		if !l.reserved[candidate] {
			l.reserved[candidate] = true
			return candidate
		}
	}
}

// indirectCall 降低通过函数值发起的调用。被调用者先于实参求值；实参顺序与
// 直接调用共用同一套物化规则。
func (l *lowerer) indirectCall(n, callee *frontend.Node, sc *scope, depth int) (*nir.Expr, []nir.Stmt, error) {
	calleeID, err := l.types.id(callee.Type)
	if err != nil {
		return nil, nil, l.fail(n.Start, "lower.bad-type", "间接调用的函数值：%v", err)
	}
	def := l.types.functionDef(calleeID)
	if def == nil {
		return nil, nil, l.fail(n.Start, "lower.bad-type", "间接调用的目标不是函数类型")
	}
	args := n.Children[1:]
	if len(args) != len(def.Params) {
		return nil, nil, l.fail(n.Start, "lower.bad-arity", "间接调用给了 %d 个实参，函数类型需要 %d 个", len(args), len(def.Params))
	}
	base, basePre, err := l.expr(callee, sc, depth+1)
	if err != nil {
		return nil, nil, err
	}
	exprs, argPre, err := l.callArguments(args, def.Params, sc, depth)
	if err != nil {
		return nil, nil, err
	}
	pre := append(basePre, argPre...)
	return &nir.Expr{Op: nir.OpInvoke, Callee: base, Args: exprs, Type: def.Result, Pos: l.pos(n.Start)}, pre, nil
}

// callArguments 按源码顺序降低实参，并在某个实参带准备语句时先把前面的实参
// 物化，避免后面的副作用跑到前面。
func (l *lowerer) callArguments(args []frontend.Node, paramIDs []int, sc *scope, depth int) ([]nir.Expr, []nir.Stmt, error) {
	exprs := make([]nir.Expr, len(args))
	var pre []nir.Stmt
	var pending []int
	for i := range args {
		arg, argPre, err := l.valueAs(&args[i], paramIDs[i], "调用的实参", sc, depth+1)
		if err != nil {
			return nil, nil, err
		}
		exprs[i] = *arg
		if len(argPre) == 0 {
			pending = append(pending, i)
			continue
		}
		for _, j := range pending {
			name := l.tempName()
			value := exprs[j]
			pre = append(pre, l.emitTemp(sc, name, &value, l.pos(args[j].Start), l.nodeNirType(&args[j])))
			exprs[j] = nir.Expr{Op: nir.OpLocal, Name: name}
		}
		pending = nil
		pre = append(pre, argPre...)
		name := l.tempName()
		value := exprs[i]
		pre = append(pre, l.emitTemp(sc, name, &value, l.pos(args[i].Start), l.nodeNirType(&args[i])))
		exprs[i] = nir.Expr{Op: nir.OpLocal, Name: name}
	}
	return exprs, pre, nil
}

// liftArrow 把无捕获的箭头函数提升成合成具名函数，并返回一个函数值表达式。
// NIR 没有匿名函数节点：函数体必须先有名字才能被间接调用。
func (l *lowerer) liftArrow(n *frontend.Node, depth int) (*nir.Expr, []nir.Stmt, error) {
	if n.Type == nil || n.Type.Kind != frontend.KindFunction {
		return nil, nil, l.fail(n.Start, "lower.bad-type", "箭头函数缺少函数类型摘要")
	}
	funcID, err := l.types.id(n.Type)
	if err != nil {
		return nil, nil, l.fail(n.Start, "lower.bad-type", "箭头函数：%v", err)
	}
	def := l.types.functionDef(funcID)
	if def == nil {
		return nil, nil, l.fail(n.Start, "lower.bad-type", "箭头函数的类型不是 function 类型")
	}
	resolved := l.facts.ResolveType(n.Type)
	if len(n.Children) != 1 || n.Children[0].Kind == "Block" {
		return nil, nil, l.fail(n.Start, "lower.unsupported-arrow", "块体箭头函数不在本阶段子集内")
	}
	bodyNode := &n.Children[0]

	name := l.arrowName()
	savedResult, savedDebug, savedScope, savedLoops := l.result, l.debugFn, l.nextScope, l.loops
	l.loops = nil
	l.beginDebugFunction(name)
	fn := &nir.Function{Name: name, Result: def.Result, Pos: l.pos(n.Start)}
	sc := l.rootScope(n.Start, n.Length)
	sc.params = map[int]string{}
	params := make(map[string]bool, len(resolved.Params))
	for i := range resolved.Params {
		paramName := resolved.Params[i].Name
		if paramName == "" {
			paramName = fmt.Sprintf("p%d", i)
		}
		if params[paramName] {
			l.finishDebugFunction()
			l.restoreFunctionState(savedResult, savedDebug, savedScope, savedLoops)
			return nil, nil, l.fail(n.Start, "lower.duplicate-name", "箭头函数参数 %q 重复", paramName)
		}
		paramID, err := l.types.id(&resolved.Params[i].Type)
		if err != nil {
			l.finishDebugFunction()
			l.restoreFunctionState(savedResult, savedDebug, savedScope, savedLoops)
			return nil, nil, l.fail(n.Start, "lower.bad-type", "箭头函数参数：%v", err)
		}
		if paramID == nir.TypeVoid {
			l.finishDebugFunction()
			l.restoreFunctionState(savedResult, savedDebug, savedScope, savedLoops)
			return nil, nil, l.fail(n.Start, "lower.bad-type", "箭头函数参数不能是 void")
		}
		params[paramName] = true
		fn.Params = append(fn.Params, nir.Param{Name: paramName, Type: paramID, Pos: l.pos(n.Start)})
		l.addDebugLocal(sc, paramName, paramName, nir.DebugLocalParameter, paramID, l.pos(n.Start), false)
	}
	l.arrowParams = append(l.arrowParams, params)
	l.result = def.Result
	value, pre, err := l.valueAs(bodyNode, def.Result, "箭头函数返回值", sc, depth+1)
	l.arrowParams = l.arrowParams[:len(l.arrowParams)-1]
	if err != nil {
		l.finishDebugFunction()
		l.restoreFunctionState(savedResult, savedDebug, savedScope, savedLoops)
		return nil, nil, err
	}
	fn.Body = pre
	if def.Result == nir.TypeVoid {
		fn.Body = append(fn.Body,
			nir.Stmt{Op: nir.OpDiscard, Value: value, Pos: l.pos(bodyNode.Start)},
			nir.Stmt{Op: nir.OpReturn, Pos: l.pos(n.Start)},
		)
	} else {
		fn.Body = append(fn.Body, nir.Stmt{Op: nir.OpReturn, Value: value, Pos: l.pos(bodyNode.Start)})
	}
	l.finishDebugFunction()
	l.restoreFunctionState(savedResult, savedDebug, savedScope, savedLoops)
	l.lifted = append(l.lifted, *fn)
	return &nir.Expr{Op: nir.OpFunc, Function: name, Type: funcID, Pos: l.pos(n.Start)}, nil, nil
}

func (l *lowerer) restoreFunctionState(result int, debug *nir.DebugFunction, scope int, loops []loopFrame) {
	l.result = result
	l.debugFn = debug
	l.nextScope = scope
	l.loops = loops
}

// arrowName 分配一个不与用户声明冲突的合成函数名。
func (l *lowerer) arrowName() string {
	for {
		l.nextArrow++
		name := fmt.Sprintf("_arrow_%d", l.nextArrow)
		if !l.reserved[name] {
			l.reserved[name] = true
			return name
		}
	}
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
		pre = append(pre, l.emitTemp(sc, name, &value, l.pos(leftNode.Start), l.nodeNirType(leftNode)))
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
	thenID, err := l.valueTypeID(thenNode.Type)
	if err != nil {
		return nil, nil, l.fail(n.Start, "lower.bad-type", "三元式的分支：%v", err)
	}
	elseID, err := l.valueTypeID(elseNode.Type)
	if err != nil {
		return nil, nil, l.fail(n.Start, "lower.bad-type", "三元式的分支：%v", err)
	}
	if thenID != elseID {
		return nil, nil, l.fail(n.Start, "lower.bad-type", "三元式两条分支类型不同：%s 与 %s", l.typeLabel(thenID), l.typeLabel(elseID))
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
	resultType := thenID
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

// valueTypeID 返回一个可用于值位置（参数、局部值、返回值）的类型 ID。
// void 只属于函数结果，不能出现在这里。
func (l *lowerer) valueTypeID(t *frontend.Type) (int, error) {
	id, err := l.types.id(t)
	if err != nil {
		return 0, err
	}
	if id == nir.TypeVoid {
		return 0, fmt.Errorf("void 不是值类型")
	}
	return id, nil
}

// valueAs 降低一个值位置，并在目标类型是联合时把成员值包成联合。它替代
// 「先 typedValue 再 expr」的组合，因为联合成员到联合需要一个显式的 wrap
// 语句，不能只做类型核对。
func (l *lowerer) valueAs(n *frontend.Node, want int, what string, sc *scope, depth int) (*nir.Expr, []nir.Stmt, error) {
	if n == nil || n.Type == nil {
		return nil, nil, l.fail(nodeStart(n), "lower.bad-type", "%s 缺少类型摘要", what)
	}
	got, err := l.valueReadType(n)
	if err != nil {
		return nil, nil, l.fail(n.Start, "lower.bad-type", "%s：%v", what, err)
	}
	if got == want {
		return l.expr(n, sc, depth)
	}
	if !l.types.isUnionMember(want, got) {
		return nil, nil, l.fail(n.Start, "lower.bad-type", "%s是 %s，需要 %s", what, l.typeLabel(got), l.typeLabel(want))
	}
	value, pre, err := l.expr(n, sc, depth)
	if err != nil {
		return nil, nil, err
	}
	name := l.tempName()
	l.addTempBinding(sc, name, want, l.pos(n.Start))
	pre = append(pre, nir.Stmt{Op: nir.OpWrap, Name: name, Type: want, Value: value, Pos: l.pos(n.Start)})
	return localExpr(name, l.pos(n.Start)), pre, nil
}

// valueReadType 返回一个值位置实际会产生的类型 ID。标识符可能被前端缩窄成
// null / undefined，但它的存储仍是联合；读取时 narrowedRead 会返回联合局部值，
// 因此这里按存储类型处理，避免把它再包一层联合。
func (l *lowerer) valueReadType(n *frontend.Node) (int, error) {
	if n == nil || n.Type == nil {
		return 0, fmt.Errorf("缺少类型摘要")
	}
	if n.Kind == "Identifier" && n.Target != 0 {
		if decl, ok := l.facts.Declaration(n.Target); ok && decl.Type != nil {
			storage, err := l.types.id(decl.Type)
			if err == nil && l.types.unionDef(storage) != nil {
				got, err := l.types.id(n.Type)
				if err != nil {
					return 0, err
				}
				if got == nir.TypeNull || got == nir.TypeUndefined || got == storage {
					return storage, nil
				}
			}
		}
	}
	return l.types.id(n.Type)
}

// typedValue 核对表达式的类型 ID。结构类型已经在类型表里去重，因此这里直接
// 比较 ID，不再按名字判断。
func (l *lowerer) typedValue(n *frontend.Node, want int, what string) error {
	if n == nil || n.Type == nil {
		return l.fail(nodeStart(n), "lower.bad-type", "%s 缺少类型摘要", what)
	}
	got, err := l.types.id(n.Type)
	if err != nil {
		return l.fail(n.Start, "lower.bad-type", "%s：%v", what, err)
	}
	if got != want {
		return l.fail(n.Start, "lower.bad-type", "%s是 %s，需要 %s", what, l.typeLabel(got), l.typeLabel(want))
	}
	return nil
}

// typeLabel 返回诊断里的类型短名。对象类型使用类型表里的名字或 ID。
func (l *lowerer) typeLabel(id int) string {
	if name := nir.BuiltinTypeName(id); name != "" {
		return name
	}
	for i := range l.types.defs {
		def := &l.types.defs[i]
		if def.ID == id {
			switch def.Kind {
			case nir.TypeKindArray:
				return fmt.Sprintf("array#%d", id)
			case nir.TypeKindTuple:
				return fmt.Sprintf("tuple#%d", id)
			case nir.TypeKindUnion:
				return fmt.Sprintf("union#%d", id)
			}
			if def.Name != "" {
				return def.Name
			}
			return fmt.Sprintf("object#%d", id)
		}
	}
	return fmt.Sprintf("type#%d", id)
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

// nodeNirType 返回表达式节点在 NIR 里的类型 ID。调用方已经核对过类型，这里
// 只用于临时值的调试信息；缺类型时退回 number。
func (l *lowerer) nodeNirType(n *frontend.Node) int {
	if n != nil && n.Type != nil {
		if id, err := l.types.id(n.Type); err == nil {
			return id
		}
	}
	return nir.TypeNumber
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
