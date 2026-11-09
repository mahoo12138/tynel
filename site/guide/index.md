# 简介

Tynel 是一个学习导向的编译器：把约定的 TypeScript 子集编译成原生可执行文件。前端用 TypeScript 官方的 tsgo 做解析与类型检查，后端用 fork 的 Go 编译器生成机器码，产物内置 Go 运行时。

它回答的问题很具体：一份 TypeScript 源码，怎样不经过 JavaScript 引擎，直接变成可以独立运行的二进制。

## 现在处于什么状态

MVP 已于 2026-11-09 通过最终验收：6 个主阶段、48 个开发子节点全部验证完成。验收把编译器目录搬到含空格和中文的路径，用包内工具构建四文件项目，再只复制可执行文件到空目录、清空环境变量运行，输出与 Node 对照逐字节一致。

当前目标平台只有 `darwin/arm64`（Apple Silicon macOS）。Windows amd64 与其他平台是后续扩展，没有可用的构建命令。

## 一条命令看到的编译

```zsh
# 检查、编译、运行；示例是仓库里的 examples/add.ts。
./bin/tynel check examples/add.ts
./bin/tynel build examples/add.ts -o out/add
./out/add
# 42
```

`build` 不执行程序；产物发布成功后才由你运行。`run` 则每次重新编译并执行本次产物，不会运行上一次遗留的文件。

## 编译路径

一次编译经过六个阶段：

1. **TypeScript 源码**：入口与显式相对 `.ts` 依赖被读入一次快照。
2. **tsgo 类型检查**：完整的 TypeScript 诊断，带文件、行、列。
3. **声明事实**：把声明身份、引用与调用提取成 Tynel 自有数据；tsgo 句柄不出前端会话。
4. **NIR**：事实降低成中间表示，独立验证引用、实参、类型与返回路径。
5. **Go bridge**：在内存中构造 Go 编译器的高层节点，不生成业务 `.go` 源码。
6. **原生二进制**：指令选择、寄存器分配与链接由匹配的 Go 工具链完成。

细节见[编译流水线](/internals/pipeline)与 [Go 桥接与运行时](/internals/go-bridge)。

## 它不是什么

- 不是完整 TypeScript 实现：字符串、对象、数组、类、闭包、异步都不在首版子集内。
- 不是 Node 替代：没有标准库、npm 和事件循环；输出只有受限的 `console.log(number | boolean)`。
- 不是生产兼容承诺：首版只验证 `darwin/arm64`，产物仍依赖 macOS 系统库。

能编译哪些写法，逐项列在[语言特性总览](/language/features)。

## 接下来读什么

- 想动手：[快速开始](/guide/getting-started)准备工具链并编译第一个程序。
- 想查写法：[语言特性总览](/language/features)与语言参考各页。
- 想接脚本：[命令行 API](/reference/cli) 与[诊断与错误码](/reference/diagnostics)。