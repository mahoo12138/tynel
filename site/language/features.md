# 语言特性总览

本页逐项列出首版编译器对 TypeScript 写法的处理结果，与仓库 `docs/reference/support.md` 的操作支持表一致。

状态含义：

- **支持**：有源码夹具和验证记录，可以编译并运行。
- **拒绝**：能力检查或 TypeScript 会明确报错，附错误码。
- **计划**：尚未实现，不能当成可用。

「TypeScript 合法」不等于「Tynel 支持」：类型检查之后还有一道原生能力白名单，越界写法按能力限制退出 3。两道检查的分工见[诊断与错误码](/reference/diagnostics)。

## 值与类型

| 写法 | 状态 | 说明 |
| --- | --- | --- |
| `number` | 支持 | binary64 双精度；字面量在 NIR 里保存 64 位位模式 |
| `boolean` | 支持 | `true` / `false`，可作参数、返回值与输出 |
| `void` | 支持 | 只用于函数返回类型与调用语句 |
| `type X = number \| boolean` | 支持 | 非递归别名，只活在类型空间 |
| `string` | 拒绝 | `cap.bad-type` 或 `cap.unsupported-syntax` |
| 对象、数组、元组 | 拒绝 | `cap.unsupported-syntax` |
| `any`、`unknown` | 拒绝 | `cap.any` |
| 联合类型 | 拒绝 | `cap.bad-type` |
| 接口 | 拒绝 | `cap.interface` |

## 声明

| 写法 | 状态 | 说明 |
| --- | --- | --- |
| 顶层具名函数 | 支持 | 直接调用；不能占用固定入口名 `main`（`cap.entry-name`） |
| 必填参数 | 支持 | `number` 或 `boolean` |
| 默认参数、可选参数 | 拒绝 | `cap.unsupported-parameter` |
| 顶层 `const` | 支持 | 初始化表达式按源码顺序求值 |
| 块内 `const` | 支持 | 只在当前块可见，同名遮蔽外层 |
| 块内 `let`（带初值） | 支持 | 可变 `let` 可简单赋值 |
| 未初始化的 `let`、`var` | 拒绝 | `cap.unsupported-syntax` |
| 模块级 `let` | 拒绝 | `cap.module-let` |
| 函数读取模块级 `const` | 拒绝 | `module.captured-variable` |
| 匿名、默认导出、嵌套函数 | 拒绝 | `cap.unsupported-syntax` |
| 闭包 | 拒绝 | `cap.unsupported-syntax` |

## 表达式

| 写法 | 状态 | 说明 |
| --- | --- | --- |
| 数字、布尔字面量 | 支持 | 数字不做十进制重解析 |
| `+` `-` `*` `/` | 支持 | 两个 `number`；保持求值顺序与 binary64 规则 |
| 一元 `-` `+` | 部分支持 | 只接受数字字面量；`-x` 变量取负报 `cap.bad-unary` |
| `!` | 支持 | 操作数必须是 `boolean`；`!1` 拒绝 |
| `<` `<=` `>` `>=` | 支持 | 两个 `number`；布尔没有原生顺序 |
| `===` `!==` | 支持 | 两侧同为 `number` 或同为 `boolean` |
| `==` `!=` | 拒绝 | `cap.bad-operator`，不做隐式转换 |
| `&&` `\|\|` | 支持 | 两侧 `boolean`，短路求值；被短路的一侧仍接受检查 |
| `? :` | 支持 | 条件 `boolean`，两臂同类；未选中的分支仍接受检查 |
| 取余 `%`、位运算 | 拒绝 | `cap.bad-operator`，未纳入首版 |
| 字符串拼接、模板字符串 | 拒绝 | 字符串不在子集内 |
| 赋值表达式 | 拒绝 | `cap.assignment-value` |

## 语句与控制流

| 写法 | 状态 | 说明 |
| --- | --- | --- |
| `return` | 支持 | 有值函数的每条路径都要结束 |
| `if` / `else if` / `else` | 支持 | 条件 `boolean`，分支各自一个作用域 |
| `while` | 支持 | 条件每轮重新求值，可能零次执行 |
| `break` / `continue` | 支持 | 无标签，只作用于最近循环 |
| 带标签跳转 | 拒绝 | `cap.labeled-jump` |
| 简单赋值 `x = value;` | 支持 | 目标必须是可变 `let`，右值先求值再写入 |
| 复合赋值、`++` / `--` | 拒绝 | `cap.compound-assignment` / `cap.inc-dec` |
| 给 `const`、参数赋值 | 拒绝 | `cap.assign-immutable` |
| 表达式语句 | 支持 | 调用保留副作用，裸值求值后丢弃 |
| `console.log(x)` | 支持 | 恰好一个 `number` 或 `boolean` 实参；核对符号来源 |
| `for`、`do` | 计划 / 拒绝 | `for` 未排入当前节点；`do` 报 `cap.unsupported-syntax` |
| `switch`、`try`、`throw` | 拒绝 | `cap.unsupported-syntax` |
| 顶层 `if`、顶层语句块 | 拒绝 | 模块体只接受声明、`const` 与表达式语句 |

## 模块

| 写法 | 状态 | 说明 |
| --- | --- | --- |
| 命名导出 | 支持 | `export function` 与顶层 `export const` |
| 值导入 `import { x }` | 支持 | 可重命名，绑定到导出声明的完整身份 |
| `import type` | 支持 | 只带类型名字，不产生运行时值 |
| 显式相对 `.ts` 路径 | 支持 | 大小写精确，符号链接不越界 |
| 无环依赖图 | 支持 | 环按 `graph.cycle` 拒绝 |
| 模块初始化 | 支持 | 按源码导入顺序深度优先，共享依赖只执行一次 |
| 默认导入、命名空间导入、再导出 | 拒绝 | 能力检查或 TypeScript 诊断 |
| 裸模块、CommonJS、动态导入 | 拒绝 | 不在子集内 |

## 构建与运行

| 能力 | 状态 | 说明 |
| --- | --- | --- |
| `check` / `build` / `run` / `version` | 支持 | 命令契约见[命令行 API](/reference/cli) |
| `dump` / `build-ir` | 支持 | 开发调试接口 |
| 多文件合并 | 支持 | 全部模块合成一个 NIR 与一个原生程序 |
| 构建记录与哈希链 | 支持 | 源码、依赖、NIR、工具链、产物逐级可核对 |
| 构建事务 | 支持 | 失败不覆盖旧产物；同输出并发按 `build.output-busy` 拒绝 |
| `darwin/arm64` | 支持 | 唯一已验证目标 |
| `windows/amd64`、交叉编译 | 计划 | `--target` 其他值退出 3 |

## 不在首版范围内

字符串、对象、数组、接口记录、通用泛型、联合类型、闭包、类、异常、异步、npm、动态导入与任意 Go 库绑定都不属于首版完成条件。`any`、不安全断言（`cap.type-escape`）、`eval`（`cap.forbidden-global`）不提供绕过检查的通道。

完整错误码表见[诊断与错误码](/reference/diagnostics)；后续方向见[路线图与边界](/project/roadmap)。