# S2.5 验收：函数值第一个切片

> 验证日期：2026-11-15（本地实际运行时间，不是 Git 提交时间）
> 目标：darwin/arm64
> 关联：[函数与闭包 RFC](../language/semantics/functions.md)、[NIR 格式](../reference/nir.md)、[bridge 说明](../reference/bridge.md)

## 1. 范围

本轮实现覆盖：

- 顶层函数作为值：赋值给局部值、作为参数、作为返回值、放进三元式结果；
- 无捕获的表达式体箭头函数，降低时提升成合成具名函数；
- 函数类型作为参数、返回值、局部值和类型别名；
- 通过函数值发起的间接调用，实参个数和类型按函数类型核对；
- `void` 函数值作为值传递和间接调用；
- NIR 的 `func` / `invoke` 与函数类型表。

不在本轮：闭包捕获、块体箭头、`void` 箭头函数、函数表达式、函数值输出、函数值身份比较、跨模块函数值 ABI。

## 2. NIR 与验证器

```zsh
# 函数类型、func、invoke、签名和实参检查。
go test ./compiler/nir -run 'FunctionValue' -v
```

结果：通过。覆盖：

- `func` 的目标函数必须存在，签名与函数类型一致；
- `invoke` 的被调用者必须是函数类型，实参个数与类型匹配，结果类型一致；
- `func` / `invoke` 的编码、解码与回放；
- 坏签名、实参个数、实参类型、结果类型和非函数被调用者分别按 `nir.*` 拒绝。

## 3. 原生输出

```zsh
# 手写 NIR：具名函数值、函数类型参数和间接调用。
go test ./tests/native -run 'TestFixtureOutputs/函数值' -v
```

实际输出：

```text
5
42
6
```

`incValue(4)` 通过函数值调用 `inc` 得到 5；`addValue(20, 22)` 得到 42；`apply(5, incValue)` 把函数值作为参数传入，再间接调用得到 6。

## 4. 源码到原生

```zsh
# 源码回归；function-value 用例同时与 Node 22 的 shared 输出逐字节对照。
go test ./tests/source -run 'TestPrograms/function-value' -v
```

`function-value.ts` 实际输出：

```text
5
42
6
42
99
3
```

覆盖：

- 顶层函数值赋值与间接调用；
- 无捕获箭头作为局部值、参数和返回值；
- 函数类型别名 `type Unary = (x: number) => number`；
- 函数类型三元式结果；
- `void` 函数值作为值传递并间接调用；
- 模块级箭头在模块初始化阶段提升和调用。

负向用例：

| 用例 | 结果 |
| --- | --- |
| `errors/function-capture.ts` | `cap.closure`，箭头捕获外层局部值 |
| `errors/function-block-arrow.ts` | `cap.unsupported-syntax`，块体箭头尚未实现 |
| `errors/function-print.ts` | `cap.bad-console-call`，函数值没有输出格式 |
| `errors/function-equality.ts` | `cap.bad-type`，函数值身份比较尚未实现 |

## 5. 后端与前端检查

```zsh
./bridge/go/build
./bridge/go/check
./bridge/tsgo/check
go test ./compiler/... ./cmd/... ./internal/... ./tests/...
```

结果：bridge 构建成功；后端检查全部通过；前端检查全部通过；全量 Go 测试通过。

## 6. 实现边界

- 第一个切片没有闭包环境；bridge 直接用 Go func 值表示无捕获函数值，这只是后端表示，不进入 NIR 或公共 API。
- NIR 没有匿名函数节点：箭头函数在降低阶段提升成 `_arrow_N` 合成具名函数，再用 `func` 引用。
- 箭头函数不继承外层作用域；捕获外层局部值或外层箭头参数按 `cap.closure` 拒绝。
- 函数值可以出现在参数、返回值、局部值和三元式结果中；不能输出，也没有 `===` / `!==`。
- 闭包捕获、块体箭头、`void` 箭头、函数表达式和跨模块函数值留到后续切片。
