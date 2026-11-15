# S2.3 验收：数组与元组第一个切片

> 验证日期：2026-11-15（本地实际运行时间，不是 Git 提交时间）
> 目标：darwin/arm64
> 关联：[对象与数组 RFC](../language/semantics/objects-arrays.md)、[NIR 格式](../reference/nir.md)、[bridge 说明](../reference/bridge.md)

## 1. 范围

本轮实现覆盖：

- 同质数组 `T[]`：字面量、索引读取、`length`；
- 嵌套数组、对象数组、对象字段里的数组；
- 数组作为参数、返回值、局部值和三元式结果；
- 元组：字面量、编译期整数索引、越界在编译期拒绝；
- 数组索引的运行时边界检查：越界按 Tynel 数据错误退出 6，不是 Go panic，也不是 JavaScript `undefined`；
- 负向能力限制：数组输出、数组身份比较、数组方法、展开、变量元组索引和空元组。

不在本轮：`push` / `slice` 等数组方法、数组赋值与扩容、稀疏/异质数组、元组可变长度操作、数组和元组的 `===` 身份比较、字符串索引（等待 S2.4）。数组切片复制语义保留到标准库阶段实现。

## 2. NIR 与验证器

```zsh
# array/tuple 语句、index、len、元素类型和元组越界。
go test ./compiler/nir
```

结果：通过。覆盖：

- `array` 的元素类型与声明结果类型；
- `tuple` 的元素个数、逐位置类型和索引范围；
- 元组索引必须是整数数字字面量；
- `len` 只接受数组；
- 编解码往返保留 `items`、`index` 和类型 ID。

## 3. 原生输出

```zsh
# 手写 NIR：数组遍历、length、索引、嵌套数组和元组。
go test ./tests/native -run 'TestFixtureOutputs/数组' -v
```

实际输出：

```text
6
3
2
1
7
true
```

`sum` 用 `while` 与 `len` 遍历数组；`grid[1][0]` 先取内层数组再取元素；`pair[0]` / `pair[1]` 读取元组的两个位置。

越界：

```zsh
go test ./tests/native -run 'TestArrayBoundsExitsWithDataError' -v
```

实际结果：

```text
exit=6
stderr> tynel: 数据错误：数组索引 2 越界；长度 2
```

程序没有标准输出。退出状态 6 是运行时数据错误，与编译失败的 1–5 分开；它不是 Go 的 panic 栈。

## 4. 源码到原生

```zsh
# 源码回归；array 用例同时与 Node 22 的 shared 输出逐字节对照。
go test ./tests/source -run 'TestPrograms/array' -v
```

`array.ts` 实际输出：

```text
6
3
2
1
3
2
0
4
7
seven
1
3
5
7
9
false
```

覆盖：

- 数组参数、返回值、局部值和对象字段；
- 空数组、嵌套数组、对象数组和字符串数组；
- `while` 中使用动态下标；
- 三元式选择数组；
- 元组字面量、元组参数、元组返回值和常量索引；
- `type Nums = number[]` 与 `type FlagPair = [number, boolean]` 别名。

负向用例：

| 用例 | 结果 |
| --- | --- |
| `errors/array-print.ts` | `cap.bad-console-call`，数组没有输出格式 |
| `errors/array-equality.ts` | `cap.bad-type`，数组身份比较尚未实现 |
| `errors/array-method.ts` | `cap.unknown-callee`，数组方法尚未实现 |
| `errors/tuple-index-variable.ts` | `cap.unsupported-syntax`，元组索引必须是编译期整数 |
| `errors/array-spread.ts` | `cap.unsupported-syntax`，数组展开尚未实现 |
| `errors/tuple-empty.ts` | `cap.bad-type`，空元组没有可执行布局 |

## 5. 后端与前端检查

```zsh
./bridge/go/build
./bridge/go/check
```

结果：bridge 构建成功；16 项检查全部通过，旧 NIR 回放和字符串/对象路径不受影响。

```zsh
./bridge/tsgo/check
go test ./compiler/... ./cmd/... ./internal/... ./tests/...
```

结果：前端检查全部通过；全量 Go 测试通过，包括新增的数组/元组原生夹具和源码用例。

## 6. 实现边界

- 数组在 bridge 里是 Go 切片；切片头对 GC 可见，元素中的对象指针保持可见。NIR 不暴露 Go 切片布局，长期 ABI 仍按 Tynel 值模型收敛。
- 元组在 bridge 里是 `*struct{f0 T0; f1 T1; ...}`，索引在 NIR 里必须已经是常数。
- 数组索引先由固定支持函数把 `number` 转成整数，再做边界检查；非整数、NaN、无穷和越界都按数据错误退出 6。
- 数组与元组目前不参与 `===` / `!==`；引用身份比较要等数组 ABI 和切片共享策略确定。
- 空数组字面量的自身类型是 `never[]`，前端在有上下文时采用目标数组/元组形状；没有上下文的 `[]` 仍会因元素类型 `never` 被拒绝。
