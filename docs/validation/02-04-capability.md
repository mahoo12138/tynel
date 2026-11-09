# 02.04 原生能力白名单：验收记录

记录 02.04 的实际运行结果。被测代码是正文带 `Task: 02.04` 的提交；本文件不预填不存在的哈希。

## 环境

| 项目 | 值 |
| --- | --- |
| 机型 | Mac mini，机型标识 Mac16,10 |
| 芯片 | Apple M4，10 核，24 GB 内存 |
| 系统 | macOS 26.6.2，构建号 25G83 |
| 隔离工具链 | go1.27.3 darwin/arm64 |
| 前端源码 | `microsoft/typescript-go`，`typescript/v7.0.2`，提交 `2bd066d8` |

## 正向验收

### 支持子集通过

`bridge/tsgo/testdata/capability/ok.ts` 覆盖本阶段全部写法：number 函数、参数、const、四则运算、直接调用、`console.log(number)`。

```text
$ consumer -capability bridge/tsgo/testdata/capability/ok.ts
[]
$ echo $?
0
```

### 受限输出核对符号来源

`console.log` 只有在内置 `console` 上才被接受。单元测试 `TestConsoleCall` 构造了三种情况：

| 输入 | 结果 |
| --- | --- |
| `console` 解析到文件外（`target = 0`） | 通过 |
| `console` 解析到本文件的声明 | `cap.shadowed-console` |
| 实参是 `string` 或个数不是 1 | `cap.bad-console-call` |

### 未调用函数同样检查

`capability/uncalled.ts` 里的 `neverCalled` 没有任何调用点，里面的 `if` 照样被报出来。检查不按可达性跳过任何函数。

## 负向验收

`./bridge/tsgo/check` 的 21 项全部通过，其中能力相关 10 项：

```text
PASS  支持子集通过能力检查（退出 0）
PASS  any.ts 按退出 3 拒绝（cap.any）
PASS  assert.ts 按退出 3 拒绝（cap.type-escape）
PASS  object.ts 按退出 3 拒绝（cap.unsupported-syntax）
PASS  eval.ts 按退出 3 拒绝（cap.forbidden-global）
PASS  dynamic-function.ts 按退出 3 拒绝（cap.unsupported-syntax）
PASS  library.ts 按退出 3 拒绝（cap.unknown-callee）
PASS  uncalled.ts 按退出 3 拒绝（cap.unsupported-syntax）
PASS  源码错误先于能力检查（退出 1）
```

| 夹具 | 写法 | 错误码 |
| --- | --- | --- |
| `any.ts` | `function identity(value: any): any` | `cap.any` |
| `assert.ts` | `(1 as any) as number` | `cap.type-escape` |
| `object.ts` | `const pair = { left: 1, right: 2 }` | `cap.unsupported-syntax` |
| `eval.ts` | `eval("1")` | `cap.forbidden-global` |
| `dynamic-function.ts` | `new Function("return 1")` | `cap.unsupported-syntax` |
| `library.ts` | `Math.max(1, 2)` | `cap.unknown-callee` |
| `uncalled.ts` | 未调用函数里的 `if` | `cap.unsupported-syntax` |

单元测试还覆盖了夹具造不出来的情况：函数值（`cap.unsupported-syntax`）、`%` 运算符（`cap.bad-operator`）、实参个数不符（`cap.arg-count`）、`console` 被遮蔽（`cap.shadowed-console`）、事实不自洽时拒绝继续检查（`facts.duplicate-id`）。

### 分层：源码错误先于能力检查

```text
$ consumer -capability bridge/tsgo/testdata/bad.ts
[ … "code": 2322 … ]
$ echo $?
1
```

类型错误存在时不会给出能力结论——类型都没定下来，「有没有原生表示」无从判断。

## 用例统计

