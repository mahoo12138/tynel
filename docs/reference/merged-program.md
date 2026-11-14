# 合并模块与入口调度

05.06 把多个模块的事实合并成一个 NIR 编译单元。函数名带模块前缀，跨模块调用指向真实声明；所有模块的顶层初始化语句按 05.05 的顺序降进同一个初始化函数 `__init`，入口 `main` 调用它一次。模块级 `const` 在 `__init` 里求值一次，绑定成带模块前缀的局部值；跨模块读取命中同一个局部值，不会重新执行初始化表达式。后端仍然只看到一个 NIR 文件，不需要为每个模块生成 Go 文件。

## 拆分前后

拆分后的夹具 `compiler/graph/testdata/merge`：

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
import { sumTo } from "./math.ts";

console.log(sumTo(10));
```

运行：

```text
$ ./bin/tynel run --root compiler/graph/testdata/merge main.ts
55
```

和单文件版本输出相同。`dump nir` 里的函数名是 `m1$sumTo`、`__init` 和 `main`；模块前缀避免两个模块都叫 `helper` 时在 NIR 里互相覆盖。

## 初始化入口

`compiler/graph/testdata/init` 的 `main.ts` 先导入 `b`、再导入 `a`，两者共享 `shared`。四个模块的顶层语句按依赖顺序降进同一个 `__init`：

```text
__init 里依次是：
  shared.ts 的打印与 let m0$shared
  b.ts      的打印与 let m1$b
  a.ts      的打印与 let m2$a
  main.ts   的打印
main 调用 __init 一次
```

运行输出：

```text
1
3
2
4
23
```

前四行是四个模块的顶层打印；`shared.ts` 只执行一次。最后一行 `23` 是 `a + b`，其中 `a`、`b` 都是导入的顶层 `const`，读取的是 `__init` 里已经算好的 `m2$a`、`m1$b`，而不是重新求值。

## 导出常量只初始化一次

模块级 `const` 的初始化表达式可能带副作用。`compiler/graph/testdata/const-init`：

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

`__init` 先把 `init()` 的结果存进 `m0$value`（打印一次 `7`），入口的 `console.log(value)` 读取这个局部值。运行输出是 `7` 然后 `41`，`init()` 只执行一次：

```text
$ ./bin/tynel run --root compiler/graph/testdata/const-init main.ts
7
41
```

不同模块可以导出同名常量。`compiler/graph/testdata/const-names` 里 `dep.ts` 与 `other.ts` 都导出 `value`，分别存成 `m0$value`、`m1$value`，入口读取两者之和输出 `30`，互不覆盖。

## 同名函数

`compiler/graph/testdata/modules` 里 `a.ts` 和 `b.ts` 都导出 `helper`。合并后：

```text
m0$helper  → a.ts 的 helper
m1$helper  → b.ts 的 helper
```

`main.ts` 的 `h`、`h2` 分别调用两个前缀函数，输出 `41`、`42`。函数身份不是名字字符串，而是 `(Module, ID)`，NIR 名字只是它的可读投影。

## 回放

```text
$ ./bin/tynel dump nir --root compiler/graph/testdata/merge main.ts > /tmp/merge.nir.json
$ ./bin/tynel build-ir /tmp/merge.nir.json -o /tmp/merge-replay
$ /tmp/merge-replay
55
```

回放不需要原始 TypeScript 文件；`Pos` 里的文件路径只用于诊断。

## 边界

- 顶层 `const` 在 `__init` 里求值一次并存成带模块前缀的局部值，跨模块读取该存储而不是重新求值；函数读取模块级 `const` 仍拒绝。
- 依赖环、缺失导出、伪造模块引用在 05.04/05.05 已经拒绝。
- NIR 验证器仍检查函数引用、实参数量和类型；合并降低产出不合法 NIR 时报内部故障。
- 默认导入、命名空间导入不在子集内。

验收记录见[05.06 验收记录](../validation/05-06-merge.md)。
