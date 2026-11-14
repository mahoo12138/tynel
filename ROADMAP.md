# Tynel 长期发展路线图

> 文档版本：0.1  
> 规划日期：2026-11-11  
> 当前基线：MVP 已完成（`darwin/arm64`）  
> 文档性质：长期技术路线与阶段验收框架，不是对发布日期的承诺

---

## 1. 项目愿景

Tynel 的长期目标，是成为一门**以 TypeScript 语法为长期语言入口、拥有独立语言规范和运行时体系、面向原生编译，并最终能够自举的编程语言**。

Tynel 不只是一个将 TypeScript 子集转换成 Go 程序的编译器，也不只是一个 Go 编译器的前端适配层。Go 当前承担的是基础设施角色：它为 Tynel 提供已经成熟的编译、代码生成、链接、运行时和平台适配能力。随着 Tynel 的语言能力扩大，这些基础设施将逐步被纳入 Tynel 自己的维护和演进体系，并针对 TypeScript 语法及 Tynel 的语言语义进行改造。

长期方向可以概括为：

**TypeScript Syntax → Tynel Language Semantics → Tynel IR → Tynel-maintained Toolchain → Native Executable → Self-hosting**

这里的“独立语言”主要指语言规范、类型规则、运行时契约、标准库和工具链演进由 Tynel 自己决定；它不要求立即抛弃 Go，也不要求所有底层组件都从零重写。Go 源码和成熟组件可以长期作为 Tynel 工具链的技术来源，关键是不能让 Go 的语言模型反过来决定 Tynel 的公共语言设计。

## 2. 长期设计原则

### 2.1 TypeScript 语法是长期承诺，不是过渡阶段

Tynel 应持续以 TypeScript 语法作为开发者编写程序的主要方式。后续增加字符串、数组、对象、泛型、函数类型、类、异步等能力时，优先沿用 TypeScript 已有的语法表达，不为了方便后端实现而另造一套不兼容的表层语法。

对于尚未实现的 TypeScript 语法或语言特性，应给出明确的编译期诊断，并在兼容性矩阵中标记状态；不应悄悄改变源码含义，也不应把当前不支持误写成永久不支持。

### 2.2 语法兼容、类型系统兼容和运行时兼容必须分开管理

“兼容 TypeScript”至少包含三个不同层次，路线图必须分别记录：

| 兼容层次 | 含义 | Tynel 的目标 |
| --- | --- | --- |
| 语法兼容 | 能识别 TypeScript 的词法、语法结构和类型语法 | 长期目标。以明确的 TypeScript 版本为语法基线，持续扩大覆盖面 |
| 类型系统兼容 | 对接口、类型别名、联合、泛型、结构类型等给出类型检查结果 | 分阶段实现。以 Tynel 语言规范为准，不要求一开始复刻 TypeScript Checker 的全部规则 |
| 运行时语义兼容 | 程序执行时的数值、字符串、对象、异常、求值顺序等行为与 JavaScript/TypeScript 生态一致 | 按特性逐项决定。明确兼容的行为和有意不同的行为，不默认承诺完整 JavaScript 兼容 |

TypeScript 本身主要定义类型语法和类型检查，并将运行时行为交给 JavaScript。Tynel 则需要把部分运行时行为变成自己的语言规范。因此，语法可以保持熟悉，运行时却必须有明确、可测试的 Tynel 定义。

### 2.3 Tynel 拥有自己的语言语义

前端可以借助 TypeScript Parser、Type Checker 或 `typescript-go`，但它们不能成为 Tynel 语义的最终裁判。Tynel 必须拥有自己的语义层，明确规定：

- 类型之间的关系、可赋值规则和类型推断边界；
- 表达式求值顺序、短路行为、控制流与作用域；
- `number`、`string`、`boolean`、数组、对象等值类型的表示和行为；
- 函数调用、闭包、模块初始化、异常和异步执行规则；
- 内存分配、对象身份、可变性、垃圾回收及跨语言调用边界。

上游 TypeScript 工具可以提供语法树和类型事实，但最终必须经过 Tynel 自己的校验、规范化和中间表示生成。

### 2.4 中间表示和 ABI 不依赖 Go 内部对象

NIR 及其后续版本应继续作为 Tynel 编译器与后端之间的稳定边界。NIR 不应直接保存 Go `ir.Node`、Go 类型对象、Go 符号名或 Go 内存布局。

Go Backend 可以把 NIR 转换为 Go 编译器 IR，但这种转换只属于后端实现。未来即使调整 Go 编译器内部结构、增加新的后端或重做 Runtime，也不应该迫使前端和语言规范跟着修改。

