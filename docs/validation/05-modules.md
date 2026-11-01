# 05.08 验收记录：多文件实验与第五阶段

本文件记录 05.08 的实际运行结果。被测代码是包含本文件的提交，其正文带 `Task: 05.08`；用 `git log --grep 'Task: 05.08'` 可以定位。实验命令在 2026-11-01 17:24–17:25（CST）执行；累计回归在 17:21–17:22 和 17:31 两次执行，最终结果取后一次。

05.08 收束前五阶段的累计回归，并用多文件报价程序检查查找、绑定、初始化和主命令行。第五篇笔记见[把一个报价程序拆成四个文件](../notes/05-modules.md)，查询入口见[多文件程序参考](../reference/modules.md)。

## 环境

| 项目 | 值 |
| --- | --- |
| 机型 | Mac mini，机型标识 Mac16,10 |
| 芯片 | Apple M4，10 核（4 性能核 + 6 能效核），24 GB 内存 |
| 系统 | macOS 26.6.2，构建号 25G83 |
| 架构 | arm64 |
| 工具链 | 锁定的 go1.27.3（`.work/toolchain`），前端为 typescript/v7.0.2 |
| 修改版编译器 | `.work/bridge/compile`，sha256 前缀 `7a75b4ac` |
| 对照运行时 | Node v22.14.0；`tests/source` 参考锁定 v22.22.2，主版本同为 22 |

## 实验源码

`examples/quote` 把报价程序拆成四个文件，入口只负责组合：

```typescript
// main.ts
import { discount } from "./discount.ts";
import { shipping } from "./shipping.ts";
import type { Money } from "./types.ts";

const price: Money = 120;
const reduced = discount(price, 25);
const freight = shipping(2);

console.log(reduced);
console.log(freight);
console.log(reduced + freight);
```

```typescript
// discount.ts
import type { Money } from "./types.ts";

export function discount(price: Money, percent: number): Money {
  return price - price * percent / 100;
}
```

```typescript
// shipping.ts
import type { Money } from "./types.ts";

export function shipping(items: number): Money {
  return items * 5;
}
```

```typescript
// types.ts
export type Money = number;
```

## 正向验收

### 从入口构建一个 exe

```zsh
# 以 examples/quote 为项目根，入口是 main.ts。
./bin/tynel build --root examples/quote main.ts -o out/quote

# 运行产物。
./out/quote

# 对照 Node。
node --experimental-strip-types examples/quote/main.ts
```

```text
$ ./bin/tynel build --root examples/quote main.ts -o out/quote
已生成 out/quote（2430114 字节）
  源码     main.ts（fd9cc3cbd795）
  输入     fccbbe3649de
  身份     b0ebd64daf80
  编译器   7a75b4acf10b
  构建记录 out/quote.build.json

$ ./out/quote
90
10
100

$ node --experimental-strip-types examples/quote/main.ts
90
10
100
```

`120` 的八五折是 `90`，两件运费是 `10`，总价 `100`；原生程序与 Node 逐行一致。

### 依赖图与类型边

```zsh
# 四个文件、五条边，其中三条 type-only。
./bin/tynel dump graph --root examples/quote main.ts
```

| 项 | 值 |
| --- | --- |
| 文件 | `main.ts`、`discount.ts`、`types.ts`、`shipping.ts` |
| 值边 | `main.ts -> discount.ts`、`main.ts -> shipping.ts` |
| 类型边 | `discount.ts -> types.ts`、`shipping.ts -> types.ts`、`main.ts -> types.ts` |
| `hash` | `9196d26c459b3776622e55c049941f9d53983f4c8d6e571d620d039887209a05` |

`types.ts` 从三条路径到达，图里只保留一份。路径都相对项目根；换一个工作目录执行不改变解析结果，这条规则在 05.01 和 05.07 已经验证。

### 符号表

```zsh
# 查看导出和导入绑定。
./bin/tynel dump modules --root examples/quote main.ts
```

