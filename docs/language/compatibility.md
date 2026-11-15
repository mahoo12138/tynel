# TypeScript 兼容性矩阵

> 文档版本：0.1  
> 修订日期：2026-11-15  
> 当前里程碑：Tynel Language Preview 0（`mvp/06-compiler-mvp`，2026-11-09）  
> 语法基线：`microsoft/typescript-go` 标签 `typescript/v7.0.2`，提交 `2bd066d8…`  
> 关联文档：[语言宪章](../../LANGUAGE.md)、[长期路线图](../../ROADMAP.md)、[操作支持表](../reference/support.md)

## 1. 读法

本表分别记录语法、类型和运行时三层状态，避免把「Parser 能解析」误写成「语言已支持」。

| 列 | 取值 | 含义 |
| --- | --- | --- |
| 语法 | `tsgo` | 锁定前端能按 TypeScript 语法解析 |
| 语法 | `未验证` | 尚未逐项验证基线前端对该写法的处理 |
| 语法 | `—` | 不属于语法层，例如包解析或平台目标 |
| 类型 | `tsgo` | 当前由 TypeScript Checker 给出类型结论，Tynel 尚未定义独立规则 |
| 类型 | `Tynel` | Tynel 已定义类型或能力规则 |
| 类型 | `拒绝` | Tynel 在类型或能力层明确拒绝 |
| 类型 | `未定义` | 尚无 Tynel 规则，不能从实现推定 |
| 运行时 | `已验证` | 有原生运行证据 |
| 运行时 | `拒绝` | 编译期明确拒绝，不生成程序 |
| 运行时 | `未实现` | 没有原生表示或执行路径 |
| 运行时 | `未定义` | 语义尚未决定 |

类型列的 `拒绝` 表示 Tynel 在类型或能力层拒绝该类型构造；`tsgo` 表示类型检查本身由前端接受，阻塞发生在运行时或能力层。

状态词汇沿用[语言宪章](../../LANGUAGE.md)：

| 状态 | 含义 |
| --- | --- |
| `supported` | 语法、Tynel 类型规则和原生运行时语义均通过验收 |
| `syntax-only` | 能识别语法，尚不能生成可执行程序，也没有稳定拒绝路径 |
| `planned` | 已确定方向，尚未实现 |
| `rejected` | 当前明确拒绝，诊断稳定 |
| `deferred` | 技术方向未定，等待 RFC |
| `not-targeted` | 明确不属于当前语言目标 |

一个能力可以同时「当前 `rejected`」和「长期 `planned`」。`planned` 和 `deferred` 行如果当前不在子集内，编译器仍会拒绝；这两个状态描述长期方向，不表示今天可以编译。`rejected` 行表示当前有明确拒绝路径，长期方向可能是 `planned`、`deferred` 或 `not-targeted`。本表不使用「部分支持」；必须说明卡在哪一层、哪些写法不支持。

Stage 2 的[字符串 RFC](semantics/strings.md)、[对象与数组 RFC](semantics/objects-arrays.md)、[联合与空值 RFC](semantics/unions-null.md)、[函数 RFC](semantics/functions.md)、[泛型 RFC](semantics/generics.md)以及 [NIR schema 3 类型表设计](../design/nir-schema-3-types.md)已经确认。字符串最小闭环（字面量、拼接、`length`、严格相等和输出）已实现并有原生回归；索引、`slice`、模板插值和字符串方法仍未实现。对象第一个切片（必填属性、字面量、字段读取和引用身份）也已实现，可选属性、字段写入和结构性放宽仍拒绝。数组与元组第一个切片（字面量、索引、`length`、常量元组索引）已实现，数组方法、展开、赋值和身份比较仍拒绝。可空联合第一个切片（`T | null` / `T | undefined`、空值比较和前端缩窄）已实现；多成员联合、`??` / `?.` 和可选属性仍拒绝。其他 RFC 不改变本表当前的 `rejected` 状态；只有实现并通过运行时验收后，状态才会变化。

## 2. 基线

