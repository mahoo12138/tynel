# 02.03 声明身份与事实：验收记录

记录 02.03 的实际运行结果。被测代码是正文带 `Task: 02.03` 的提交；本文件不预填不存在的哈希。

## 环境

| 项目 | 值 |
| --- | --- |
| 机型 | Mac mini，机型标识 Mac16,10 |
| 芯片 | Apple M4，10 核，24 GB 内存 |
| 系统 | macOS 26.6.2，构建号 25G83 |
| 隔离工具链 | go1.27.3 darwin/arm64 |
| 前端源码 | `microsoft/typescript-go`，`typescript/v7.0.2`，提交 `2bd066d8` |

## 正向验收

### 同名参数拿到不同 ID

夹具 `bridge/tsgo/testdata/facts.ts` 里 `add(a, b)` 与 `scale(a)` 各有一个 `a`：

```text
$ consumer -facts bridge/tsgo/testdata/facts.ts
1 const answer 82
2 function add 111       参数 [3, 4]
3 parameter a 126
4 parameter b 136
5 function scale 175     参数 [6]
6 parameter a 264
7 const doubled 309
```

两处引用分别指向各自的声明：

```text
{"name": "a", "start": 166, "length": 2, "target": 3}
{"name": "a", "start": 293, "length": 2, "target": 6}
```

`TestFactsDistinguishSameNamedParameters` 断言两个 ID 不同，且每个 ID 恰好被引用一次。

### 前向调用解析正确

`add(40, 2)` 出现在 `add` 的声明之前（调用在字节 99，声明在 111）。事实里的 `callee` 是 2，指向后面那个声明：

```text
{"start": 99, "length": 11, "callee": 2,
 "arguments": [{"kind":"number"},{"kind":"number"}],
 "result": {"kind":"number"}}
```

`TestFactsResolveForwardCall` 断言被调用者是函数 `add`、声明位置在调用之后、实参个数与形参一致。

### 会话关闭后事实仍可读

宿主先 `session.Close()` 再 `frontend.Decode(raw)`，解码与校验都在句柄清空之后完成，数据完整。`TestFactsRemainReadableAfterSessionClose` 还确认了 `Session` 标识存在、声明与引用非空、`Validate` 通过。

### 类型摘要

`const answer = add(40, 2)` 的类型是 `number`，尽管检查器对字面量给出的是字面量类型。摘要来自类型标志位，不来自 `"42"` 这样的字符串。

## 负向验收

`compiler/frontend` 的 18 条不一致事实用例全部按预期错误码拒绝：

| 场景 | 做法 | 错误码 |
| --- | --- | --- |
| 重复 ID | 把参数 ID 改成函数的 ID | `facts.duplicate-id` |
| 引用指向不存在的声明 | `target = 99` | `facts.unknown-target` |
| 调用指向不存在的声明 | `callee = 99` | `facts.unknown-target` |
| 调用指向常量 | `callee = 4` | `facts.call-target` |
| 实参个数不符 | 只留一个实参 | `facts.arg-count` |
| 实参类型不符 | 第一个实参改成 `string` | `facts.arg-type` |
| 返回类型摘要不符 | 调用结果改成 `void` | `facts.result-type` |
| 函数缺返回类型 | `result = null` | `facts.bad-type` |
| 参数缺类型 | `type = null` | `facts.bad-type` |
| 参数重复归属 | 函数参数列表写 `[2, 2]` | `facts.shared-parameter` |
| 参数没有归属 | 函数参数列表漏掉一个 | `facts.orphan-parameter` |
| 参数 ID 指向函数 | 参数列表写 `[1]` | `facts.bad-parameter` |
| 声明种类不认识 | `kind = "variable"` | `facts.bad-decl-kind` |
| 类型类别带空格 | `kind = "number "` | `facts.bad-type` |
| 类型类别大小写不符 | `kind = "Number"` | `facts.bad-type` |
| 位置为负 | `start = -1` | `facts.bad-position` |
| 缺会话标识 | `session = ""` | `facts.no-session` |
| schema 版本不符 | `schemaVersion = 2` | `facts.schema` |

