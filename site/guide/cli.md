# CLI 使用

`tynel` 有四个用户命令：`version`、`check`、`build`、`run`。本页讲日常怎么用；完整的参数契约、路径规则与退出状态见[命令行 API](/reference/cli)。

## 检查：check

`check` 做类型检查、能力判断、降低并验证 NIR，不生成机器码，也不需要后端工具链就绪。

```zsh
# 成功时标准输出一行摘要，诊断走标准错误。
./bin/tynel check examples/add.ts
# ok: /…/examples/add.ts（2 个函数，NIR 已验证）

# 机器可读的完整结果：诊断、能力结论、函数数量与 verified。
./bin/tynel check examples/add.ts --json
```

`check` 通过只说明程序可以进入后端，不保证 `build` 成功——后者还要求工具链已准备好。

## 编译：build

```zsh
# -o 必填；产物路径相对当前工作目录解释。
./bin/tynel build examples/add.ts -o out/add

# 多文件项目用 --root 指定项目根，入口相对根解析。
./bin/tynel build --root examples/quote main.ts -o out/quote
```

成功后在同目录写 `<产物>.build.json` 构建记录：入口与依赖哈希、NIR 快照、工具链身份和产物哈希构成一条可核对的链。失败构建不覆盖旧的成功产物。

`build` 不运行程序。要运行，执行刚生成的文件：

```zsh
# 只有上一条构建成功，才运行这次的产物。
./out/quote
```

## 运行：run

```zsh
# 每次重新编译并执行本次产物；产物固定放在 .work/run/app，不接受 -o。
./bin/tynel run examples/collatz.ts
```

构建失败就直接返回，不会执行目标位置遗留的旧文件。程序启动后，它的退出状态原样作为 `run` 的退出状态。

## 观察中间结果：dump 与 build-ir

这些是开发调试接口，不是用户构建的必经步骤：

```zsh
# 五种 JSON 视图：前端事实、NIR、依赖图、符号表、初始化计划。
./bin/tynel dump facts examples/add.ts
./bin/tynel dump nir examples/add.ts
./bin/tynel dump graph --root examples/quote main.ts
./bin/tynel dump modules --root examples/quote main.ts
./bin/tynel dump init --root examples/quote main.ts

# 跳过前端，把一份 NIR 直接编成原生程序；相同 NIR 回放得到相同产物。
./bin/tynel dump nir examples/add.ts > out/add.nir.json
./bin/tynel build-ir out/add.nir.json -o out/add-replay
```

## 目标平台

首版只支持 `darwin/arm64`。请求其他平台按能力限制退出 3，不会悄悄改成本机后端：

```zsh
# 预期退出 3，消息同时写出支持目标与请求目标。
./bin/tynel build examples/add.ts -o out/add --target windows/amd64
```

## 机器可读诊断

全局选项 `--diagnostics=json` 把失败改写成标准错误上的单个 JSON 信封，可以写在命令前后：

```zsh
# 失败时标准错误只有一个 JSON 文档，标准输出不混入诊断。
./bin/tynel --diagnostics=json check examples/add.ts
```

信封字段与错误码见[诊断与错误码](/reference/diagnostics)。

## 常用选项速查

| 选项 | 适用命令 | 说明 |
| --- | --- | --- |
| `--root <目录>` | check / build / run / dump | 项目根；省略时使用编译器检出根 |
| `-o <路径>` | build / build-ir | 输出路径，必填 |
| `--target <平台>` | build / run / build-ir | 只接受 `darwin/arm64` |
| `--json` | check | 完整检查结果写标准输出 |
| `--diagnostics <模式>` | 全局 | `human`（默认）或 `json` |

未知选项不会被忽略，按用法错误退出 2。