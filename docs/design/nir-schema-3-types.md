# 设计记录：NIR schema 3 类型表

> 状态：S2.0 已确认；schema 3 类型表、字符串最小闭环、对象第一个切片、数组/元组第一个切片和可空联合第一个切片已实现
> 关联：[Stage 2 计划](../plans/stage-2.md)、[NIR 格式](../reference/nir.md)、[字符串 RFC](../language/semantics/strings.md)

## 1. 为什么需要 schema 3

schema 2 的类型字段只有 `number`、`bool`、`void` 三个字符串。Stage 2 要加入字符串、对象、数组、元组、联合和函数类型，继续用字符串会带来三个问题：

1. 类型语法要在 NIR 里重新解析一遍，和 Facts 里的结构化类型重复；
2. 结构化类型会内联在参数、结果和调试段里，重复且难以比较；
3. 每加一种值类型都要修改一批字段的含义。

schema 3 把类型提升为模块级类型表，执行节点只保存类型 ID。这样类型只定义一次，参数、结果、`select` 和调试局部变量都引用同一份定义。

## 2. 类型 ID

内置类型使用保留 ID，不需要在 `types` 表里重复声明：

| ID | 类型 |
| --- | --- |
| 1 | `number` |
| 2 | `bool` |
| 3 | `void` |
| 4 | `string` |
| 5 | `null` |
| 6 | `undefined` |
| 7 | `never` |

复合类型从 8 开始，由 `types` 表定义。ID 在模块内唯一、为正整数。降低器按首次使用顺序分配，并对结构相同的类型去重；手写 NIR 可以使用任意合法 ID，但必须自洽。

## 3. 类型表

```json
{
  "schemaVersion": 3,
  "types": [
    {"id": 8, "kind": "object", "name": "Point",
     "properties": [
       {"name": "x", "type": 1},
       {"name": "label", "type": 9, "optional": true}
     ]},
    {"id": 9, "kind": "union", "members": [4, 6]},
    {"id": 10, "kind": "array", "element": 1},
    {"id": 11, "kind": "tuple", "elements": [1, 2]},
    {"id": 12, "kind": "function", "params": [1, 4], "result": 2}
  ],
  "entry": "main",
  "functions": [
    {
      "name": "main",
      "params": [],
      "result": 3,
      "body": [
        {"op": "print", "value": {"op": "string", "value": "hello"}}
      ]
    }
  ]
}
```

`TypeDef` 字段：

| 字段 | 适用 kind | 含义 |
| --- | --- | --- |
| `id` | 全部 | 复合类型 ID，>= 8 |
| `kind` | 全部 | `object`、`array`、`tuple`、`union`、`intersection`、`function` |
| `name` | `object` | 可选类型名，用于诊断和调试 |
| `properties` | `object` | 属性名、类型 ID、可选性 |
| `element` | `array` | 元素类型 ID |
| `elements` | `tuple` | 元素类型 ID 列表 |
| `members` | `union` / `intersection` | 成员类型 ID，至少两个 |
| `params` | `function` | 参数类型 ID 列表 |
| `result` | `function` | 返回类型 ID |

执行核心的类型字段从字符串改为类型 ID：

- `Function.result`；
- `Param.type`；
- `Stmt.type`（`select` 结果）；
- `DebugLocal.type`。

## 4. 校验规则

- 内置 ID 1–7 不需要 `types` 条目，也不能被覆盖；
- 复合类型 ID 必须 >= 8、唯一；
- 所有类型引用必须指向内置 ID 或已定义复合类型；
- `object` 属性名非空，属性类型存在；
- `array` 必须有 `element`；
- `tuple` 的 `elements` 非空；
- `union` / `intersection` 至少两个成员；
- `function` 必须有 `result`，参数可以为空；
- `void` 只能用于函数结果，不能作为参数、局部变量或数组元素；
- 类型图允许通过 ID 表达递归对象，但验证器和消费者必须有遍历预算；
- 类型表中的顺序不决定语义，但编码器按 ID 升序输出。

## 5. 迁移

| 输入 | 动作 | 输出 |
| --- | --- | --- |
| 1 | 先迁移到 2，再按 2→3 | 3 |
| 2 | 字符串类型映射为 ID：`number`→1、`bool`→2、`void`→3；补 `types: []`；调试局部类型同样改写 | 3 |
| 3 | 原样校验 | 3 |
| 其他 | 拒绝 | `nir.version` |

