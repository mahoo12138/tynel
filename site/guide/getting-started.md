# 快速开始

本页带你在 Apple Silicon macOS 上从仓库源码准备工具链，编译并运行第一个原生程序。首版只验证 `darwin/arm64`；Windows 与其他平台没有可用的准备命令，不在本页提供伪装成可用的步骤。

## 前置条件

| 项目 | 要求 | 检查方式 |
| --- | --- | --- |
| 系统 | Apple Silicon macOS | `uname -m` 输出 `arm64` |
| 宿主 Go | go1.24 或更新，只用于编译准备入口 | `go version` |
| 网络 | 可访问 `go.dev/dl`、`codeload.github.com` 与 Go 模块代理 | 准备命令会报告失败原因 |
| 磁盘 | 至少 1.5 GB 空闲 | `df -h .` |
| Git | 用于取得代码 | `git --version` |

准备过程不修改宿主 Go 的安装目录，也不写入全局环境变量。所有工具落在项目的 `.work/` 内。

## 取得代码并准备工具链

```zsh
# 克隆仓库并进入项目根目录。
git clone git@github.com:Mahoo12138/tynel.git
cd tynel

# 读取锁文件，下载并校验 Go SDK、Go 源码与 TypeScript 前端源码，
# 解压到 .work/toolchain/ 与 .work/frontend/。重复执行会复用缓存。
./tools/toolchain/prep
```

命令成功后打印各工具的绝对路径。首次构建前端宿主时需要访问 Go 模块代理，之后可以离线使用。

## 构建编译器入口

```zsh
# 用 .work 内的隔离 Go 构建开发用 CLI；bin/ 不进入版本库。
./tools/build

# 显示程序版本、目标平台与锁定的配套工具身份。
./bin/tynel version
```

## 编译第一个程序

仓库自带 `examples/add.ts`：

```typescript
function add(a: number, b: number): number {
  return a + b;
}

console.log(add(40, 2));
```

```zsh
# 只做检查：类型检查、能力判断、降低并验证 NIR，不生成机器码。
./bin/tynel check examples/add.ts

# 编译成原生程序；-o 必填，build 本身不运行程序。
./bin/tynel build examples/add.ts -o out/add

# 运行上一条命令生成的文件，预期输出 42。
./out/add
```

也可以一步到位：

```zsh
# run 每次重新编译，产物固定放在 .work/run/app，然后执行本次产物。
./bin/tynel run examples/add.ts
# 42
```

## 试试多文件项目

`examples/quote` 把折扣、运费和共享类型拆成四个文件，仍然只生成一个原生程序：

```zsh
# --root 指定项目根；入口相对项目根解析。预期输出 90、10、100。
./bin/tynel build --root examples/quote main.ts -o out/quote
./out/quote

# 查看模块初始化顺序：类型模块 types.ts 只走类型边，不执行。
./bin/tynel dump init --root examples/quote main.ts
```

## 得到可搬移的编译器目录

```zsh
# 组装发行目录：预构建前端宿主、修改版编译器、Go SDK 与支持文件，
# 生成 release.json 清单，并用包内 CLI 自测示例。
./tools/package
```

发行目录可以整体搬到别处使用；被编译出来的用户程序则只需要 macOS 系统库，不依赖发行目录。布局与完整性规则见仓库 `docs/reference/release.md`。

## 失败时看什么

退出状态按类别分开：`1` 源码错误、`2` 用法错误、`3` 能力限制、`4` 环境缺失、`5` 内部故障。「工具没装好」不会被读成「用户代码有错」。完整契约见[命令行 API](/reference/cli)，错误码见[诊断与错误码](/reference/diagnostics)。

下一步：逐项了解[当前支持的语言特性](/language/features)。