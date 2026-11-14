# 05.06 验收记录：合并模块生成原生程序

本文件记录 05.06 的实际运行结果。被测代码是包含本文件的提交，其正文带 `Task: 05.06`；用 `git log --grep 'Task: 05.06'` 可以定位。

## 环境

| 项目 | 值 |
| --- | --- |
| 机型 | Mac mini，机型标识 Mac16,10 |
| 芯片 | Apple M4，10 核（4 性能核 + 6 能效核），24 GB 内存 |
| 系统 | macOS 26.6.2，构建号 25G83 |
| 架构 | arm64 |
| 工具链 | 锁定的 go1.27.3（`.work/toolchain`），前端为 typescript/v7.0.2 |
| 修改版编译器 | `.work/bridge/compile`，sha256 前缀 `7a75b4ac` |
| 对照运行时 | Node v22.22.2，`node --experimental-strip-types <file>` |

## 正向验收

### 拆分后的 sumTo

`compiler/graph/testdata/merge/math.ts` 导出 `sumTo`，`main.ts` 导入并调用：

```text
$ ./bin/tynel run --root compiler/graph/testdata/merge main.ts
55
```

`dump nir` 的函数：

```text
m1$sumTo
__init$0
__init$1
main
```

`__init$0` 是 `math.ts`，`__init$1` 是 `main.ts`；入口 `main` 依次调用它们，再返回。模块前缀避免同名函数在 NIR 里覆盖。

### 初始化顺序与跨模块 return

`compiler/graph/testdata/init/main.ts`：

```text
$ ./bin/tynel run --root compiler/graph/testdata/init main.ts
1
3
2
4
23
```

前四行是 `shared.ts`、`b.ts`、`a.ts`、`main.ts` 的顶层打印；`shared.ts` 只执行一次。最后一行 `23` 是 `a + b`，两个顶层 `const` 都按模块身份读取。

### 同名 helper

`compiler/graph/testdata/modules/main.ts`：

```text
$ ./bin/tynel run --root compiler/graph/testdata/modules main.ts
41
42
```

`a.ts` 和 `b.ts` 都导出 `helper`，`h` 和 `h2` 分别调用两个模块的函数；函数名在 NIR 里是 `m0$helper` / `m1$helper`，没有互相覆盖。

### NIR 回放不需要源码

```text
$ ./bin/tynel dump nir --root compiler/graph/testdata/merge main.ts > /tmp/merge.nir.json
$ ./bin/tynel build-ir /tmp/merge.nir.json -o /tmp/merge-replay
$ /tmp/merge-replay
55
```

`Pos` 里的文件路径只用于诊断，回放不读 TypeScript 源文件。

## 负向验收

| 情况 | 拒绝点 |
| --- | --- |
| 初始化目标不存在 | `modules.Plan` 的 `init.missing-module` / `init.uninitialized` |
| 值依赖环 | `graph.cycle`，`Plan` 再报 `init.cycle` |
| 缺失导出 | `module.missing-export` |
| 同名覆盖 | 合并时函数名加模块前缀，不会覆盖；NIR 验证器再检查重名 |
| 跨模块签名错配 | TypeScript 诊断；NIR 验证器检查实参数量与类型 |
| 错误源码引用 | `nir.Verify` 拒绝未知函数/参数 |

`nir.Verify` 是独立验证器；合并降低产出不合法 NIR 时返回 `lower.invalid-output`，不会把半份程序交给后端。

## 实现要点

- `compiler/merge` 重新分配全局声明 ID，函数名加 `mN$` 前缀，重映射 `Decl`、`Target`、`Callee` 和跨模块引用。
- `lower.LowerMerged` 把每个模块体降低成 `__init$N`，入口 `main` 按 `order` 调用它们。
- 顶层 `const` 在初始化阶段按表达式内联读取；函数读取模块级 `const` 仍由 `module.captured-variable` 拒绝。
- `build` / `run` 对多文件使用合并 NIR；单文件仍走同一条合并路径（只有一个模块）。
- `dump nir` 对多文件输出合并后的 NIR，供 `build-ir` 回放。

## 累计测试

| 入口 | 结果 |
| --- | --- |
| `./tools/test` | 16 个包通过，根模块 430 个测试（210 个顶层 + 220 个子测试） |
| `tests/source` | 105 个用例（7 个测试函数，153 个子测试） |
| `cmd/tynel` | 27 个测试函数，6 个子测试 |
| `tests/native` | 12 个用例 |
| `./bridge/go/check` | 13 项全过 |
| `./bridge/tsgo/check` | 96 项全过 |
| `go vet ./...` | 无输出 |

## 未覆盖

- 默认导入、命名空间导入、函数捕获模块级 `const`。
- 任意顶层初始化表达式的完整存储模型；当前按表达式内联。
- 只在 `darwin/arm64` 上验证。

## 交付后的副作用导入执行修复（2026-11-11）

