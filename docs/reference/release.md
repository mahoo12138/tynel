# 编译器发行目录

本页说明 `tools/package` 生成的发行目录里有什么、为什么可以移动，以及构建出的用户程序需要什么。发行目录只面向运行编译器，不是开发仓库的副本；干净重建步骤见[06.05 验收记录](../validation/06-05-clean.md)，独立机器运行属于 06.06。

## 生成

```zsh
# 用当前 .work 工具构建 bin/tynel，再组装发行目录并自测示例。
./tools/package

# 指定输出目录；目标必须尚不存在。
./tools/package --out out/release/tynel-test

# 只复核已有目录的 release.json。
./tools/package --verify out/release/tynel-<commit>-darwin-arm64
```

默认目录名是 `tynel-<commit>-<goos>-<goarch>`，当前只允许在 `darwin/arm64` 上组装。打包入口先调用 `./tools/build`，然后复用 `.work` 中已准备的 SDK、前端宿主和修改版编译器；它不会重新下载工具链，也不会改写 `.work`。

打包在目标父目录创建临时目录，复制完成后写 `release.json`，用包内 `bin/tynel` 编译并运行 `examples/add.ts`，确认输出 `42`，最后把临时目录改名为目标目录。目标已存在时直接拒绝，不覆盖旧发行包。

## 目录内容

| 路径 | 作用 | 是否可重建 |
| --- | --- | --- |
| `bin/tynel` | 命令行入口与编译驱动 | 从仓库重新构建 |
| `.work/frontend/bin/consumer` | 预构建的 tsgo 前端宿主 | 从仓库和前端源码重建 |
| `.work/frontend/bin/consumer.stamp.json` | 宿主二进制戳记 | 随宿主重建 |
| `.work/bridge/compile` | 修改版 Go 编译器 | 用 `./bridge/go/build` 重建 |
| `.work/bridge/stamp.json` | 编译器、bridge 源码和 SDK 源码戳记 | 随编译器重建 |
| `.work/toolchain/<sdk>` | Go SDK、标准库、`go`、`compile`、`link`、`asm` | 按锁文件重新准备 |
| `.work/toolchain/toolchain.json` | 发行包内的 SDK 安装报告 | 重新打包 |
| `bridge/go/support` | 固定支持包，提供受限输出函数 | 从仓库复制 |
| `bridge/go/toolexec` | 只替换 `compile` 的工具包装 | 从仓库复制 |
| `bridge/go/gc`、`bridge/go/patches` | 修改版编译器源码身份与补丁 | 从仓库复制 |
| `tools/toolchain/go.lock.json` | Go 与前端版本、提交、许可证和目标 | 从仓库复制 |
| `go.mod` | 支持包的模块边界 | 从仓库复制 |
| `examples/add.ts` | 打包自测示例 | 从仓库复制 |
| `release.json` | 每个文件的 sha256、大小和权限 | 重新打包 |
| `LICENSES/` | Go 与 typescript-go 的许可证文本和来源说明 | 重新打包 |

发行包不带 `bridge/tsgo` 源码、`.work/frontend/typescript-go` 源码树或模块缓存：它使用预构建宿主，不在这里重建前端。因此发行包不能用于修改前端代码；开发构建仍使用仓库。

## 为什么能移动

开发准备报告里的绝对路径不能跟着目录搬走。打包时重写 `.work/toolchain/toolchain.json`，把 SDK 目录和工具写成相对发行根的路径：

```json
{
  "kind": "sdk",
  "dir": ".work/toolchain/go1.27.3-sdk-darwin-arm64",
  "tools": {
    "go": ".work/toolchain/go1.27.3-sdk-darwin-arm64/bin/go",
    "compile": ".work/toolchain/go1.27.3-sdk-darwin-arm64/pkg/tool/darwin_arm64/compile"
  }
}
```

`internal/toolchain.ResolvePath` 把相对路径固定到编译器根。绝对路径只有在位于同一个根内时才接受；指向开发机旧目录的路径按 `toolchain.path-escape` 退出 4，不会去读根外文件。构建记录里的 `tools` 仍是本次实际使用的绝对路径，但都落在搬移后的发行根内。

`host.Ensure` 发现发行根下有 `release.json` 时进入密封模式：它复核清单，只确认预构建宿主与戳记一致，不再要求前端源码、适配包副本或源码树。开发目录没有清单，仍走原来的源码戳记与按需重建逻辑。

## 完整性

`release.json` 的 `files` 按路径排序，每项记录 `sha256`、`bytes` 和四位八进制 `mode`。清单不登记自身，所以无法用清单验证清单；它不是数字签名，不能防止同时改写文件和清单的人。

`build` 和 `check` 启动时，`internal/release.VerifyIfSealed` 会逐个复核清单文件。失败码：

| 代码 | 含义 | 退出 |
| --- | --- | --- |
| `release.missing` | 清单登记的文件不存在，或必需文件没有登记 | 4 |
| `release.mismatch` | 文件大小、sha256 或权限与清单不符 | 4 |
| `release.invalid` | 清单结构、路径或必需文件列表不合法 | 4 |
| `release.unreadable` | 清单或文件无法读取 | 4 |
| `release.host-stamp` | 前端宿主与自己的戳记不一致 | 4 |

清单校验成功后在进程内缓存，同一构建不会对几百 MB 的目录重复哈希；失败不缓存。构建产生的 `.work/build/snapshots`、输出记录和用户程序不属于发行清单，可以额外存在。

## 编译器与用户程序

两件事必须分开：

| 对象 | 需要什么 |
| --- | --- |
| `bin/tynel` | 本发行目录内的 SDK、修订编译器、工具包装、支持包和前端宿主 |
| 编译出的程序 | 无。它是静态链接的 `darwin/arm64` Go 二进制，不需要 Node、Go、源码或发行目录 |

发行包只需要普通的 macOS arm64 环境；不需要系统 Go、Node、npm 或仓库脚本。用户程序仍受目标平台限制。

## 许可证

`LICENSES/go.LICENSE` 来自 SDK 的 `LICENSE`（BSD-3-Clause），`LICENSES/typescript-go.LICENSE` 来自前端源码树（Apache-2.0）。`LICENSES/NOTICE.md` 记录版本字段的位置和来源。Tynel 仓库当前没有项目级 LICENSE，发行包不替项目补一份，也不额外授予 Tynel 代码的许可证。

## 验证与限制

- 实际打包、搬移和示例构建见[06.04 验收记录](../validation/06-04-release.md)。
- 只组装 `darwin/arm64`，不交叉打包。
- 发行包是本地目录，不包含压缩包、签名或远程发布步骤。
- 发行包不能重建前端宿主；需要修改前端时回到仓库开发树。
- 目录名包含空格或中文时，`-toolexec` 路径会按 Go 的 quoted.Split 规则加引号；搬移后的构建已在 06.05 验证。
- 只在开发机的隔离工具链上验证，尚未在无项目源码、无 Node 的独立机器运行用户程序；那是 06.06 的范围。
