# RFC：字符串语义与表示

> 状态：S2.0 已确认；最小闭环（字面量、拼接、长度、严格相等、输出）已实现
> 关联：[Stage 2 计划](../../plans/stage-2.md)、[NIR schema 3](../../design/nir-schema-3-types.md)、[语义索引](README.md)

## 1. 目标

让 `string` 成为可检查、可降低、可原生执行的 Tynel 值类型，并先固定可观察语义，再决定存储和 ABI。

首个切片只要求：

- 字符串字面量；
- `+` 拼接；
- `===` / `!==`；
- `length`；
- `console.log(string)`。

索引、`slice`、模板字符串、遍历和字符串方法随后扩展。

## 2. 推荐的可观察语义

| 操作 | 推荐规则 |
| --- | --- |
| 可观察单位 | UTF-16 code unit；`length` 返回 code unit 数 |
| 索引 `s[i]` | 返回第 `i` 个 code unit 组成的单 code unit 字符串；越界返回 `undefined`（随 S2.4 落地） |
| 相等 | 按 UTF-16 code unit 序列比较；`===` 不做隐式转换 |
| 拼接 | `+` 要求两侧都是 `string`；结果按 code unit 拼接 |
| 遍历 | `for...of` 按 Unicode 码点遍历（与 JavaScript 一致），与索引单位区分 |
| 模板字符串 | 插值先转成字符串；首版只允许 `string` / `number` / `boolean` 插值 |
| 空字符串 | 合法值，长度为 0 |
| 孤立代理项 | 允许作为运行时值存在，因为 `slice` 等操作可以产生；源码字面量仍必须是合法 UTF-8 |

这意味着不能把 Go 的 `len(string)` 或字节索引直接暴露为 Tynel 语义。UTF-8 只作为源码、I/O 和打印边界的编码。

## 3. 表示与 ABI

推荐第一版使用 **UTF-16 存储**：

```text
TynelString {
    data *uint16
    len  int32
}
```

- 索引、`length`、拼接和相等是 O(1) 或 O(n)，与 code unit 语义直接对应；
- ASCII 字符串占用约为 UTF-8 的两倍，作为早期正确性优先的代价；
- 打印和 I/O 边界转换成 UTF-8；
- 字符串头部布局由 Tynel Runtime ABI 定义，不直接暴露 Go `string`。

未来可以在 ABI 不变的前提下换成 UTF-8 + 长度缓存；调用方只依赖 `length`、索引和操作，不依赖底层字节。

## 4. NIR schema 3 影响

- 类型表新增内置 `string` 类型 ID；
- 新增字符串字面量操作，字面量在 JSON 里用 UTF-8 字符串保存，Runtime 构造 UTF-16；
- 新增 `strconcat`、`streq`、`strlen` 操作；首版 `print` 接受 `string`；
- 字符串操作的结果类型在类型表里明确；
- 调试段的局部变量类型引用类型 ID。

## 5. 暂缓

- `String` 方法与原型链；
- 正则、locale、normalization、编码 API；
- 字符串插值的完整 `toString` 协议；
- UTF-8 存储优化；
- 字符串作为对象属性键的运行时行为（等对象模型确定）。

## 6. 验收用例

- `""`、`"a"`、中文、emoji、emoji + 中文混合；
- `"é"` 与组合字符的长度差异；
- `+` 拼接顺序和副作用；
- `===` / `!==` 的相同与不同内容；
- `length` 对 ASCII、中文、emoji 的结果；
- `console.log(string)` 输出；
- 类型错误：`"a" + 1`、`1 + "a"`、`string` 与 `number` 严格相等；
- NIR 回放：字符串字面量和操作的序列化/反序列化；
- 原生程序输出与预期逐字节一致。

## 7. 已确认决定

1. **存储**：第一版使用 UTF-16 存储，ABI 为 `{data *uint16, len int32}`；UTF-8 只用于源码、I/O 和打印边界。
2. **可观察单位**：`length`、索引、`slice` 和相等都按 UTF-16 code unit；遍历按 Unicode 码点。
3. **索引越界**：对齐 JavaScript，返回 `undefined`。字符串索引访问在 S2.4 的空值和联合表示落地后启用；在此之前能力检查明确拒绝。
4. **模板插值**：首版只允许 `string` / `number` / `boolean` 插值，完整 `toString` 协议后续再定。
5. **切片**：`slice` 采用复制语义，不共享底层存储。
6. **孤立代理项**：允许作为运行时值存在；源码字面量仍必须是合法 UTF-8。

## 8. 复评条件

- UTF-16 存储的 ASCII 内存成本成为实际瓶颈；
- JavaScript 字符串方法需要原型或对象模型；
- 索引越界的 `undefined` 语义与数组越界决定产生冲突；
- 模板插值需要支持对象和自定义 `toString`。
