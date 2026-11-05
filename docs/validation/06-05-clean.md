# 06.05 验收记录：在干净目录重建工具和示例

本文件记录 06.05 的实际运行结果。被测源码是提交 `236cbe63c37884463f78f6e79b06807d8372c03b`；干净构建在 2026-11-05 10:16（CST）生成报告。之后追加本记录不会改变已经运行的工具和产物。

## 环境

| 项目 | 实际值 |
| --- | --- |
| 系统 | macOS 26.6.2，Darwin 25.6.0，`arm64` |
| 硬件 | Apple M4 |
| 宿主 Go | `go1.27.0 darwin/arm64`，只用于准备入口 |
| 隔离 Go | `go1.27.3 darwin/arm64`，来自锁文件下载 |
| 网络 | 下载 Go SDK、Go 源码和 typescript-go 归档 |
| Python 3 | 生成 `report.json`、核对构建记录和清理验证辅助 |
| 干净根 | `out/clean/06-05/source` |
| 提交 | `236cbe63c37884463f78f6e79b06807d8372c03b` |

命令：

```zsh
# 从 HEAD 建立 detached worktree，重建工具、测试、示例、发行包和搬移测试。
./tools/clean-build --tests
```

完整日志在 `.work/validation/06-05/clean-build.log`，机器报告在 `.work/validation/06-05/report.json`。

## 干净边界

`tools/clean-build` 先要求没有未提交的跟踪文件，再从 HEAD 建立新的 detached worktree。进入准备阶段前，脚本检查新树没有 `.work`、`.tynel`、`.upstream`、`dist`、`bin` 和 `out`；如果发现任何一项就停止。新树的 `.work`、Go 构建缓存和模块缓存都落在 `out/clean/06-05/` 下，不复用开发目录。

检查结果：

```text
干净树中不存在 .work / .tynel / .upstream / dist / bin / out（准备前）
准备后 .work 由本轮 prep 新建
```

## 源码与依赖核对

锁文件 `tools/toolchain/go.lock.json` 的 sha256：

```text
6df1cf01c9b6640ec9d59e5d51e313982a0fee8a830cc8c803e4547b4da708b4
```

准备命令重新下载并核验了三份归档：

| 输入 | 文件 | 字节 | 锁文件 sha256 |
| --- | --- | ---: | --- |
| Go SDK | `go1.27.3.darwin-arm64.tar.gz` | 68,196,737 | `dd3908cf20959d798c1e06ebe5b72213f0505ce01373b40ce3110975a6cd99c2` |
| Go 源码 | `go1.27.3.src.tar.gz` | 35,134,035 | `6e7749035cf360f06b2575d162ea79df9fda8ef671889e5c375f8e50777f26bb` |
| TypeScript 前端 | `typescript-go-2bd066d8.tar.gz` | 20,375,158 | `5ccb47dbb3f68cd0da58b71e6f445eee36a50bd3e9f9f330cc23a97b88500119` |

准备报告还记录 Go 标签 `go1.27.3`、源码提交 `2aae1000ea1d4b28923b96db28acf4e3b99d6aff` 和前端提交 `2bd066d87f5bafd315be9f40889d0a60b9e58e0b`。宿主 Go 的 GOROOT 在准备前后没有变化。

## 工具编译

| 产物 | sha256 | 结果 |
| --- | --- | --- |
| bridge 修改版编译器 | `5555ef6d2b03047bf1441305bef0806f46a1169502ada0405b7ae80c9c60077e` | 从新 SDK 和 bridge 源码生成 |
| clean tree 的 `bin/tynel` | `2a6761bdf3509de3b454651d97c3ac8f73dca5d432efdc71a03fc6f4202a587d` | `tynel dev (commit 236cbe6)` |

在同一个干净树里运行 `./tools/test`：

```text
测试通过：18 个包
```

所有示例和测试记录里的 `tools.go`、`tools.compiler`、`tools.toolexec` 都位于 `out/clean/06-05/source/`，没有回落到开发目录的 `.work`。