- `discount.ts` 导出 `discount`，`shipping.ts` 导出 `shipping`，`types.ts` 导出 `Money`。
- 入口的 `discount` 绑定到 `…/discount.ts#discount`，`shipping` 绑定到 `…/shipping.ts#shipping`。
- 同名函数夹具 `compiler/graph/testdata/modules` 运行输出 `41`、`42`，`h` 与 `h2` 没有互相覆盖。

### 初始化顺序与类型模块不执行

```zsh
# 报价程序的初始化计划。
./bin/tynel dump init --root examples/quote main.ts

# 纯类型依赖夹具：types.ts 里有 console.log(9)，但不执行。
./bin/tynel run --root compiler/graph/testdata/init type-main.ts

# 菱形依赖夹具：shared.ts 只执行一次。
./bin/tynel run --root compiler/graph/testdata/init main.ts
```

```text
order      discount.ts, shipping.ts, main.ts
typeEdges  discount.ts -> types.ts
           shipping.ts -> types.ts
           main.ts -> types.ts
prints     null
```

`type-main.ts` 只输出 `5`，没有 `9`。`init/main.ts` 输出：

```text
1
3
2
4
23
```

顺序是 `shared.ts`、`b.ts`、`a.ts`、`main.ts`；共享模块只打印一次。入口先写 `b` 的导入，所以 `b` 在 `a` 前。

### 合并 NIR 与回放

```zsh
# 导出合并 NIR，记录函数名。
./bin/tynel dump nir --root examples/quote main.ts > out/quote.nir.json

# 不读 TypeScript，只按 NIR 构建。
./bin/tynel build-ir out/quote.nir.json -o out/quote-replay

# 运行回放产物并比较字节。
./out/quote-replay
shasum -a 256 out/quote out/quote-replay
```

函数列表：

```text
m1$discount
m3$shipping
__init$0
__init$1
__init$2
main
```

`m1$` / `m3$` 的编号来自依赖图发现顺序；`__init$0` 至 `__init$2` 来自初始化顺序，分别对应 `discount.ts`、`shipping.ts`、`main.ts`。

```text
已生成 out/quote-replay（2430114 字节）
  输入     fccbbe3649de
  身份     eac40ca665de
  编译器   7a75b4acf10b

90
10
100
a00bb4781d51266f8fcb7a1052dd001d0a0ae105cc44b25fd3f337bc24bae1b4  out/quote
a00bb4781d51266f8fcb7a1052dd001d0a0ae105cc44b25fd3f337bc24bae1b4  out/quote-replay
```

回放的完整身份 `eac40ca665de…` 不含源码文件列表；它覆盖 NIR、配置和工具链，产物与源码构建逐字节相同。

### 构建身份与依赖变化

基线记录：

| 字段 | 值 |
| --- | --- |
| `identity.hash` | `b0ebd64daf80363f7355b2508cc6bb961fe01b3d0b92b212d48151808e59d3f6` |
| `identity.artifactSha256` | `3c8cffcbf21492f08d860bccccb3875b87cc6c0534a75a38fd5c984021aac30d` |
| `identity.inputSha256` | `fccbbe3649de983abbe378086f77a002b0bcdbbafee78ad388a7992c8ac2f5a4` |
| `output.sha256` | `a00bb4781d51266f8fcb7a1052dd001d0a0ae105cc44b25fd3f337bc24bae1b4` |
| `identity.files` | `discount.ts`、`main.ts`、`shipping.ts`、`types.ts` |

把 `shipping.ts` 的每件运费从 `5` 改成 `6`，入口文件不动：

```zsh
# 只改依赖里的乘数，再重新构建同一个入口。
sed -i '' 's/return items \* 5;/return items * 6;/' examples/quote/shipping.ts
./bin/tynel build --root examples/quote main.ts -o out/quote
./out/quote
```

| 项 | 修改前 | 修改后 |
| --- | --- | --- |
| `identity.hash` | `b0ebd64daf80…` | `605c0c11ef06…` |
| `identity.artifactSha256` | `3c8cffcbf214…` | `c5291f226e3c…` |
| `identity.inputSha256` | `fccbbe3649de…` | `4d7f71cc58ab…` |
| `output.sha256` | `a00bb4781d51…` | `29611bf99ff0…` |
| 运行输出 | `90 10 100` | `90 12 102` |

