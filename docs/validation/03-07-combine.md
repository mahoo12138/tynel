# 03.07 验收记录：条件表达式的组合行为

本文件记录 03.07 的实际运行结果。被测代码是包含本文件的提交，其正文带 `Task: 03.07`；用 `git log --grep 'Task: 03.07'` 可以定位。03.07 不新增语法，只验证 03.01 至 03.06 的组合行为，并把发现的求值顺序问题修掉。

## 环境

| 项目 | 值 |
| --- | --- |
| 机型 | Mac mini，机型标识 Mac16,10 |
| 芯片 | Apple M4，10 核（4 性能核 + 6 能效核），24 GB 内存 |
| 系统 | macOS 26.6.2，构建号 25G83 |
| 架构 | arm64 |
| 工具链 | 锁定的 go1.27.3（`.work/toolchain`），前端为 typescript/v7.0.2 |
| 修改版编译器 | `.work/bridge/compile`，sha256 前缀 `02ba7de9` |
| 对照运行时 | Node v22.22.2，`node --experimental-strip-types <file>` |

## 组合表

| 输入表达式 | 打印序列 | 结果 |
| --- | --- | --- |
| `t(1,true) \|\| t(2,false) && t(3,true)` | `1` | `true` |
| `(t(4,false) \|\| t(5,true)) && t(6,false)` | `4`、`5`、`6` | `false` |
| `t(7,false) && t(8,true) \|\| t(9,true)` | `7`、`9` | `true` |
| `mark(1,false) ? 10 : mark(2,true) ? 20 : 30` | `1`、`2` | `20` |
| `mark(3,true) ? (mark(4,false) ? 40 : 50) : 60` | `3`、`4` | `50` |
| `true ? (false ? 1 : 2) : (true ? 3 : 4)` | 无 | `2` |
| `(mark(5,true) \|\| mark(6,false)) ? 70 : 80` | `5` | `70` |
| `f(1,10) + f(2,20)` | `1`、`2` | `30` |
| `(f(3,1) < f(4,2)) ? f(5,50) : f(6,60)` | `3`、`4`、`5` | `50` |
| `f(7,1) + (f(8,2) < f(9,3) ? f(10,10) : f(11,20))` | `7`、`8`、`9`、`10` | `11` |

`t`、`mark` 返回 `boolean`，`f` 返回 `number`；它们都先打印编号再返回参数。表中「打印序列」是这一行表达式新增的输出，不含上一行。

## 正向验收

### 优先级与括号

```text
$ ./bin/tynel run tests/source/testdata/combine.ts
1
true
4
5
6
false
7
9
true
```

第一行 `&&` 先结合，`t(2)`、`t(3)` 被 `t(1)` 短路；第二行括号让 `t(4)`、`t(5)` 先算，再与 `t(6)` 组合；第三行 `t(8)` 被 `t(7)` 短路，`t(9)` 仍然求值。

### 多层选择

```text
$ ./bin/tynel run tests/source/testdata/combine-ternary.ts
1
2
20
3
4
50
2
5
70
```

第三行 `true ? (false ? 1 : 2) : (true ? 3 : 4)` 只输出结果 `2`，两条未选分支都没有调用；第四行的短路只打印 `5`。

### 输出顺序

```text
$ ./bin/tynel run tests/source/testdata/combine-order.ts
1
2
30
3
4
5
50
7
8
9
10
11
```

第三行在修正前是 `8, 9, 10, 7, 11`：右侧三元式的准备语句跑到了左侧 `f(7, 1)` 前面。降低器现在在右侧带准备语句时先把左侧物化成临时值，顺序恢复为 `7, 8, 9, 10, 11`。这是 03.07 修掉的真实缺陷，不是测试期望写错。

### 产物与回放一致

```text
$ ./bin/tynel build tests/source/testdata/combine.ts -o out/combine
已生成 out/combine（2429986 字节）
  源码     tests/source/testdata/combine.ts（89aa35e6f197）
  输入     da0f7ae17d45
  编译器   02ba7de95dd3

$ ./bin/tynel dump nir tests/source/testdata/combine.ts > out/combine.nir.json
$ ./bin/tynel build-ir out/combine.nir.json -o out/combine-replay
已生成 out/combine-replay（2429986 字节）
  输入     da0f7ae17d45
  编译器   02ba7de95dd3

$ shasum -a 256 out/combine out/combine-replay
5240063da3c455052a28296dd4a2eab14dad871d9845e43f4d9a1001553676ab  out/combine
5240063da3c455052a28296dd4a2eab14dad871d9845e43f4d9a1001553676ab  out/combine-replay
```