| 项目 | 当前值 |
| --- | --- |
| 语法与诊断基线 | `typescript-go`，`typescript/v7.0.2`，提交 `2bd066d8…` |
| Go 工具链 | `go1.27.3` |
| 原生目标 | `darwin/arm64` |
| 用户命令 | `version`、`check`、`build`、`run` |
| 开发接口 | `dump`、`build-ir`、`toolchain` |

语法基线表示 Tynel 使用该版本的 TypeScript 语法和诊断，不等于承诺 TypeScript 7.0 的全部类型规则或 JavaScript 运行时行为。上游升级流程见[语言宪章](../../LANGUAGE.md)与[前端适配](../reference/frontend.md)。

## 3. 值与类型

| 能力 | 语法 | 类型 | 运行时 | 状态 | 说明与后续 |
| --- | --- | --- | --- | --- | --- |
| `number` | `tsgo` | `Tynel` | `已验证` | `supported` | binary64；负零、NaN、无穷和求值顺序有验证记录。见[比较语义](../reference/comparison.md) |
| `boolean` | `tsgo` | `Tynel` | `已验证` | `supported` | `true` / `false`，不做数字真假转换。见[03.01 验收](../validation/03-01-boolean.md) |
| `void` | `tsgo` | `Tynel` | `已验证` | `supported` | 只用于函数返回类型和调用语句。见[输出格式](../reference/output.md) |
| `type X = number` / `boolean` | `tsgo` | `Tynel` | `已验证` | `supported` | 非递归别名，只活在类型空间。见[类型别名](../reference/type-aliases.md) |
| `string`：字面量、`+` 拼接、`length`、`===` / `!==`、输出 | `tsgo` | `Tynel` | `已验证` | `supported` | UTF-16 code unit 语义；ASCII、中文、emoji 与空字符串有原生输出。见[字符串 RFC](semantics/strings.md) |
| `string`：索引、`slice`、模板插值、方法 | `tsgo` | `Tynel` | `拒绝` | `rejected` | 索引、`slice`、模板插值和字符串方法均未实现；`undefined` 表示已由 S2.4 可空联合落地 |
| 静态对象：必填属性、字面量、字段读取、引用相等 | `tsgo` | `Tynel` | `已验证` | `supported` | 静态形状布局；对象 `===` 比较引用身份；嵌套对象可用。见[对象与数组 RFC](semantics/objects-arrays.md) |
| 对象：可选属性、字段写入、结构性放宽 | `tsgo` | `Tynel` | `拒绝` | `rejected` | 可选属性尚未实现；`undefined` 表示已由 S2.4 落地；字段写入属于第二个对象切片；`cap.object-shape` |
| `interface`（对象形状） | `tsgo` | `Tynel` | `已验证` | `supported` | 接口展开成静态对象形状；非对象接口仍按 `cap.interface` 拒绝 |
| 数组与元组：字面量、索引、`length`、常量索引 | `tsgo` | `Tynel` | `已验证` | `supported` | 同质数组与定长元组；索引越界按数据错误退出 6，元组越界在编译期拒绝。见[对象与数组 RFC](semantics/objects-arrays.md) |
| 数组：方法、展开、赋值、扩容、身份比较 | `tsgo` | `Tynel` | `拒绝` | `rejected` | `push` / `slice` 留到标准库阶段；`[...xs]`、`xs === xs` 暂时拒绝 |
| 可空联合：`T \| null`、`T \| undefined` | `tsgo` | `Tynel` | `已验证` | `supported` | 空值比较与前端缩窄；多成员联合、`??` / `?.` 仍未实现 |
| 多成员联合与判别联合 | `tsgo` | `Tynel` | `拒绝` | `rejected` | 需要比单成员可空联合更广的标签与缩窄规则；`cap.bad-type` |
| 字面量类型 | `tsgo` | `未定义` | `未实现` | `planned` | 阶段 3；需要与类型收窄一起设计 |
| `any` / `unknown` | `tsgo` | `拒绝` | `拒绝` | `rejected` | `cap.any`；不作为原生表示目标，是否提供受控动态类型需另行 RFC |
| `enum` | `tsgo` | `未定义` | `未实现` | `deferred` | 需要先决定编译期常量还是运行时对象，阶段 3B |
| 泛型 | `tsgo` | `未定义` | `拒绝` | `rejected` | 通用泛型待实现策略 RFC（擦除、单态化或字典），阶段 3B |
| `class` | `tsgo` | `未定义` | `拒绝` | `rejected` | `cap.unsupported-syntax`；需要对象模型和构造语义，阶段 3C |
| 类型断言、`as`、`satisfies`、非空断言 | `tsgo` | `拒绝` | `拒绝` | `rejected` | `cap.type-escape`；保留诊断，不作为绕过检查的通道 |
| `bigint` | `tsgo` | `未定义` | `未实现` | `deferred` | 定宽整数与 `bigint` 尚未排期 |

