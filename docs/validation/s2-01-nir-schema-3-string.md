# S2.0 / S2.1 验收：NIR schema 3 与字符串最小闭环

> 验证日期：2026-11-15（本地实际运行时间，不是 Git 提交时间）  
> 目标：darwin/arm64  
> 关联：[NIR schema 3 设计](../design/nir-schema-3-types.md)、[字符串 RFC](../language/semantics/strings.md)、[NIR 格式](../reference/nir.md)、[bridge 说明](../reference/bridge.md)

## 1. 范围

本轮实现覆盖：

- NIR schema 3 类型表：内置 ID 1–7、复合类型 ID >= 8、v1/v2 迁移、类型引用与 `void` 规则；
- 降低器输出类型 ID；
- 字符串最小闭环：字面量、无插值模板、`+` 拼接、`===` / `!==`、`length`、`console.log(string)`，以及 `string` 参数、返回值、`const`、`let` 和三元式结果；
- bridge 接受 schema 1/2/3，用 UTF-16 结构体执行字符串操作；
- 负向能力限制：`"a" + 1`、字符串与数字严格相等、字符串方法、模板插值。

不在本轮：字符串索引、`slice`、`for...of`、模板插值、字符串方法、对象/数组/联合/函数值的执行操作。

## 2. NIR 与单元测试

```zsh
# NIR 的版本迁移、类型表、字符串操作和调试段。
go test ./compiler/nir
```

结果：通过。覆盖：

- v1 回放迁移到 3 且清空调试段；
- v2 回放的 `result` / `params[].type` / `DebugLocal.type` 映射为 1/2/3；
- 对象、数组、元组、联合和函数类型往返，编码按 ID 升序；
- 递归对象类型按 ID 自引用通过验证；
- 覆盖内置 ID、重复 ID、未知 kind、缺 `element`、空元组、单成员联合、缺 `result`、未知引用和 `void` 参数按 `nir.type-*` 拒绝；
- `string`、`concat`、`strlen` 与字符串 `===` / `!==` 的类型规则；
- 空字符串的 `value` 字段不会被省略。

```zsh
# 全部编译器、命令和内部包测试。
go test ./compiler/... ./cmd/... ./internal/...
```

结果：通过。

## 3. 原生输出

```zsh
# 手写 schema 3 字符串模块：greet(世界)、emoji 长度、相等、空字符串。
go test ./tests/native -run 'TestFixtureOutputs/字符串最小闭环' -v
```

实际输出：

```text
hello, 世界
2
true
true

```

其中 `😀` 的 `strlen` 是 2，符合 UTF-16 code unit 语义；空字符串打印为空行。

```zsh
# 全部原生夹具，包括已有的 v1 回放、循环、递归和特殊数值。
go test ./tests/native
```

结果：通过。

## 4. 源码到原生

```zsh
# 源码回归；string 用例同时与 Node 22 的 shared 输出逐字节对照。
go test ./tests/source
```

结果：通过，包含：

- `string.ts`：拼接、`.length`、emoji、组合字符、空字符串、严格相等、`string` 参数与返回、局部 `let` 写回、`string` 三元式结果；
- `let-string.ts`：局部 `string` `let` 的拼接与赋值；
- 负向：`string-plus-number.ts`（`cap.bad-type`）、`string-number-equal.ts`（TypeScript 2367）、`string-method.ts`（`cap.unknown-callee`）。

## 5. 后端检查

```zsh
./bridge/go/build
./bridge/go/check
```

结果：修改版编译器构建成功；16 项 bridge 检查全部通过，包括旧 schema 回放和缺少支持声明的拒绝路径。

```zsh
./bridge/tsgo/check
```

结果：全部通过，包括外部消费者测试、适配包测试、源码到原生和命令行走查。

## 6. 已知限制

- 字符串索引返回 `undefined` 需要先有联合/空值表示，留给 S2.4；
- 模板插值、`slice`、字符串方法和 UTF-8 存储优化未实现；
- 类型表已经能描述对象、数组、元组、联合和函数类型，但对应的 NIR 执行操作按 Stage 2 后续工作包加入；
- bridge 接受 schema 1/2/3，但类型表只解释当前后端能映射的内置标量；复合类型的执行支持随后续工作包扩展。