| 范围 | 用例数 |
| --- | --- |
| `compiler/capability` | 11 个 `=== RUN`（10 个测试函数） |
| 宿主 `bridge/tsgo/consumer` | 28 个 `=== RUN`（含 8 个能力夹具子用例） |
| `./bridge/tsgo/check` | 21 项 |

`./tools/test` 覆盖 `compiler/capability`，9 个包通过。

## 过程中发现的问题

1. **`Kind.String()` 带前缀**。stringer 生成的是 `KindBlock`、`KindReturnStatement`，而能力检查按 `Block`、`ReturnStatement` 匹配，结果所有节点都被当成「未知语法」。适配层统一去掉 `Kind` 前缀。
2. **运算符 token 不能调 `Text()`**。对 `*ast.BinaryOperatorToken` 调 `Text()` 会 panic（`Unhandled case in Node.Text: *ast.Token`）。改成按 `Kind` 映射成符号文本，未识别的保留种类名交给能力检查拒绝。
3. **前向调用在前一趟里丢了**。为了不把库符号（`Math`、`console`）错认成本文件声明，引用改成「只查已登记符号」。但声明与函数体在同一趟里处理，`add(40, 2)` 出现在 `add` 声明之前时查不到，`callee` 变成 0。改成两趟：先登记全部声明，再遍历函数体。
4. **属性访问多收了一个子节点**。`console.log` 的 `PropertyAccessExpression` 通用递归会把属性名 `log` 也当成 Identifier 收进来，看起来像两个子节点。改为只收对象那一侧。

## 与计划的偏差

- **事实在 02.04 增加了语法骨架**。02.03 的事实只有声明、引用、调用和表达式类型，没有语句结构；而「遍历所有载入函数体」必须有函数体的形状。骨架作为 `body` 字段挂在声明上，02.05 的降低阶段直接用它。
- **`console.log` 在能力层接受，实际生成留到 02.06**。能力检查回答的是「能不能进后端」，02.06 才实现输出。中间的 02.05 只降低数值函数，遇到 `console.log` 会按未实现处理——这是阶段划分的缝隙，不是能力判断的错。

## 交付后的跨模块引用加固（2026-11-09）

上面各节的命令输出和用例统计属于 02.04 原始验收；本节记录之后的一次修复，执行时间是 2026-11-09 19:09 CST，代码基于提交 `15fa96e` 的工作树。环境同为 macOS 26.6.2、arm64 与锁定的 go1.27.3，Node 为 v22.22.2，命令行程序由 `./tools/build` 从当前工作树生成，夹具放在不入库的 `.work/validation/global-call/` 与 `.work/validation/alias-import/`。

问题出在跨模块引用的放行条件。前端适配层的 `target()` 从符号声明所在的文件取模块名，标准库的声明文件一样会给出这个字段。`consumer -facts` 对 `console.log(isFinite(1));` 给出：

```json
{"start": 12, "callee": 0, "calleeModule": "bundled:///libs/lib.es5.d.ts", "calleeName": "isFinite"}
```

而 `imports` 是空的。旧实现里 `crossModuleCall()` 只看 `CalleeModule` 与 `CalleeName` 非空就返回真，`crossModuleRef()` 对 `TargetModule` / `TargetName` 也一样；能力检查于是直接放行，降低阶段拿到 `Target` 为 0 的调用，报成内部故障：`console.log(Infinity)` 则是 `lower.unknown-name`：

```text
$ ./bin/tynel check --root .work/validation/global-call main.ts
tynel: 内部故障 (host.failed): 前端宿主执行失败（退出状态 5）
  阶段: frontend
  原因: tynel: 内部故障 (lower.unknown-callee): 调用目标 "isFinite" 不是本文件里的函数
      阶段: lower
      位置: …/global-call/main.ts:1:13
$ echo $?
5
```

