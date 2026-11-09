# 语句与控制流

本页说明函数体内的声明、赋值、分支与循环。所有条件都要求 `boolean`；不做数字的真假转换。

## 声明与作用域

`const` 一次声明一个值，不可重新赋值。块内 `let` 必须带初始值，可读、可遮蔽，可变 `let` 可以简单赋值：

```typescript
function scope(n: number): number {
  const base: number = 10;
  if (n > 0) {
    const base: number = 20;   // 遮蔽外层，只在当前块可见
    let current: number = base;
    current = current + 1;     // 简单赋值：右值先求值再写入
    return current;
  }
  return base;
}
```

未初始化的 `let` 与 `var` 按 `cap.unsupported-syntax` 拒绝；模块级 `let` 按 `cap.module-let` 拒绝。局部变量先初始化再读取。

## 赋值

简单赋值只作为独立语句，目标必须是可变的 `let`，右值与目标同类型：

```typescript
let count: number = 0;
count = count + 1;    // 先读旧值再写回
```

以下写法明确拒绝：

| 写法 | 错误码 |
| --- | --- |
| 给 `const`、参数或模块常量赋值 | `cap.assign-immutable` |
| 赋值出现在表达式里，如 `const y = (x = 1)` | `cap.assignment-value` |
| 复合赋值 `+=`、`-=` 等 | `cap.compound-assignment` |
| 自增自减 `++` / `--` | `cap.inc-dec` |

## 分支

支持 `if`、`else if`、`else` 与嵌套分支，分支各自一个作用域。条件必须是 `boolean`：`if (1)` 在 TypeScript 里合法，能力检查按 `cap.bad-type` 拒绝。

```typescript
function classify(n: number): number {
  if (n < 0) {
    return 0 - 1;
  } else if (n === 0) {
    return 0;
  }
  return 1;
}
```

有值函数的每条返回路径都必须符合签名；提前返回合法。能力检查遍历未选中的分支：`if (false) { ... }` 里的未实现写法同样拒绝。

## 循环

`while` 的条件每轮重新求值，循环可能零次执行。循环只在运行时发生；编译器不承担证明终止的任务。

```typescript
function sumTo(n: number): number {
  let total: number = 0;
  let i: number = 1;
  while (i <= n) {
    total = total + i;
    i = i + 1;
  }
  return total;
}
```

`break` / `continue` 无标签，只作用于最近循环；带标签写法按 `cap.labeled-jump` 拒绝。支持嵌套循环：

```typescript
function findPair(limit: number): number {
  let i: number = 0;
  while (i < limit) {
    let j: number = 0;
    while (j < limit) {
      if (i + j === limit) {
        return i;        // 提前返回退出整个函数
      }
      j = j + 1;
    }
    i = i + 1;
  }
  return 0 - 1;
}
```

`for` 未排入当前节点；`do`、`switch`、`try` / `throw` 按 `cap.unsupported-syntax` 拒绝。

## 表达式语句与输出

表达式语句的结果被丢弃，不打印：`answer;` 与 `add(1, 2);` 求值照做，值不进输出。调用保留副作用。只有 `console.log(x)` 写一行。

模块体（顶层）只接受函数声明、类型别名、`const` 与表达式语句；顶层 `if`、顶层语句块与模块级 `while` 都拒绝。