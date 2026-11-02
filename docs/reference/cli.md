# 命令行参考

本页说明 `tynel` 已经固定的命令契约：哪些是用户命令，哪些是开发调试接口，参数怎样解析，路径以什么为基准，以及失败时返回什么状态。命令集合在 06.01 固定；后续增加能力时沿用这里的入口和退出规则。

## 构建命令行本身

```zsh
# 在项目根目录构建开发用入口；需要 01.01 准备的隔离 Go。
./tools/build

# 显示程序、目标平台和锁定的配套工具身份。
./bin/tynel version
```

`bin/` 不进入版本库，换一份工作副本要重新构建。构建脚本使用 `.work/toolchain` 下的隔离 Go，不使用宿主安装。

## 用户命令

| 命令 | 作用 | 成功输出 |
| --- | --- | --- |
| `tynel version` | 显示程序版本、目标平台和配套工具身份 | 多行身份摘要 |
| `tynel check <file.ts> [--root <目录>] [--json]` | 类型检查、能力判断、降低并验证 NIR，不生成机器码 | 一行摘要；`--json` 时输出完整结果 |
| `tynel build <file.ts> -o <路径> [--root <目录>] [--target <平台>]` | 编译成原生程序，不运行 | 产物路径、源码与 NIR 哈希、构建记录位置 |
| `tynel run <file.ts> [--root <目录>] [--target <平台>]` | 编译并运行本次产物 | 程序自己的输出 |

```zsh
# 检查、编译、运行；示例在 examples/add.ts。
./bin/tynel check examples/add.ts
./bin/tynel build examples/add.ts -o out/add
./out/add
./bin/tynel run examples/add.ts

# 机器可读的检查结果。
./bin/tynel check examples/add.ts --json
```

`build` 的输出路径必填；`run` 的产物固定放在编译器检出目录的 `.work/run/app`，不接受 `-o`。`check` 不接受 `-o` 和 `--target`；三个命令都不忽略未知选项，遇到就按用法错误退出 2。

## 开发与调试命令

这些命令用于观察中间结果或回放后端，不是用户构建程序的必经步骤。

| 命令 | 作用 |
| --- | --- |
| `tynel dump facts\|nir\|graph\|modules\|init <file.ts> [--root <目录>]` | 输出前端事实、NIR、依赖图、符号表或初始化计划（JSON） |
| `tynel build-ir <NIR> -o <路径> [--target <平台>]` | 把一份 NIR 编成原生程序，不读 TypeScript |
| `tynel toolchain` | 列出 SDK 目录与 `go`、`compile`、`link`、`asm` 的实际路径 |
| `tynel toolchain which <名>` | 只解析一个工具，输出绝对路径 |
| `tynel help [命令]` | 总体用法或单个命令说明 |

`dump` 只写 JSON，不生成机器码，也不接受 `-o`、`--json` 或 `--target`。`build-ir` 不接受 `--root`：它不读取用户项目，输入和输出相对当前工作目录解释，工具链根由编译器自动定位。

`build-ir` 保留下来是因为它跳过了前端：手写 NIR、检查后端、复现产物都能用它，不必先有一份能通过类型检查的源码。

## 参数、路径与默认值

| 项目 | 规则 |
| --- | --- |
| 入口文件 | 相对 `--root` 解释；省略 `--root` 时使用编译器检出根，而不是当前工作目录 |
| `--root <目录>` | 用户项目根；可以写 `--root=<目录>`，同一个命令只能出现一次 |
| `-o <路径>` | `build` 和 `build-ir` 必填；相对当前工作目录解释，可以写 `-o=<路径>` |
| `--target <平台>` | 只影响 `build`、`run`、`build-ir`；省略时使用锁文件主目标 |
| `--json` | 只有 `check` 接受；把完整结果写到标准输出 |
| 重复选项 | `--root`、`-o`、`--target`、`--json` 重复出现按用法错误退出 2 |
| 未知选项 | 明确报出不认识的选项，不忽略、不传给其他工具 |

路径规则的可执行细节见[项目根与路径解析](project-paths.md)。源文件的显式相对导入仍按来源文件目录解析，`--root` 只限定许可根。

### 目标

首版只支持 `darwin/arm64`。`--target darwin/arm64` 是显式确认；省略时使用 `tools/toolchain/go.lock.json` 的主目标。请求其他平台按能力限制退出 3，并在消息中同时写出支持目标和请求目标：

```zsh
# 不支持的平台不会悄悄改成本机后端，也不会调用解释器。
./bin/tynel build examples/add.ts -o out/add --target windows/amd64
# tynel: 能力限制 (target.unsupported): 首版只支持 darwin/arm64；请求的目标是 "windows/amd64"…
```

## 一次编译经过哪些步骤

`check`、`build`、`run` 共用同一条前半程：

