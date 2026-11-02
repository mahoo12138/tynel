# 多文件程序参考

这一页回答“怎样把程序拆成几个文件，并用 `tynel` 编成一个原生程序”。它按使用顺序整理项目根、导入导出、初始化、命令和诊断；每一步的完整推导在对应的专题页里。

## 项目怎么摆

项目根是一个目录，入口是根内的一个 `.ts` 文件。导入只接受显式相对路径，并且带 `.ts` 后缀：

```text
examples/quote/
├── main.ts        入口，按 --root examples/quote 解析
├── discount.ts    main.ts 用 ./discount.ts 导入
├── shipping.ts    main.ts 用 ./shipping.ts 导入
└── types.ts       三个模块用 import type 导入
```

```typescript
// main.ts
import { discount } from "./discount.ts";
import type { Money } from "./types.ts";
```

`--root` 决定入口从哪里找；`./` 和 `../` 相对来源文件所在目录，不相对当前工作目录，也不相对项目根。换一个工作目录，入口和依赖仍然解析到同一组文件。

| 写法 | 结果 |
| --- | --- |
| `tynel build --root examples/quote main.ts -o out/quote` | 入口是 `examples/quote/main.ts` |
| `import { discount } from "./discount.ts"` | 目标在 `main.ts` 同目录 |
| `import { x } from "../shared/x.ts"` | 允许，但不能越出项目根 |

裸模块名、绝对路径导入、目录导入和省略 `.ts` 都拒绝，不做后缀推断或 `node_modules` 搜索。完整规则见[项目根与路径解析](project-paths.md)。

## 导入与导出

当前只支持命名导出和命名导入：

```typescript
// math.ts
export function sumTo(n: number): number {
  let sum = 0;
  let i = 1;
  while (i <= n) {
    sum = sum + i;
    i = i + 1;
  }
  return sum;
}

// main.ts
import { sumTo as total } from "./math.ts";
console.log(total(10));
```

- 导入重命名只改变本地名字，绑定目标仍是 `math.ts` 里的 `sumTo`。
- 两个模块可以导出同名函数；模块身份由“规范化绝对路径 + 模块内声明 ID”组成，不是函数名字符串。
- 顶层 `const` 可以作为值导出和导入；函数读取自己模块的顶层 `const` 仍被拒绝（`module.captured-variable`）。
- `import type` 只建立类型边，不产生运行时值；类型别名仍然参与检查。
- 默认导入、命名空间导入、再导出和动态导入不在子集内。

绑定细节见[跨模块名称绑定](module-bindings.md)，类型边见[类型别名与纯类型导入](type-aliases.md)。

## 初始化顺序

初始化从入口出发，按源码里的导入顺序深度优先：先完成值依赖，再执行当前模块。每个值模块只执行一次；纯类型导入不执行模块体。

```text
main.ts 先导入 discount.ts，再导入 shipping.ts
→ discount.ts
→ shipping.ts
→ main.ts
```

`types.ts` 只被 `import type` 引用，所以不进入执行顺序，即使文件里有顶层语句也不会运行。菱形依赖里的共享模块只执行一次。

顶层 `const` 在所属模块初始化时求值；导入方通过模块身份读取目标存储。函数读取模块级 `const` 需要闭包或隐式环境，本轮不实现，直接拒绝。

顺序规则见[模块初始化顺序](init-order.md)，权威计划可以从 `tynel dump init` 查看。

## 一个程序怎样生成

多个模块最终合并成一个 NIR 编译单元：

- 模块函数名加 `mN$` 前缀，`N` 是依赖图里的文件序号。
- 每个执行模块的顶层语句变成 `__init$N`，按初始化顺序编号。
- 入口 `main` 依次调用这些 `__init$N`；入口自己的顶层语句也在最后一个 `__init$N` 里。
- 后端仍然只看到一个 NIR 文件，不会为每个模块生成 Go 源文件。

```zsh
# 构建多文件入口；成功后才运行产物。
./bin/tynel build --root examples/quote main.ts -o out/quote
./out/quote

# 查看合并 NIR。
./bin/tynel dump nir --root examples/quote main.ts > out/quote.nir.json

# 不读 TypeScript，按 NIR 回放。
./bin/tynel build-ir out/quote.nir.json -o out/quote-replay
./out/quote-replay
```