### 2.5 Go 是基础设施来源，不是语言边界

当前 Go 编译器、链接器和 Runtime 的成熟能力值得继续利用。后续可以维护 Tynel 自己的 Go Toolchain Fork，逐步修改编译器、运行时、链接器和平台适配逻辑。

但不能把 Go 的公共语言 API 当作 Tynel 的标准库 API。例如，Tynel 的文件、网络、时间和并发 API 不应直接暴露 `io.Reader`、`os.File`、`context.Context`、goroutine 或 channel 等 Go 语言概念。底层可以借用 Go 实现，公共契约由 Tynel 定义。

### 2.6 自举是阶段性验收目标，不是单一口号

自举应分为编译器核心自举、完整工具链自举两个层次。编译器主要实现可以由 Tynel 编写，不代表汇编器、链接器、系统库和平台 SDK 也必须由 Tynel 重写。

是否自举成功，需要通过 Bootstrap 过程、连续编译结果、测试一致性和干净环境重建来判断，而不是仅仅因为仓库里出现了用 Tynel 编写的编译器代码。

---

## 3. 当前工程基线

截至 2026-11-11，仓库已经完成首版编译器 MVP。它是后续语言工程的起点，不应被误认为最终语言边界。

### 3.1 已经建立的能力

- 以 TypeScript 源文件作为输入，使用 `typescript-go` 前端能力解析和检查源码，并通过 Tynel 自己的 Facts、Capability 和 Lowering 流程处理程序。
- 建立了独立 NIR，可序列化、回放和验证；NIR 不依赖 Go 编译器内部对象。
- 已支持 `number`、`boolean`、`void`，顶层具名函数、函数调用和递归、`const` / `let`、局部赋值、`if/else`、短路逻辑、三元表达式、`while`、`break/continue`。
- 已支持显式相对 `.ts` 模块、命名导入导出、类型别名及 `import type`，并建立模块依赖图和初始化顺序。
- 通过修改版 Go 编译器将 NIR 直接构造成 Go IR，再使用 Go 工具链完成目标代码生成和链接；没有为用户业务代码生成 `.go` 源文件，也没有嵌入 JavaScript 引擎。
- 已交付 `version`、`check`、`build`、`run` 等主要命令，以及构建身份、输入快照、构建事务和发行目录机制。
- 当前经过完整验收的原生目标为 `darwin/arm64`。

### 3.2 当前明确的边界

当前 MVP 不支持字符串、对象、数组、泛型、类、闭包、异常、异步、npm 包解析和任意 Go 包导入。模块解析主要面向项目根内的显式相对 `.ts` 导入。用户程序仍不能把 Go 库当成自己的语言 API 使用。

这些限制是 MVP 的范围，不应自动继承为 Tynel 的长期边界。后续需要逐项规划语言语义、表示方式、IR、Runtime 和测试，而不是简单放开前端白名单。

### 3.3 当前架构位置

```text
TypeScript 源码
      │
      ▼
typescript-go / Tynel 前端适配
      │
      ▼
Facts + Capability + Lowering
      │
      ▼
NIR（独立、可验证、可序列化）
      │
      ▼
Go Bridge（构造 Go IR）
      │
      ▼
修改版 Go Compiler + Go Linker + Go Runtime
      │
      ▼
Native Executable
```

现有架构已经有一个很重要的基础：Tynel NIR 与 Go 内部 IR 分离。后续应继续强化这个边界，而不是绕过它直接在前端生成 Go IR。

参考：