另外两条：

- **未知字段**：在合法 JSON 里加 `"upstreamSymbol"` 后 `Decode` 报 `facts.decode`。适配层与本包的字段一旦漂移就会被发现，不会静默丢一半事实。
- **会话混用**：两次检查同一份文件，`Combine` 报 `facts.session-mismatch`。`TestFactsFromDifferentSessionsCannotCombine` 用两个真实会话验证，两次的 `session` 标识确实不同。

全部错误都属于内部故障（退出 5），不是源码错误。

### 纯数据检查

`TestFactsHoldOnlyPlainData` 用反射遍历 `Facts` 的类型图：只允许基本类型、本包结构体和它们的切片，指针只能指向本包结构体。加入 `*ast.Node` 这类上游字段会让它直接失败。

## 用例统计

| 范围 | 用例数 |
| --- | --- |
| `compiler/frontend` | 23 个 `=== RUN`（5 个测试函数，含 18 个子用例） |
| 宿主 `bridge/tsgo/consumer` | 18 个 `=== RUN`（14 个测试函数） |
| `./bridge/tsgo/check` | 12 项 |

`./tools/test` 覆盖 `compiler/frontend`，8 个包通过。

## 过程中发现的问题

1. **`fn.Parameters` 是字段，不是方法**。`FunctionDeclaration` 结构体里 `Parameters` 是 `*ParameterList`，`Body` 是 `*NodeBody`；要拿参数节点列表和函数体，得用 `*ast.Node` 上的同名方法。写成 `fn.Parameters()` 编译不过。
2. **声明顺序最初是「参数先于函数」**。因为先追加参数再追加函数，读起来像参数没有归属。改成先算 ID、再按「函数、参数、参数」写入，输出顺序与源码一致。
3. **类型注解里的 `number` 会被当成引用**。第一版遍历整个函数节点，`a: number` 的 `number` 也进了引用表，指向一个不存在的声明。改成只走函数体与初始化表达式，不进类型节点。

## 与计划的偏差

- **`dump facts` 目前是宿主的 `-facts` 模式，不是 `tynel dump facts`**。根模块不能导入 `tyneladapter`（它在另一个模块的源码树里），主 CLI 要到 02.07 才接入前端。现在先把它做成外部宿主的一个开关，命令行参考里仍把 `dump facts` 标为未实现。
- **事实通过 JSON 传递，而不是共享结构体**。适配层在另一个模块里，无法导入 `compiler/frontend`；两边各写一份结构体、靠严格解码对齐，比在根模块加一条指向 `.work` 的 `replace` 更省事，也让「事实可以被写出、被别的进程读」这件事在 02.03 就成立。

## 后续修订

02.09 复查源码位置时修正了事实起点的取法：从上游的完整起点（`node.Pos()`，含前导空白与注释）改为第一个有效记号的位置。本记录里的偏移对应 02.03 的提交；当前值见[声明身份与事实](../reference/facts.md)与[节点 02 累计验证记录](02-source.md)。

## 交付后的参数语义（2026-11-09）

上面各节的命令输出和用例统计属于 02.03 原始验收；本节记录之后的一次修复，执行时间是 2026-11-09 19:49 CST，代码基于提交 `c8b5d49` 的工作树。环境仍是 macOS 26.6.2、arm64 与锁定的 go1.27.3，夹具放在不入库的 `.work/validation/default-param/` 与 `.work/validation/default-param-cross/`。

`function add(a: number, b: number = 10)` 配 `console.log(add(5))` 是合法 TypeScript，修复前却在事实校验阶段就被判成内部不一致：

