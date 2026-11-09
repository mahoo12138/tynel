# 值与表达式

首版的值类型只有 `number` 和 `boolean`，函数返回其中一种或显式 `void`。本页说明值的表示、运算规则与表达式的边界。

## number：binary64

`number` 固定为双精度浮点，后端使用 `float64`。数字字面量在 NIR 里保存为 64 位位模式，不做十进制重解析，因此负零、NaN 和无穷的位模式在编译期不会被改写。

```typescript
const tiny: number = 0.1;
const huge: number = 1e308;

console.log(0.1 + 0.2);
// 0.30000000000000004：保留 binary64 的真实结果，不四舍五入
```

输出使用固定拼写：有限值用最短且可回读的十进制写法；特殊值有约定拼写。输出是受限协议，不是完整的 Node `console.log`。

## boolean 与 void

`boolean` 是完整标量：可以作常量、参数、返回值和输出，拼写为 `true` / `false`。

`void` 只描述「没有值」，只能出现在两个位置：函数返回类型，以及一条完整的表达式语句。

```typescript
function announce(n: number): void {
  console.log(n);
}

announce(1);               // 可以：调用语句，结果丢弃
console.log(announce(1));  // 拒绝：cap.void-value，void 没有值可传
const x = announce(1);     // 拒绝：cap.void-value，void 没有值可绑
```

## 算术与一元运算

`+` `-` `*` `/` 接受两个 `number`，求值顺序与 JavaScript 一致：左操作数先于右操作数物化。

一元 `-` / `+` 只接受数字字面量，直接折成位模式：

```typescript
const a: number = -40;   // 支持：字面量取负
const b: number = +2;    // 支持

const n: number = 40;
const c: number = -n;    // 拒绝：cap.bad-unary，变量取负需要额外的取负操作
```

取余 `%` 与位运算未纳入首版，按 `cap.bad-operator` 拒绝。

## 比较与严格相等

有序比较 `<` `<=` `>` `>=` 只接受两个 `number`。布尔值没有原生顺序，`true < false` 虽然在 TypeScript 里能通过，能力检查按 `cap.bad-type` 拒绝。

`===` `!==` 要求两侧同为 `number` 或同为 `boolean`。宽松 `==` / `!=` 会做隐式类型转换，按 `cap.bad-operator` 拒绝；混合类型比较（`1 === true`）由 TypeScript 直接拒绝。

## 短路与三元式

`&&` / `||` 两侧都必须是 `boolean`，结果绑定到临时值。条件只求值一次，未选择的路径不执行——但静态检查仍然检查两侧：

```typescript
const ok: boolean = false && (1 % 2 === 0);
// 拒绝：cap.bad-operator。右侧虽然被短路跳过，取余仍不在子集内。
```

三元式的条件必须是 `boolean`，两臂同为 `number` 或同为 `boolean`：

```typescript
function absLike(n: number): number {
  return n < 0 ? 0 - n : n;   // 两臂同为 number
}
```

`true ? 1 : false` 在 TypeScript 里得到联合类型，本阶段按 `cap.bad-type` 拒绝，不把联合带进原生后端。未选中的分支同样接受检查。

## 调用

调用目标必须是本文件声明的函数，或经值导入绑定到项目模块导出的函数；实参个数与形参一致。不支持函数值、方法调用与标准库全局函数：`isFinite(1)` 按 `cap.unknown-callee` 拒绝。

`console.log` 是唯一的输出入口，恰好接受一个 `number` 或 `boolean` 实参。识别时核对符号来源：如果文件里声明了自己的 `console`，按 `cap.shadowed-console` 拒绝，不会按名字替换成输出。