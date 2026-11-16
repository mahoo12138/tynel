# S3.1 验收：空值合并 `??`

> 验证日期：2026-11-15（本地实际运行时间，不是 Git 提交时间）
> 目标：darwin/arm64
> 关联：[Stage 3 计划](../plans/stage-3.md)、[联合与空值 RFC](../language/semantics/unions-null.md)、[降低器](../reference/lowering.md)

## 1. 范围

本轮实现覆盖：

- `value ?? fallback`，左侧是 `T | null` / `T | undefined` 或非空具体类型；
- 左侧只求值一次，物化成临时值；
- 左侧非空时右侧完全不执行；
- 前端缩窄成 `null` / `undefined` / 成员类型的标识符，仍按存储的联合标签检查；
- `return`、`const` / `let`、实参、算术和嵌套 `??` 的组合。

不在本轮：`?.`、多成员联合左侧、裸 `null` / `undefined` 左侧、`??=` 和可选属性。NIR 不新增操作，schema 版本不变。

## 2. 能力检查

```zsh
# 右侧类型放不进可执行成员。
go run ./cmd/tynel check tests/source/testdata/errors/nullish-right.ts --diagnostics=json

# 多成员联合存储的左侧。
go run ./cmd/tynel check tests/source/testdata/errors/nullish-multi.ts --diagnostics=json
```

实际结果：

| 用例 | 退出状态 | 诊断 |
| --- | --- | --- |
| `nullish-right.ts` | 3 | `cap.bad-type`：`?? 的右操作数是 number，需要 string` |
| `nullish-multi.ts` | 3 | `cap.bad-type`：`?? 的左侧需要 T \| null 或 T \| undefined，实际为 null \| string \| number`；参数类型同时被联合检查拒绝 |

两个用例都是能力限制，不是内部故障。`const b: number | null = null; b ?? 7` 这类引用处缩窄成 `null`、存储仍是联合的写法被允许，能力检查按声明类型判断。

## 3. NIR 形状

```zsh
# 导出通过验证的 NIR，检查 ?? 的降低形状。
go run ./cmd/tynel dump nir tests/source/testdata/nullish.ts
```

`pick` 的 NIR 节选：

```json
{"op":"let","name":"_tmp_1","value":{"op":"param","name":"value"}},
{"op":"select","name":"_tmp_3",
 "cond":{"op":"not","operand":{"op":"is","operand":{"op":"local","name":"_tmp_1"},"type":5}},
 "then":[{"op":"unwrap","name":"_tmp_2","value":{"op":"local","name":"_tmp_1"},"type":1}],
 "thenValue":{"op":"local","name":"_tmp_2"},
 "elseValue":{"op":"call","function":"mark","args":[...]},
 "type":1}
```

左侧参数只读一次；`is` 检查空值标签；`unwrap` 在非空分支取出成员；右侧调用只挂在 else 分支，不会提前执行。

## 4. 原生输出

```zsh
# 构建并运行正向用例。
go run ./cmd/tynel run tests/source/testdata/nullish.ts

# 源码回归；nullish 用例同时与 Node 22 的 shared 输出逐字节对照。
go test ./tests/source -run 'TestPrograms/nullish|TestErrors/nullish|TestNodeReference/nullish' -v
```

实际输出：

```text
5
7
hi
none
3
9
9
5
11
11
5
4
6
5
```

覆盖：

- `pick(5, 7)` 返回 5，`pick(null, 7)` 返回 7；
- `label("hi")` 与 `label(undefined)` 分别返回 `hi`、`none`；
- `first(3)` 不执行 `mark(9)`；`first(null)` 先打印 9，再返回 9；
- `const d = a ?? mark(10)` 不打印 10；
- `b ?? mark(11) ?? 0` 打印 11 并得到 11；
- `a ?? (b ?? mark(12))` 不执行内层右侧；
- `let mutable: number | undefined` 在 `undefined` 与赋值后分别得到 4 和 6；
- `if (a !== null)` 内的 `a ?? 0` 仍生成存储标签检查。

`TestNodeReference/nullish` 通过，Node 22.22.2 的输出与黄金输出逐字节一致。

## 5. 全量检查

```zsh
gofmt -l compiler/capability compiler/lower
git diff --check
go test ./compiler/capability ./compiler/lower ./compiler/nir
go test -p 1 -count=1 ./compiler/... ./cmd/... ./internal/... ./tests/...
./bridge/go/build
./bridge/go/check
./bridge/tsgo/check
```

结果：`gofmt` 无输出；`git diff --check` 无输出；目标包通过；全量 Go 测试串行通过；bridge 构建成功；`bridge/go/check` 16 项全部通过；`bridge/tsgo/check` 全部通过。串行执行是为了避免多个包同时触发 Go 工具链构建时的偶发工具失败，不是代码问题。

## 6. 实现边界

- `??` 不新增 NIR 操作，复用 `is`、`not`、`select` 和 `unwrap`。
- 能力检查按标识符的存储类型判断，不只依赖引用处的缩窄类型；这与降低器的 `valueReadType` / `storageValue` 保持一致。
- 左侧是多成员联合存储时，能力检查先拒绝，避免降低阶段报内部故障。
- `null` / `undefined` 字面量直接作为左侧时，TypeScript 先按 `TS2871` 拒绝；能力检查保留防御性拒绝，不额外生成源码用例。
- `?.`、可选属性、`??=` 和多成员联合仍拒绝，留到后续切片。