## 4. 声明与函数

| 能力 | 语法 | 类型 | 运行时 | 状态 | 说明与后续 |
| --- | --- | --- | --- | --- | --- |
| 顶层具名函数声明 | `tsgo` | `Tynel` | `已验证` | `supported` | 不能占用固定入口名 `main`。见[能力白名单](../reference/capability.md) |
| 必填参数与返回值 | `tsgo` | `Tynel` | `已验证` | `supported` | 参数限 `number` / `boolean` / `string` / 静态对象 / 数组 / 元组 / 可空联合，返回再加 `void` |
| 直接递归与相互递归 | `tsgo` | `Tynel` | `已验证` | `supported` | 见[递归与调用层](../reference/recursion.md) |
| 顶层 `const` | `tsgo` | `Tynel` | `已验证` | `supported` | 初始化表达式按源码顺序求值 |
| 块内 `const` / 带初值 `let` | `tsgo` | `Tynel` | `已验证` | `supported` | 块级作用域，同名遮蔽。见[声明与可变存储](../reference/mutability.md) |
| 模块级 `let` | `tsgo` | `拒绝` | `拒绝` | `rejected` | `cap.module-let`；模块变量捕获需单独设计 |
| 函数读取模块级 `const` | `tsgo` | `拒绝` | `拒绝` | `rejected` | `module.captured-variable`；当前函数只能读取参数和局部值 |
| 默认参数、可选参数 | `tsgo` | `拒绝` | `拒绝` | `rejected` | `cap.unsupported-parameter`；阶段 3A 计划 |
| 剩余参数 | `tsgo` | `未定义` | `未实现` | `planned` | 阶段 3A；依赖数组与调用约定 |
| 箭头函数、函数表达式 | `tsgo` | `未定义` | `拒绝` | `rejected` | `cap.unsupported-syntax`；阶段 3A 计划 |
| 函数类型与回调 | `tsgo` | `未定义` | `未实现` | `planned` | 阶段 3A；需要函数值与调用约定 |
| 闭包、嵌套函数 | `tsgo` | `未定义` | `拒绝` | `rejected` | `cap.unsupported-syntax`；阶段 3B 计划 |
| 函数重载 | `tsgo` | `未定义` | `未实现` | `planned` | 阶段 3B；需要声明与实现的匹配规则 |
| 匿名函数与默认导出函数 | `tsgo` | `拒绝` | `拒绝` | `rejected` | `cap.unsupported-syntax` |
| `declare` / ambient 声明 | `tsgo` | `拒绝` | `拒绝` | `rejected` | `cap.unsupported-declaration` |
| 类声明 | `tsgo` | `未定义` | `拒绝` | `rejected` | `cap.unsupported-syntax`；阶段 3C |
| 命名空间与模块增强 | `tsgo` | `未定义` | `未实现` | `deferred` | 需要先确定模块与声明文件策略 |
| 装饰器 | `tsgo` | `未定义` | `未实现` | `deferred` | 仅在确定编译期或运行时语义后考虑 |

## 5. 表达式