```text
$ ./bin/tynel check --root .work/validation/default-param main.ts
tynel: 内部故障 (host.failed): 前端宿主执行失败（退出状态 5）
  阶段: frontend
  原因: tynel: 内部故障 (facts.arg-count): 调用 "add" 给了 1 个实参，函数声明有 2 个参数
      阶段: frontend
$ echo $?
5
```

两个原因叠在一起。适配层的 `declareFunction()` 只写参数 ID 与类型，没有记录 `b` 带初始化表达式；`Facts.Validate()` 又强制 `len(call.Arguments) == len(callee.Parameters)`，并且按形参数量去索引 `call.Arguments[i]`。能力检查里的 `cap.arg-count` 是同样一份固定个数假设，两边各写一遍，将来放开一边就会对不上。

修法分四步。`frontend.Declaration` 加 `optional` / `hasDefault`，只属于 `parameter`；适配层从 `ParameterDeclaration.QuestionToken` 与 `.Initializer` 填这两个字段。`consumer -facts` 对 `add(a, b = 10)` 现在给出：

```json
{"id": 3, "kind": "parameter", "name": "b", "type": {"kind":"number"}, "hasDefault": true}
```

参数数量规则抽到 `compiler/frontend/signature.go`：`Arity{Required, Total}` 与 `Facts.ArityOf(callee)`，下界取「最后一个必填参数的下标 + 1」。`Facts.Validate()` 与 `capability.Check()` 都调它，不再各自比较个数；校验也改成按实际给出的实参比较类型，省略的尾部参数不参与，可省略位置上传的 `undefined`（前端压成 `other`）不算类型不一致。这样 `f(a = 1, b)` 的第一个参数虽然带默认值，后面还有必填参数，仍要求两个实参，与 TypeScript 一致。

策略落在能力检查的声明上：带默认值或 `?` 的参数都报 `cap.unsupported-parameter`，诊断指向参数自己的位置；必填参数照常支持。事实层因此可以先承认 `add(5)` 是自洽的，再由能力层明确说「这个写法暂不支持」：

```text
$ ./bin/tynel check --root .work/validation/default-param main.ts
…/default-param/main.ts:1:25: 能力限制 cap.unsupported-parameter: 参数 "b" 带默认值，本阶段只支持必填参数
$ echo $?
3

$ ./bin/tynel check --root .work/validation/default-param optional.ts
…/default-param/optional.ts:1:25: 能力限制 cap.unsupported-parameter: 参数 "b" 是可选参数，本阶段只支持必填参数

$ ./bin/tynel check --root .work/validation/default-param full.ts
ok: …/default-param/full.ts（2 个函数，NIR 已验证）
```

还有一条跨模块支路：前端宿主只对入口跑能力检查，依赖模块里的默认参数过去会一路走到合并降低，报 `lower.bad-arity` 内部故障。`cmd/tynel` 的 `loadMerged` 现在对图里每个模块各跑一次 `capability.Check`，把结论指向依赖文件自己的行列：

```text
$ ./bin/tynel check --root .work/validation/default-param-cross main.ts
tynel: 能力限制 (cap.unsupported-parameter): 参数 "b" 带默认值，本阶段只支持必填参数
  阶段: capability
  位置: …/default-param-cross/lib.ts:1:32
$ echo $?
3
```

回归覆盖四层。`compiler/frontend/signature_test.go` 锁住 `ArityOf` 的五种参数形状、`Allows` 边界、`add(5)` 的 0/1/2/3 个实参、参数语义只能挂在参数上、`undefined` 的位置规则与 JSON 往返。`compiler/capability/capability_test.go` 锁住两种参数的诊断措辞、实参给齐仍按声明拒绝，以及 `TestArityRuleIsSharedWithFactsValidation`——同一份事实上，事实校验与能力检查对 0/1/2/3 个实参必须给出一致结论。`bridge/tsgo/consumer/facts_test.go` 走真实前端断言三个参数分别只有 `hasDefault`、`optional` 或都没有。`tests/source` 加 `default-parameter.ts` 与 `optional-parameter.ts` 两个端到端能力用例；`cmd/tynel/diagnostics_test.go` 的 `TestDefaultParameterIsCapabilityError` 分入口与依赖两种布局，断言退出 3 且不出现 `facts.arg-count` / `lower.bad-arity` / `host.failed`。

