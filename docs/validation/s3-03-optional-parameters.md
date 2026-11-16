# S3.3 验收：尾部可选参数

> 验证日期：2026-11-16（本地实际运行时间，不是 Git 提交时间）
> 目标：darwin/arm64
> 关联：[Stage 3 计划](../plans/stage-3.md)、[能力检查](../reference/capability.md)、[降低器](../reference/lowering.md)

## 1. 范围

本轮实现覆盖：

- `function f(a: number, b?: number)`，可选参数位于必填参数之后；
- 参数类型是 `T | undefined`；
- 调用点省略尾部可选参数；
- 显式传 `undefined`；
- 多个连续尾部可选参数；
- 函数体内用 `??`、`=== undefined` 和 `!== undefined` 处理可选值。

不在本轮：默认参数、泛型函数的可选参数、函数类型与函数值里的可选参数、剩余参数、`arguments`。NIR 不新增操作。

## 2. NIR 形状

```zsh
# 导出通过验证的 NIR，检查省略参数时补出的 undefined。
go run ./cmd/tynel dump nir tests/source/testdata/optional.ts
```

`add(3)` 的调用节选：

```json
{"op":"wrap","name":"_tmp_18","value":{"op":"undefined"},"type":8},
{"op":"print","value":{"op":"call","function":"add","args":[
  {"op":"number","bits":"0x4008000000000000"},
  {"op":"local","name":"_tmp_18"}
]}}
```

`add` 的第二个参数声明类型是 `number | undefined`（类型 ID 8）。省略的实参补一个 `wrap undefined` 临时值；显式 `add(3, undefined)` 走同一条装箱路径，生成相同的联合值。

## 3. 原生输出

```zsh
# 构建并运行正向用例。
go run ./cmd/tynel run tests/source/testdata/optional.ts

# 源码回归；optional 用例同时与 Node 22 的 shared 输出逐字节对照。
go test ./tests/source -run 'TestPrograms/optional$|TestErrors/(optional-parameter|default-parameter)$|TestNodeReference/optional$' -v
```

实际输出：

```text
3
7
3
hi!
hi?
1
3
6
x
empty
2
2
0
```

覆盖：

- `add(3)` 省略第二个参数，返回 3；
- `add(3, 4)` 显式给值，返回 7；
- `add(3, undefined)` 与省略等价，返回 3；
- `pick("hi")` 用 `suffix ?? "!"` 得到 `hi!`，`pick("hi", "?")` 得到 `hi?`；
- `twice(1)`、`twice(1, 2)`、`twice(1, 2, 3)` 分别得到 1、3、6；
- `show("x")` 与 `show(undefined)` 走已有的 `string | undefined` 参数路径；
- `steps(1)` 在 `while` 条件里调用省略实参的 `add(0)`，得到 2；
- `both(true)` 在 `&&` 右侧和 `if` 条件里调用省略实参的函数，得到 2；`both(false)` 短路得到 0。补出的 `wrap undefined` 能出现在循环条件和短路准备块里。

`TestNodeReference/optional` 通过，Node 22.22.2 的输出与黄金输出逐字节一致。

## 4. 负向用例

```zsh
# 默认参数仍应在声明处退出 3。
go run ./cmd/tynel check tests/source/testdata/errors/default-parameter.ts --diagnostics=json

# 泛型函数的可选参数仍应退出 3。
go run ./cmd/tynel check tests/source/testdata/errors/optional-parameter.ts --diagnostics=json
```

实际结果：

| 用例 | 退出状态 | 诊断 |
| --- | --- | --- |
| `default-parameter.ts` | 3 | `cap.unsupported-parameter`：参数带默认值 |
| `optional-parameter.ts` | 3 | `cap.unsupported-parameter`：泛型函数的可选参数 |

## 5. 全量检查

```zsh
gofmt -l compiler/capability compiler/lower
git diff --check
go test ./compiler/capability ./compiler/lower ./compiler/nir ./compiler/frontend
go test -p 1 -count=1 ./compiler/... ./cmd/... ./internal/... ./tests/...
./bridge/go/build
./bridge/go/check
./bridge/tsgo/check
```

结果：`gofmt` 无输出；`git diff --check` 无输出；目标包通过；全量 Go 测试串行通过；bridge 构建成功；`bridge/go/check` 16 项全部通过；`bridge/tsgo/check` 全部通过。

## 6. 实现边界

- 可选参数的类型必须是 `T | undefined`；缺少 `undefined` 成员按 `cap.bad-type` 拒绝。
- 省略参数只发生在尾部；`frontend.Arity` 的必填下界保持不变，`f(a = 1, b)` 这种写法仍要求两个实参。
- 默认参数仍拒绝：事实只记录 `hasDefault`，没有保留初始化表达式，后端无法在调用点求值。
- 泛型函数的可选参数仍拒绝：调用点推断和单态化还没有处理省略实参。
- 函数类型、函数值和间接调用里的可选参数仍拒绝；间接调用要求实参个数完全匹配。
- `arguments` 对象和剩余参数不在本阶段。