| 能力 | 语法 | 类型 | 运行时 | 状态 | 说明与后续 |
| --- | --- | --- | --- | --- | --- |
| 数字、布尔字面量 | `tsgo` | `Tynel` | `已验证` | `supported` | 数字不做十进制重解析，NIR 保存位模式 |
| `+` `-` `*` `/` | `tsgo` | `Tynel` | `已验证` | `supported` | 两侧 `number`，保持求值顺序与 binary64 规则 |
| 一元 `-` / `+` | `tsgo` | `Tynel` | `已验证` | `supported` | 只接受数字字面量并折成位模式；变量取负见[操作支持表](../reference/support.md) |
| 变量取负 `-x` | `tsgo` | `拒绝` | `拒绝` | `rejected` | `cap.bad-unary`；需要新增取负操作，后续节点处理 |
| 取余 `%`、位运算 | `tsgo` | `拒绝` | `拒绝` | `rejected` | `cap.bad-operator`；未纳入 Preview 0 |
| `!` | `tsgo` | `Tynel` | `已验证` | `supported` | 操作数必须是 `boolean`；`!1` 拒绝 |
| `<` `<=` `>` `>=` | `tsgo` | `Tynel` | `已验证` | `supported` | 只接受两个 `number`；布尔没有原生顺序 |
| `===` `!==` | `tsgo` | `Tynel` | `已验证` | `supported` | 两侧同为 `number`、`boolean`、`string` 或同一对象形状；对象比较引用身份；可空联合可与 `null` / `undefined` 比较；数组和元组暂不支持 |
| `==` `!=` | `tsgo` | `拒绝` | `拒绝` | `rejected` | `cap.bad-operator`；有意不做隐式转换 |
| `&&` / `\|\|` | `tsgo` | `Tynel` | `已验证` | `supported` | 两侧 `boolean`，短路求值。见[短路求值](../reference/short-circuit.md) |
| `??` 与可选链 `?.` | `tsgo` | `Tynel` | `拒绝` | `rejected` | 依赖多成员联合与可选属性，留到后续切片 |
| 三元式 `? :` | `tsgo` | `Tynel` | `已验证` | `supported` | 条件 `boolean`，两臂同为标量、同一对象形状、同一数组或同一元组；联合两臂仍未支持。见[三元式](../reference/ternary.md) |
| 字符串字面量与无插值模板 | `tsgo` | `Tynel` | `已验证` | `supported` | UTF-16 code unit 存储；插值、索引、切片和方法仍拒绝 |
| 对象字面量（静态形状） | `tsgo` | `Tynel` | `已验证` | `supported` | 必填属性、字段读取与引用身份；可选属性、字段写入和结构性放宽拒绝 |
| 数组与元组字面量 | `tsgo` | `Tynel` | `已验证` | `supported` | 同质数组、定长元组；数组展开与空槽拒绝 |
| 属性访问与索引访问 | `tsgo` | `Tynel` | `已验证` | `supported` | 对象字段、`string.length`、数组 `length` 与数组/元组索引可用；字符串索引和其他属性仍拒绝 |
| 直接函数调用 | `tsgo` | `Tynel` | `已验证` | `supported` | 实参个数与类型必须匹配 |
| `new` 表达式 | `tsgo` | `未定义` | `拒绝` | `rejected` | `cap.unsupported-syntax`；与类一起设计 |
| JSX / TSX | `未验证` | `未定义` | `未实现` | `deferred` | 视未来应用方向决定，不作为语法兼容的默认优先项 |
| 赋值表达式 | `tsgo` | `拒绝` | `拒绝` | `rejected` | `cap.assignment-value`；赋值只作为独立语句 |
| 复合赋值、`++` / `--` | `tsgo` | `拒绝` | `拒绝` | `rejected` | `cap.compound-assignment` / `cap.inc-dec` |
| 解构赋值 | `tsgo` | `未定义` | `未实现` | `planned` | 阶段 3B |
| rest / spread | `tsgo` | `未定义` | `未实现` | `planned` | 阶段 3B；依赖数组与对象模型 |
| `typeof` / `in` / `instanceof` | `tsgo` | `未定义` | `未实现` | `planned` | 阶段 3B；需要类型守卫与运行时类型信息 |
| `eval` 与动态 `Function` | `tsgo` | `拒绝` | `拒绝` | `not-targeted` | `cap.forbidden-global`；不提供绕过检查的通道 |

## 6. 语句与控制流

