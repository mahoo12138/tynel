# 06.06 验收记录：验证生成物可以独立运行

状态：**passed**，06.06 标为 `verified`。2026-11-09 11:17（CST）在普通 macOS 终端的限制性 Seatbelt 环境中，四个载荷全部退出 0、stdout 逐字节匹配，源码与 Node 拒绝探针均生效。先前同一会话内的 `sandbox_apply` 受阻记录保留在文末，但不再代表当前结果。

## 环境与被测输入

| 项目 | 实际值 |
| --- | --- |
| 系统 | macOS 26.6.2，Darwin 25.6.0，`arm64` |
| 硬件 | Apple M4 |
| 沙箱入口 | `/usr/bin/sandbox-exec` |
| 执行方式 | 未嵌套 Seatbelt 的普通终端 |
| 产物目录 | `out/clean/06-05/outputs/` |
| 实际报告 | `.work/validation/06-06/host-run/report.json` |
| 报告生成时间 | 2026-11-09T11:17:53+08:00 |
| 被测产物 | 06.05 提交 `236cbe63c37884463f78f6e79b06807d8372c03b` 的四个 exe |
| 验收器 | 实现提交 `96545a2`，入口和检查逻辑在本次运行前已落地 |

验收命令：

```zsh
# 新目录只包含本次传输的 exe；已存在目录会被拒绝，避免复用旧结果。
./tools/independent-run --out .work/validation/06-06/host-run
```

```text
add: 退出 0, passed=true
choices: 退出 0, passed=true
collatz: 退出 0, passed=true
quote: 退出 0, passed=true
独立运行验收通过: 4 个程序
```

## 产物身份与运行依赖

输入 exe 先与构建记录核对 sha256 和字节数，再复制到 `payload/`；传输后的 exe 重新计算哈希。`payload/` 的文件只有四个 exe，没有 `.ts`、NIR、Go SDK、Node 或编译器发行目录。

| 程序 | 产物 sha256 | 字节 | 动态库 |
| --- | --- | ---: | --- |
| `add` | `28b6a55a9aa3cf2cb6a0e84d08d345086e10ff4f2ad50177a7449864a073aee0` | 2,446,530 | libSystem、libresolv |
| `choices` | `f5105648f5faef7e48145e8cad15c24943febfd92ef8852dfea52082482a615a` | 2,446,562 | libSystem、libresolv |
| `collatz` | `37d8d94d0667174726558e7e3b4110299121873bb13bdba863c1197807794817` | 2,446,562 | libSystem、libresolv |
| `quote` | `75995f63eb2e4a5cf6990fc90723a4a6e531c992af51a47bcfe648c1774f3058` | 2,446,626 | libSystem、libresolv |

动态库的完整路径只有 `/usr/lib/libSystem.B.dylib` 和 `/usr/lib/libresolv.9.dylib`。Go 代码和 Go 运行时已经链入 exe，但 macOS 系统库仍是动态依赖；不能把产物写成“完全静态链接”。验收器用 `debug/macho` 读取实际 load command，遇到白名单之外的库会拒绝继续。

## 隔离边界

每个载荷使用单独的 Seatbelt 配置和最小环境：

- 拒绝读取和写入项目根、用户目录、`/opt/homebrew`、`/usr/local`，只放行新 `payload/`；
- 全局禁止启动子进程，只允许当前载荷自身；
- 继承环境被清空，只设置载荷内的 `HOME`、`TMPDIR`、`LC_ALL=C` 和 `PATH=/nonexistent`；
- 宿主 Node 为 `/Users/mahoo/.nvm/versions/node/v22.14.0/bin/node`，同时被显式拒绝；
- 每个载荷限制 10 秒，stdout 或 stderr 超过 1 MiB 会使验收失败。

| 探针 | 预期 | 实际 |
| --- | --- | --- |
| 限制性沙箱启动 `/usr/bin/true` | 退出 0 | 退出 0 |
| 沙箱内读取 `forbidden/source-canary.ts` | 拒绝 | 退出 1，`Operation not permitted` |
| 沙箱内读取宿主 Node | 拒绝 | 退出 1，`Operation not permitted` |

源码探针在执行前先由宿主成功读取，排除“文件本来不存在”的假拒绝。载荷运行结果：

| 程序 | 退出 | stdout | stderr | 超时或截断 |
| --- | ---: | --- | --- | --- |
| `add` | 0 | `42\n` | 空 | 否 |
| `choices` | 0 | `-1\n0\n1\n1\nfalse\n3\ntrue\n` | 空 | 否 |
| `collatz` | 0 | `111\n111\n8\n8\n0\n` | 空 | 否 |
| `quote` | 0 | `90\n10\n100\n` | 空 | 否 |

同一台物理 Mac 上完成的隔离方式是 Seatbelt 进程沙箱，不是另一台虚拟机。它证明了这批 exe 在无法访问项目源码、NIR、Node 和编译器目录的进程中仍能运行；没有声称已经验证另一台机器或另一个操作系统。

## 先前受阻与恢复

2026-11-08 的首次执行在工具宿主自带的嵌套沙箱中运行，限制性配置在 `sandbox_apply` 阶段退出 71。验收器当时按设计停止，四个载荷没有运行，节点标为 `blocked`，没有降级为普通 `exec`。该受阻报告仍保存在 `.work/validation/06-06/final/report.json`。

在普通终端重跑后，限制性沙箱与全部探针通过，说明先前失败属于执行环境边界，不是 TypeScript 程序或原生后端错误。受阻记录没有删除，便于区分两次环境条件。

## 累计回归与测试边界

首次运行 `./tools/test` 时，`go test ./...` 进入了 `out/clean/06-05-failed/source/` 和 `out/clean/06-05-invalid-root/source/` 两个历史 worktree，因残缺树缺少 `examples/ir/add.nir.json` 而失败。`tools/test` 随后显式枚举 `./cmd/...`、`./compiler/...`、`./internal/...`、`./tests/...` 和 `./tools/...`，避免把生成目录当作当前源码。

```zsh
# 当前源码的累计测试。
./tools/test
```

```text
测试通过：20 个包
```

`go vet ./...`、新增 shell 入口与修改后的测试入口的 `zsh -n`、状态 JSON 解析和 `git diff --check` 同时通过。20 个包指含测试文件的包；`tools/release` 与 `tools/toolchain` 报告 `[no test files]`。

## 结论与范围

06.06 的交付条件已满足：多个不同源程序在隔离环境中给出规定输出与顺序，源码、NIR、Node 和编译器目录访问被实际拒绝，系统动态库依赖已核对。后续 06.07 已在最终源码上通过完整能力与故障矩阵，见[06.07 验收记录](06-07-mvp.md)；06 主节点仍需完成第六篇笔记，现阶段不创建 `mvp/06-compiler-mvp` 标签。

本记录只覆盖 `darwin/arm64` 和四个 06.05 示例，不声明其他 macOS 版本、其他程序或交叉平台已经通过。
