# Go 桥接与运行时

后端不生成业务 Go 源码，也不内嵌 JavaScript 引擎。本页说明 bridge 怎样把 NIR 交给 Go 编译器，产物里的运行时承担什么，以及并发能力的当前位置。

## 在内存中构造节点

bridge 在合法的编译器包树内工作，直接创建 Go 编译器的高层 IR 节点。构建通过 `-toolexec` 替换 `compile`，用 `TYNEL_NIR` 指定输入快照：

- 用户函数从 NIR 构造成节点，加入固定的包；
- 指令选择、寄存器分配、目标代码与链接由匹配的 Go 工具链完成，不自行实现第二套机器码后端；
- Go 内部对象只在 bridge 包树内访问，NIR 与验证器不依赖它们。

```zsh
# 开发接口：直接驱动 bridge 编译一份手写 NIR，预期输出 42。
./bridge/go/run examples/ir/add.nir.json

# 正式入口多了快照、事务发布与构建记录。
./bin/tynel build-ir examples/ir/add.nir.json -o out/add
```

## 固定宿主支持

NIR 无法表达、也不该由用户书写的最小宿主能力，由手写 Go 文件提供：入口调度、按位构造 number、数值与布尔输出。这个文件对所有输入都相同，不随 NIR 变化：

```go
// tynelPrintFloat64 是 NIR 里 print 语句的目标。
// 每次调用写一行；受限输出，不是 Node 的 console。
func tynelPrintFloat64(v float64) {
	fmt.Fprintln(os.Stdout, tynelFormatNumber(v))
}
```

数值输出保留 binary64 的真实结果：`0.1 + 0.2` 打印 `0.30000000000000004`；特殊值用固定拼写。`go/constant` 表示不了 NaN、无穷与负零，bridge 对这三类值改用按位构造，避免位模式在编译期被改写。

这就是自举边界的明确划分：编译器代码与固定宿主支持可以用 Go 手写；用户代码的编译路径永远不经过生成的 `.go` 文件。

## 产物里的运行时

生成的可执行文件内置 Go 运行时：

- **GC**：内存管理随产物分发，不需要外部运行时安装；
- **调度器**：goroutine 调度器在运行时内，为后续并发能力提供基座；
- **系统依赖**：产物仍需要 macOS 的 `libSystem` 与 `libresolv`，此外不依赖 Node、Go 工具链或编译器目录。

验收时只复制 exe 到空目录、`env -i` 清空环境运行成功；Mach-O 检查确认了动态库依赖。这个结果只覆盖 `darwin/arm64`。

## 并发的当前位置

当前已验收子集**不提供用户态并发**：没有 async/await，没有 goroutine 绑定，也没有通道。这不是遗漏，而是范围契约——首版专注数值与布尔计算的语义正确性（求值顺序、binary64、引用身份）。

Go 运行时的调度器与 GC 已经随二进制分发，构成后续并发能力的基座；是否开放、以什么形式开放（例如受控的结构化并发），属于路线图上的后续决策，不作为已实现能力宣传。

## 身份与锁定

Go、tsgo、接入补丁、声明库与支持库的身份都重新核验并锁定在 `tools/toolchain/go.lock.json`。修改版编译器带戳记，源码变化而未重编时构建拒绝产出。发行目录携带明确列出的 Go 工具，不要求把整个工具链压成单个文件。