schema 2 没有复合类型，所以 2→3 不生成 `types` 条目；未来从 3 升级时再增加显式迁移。旧二进制只认 1/2，会拒绝 3，这是协议升级预期。

## 6. 第一批操作

字符串最小闭环只需要：

| 操作 | 字段 | 结果类型 |
| --- | --- | --- |
| `string` | `value`（UTF-8 JSON 字符串） | 4 |
| `concat` | `left`、`right` | 4 |
| `strlen` | `operand`（与 `not` 一致，表示一个子表达式） | 1 |
| `compare` | 扩展 `===` / `!==` 支持 4 | 2 |
| `print` | 扩展接受 4 | — |

对象第一个切片已经加入 `object` 语句和 `field` 读取；数组/元组第一个切片加入 `array`、`tuple`、`index` 语句和 `len` 表达式；可空联合第一个切片加入 `wrap`、`unwrap`、`is` 和 `null` / `undefined` 表达式。其余操作仍在后续工作包：

- 多成员判别联合、`??` / `?.`；
- 函数值构造与间接调用；
- 数组方法与切片、数组/元组身份比较。

## 7. 调试段

- `DebugLocal.type` 从字符串改为类型 ID；
- 作用域树和 `scope.path` 保持不变；
- 类型名不写进调试段；需要显示时通过类型表还原；
- 调试段仍然可选，后端忽略。

## 8. 后端

- Go bridge 接受 schema 1、2、3；
- schema 3 用类型 ID 选择布局和操作；
- `debug` 仍然不参与代码生成；
- 构建器把 v1/v2 迁移成 v3 后写快照，保证 bridge 只处理当前版本；
- 类型布局变化必须进入 Runtime ABI 版本，而不是悄悄改 NIR。

## 9. 确定性

- 内置 ID 固定；
- 复合类型按首次使用顺序分配；
- 结构相同的类型去重；
- 编码按 ID 升序；
- 同一份源码在相同编译器版本下应得到相同类型表和相同 NIR 字节。

## 10. 验收

- v2 NIR 迁移到 v3 后能通过验证和构建；
- v3 字符串程序能编码、解码、回放并原生输出；
- 类型表引用错误、重复 ID、缺失 `element`/`result`、`void` 参数等按 `nir.type-*` 拒绝；
- 调试段局部变量类型 ID 正确；
- 对象、数组、元组、联合和函数类型定义能往返，即使对应操作尚未实现；
- 现有 v1/v2 回放夹具继续通过。

## 11. 已确认决定

1. `never` 现在保留 ID 7，避免以后重排内置 ID。
2. 允许通过类型 ID 表达递归对象类型；验证器和消费者使用遍历预算，不要求类型图无环。
3. 类型去重忽略 `name`；名字只作为首次出现的诊断/调试标签。
4. 可选属性用独立存在性标记；显式 `undefined` 表示已随 S2.4 可空联合接入。
5. `intersection` 只保留 kind 名称，暂不实现操作。
6. schema 3 的第一批操作是字符串最小闭环；对象、数组/元组、可空联合与函数值按后续切片加入，闭包操作仍待实现。
7. 对象、数组/元组、可空联合和函数值第一个切片已完成；下一步是闭包捕获、块体箭头与函数值身份比较。
8. 泛型在调用点单态化，NIR 不保存类型参数；类型表只接收实例化后的实际类型。

## 12. 实施顺序

1. 类型表结构、内置 ID、校验和 v2→v3 迁移（已完成）；
2. 降低器输出类型 ID；当时子集只用到内置标量，复合类型表由手写 NIR 先行验证（已完成）；
3. 调试段类型字段改为 ID（已完成）；
4. 字符串字面量、拼接、长度、严格相等和 `print`（已完成）；
5. 原生回归、回放和文档更新（已完成，证据见 [S2.0 / S2.1 验收](../validation/s2-01-nir-schema-3-string.md)）；
6. 对象、数组/元组和可空联合第一个切片已完成；下一步是 `??` / `?.`、判别联合与函数值（可选属性等待后续）。