同一份 NIR 的回放产物与源码构建逐字节相同；回放身份不含源码文件列表，但覆盖 NIR、配置和工具链。合并规则见[合并模块与入口调度](merged-program.md)。

## 命令

| 命令 | 用途 |
| --- | --- |
| `tynel check <入口> --root <根>` | 类型检查、能力检查、降低并验证合并 NIR，不生成机器码 |
| `tynel build <入口> -o <路径> --root <根> [--target <平台>]` | 生成一个原生程序和构建记录 |
| `tynel run <入口> --root <根> [--target <平台>]` | 构建并运行本次产物 |
| `tynel dump graph <入口> --root <根>` | 文件、边、类型边和图哈希 |
| `tynel dump modules <入口> --root <根>` | 导出、导入和跨模块绑定 |
| `tynel dump init <入口> --root <根>` | 初始化顺序、值边、类型边和可识别的字面量打印 |
| `tynel dump nir <入口> --root <根>` | 合并后的 NIR |
| `tynel build-ir <NIR> -o <路径> [--target <平台>]` | 从固定 NIR 回放，不读源码；不接受 `--root` |

`dump` 只输出 JSON；失败时不输出部分结果。命令和退出状态的完整说明见[命令行参考](cli.md)。

## 构建身份

成功构建在产物旁边写 `<产物>.build.json`。多文件构建的身份包含：

- 入口的相对路径和内容哈希；
- 全部类型和值依赖的相对路径与内容哈希；
- NIR 快照的内容哈希；
- 目标平台、固定支持目录和工具链内容。

只改一个依赖，入口源码哈希可以不变，但 `identity.hash`、`identity.inputSha256`、`identity.artifactSha256` 和产物都会更新。只改类型依赖时 NIR 可能不变，产物可以复用，但完整身份和构建记录仍会更新。规则见[构建身份与依赖变化](build-identity.md)。

## 诊断

| 错误码 | 含义 |
| --- | --- |
| `project.missing` | 入口或导入目标不存在 |
| `project.bare-module` | 导入没有 `./` 或 `../` 前缀 |
| `project.absolute-path` | 导入写了绝对路径 |
| `project.outside-root` | 导入或符号链接越出项目根 |
| `project.case-conflict` | 请求大小写与磁盘条目不一致 |
| `project.non-ts` | 目标不是 `.ts` 文件 |
| `graph.cycle` | 值依赖或类型依赖成环；消息列出完整路径 |
| `graph.resolution-mismatch` | tsgo 与项目解析指向不同文件 |
| `module.missing-export` | 导入的模块没有该导出；消息给出导出和导入文件路径 |
| `module.missing-module` | 导入指向依赖图里不存在的模块 |
| `module.type-import` | 值导入指向纯类型导出 |
| `module.duplicate-export` | 同一模块重复导出同名声明 |
| `module.captured-variable` | 函数读取模块级运行时变量；本轮不实现闭包 |
| `init.cycle` | 初始化计划里出现值依赖环 |
| `init.uninitialized` | 初始化时读取尚未执行的模块值 |
| `init.type-value-mix` | 把 `import type` 绑定当值使用 |
| `init.missing-module` | 初始化计划里出现不存在的模块 |

源码错误退出 `1`，能力限制退出 `3`。跨模块函数签名不匹配通常由 TypeScript 先报；合并降低后，NIR 验证器仍会检查函数引用、实参数量和类型。

## 边界

- 只有显式相对 `.ts` 导入；没有 `tsconfig.json`、`paths`、`baseUrl`、包管理器或 `node_modules`。
- 只有命名导入、命名导出和导入重命名；没有默认导入、命名空间导入、再导出、CommonJS 或动态导入。
- 函数不能捕获模块级 `const`；模块级 `let`、顶层 `if` 和顶层语句块不在子集内。
- 不读取时间、环境变量或网络状态进入构建身份；产物不依赖 Node 或 Go 工具链运行。
- 只在 `darwin/arm64` 上验证。

相关材料：[第五篇笔记](../notes/05-modules.md)、[05.08 验收记录](../validation/05-modules.md)、[模块图与依赖快照](module-graph.md)、[合并模块与入口调度](merged-program.md)、[操作支持表](support.md)。