把 `HEAD` 里的旧 `validate.go` 与 `capability.go` 临时换回（保留新字段定义与 `Arity` 文件）后，新单测按预期失败：

```text
--- FAIL: TestValidateAcceptsOmittedArguments/1 个实参
    signature_test.go:100: 合法事实被拒绝：内部故障 (facts.arg-count): 调用 "add" 给了 1 个实参，函数声明有 2 个参数
--- FAIL: TestDefaultAndOptionalParametersAreRejected/默认值
    capability_test.go:592: 应报 cap.unsupported-parameter，实际没有发现
--- FAIL: TestArityRuleIsSharedWithFactsValidation/1 个实参
    capability_test.go:626: 事实校验放行=false，期望 true
```

恢复修复后的全量检查：

```text
$ ./tools/test
测试通过：20 个包

$ ./bridge/tsgo/check
全部通过

$ ./.work/toolchain/go1.27.3-sdk-darwin-arm64/bin/go vet ./...
（无输出）

$ ./.work/toolchain/go1.27.3-sdk-darwin-arm64/bin/gofmt -l compiler/frontend compiler/capability cmd/tynel bridge/tsgo/_adapter bridge/tsgo/consumer
（无输出）
```

已有跨模块程序没有回归，`modules / merge / init / alias / linear / diamond` 分别输出 `41 42`、`55`、`1 3 2 4 23`、`42`、`2`、`5`。`docs/reference/facts.md` 补了「参数语义与实参数量」，`docs/reference/capability.md` 补了「参数与实参数量」与 `cap.unsupported-parameter` 错误码。本次未覆盖：默认初始化表达式本身不进语法骨架，后端没有合成缺省值的能力；rest 参数仍由 `cap.bad-type` 拒绝，没有独立语义字段；仍只在 `darwin/arm64` 上验证。适配包改动需要 `./tools/toolchain/prep --frontend-only` 重新同步到 `.work` 里的副本。

## 交付后的数字字面量语义（2026-11-09）

本节记录 `numericBits` 的一次修复，执行时间是 2026-11-09 22:32 CST，代码基于提交 `ac5ee65` 的工作树。环境仍是 macOS 26.6.2、arm64 与锁定的 go1.27.3，夹具放在不入库的 `.work/validation/numeric-literals/`。

问题报告称 `const n: number = 0x10000000000000000` 会在生成事实时失败：适配层用 `strconv.ParseUint(s, base, 64)` 解析非十进制字面量，2^64 超出 uint64。实测发现真实现象被一层上游行为掩盖：扫描器的 `scanBigIntSuffix()` 对每个数字字面量先做 `jsnum.FromString(...).String()`，交给适配层的 `node.Text()` 已经是归一化的十进制文本（`0x10000000000000000` 变成 `18446744073709552000`），所以 `ParseUint` 分支在真实管道里走不到，用户的例子在修复前就能通过。但分支里的错误是真实存在的：任何原始非十进制文本直接进来都会报溢出，十进制支路的 `ParseFloat` 也把 `1e400` 这样的合法上溢误判为错误。用隔离探针逐字复制旧实现确认了两条路径都返回 `strconv.ErrRange`。

修法是把 `numericBits` 整体委托给 `jsnum.FromString`——检查器给字面量类型取值用的正是同一个函数（`checker.go` 的 `getNumberLiteralType(jsnum.FromString(node.Text()))`），事实位模式与检查器字面值因此共用一条规则：任意精度整数经 `big.Int` 按最近偶数舍入到 binary64，上溢得到 Infinity，下溢得到零。数字分隔符仍先剥掉（`jsnum.FromString` 不认识 `_`）；返回 NaN 的文本不是合法字面量，报错。修复后用户的例子直接编译运行：

