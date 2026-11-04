# 06.04 验收记录：组装编译器发行目录

本文件记录 06.04 的实际运行结果。被测源码是包含本文件的提交，提交正文带 `Task: 06.04`；`bin/tynel` 在提交前构建，所以 `version` 显示开发提交 `77e03b9`。命令在 2026-11-04 09:10–09:25（CST）执行。

## 环境

| 项目 | 值 |
| --- | --- |
| 机型 | Mac mini，机型标识 Mac16,10 |
| 芯片 | Apple M4，24 GB 内存 |
| 系统 | macOS 26.6.2，构建号 25G83 |
| 架构 | arm64 |
| 工具链 | 锁定的 go1.27.3（`.work/toolchain`），前端 typescript/v7.0.2（提交 `2bd066d8`） |
| 修改版编译器 | `.work/bridge/compile`，sha256 前缀 `7a75b4ac` |
| 命令行 | `bin/tynel`，构建时提交 `77e03b9` |

发行包生成在 `out/release/`，随后搬到 `.work/validation/06-04/moved-compiler` 验证；两者都不进入版本库。打包日志和搬移后的构建记录保存在 `.work/validation/06-04/`。

## 打包

```zsh
# 构建当前 CLI，组装发行目录，并在目录内编译、运行示例。
./tools/package
```

```text
已生成 /Users/mahoo/Projects/tynel/bin/tynel（commit 77e03b9）
已生成 /Users/mahoo/Projects/tynel/out/release/tynel-77e03b9-darwin-arm64（15674 个文件，darwin/arm64）
  版本       tynel dev（commit 77e03b9）
  清单       /Users/mahoo/Projects/tynel/out/release/tynel-77e03b9-darwin-arm64/release.json
```

发行目录 339 MB，`release.json` 3.6 MB。清单记录 15,674 个文件，每项带 sha256、字节数和权限；清单自身不登记。打包命令先检查 `bin/tynel` 的提交与 Git HEAD 一致，复制 SDK、前端宿主、修改版编译器、支持文件和许可证，重写相对工具路径，再执行 `bin/tynel build examples/add.ts` 和运行产物，输出必须是 `42`。

## 正向验收：搬移后仍可编译

```zsh
# 把发行目录搬到另一个普通路径，再在包内构建示例。
cd .work/validation/06-04/moved-compiler
./bin/tynel build examples/add.ts -o "$PROJECT/.work/validation/06-04/moved-out/add"
"$PROJECT/.work/validation/06-04/moved-out/add"
```

```text
已生成 /…/.work/validation/06-04/moved-out/add（2446530 字节）
  源码     examples/add.ts（12269d671d8c）
  输入     e2c01c8ddc53
  身份     5b7e32aefb88
  编译器   7a75b4acf10b
  构建记录 /…/.work/validation/06-04/moved-out/add.build.json
42
```

产物 sha256 是 `33c884f4ae0bd70fbbf6de955e69355ff4f3c308563738d88fd8654235e4adb3`，记录 sha256 是 `2ae05c1c61bde6f25b8aa2a41bde140d8820db86a127e6bd564a73dd5ac2385c`。完整身份是 `5b7e32aefb88e4a0f436bf37fe9342a02d71aa10d5a127c54826b55aa598c1a0`，产物输入身份是 `a520ab2b33194cfc0751c29ed96ec0a234d4216d094adf18cc7f35566580d28f`。

### 工具来自声明位置

```zsh
# 搬移后的发行包列出 SDK 与四个工具的实际路径。
./bin/tynel toolchain
```

```text
SDK 目录     /…/.work/validation/06-04/moved-compiler/.work/toolchain/go1.27.3-sdk-darwin-arm64
  go       /…/.work/toolchain/go1.27.3-sdk-darwin-arm64/bin/go
  compile  /…/.work/toolchain/go1.27.3-sdk-darwin-arm64/pkg/tool/darwin_arm64/compile
  link     /…/.work/toolchain/go1.27.3-sdk-darwin-arm64/pkg/tool/darwin_arm64/link
  asm      /…/.work/toolchain/go1.27.3-sdk-darwin-arm64/pkg/tool/darwin_arm64/asm
```

