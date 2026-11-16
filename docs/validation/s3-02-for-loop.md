# S3.2 验收：经典 `for` 循环

> 验证日期：2026-11-16（本地实际运行时间，不是 Git 提交时间）
> 目标：darwin/arm64
> 关联：[Stage 3 计划](../plans/stage-3.md)、[降低器](../reference/lowering.md)、[能力检查](../reference/capability.md)

## 1. 范围

本轮实现覆盖：

- `for (let i = 0; cond; i = i + 1) { ... }`；
- 初始化只支持单个 `let` 声明，并带初始值；
- 条件必须存在且为 `boolean`；
- 更新可选，只支持简单赋值；
- `break`、`continue`、`return` 和嵌套循环；
- 循环变量只在 `for` 头部、条件和循环体内可见。

不在本轮：表达式初始化、`const` / `var` 循环变量、一次声明多个变量、`i++` / `i--`、复合赋值更新、省略条件、`for...of`、`for...in`、模块级 `for`。NIR 不新增操作。

## 2. NIR 形状

```zsh
# 导出通过验证的 NIR，检查 for 的降低形状。
go run ./cmd/tynel dump nir tests/source/testdata/for.ts
```

`sum` 的 NIR 节选：

```json
{"op":"let","name":"total","value":{"op":"number","bits":"0x0000000000000000"},"mutable":true},
{"op":"let","name":"i$for1","value":{"op":"number","bits":"0x0000000000000000"},"mutable":true},
{"op":"while","id":1,
 "cond":{"op":"compare","operator":"<","left":{"op":"local","name":"i$for1"},"right":{"op":"param","name":"n"}},
 "body":[
   {"op":"set","name":"total","value":{"op":"binary","operator":"+","left":{"op":"local","name":"total"},"right":{"op":"local","name":"i$for1"}}},
   {"op":"set","name":"i$for1","value":{"op":"binary","operator":"+","left":{"op":"local","name":"i$for1"},"right":{"op":"number","bits":"0x3ff0000000000000"}}}
 ]}
```

初始化在 `while` 之前只执行一次；条件每轮重新检查；更新在循环体末尾。

`skip` 的 `continue` 分支里，更新先于跳转：

```json
{"op":"if","cond":{"op":"compare","operator":"===","left":{"op":"local","name":"i$for2"},"right":{"op":"number","bits":"0x4000000000000000"}},
 "then":[
   {"op":"set","name":"i$for2","value":{"op":"binary","operator":"+","left":{"op":"local","name":"i$for2"},"right":{"op":"number","bits":"0x3ff0000000000000"}}},
   {"op":"continue","target":2}
 ]}
```

正常路径的更新仍在循环体末尾；两个位置互斥，`continue` 不会跳过更新，也不会执行两次。

## 3. 原生输出

```zsh
# 构建并运行正向用例。
go run ./cmd/tynel run tests/source/testdata/for.ts

# 源码回归；for 用例同时与 Node 22 的 shared 输出逐字节对照。
go test ./tests/source -run 'TestPrograms/for$|TestErrors/for|TestNodeReference/for$' -v
```

实际输出：

```text
10
0
8
1
4
13
3
3
-1
5
```

覆盖：

- `sum(5)` 得到 10，`sum(0)` 零次循环得到 0；
- `skip(5)` 跳过 2，得到 8，证明 `continue` 执行了更新；
- `halt(5)` 在 2 处 `break`，得到 1；
- `nested(4)` 的内层 `continue` 不结束外层循环，得到 4；
- `shadow()` 在循环后重新声明同名 `i`，得到 13；
- `countdown(3)` 倒序更新得到 3；
- `find(6)` 在循环内 `return` 得到 3，`find(2)` 走循环后的 `return -1`；
- `once()` 的无更新 `for` 通过 `break` 结束，得到 5。

`TestNodeReference/for` 通过，Node 22.22.2 的输出与黄金输出逐字节一致。

## 4. 负向用例

```zsh
# 每个用例都应在能力检查阶段退出 3。
go run ./cmd/tynel check tests/source/testdata/errors/for-expression-init.ts --diagnostics=json
go run ./cmd/tynel check tests/source/testdata/errors/for-const.ts --diagnostics=json
go run ./cmd/tynel check tests/source/testdata/errors/for-increment.ts --diagnostics=json
go run ./cmd/tynel check tests/source/testdata/errors/for-multi.ts --diagnostics=json
go run ./cmd/tynel check tests/source/testdata/errors/for-of.ts --diagnostics=json
go run ./cmd/tynel check tests/source/testdata/errors/for-no-condition.ts --diagnostics=json
```

实际结果：

| 用例 | 退出状态 | 诊断 |
| --- | --- | --- |
| `for-expression-init.ts` | 3 | `cap.unsupported-syntax`：for 的初始化只支持一个 let 声明 |
| `for-const.ts` | 3 | `cap.unsupported-syntax`：for 的循环变量必须是可变的 let |
| `for-increment.ts` | 3 | `cap.inc-dec`：自增自减 `++` 不在本阶段子集内 |
| `for-multi.ts` | 3 | `cap.unsupported-syntax`：for 的初始化只支持一个 let 声明 |
| `for-of.ts` | 3 | `cap.unsupported-syntax`：控制流 ForOfStatement 不在本阶段子集内 |
| `for-no-condition.ts` | 3 | `cap.unsupported-syntax`：for 必须带条件 |

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

结果：`gofmt` 无输出；`git diff --check` 无输出；目标包通过；全量 Go 测试串行通过；bridge 构建成功；`bridge/go/check` 16 项全部通过；`bridge/tsgo/check` 全部通过。

## 6. 实现边界

- `for` 复用 NIR 的 `while`，不新增操作。
- 循环变量使用 `i$forN` 唯一名字；源码循环后的同名局部值是另一个 NIR 名字。
- `continue` 在跳转前补执行更新；`break` 和 `return` 跳过更新。
- 初始化表达式、`const` / `var`、多变量声明、`i++`、省略条件、`for...of` / `for...in` 和模块级 `for` 仍拒绝。
- 既有夹具里依赖 `for` 被拒绝的两处已改用其他未实现控制流：`tests/source/testdata/errors/control.ts` 用 `do...while`，`bridge/tsgo/testdata/capability/uncalled.ts` 用 `switch`，继续验证「不可达函数仍检查」。
- 测试函数 `halt` 避开 `lib.dom` 的全局 `stop()`：同名脚本函数会让检查器把调用解析成全局 `void` 函数，这是前端事实一致性的既有边界，不是本轮引入的行为。
