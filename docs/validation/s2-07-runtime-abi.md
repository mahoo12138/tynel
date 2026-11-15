# S2.7 验收：Runtime ABI v1 布局快照

> 验证日期：2026-11-15（本地实际运行时间，不是 Git 提交时间）
> 目标：darwin/arm64
> 关联：[Runtime ABI v1](../reference/runtime-abi.md)、[bridge 说明](../reference/bridge.md)、[构建身份](../reference/build-identity.md)

## 1. 范围

本轮实现覆盖：

- ABI 版本 `tynel-runtime-abi/1`，写进构建记录；
- `tynel dump abi` 输出机器可读的布局快照；
- `number`、`boolean`、`string`、对象、数组、元组、可空联合、函数值的物理表示；
- GC 可见指针契约和禁止隐藏指针；
- 编译失败 1–5 与运行时数据错误 6 的退出边界；
- 跨模块使用同一张类型表和同一套布局；
- GC 压力回归与跨模块对象回归。

不在本轮：独立 GC、手写分配器、写屏障实现、闭包环境布局、`TynelSlice` 公开 ABI、跨平台 ABI、FFI。

## 2. 布局快照

```zsh
# ABI 快照与提交的 JSON 夹具逐字节一致。
go test ./compiler/abi -run Snapshot -v
```

结果：通过。快照包含 10 类值布局、GC 契约、7 个退出状态和跨模块规则。任何布局变化都会先让夹具测试失败，必须提升 ABI 版本并更新快照。

```zsh
# 开发接口：输出 ABI v1 快照。
go run ./cmd/tynel dump abi
```

实际输出开头：

```text
{
  "schemaVersion": 1,
  "name": "tynel-runtime-abi",
  "version": 1,
  "target": "darwin/arm64",
  "valueLayouts": [
```

## 3. 构建记录

```zsh
# 记录里的 runtimeABI 与 ABI 版本常量一致。
go test ./internal/build -run RunPublishesArtifactAndRecord -v
```

结果：通过。`.build.json` 新增 `runtimeABI: "tynel-runtime-abi/1"`；它不进入构建哈希，布局变化仍通过 bridge 源码哈希和 NIR 反映到身份。

## 4. GC 可见指针

```zsh
# 在 GOGC=1 下反复分配对象、数组、元组、联合和字符串。
go test ./tests/source -run TestGCVisiblePointers -v
```

`gc-visible.ts` 每轮分配：

- 一个带 `number` 和 `string` 字段的对象；
- 一个对象数组；
- 一个包含对象和 `number` 的元组；
- 一个 `Item | null` 联合；
- 每次调用 `make` 都新建 UTF-16 字符串存储。

程序在 `GOGC=1` 下运行 1000 轮，读取对象字段、数组元素、元组位置、联合 payload 和字符串 `length`，输出 `503500`。如果指针被藏进 `uintptr`，回收后会出现错误数值或崩溃。

## 5. 跨模块布局

```zsh
# 导出的静态对象跨模块读取，产物输出 42。
go test ./cmd/tynel -run TestCrossModuleObjectLayout -v
```

结果：通过。`lib.ts` 导出 `Point` 对象，`main.ts` 导入后读取 `x`/`y`；两个模块合并成一份 NIR、一张类型表，不复制对象也不切换布局。

## 6. 错误边界

运行时数据错误沿用已有回归：数组越界由 `tynelDataError` 退出 6，联合标签不符同样退出 6；编译失败使用 1–5。快照里的 `errorExits` 与 `compiler/diagnostic` 的 0–5 以及 `bridge/go/support` 的 6 对齐。

## 7. 检查命令

```zsh
./bridge/go/build
./bridge/go/check
./bridge/tsgo/check
go test ./compiler/... ./cmd/... ./internal/... ./tests/...
```

结果：bridge 构建成功；后端检查全部通过；前端检查全部通过；全量 Go 测试通过。

## 8. 实现边界

- ABI v1 是 `darwin/arm64` 的预览检查点，不是生产级稳定 ABI。
- 数组仍是 Go 切片表示；长期目标是 `TynelSlice`，公开字段和切片共享策略未定。
- 闭包环境尚未落地；当前函数值是 Go func 值，无捕获时 environment 为 nil。
- GC 压力回归证明当前布局的指针可见性，不证明独立 GC 或写屏障实现。
- 跨平台、FFI 和 Native ABI 仍属后续阶段。
