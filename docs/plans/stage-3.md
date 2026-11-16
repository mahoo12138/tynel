# Stage 3 实施计划：核心 TypeScript 语法覆盖

> 状态：S3.1 空值合并 `??` 已实现并通过验收；其他工作包仅为候选顺序，尚未开始
> 前置：Stage 2 已提交（`0e4f412`、`bfeddc9`、`3fc9302`、`6dfcfb7`、`728d053`）
> 路线图：[ROADMAP.md](../../ROADMAP.md) Stage 3
> 关联：[兼容性矩阵](../language/compatibility.md)、[操作支持表](../reference/support.md)、[联合与空值 RFC](../language/semantics/unions-null.md)

## 1. 阶段目标

Stage 3 扩展语法入口，让 Tynel 能写更接近日常 TypeScript 的程序，同时坚持一条规则：能力只有在语法、Tynel 类型规则、NIR 降低和原生运行四层都通过验收后，才从 `rejected` 变成 `supported`。

本阶段不追求完整 TypeScript。每个工作包选择一个可运行切片，先覆盖最常用的形态，再按证据扩展。

## 2. 当前基础与缺口

已具备：

- 可空联合 `T | null` / `T | undefined`、`wrap` / `unwrap` / `is`、空值比较和前端控制流缩窄；
- 标量、字符串、静态对象、数组/元组和函数值的类型表与原生表示；
- `select` 分支、短路和求值顺序的降低基础设施；
- NIR schema 3、Runtime ABI v1 和源码到原生的回归集合。

主要缺口：

- `??` 已在 S3.1 落地；`?.`、可选属性和多成员联合尚未设计；
- 只有 `while`，没有 `for` / `for...of`；
- 参数必须全部必填，函数表达式和闭包捕获仍拒绝；
- 字面量类型、`typeof` / `in` 缩窄没有 Tynel 规则。

## 3. 工作包

| ID | 工作包 | 依赖 | 交付物 | 主要验收 |
| --- | --- | --- | --- | --- |
| S3.1 | 空值合并 `??` | Stage 2；S2.4 | 可空联合与具体值上的 `??`；单次求值、右侧惰性、NIR 复用 `select` / `is` / `unwrap` | 正向/负向源码用例、原生输出、Node 对照、验收记录 |
| S3.2 | `for` 与 `for...of` | S3.1 | 循环语法与循环变量作用域；`break` / `continue` 复用现有跳转 | 循环边界、嵌套、提前返回和未实现写法的拒绝 |
| S3.3 | 可选参数与默认参数 | S3.1 | 缺省值合成、调用点参数个数核对、可选参数类型 | 省略参数、显式 `undefined`、默认值求值顺序 |
| S3.4 | 函数表达式与闭包 | S3.1 | 函数表达式提升、词法捕获、捕获生命周期 | 高阶函数、捕获写回和逃逸对象 |
| S3.5 | 可选链与类型语法收束 | S3.1–S3.4 | `?.`、可选属性、字面量类型或 `typeof` 缩窄中的可落地部分 | 逐项决定 `supported` 或继续 `rejected` |

工作包顺序是建议，不是承诺。只有当前工作包的计划和验收写清楚后才开始下一个。

## 4. S3.1 第一个切片：空值合并 `??`

> 状态：已实现；实际命令、输出和边界见 [S3.1 验收记录](../validation/s3-01-nullish-coalescing.md)。

### 4.1 范围

支持：

- `value ?? fallback`，其中 `value` 是 `T | null`、`T | undefined`，或已经是非空具体类型；
- `fallback` 可赋给 `T`；
- 结果类型是 `T`；
- 左侧只求值一次；左侧非空时不求值右侧；
- 左侧是可空联合且前端已缩窄成具体类型时，仍按存储的联合值做运行时标签检查；
- 右侧可以是带副作用的调用，未选中时不得执行；
- 与 `return`、`const` / `let`、实参、算术和三元式组合。

不支持：

- `?.` 和可选链；
- 多成员联合左侧（如 `number | string | null`）；
- 裸 `null` / `undefined` 字面量作为左侧；
- 右侧类型不能赋给左侧可执行成员；
- 联合值直接输出。

### 4.2 能力检查

文件：`compiler/capability/capability.go`