## 数值回归

原有的数值用例没有受选择扩展影响：

```text
$ ./bin/tynel run tests/source/testdata/add.ts
42

$ ./bin/tynel run tests/source/testdata/order.ts
1
2
3

$ ./bin/tynel run tests/source/testdata/compare.ts
true
true
true
false
true
false
true
false

$ ./bin/tynel run tests/source/testdata/compare-order.ts
1
2
true
```

## 协议与预算

| 检查 | 值 | 结果 |
| --- | --- | --- |
| 当前 `schemaVersion` | 1 | 接受 |
| 旧协议 `schemaVersion` | 2 | `nir.version`，退出 1 |
| 最大字节数 | 1 MiB | 超出报 `nir.budget` |
| 最大嵌套深度 | 64 | 65 层 `if` 报 `nir.budget` |
| 最大节点数 | 10000 | 单元测试覆盖 |
| 最大函数数 | 256 | 单元测试覆盖 |

结构扩展后重新核对了协议版本：`short`、`select` 都留在 `schemaVersion` 1 里，字段是新增的可选/必填项，不兼容旧文件时由解码器明确拒绝，不做缺省兼容。

## 坏 NIR 直接交给编译器

这些用例绕过主 CLI 的源码路径，直接调用 `tynel build-ir`；测试在 `cmd/tynel/buildir_test.go`，不依赖真实工具链。

| 输入 | 结果 | 退出 |
| --- | --- | --- |
| `schemaVersion` 为 2 | `nir.version` | 1 |
| 未知 `op` | `nir.bad-op` | 1 |
| 引用未定义局部值 | `nir.unknown-local` | 1 |
| 同一块重复 `let` | `nir.duplicate-name` | 1 |
| `short` 缺少 `value` | `nir.missing` | 1 |
| `select` 两臂类型不同 | `nir.result-type` | 1 |
| 嵌套深度超过 64 | `nir.budget` | 1 |

```text
$ go test ./cmd/tynel -run 'TestBuildIRRejects' -v
=== RUN   TestBuildIRRejectsBadNIR
=== RUN   TestBuildIRRejectsBadNIR/旧协议
=== RUN   TestBuildIRRejectsBadNIR/未知操作
=== RUN   TestBuildIRRejectsBadNIR/未定义局部值
=== RUN   TestBuildIRRejectsBadNIR/重复定义
=== RUN   TestBuildIRRejectsBadNIR/短路缺少右值
=== RUN   TestBuildIRRejectsBadNIR/三元式两臂类型不同
=== RUN   TestBuildIRRejectsDeepNesting
--- PASS: TestBuildIRRejectsBadNIR (0.00s)
--- PASS: TestBuildIRRejectsDeepNesting (0.00s)
PASS
```

## 计数

| 项目 | 数量 |
| --- | --- |
| 条件组合程序 | 3 个（`combine`、`combine-ternary`、`combine-order`） |
| 组合表达式 | 10 条 |
| 组合打印行 | 30 行 |
| 坏 NIR 直接拒绝用例 | 7 个（6 个子测试 + 1 个深度测试） |

## 累计测试

| 入口 | 结果 |
| --- | --- |
| `./tools/test` | 13 个包通过，根模块 315 个测试（168 个顶层 + 147 个子测试） |
| `tests/source` | 62 个用例（7 个测试函数，93 个子测试） |
| `cmd/tynel` | 25 个测试函数，6 个子测试 |
| `tests/native` | 8 个用例 |
| `./bridge/go/check` | 10 项全过 |
| `./bridge/tsgo/check` | 66 项全过 |
| `go vet ./...` | 无输出 |

Node 对照全部通过，3 个组合程序都标记为 `shared`，输出逐字节相同。

## 未覆盖

- 没有 `while`、`for`、赋值与递归；这些属于节点 04。
- 没有联合类型、`undefined` 与对象。
- 组合表只覆盖当前子集，不是完整 TypeScript 优先级表。
- 只在 `darwin/arm64` 上验证。

## 交付后的求值顺序加固（2026-11-09）

上面各节的命令输出和累计数字属于 03.07 原始验收；本节记录之后的一次修复，执行时间是 2026-11-09 15:55 CST，代码基于提交 `8133d9c` 的工作树。环境仍是 macOS 26.6.2、arm64、锁定的 go1.27.3，Node 为 v22.22.2，命令行程序由 `./tools/build` 从当前工作树生成。

