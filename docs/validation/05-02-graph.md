# 05.02 验收记录：依赖图与源码快照

本文件记录 05.02 的实际运行结果。被测代码是包含本文件的提交，其正文带 `Task: 05.02`；用 `git log --grep 'Task: 05.02'` 可以定位。

## 环境

| 项目 | 值 |
| --- | --- |
| 机型 | Mac mini，机型标识 Mac16,10 |
| 芯片 | Apple M4，10 核（4 性能核 + 6 能效核），24 GB 内存 |
| 系统 | macOS 26.6.2，构建号 25G83 |
| 架构 | arm64 |
| 工具链 | 锁定的 go1.27.3（`.work/toolchain`），前端为 typescript/v7.0.2 |
| 修改版编译器 | `.work/bridge/compile`，sha256 前缀 `7a75b4ac` |
| 对照运行时 | Node v22.22.2，`node --experimental-strip-types <file>` |

## 夹具

- `compiler/graph/testdata/linear`：`main.ts → a.ts → b.ts`。
- `compiler/graph/testdata/diamond`：`main.ts` 分别导入 `a.ts`、`b.ts`，两者都导入 `shared.ts`；`a.ts` 还 `import type` 了 `types.ts`。
- `compiler/graph/testdata/cycle`：`main.ts → a.ts → b.ts → a.ts`。

## 正向验收

### 线性依赖

```text
$ ./bin/tynel dump graph --root compiler/graph/testdata/linear main.ts
…
"files": 3 个，3 条边中的 2 条
"hash": "2417bbf198bc…"
```

入口、`a.ts`、`b.ts` 全部载入，3 个文件、2 条边；`b.ts` 只出现一次。

### 菱形依赖与类型边

```text
$ ./bin/tynel dump graph --root compiler/graph/testdata/diamond main.ts
{
  "entry": "main.ts",
  "files": [
    {"path": "main.ts", "sha256": "ae5c8429…"},
    {"path": "a.ts", "sha256": "d0399273…"},
    {"path": "shared.ts", "sha256": "e85f9652…"},
    {"path": "types.ts", "sha256": "1fac0388…"},
    {"path": "b.ts", "sha256": "61c96df3…"}
  ],
  "edges": [
    {"from": "main.ts", "to": "a.ts", "specifier": "./a.ts", "typeOnly": false, "start": 45, "length": 9},
    {"from": "a.ts", "to": "shared.ts", "specifier": "./shared.ts", "typeOnly": false, "start": 86, "length": 14},
    {"from": "a.ts", "to": "types.ts", "specifier": "./types.ts", "typeOnly": true, "start": 129, "length": 13},
    {"from": "main.ts", "to": "b.ts", "specifier": "./b.ts", "typeOnly": false, "start": 73, "length": 9},
    {"from": "b.ts", "to": "shared.ts", "specifier": "./shared.ts", "typeOnly": false, "start": 41, "length": 14}
  ],
  "hash": "e580ced98df33889c729b749777e5e507a86b5c2ea455ce91027c94a92d8850b"
}
```

5 个文件、5 条边；`shared.ts` 在两条路径上都出现，但只保留一份快照。`a.ts → types.ts` 的 `typeOnly` 为 `true`，其余为 `false`。

### 图哈希进入构建记录

```text
$ ./bin/tynel dump graph examples/add.ts | python3 -c 'import json,sys; print(json.load(sys.stdin)["hash"])'
17b0c1024dc6ca133d8087aa5b45dbf33aca33adf3ca0fa64c5ed2febd779c55

$ ./bin/tynel build examples/add.ts -o /tmp/add-graph
$ python3 -c 'import json; print(json.load(open("/tmp/add-graph.build.json"))["graphHash"])'
17b0c1024dc6ca133d8087aa5b45dbf33aca33adf3ca0fa64c5ed2febd779c55
```

检查和构建都在分析前调用同一个 `loadGraph`；构建记录保存同一版图哈希，便于复核。

## 负向验收

### 环

```text
$ ./bin/tynel dump graph --root compiler/graph/testdata/cycle main.ts
tynel: 源码错误 (graph.cycle): 依赖环：a.ts -> b.ts -> a.ts
$ echo $?
1
```

### 缺失目标

```text
$ ./bin/tynel dump graph --root /tmp/tynel-missing-graph main.ts
tynel: 源码错误 (project.missing): 导入 nope.ts 不存在
$ echo $?
1
```

### tsgo 与项目解析不一致

`compiler/graph` 的 `TestLoadRejectsResolutionMismatch` 给出一条 `Resolved` 指向根外的导入，得到 `graph.resolution-mismatch`，退出 1。

### 不用正则代替语法树

导入发现走 `tyneladapter.Session.Imports`：tsgo AST 给出 `ImportDeclaration`、`ModuleSpecifier` 和 type-only 标记，`bridge/tsgo/check` 用 `-imports` 验证类型导入为 `true`、值导入为 `false`。源码里的 `import` 字样不会直接被字符串匹配。

## 本轮边界

- 只支持显式相对 `.ts` 导入；裸模块、目录导入、后缀推断仍拒绝。
- 依赖环本轮一律拒绝，包括只用于类型的环。
- 图已固定全部快照，但跨模块名称绑定和值导入尚未进入降低；`import` 在检查阶段仍会被能力检查拒绝，05.03/04 接上类型别名和名称绑定。
- 没有读 `tsconfig.json`。

## 累计测试

