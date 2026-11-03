# 06.03 验收记录：构建事务、并发与缓存策略

本文件记录 06.03 的实际运行结果。被测代码是包含本文件的提交，其正文带 `Task: 06.03`；用 `git log --grep 'Task: 06.03'` 可以定位。以下命令在 2026-11-03 23:58–2026-11-04 00:06（CST）执行。

## 环境

| 项目 | 值 |
| --- | --- |
| 机型 | Mac mini，机型标识 Mac16,10 |
| 芯片 | Apple M4，24 GB 内存 |
| 系统 | macOS 26.6.2，构建号 25G83 |
| 架构 | arm64 |
| 工具链 | 锁定的 go1.27.3（`.work/toolchain`），前端 typescript/v7.0.2（提交 `2bd066d8`） |
| 修改版编译器 | `.work/bridge/compile`，sha256 前缀 `7a75b4ac` |
| 命令行 | `bin/tynel`，构建时提交 `e104608` |

`bin/tynel` 在 06.03 代码之前构建，所以 `version` 里的提交号仍是 06.02 的 `e104608`；被测源码包含本文件所在的提交。验证夹具在 `.work/validation/06-03/`，不进入版本库。

## 夹具

入口只打印一个数，便于观察产物是否被替换：

```typescript
// main.ts
console.log(42);
```

失败构建使用同目录的另一份源码：

```typescript
// bad.ts：把字符串赋给 number，TypeScript 报 TS2322。
const value: number = "not a number";
console.log(value);
```

## 正向验收

### 同输入重复构建

```zsh
# 连续构建两次到同一个输出，记录两次的身份和日志。
./bin/tynel build --root .work/validation/06-03/project main.ts \
  -o "$PWD/.work/validation/06-03/out/app"
./bin/tynel build --root .work/validation/06-03/project main.ts \
  -o "$PWD/.work/validation/06-03/out/app"
```

两次输出完全一致：

```text
已生成 /Users/mahoo/Projects/tynel/.work/validation/06-03/out/app（2429938 字节）
  源码     main.ts（7df141f2f2a2）
  输入     4f8fa3fea486
  身份     7dd4e7a4a217
  编译器   7a75b4acf10b
  构建记录 /Users/mahoo/Projects/tynel/.work/validation/06-03/out/app.build.json
```

产物哈希为 `7e591c4310fac3aab7baeb4493c4d6eee0cc3ba90e3418cadf5e6241eac6ae7b`，记录文件哈希为 `eeee3fc9440bbbb2b1a2d7a79da20850fafc3562a241605a0b777d06283de3c0`。`identity.hash` 是 `7dd4e7a4a217654fb1ad6715fb1e57deeef488065271880bc1d7a070d30f6a70`，标签是 `tynel_1cc5d334a74ffe0d34d85034bece52a6a89f07a40a2e80c5b819c0900a51283d`。同一输入没有因为第二次构建得到不同产物或记录。

### 产物与记录内容一致

```zsh
# 独立解析记录并重新计算产物哈希，不依赖构建进程的返回对象。
python3 - "$PWD/.work/validation/06-03/out/app" <<'PY'
import hashlib, json, os, sys
app = sys.argv[1]
record = json.load(open(app + ".build.json", encoding="utf-8"))
actual = hashlib.sha256(open(app, "rb").read()).hexdigest()
print(record["output"]["sha256"] == actual)
print(record["output"]["bytes"] == os.path.getsize(app))
PY
```

```text
output.sha256 7e591c4310fac3aab7baeb4493c4d6eee0cc3ba90e3418cadf5e6241eac6ae7b
actual.sha256 7e591c4310fac3aab7baeb4493c4d6eee0cc3ba90e3418cadf5e6241eac6ae7b
output.bytes 2429938
match True
```

运行产物输出 `42`。记录里的 `output.sha256`、`output.bytes` 与实际文件一致；`internal/build.VerifyPair` 也做同一项复核，发布事务在删除备份之前调用它。

### 失败重建保留旧产物

```zsh
# 先用 main.ts 成功构建，再用 bad.ts 构建到同一输出。
./bin/tynel build --root .work/validation/06-03/project bad.ts \
  -o "$PWD/.work/validation/06-03/out/app"
```

```text
/Users/mahoo/Projects/tynel/.work/validation/06-03/project/bad.ts:1:7: error TS2322: Type 'string' is not assignable to type 'number'.
```

退出状态为 1。失败前后的产物哈希都是 `7e591c4310fac3aab7baeb4493c4d6eee0cc3ba90e3418cadf5e6241eac6ae7b`，`kept=yes`。源码错误在发布之前结束，旧产物和旧记录没有被改动。