入口的 `source.sha256` 仍是 `fd9cc3cbd795…`；依赖内容变化仍然改变 NIR、产物和完整身份。把 `6` 改回 `5` 后重新构建，身份回到 `b0ebd64daf80…`，输出回到 `90 10 100`。

## 负向验收

### 缺导出

```text
$ ./bin/tynel check --root compiler/graph/testdata/modules missing.ts
tynel: 源码错误 (module.missing-export): 模块 /Users/mahoo/Projects/tynel/compiler/graph/testdata/modules/a.ts 没有导出 "nope"（被 /Users/mahoo/Projects/tynel/compiler/graph/testdata/modules/missing.ts 导入）
$ echo $?
1
```

导出模块和导入模块都给出完整路径，退出状态为 `1`。

### 依赖环

```text
$ ./bin/tynel dump graph --root compiler/graph/testdata/cycle main.ts
tynel: 源码错误 (graph.cycle): 依赖环：/Users/mahoo/Projects/tynel/compiler/graph/testdata/cycle/a.ts -> /Users/mahoo/Projects/tynel/compiler/graph/testdata/cycle/b.ts -> /Users/mahoo/Projects/tynel/compiler/graph/testdata/cycle/a.ts
$ echo $?
1
```

05.08 把环消息从项目内文件名改成绝对路径，避免不同目录存在同名模块时指向不明。`compiler/graph` 的 `TestLoadRejectsCycle` 同时检查 `a.ts` 和 `b.ts` 的完整路径。

### 函数读取模块级 const

```text
$ ./bin/tynel check --root compiler/graph/testdata/modules capture-main.ts
tynel: 源码错误 (module.captured-variable): 函数 "add" 读取模块级变量 "offset"；本阶段不实现闭包或隐式模块环境
$ echo $?
1
```

本阶段不提供隐式模块环境，例子把价格作为参数传入，不依赖未实现的全局捕获行为。

## 累计测试

| 入口 | 结果 |
| --- | --- |
| `./tools/test` | 17 个包通过 |
| `go test -count=1 -p 1 -v ./...` | 根模块 438 个测试（218 个顶层 + 220 个子测试） |
| `tests/source` | 160 个检查（7 个测试函数，153 个子测试） |
| `cmd/tynel` | 35 个测试（29 个顶层 + 6 个子测试） |
| `tests/native` | 12 个测试（9 个顶层 + 3 个子测试） |
| `./bridge/go/check` | 13 项全过 |
| `./bridge/tsgo/check` | 96 项全过 |
| `go vet ./...` | 无输出 |

`./tools/test` 的最终执行时间是 2026-11-01 17:31:00–17:31:49（CST），退出状态 `0`；新增缺导出路径断言后重新跑过全量回归。详细日志留在 `.work/validation/05-08/`，不进入版本库。

## 实现要点

- `examples/quote` 是一个四文件项目：入口、两个值依赖和一个纯类型依赖；它用参数传入价格，避开未实现的函数捕获模块级 `const`。
- `compiler/graph.cycleText` 改为接收 `project.File`，环消息输出绝对路径；`TestLoadRejectsCycle` 增加完整路径断言。
- 多文件主路径没有新增后端分支：`build` / `run` 仍然先载图、取事实、建符号表、排初始化、合并降低，再交给同一个 Go 后端。
- 构建身份覆盖入口和全部类型/值依赖；改依赖后入口源码哈希不变，身份和产物仍然更新。

## 未覆盖

- 默认导入、命名空间导入、再导出、裸模块、`node_modules`、CommonJS 和动态导入。
- 函数捕获模块级 `const`、模块级 `let`、顶层 `if` / 语句块。
- `tsconfig.json`、`paths` / `baseUrl` 和包管理器解析。
- 跨机器、跨用户的缓存复用；业务层没有产物缓存。
- 只在 `darwin/arm64` 上验证。
