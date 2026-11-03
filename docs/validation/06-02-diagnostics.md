# 06.02 验收记录：统一诊断与外部进程故障

本文件记录 06.02 的实际运行结果。被测代码是包含本文件的提交，其正文带 `Task: 06.02`；用 `git log --grep 'Task: 06.02'` 可以定位。以下命令在 2026-11-03 09:27–09:40（CST）执行。

## 环境

| 项目 | 值 |
| --- | --- |
| 机型 | Mac mini，机型标识 Mac16,10 |
| 芯片 | Apple M4，24 GB 内存 |
| 系统 | macOS 26.6.2，构建号 25G83 |
| 架构 | arm64 |
| 工具链 | 锁定的 go1.27.3（`.work/toolchain`），前端 typescript/v7.0.2（提交 `2bd066d8`） |
| 修改版编译器 | `.work/bridge/compile` |
| 命令行 | `bin/tynel`，构建时提交 `4a82b3a` |

`bin/tynel` 在提交前构建，所以 `version` 里的提交号是上一个开发提交；被测源码包含本文件所在的提交。验证夹具在 `.work/validation/06-02/`，不进入版本库。

跨文件夹具由两个文件组成：

```typescript
// main.ts
import { value } from "./math.ts";

console.log(value);
```

```typescript
// math.ts：把字符串赋给 number，TypeScript 报 TS2322。
export const value: number = "not a number";
```

## 正向验收

### 人类输出与 JSON 对应

```zsh
# 人类模式：诊断写到标准错误，标准输出为空。
./bin/tynel check --root .work/validation/06-02/cross-file main.ts

# 机器模式：失败时标准错误只有一个 JSON 信封。
./bin/tynel --diagnostics=json check --root .work/validation/06-02/cross-file main.ts
```

```text
/Users/mahoo/Projects/tynel/.work/validation/06-02/cross-file/math.ts:1:14: error TS2322: Type 'string' is not assignable to type 'number'.
```

```json
{
  "schemaVersion": 1,
  "ok": false,
  "exitCode": 1,
  "diagnostics": [
    {
      "category": "source",
      "categoryName": "源码错误",
      "code": "TS2322",
      "stage": "frontend",
      "message": "Type 'string' is not assignable to type 'number'.",
      "severity": "error",
      "file": "/Users/mahoo/Projects/tynel/.work/validation/06-02/cross-file/math.ts",
      "line": 1,
      "column": 14
    }
  ]
}
```

两次退出状态都是 1，标准输出都为空。人类行与 JSON 条目的文件、行、列、错误码和严重程度一致；位置在依赖文件 `math.ts`，不在入口 `main.ts`。修复前，同一夹具会进入合并降低并以 `lower.bad-type` 退出 5；现在源码错误先结束检查。

### 两种 JSON 输出并存

```zsh
# --json 是检查结果，--diagnostics=json 是失败信封，两路分别写到 stdout 和 stderr。
./bin/tynel check --root .work/validation/06-02/cross-file main.ts --json --diagnostics=json
```

标准输出是 `pipeline.Result`：`sourceSha256` 为入口快照，`diagnostics[0]` 带数字错误码、字节区间和行列。标准错误是与上文相同的诊断信封。退出状态 1。

### 跨文件诊断带位置

`bridge/tsgo/consumer` 现在检查程序里的全部非声明文件，并按诊断所属文件选择快照换算位置。`TestDependencyDiagnosticsAreReported` 确认依赖的 TS2322 出现在适配层诊断里；`TestMapDiagnosticsUsesDependencySnapshot` 确认它映射到依赖自己的行列；`TestDependencyTypeErrorKeepsSourcePosition` 走真实前端和命令行，要求人类输出含 `math.ts:1:14`、JSON 条目同样带位置，并明确不允许 `lower.bad-type`。

### 能力限制

```zsh
# any 超出原生子集，人类和 JSON 都退出 3。
./bin/tynel check bridge/tsgo/testdata/capability/any.ts
./bin/tynel --diagnostics=json check bridge/tsgo/testdata/capability/any.ts
```

人类输出两条 `cap.any`，分别在 `2:1` 和 `2:19`。JSON 信封的 `exitCode` 为 3，两条的 `category` 是 `capability`、`stage` 是 `capability`，文件、行列和消息与人类输出逐条对应。

### 进度与诊断分流

```zsh
# 移走宿主戳记，强制下一次调用重建前端宿主。
mv .work/frontend/bin/consumer.stamp.json .work/tmp/consumer.stamp.json
./bin/tynel check --root .work/validation/06-02/cross-file main.ts
```

人类模式的标准错误先出现进度行 `前端宿主需要重建（源码有变化或尚未构建）`，再出现 TS2322。再次移走戳记后改用 `--diagnostics=json`，标准错误只有一个 JSON 文档；`python3 -c 'json.load(...)'` 解析成功，没有进度文本。宿主重建的 stderr 没有被吞掉，而是按模式选择显示或丢弃。