```text
$ ./bin/tynel run .work/validation/numeric-literals/main.ts
1.8446744073709552e+19
$ ./bin/tynel dump facts .work/validation/numeric-literals/main.ts
# 声明 n 的初始化表达式位模式：0x43f0000000000000（2^64）
```

回归覆盖四层。`bridge/tsgo/_adapter/facts_test.go` 新增 22 个正向用例直接拿原始文本测 `numericBits`：超出 uint64 的十六进制 / 二进制 / 八进制（都是 2^64）、uint64 上界舍入、2^53+1 与 2^53+3 的最近偶数舍入（2^53 处间距为 2，2^53+4 的位模式是 `0x4340000000000002`）、53 位以内精确表示、三种进制的上溢、十进制上下溢、分隔符与扫描器归一化文本；另有 4 个非法文本负向用例。真实前端里扫描器会先归一化文本，只有这一层能覆盖「原始写法进来」的路径。`bridge/tsgo/consumer/facts_test.go` 的 `TestFactsNumericLiteralBits` 走真实前端断言 8 个声明的位模式。`tests/source` 新增 `numeric-literals.ts` 端到端程序：大数的十进制拼写两个运行时不同（Tynel 用 Go 的 `'g'` 格式），所以只输出比较结果与归一后的商，与 Node 逐字节一致（`shared` 对照通过）。`bridge/tsgo/check` 新增第 2b 步，在源码树里跑 `go test ./tyneladapter`，适配包单测进入受控驱动。

把源码树副本里的 `numericBits` 临时换回旧实现后，新单测按预期失败：

```text
--- FAIL: TestNumericBits/超出_uint64_的十六进制
    numericBits("0x10000000000000000") 报错：strconv.ParseUint: parsing "10000000000000000": value out of range
--- FAIL: TestNumericBits/超出_uint64_的二进制
--- FAIL: TestNumericBits/超出_uint64_的八进制
--- FAIL: TestNumericBits/十六进制上溢
--- FAIL: TestNumericBits/二进制上溢
--- FAIL: TestNumericBits/十进制上溢
    numericBits("1e400") 报错：strconv.ParseFloat: parsing "1e400": value out of range
```

恢复修复后的全量检查：

```text
$ ./tools/test
测试通过：20 个包

$ ./bridge/tsgo/check
全部通过

$ go vet ./tyneladapter   # 在源码树内执行；消费者模块与根模块 ./tests/... 同样无输出
（无输出）

$ gofmt -l bridge/tsgo/_adapter bridge/tsgo/consumer tests/source
（无输出）
```

本次未覆盖：运行时的数字拼写仍是 Go 的 `'g'` 格式与 `+Inf` / `-Inf`（接完整数值语义时一并改）；仍只在 `darwin/arm64` 上验证。适配包改动需要 `./tools/toolchain/prep --frontend-only` 重新同步到 `.work` 里的副本。

## 交付后的匿名默认导出函数（2026-11-10）

本节记录匿名 `export default function` 导致前端事实提取崩溃的一次修复，执行时间是 2026-11-10 00:07 CST，代码基于提交 `2fd4252` 的工作树。

与前几节不同，这次在 Windows 上验证：系统 Windows 10（10.0.26200）x64，编译器用缓存的 go1.27.3 windows/amd64（`GOTOOLCHAIN=local` 直接调用工具链二进制，避开本机默认 Go 的版本委派）。前端仍是锁定的 `microsoft/typescript-go` 归档（SHA256 `5ccb47dbb3f68cd0da58b71e6f445eee36a50bd3e9f9f330cc23a97b88500119`，提交 `2bd066d8`），手动解压到 `.work/frontend/typescript-go` 并核对 `go.mod`、`LICENSE`、`program.go`、`lib.es5.d.ts` 四个标记与锁文件一致，再把修复后的适配包复制进 `tyneladapter/`。官方验收平台仍是 `darwin/arm64`；凡依赖隔离 SDK 报告或 zsh 驱动的全量流程在本机跑不了，下面逐项说明。