修法是把两条判断合并成一套：`checker` 在 `Check` 开头从 `facts.Imports` 收集非 type-only 的值导入，键是 `(模块, 导出名, 本地名)`；`crossModuleUse()` 按节点种类分别查 `Calls` 或 `References`，取出前端解析的 `(模块, 导出名)` 后连同源码里的本地名交给 `resolvesImport()` 核对。模块名等于当前模块的也不算跨模块引用。真正的项目模块在 `graph.Load` 里解析过路径，导入名由前端对着目标模块的导出表解析，`modules.Build` 之后还会用目标模块的事实复核一次；标准库声明文件没有导入绑定，自然被挡在能力层。修复后：

```text
$ ./bin/tynel check --root .work/validation/global-call main.ts
…/global-call/main.ts:1:13: 能力限制 cap.unknown-callee: 调用目标 "isFinite" 不是本文件里声明的函数
$ echo $?
3

$ ./bin/tynel check --root .work/validation/global-call global-value.ts
…/global-call/global-value.ts:1:13: 能力限制 cap.unknown-name: 名称 "Infinity" 不是本文件里的参数或常量
$ echo $?
3
```

回归分三层。`compiler/capability/capability_test.go` 的 `TestCrossModuleUsesShareOneRule` 把同一组 `(模块, 导出名, 本地名, 导入表)` 分别做成 `CallExpression` 与 `Identifier`，断言两种形状放行结论一致：标准库全局函数与全局值、项目模块但没导入、导出名/模块路径/本地名对不上、type-only 导入、当前模块冒充跨模块，共 8 组一律拒绝，普通导入与重命名导入 2 组放行；`TestLocalCallWithSameModuleTargetPasses` 锁住本文件调用不受影响。`tests/source` 增加 `global-call.ts` 与 `global-value.ts` 两个端到端能力用例，走真实前端事实。`cmd/tynel/diagnostics_test.go` 增加真实宿主的 `TestBuiltinGlobalCallIsCapabilityError`（断言退出 3 且不出现 `lower.unknown-callee` / `host.failed`）和 `TestProjectImportsStillResolve`（重命名导入的跨模块函数与顶层 `const` 一路构建成原生程序，输出 `7`）。

把 `HEAD` 里的旧 `capability.go` 临时换回后，单测有 8 个子用例失败、CLI 用例以「退出状态为 5，期望 3」失败，说明这些用例确实守住了缺陷：

```text
--- FAIL: TestCrossModuleUsesShareOneRule/标准库全局函数
    capability_test.go:686: call：放行=true，期望 false；诊断 []
--- FAIL: TestBuiltinGlobalCallIsCapabilityError
    diagnostics_test.go:311: 退出状态为 5，期望 3；stderr=… lower.unknown-callee …
```

恢复修复后的全量检查，并确认已有跨模块程序没有回归：

```text
$ ./tools/test
测试通过：20 个包

$ ./.work/toolchain/go1.27.3-sdk-darwin-arm64/bin/go vet ./...
（无输出）

$ ./.work/toolchain/go1.27.3-sdk-darwin-arm64/bin/gofmt -l compiler/capability cmd/tynel compiler/lower
（无输出）

$ for d in modules merge init alias linear diamond; do printf '%s: ' "$d"; ./bin/tynel run --root "compiler/graph/testdata/$d" main.ts | tr '\n' ' '; echo; done
modules: 41 42
merge: 55
init: 1 3 2 4 23
alias: 42
linear: 2
diamond: 5

$ ./bin/tynel run --root examples/quote main.ts
90
10
100
```

`docs/reference/capability.md` 补了「跨模块引用怎么核对」一节并更新两个错误码的说明，`docs/reference/module-bindings.md` 点明能力检查先用本文件的 `Imports` 挡一道、`modules.Build` 再复核导出表。本次未覆盖：默认导入、命名空间导入与再导出仍不在子集内；伪造事实里「导入与目标模块导出表不一致」由 `modules.Build` 的 `module.missing-export` 负责，能力层只核对本文件的导入绑定。仍只在 `darwin/arm64` 上验证。