## 用户程序构建

四个示例都由 clean tree 的 `bin/tynel` 构建并运行。输出路径、产物哈希和构建记录在 `out/clean/06-05/outputs/`。

| 示例 | 输出 | sha256 | 字节 |
| --- | --- | --- | ---: |
| `examples/add.ts` | `42` | `28b6a55a9aa3cf2cb6a0e84d08d345086e10ff4f2ad50177a7449864a073aee0` | 2,446,530 |
| `examples/choices.ts` | `-1, 0, 1, 1, false, 3, true` | `f5105648f5faef7e48145e8cad15c24943febfd92ef8852dfea52082482a615a` | 2,446,562 |
| `examples/collatz.ts` | `111, 111, 8, 8, 0` | `37d8d94d0667174726558e7e3b4110299121873bb13bdba863c1197807794817` | 2,446,562 |
| `examples/quote` | `90, 10, 100` | `75995f63eb2e4a5cf6990fc90723a4a6e531c992af51a47bcfe648c1774f3058` | 2,446,626 |

每个产物都有对应的 `.build.json`；示例构建记录里的 `tools.toolexec` 是 clean tree 的 `bridge/go/toolexec`。

## 发行目录与搬移

clean tree 内重新打包：

```text
tynel-236cbe6-darwin-arm64
15,674 个文件
release.json sha256 21695104bcb80943ec30c9464ebb1ddb12ca3451a898142ad0c6cf8e00566dc4
```

把发行目录复制到含空格和中文的 `搬移 后的编译器` 后，clean tree 内的 `tools/package --verify` 再次通过。包内构建：

```zsh
# 在搬移后的发行目录中构建示例。
cd "out/clean/06-05/搬移 后的编译器"
./bin/tynel build examples/add.ts -o ../outputs/space-add
```

运行输出 `42`，产物 sha256 为 `dec65c51fa813205c9d8432c8ce28f13bc71a9edd77a9f75e814e70671d7c998`。记录中的 `go`、`compiler`、`toolexec` 全部位于 `搬移 后的编译器/` 内；`toolexec` 路径含空格和中文也能被 Go 正确解析。

## 负向验收

| 场景 | 结果 | 证据 |
| --- | --- | --- |
| 无网络 | 退出 5，代理拒绝后立即失败 | `negative/offline.log` |
| 同尺寸坏哈希 | 退出 6，报告实际 sha256 与锁文件不同 | `negative/badhash-full.log` |
| 错误 SDK | 退出 8，`VERSION` 首行为 `go9.99.0`，不再复用该目录 | `negative/wrong-sdk.log` |
| 已有输出目录 | 退出 1，拒绝把旧结果当成新干净构建 | `existing.log` |
| 旧式 `.tynel/compile` 诱饵 | 退出 0，实际 `toolexec` 仍是发行目录内的 `bridge/go/toolexec` | `negative/decoy.log` |

同尺寸坏哈希用例先创建一个与锁文件长度相同、内容全为零的归档；实际 sha256 是 `21bbb3bed1416fbe3592c1df8e4875ebb8ba2b9dd6e422b66ee72d4dd737f06a`，因此失败发生在内容哈希核对，而不是文件大小检查。

## 未覆盖

- 只在 `darwin/arm64` 上运行；其他平台没有交叉重建或运行证据。
- 干净构建需要宿主 Go、Git 和网络；没有实现或验证可离线携带的发行归档。缺网时只验证了明确失败退出 5。
- 生成的 exe 只在当前机器运行，尚未在没有源码、NIR、Node 和编译器目录的独立环境中运行；那是 06.06。
- `out/clean/06-05/` 和 `.work/validation/06-05/` 是本次证据目录，不进入 Git；清理它们不会改变已提交的工具逻辑。
- 发行清单没有数字签名；它能发现单方面文件或哈希变化，不能防止文件与清单同时被改写。