| 能力 | 语法 | 类型 | 运行时 | 状态 | 说明与后续 |
| --- | --- | --- | --- | --- | --- |
| 语句块 | `tsgo` | `Tynel` | `已验证` | `supported` | 只在函数体或分支内出现 |
| 表达式语句 | `tsgo` | `Tynel` | `已验证` | `supported` | 调用保留副作用，裸值求值后丢弃 |
| `return` | `tsgo` | `Tynel` | `已验证` | `supported` | 有值函数的每条路径都要结束。见[返回路径](../reference/returns.md) |
| `if` / `else if` / `else` | `tsgo` | `Tynel` | `已验证` | `supported` | 条件 `boolean`，分支各自一个作用域 |
| `while` | `tsgo` | `Tynel` | `已验证` | `supported` | 条件每轮重新检查，可能零次执行 |
| `break` / `continue` | `tsgo` | `Tynel` | `已验证` | `supported` | 无标签，只作用于最近循环 |
| 带标签跳转 | `tsgo` | `拒绝` | `拒绝` | `rejected` | `cap.labeled-jump` |
| `for` | `tsgo` | `拒绝` | `拒绝` | `rejected` | 当前按 `cap.unsupported-syntax` 拒绝；阶段 3A 计划 |
| `for...of` | `tsgo` | `未定义` | `未实现` | `planned` | 阶段 3A；依赖数组与迭代协议 |
| `for...in` | `tsgo` | `未定义` | `未实现` | `deferred` | 依赖对象属性枚举语义 |
| `do...while` | `tsgo` | `拒绝` | `拒绝` | `rejected` | 不在当前子集内 |
| `switch` | `tsgo` | `未定义` | `拒绝` | `rejected` | `cap.unsupported-syntax`；需要与类型收窄一起设计 |
| `try` / `catch` / `finally` / `throw` | `tsgo` | `未定义` | `拒绝` | `rejected` | 异常或统一错误模型未定，见[语义索引](semantics/README.md) |
| `async` / `await` | `tsgo` | `未定义` | `未实现` | `planned` | 阶段 3A 之后；依赖 Promise 与调度模型 |
| 生成器与 `yield` | `tsgo` | `未定义` | `未实现` | `deferred` | 需要协程或状态机语义 |
| 顶层 `if`、顶层语句块 | `tsgo` | `拒绝` | `拒绝` | `rejected` | 模块体只接受声明、`const` 与表达式语句 |
| `debugger` | `tsgo` | `未定义` | `未实现` | `deferred` | 调试器阶段再决定 |
| `with` | `tsgo` | `拒绝` | `拒绝` | `not-targeted` | 不进入严格语义设计 |

## 7. 模块

| 能力 | 语法 | 类型 | 运行时 | 状态 | 说明与后续 |
| --- | --- | --- | --- | --- | --- |
| 命名导出 | `tsgo` | `Tynel` | `已验证` | `supported` | `export function` 与顶层 `export const` |
| 命名导入 | `tsgo` | `Tynel` | `已验证` | `supported` | 可重命名，绑定到导出声明的完整身份 |
| 副作用导入 | `tsgo` | `Tynel` | `已验证` | `supported` | 只建立依赖边，目标模块按初始化顺序执行 |
| `import type` | `tsgo` | `Tynel` | `已验证` | `supported` | 只进入类型边，不产生运行时值 |
| 显式相对 `.ts` 路径 | `tsgo` | `Tynel` | `已验证` | `supported` | 大小写精确，符号链接不越界。见[项目根与路径解析](../reference/project-paths.md) |
| 无环依赖图 | `tsgo` | `Tynel` | `已验证` | `supported` | 环按 `graph.cycle` 拒绝 |
| 模块初始化顺序 | `tsgo` | `Tynel` | `已验证` | `supported` | 按源码导入顺序深度优先，共享依赖只执行一次。见[初始化顺序](../reference/init-order.md) |
| 默认导入与默认导出 | `tsgo` | `拒绝` | `拒绝` | `rejected` | `cap.unsupported-syntax` |
| 命名空间导入 | `tsgo` | `拒绝` | `拒绝` | `rejected` | `cap.unsupported-syntax` |
| 再导出 `export ... from` | `tsgo` | `拒绝` | `拒绝` | `rejected` | 当前不在子集内 |
| 动态 `import()` | `tsgo` | `未定义` | `未实现` | `deferred` | 涉及代码分割与运行时模块加载，长期再评估 |
| 裸模块说明符 | `tsgo` | `拒绝` | `拒绝` | `rejected` | 当前解析器只接受根内显式相对路径 |
| npm 包解析 | `—` | `未定义` | `未实现` | `not-targeted` | 见[路线图](../../ROADMAP.md)第 10 节，不作为近期目标 |
| JSON 模块 | `tsgo` | `未定义` | `未实现` | `not-targeted` | 不在当前语言目标内 |
| `import.meta` | `tsgo` | `未定义` | `未实现` | `deferred` | 需要模块运行时元数据 |
| CommonJS `require` | `—` | `未定义` | `未实现` | `not-targeted` | 不承诺 Node 模块语义 |

