# 把一个报价程序拆成四个文件

前四章的例子都只有一个源文件。这一章用一个报价程序来试多文件：入口、折扣、运费和一个只放类型别名的模块。程序仍然只生成一个原生程序，命令行仍然是 `tynel build`，变的是入口不再包含全部代码，编译器要先找到依赖、把名字绑到正确的声明，再按顺序执行模块初始化。

拆分后的源码在 `examples/quote`：

```typescript
// main.ts：入口，只负责组合两个依赖并打印结果。
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
// discount.ts：按百分比打折。
import type { Money } from "./types.ts";

export function discount(price: Money, percent: number): Money {
  return price - price * percent / 100;
}
```

```typescript
// shipping.ts：每件 5 元运费。
import type { Money } from "./types.ts";

export function shipping(items: number): Money {
  return items * 5;
}
```

```typescript
// types.ts：只用于检查的金额别名，没有运行时代码。
export type Money = number;
```

`main.ts` 不再自己算折扣和运费，而是从两个模块导入函数；三个模块共享同一个 `Money` 别名。`types.ts` 只有类型声明，按理说它不应该出现在运行时的初始化里。这正是这一章要验证的三件事：文件怎么找、名字怎么绑、模块什么时候执行。

## 先看结果

```zsh
# 以 examples/quote 为项目根，入口是 main.ts；先构建，成功后再运行产物。
./bin/tynel build --root examples/quote main.ts -o out/quote || { echo '报价程序构建失败。' >&2; exit 1; }

# 运行上一条命令生成的程序。
./out/quote
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
```

`120` 打八五折是 `90`，两件运费是 `10`，总价 `100`。入口只写了组合逻辑，计算分别落在两个依赖里。`--root` 指定项目根，入口 `main.ts` 按这个根解释；`./discount.ts` 和 `./shipping.ts` 则按 `main.ts` 所在目录解释，和当前工作目录无关。

## 查找：项目根加相对路径

`dump graph` 把找文件的结果直接列出来：

```zsh
# 输出入口、全部依赖、边和类型边标记。
./bin/tynel dump graph --root examples/quote main.ts
```

```text
files  main.ts, discount.ts, types.ts, shipping.ts
edges  main.ts -> discount.ts           value
       discount.ts -> types.ts          type
       main.ts -> shipping.ts           value
       shipping.ts -> types.ts          type
       main.ts -> types.ts              type
hash   9196d26c459b3776622e55c049941f9d53983f4c8d6e571d620d039887209a05
```

四个文件、五条边，其中三条是 `typeOnly`。`types.ts` 从三个模块到达，但图里只有一份；它不会因为被导入三次就复制三份。

路径规则只有两条：入口相对项目根，导入相对来源文件所在目录。`main.ts` 里的 `./discount.ts` 指向 `examples/quote/discount.ts`，不是仓库根下的 `discount.ts`；`discount.ts` 里的 `./types.ts` 又指向它自己旁边的 `types.ts`。解析器只接受显式 `.ts` 路径，大小写必须精确，符号链接不能越出项目根。`examples/quote` 里没有 `tsconfig.json`，也没有裸模块名，这一章不引入包管理器。

找不到文件时，错误会指出缺的是哪个路径。缺导出和环的完整路径记录在后面的“出错的时候”一节；路径解析本身的拒绝条件见[项目根与路径解析](../reference/project-paths.md)。

## 绑定：名字指向声明，不指向字符串

`dump modules` 显示每个模块导出了什么、入口导入了什么：

```zsh
# 查看导出、导入和跨模块引用。
./bin/tynel dump modules --root examples/quote main.ts
```

```text
discount.ts  exports discount
shipping.ts  exports shipping
types.ts     exports Money
main.ts      imports discount -> discount.ts#discount
             imports shipping -> shipping.ts#shipping
```

`#` 后面的名字只是给人看的投影。真正的绑定是“模块绝对路径 + 模块内声明 ID”，所以两个模块都导出 `helper` 也不会混。拿现成的夹具试一下：

```zsh
# a.ts 和 b.ts 都导出 helper；h 和 h2 分别绑定到两个声明。
./bin/tynel run --root compiler/graph/testdata/modules main.ts
```

```text
41
42
```

导入重命名只改变本地名字，目标身份仍是导出模块里的那个声明。NIR 里 `m1$discount`、`m3$shipping` 的前缀来自依赖图的文件顺序：`main` 是 0，`discount` 是 1，`types` 是 2，`shipping` 是 3。前缀不是模块名，也不是执行顺序；它只是把同一个 NIR 里的同名函数区分开。

## 初始化：先值依赖，后入口

`dump init` 给出执行顺序：