### 重新构建不读旧归档目录

`internal/build` 的 `TestRunIgnoresLegacyArchiveDirectories` 在项目根放入 `.tynel/bridge/compile` 和 `.upstream/bridge/compile` 两个诱饵文件，再完成一次构建。记录里的编译器仍是 `.work/bridge/compile`，快照仍是 `.work/build/snapshots/...`，没有读取诱饵。

## 负向验收

### 同一输出被并发写入

```zsh
# 先由另一个进程持有与构建相同的 flock，再调用 CLI。
./bin/tynel build --root .work/validation/06-03/project main.ts \
  -o "$PWD/.work/validation/06-03/out/app"
```

```text
tynel: 环境缺失 (build.output-busy): 另一个构建正在写 /…/.work/validation/06-03/out/app；等它结束或换一个输出路径
  阶段: build
```

退出状态为 4。CLI 没有调用后端，也没有改动原产物；持锁进程结束后同一输出可以继续构建。

### 发布事务的故障路径

这些路径需要精确控制改名或 context，使用 `internal/build` 的模拟夹具：

| 场景 | 测试 | 实际结果 |
| --- | --- | --- |
| 记录改名失败，已有旧产物和旧记录 | `TestCommitRestoresOldPairOnRecordFailure` | 返回错误；旧产物、旧记录恢复；无 `.pending`/`.bak` |
| 记录改名失败，目标原本没有产物 | `TestCommitLeavesNoArtifactWhenRecordFailsWithoutOldPair` | 返回错误；不留下无记录产物 |
| 只移动了旧产物、旧记录还没移动 | `TestRecoverKeepsOldRecordWhenOnlyArtifactMoved` | 恢复旧产物，旧记录不被误删 |
| 中断后只剩新产物、没有新记录 | `TestRecoverRollsBackInterruptedPublish` | 恢复上一对成功文件 |
| 两次改名都完成但未清理 | `TestRecoverKeepsCompletedPair` | 新产物与新记录一致，保留新的一对 |
| 后端运行期间取消 | `TestRunStopsOnCancellation` | `build.canceled`，旧产物不变，无临时文件 |
| 后端运行期间 `go` 被替换 | `TestRunRechecksGoTool` | `build.input-changed`，不发布 |
| 同一输出已有构建锁 | `TestRunRejectsConcurrentOutput` | `build.output-busy`，退出 4，后端调用 0 次 |
| 记录存在但产物被改动 | `TestVerifyPairRejectsMismatchedRecord` | `build.record-mismatch`，不把这次构建当成成功 |
| 成功或失败后再次构建 | `TestRunReleasesLockAfterSuccessAndFailure` | 锁已释放，后续构建成功 |
| 构建失败后的 `run` | `TestRunDoesNotExecuteAfterBuildFailure` | 执行次数 0，目标位置的旧文件保持原样 |

### 发布前取消

`TestRunStopsOnCancellation` 使用实现了 `ContextRunner` 的测试替身：后端启动后阻塞，测试取消 context，构建返回 `build.canceled`，不进入发布。真实 `toolchain.Exec` 用 `exec.CommandContext` 终止子进程；这条真实进程路径由 `go test` 的构建调用间接覆盖，但没有用交互式 Ctrl-C 做端到端计时。

## 累计回归

| 检查 | 命令 | 结果 |
| --- | --- | --- |
| 全部 Go 测试 | `./tools/test` | 17 个包通过 |
| bridge 后端 | `./bridge/go/check` | 13 项通过 |
| 前端宿主 | `./bridge/tsgo/check` | 96 项通过，末尾 `全部通过` |
| 静态检查 | `go vet ./...` | 无输出，退出 0 |

原始日志保存在 `.work/validation/06-03/`：`build-1.log`、`build-2.log`、`failed-build.log`、`busy.log`、`hashes.txt`、`pair.txt`、`old-artifact.txt`、`run.log`。

## 未覆盖

- 只验证 `darwin/arm64`；非 Unix 平台会以 `build.lock-unsupported` 明确拒绝同一输出锁，没有跨平台锁实现。
- 中断恢复由测试在进程内构造 `.pending`、备份和新文件来验证，没有真的 `kill -9` 构建进程后重启；恢复逻辑本身已执行。
- 没有验证网络文件系统、跨文件系统改名、磁盘写满或断电后的持久性；这些环境下的失败会走诊断和回滚，但没有实测。
- 没有业务级产物缓存，因此没有缓存命中率或跨机器缓存复用数据；这里只验证同一输入重复构建和工具变化失效。
- 没有做发行目录搬移或独立机器运行；分别属于 06.04 和 06.06。
