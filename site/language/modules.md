# 函数与模块

本页说明跨文件的程序组织：项目根、导入导出、类型别名与初始化顺序。全部模块最终合并成一个 NIR 和一个原生程序。

## 项目根与路径

入口文件相对 `--root` 解释；省略 `--root` 时使用编译器检出根。导入只认显式相对 `.ts` 路径，按来源文件目录解析：

```zsh
# 从任意工作目录构建同一个项目，产物一致。
./bin/tynel build --root examples/quote main.ts -o out/quote
```

路径大小写精确；符号链接解析后不允许越出项目根。缺文件、越界与错误大小写都在检查阶段拒绝。

## 命名导出与值导入

支持 `export function`、顶层 `export const`，以及可重命名的值导入：

```typescript
// discount.ts
export function discount(price: number, pct: number): number {
  return price * (1 - pct / 100);
}
```

```typescript
// main.ts
import { discount as reduce } from "./discount.ts";

const reduced: number = reduce(120, 25);
console.log(reduced);
// 90
```

跨模块绑定到导出声明的完整身份：两个模块都导出 `helper` 时，重命名后的导入各自绑定到正确的文件。导入指向的目标是否真的导出该名字，在合并前用目标模块的事实复核；缺导出按 `module.missing-export` 报源码错误，诊断带两个文件的完整路径。

默认导入、命名空间导入与再导出拒绝；裸模块、CommonJS 与动态导入不在子集内。

## 类型别名与 import type

非递归类型别名只活在类型空间；`import type` 只带类型名字，不产生运行时值：

```typescript
// types.ts
export type Money = number;
```

```typescript
// main.ts
import type { Money } from "./types.ts";

const price: Money = 120;
console.log(price);
```

纯类型依赖参与检查而不执行：修改 `types.ts` 的别名会触发类型错误，但类型模块不进入运行时初始化顺序。联合、接口与 `any` 别名拒绝。

## 初始化顺序

运行时模块按源码导入顺序深度优先执行，共享依赖只执行一次。`examples/quote` 的顺序是 `discount.ts`、`shipping.ts`、`main.ts`；`types.ts` 只走类型边：

```zsh
# 查看初始化计划、值边与类型边。
./bin/tynel dump init --root examples/quote main.ts
```

模块体只接受函数、类型别名、`const` 与顶层执行语句。模块级 `let` 拒绝；函数读取模块级运行时变量按 `module.captured-variable` 拒绝——跨模块共享的值通过参数传递。

## 合并成一个程序

入口有值导入时，`build` / `run` 把依赖图里的模块合并成一个 NIR：函数名带模块前缀，模块体变成 `__init$N`，入口 `main` 按初始化顺序调用。产物仍然是一个原生程序，不会为每个模块生成 Go 文件。

依赖图要求无环；环按 `graph.cycle` 拒绝并打印完整路径。构建身份覆盖入口、全部类型与值依赖：改动任何一个依赖文件都会反映在构建记录里，改类型或导出出错时保留旧产物。

## 函数边界

函数体引用自身参数、局部绑定与已解析的直接调用目标。不支持闭包与嵌套函数；函数不能捕获模块级变量。直接递归与相互递归都支持，每层调用各自持有参数和局部值。