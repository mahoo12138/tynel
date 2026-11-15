# Runtime ABI v1

`tynel dump abi` 输出的布局快照是当前后端的 ABI 检查点：版本、值布局、GC 可见指针、退出状态和跨模块规则都从这里读。快照由 `compiler/abi` 生成，测试把它与提交进仓库的 JSON 逐字节比较；布局变化必须先提升版本，不能悄悄改字段。

```zsh
# 输出 ABI v1 布局快照（JSON），不需要 TypeScript 源文件。
go run ./cmd/tynel dump abi
```

```json
{
  "schemaVersion": 1,
  "name": "tynel-runtime-abi",
  "version": 1,
  "target": "darwin/arm64",
  "valueLayouts": [
    {"kind": "string", "representation": "struct { data *uint16; length int32 }"},
    {"kind": "union", "representation": "*struct { tag int32; payload 字段 }"}
  ]
}
```

完整快照见 `compiler/abi/testdata/runtime-abi-v1.json`。构建记录里的 `runtimeABI` 写同一个版本标识 `tynel-runtime-abi/1`，因此产物可以追溯到生成它的 ABI。

## 值布局

| 值 | 当前表示 | GC 可见指针 |
| --- | --- | --- |
| `number` | IEEE 754 binary64 | 无 |
| `boolean` | 8 位布尔，不用数字冒充 | 无 |
| `string` | `struct { data *uint16; length int32 }` | `data` |
| 静态对象 | `*struct`，字段按属性名排序 | 指针类型字段 |
| 数组 | Go 切片 `[]T`（预览表示） | 底层数组 |
| 元组 | `*struct`，字段按位置排列 | 指针类型字段 |
| 可空联合 | `*struct { tag int32; payload }` | payload 中的指针字段 |
| 函数值 | Go func 值（code + environment） | 闭包 environment；当前无捕获时为 nil |
| `void` | 没有值 | 无 |
| `null` / `undefined` | 只作为联合标签 | 无 |

对象和元组的引用身份来自分配地址；字符串和数组不暴露底层指针给用户代码。数组的 Go 切片头只是当前 bridge 的表示，长期目标是 `TynelSlice`，不能把 `len`/`cap` 字段当成语言 ABI。

## GC 契约

- 对象、元组、联合 payload、数组元素和字符串 `data` 里的指针必须是 GC 可见指针；
- 不允许用 `uintptr` 或无类型整数保存需要保活的对象地址；
- 对象、元组、联合和字符串存储由 Go 堆分配，bridge 不手写分配器；
- GC 随产物分发，当前是 Go GC 基座，不承诺替换成独立 GC 或跨平台 ABI。

[GC 可见指针回归](../../tests/source/source_test.go)用 `GOGC=1` 反复分配对象、数组、元组、联合和字符串，再读取其中的字段；隐藏指针会在回收后表现为错误数值或崩溃。

## 错误边界

| 退出状态 | 含义 |
| --- | --- |
| 0 | 成功 |
| 1 | 源码错误 |
| 2 | 命令行用法错误 |
| 3 | 能力限制 |
| 4 | 环境缺失 |
| 5 | 内部故障 |
| 6 | 运行时数据错误：数组索引越界、联合标签不符 |

编译失败使用 1–5，运行时数据错误单独使用 6，不复用 Go panic。数据错误由 `tynelDataError` 写出原因后退出，不是 JavaScript 异常。

## 跨模块规则

所有模块合并成一份 NIR 和一张类型表；跨模块值使用同一套布局，不按模块切换 ABI。模块函数加模块前缀，模块体在 `__init$N` 里初始化；导出的静态对象由依赖模块分配，入口模块按同一类型 ID 读取字段。跨模块对象布局的回归见[诊断测试](../../cmd/tynel/diagnostics_test.go)。

## 版本与边界

- 当前只验证 `darwin/arm64`；其他平台是计划，不是 ABI v1 承诺。
- ABI v1 描述的是 Tynel Language Preview 的预览布局，不是生产级稳定 ABI。
- 闭包 environment、`TynelSlice`、独立 GC 和跨语言 FFI 仍是后续设计；它们落地时需要新版本或新快照字段。