| 入口 | 结果 |
| --- | --- |
| `./tools/test` | 15 个包通过，根模块 411 个测试（195 个顶层 + 216 个子测试） |
| `tests/source` | 105 个用例（7 个测试函数，153 个子测试） |
| `cmd/tynel` | 27 个测试函数，6 个子测试 |
| `tests/native` | 12 个用例 |
| `./bridge/go/check` | 13 项全过 |
| `./bridge/tsgo/check` | 86 项全过 |
| `go vet ./...` | 无输出 |

## 未覆盖

- 没有跨模块名称绑定、类型别名导入和运行值导入。
- 没有读 `tsconfig.json`、裸模块或 `node_modules`。
- 只在 `darwin/arm64` 上验证。

## 交付后的类型环误判修复（2026-11-10）

本节记录 `graph.Load()` 把类型边误纳入运行时环检测的一次修复，执行时间是 2026-11-10 14:06 CST，代码基于提交 `0a2e597` 的工作树。环境：Mac mini（Apple M4，arm64）、macOS 26.6.2、隔离 go1.27.3、前端 typescript/v7.0.2。上文「本轮边界」中「依赖环一律拒绝，包括只用于类型的环」自本节起不再成立。

`Load()` 过去用同一份访问状态既做载入去重又做环检测，任何导入边指向正在访问的文件都报 `graph.cycle`。05.05 的 `modules.Plan()` 早已只沿值边排初始化顺序，类型边只记入 `TypeEdges`；两处规则不一致，导致「值依赖 + 反向类型依赖」和纯类型循环这类合法 TypeScript 在图阶段就被拒绝。

修复把载入与环检测拆成两步：所有边（含类型边）照常递归载入，`seen` 集合保证每个文件只载入一次；全部载入后由新增的 `checkValueCycle` 从入口只沿值边做 DFS，成环才报 `graph.cycle`，诊断码、源码错误类别和完整路径消息不变。只经类型边才能到达的值环同样不拒绝：这些模块不进入初始化顺序，与 `Plan()` 从入口只走值边的规则一致。

`compiler/graph/graph_test.go` 新增三个用例：`TestLoadAcceptsReverseTypeEdge`（入口值导入 `b.ts`，`b.ts` 用 `import type` 指回入口）、`TestLoadAcceptsTypeOnlyCycle`（`a.ts ↔ b.ts` 纯类型循环）、`TestLoadAcceptsValueCycleBehindTypeEdge`（入口只类型导入 `a.ts`，`a.ts ↔ b.ts` 是值环）。把 `graph.go` 临时换回 `HEAD` 的原始实现后，三个用例全部以 `graph.cycle` 失败，复现了误判：

```text
--- FAIL: TestLoadAcceptsReverseTypeEdge (0.00s)
    graph_test.go:135: 值依赖加反向类型边应能载入：源码错误 (graph.cycle): 依赖环：…/main.ts -> …/b.ts -> …/main.ts
--- FAIL: TestLoadAcceptsTypeOnlyCycle (0.00s)
    graph_test.go:160: 纯类型循环应能载入：源码错误 (graph.cycle): 依赖环：…/a.ts -> …/b.ts -> …/a.ts
--- FAIL: TestLoadAcceptsValueCycleBehindTypeEdge (0.00s)
    graph_test.go:187: 类型边背后的值环不影响入口初始化，应能载入：源码错误 (graph.cycle): 依赖环：…/a.ts -> …/b.ts -> …/a.ts
```

恢复修复后全部通过。端到端用真实前端复核（夹具在 `.work/`，不提交）：

```zsh
# 值依赖 + 反向类型依赖：a.ts 值导入 run、类型导入 B；b.ts 类型导入指回 a.ts。
# 类型别名落在子集内；构建成功并运行，输出 1。
./bin/tynel build --root .work/type-cycle a.ts -o out/type-cycle && ./out/type-cycle
# 输出：1，退出 0。

# 纯类型循环：a.ts 与 b.ts 互相 import type，入口只类型导入 a.ts。
# 构建成功并运行，输出 7（入口的 console.log），类型模块不执行。
./bin/tynel build --root .work/pure-type-cycle main.ts -o out/pure-type-cycle && ./out/pure-type-cycle
# 输出：7，退出 0。

# 值依赖环仍拒绝：完整路径消息，退出 1。
./bin/tynel dump graph --root compiler/graph/testdata/cycle main.ts
# tynel: 源码错误 (graph.cycle): 依赖环：/…/cycle/a.ts -> /…/cycle/b.ts -> /…/cycle/a.ts，退出 1。
```

`dump graph --root .work/type-cycle a.ts` 现在输出 2 个文件、3 条边，`b.ts → a.ts` 的 `typeOnly` 为 `true`。报告人给出的接口示例也能过图阶段，随后在能力层按既有边界报 `cap.interface`（接口不在子集内，退出 3），不再误报 `graph.cycle`。

累计验证：

| 入口 | 结果 |
| --- | --- |
| `./tools/test` | 20 个包全部通过，与修复前基线一致 |
| `go test -count=1 ./compiler/graph` | 11 个测试通过（含 3 个新增） |
| `go test -count=1 ./compiler/modules` | 10 个测试通过 |
| `go vet ./...` | 无输出 |
| `gofmt`（graph 包） | 无差异 |

环境备注：本机的 `.work/frontend` 适配包副本停留在上一提交之前，基线最初报 `host.stale-adapter`；按提示运行 `./tools/toolchain/prep --frontend-only` 重新同步后，基线 20 个包全过，再开始改动。

本次未覆盖：只在 `darwin/arm64` 上验证；接口声明仍在子集外，类型循环的端到端运行证据用类型别名夹具取得；Windows 平台的复核待补。