- [README.md](https://github.com/mahoo12138/tynel/blob/main/README.md)
- [SPEC.md](https://github.com/mahoo12138/tynel/blob/main/SPEC.md)
- [PLAN.md](https://github.com/mahoo12138/tynel/blob/main/PLAN.md)
- [当前路线图](https://github.com/mahoo12138/tynel/blob/main/site/project/roadmap.md)
- [Go 桥接与运行时](https://github.com/mahoo12138/tynel/blob/main/site/internals/go-bridge.md)
- [NIR 到机器码](https://github.com/mahoo12138/tynel/blob/main/docs/reference/bridge.md)

---

## 4. 目标架构

长期架构建议逐步演进为以下结构：

```text
                       Tynel Source Code (.ts)
                                  │
                                  ▼
                 TypeScript-compatible Frontend
               Parser / AST / Syntax Diagnostics
                                  │
                                  ▼
                     Tynel Semantic Layer
              Name / Type / Scope / Control / Effects
                                  │
                                  ▼
                     Typed Tynel IR / HIR
                                  │
                                  ▼
                     Tynel NIR / Lower IR
                                  │
                    ┌─────────────┴─────────────┐
                    ▼                           ▼
              Go-based Backend              Future Backend
          (Tynel-maintained Fork)          (optional target)
                    │
                    ▼
          Tynel Runtime + Native Bindings
                    │
                    ▼
             Native Executable
```

围绕编译主链路，逐步建立配套子系统：

| 子系统 | 长期职责 |
| --- | --- |
| Language Specification | 语言版本、语法范围、类型规则和运行时语义 |
| Frontend | TypeScript 语法解析、AST、源码位置和诊断 |
| Semantic Layer | Tynel 类型检查、符号绑定、控制流和效果分析 |
| HIR / NIR | 与前端和后端解耦的类型化程序表示 |
| Backend | 目标平台代码生成、ABI、对象布局、链接 |
| Runtime | 内存管理、字符串、对象、异常、调度和系统接口 |
| Standard Library | 面向 Tynel 用户的标准 API 和可复用实现 |
| Module / Package System | 模块解析、依赖、包版本、构建图和缓存 |
| Developer Tooling | LSP、Formatter、测试工具、调试器、文档和包管理 |
| Bootstrap | 使用已知可用的种子工具链构建 Tynel 编译器自身 |

架构演进不要求一次性建成全部模块。每个子系统先建立清晰边界，再随语言能力扩张逐步丰富。

---

## 5. 分阶段路线图

路线图以可验证里程碑划分，不绑定日历日期。每一阶段都必须有实现范围、语言规范、正向用例、负向用例和回归证据。

### 阶段 0：确立语言宪章与兼容性政策

**目标：** 从“编译器 MVP”进入“独立语言项目”的规划状态，明确长期边界，避免后续功能由局部实现临时决定。

主要工作：

1. 新建 Tynel Language Charter，明确 TypeScript 语法是长期入口，而不是短期迁移方案。
2. 建立 TypeScript 版本基线和语法兼容矩阵，记录每种语法是 `supported`、`planned`、`parsed-but-rejected` 还是 `not-in-scope`。
3. 明确语法兼容、类型系统兼容和运行时兼容的不同承诺。
4. 建立语言语义文档：数值、布尔、字符串、对象、数组、函数、模块、内存、异常和并发逐项有规范入口。
5. 将当前 MVP 标记为 `Tynel Language Preview 0`，保留其历史验收标签，不通过重写旧计划来描述未来目标。
6. 建立 RFC / ADR 流程：影响语言语义、NIR、ABI 或兼容性的改动，先记录设计决策再实现。

交付物建议：

- `LANGUAGE.md`：语言总览与设计原则；
- `docs/language/compatibility.md`：TypeScript 语法和类型能力矩阵；
- `docs/language/semantics/`：语言语义文档；
- `docs/decisions/`：关键设计决策；
- `docs/roadmap.md`：长期路线图。

完成标准：任何人都能从文档中区分 Tynel 已实现能力、计划能力、兼容目标和明确不承诺的行为。

### 阶段 1：编译器架构解耦与前端兼容性基础

**目标：** 保留当前可工作的 tsgo + Go Backend，同时把 Tynel 自己的语言层边界做扎实，为后续更复杂的 TS 语法和自有语义打基础。

主要工作：

1. 将 Frontend、Semantic Layer、HIR/NIR、Backend 的接口明确化，前端不直接依赖 Go IR。
2. 对 `typescript-go` 的依赖进行版本锁定，记录上游版本、Tynel 补丁和适配层版本。
3. 建立上游同步机制：升级 TypeScript 语法版本时运行 Tynel 语法回归，不直接无条件追随上游主线。
4. 把上游 AST / Checker 对象转换为稳定的 Tynel DTO / Facts；不允许上游对象类型穿过编译器边界长期传播。
5. 扩展 Facts 和类型表示，使其能描述结构化类型，而不是将复杂类型全部降成 `KindOther`。
6. 让模块解析器成为可替换接口，为未来的 `@tynel/*` 标准库模块和包解析预留 resolver。
7. 为 NIR 建立严格的 schema/version 迁移规则、验证器和兼容回放测试。
8. 为源码位置、类型信息、变量作用域和调试信息保留稳定映射。

关键原则：不要为了支持一个新语法，直接把 tsgo 内部节点塞进 NIR；也不要让后端通过猜测源码信息来补足前端没有表达的语义。

完成标准：新增一个语言能力时，可以分别测试前端解析、Tynel 语义检查、IR 降低、IR 验证和后端生成；更换或升级某个上游组件不会要求重写所有层。

### 阶段 2：基础值类型、内存模型与 Runtime ABI

**目标：** 从仅有数值和布尔的编译器，发展到能够承载典型 TypeScript 程序的数据模型。

推荐推进顺序：

1. **字符串（`string`）**：确定字符串编码、索引单位、长度、相等、切片、遍历和模板字符串语义。必须正面处理 JavaScript/TypeScript 字符串通常以 UTF-16 code unit 为可观察单位、而 Go 字符串通常以 UTF-8 字节为底层表示这一差异，不能直接用 Go `len(string)` 作为 Tynel 字符串长度。
2. **对象与结构类型**：定义对象字面量、字段访问、字段可变性、结构类型兼容、对象身份和属性缺失语义。
3. **数组与元组**：定义数组长度、索引、越界行为、可变性、切片、数组字面量、元组类型和扩容策略。
4. **联合与可空类型**：建立判别联合、`null` / `undefined`（是否分别支持）及控制流缩窄规则。
5. **函数类型**：建立函数类型、回调、函数值、闭包捕获和调用约定。
6. **泛型基础**：先支持常见泛型函数、泛型类型和类型参数约束，再扩展复杂类型运算。

Runtime ABI 应同步规划：

- 字符串、数组、对象的头部布局和指针字段；
- 堆分配、对象身份、引用和写屏障；
- Go Runtime GC 的使用边界，以及未来可替换的抽象层；
- Native Function ABI、调用约定和跨模块类型布局；
- 错误、异常或 Result 类型如何跨越 Runtime 边界。

推荐策略是先定义 Tynel 自己的逻辑类型和 Runtime ABI，再由 Go 后端选择合适的物理表示。早期可以借用 Go 的字符串、切片、结构体和 GC，但这些只是实现方式，不能直接等同于 Tynel 的语言语义。

完成标准：字符串、对象和数组具有明确规范；同一份语义测试能够在 Facts、NIR、Go Backend 和最终产物之间追踪；内存布局变更不会无记录地破坏模块 ABI。

### 阶段 3：核心 TypeScript 语法覆盖

**目标：** 扩展语言表达能力，使 Tynel 能够编写更真实的 TypeScript 风格程序，同时保持语法入口一致。

建议分批推进：

**3A：日常程序必需能力**

- 字符串字面量、模板字符串和字符串插值；
- 对象字面量、属性访问、计算属性和对象类型；
- 数组字面量、索引访问、数组方法所需的基础类型规则；
- `for`、`for...of`，以及必要的循环控制；
- 箭头函数、函数表达式、函数类型和回调；
- 可选属性、可选参数、默认参数、剩余参数；
- `??`、`?.` 等常用表达式；
- `interface`、扩展的 `type` alias、联合类型和字面量类型。

**3B：完整函数与类型系统基础**

- 泛型函数和泛型接口；
- 类型参数约束与默认类型参数；
- 类型收窄、类型守卫和 `typeof` / `in` 等相关表达式；
- 解构赋值、rest/spread；
- 闭包、嵌套函数和词法捕获；
- 函数重载声明与实现规则；
- `enum` 或其他枚举语法的明确支持策略。

**3C：更复杂的 TypeScript 语言能力**

- 类、构造函数、继承、访问器和成员可见性；
- 装饰器（仅在有明确 Runtime / 编译期语义后）；
- 交叉类型、索引访问类型、映射类型和条件类型；
- `infer`、模板字面量类型及高级类型推导；
- 命名空间、模块增强和声明文件的适用范围；
- JSX/TSX（如未来定位需要 UI / 跨端应用场景）。

并非每一个语法特性都应该只按解析器工作量排序。每一个特性都要考虑它对类型系统、运行时、对象表示、调试信息和标准库的影响。

完成标准：兼容性矩阵中的每项能力都具有可执行样例和拒绝样例；支持表不根据“Parser 能解析”就标记为已支持。

### 阶段 4：Tynel 标准库与模块生态

**目标：** 建立由 Tynel 自己定义和维护的标准库，使用户不必直接接触 Go API。

标准库建议分层：

1. **公共 API 层**：TypeScript 风格的 `.ts` 源码和类型声明，是用户实际导入的模块。
2. **Tynel 实现层**：用 Tynel 实现可移植的算法、格式化、集合操作和常用抽象。
3. **Native Runtime 层**：提供操作系统、I/O、时间、随机数、网络和其他必须使用底层能力的原生实现。

初步模块建议：

| 模块 | 主要职责 | 依赖能力 |
| --- | --- | --- |
| `@tynel/math` | 数学函数、常量、数值辅助函数 | 当前 number 即可起步 |
| `@tynel/text` | 字符串处理、编码和格式化 | string |
| `@tynel/collections` | List、Map、Set 等集合 | arrays、objects、generics |
| `@tynel/io` | 标准输入输出和流 | string、bytes、Runtime I/O |
| `@tynel/path` | 路径解析和平台差异 | string、平台抽象 |
| `@tynel/fs` | 文件读取、写入和目录操作 | string、错误模型、Runtime I/O |
| `@tynel/time` | 时间点、Duration、时钟 | Runtime clock |
| `@tynel/process` | 参数、环境变量、进程退出 | arrays、string、Runtime process |
| `@tynel/crypto` | 哈希、随机数、编码 | bytes、Runtime entropy |
| `@tynel/net` | DNS、TCP、HTTP 等网络能力 | bytes、异步和错误处理 |

模块解析从当前显式相对 `.ts` 导入扩展：先支持编译器内置 `@tynel/*`，再考虑本地包和锁文件，之后再决定是否需要 npm 兼容。不要把 npm 兼容作为标准库的前置条件。

原生绑定需要稳定的 Tynel Native ABI：使用稳定 native ID、签名、效果声明和目标平台清单，而不是让用户源码直接引用 Go 包名或 Go 符号名。绑定表、标准库源码、Runtime 源码和目标信息都必须纳入构建身份与发行校验。

完成标准：标准库有独立版本、模块清单、API 文档、跨平台支持标记和回归测试；用户程序只依赖 `@tynel/*` 公共 API，不感知其底层是否由 Go 实现。

### 阶段 5：Go 基础设施 Fork 与原生工具链自主化

**目标：** 将当前“依赖修改版 Go Compiler 的 Bridge”发展为“Tynel 自己维护和演进的一套底层编译基础设施”。

这一阶段不建议从复制整个 Go 仓库并大规模修改开始。更稳妥的顺序是先明确 fork 边界、补丁策略和 ABI，再逐步把真实需求沉淀进 fork。

建议工作流：

1. 建立独立的 Tynel Toolchain Fork 仓库或明确的 `toolchain/go` 维护目录，锁定上游 Go 版本和提交。
2. 将当前注入 Go IR 的桥接补丁整理成可复现的 patch set，明确每个补丁的原因、测试和上游差异。
3. 建立上游同步流程：升级 Go 时先应用补丁，再运行 Tynel 后端回归、Runtime 回归和目标平台验证。
4. 把 Tynel 特有的类型表示、字符串 / 对象 / 数组布局、函数调用和 Native ABI 逐步纳入受控后端接口。
5. 对 Go Runtime 的 GC、调度器、栈、panic、系统调用和链接机制建立适配层；先复用，再按性能和语言语义需求决定是否修改。
6. 当 Go IR 的表达方式限制 Tynel 优化或类型语义时，在 fork 中扩展编译器内部表示和转换过程，而不是在 Tynel 前端绕过 NIR。
7. 将汇编器、链接器、目标平台和系统 ABI 作为明确的 Toolchain 子系统维护，先做到版本和构建可控，再考虑更大的替换。
8. 持续保留 Backend 抽象，使将来可以增加其他目标后端，但不以“多后端”拖慢当前主线。

需要长期管理的风险：

- Go 上游内部 API 变动会增加 rebase 成本；
- Go Runtime 的内存模型不一定天然匹配所有 TypeScript 风格对象语义；
- Go GC、异常机制、协程调度和 Tynel 语言行为可能存在边界差异；
- Go 目标平台、系统调用和链接方式会影响原生发行；
- 自有补丁可能逐渐偏离上游，需要持续维护自动化回归和差异审查。

完成标准：Tynel 能够从锁定的上游源码和补丁集构建自己的工具链；编译器不依赖开发机中未声明的 Go 安装；Tynel 的类型和 Runtime 契约不暴露 Go 内部类型；升级 Toolchain 有自动化回归和回滚路径。

### 阶段 6：工具链与开发者体验

**目标：** 让 Tynel 从能够编译程序的语言原型，成为可持续开发和维护的软件工程工具链。

主要工作：

- Language Server Protocol：补全、跳转、引用查找、诊断和重构；
- Formatter：统一 TypeScript 风格代码格式；
- Test Runner：单元测试、原生集成测试和测试过滤；
- Package Manager：包元数据、版本约束、锁文件、缓存和供应链校验；
- Debugger：源码断点、变量查看、调用栈和调试信息；
- Profiler：CPU、内存和分配分析；
- Documentation Generator：API 文档和标准库文档生成；
- Cross-platform Build：逐步扩展到 `linux/amd64`、`linux/arm64`、`windows/amd64`、`darwin/arm64` 等目标。

先确保编译器、标准库和 Runtime 的语义稳定，再大规模投入 IDE 生态。调试信息、源码位置和包元数据应从前面的 IR 与构建身份阶段开始预留，不能等工具阶段才补救。

完成标准：新用户可以通过可复现的安装和包管理流程建立项目，完成检查、测试、构建、调试和发布；文档明确标记每个目标平台的真实验证状态。

### 阶段 7：编译器自举

**目标：** 让 Tynel 编译器的核心实现由 Tynel 自己编写，并能使用已经构建出的 Tynel 编译器编译自身。

建议分四步完成：

**7A：建立种子编译器（Stage 0）**

当前 Go 实现的 Tynel 编译器作为可信种子工具链。它负责编译早期 Tynel 编译器源码，并生成第一个可执行的 Tynel 编译器。

**7B：用 Tynel 编写编译器（Stage 1）**

先将词法 / 语法处理、诊断、符号表、类型表示、模块加载、HIR / NIR 构建等适合用 Tynel 表达的组件迁移到 Tynel。初期可以通过 FFI 或 Native ABI 调用仍由 Go 提供的底层能力。

**7C：Tynel 编译器编译自身（Stage 2）**

使用 Stage 0 编译 Stage 1，再使用 Stage 1 编译自身得到 Stage 2。对三个阶段执行相同的语言测试、IR 测试和原生程序测试，确认行为一致。

**7D：建立可重复 Bootstrap 流程**

新环境从源码、锁定的依赖和已知种子编译器开始，能够构建当前 Tynel 编译器。保留明确的种子版本和恢复流程，避免自举链路断裂后无法恢复。

自举验收条件：

- 编译器核心源码使用 Tynel 编写；
- Stage 0 能构建 Stage 1，Stage 1 能构建 Stage 2；
- Stage 1 和 Stage 2 对语言测试集、IR 测试集及原生输出测试给出一致结果；
- 能从干净工作目录完成 Bootstrap，不依赖未声明的开发机文件；
- 编译器构建输入、种子版本、工具链版本和产物哈希可追踪；
- 即使底层仍使用 Tynel Fork 的 Go Linker、Runtime 或汇编器，也明确记录它们是自举工具链的外部基础设施。

自举不等于“所有工具都必须由 Tynel 写”。语言编译器核心自举后，是否继续用 Tynel 重写链接器、汇编器、操作系统接口或 Runtime，应由长期维护成本和实际需求决定。

---

## 6. 语言特性优先级建议

在未来几个阶段里，建议按“能否支撑真实程序”而不是按“TypeScript 语法列表顺序”排序。

| 优先级 | 能力 | 原因 |
| --- | --- | --- |
| P0 | 字符串、模板字符串 | 基本文本处理、错误信息、I/O 和标准库的前提 |
| P0 | 对象、接口 / 结构类型 | TypeScript 程序最常见的数据建模方式 |
| P0 | 数组、索引、基础集合 | 数据处理和函数 API 的基础 |
| P0 | 函数值、箭头函数、闭包 | 回调、集合操作和模块抽象的基础 |
| P1 | 联合类型、类型收窄、可空值 | 实际 TS 类型设计中高频出现 |
| P1 | 泛型、约束、函数重载 | 构建可复用标准库和类型安全 API 的基础 |
| P1 | 模块解析、标准库 resolver | 形成可维护的语言和包生态 |
| P1 | 异常或统一错误模型、异步基础 | I/O、网络和系统 API 的前提 |
| P2 | 类、继承、装饰器 | 生态兼容和特定编程范式需求，需要配套对象模型 |
| P2 | 条件类型、映射类型、`infer` | 高级类型编程能力，实施复杂度较高 |
| P2 | TSX / JSX | 视未来应用方向决定，不能因语法兼容目标而默认优先 |
| P3 | npm / Node 生态兼容 | 涉及包格式、动态行为、JS 运行时和大量宿主 API，成本远高于模块解析 |

优先级是建议，不是固定顺序。若某项能力成为另一项能力的结构性前提，应先完成前置语义设计。

---

## 7. 必须提前做出的关键设计决策

以下事项会影响多个子系统，不能只在实现过程中临时决定。

### 7.1 Tynel 与 JavaScript 的运行时语义边界

需要建立一份“与 JavaScript 一致 / 有意不同 / 尚未决定”的语义表，重点包括：

- `number` 是否始终为 IEEE 754 binary64；
- 字符串是 UTF-16 code unit 语义，还是采用其他字符模型；
- `undefined` 与 `null` 是否是独立类型和值；
- 对象属性缺失与显式 `undefined` 如何区分；
- 数组越界、稀疏数组和数组扩容行为；
- 对象的相等是引用相等还是结构相等；
- 赋值、参数传递和对象复制规则；
- 异常是语言级机制、Runtime 机制还是由标准库 Result 处理；
- 异步是否遵循 Promise 风格语义，调度器由谁负责。

建议优先兼容开发者对常用 TypeScript / JavaScript 语法的预期行为；对会增加显著运行时复杂度的能力，允许定义 Tynel 语义，但必须显式记录差异。

### 7.2 类型系统采取什么兼容策略

TypeScript 的类型系统有大量复杂能力。建议分层实施：

- 先支持常见结构类型、联合类型、泛型和类型收窄；
- 再支持高级类型运算和更复杂的推导规则；
- 对暂不实现的类型构造保留语法识别和明确诊断；
- 对 `any`、类型断言、声明文件等高风险逃逸口设置清晰边界。

不要直接把 TypeScript Checker 的某个内部类型对象作为 NIR 的类型定义。Tynel 应定义自己的 canonical type representation，并明确类型擦除、单态化、字典传递或其他泛型实现策略。

### 7.3 内存管理与对象模型

Go GC 是非常有价值的早期基础，但不应默认所有 Tynel 对象都必须永久采用 Go 的数据布局。先由 Tynel 定义逻辑对象模型和 Runtime ABI，再确定早期后端如何映射。

重点验证：循环引用、闭包捕获、对象字段中的指针、数组扩容、写屏障、栈逃逸、FFI 生命周期和多线程访问。

### 7.4 Fork 的维护边界

需要决定 Tynel Fork 将包含哪些 Go 子系统：编译器核心、Runtime、链接器、汇编器、标准库中的部分实现等。不要无差别复制整个 Go 生态，也不要让项目长期依赖大量未记录的本地 patch。

每个补丁应具备：上游基线、修改原因、受影响模块、回归用例、合并 / 放弃策略。

### 7.5 原生绑定与平台能力

标准库需要有稳定的 Native ABI：函数 ID、类型签名、效果（I/O、文件、网络、时钟、随机数等）、目标平台和 ABI 版本。构建器应能检查某个程序需要的原生能力是否被当前目标支持。

这只是编译和链接层面的能力声明，不等同于操作系统安全沙箱。真正的权限隔离仍需要由操作系统和部署环境提供。

---

## 8. 版本、测试和质量体系

### 8.1 语言版本独立于工具链版本

建议分别维护：

- Language Version：语言语法和语义版本；
- Compiler Version：编译器实现版本；
- NIR Schema Version：中间表示协议版本；
- Runtime ABI Version：用户程序与 Runtime 的调用契约版本；
- Standard Library Version：标准库公共 API 版本；
- Toolchain Version：Go Fork、目标工具、链接器和平台 SDK 的组合版本。

这些版本可以有关联，但不应使用一个版本号混合表达所有兼容性。

### 8.2 测试分层

| 测试层 | 验证内容 |
| --- | --- |
| Syntax Conformance | TypeScript 语法解析和版本兼容性 |
| Type / Semantic Tests | Tynel 类型规则、绑定、控制流和错误诊断 |
| IR Tests | HIR / NIR 结构、类型、验证和序列化回放 |
| Runtime Tests | 字符串、对象、数组、GC、异常、异步等运行时行为 |
| Backend Tests | ABI、代码生成、链接和目标平台行为 |
| Differential Tests | 对有意兼容 JavaScript 的行为与参考执行结果做差分 |
| Standard Library Tests | 标准 API 的功能、边界和跨平台测试 |
| Bootstrap Tests | Stage 0 / Stage 1 / Stage 2 的构建和行为一致性 |
| Reproducibility Tests | 锁定输入、干净环境、工具链和产物身份 |

每个新增语法特性至少有：解析成功用例、类型正确用例、类型错误用例、运行时行为用例和组合回归用例。只有 Parser 通过，不算语言特性完成。

### 8.3 兼容性矩阵状态

推荐统一使用以下状态：

- `supported`：语法、类型检查和运行时语义均通过对应验收；
- `syntax-only`：能识别语法，但尚不能生成可执行程序；
- `planned`：已确定方向，尚未实现；
- `rejected`：当前明确拒绝，诊断稳定；
- `deferred`：技术方向未定，等待 RFC；
- `not-targeted`：明确不属于当前语言目标。

避免使用含义不清的“部分支持”。必须说明部分支持到哪一层、哪些写法不支持。

---

## 9. 建议的近期实施顺序

在当前 MVP 已完成的前提下，下一步不建议立刻大规模 Fork Go，也不建议同时铺开几十种 TypeScript 特性。推荐先完成以下工作：

### 第一步：发布 Tynel Language Charter

明确语言目标、TypeScript 语法兼容策略、Go 基础设施定位、自有语义边界和自举目标。这一步是后续所有设计的判断依据。

### 第二步：建立 TypeScript 兼容性矩阵

以当前 MVP 支持表为初始数据，扩充为完整的语法 / 类型 / 运行时兼容矩阵。为后续每个特性定义规范、测试目录和验收状态。

### 第三步：做 Frontend / Semantic / NIR 架构解耦

梳理当前 Facts、Capability、Lowering 和 NIR 的职责；确定统一的类型表示、模块身份、源码位置和诊断协议。先消除架构耦合，再大量增加语法。

### 第四步：确定字符串、对象和数组的语义与表示

优先完成 `string`、对象字面量 / 结构类型、数组这三个基础能力的设计 RFC。它们将决定后续泛型、标准库、GC、Runtime ABI 和大量 TypeScript 语法的实现方向。

### 第五步：引入第一版 Tynel 标准库和 Native ABI

以 `@tynel/math` 作为最小试点，再逐步扩展 `text`、`collections` 和 `io`。验证纯 Tynel 实现与 Native Runtime 实现如何组合，确保标准库从一开始就属于 Tynel。

### 第六步：建立可维护的 Go Toolchain Fork 工作流

先对当前修改版 Go 编译器形成可复现的补丁集、锁定来源和自动化回归，再根据真实语言需求修改内部实现。不要为“拥有 Fork”而 Fork；每一个深入修改都应对应一个具体的 Tynel 语义、性能或平台需求。

这些步骤完成后，再根据实际能力扩张情况推进完整标准库、更多 TypeScript 特性、跨平台、开发工具和自举。

---

## 10. 当前不建议过早投入的事项

1. **不要立即追求完整 npm / Node 兼容。** 这不仅是模块解析工作，还牵涉动态对象、JavaScript 运行时、事件循环、宿主 API 和大量包的隐含约定。
2. **不要一次性 Fork 并重写整个 Go 工具链。** 先维护当前实际使用的最小补丁集，逐个把有必要的改动沉淀进 Fork。
3. **不要只扩充 Parser 支持列表。** 每种语法都要经过 Tynel 类型语义、IR、Runtime、后端和测试。
4. **不要让标准库直接变成 Go API 包装层。** 标准库公共类型和签名必须由 Tynel 定义。
5. **不要过早自举。** 在字符串、对象、数组、函数值、模块和标准库还不稳定时，自举只会把尚未成熟的设计复制进编译器自身。
6. **不要以“完全兼容 TypeScript”作为没有边界的承诺。** 应维护明确的 TypeScript 版本基线和兼容性矩阵，逐步扩大支持范围。
7. **不要把自举和底层组件全部自研混为一谈。** 自举首先是编译器实现语言的自主化，底层工具链可以继续复用 Tynel 维护的 Go Fork。

---

## 11. 长期成功标准

当 Tynel 达到以下状态时，可以认为它已经从一个依赖 Go 基础设施的编译器原型，发展为一门具有自主工程体系的语言：

- TypeScript 语法是长期稳定的源代码入口，兼容性有版本基线和自动化测试；
- Tynel 的语言规范独立于 TypeScript Checker 和 Go Compiler 的内部实现；
- 常见 TypeScript 程序结构有明确的 Tynel 类型规则和原生运行时语义；
- HIR / NIR 能稳定表达 Tynel 语言特性，不泄漏 Go 内部对象；
- Tynel 有自己的 Runtime ABI、标准库和模块 / 包系统；
- Go 基础设施由 Tynel 项目维护和演进，能够按锁定来源、补丁和测试重建；
- 至少一个正式支持的平台可以从源码稳定构建并运行原生程序；
- 编译器核心由 Tynel 编写，并能通过 Stage 0 / Stage 1 / Stage 2 完成自举；
- 开发者能使用 Tynel 完成真实项目，而不需要把 Tynel 当作 Go 或 JavaScript 的语法包装。

**最终定位：Tynel 是一门长期兼容 TypeScript 语法、拥有自身语言语义与标准库、以原生编译为目标，并逐步实现工具链自主化和自举的编程语言。**
