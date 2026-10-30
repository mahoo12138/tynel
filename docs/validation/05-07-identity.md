# 05.07 验收记录：依赖变化与构建身份

本文件记录 05.07 的实际运行结果。被测代码是包含本文件的提交，其正文带 `Task: 05.07`；用 `git log --grep 'Task: 05.07'` 可以定位。以下命令在 2026-10-30 16:40–16:47（CST）执行。

## 环境

| 项目 | 值 |
| --- | --- |
| 机型 | Mac mini，机型标识 Mac16,10 |
| 芯片 | Apple M4，10 核（4 性能核 + 6 能效核），24 GB 内存 |
| 系统 | macOS 26.6.2，构建号 25G83 |
| 架构 | arm64 |
| 工具链 | 锁定的 go1.27.3（`.work/toolchain`），前端为 typescript/v7.0.2 |
| 修改版编译器 | `.work/bridge/compile`，sha256 前缀 `7a75b4ac` |
| 对照运行时 | Node v22.14.0（本节点不新增 Node 用例） |

## 夹具

验收目录不进入版本库：`.work/validation/05-07/project`。入口 `main.ts` 同时有值依赖和纯类型依赖：

```typescript
// math.ts
export function sumTo(n: number): number {
  let sum = 0;
  let i = 1;
  while (i <= n) {
    sum = sum + i;
    i = i + 1;
  }
  return sum;
}

// types.ts
export type Amount = number;

// main.ts
import { sumTo } from "./math.ts";
import type { Amount } from "./types.ts";

const n: Amount = 10;
console.log(sumTo(n));
```

`main.ts`、`math.ts`、`types.ts` 三个文件参与依赖图；`types.ts` 只有类型边。相同内容的回归也写进了 `cmd/tynel/identity_test.go`，每次测试在 `.work` 下新建临时目录，不修改上面的验收目录。

## 正向验收

### 基线构建

```zsh
# 从项目根构建；输出用绝对路径，避免把工作目录混进产物路径。
./bin/tynel build --root .work/validation/05-07/project main.ts \
  -o "$PWD/.work/validation/05-07/app"
./.work/validation/05-07/app
```

```text
已生成 /Users/mahoo/Projects/tynel/.work/validation/05-07/app（2429970 字节）
  源码     main.ts（e2147837c982）
  输入     5c11711bca81
  身份     b867d5c8fe95
  编译器   7a75b4acf10b
  构建记录 /Users/mahoo/Projects/tynel/.work/validation/05-07/app.build.json
55
```

记录里的身份：

| 字段 | 值 |
| --- | --- |
| `identity.hash` | `b867d5c8fe95071296f37b164135843bca92b87b71d202e6b783e27ad6d2796d` |
| `identity.artifactSha256` | `e8282b8c9261790045f3e8d3dcf7b703f2e6f8ffdf67825248f75f9b5e56bbe7` |
| `tools.buildTag` | `tynel_e8282b8c9261790045f3e8d3dcf7b703f2e6f8ffdf67825248f75f9b5e56bbe7` |
| `identity.files` | `main.ts`、`math.ts`、`types.ts` 三个路径及内容哈希 |
| `output.sha256` | `7f80fd80c5a2740fae97a40f1579f6aa545116d1cff4158e85a6319ef211c25c` |

`identity.files` 与 `dump graph` 的文件列表使用同一套路径和哈希；`identity.hash` 与 `graphHash` 不是同一个值：前者还包含 NIR、配置和工具链。

### 相同完整输入

```zsh
# 相同输入再构建一次，比较记录和产物。
./bin/tynel build --root .work/validation/05-07/project main.ts \
  -o "$PWD/.work/validation/05-07/app"
shasum -a 256 .work/validation/05-07/app
```

两次构建的四项比较全部相等：`identity.hash`、`artifactSha256`、`tools.buildTag`、`output.sha256`。产物哈希仍为 `7f80fd80c5a2…`。

### 修改辅助函数

只改 `math.ts` 的返回值，入口文件和类型文件不动：