`export default function (): number { return 42; }` 是合法的 TypeScript 模块语法。修复前 `collect()` 把所有 `FunctionDeclaration` 都交给 `declareFunction()`，后者取 `fn.Name()` 登记声明；匿名默认导出没有名字，`Name()` 返回 nil，`GetSymbolAtLocation(nil)` 在检查器里解引用崩溃，退出 2，根本走不到能力检查：

```text
$ consumer-baseline.exe -facts tests/source/testdata/errors/default-export-anon.ts
panic: runtime error: invalid memory address or nil pointer dereference
[signal 0xc0000005 code=0x0 addr=0x0]
github.com/microsoft/typescript-go/internal/checker.(*Checker).GetSymbolAtLocation(...)
github.com/microsoft/typescript-go/tyneladapter.(*factBuilder).declareFunction(...)
    .work/frontend/typescript-go/tyneladapter/facts.go:373
github.com/microsoft/typescript-go/tyneladapter.(*factBuilder).collect(...)
    .work/frontend/typescript-go/tyneladapter/facts.go:232
$ echo $?
2
```

`-capability` 同样在取事实这一步崩溃，退出 2 而不是 3。

修法是在事实提取阶段就把匿名与默认导出的函数声明挡在声明表之外。`collect()` 的分支改成只登记「顶层、具名、非默认导出」的函数：

```go
case ast.IsFunctionDeclaration(stmt) && stmt.Name() != nil && !ast.HasSyntacticModifier(stmt, ast.ModifierFlagsDefault):
    queue = append(queue, b.declareFunction(stmt, ast.HasSyntacticModifier(stmt, ast.ModifierFlagsExport)))
```

匿名函数与默认导出落到原有的模块体骨架路径，成为模块 `Block` 下的一个 `FunctionDeclaration` 骨架节点；能力检查新增对应分支，按 `cap.unsupported-syntax` 拒绝。默认导出本就不在 SPEC 的命名导出子集内，这里不建立独立导出身份，只明确拒绝。这条守卫还顺带修掉一个潜在错认：`export default function answer()` 过去会被当成具名导出 `answer` 放行，现在同样按骨架拒绝。修复后：

```text
# 先生成宿主二进制，再运行它检查夹具。
$ go build -o .work/frontend/bin/consumer.exe ./bridge/tsgo/consumer
$ .work/frontend/bin/consumer.exe -capability tests/source/testdata/errors/default-export-anon.ts
[
  {
    "line": 3, "column": 1,
    "category": "能力限制",
    "code": "cap.unsupported-syntax",
    "message": "函数声明不在本阶段子集内；只支持顶层具名函数声明"
  }
]
$ echo $?
3
```

`default-export-named.ts`（带名字的默认导出）给出同样的退出 3 与错误码。`-facts` 不再崩溃，输出合法 JSON：匿名函数是模块 `Block` 的 `FunctionDeclaration` 子节点，`declarations` 为 null——它没有被登记成具名声明：

```json
{
  "module": {"kind": "Block", "children": [{"kind": "FunctionDeclaration", "start": 199, "length": 51}]},
  "declarations": null
}
```

回归覆盖四层。`compiler/capability/capability_test.go` 的 `TestFunctionDeclarationSkeletonRejected` 直接构造一个 `FunctionDeclaration` 骨架节点，断言按 `cap.unsupported-syntax` 拒绝。`bridge/tsgo/consumer/capability_test.go` 的 `TestCapabilityFixtures` 加入 `default-export-anon.ts` 与 `default-export-named.ts` 两个夹具，走真实前端「检查 → 事实 → 能力」全程，两者都通过。`bridge/tsgo/check` 的能力循环加入这两项（zsh 驱动，本机未执行）。`tests/source` 加入同名两个端到端能力用例（`kind=capability`、`code=cap.unsupported-syntax`）；`TestManifest` 在本机通过，确认清单形状与夹具存在，完整的 `TestErrors` 需要 darwin 宿主。