1. **解析入口**。按项目根把入口固定成绝对路径；没有 `--root` 时项目根是仓库根。
2. **准备前端宿主**。宿主是 `bridge/tsgo/consumer`，一个独立模块里的子进程。它缺失或与源码不一致时，命令行用隔离工具链重建它，进度写到标准错误。
3. **源码检查**。宿主把 TypeScript 诊断连同 UTF-8 字节区间与行列一起交回来。
4. **能力检查**。判断这段程序是否落在原生子集内，结论与源码错误分开。
5. **降低与验证**。事实降低成 NIR，再由 `nir.Verify` 检查引用、实参和类型。

`check` 到这里就停。`build` 与 `run` 继续：

6. **后端**。NIR 写成快照，交给修改版 Go 编译器，经 `-toolexec` 包装的 `compile` 生成目标文件，再由 Go 自己的 `link` 链接。
7. **发布**。产物先写到目标目录的临时文件，成功后才改名。`run` 在发布之后立即执行它。

## 运行依赖

要把两件事分开：

| 谁 | 需要什么 |
| --- | --- |
| 编译器本身（`tynel`） | 已准备的 `.work/toolchain`；前端源码树与模块缓存（首次构建宿主时）；`build`/`run` 还需要 `.work/bridge/compile` |
| 被编译出来的程序 | 无。它是静态链接的 Go 二进制，不依赖 Node、不依赖 Go 工具链，也不需要本项目 |

`check` 不需要后端：它不生成机器码，也不检查 SDK 或修改版编译器是否就绪。`check` 通过只说明这份程序可以进入后端，不保证 `build` 能成功——那要看工具链有没有准备好。

## check

```zsh
./bin/tynel check bridge/tsgo/testdata/add.ts
# ok: /…/bridge/tsgo/testdata/add.ts（2 个函数，NIR 已验证）

./bin/tynel check bridge/tsgo/testdata/bad.ts
# bridge/tsgo/testdata/bad.ts:2:7: error TS2322: Type 'string' is not assignable to type 'number'.

./bin/tynel check bridge/tsgo/testdata/capability/any.ts
# bridge/tsgo/testdata/capability/any.ts:1:1: 能力限制 cap.any: 函数 "identity" 的返回类型是 any 或 unknown…
```

默认把诊断写到标准错误，成功时在标准输出打一行摘要。`--json` 把完整结果写到标准输出：诊断、能力结论、函数数量与 `verified`。此时标准错误为空，进度与结果不混。`--json` 可以与 `--root` 同时使用；`check` 本身不接受 `-o` 或 `--target`。

## build 与构建记录

```zsh
./bin/tynel build --root examples/quote main.ts -o out/quote
```

入口有值导入时，`build` / `run` 会把依赖图里的模块合并成一个 NIR：函数名带模块前缀，模块体变成 `__init$N`，入口 `main` 按初始化顺序调用。产物仍然是一个原生程序，不会为每个模块生成 Go 文件。

成功后在同目录写 `<产物>.build.json`：

| 字段 | 内容 |
| --- | --- |
| `source` | 入口路径、sha256、字节数；从 NIR 构建时没有这一项 |
| `graphHash` | 入口和全部依赖的图哈希；从 NIR 构建时没有这一项 |
| `identity` | 完整输入身份：入口、全部类型/值依赖、NIR、配置和工具链；另含产物输入身份 `artifactSha256` |
| `input` | 实际被编译的 NIR 快照路径、sha256、字节数 |
| `toolchain` | 锁定的 Go 版本与目标平台 |
| `tools` | `go`、修改版 `compiler` 及其 sha256、`toolexec`、build tag、快照路径 |
| `output` | 产物路径、sha256、字节数 |

源码 → NIR → 产物是一条哈希链：`source.sha256` 对上入口，`identity.files` 对上全部类型和值依赖，`input.sha256` 对上降低结果，`output.sha256` 对上产物。完整身份还会在构建前后复核依赖与工具内容，规则见[构建身份与依赖变化](build-identity.md)。

产物与记录的一致性策略：

- 产物发布成功后才写记录；记录写不进去时产物会被删除。没有记录的产物无法复核来源，留着比没有更危险。
- 记录里的 `input.path` 指向 `.work/build/snapshots/` 下的快照，不是内存地址，也不是用户临时文件；快照按内容哈希命名，内容与名字不符时构建直接拒绝。
- `tools.buildTag` 使用完整的 `identity.artifactSha256`，覆盖 NIR、配置、工具链和固定支持内容。Go 的缓存键不含这些输入，标签让它们参与缓存区分；它不含源码文件列表，同一份 NIR 的回放因此仍能得到相同产物。

修改版编译器带戳记 `.work/bridge/stamp.json`。bridge 源码或 SDK 源码变了而编译器没重编时，构建按「编译器已过期」退出 4，不产出任何文件。

## run

```zsh
./bin/tynel run examples/add.ts
# 42
```

`run` 每次都重新编译，产物固定放在 `.work/run/app`，然后执行它。它不执行任何先前存在的文件：构建失败就直接返回，即使目标位置留着上一次的产物也不会被运行。