```zsh
# 把 return sum; 改成 return sum + 1;
sed -i '' 's/  return sum;/  return sum + 1;/' .work/validation/05-07/project/math.ts
./bin/tynel build --root .work/validation/05-07/project main.ts \
  -o "$PWD/.work/validation/05-07/app"
./.work/validation/05-07/app
```

| 项 | 修改前 | 修改后 |
| --- | --- | --- |
| `identity.hash` | `b867d5c8fe95…` | `6c0ac52d7a11…` |
| `artifactSha256` | `e8282b8c9261…` | `3db29613582f…` |
| `input.sha256` | `5c11711bca81…` | `40eebcfcc251…` |
| `output.sha256` | `7f80fd80c5a2…` | `8064bf7e9038…` |
| 运行输出 | `55` | `56` |

辅助函数变化进入了 NIR，产物输入身份和 build tag 都变化。这里没有复用旧对象；如果是旧缓存误命中，运行结果仍会是 `55`。

### 修改纯类型依赖

把 `Amount` 从 `number` 改成 `boolean`：

```zsh
sed -i '' 's/export type Amount = number;/export type Amount = boolean;/' \
  .work/validation/05-07/project/types.ts
./bin/tynel build --root .work/validation/05-07/project main.ts \
  -o "$PWD/.work/validation/05-07/app"
```

```text
…/project/main.ts:4:7: error TS2322: Type 'number' is not assignable to type 'boolean'.
…/project/main.ts:5:19: error TS2345: Argument of type 'boolean' is not assignable to parameter of type 'number'.
```

退出状态为 1。检查确实重新发生；旧产物 `8064bf7e9038…` 和旧记录 `92f3cd8417e8…` 都没有被改动。

纯类型变化也可以通过而不改变 NIR。例如只给 `types.ts` 追加一行注释，重新构建后 `identity.hash` 从 `7030c7c13471…` 变成 `8d81fb3a3dbf…`，但 `artifactSha256`、build tag、`input.sha256` 和产物哈希都不变，运行仍是 `56`。这时复用目标代码是正确的：生成的程序没有变化；改变的是这次构建的完整输入记录。

### 修改导出

把 `export function sumTo` 改成 `export function sum`，入口仍导入旧名字：

```text
…/project/main.ts:1:10: error TS2724: '"./math.ts"' has no exported member named 'sumTo'. Did you mean 'sum'?
```

退出状态为 1，旧产物 `8064bf7e9038…` 保持不变。把入口的导入和调用改成 `sum` 后重新构建，输出仍为 `56`，身份从 `6c0ac52d7a11…` 变为 `dd05f1cd649e…`。

### 移动文件

把 `math.ts` 改名为 `arithmetic.ts`，并更新入口的相对导入：

```zsh
mv .work/validation/05-07/project/math.ts .work/validation/05-07/project/arithmetic.ts
sed -i '' 's#\./math\.ts#./arithmetic.ts#' .work/validation/05-07/project/main.ts
./bin/tynel build --root .work/validation/05-07/project main.ts \
  -o "$PWD/.work/validation/05-07/app"
```

```text
身份 7030c7c13471
运行 56
文件 arithmetic.ts、main.ts、types.ts
```

身份从 `dd05f1cd649e…` 变为 `7030c7c13471…`；`identity.files` 里不再有 `math.ts`，新路径 `arithmetic.ts` 的内容哈希为 `fa92eb24d5bd…`。旧文件没有被当成新模块继续使用。

### 从另一个工作目录构建

```zsh
cd /tmp
/Users/mahoo/Projects/tynel/bin/tynel build \
  --root /Users/mahoo/Projects/tynel/.work/validation/05-07/project main.ts \
  -o /Users/mahoo/Projects/tynel/.work/validation/05-07/app-from-tmp
```

身份、产物输入身份、build tag 和产物哈希都与上一节完全相同（`7030c7c13471…`、`30d818b1f865…`、`tynel_30d818…`、`c2d2d2cbf2ce…`）。`--root` 固定源码根，工作目录不进入身份。

## 负向验收