独立解析搬移后的构建记录，`tools.go`、`tools.compiler`、`tools.toolexec` 都在发行根内；`.work/toolchain/toolchain.json` 的 `dir` 是相对路径 `.work/toolchain/go1.27.3-sdk-darwin-arm64`，清单里没有绝对路径或 `..`。因此工具不是从开发机 `.work` 或旧归档目录读到的。

### 清单复核

```zsh
# 从仓库校验搬移后的目录。
./tools/package --verify .work/validation/06-04/moved-compiler
```

```text
清单有效：/…/.work/validation/06-04/moved-compiler（15674 个文件，darwin/arm64）
```

`release.json` 的 sha256 是 `1a31804c6a9775279462bc8051f60d867ea79f725559a7533346be1aec1bb9cc`，`bin/tynel` 是 `8178167233c37527f8701bdeefd4bdfdd70e1889df82b7bbefe2e03b39ccb569`。打包自测和搬移后构建都输出 `42`。

## 负向验收

以下命令都在搬移后的目录内执行，退出状态由实际进程返回。

| 场景 | 操作 | 实际诊断 | 退出 |
| --- | --- | --- | --- |
| 删去必需文件 | 暂时移走 `bridge/go/support/support.go` | `release.missing`: 发行包缺少 `bridge/go/support/support.go`；阶段 `toolchain` | 4 |
| 混入错配支持库 | 在 `support.go` 末尾追加注释 | `release.mismatch`: 大小是 2144，清单记录 2131；阶段 `toolchain` | 4 |
| 使用开发机绝对路径 | 去掉清单，把报告 SDK 目录改回仓库 `.work/toolchain`，用 `build-ir` 构建 | `toolchain.path-escape`: 路径在发行根之外；阶段 `toolchain` | 4 |

三种失败都在启动后端之前结束，没有生成 `missing`、`mismatch` 或 `absolute` 产物；恢复文件和清单后，`--verify` 再次通过。绝对路径这条使用 `build-ir`，因为去掉清单后密封宿主不再可验证，而 `build-ir` 不经过前端；它验证的是同一份工具路径解析。

### 空格路径的已知限制

把同一发行包放到带空格的 `搬移 后的编译器` 后，`build` 失败：

```text
go: error obtaining buildID for go tool compile: fork/exec /…/搬移: no such file or directory
```

原因是 `go build -toolexec=<路径>` 把命令按空格拆分，路径停在第一个空格。当前普通路径的搬移已经通过；空格和中文目录的完整构建属于 06.05，不在 06.04 冒充已解决。

## 累计回归

| 检查 | 命令 | 结果 |
| --- | --- | --- |
| 全部 Go 测试 | `./tools/test` | 18 个包通过 |
| bridge 后端 | `./bridge/go/check` | 13 项通过 |
| 前端宿主 | `./bridge/tsgo/check` | 96 项通过，末尾 `全部通过` |
| 静态检查 | `go vet ./...` | 无输出，退出 0 |

新增回归包括 `internal/release` 的清单收集/缺失/内容/权限/路径检查、`internal/host` 的密封宿主复用与缺宿主、`internal/toolchain` 的根外路径拒绝，以及 `internal/build` 的相对 SDK 目录和根外 SDK 拒绝。

原始日志在 `.work/validation/06-04/`：`package.log`、`version.log`、`toolchain.log`、`moved-build.log`、`moved-run.log`、`verify.log`、`missing.log`、`mismatch.log`、`absolute.log`、`hashes.txt`、`output-hashes.txt`。

## 未覆盖

- 只验证 `darwin/arm64`；没有交叉打包或其他平台。
- 没有在无项目源码、无 Node 的独立机器运行生成物；用户程序独立运行属于 06.06。
- 打包入口依赖开发机已准备的 `.work`，不是从空目录重建工具链；那是 06.05。
- 发行包不带前端源码，不能在包内重建前端宿主；这是有意的裁剪，不是缺失。
- `release.json` 没有数字签名，不能防止同时改写文件和清单；它只保证单方面改动可被发现。
- 目录名含空格时 `-toolexec` 的拆分问题尚未修复；普通路径搬移已通过。