合并降低按 `modules.Plan()` 的 `order` 生成 `__init$N`。副作用导入此前没有进入 `Facts.Imports`，初始化计划缺少目标模块，`__init$N` 也就不会生成：依赖文件被发现、读取和检查，模块体却不执行。修复把依赖边与绑定分开记录，`Plan()` 改读 `dependencies`，`side-effect.ts` 因此得到 `__init$0`，入口 `main` 先调用它再调用入口自己的 `__init$1`。

```zsh
./bin/tynel run --root compiler/graph/testdata/init side-effect-main.ts
# 2
# 1
```

原因链、单元测试与累计验证见[05.05 验收记录](05-05-init.md)的「交付后的副作用导入初始化修复（2026-11-11）」一节。

## 交付后的跨模块常量存储修复（2026-11-11）

本节记录跨模块读取导出 `const` 时重新执行初始化表达式的一次运行时语义修复，执行时间是 2026-11-11 10:02 CST，代码基于提交 `7855d58` 的工作树。环境：Mac mini（Apple M4，arm64）、macOS 26.6.2、隔离 go1.27.3、前端 typescript/v7.0.2、Node v22.14.0。上文「实现要点」中「顶层 const 在初始化阶段按表达式内联」自本节起不再成立。

合并降低过去把每个模块体降成独立的 `__init$N`，各自一个作用域。入口读取另一个模块的导出 `const` 时，`lower.identifier` 在当前作用域查不到目标存储，就无条件重新降低 `decl.Body`。这段逻辑没有判断初始化表达式是否纯净：

```typescript
// dep.ts
function init(): number {
  console.log(7);
  return 41;
}

export const value = init();
```

```typescript
// main.ts
import { value } from "./dep.ts";

console.log(value);
```

修复前 `init()` 被执行两次（依赖初始化一次、入口读取又一次），输出 `7 7 41`；Node 的 ES 模块语义只初始化一次，输出 `7 41`。能力检查允许模块级 `const` 以非 void 调用作初始化，初始化计划也先执行值依赖，所以问题不在排序，而在合并降低没有保留跨模块常量的运行时存储身份。

修复采用单一共享初始化函数：`LowerMerged` 把所有模块体按 `order` 降进同一个 `__init`，共享一个作用域；模块级 `const` 求值一次，绑定成带模块前缀（`mN$`）的局部值，声明 ID 仍是全局唯一身份。跨模块读取按 ID 命中同一个局部值，不再重新展开初始化表达式。`lower.identifier` 删去无条件内联分支：初始化作用域里查不到常量存储时按内部故障拒绝，而不是重复执行副作用。不同模块的同名常量因前缀不同而互不覆盖。NIR 形状从每模块一个 `__init$N` 变为单一 `__init`，`main` 调用它一次；NIR schema 与 Go 后端不变。

新增回归：

| 入口 | 用例 | 结果 |
| --- | --- | --- |
| `compiler/lower` | `TestMergedConstInitializedOnce` | 通过；`__init` 里对初始化函数只调用一次，入口读取是 `local` 而非重新求值 |
| `bridge/tsgo/check` | 「导出常量的副作用初始化只执行一次」 | 通过；`const-init` 夹具输出 `7` 后 `41` |
| `bridge/tsgo/check` | 「同名导出常量按模块存储互不覆盖」 | 通过；`const-names` 夹具输出 `30` |

端到端复核：

```zsh
# 副作用初始化只执行一次。
./bin/tynel run --root compiler/graph/testdata/const-init main.ts
# 7
# 41

# 同名导出常量按模块存储。
./bin/tynel run --root compiler/graph/testdata/const-names main.ts
# 30

# 既有夹具输出不变。
./bin/tynel run --root compiler/graph/testdata/init main.ts      # 1 3 2 4 23
./bin/tynel run --root compiler/graph/testdata/merge main.ts     # 55
./bin/tynel run --root compiler/graph/testdata/modules main.ts   # 41 42
```

`dump nir --root compiler/graph/testdata/const-init main.ts` 的函数为 `m1$init`、`__init`、`main`；`__init` 体先 `let m0$value = call m1$init()`，再 `print local m0$value`，初始化调用只出现一次。

累计验证：

| 入口 | 结果 |
| --- | --- |
| `./tools/test` | 20 个包全部通过 |
| `go test -count=1 ./compiler/lower` | 18 个顶层测试通过（含新增 1 个） |
| `./bridge/tsgo/check` | 103 项全过（含新增 2 项） |
| `./bridge/go/check` | 16 项全过 |
| `go vet ./...` | 无输出 |
| `gofmt` | 无差异 |

本次未覆盖：只在 `darwin/arm64` 上验证；模块级 `const` 仍是只读存储，模块变量、闭包与更完整模块语义留待后续；NIR 局部值仍按名字寻址，独立数值 ID 方案未做。