```zsh
# 入口先导入 discount、再导入 shipping；类型边单独列出。
./bin/tynel dump init --root examples/quote main.ts
```

```text
order      discount.ts, shipping.ts, main.ts
typeEdges  discount.ts -> types.ts
           shipping.ts -> types.ts
           main.ts -> types.ts
prints     null
```

顺序来自源码里的导入顺序：`main.ts` 先写 `discount`，所以 `discount.ts` 先执行；再写 `shipping`，所以 `shipping.ts` 第二；最后才是入口自己。`types.ts` 只出现在 `typeEdges` 里，没有进入 `order`。这不是因为它的文件名为空，而是因为这次导入只有类型用途。

一个带打印的夹具能把“类型模块不执行”看得更直接：

```zsh
# types.ts 里有一行 console.log(9)，但它只是 import type 的目标。
./bin/tynel run --root compiler/graph/testdata/init type-main.ts
```

```text
5
```

输出只有入口的 `5`，没有 `9`。类型边仍然参与检查，只是不产生运行值。菱形依赖里的共享模块也只执行一次：

```zsh
# shared.ts 从两条路径到达，只打印一次。
./bin/tynel run --root compiler/graph/testdata/init main.ts
```

```text
1
3
2
4
23
```

前四行对应 `shared.ts`、`b.ts`、`a.ts`、`main.ts`。`b` 在 `a` 前，是因为入口先写 `b` 的导入；调整两行导入的顺序，打印顺序也会跟着变。

顶层 `const` 也在这个阶段初始化。入口的 `price` 在 `main.ts` 初始化时写入，再作为参数传给 `discount`。函数读取自己模块的顶层 `const` 仍然被拒绝，因为那需要闭包或隐式模块环境；本阶段不实现这些机制。试一下保留的失败夹具：

```zsh
# add 读取本模块的顶层 offset；当前阶段不提供隐式环境。
./bin/tynel check --root compiler/graph/testdata/modules capture-main.ts
```

```text
tynel: 源码错误 (module.captured-variable): 函数 "add" 读取模块级变量 "offset"；本阶段不实现闭包或隐式模块环境
```

所以这一章的例子把价格作为参数传进去，而不是让 `discount` 去读一个全局值。正文也不会依赖未实现的全局捕获行为。

## 合并：一个 NIR，一个程序

`dump nir` 显示合并后的函数：

```zsh
# 导出合并后的 NIR；函数名带模块前缀，模块体变成 __init$N。
./bin/tynel dump nir --root examples/quote main.ts > out/quote.nir.json
```

```text
m1$discount
m3$shipping
__init$0
__init$1
__init$2
main
```

`__init$0`、`__init$1`、`__init$2` 按初始化顺序对应 `discount.ts`、`shipping.ts`、`main.ts`；入口 `main` 依次调用它们，再返回。后端只看到一个 NIR 文件，不会为每个模块生成 Go 源文件。`__init$0` 和 `m1$discount` 的编号来源不同：前者按执行顺序分配，后者按依赖图的发现顺序分配。

存下来的 NIR 可以单独回放：

```zsh
# 不读 TypeScript，只按这份 NIR 构建。
./bin/tynel build-ir out/quote.nir.json -o out/quote-replay || { echo 'NIR 回放失败。' >&2; exit 1; }

# 运行回放产物，并与源码构建的产物比较。
./out/quote-replay
shasum -a 256 out/quote out/quote-replay
```

```text
$ ./bin/tynel build-ir out/quote.nir.json -o out/quote-replay
已生成 out/quote-replay（2430114 字节）
  输入     fccbbe3649de
  身份     eac40ca665de
  编译器   7a75b4acf10b
  构建记录 out/quote-replay.build.json

$ ./out/quote-replay
90
10
100

$ shasum -a 256 out/quote out/quote-replay
a00bb4781d51266f8fcb7a1052dd001d0a0ae105cc44b25fd3f337bc24bae1b4  out/quote
a00bb4781d51266f8fcb7a1052dd001d0a0ae105cc44b25fd3f337bc24bae1b4  out/quote-replay
```

两次产物的字节完全相同。回放记录的身份 `eac40ca665de` 和源码构建的 `b0ebd64daf80` 不同，因为回放没有源码文件列表；它覆盖的是 NIR、配置和工具链。产物相同说明这次降低结果已经足够重建程序，源码路径只用于诊断。

## 改一个依赖，看结果和身份一起变

构建记录里的身份包含四个文件：