## 8. 标准库与运行时

| 能力 | 语法 | 类型 | 运行时 | 状态 | 说明与后续 |
| --- | --- | --- | --- | --- | --- |
| `console.log`（一个 `number` 或 `boolean` 参数） | `tsgo` | `Tynel` | `已验证` | `supported` | 恰好一个参数，核对内置 `console` 来源。见[能力白名单](../reference/capability.md) |
| 全局 `isFinite`、`NaN`、`Infinity` 等 | `tsgo` | `拒绝` | `拒绝` | `rejected` | `cap.unknown-callee` / `cap.unknown-name` |
| `Math` 与内置对象 | `tsgo` | `未定义` | `未实现` | `planned` | 标准库阶段以 `@tynel/math` 等形式提供 |
| Node.js API | `—` | `未定义` | `未实现` | `not-targeted` | 不承诺 Node 兼容 |
| `@tynel/*` 标准库 | `—` | `未定义` | `未实现` | `planned` | 阶段 4；公共 API 由 Tynel 定义 |
| 语言级异常 | `tsgo` | `未定义` | `未定义` | `deferred` | 需要决定异常、`Result` 或两者并存 |
| Promise 与异步调度 | `tsgo` | `未定义` | `未定义` | `deferred` | 需要事件循环或调度器契约 |
| 用户态并发 | `—` | `未定义` | `未定义` | `deferred` | Go 调度器只是实现基座，公共语义待 RFC |
| GC 与内存管理 | `—` | `未定义` | `已验证`（实现层） | `deferred` | Preview 0 借用 Go Runtime 作为基座；长期对象布局、GC 契约与 ABI 尚未定义 |
| FFI 与 Native ABI | `—` | `未定义` | `未实现` | `planned` | 阶段 4；使用稳定 native ID，不暴露 Go 符号 |

## 9. 工具与平台

| 能力 | 状态 | 说明 |
| --- | --- | --- |
| `version` / `check` / `build` / `run` | `supported` | 命令契约见[命令行参考](../reference/cli.md) |
| `dump facts` / `nir` / `graph` / `modules` / `init` | `supported` | 开发调试接口 |
| `build-ir` 与 NIR 回放 | `supported` | 见[NIR 到机器码](../reference/bridge.md) |
| 构建身份与构建事务 | `supported` | 见[构建身份](../reference/build-identity.md)、[构建事务](../reference/build-transactions.md) |
| `darwin/arm64` | `supported` | 唯一已验证目标 |
| `windows/amd64` | `planned` | 保留在延迟目标列表，`--target` 当前拒绝 |
| `linux/amd64`、`linux/arm64` | `deferred` | 尚未排期 |
| LSP、Formatter、Test Runner | `planned` | 阶段 6 |
| Debugger、Profiler | `planned` | 阶段 6 |
| Package Manager | `planned` | 阶段 6；npm 兼容不是前置条件 |

## 10. 维护规则

1. 能力状态变化时，同时更新本表、[操作支持表](../reference/support.md)与相关验证记录。
2. 新增 `supported` 行必须有正向、负向、边界和组合用例，不能只凭 Parser 通过。
3. 新增 `rejected` 行必须给出稳定错误码；没有稳定诊断的写法标为 `deferred` 或 `planned`，不能写成 `rejected`。
4. 升级 TypeScript 或 Go 基线后，先运行语法与诊断回归，再修改本表。
5. 本表记录当前状态，不替代[长期路线图](../../ROADMAP.md)的阶段计划；路线图调整不自动改变本表状态。