程序的标准输出、标准错误直接连到终端。程序退出状态原样作为 `run` 的退出状态——这与 `go run` 一致，也意味着程序返回 1 时看起来和源码错误一样。编译失败时仍用 1、3、4、5 这几类。

## dump

`dump` 的五个内容名只输出 JSON，没有进度日志。源码有错退出 1，超出原生子集退出 3，两种情况都不输出 JSON。`dump` 是调试接口，不是用户构建的必经步骤。

事实的字段、ID 与校验规则见[声明身份与事实](facts.md)，NIR 的格式见 [NIR 格式](nir.md)，依赖图见[模块图与依赖快照](module-graph.md)，模块符号表见[跨模块名称绑定](module-bindings.md)，初始化顺序见[模块初始化顺序](init-order.md)。

## 退出状态

| 状态 | 类别 | 典型原因 |
| --- | --- | --- |
| 0 | 成功 | 命令完成 |
| 1 | 源码错误 | 语法、类型、未定义名称、坏 NIR |
| 2 | 用法错误 | 未知命令、缺少参数、多余的参数、重复或未知选项、未知帮助主题、未知工具名 |
| 3 | 能力限制 | 写法合法但当前原生后端不支持，或请求了未实现的构建目标 |
| 4 | 环境缺失 | 不在项目目录内、工具链或前端源码未准备、编译器过期、输出目录不可写 |
| 5 | 内部故障 | 编译器自身、前端宿主或它调用的外部进程失败 |

`run` 例外：程序成功启动后，它的退出状态原样传出。

退出状态只由 `compiler/diagnostic` 的类别决定，命令实现不各自约定数字。几类失败分开，是为了让「工具没装好」不被读成「用户代码有错」。外部进程以非零状态退出时，诊断类别是内部故障，并保留该进程的 stderr 作为原因。

## 诊断与机器可读输出

- 标准输出只放命令的结果：`check` 的摘要或 JSON、`build` 的产物信息、`dump` 的 JSON、`run` 的被运行程序输出。
- 标准错误放诊断、进度和宿主的重建提示。
- `check --json` 是唯一会同时产出机器可读结果与退出状态的命令；其余命令的 JSON 只出现在 `dump`，且失败时不输出。

## 命令行与外部工具的职责

命令行负责四件事：解析参数、准备前端宿主、按结果选择诊断类别、把结果写到标准输出或标准错误。它不解析 TypeScript，也不解析 Go 的内部数据结构。

外部工具通过 `internal/toolchain` 的 `Runner` 接口调用：

- `LookPath` 解析工具路径；
- `Run` 启动工具并收集标准输出、标准错误和退出状态，非零退出作为数据返回，只有无法启动才算调用失败；
- `RunEnv` 在给定环境下做同样的事，构建与宿主重建需要它来设置 `TYNEL_NIR`、`GOMODCACHE` 等变量。

真实实现用操作系统进程；测试用固定工具表替换它，因此错误路径不依赖本机是否真的安装了某个工具。工具路径来自 01.01 写出的 `.work/toolchain/toolchain.json`，本包不重复推导目录命名。

## 身份从哪里来

`version` 的程序版本和提交由 `tools/build` 注入；Go 版本、源码提交、许可证、目标平台与 TypeScript 前端身份来自 `tools/toolchain/go.lock.json`，准备状态来自 `.work/toolchain/toolchain.json`。程序不内置第二份常量，避免与准备入口记录的版本不一致。

不在项目目录内运行时，`version` 仍输出程序版本，但把目标平台标为未知并以 0 退出；需要项目的命令按环境缺失以 4 退出。

## 首版能力清单

这张清单回答「现在能编译什么」，详细逐项状态见[操作支持表](support.md)。

| 类别 | 已实现 |
| --- | --- |
| 值 | `number`、`boolean`、`void`；输出支持有限值、负零、NaN 和正负无穷的固定拼写 |
| 声明 | 顶层函数、参数、顶层 `const`、块内 `const` 与带初始值的 `let` |
| 控制 | `if` / `else`、`while`、`break` / `continue`、直接与相互递归、短路、三元式 |
| 模块 | 显式相对 `.ts` 的命名导出/导入、导入重命名、`import type`、顶层 const 导出 |
| 构建 | `check`、`build`、`run`；一个入口加全部依赖合并成一个 NIR 和一个原生程序 |
| 目标 | `darwin/arm64` 静态链接产物；产物不需要源码、Node 或编译器目录 |

不包含：字符串、对象、数组、接口记录、联合类型、`any` 的装箱、类、闭包、异常、异步、`for` / `switch` / `try`、默认导入、命名空间导入、再导出、裸模块、CommonJS、动态导入、`tsconfig.json` 路径和跨平台交叉编译。`support.md` 中的「支持」表示有源码夹具和验证记录；「计划」不表示已经可用。
