# S2.6 验收：泛型函数第一个切片

> 验证日期：2026-11-15（本地实际运行时间，不是 Git 提交时间）
> 目标：darwin/arm64
> 关联：[泛型 RFC](../language/semantics/generics.md)、[NIR 格式](../reference/nir.md)、[降低器](../reference/lowering.md)

## 1. 范围

本轮实现覆盖：

- 泛型函数声明与调用点类型实参推断；
- 按具体类型单态化，NIR 不保留类型参数；
- 类型参数出现在顶层参数、数组元素、元组位置、对象属性和函数类型里；
- 同一组类型实参重复调用复用同一实例；
- 递归泛型调用；
- 泛型函数与字符串、数组、对象和函数值组合。

不在本轮：泛型接口、泛型类型别名、显式类型实参、默认类型参数、约束驱动的运算、泛型函数作为值、联合中的类型参数推断。

## 2. 类型推断与替换

```zsh
# 类型参数收集、结构推断、替换和负向形状。
go test ./compiler/frontend -run 'TypeParameters|Infer|Substitute' -v
```

结果：通过。覆盖：

- 泛型函数签名里的类型参数按首次出现顺序去重；
- 数组元素、对象属性和函数类型的直接推断；
- 联合推断和参数个数不符按失败处理；
- 替换后的对象、元组和数组不再含类型参数；
- 缺少类型实参时替换返回错误。

## 3. 源码到原生

```zsh
# 源码回归；generic 用例同时与 Node 22 的 shared 输出逐字节对照。
go test ./tests/source -run 'TestPrograms/generic' -v
```

`generic.ts` 实际输出：

```text
42
hi
7
4
5
5
2
9
5
```

覆盖：

- `identity<number>` 与 `identity<string>`；
- 数组、对象属性和函数类型参数推断；
- 泛型函数返回数组、对象和函数值；
- 递归泛型调用 `down<T>`；
- 嵌套泛型调用 `identity(identity(5))`。

单态化结果（`tynel dump nir`）包含：

```text
identity$t1  value: number -> number
identity$t4  value: string -> string
first$t1     items: number[] -> number
pair$t1      a: number, b: number -> number
wrap$t1      value: number -> number[]
apply$t1     f: (number) => number, value: number -> number
get$t1       box: { value: number } -> number
down$t1      value: number, n: number -> number
```

`identity$t1` 出现两次调用但只生成一个函数，证明实例缓存按结构化类型身份复用。

负向用例：

| 用例 | 结果 |
| --- | --- |
| `errors/generic-value.ts` | `cap.unsupported-syntax`，泛型函数未实例化不能作为值 |
| `errors/generic-alias.ts` | `cap.unsupported-syntax`，泛型类型别名不在本切片 |
| `errors/generic-uninferable.ts` | `cap.bad-type`，无法从实参推断类型参数 |
| `errors/generic-constraint-op.ts` | `cap.bad-type`，约束不会把 `T` 变成可做算术的 `number` |

## 4. NIR 与回放

泛型不进入 NIR：降低器跳过泛型声明，在调用点生成具体函数。`dump nir` 的类型表只包含实例化后的实际类型（`number[]`、函数类型和对象类型），函数签名里没有 `type-parameter`。生成的 NIR 可以正常编码、解码和原生构建。

## 5. 后端与前端检查

```zsh
./bridge/go/build
./bridge/go/check
./bridge/tsgo/check
go test ./compiler/... ./cmd/... ./internal/... ./tests/...
```

结果：bridge 构建成功；后端检查全部通过；前端检查全部通过；全量 Go 测试通过。

## 6. 实现边界

- 类型实参只从实参类型直接推断；显式 `<number>`、默认类型参数和返回位置推断留给后续。
- 实例名使用目标函数名加类型 ID，如 `identity$t1`；NIR 自包含，回放不依赖源码。
- 约束由 TypeScript 检查，本轮不写入事实，也不放宽运算规则。
- 泛型接口、泛型类型别名和未实例化的泛型函数值按能力限制拒绝。
- 单态化会增加函数数量；代码体积和编译时间在后续标准库切片复评。