### 运行退出状态

`TestRunPropagatesProgramExit` 用返回 7 的测试程序确认 `run` 原样传出 7。`TestJSONModeKeepsProgramOutputSeparate` 确认程序自己写的 `program-out` / `program-err` 分别留在标准输出和标准错误，成功运行时不产生诊断信封。编译器失败与程序退出因此可以按「是否有信封」区分。

## 负向验收

### 源码与环境

| 场景 | 命令 | 实际诊断 | 退出 |
| --- | --- | --- | --- |
| 入口不存在 | `check --root .work/validation/06-02/single missing.ts` | `project.missing`，阶段 `project` | 1 |
| 依赖环 | `check --root compiler/graph/testdata/cycle main.ts` | `graph.cycle`，三个文件的完整路径 | 1 |
| 缺导出 | `check --root compiler/graph/testdata/modules missing.ts` | `module.missing-export`，导出模块和导入模块的完整路径 | 1 |
| 依赖语法错误 | `check --root .work/validation/06-02/syntax-file main.ts` | `math.ts:1:30: error TS1109: Expression expected.`，阶段 `frontend` | 1 |
| 输出不可写 | `build --root examples/quote main.ts -o .work/validation/06-02/blocked/app` | `build.output-unwritable`，阶段 `build`，原因含 `not a directory` | 4 |

依赖语法错误这条路径还确认了失败时的模块探测不会反过来覆盖前端结论：`TestDependencySyntaxErrorNotMaskedByModuleProbe` 要求保留 `math.ts` 的 TS1109，并且输出里不能出现 `host.facts-failed`、`facts.bad-position` 或 `lower.bad-type`。输出失败没有生成产物，也没有把目录错误写成源码类型错误。缺导出和依赖环都保留完整文件路径；缺导出的消息同时指出 `a.ts` 没有导出和 `missing.ts` 在导入。

### 外部进程故障

这些路径由单元测试的模拟夹具覆盖，部分需要构造外部工具状态，不能靠正常工具链复现：

| 故障 | 测试 | 实际分类 |
| --- | --- | --- |
| 工具崩溃 | `TestJSONToolFailureIsInternalNotSource` | `internal`，阶段 `backend`，原因保留 `signal: killed` |
| 启动超时或 deadline | `TestRunClassifiesToolLaunchFailureAsEnvironment` | `environment`，`build.tool-missing`，原因保留 `deadline exceeded` |
| 找不到链接器 | `TestMissingIsEnvironment` | `environment`，阶段 `toolchain`，消息含工具名 `link` |
| 子进程按类别退出 | `TestAnalyzePreservesChildCategoryAndStderr` | 1/2/3/4/5 分别映射为 source/usage/capability/environment/internal，stderr 保留 |
| 链接失败 | `TestRunDoesNotExecuteAfterBuildFailure` | `internal`，原因保留 `link: undefined reference`，不执行旧产物 |
| 输出不可写 | `TestJSONOutputFailureIsEnvironment` | `environment`，阶段 `build` |
| 程序自己退出 | `TestRunPropagatesProgramExit` | 原样传出 7，不写诊断信封 |
| 程序输出分离 | `TestJSONModeKeepsProgramOutputSeparate` | 程序 stdout/stderr 原样保留 |

前端宿主无法启动、无法启动构建工具、构建工具崩溃和输出发布失败分别走环境缺失或内部故障；没有一条会被改写成源码错误。

## 累计回归

| 检查 | 命令 | 结果 |
| --- | --- | --- |
| 全部 Go 测试 | `./tools/test` | 17 个包通过 |
| bridge 后端 | `./bridge/go/check` | 13 项通过 |
| 前端宿主 | `./bridge/tsgo/check` | 96 项通过，末尾 `全部通过` |
| 静态检查 | `go vet ./...` | 无输出，退出 0 |

原始日志保存在 `.work/validation/06-02/`：`test.log`、`bridge-go.log`、`bridge-tsgo.log`、`vet.log`，以及每个 CLI 场景的 `.out`、`.err`、`.code`。强制重建的两次输出是 `rebuild-human.*` 和 `rebuild-json.*`。

## 未覆盖

- 只验证 `darwin/arm64`；`windows/amd64` 仍是计划目标。
- 没有验证从网络或空环境重新准备工具链；那是 06.05 的范围。
- 没有验证发行目录搬移、独立运行或完整故障矩阵；分别属于 06.04、06.06 和 06.07。
- `tynel` 不会主动终止挂起的外部工具。这里验证的是已经发生的启动 deadline 如何分类，不是统一的执行超时策略。
- 声明库诊断只验证了保留路径、不硬套行列，没有验证声明库内部的源码位置。
- `check --json` 的原始结果不是诊断信封；两种机器格式的字段不同，脚本需要按用途选择。