```text
identity  b0ebd64daf80363f7355b2508cc6bb961fe01b3d0b92b212d48151808e59d3f6
artifact  3c8cffcbf21492f08d860bccccb3875b87cc6c0534a75a38fd5c984021aac30d
input     fccbbe3649de983abbe378086f77a002b0bcdbbafee78ad388a7992c8ac2f5a4
output    a00bb4781d51266f8fcb7a1052dd001d0a0ae105cc44b25fd3f337bc24bae1b4
files     discount.ts, main.ts, shipping.ts, types.ts
```

把 `shipping.ts` 的每件运费从 `5` 改成 `6`：

```diff
-// 运费：每件 5 元。
+// 运费：每件 6 元。
 export function shipping(items: number): Money {
-  return items * 5;
+  return items * 6;
 }
```

```zsh
# 入口没有改动，但依赖变了；重新构建并运行同一个入口。
./bin/tynel build --root examples/quote main.ts -o out/quote || { echo '依赖变化后构建失败。' >&2; exit 1; }
./out/quote
```

```text
90
12
102
```

| 项 | 修改前 | 修改后 |
| --- | --- | --- |
| `identity.hash` | `b0ebd64daf80…` | `605c0c11ef06…` |
| `identity.artifactSha256` | `3c8cffcbf214…` | `c5291f226e3c…` |
| `identity.inputSha256` | `fccbbe3649de…` | `4d7f71cc58ab…` |
| `output.sha256` | `a00bb4781d51…` | `29611bf99ff0…` |
| 运行输出 | `90 10 100` | `90 12 102` |

入口的 `source.sha256` 仍然是 `fd9cc3cbd795…`，但身份、NIR 输入和产物都变了。这说明构建不是只看入口文件；它把全部类型和值依赖的内容一起带进了身份。把 `6` 改回 `5`，身份会回到 `b0ebd64daf80…`，运行输出也回到 `100`。

这里没有“第一次构建后就把结果缓存起来”的业务层。`build` 每次都重新载图、检查和降低；Go 自己的缓存只负责底层编译，构建标签再补上 NIR、配置和工具链的差异。判断结果是否正确要看记录里的身份和实际产物，不看缓存命中率。完整规则见[构建身份与依赖变化](../reference/build-identity.md)。

## 出错的时候

缺导出时，诊断同时给出导出模块和导入模块的完整路径：

```zsh
# missing.ts 导入 a.ts 里不存在的 nope。
./bin/tynel check --root compiler/graph/testdata/modules missing.ts
```

```text
tynel: 源码错误 (module.missing-export): 模块 /Users/mahoo/Projects/tynel/compiler/graph/testdata/modules/a.ts 没有导出 "nope"（被 /Users/mahoo/Projects/tynel/compiler/graph/testdata/modules/missing.ts 导入）
$ echo $?
1
```

这是进入模块符号表后的拒绝。如果只是把导出改了名字，TypeScript 可能先报 `TS2724`，消息里只有导入文件的位置和模块说明符；两种诊断都保留，前者给出导出模块的完整路径，后者由 TypeScript 负责“你是不是想用另一个名字”。

依赖环也一样。05.08 把环消息从文件名改成完整路径，避免不同目录下的同名模块指向不明：

```zsh
# main.ts -> a.ts -> b.ts -> a.ts。
./bin/tynel dump graph --root compiler/graph/testdata/cycle main.ts
```

```text
tynel: 源码错误 (graph.cycle): 依赖环：/Users/mahoo/Projects/tynel/compiler/graph/testdata/cycle/a.ts -> /Users/mahoo/Projects/tynel/compiler/graph/testdata/cycle/b.ts -> /Users/mahoo/Projects/tynel/compiler/graph/testdata/cycle/a.ts
$ echo $?
1
```

源码错误（类型错误、缺导出、依赖环）退出 `1`，能力限制退出 `3`，工具链缺失退出 `4`。跨模块函数签名不匹配由 TypeScript 先报；即使前端放行，NIR 验证器还会检查实参数量和类型。

## 这一章之后

现在可以从一个入口构建多文件程序：命名导出、命名导入和导入重命名可用，`import type` 只建立类型边，模块按源码导入顺序初始化，共享依赖只执行一次，合并后的 NIR 能脱离源码回放。报价实验得到一个 exe，改运费依赖后结果和构建身份一起变化。

还没有做到：默认导入、命名空间导入、再导出、裸模块、`node_modules`、CommonJS 和动态导入；函数读取模块级 `const` 仍被拒绝，顶层 `let` 也不在子集内。这些边界由编译器和能力检查明确拒绝，不会在运行时另找一条解释路径。完整清单见[多文件程序参考](../reference/modules.md)，本阶段验收见[05.08 验收记录](../validation/05-modules.md)。

节点 06 不再增加语言语法，而是把已有命令、诊断、构建事务和发行目录收敛成一套可交付的编译器。