| 情况 | 拒绝点 | 结果 |
| --- | --- | --- |
| 删除 `arithmetic.ts` 后构建 | `project.missing` | 退出 1；旧产物 `c2d2d2cbf2ce…` 与旧记录 `1d5ef7efb917…` 不变 |
| 图建立后替换文件再调用 `build.Run` | `build.file-changed` | 不启动后端；旧产物与旧记录不变 |
| 逐文件身份与 `graphHash` 不一致 | `build.identity-mismatch` | 不启动后端；不产出文件 |
| 有 `Files` 没有 `GraphHash`，或有 `GraphHash` 没有 `Files` | `build.identity-mismatch` | 身份不完整，拒绝构建 |
| 后端运行期间 NIR 快照被替换 | `build.input-changed` | 不发布临时产物；旧产物与旧记录不变 |

后四项由 `internal/build` 的单元测试覆盖：

```zsh
./.work/toolchain/go1.27.3-sdk-darwin-arm64/bin/go test -count=1 -v \
  -run 'TestIdentity|TestRunRejectsStaleDependency|TestRunRejectsGraphIdentityMismatch|TestRunRejectsIdentityWithoutItsCompanion|TestRunRejectsInputReplacedDuringBuild' \
  ./internal/build
```

```text
--- PASS: TestIdentityIsStableAndContentAddressed
--- PASS: TestIdentityCoversToolchainContents
--- PASS: TestRunRejectsStaleDependency
--- PASS: TestRunRejectsGraphIdentityMismatch
--- PASS: TestRunRejectsIdentityWithoutItsCompanion
--- PASS: TestRunRejectsInputReplacedDuringBuild
ok  github.com/Mahoo12138/tynel/internal/build
```

`TestRunRejectsInputReplacedDuringBuild` 让假后端在运行期间改写 `TYNEL_NIR` 指向的快照。构建在发布前复核失败，临时文件被删除，目标位置仍是上一次成功的产物和记录。

## 实现要点

- `compiler/graph.HashFiles` 公开稳定的图哈希算法，`graph.Load` 与构建身份共用它。
- `internal/build/identity.go` 定义逐文件身份、配置和工具链身份，计算 `identity.hash` 与 `identity.artifactSha256`。
- 构建开始前重新读取全部源码文件，与图固定下来的哈希比较；开始时依赖已变化就不启动后端。
- 构建成功后、发布前再次哈希 NIR 快照、修改版编译器、工具包装和固定支持目录；任何一项变化都不发布。
- `cmd/tynel` 传入 `ProjectRoot`、`Entry`、`GraphHash` 和 `Files`，路径保持相对 `--root`；同一个项目换工作目录得到同一身份。
- `tools.buildTag` 使用完整的 `artifactSha256`，不再使用 8 位短前缀。NIR、配置或工具链变化都会换标签；同一份 NIR 的回放仍保持逐字节相同。
- `cmd/tynel/identity_test.go` 走真实前端、依赖图、合并降低和 Go 后端，覆盖改辅助函数、改类型、改导出、移文件、纯类型变化和缺依赖。

## 累计测试

| 入口 | 结果 |
| --- | --- |
| `./tools/test` | 17 个包通过，根模块 437 个测试（217 个顶层 + 220 个子测试） |
| `tests/source` | 160 个检查（7 个测试函数，153 个子测试） |
| `cmd/tynel` | 34 个测试（28 个顶层 + 6 个子测试） |
| `tests/native` | 12 个测试（9 个顶层 + 3 个子测试） |
| `./bridge/go/check` | 13 项全过 |
| `./bridge/tsgo/check` | 96 项全过 |
| `go vet ./...` | 无输出 |

## 未覆盖

- 没有跨机器、跨用户的缓存复用，也没有业务级产物缓存；`build`/`run` 每次都重新检查。
- 最终复核与后端读取之间仍有一个很小的替换窗口；本节点不把构建声明为密封沙箱。
- 只在 `darwin/arm64` 上验证。
- 纯类型变化导致的「同一 NIR、同一产物」只在本地测试和上面的注释实验中观察，没有扩展成跨平台差异测试。