负向对照在 scratch 树副本里把 `HEAD` 的原始适配层换回（不动仓库源码），重新构建宿主：`-facts` 与 `-capability` 对匿名夹具都 panic、退出 2，`TestCapabilityFixtures/default-export-anon.ts` 以 nil 指针 panic 失败——说明新用例确实守住了缺陷。同一份原始适配层下，下面四个与本次改动无关的消费者用例失败方式与修复后完全一致，确认它们是 Windows 平台的既有问题，不是本次引入。

本机能跑的全量检查如下。消费者套件 26 个顶层用例通过、4 个失败；适配包单测 `go test ./tyneladapter` 通过；纯编译器测试里 `capability`、`frontend`、`diagnostic`、`lower`、`merge`、`modules`、`nir`、`pipeline`、`source` 通过；`go vet` 对 `capability`、消费者、适配包均无输出；`gofmt -l` 对四个改动的 Go 文件无输出。

```text
# 消费者套件：真实前端，统计顶层通过/失败。
$ go test -count=1 -v ./bridge/tsgo/consumer   # 在消费者模块内执行
--- PASS: TestCapabilityFixtures (0.37s)        # 含 default-export-anon / default-export-named
26 个 --- PASS，4 个 --- FAIL

# 适配包单测：在源码树内执行。
$ go test -count=1 ./tyneladapter
ok  	github.com/microsoft/typescript-go/tyneladapter	0.643s

# 纯编译器测试与静态检查。
$ GOTOOLCHAIN=go1.27.3 go test -count=1 ./compiler/capability ./compiler/frontend
ok  	github.com/Mahoo12138/tynel/compiler/capability	0.030s
ok  	github.com/Mahoo12138/tynel/compiler/frontend	0.531s
$ GOTOOLCHAIN=go1.27.3 go vet ./compiler/capability
（无输出）
$ gofmt -l bridge/tsgo/_adapter/facts.go compiler/capability/capability.go compiler/capability/capability_test.go bridge/tsgo/consumer/capability_test.go
（无输出）
```

四个失败的消费者用例都是 Windows 平台差异，且与匿名函数无关：`TestTypeErrorCarriesCodeAndPosition` 与 `TestDependencyDiagnosticsAreReported` 断言用 `filepath.Join` 的反斜杠路径，前端诊断给的是正斜杠（`C:\…` 对 `C:/…`）；`TestPositionAfterChinesePrefixEndToEnd` 与 `TestTruncatedSourceIsSyntaxError` 的行列在本机算成 `0:0`。纯编译器测试里 `graph` 与 `project` 也有既有 Windows 失败：创建符号链接需要「客户端没有持有的特权」，`TestResolveRejects/绝对路径越界` 用的 `/tmp/outside.ts` 在 Windows 不存在，报 `project.missing` 而非 `project.outside-root`。这两个包本次未改动。

本次未覆盖：完整的 `tests/source`（`TestErrors` / `TestPrograms`）、`./tools/test` 与 `./bridge/tsgo/check` 依赖隔离 SDK 报告或 zsh 驱动，只能在官方 `darwin/arm64` 上跑，本机仅验证到 `TestManifest`、真实前端消费者套件、适配包单测与纯编译器测试；默认导出仍不在子集内，没有建立独立导出身份；嵌套函数声明也由同一骨架分支拒绝，未单独区分子集措辞；本次只在 `windows/amd64` 上验证，`darwin/arm64` 的复核待补。适配包改动需要 `./tools/toolchain/prep --frontend-only` 重新同步到 `.work` 里的副本。