- 在 `node` 的 `BinaryExpression` 分支中，`len(n.Children) == 2` 之后识别 `n.Op == "??"`，调用 `c.nullishCoalesce(n)`；
- `nullishCoalesce` 复用 `nullableUnion` / `assignableTo` / `nullishKind`：
  - 标识符优先按声明的存储类型判断，引用处可能已被前端缩窄成成员或空值；
  - 存储或表达式类型是可空联合时取可执行成员，多成员联合报 `cap.bad-type`；
  - 非联合且 `nullishKind` 为真时，说明是裸空值或没有联合存储，报 `cap.bad-type`；
  - 其余非空具体类型直接作为结果类型；
  - 右侧必须能赋给结果类型，否则报 `cap.bad-type`；
  - 两侧都继续走 `c.node`，未选中分支同样检查。

### 4.3 降低

文件：`compiler/lower/lower.go`

- `BinaryExpression` 分派在 `&&` / `||` 之前识别 `??`，调用 `l.nullish(n, sc, depth)`；
- `nullish`：
  1. 用 `valueReadType` 取读取类型；若为联合，找出唯一的空值成员和可执行成员，否则报 `lower.bad-type`；
  2. 左侧按存储值求值一次并物化成临时值。标识符需要绕开 `narrowedRead`，直接读取联合局部值或参数，避免先把联合拆箱；
  3. 生成 `not(is <空值成员> left)` 作为条件；
  4. 选中分支 `unwrap` 左侧联合值，未选中分支用 `valueAs` 把右侧降成可执行成员；
  5. 用 `select` 绑定结果，返回 `local`；
- 左侧已经是具体类型时，结果就是左侧值，右侧不求值；
- 不新增 NIR 操作，`SchemaVersion` 不变。

### 4.4 测试

- `tests/source/testdata/nullish.ts`：覆盖 `number | null`、`string | undefined`、左右副作用顺序、右侧惰性、嵌套 `??`、`return` / 实参 / 算术组合和缩窄后的 `??`；
- `tests/source/testdata/errors/nullish-right.ts`：右侧不能赋给可执行成员，`cap.bad-type`；
- `tests/source/testdata/errors/nullish-multi.ts`：多成员联合存储的左侧，`cap.bad-type`；
- `tests/source/testdata/cases.json` 与 `tests/source/reference.json` 登记正向用例并锁定 Node 对照；
- NIR 不新增操作，验证器无需改动；`select` / `is` / `unwrap` 已有测试。

### 4.5 文档

- 本计划；
- `docs/language/semantics/unions-null.md`：`??` 的语义、表示和验收；
- `docs/reference/support.md`、`docs/language/compatibility.md`、`docs/reference/capability.md`：更新 `??` 状态；
- `docs/reference/lowering.md`：说明 `??` 的 `select` / `is` / `unwrap` 降低；
- `docs/validation/s3-01-nullish-coalescing.md`：记录实际命令、输出和边界。

### 4.6 验收命令

```zsh
# Go 代码格式与空白检查。
gofmt -l compiler/capability compiler/lower
git diff --check

# 目标包与全量回归。
go test ./compiler/nir ./compiler/capability ./compiler/lower
go test ./compiler/... ./cmd/... ./internal/... ./tests/...

# 前端与后端基线，没有改 bridge 也要确认未受影响。
./bridge/go/check
./bridge/tsgo/check
```

### 4.7 完成标准

- 正向程序有原生输出，并与 Node 22 的 `shared` 输出逐字节一致；
- 负向用例给出稳定能力错误码，而不是内部故障；
- 全量 Go 测试、`bridge/go/check` 和 `bridge/tsgo/check` 通过；
- 支持表、兼容性矩阵和能力文档同步，不声称完整 TypeScript 兼容。

## 5. 明确不在本阶段

- 类、继承、装饰器；
- 异常、`async` / `await`、Promise；
- npm、Node API 和完整 JavaScript 运行时；
- 条件类型、映射类型和高级类型运算；
- 多后端与跨平台承诺。

## 6. 主要风险

- `??` 与 `?.` 都要求短路；把左侧读两次会悄悄改变副作用次数；
- 前端缩窄后的类型和存储联合类型不一致，降低必须按存储值检查标签；
- 多成员联合一旦放开，会同时影响标签布局、缩窄和输出；
- 循环、闭包和可选参数都会引入新的作用域或调用约定，不能借 `??` 顺手放开。
