# S2.4 验收：可空联合与缩窄第一个切片

> 验证日期：2026-11-15（本地实际运行时间，不是 Git 提交时间）
> 目标：darwin/arm64
> 关联：[联合与空值 RFC](../language/semantics/unions-null.md)、[NIR 格式](../reference/nir.md)、[bridge 说明](../reference/bridge.md)

## 1. 范围

本轮实现覆盖：

- `T | null` 与 `T | undefined` 两种两成员可空联合；
- `null` / `undefined` 值、联合构造和标签读取；
- 联合作为局部值、参数和返回值；
- `===` / `!==` 与 `null` / `undefined` 的比较；
- 基于前端缩窄结果的读取：`if (x !== null)` 内直接把联合值拆成成员；
- 联合标签与 payload 的运行时检查：错误标签按数据错误退出 6。

不在本轮：多成员联合、判别联合、`??`、`?.`、可选属性、`in` / `typeof` 缩窄、联合身份比较和联合输出。未选中的缩窄分支仍按既有规则做源码与能力检查。

## 2. NIR 与验证器

```zsh
# union 类型、wrap/unwrap、is、null/undefined 与错误码。
go test ./compiler/nir
```

结果：通过。覆盖：

- `wrap` 的值必须是联合成员；
- `unwrap` 的目标必须是联合成员；
- `is` 的目标必须是联合成员；
- `null` / `undefined` 表达式和联合类型往返；
- 坏成员按 `nir.union-member` 拒绝。

## 3. 原生输出

```zsh
# 手写 NIR：number | null 参数、标签判断、拆箱和 null 构造。
go test ./tests/native -run 'TestFixtureOutputs/可空联合' -v
```

实际输出：

```text
5
0
true
```

`show(5)` 在非空分支拆箱返回 5；`show(null)` 走返回 0 的分支；`is null` 对 null 值返回 true。

## 4. 源码到原生

```zsh
# 源码回归；nullable 用例同时与 Node 22 的 shared 输出逐字节对照。
go test ./tests/source -run 'TestPrograms/nullable' -v
```

`nullable.ts` 实际输出：

```text
5
0
6
1
5
2
3
-1
```

覆盖：

- `number | null` 与 `string | undefined` 的参数、返回值和局部值；
- 成员值与 `null` / `undefined` 的赋值；
- `===` / `!==` 空值比较；
- `if (x !== null)` / `if (x !== undefined)` 后的成员读取；
- 已被前端缩窄成具体类型的比较仍能生成原生代码。

负向用例：

| 用例 | 结果 |
| --- | --- |
| `errors/union-print.ts` | `cap.bad-console-call`，联合值没有输出格式 |
| `errors/union-general.ts` | `cap.bad-type`，非空值联合不在本阶段子集内 |
| `errors/null-literal.ts` | `cap.bad-type`，裸 `null` 没有可执行存储类型 |

## 5. 后端与前端检查

```zsh
./bridge/go/build
./bridge/go/check
./bridge/tsgo/check
go test ./compiler/... ./cmd/... ./internal/... ./tests/...
```

结果：bridge 构建成功；16 项后端检查全部通过；前端检查全部通过；全量 Go 测试通过。

## 6. 实现边界

- 联合在 bridge 里是 `*struct{tag int32; v0 ...; v1 ...}`；空值成员只占标签。
- `wrap` 分配联合结构并写入标签和成员 payload；`unwrap` 先检查标签，再读对应字段；`is` 直接比较标签。
- 标签不一致按运行时数据错误退出 6，不复用 Go panic。
- 前端负责 TypeScript 控制流缩窄；降低器只信任每个引用位置的缩窄类型，并把它转成显式 `unwrap`。
- `T | null | undefined`、`number | string` 和多成员判别联合仍拒绝；`??` / `?.` 留到下一个切片。
