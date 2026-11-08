# 06.06 验收记录：验证生成物可以独立运行

状态：**blocked**。本次已经实现验收器并完成产物身份与动态库预检，但当前执行环境不能应用限制性 macOS Seatbelt 配置，四个载荷均未在隔离环境中执行。不得把本记录解释成独立运行通过，也不得进入 06.07。

## 环境与被测输入

操作在 2026-11-08 11:58 至 2026-11-09 00:20（CST）执行。当前开发基线是 `1d990fd36cb1af837b3fe741c93b19e7f25ab765`，06.06 验收器在这个基线上新增；产物来自 06.05 的干净构建提交 `236cbe63c37884463f78f6e79b06807d8372c03b`。

| 项目 | 实际值 |
| --- | --- |
| 系统 | macOS 26.6.2，Darwin 25.6.0，`arm64` |
| 沙箱入口 | `/usr/bin/sandbox-exec` |
| 产物目录 | `out/clean/06-05/outputs/` |
| 最终受阻报告 | `.work/validation/06-06/final/report.json` |
| 控制台日志 | `.work/validation/06-06/independent-final.log` |
| Node | 宿主存在；验收器把它所在的用户目录列为禁止读取和禁止执行范围 |

验收器使用独立的期望输出，不读取 `tools/clean-build` 的期望值。输入 exe 与对应 `.build.json` 先核对 sha256 和字节数，再复制到新的 `payload/`；传输后的 exe 再计算一次 sha256。载荷目录不复制 TypeScript、NIR、Go SDK、Node 或编译器发行目录。

## 已完成的预检

四个产物都通过构建记录、传输哈希和 Mach-O 检查：

| 程序 | 产物 sha256 | 字节 | 动态库 |
| --- | --- | ---: | --- |
| `add` | `28b6a55a9aa3cf2cb6a0e84d08d345086e10ff4f2ad50177a7449864a073aee0` | 2,446,530 | libSystem、libresolv |
| `choices` | `f5105648f5faef7e48145e8cad15c24943febfd92ef8852dfea52082482a615a` | 2,446,562 | libSystem、libresolv |
| `collatz` | `37d8d94d0667174726558e7e3b4110299121873bb13bdba863c1197807794817` | 2,446,562 | libSystem、libresolv |
| `quote` | `75995f63eb2e4a5cf6990fc90723a4a6e531c992af51a47bcfe648c1774f3058` | 2,446,626 | libSystem、libresolv |

动态库的完整路径只有 `/usr/lib/libSystem.B.dylib` 和 `/usr/lib/libresolv.9.dylib`。因此，旧文档中的「完全静态链接」不准确：Go 代码与 Go 运行时已经链入 exe，但 macOS 程序仍动态加载这两个系统库。验收器用 `debug/macho` 读取实际 load command，遇到其他库会拒绝继续。

聚焦测试已执行：

```zsh
# 测试产物身份、传输、动态库白名单、沙箱规则、输出上限和项目根解析。
GOCACHE=.work/cache/independent-go-build .work/toolchain/go1.27.3-sdk-darwin-arm64/bin/go test -count=1 ./internal/independent ./tools/independent
```

```text
ok  github.com/Mahoo12138/tynel/internal/independent
ok  github.com/Mahoo12138/tynel/tools/independent
```

## 累计回归与测试边界

首次运行 `./tools/test` 时，`go test ./...` 不仅测试当前源码，还进入了 `out/clean/06-05-failed/source/` 和 `out/clean/06-05-invalid-root/source/` 两个历史 worktree。这些残缺树缺少 `examples/ir/add.nir.json`，因此 NIR 示例测试失败。这是测试入口把生成目录当成源码输入的问题，不是当前编译器语义回归。

`tools/test` 现在显式枚举 `./cmd/...`、`./compiler/...`、`./internal/...`、`./tests/...` 和 `./tools/...`。`go list` 核对得到 22 个当前源码包，没有 `out/` 下的包；2026-11-09 00:20 重跑累计测试，20 个含用例的包全部通过，`tools/release` 与 `tools/toolchain` 没有测试文件。`go vet ./...`、两个新增 shell 入口的 `zsh -n`、状态 JSON 解析和 `git diff --check` 也通过。

这次修正只限制测试发现范围，不删除或改写历史 worktree。独立运行仍未验证，累计测试通过不改变 06.06 的 blocked 状态。

## 受阻过程

```zsh
# 用新的证据目录执行验收；目录已存在时工具会拒绝复用旧结果。
./tools/independent-run --out .work/validation/06-06/final
```

实际结果：

```text
add：已核对并传输 sha256 28b6a55a…，未运行
choices：已核对并传输 sha256 f5105648…，未运行
collatz：已核对并传输 sha256 37d8d94d…，未运行
quote：已核对并传输 sha256 75995f63…，未运行
独立运行验收受阻：限制性 sandbox-exec 无法启动（退出 71）：
sandbox-exec: sandbox_apply: Operation not permitted
shell 退出状态：3
```

普通 `(allow default)` 配置可以启动 `/usr/bin/true`，但 `deny default`、命名限制配置以及最小的路径拒绝配置都在 `sandbox_apply` 阶段失败。失败发生在载荷启动前，不是某个 TypeScript 程序返回了错误。

`tools/independent-run` 先构建再执行验收器，保留退出码 3；没有使用 `go run` 把它折叠成通用退出码 1。预检失败时没有回退成普通 `exec`，也没有把 06.05 已经观察到的 `42` 等输出复制到本报告。

## 沙箱通过后将检查什么

`internal/independent` 已保存以下实际检查逻辑：

1. 用限制性配置启动 `/usr/bin/true`，确认沙箱本身可用。
2. 先在宿主读取 `forbidden/source-canary.ts`，再要求沙箱内 `/bin/cat` 读取同一文件失败；`sandbox_apply` 自身失败不算拒绝探针通过。
3. 宿主存在 Node 时，要求沙箱内读取 Node 失败；载荷只能执行自身，不能启动任何子进程。
4. 清空继承环境，只设置载荷内的 `HOME`、`TMPDIR`、`LC_ALL=C` 和不存在的 `PATH`。
5. 分别运行四个 exe，要求 stdout 逐字节等于独立期望、stderr 为空、退出 0、未超时且日志未截断。任一条件不符，报告为 `failed`。

这些逻辑有单元测试，但源码拒绝探针、Node 拒绝探针和四个沙箱运行本次均未执行，不能写成已经通过。

## 恢复条件

在未嵌套 Seatbelt 的 macOS arm64 终端，或另一台匹配的 macOS arm64 环境中，使用一个新的证据目录重跑：

```zsh
# 前置：06.05 的 outputs 仍在；host-run 必须是尚不存在的新目录。
./tools/independent-run --out .work/validation/06-06/host-run
```

只有命令退出 0、`host-run/report.json` 的 `status` 为 `passed`，并且四个 `artifacts[].run.passed` 都为 `true`，才能把 06.06 改为 `verified`。Linux 容器不能运行这些 Mach-O；仅隐藏 Node 的 `PATH`、仅复制 exe 后在普通进程运行、仅查看 Mach-O 文件头，都不能替代这项证据。