03.07 修掉的是「左侧没有准备语句、右侧有」这一半：`pairOperands()` 的条件写成 `len(rightPre) > 0 && len(leftPre) == 0`。剩下的一半是两侧都带准备语句。看这段程序，左侧 `identity(...)` 的实参是三元式，准备工作会先算出实参并绑定临时值，但 `identity(...)` 这次调用本身还没执行；右侧的三元式另带一份准备工作：

```typescript
function mark(n: number): number { console.log(n); return n; }
function identity(n: number): number { console.log(10); return n; }
function add(flag: boolean): number {
  return identity(flag ? mark(1) : mark(2)) + (flag ? mark(3) : mark(4));
}
console.log(add(true));
```

因为 `leftPre` 非空，旧条件不成立，左侧最终调用没有物化；NIR 里 `binary` 的左操作数仍是 `call identity`，准备工作却是「左侧 select → 右侧 select」。后端把 `binary` 变成 Go 的二元表达式，`identity` 的副作用就落到右侧 select 之后。修复前 `./bin/tynel run tests/source/testdata/binary-order.ts` 的四组输出里，`10` 都排到了右侧标记后面：

```text
$ ./bin/tynel run tests/source/testdata/binary-order.ts
1
3
10
4
5
7
10
true
2
4
10
6
6
8
10
true
```

Node 的正确顺序是 `1, 10, 3, 4` 这一组起头。修法就是去掉 `len(leftPre) == 0`：只要右侧有准备语句，就在左侧准备之后、右侧准备之前，把左侧最终表达式绑定成临时值，`binary` / `compare` 改用 `local` 读它。修复后与 Node 逐字节一致：

```text
$ node --experimental-strip-types tests/source/testdata/binary-order.ts > /tmp/binary-order.node.txt
$ ./bin/tynel run tests/source/testdata/binary-order.ts > /tmp/binary-order.tynel.txt
$ diff /tmp/binary-order.node.txt /tmp/binary-order.tynel.txt
（无输出）

$ ./bin/tynel run tests/source/testdata/binary-order.ts
1
10
3
4
5
10
7
true
2
10
4
6
6
10
8
true
```

回归覆盖分两层。`tests/source/testdata/binary-order.ts` 走完整前端到原生二进制：`+` 和 `<` 各写一个函数，左右两侧都含三元式，左侧的 `identity` 带打印副作用，`flag` 取 `true` 与 `false` 各跑一遍，连未选中分支不能执行也一起验了；用例登记在 `cases.json`，并在 `reference.json` 标记为 `shared`，由 `TestPrograms` 和 `TestNodeReference` 各核对一次。`compiler/lower/lower_test.go` 的 `TestMaterializesLeftBeforeRightPrelude` 则钉住 NIR 形状：每个运算的准备语句必须是 4 条，顺序为「左侧 select → 实参物化 → 左侧最终调用物化 → 右侧 select」，且二元表达式的左操作数读的正是那条物化结果。

把 `lower.go` 里的旧条件临时换回后，两层都失败，说明它们确实守住了这个缺陷：

```text
$ ./.work/toolchain/go1.27.3-sdk-darwin-arm64/bin/go test ./compiler/lower -run TestMaterializesLeftBeforeRightPrelude -count=1
--- FAIL: TestMaterializesLeftBeforeRightPrelude/加法
    lower_test.go:826: 期望 4 条准备语句，实际 3：...

$ ./.work/toolchain/go1.27.3-sdk-darwin-arm64/bin/go test ./tests/source -run 'TestPrograms/binary-order' -count=1
--- FAIL: TestPrograms/binary-order
    source_test.go:269: binary-order：输出为 "1\n3\n10\n4\n5\n7\n10\ntrue\n2\n4\n10\n6\n6\n8\n10\ntrue\n"，参考为 "1\n10\n3\n4\n5\n10\n7\ntrue\n2\n10\n4\n6\n6\n10\n8\ntrue\n"
```

恢复修复后的全量检查：

```text
$ ./.work/toolchain/go1.27.3-sdk-darwin-arm64/bin/gofmt -l compiler/lower/lower.go compiler/lower/lower_test.go
（无输出）

$ ./.work/toolchain/go1.27.3-sdk-darwin-arm64/bin/go vet ./...
（无输出）

$ ./tools/test
测试通过：20 个包
```

`docs/reference/lowering.md` 的「求值顺序」一节补上了二元操作数的物化规则。本次未覆盖：`-`、`*`、`/`、`===` 与 `!==` 没有逐个写夹具，它们与 `+`、`<` 共用 `pairOperands()`，由同一条分支保证；仍只在 `darwin/arm64` 上验证。
