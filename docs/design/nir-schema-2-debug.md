# 设计记录：NIR schema 2 的作用域 ID 与调试段

> 状态：已实现（NIR schema 2）  
> 目标协议版本：NIR schema 2  
> 已确认决定：调试函数放在模块级 `debug.functions`；作用域需要人类可读的 `path`  
> 关联：[长期路线图](../../ROADMAP.md) Stage 1、[NIR 格式](../reference/nir.md)、[Go 桥接与运行时](../reference/bridge.md)

## 1. 要解决的问题

当前 NIR 已经保存函数、参数、语句和表达式的 `pos`，降级器写出的位置也能通过 `source.Snapshot.Offset` 回到源码字节。但调试器还需要回答三个问题：

1. 某个源码位置处在哪个词法作用域；
2. 这个作用域里有哪些可见变量；
3. 每个变量的源码名、NIR 名、类型和可变性是什么。

当前 NIR 只有 `let` 的名字和语句树，作用域是验证器内部临时重建的，不进入文件；编译器临时值（`_tmp_N`）也没有标记。调试器无法区分用户变量和临时值，也无法在嵌套分支里确定变量属于哪一层。

本设计把作用域和局部变量显式写进 NIR 的可选 `debug` 段，并定义 schema 1 到 2 的迁移规则。它只解决「保留稳定映射」，不实现调试器、DWARF 或 LSP。

## 2. 目标与非目标

### 2.1 目标

- 每个函数有一棵稳定的作用域树，作用域有正整数 ID、父 ID、种类和源码范围。
- 参数、局部值、模块常量和编译器临时值都有调试条目，记录 NIR 名、源码名、类型、作用域和声明位置。
- `debug` 段可选：手写 NIR 可以省略，后端执行时完全忽略。
- schema 1 文件能迁移到 schema 2；迁移结果可以回放和构建。
- 调试段不改变求值顺序、类型规则或原生产物行为。

### 2.2 非目标

- 不实现 DWARF、断点、单步或变量求值器。
- 不把 Go 内部 `src.XPos` 或 `ir.Node` 写进 NIR。
- 不把结构化 TypeScript 类型搬进 NIR；调试类型暂时只使用 NIR 的 `number` / `bool`。
- 不承诺作用域 ID 跨编译器版本或跨构建稳定。ID 只在一个函数的同一次编译结果内有意义。
- 不引入 HIR 或新的优化层。

## 3. 作用域模型

### 3.1 作用域 ID

| 字段 | 类型 | 规则 |
| --- | --- | --- |
| `id` | 正整数 | 在同一个函数内唯一，从 1 开始；0 表示无效 |
| `parent` | 非负整数 | 根作用域为 0；其余必须指向同函数内已存在的、ID 更小的作用域 |
| `path` | 字符串 | 人类可读且同函数内唯一；根为 `function`，子作用域为 `父path/kind#序号` |
| `kind` | 字符串 | `function`、`branch`、`loop`、`prelude` |
| `start` | `Pos` | 作用域起点，闭区间 |
| `end` | `Pos` | 作用域终点，开区间；不得早于 `start` |

`path` 给人和工具一个比整数 ID 更易读的稳定引用：根作用域是 `function`，子作用域按父路径加 `kind#序号` 生成，例如 `function/loop#1/branch#1`。序号只在同一父作用域、同一 `kind` 内递增。增加一个同级作用域会改变后续序号，所以 `path` 也不是跨版本 ABI；它解决的是同一次编译结果里的可读性。

ID 在降低时按确定性顺序分配：先根作用域，再按源码遍历顺序创建子作用域。新增一个作用域会让它之后的 ID 变化，因此 ID 不是跨版本 ABI。需要跨版本定位时，应使用源码位置，而不是 ID 或 `path`。

### 3.2 作用域种类

| `kind` | 对应结构 | 说明 |
| --- | --- | --- |
| `function` | 函数体 | 根作用域；参数和函数体顶层局部值在这里 |
| `branch` | `if` 的 `then` / `else` | 每条存在的分支一个作用域；`else if` 会形成嵌套 |
| `loop` | `while` 的 `body` | 循环体作用域 |
| `prelude` | `while` 的 `condPre`、`short` 的 `right`、`select` 的 `then` / `else` | 表达式准备块；只在对应条件成立或分支被选中时执行 |

`break` / `continue` / `return` 不创建作用域；`set` 不创建新变量。

### 3.3 为什么不给每条语句加作用域 ID

可以在每个 `Stmt` 上加 `scope`，但那会让执行核心变大，而且和 `debug` 可选的目标冲突。作用域树带源码范围后，调试器可以用「位置落在哪个最内层范围」恢复活动作用域；变量归属已经由 `locals[].scope` 明确记录。

如果未来证明范围恢复不够精确，可以再加 `Stmt.scope`，但那是一次独立协议变更，不放进 schema 2 的第一版。

## 4. 局部变量模型

`debug.locals` 是一个扁平列表，每条记录一个变量或临时值：

| 字段 | 类型 | 规则 |
| --- | --- | --- |
| `name` | 字符串 | NIR 里的名字；同一作用域内唯一，不同作用域可以同名表示遮蔽 |
| `sourceName` | 字符串 | 用户源码名；编译器临时值为空；不同作用域可以重复 |
| `kind` | 字符串 | `parameter`、`local`、`module-const`、`temporary` |
| `type` | 字符串 | 当前只接受 `number` / `bool` |
| `scope` | 正整数 | 必须指向同函数内存在的 `scope.id` |
| `pos` | `Pos` | 声明位置；可选 |
| `mutable` | 布尔 | 只有可变 `let` 为 true；参数、`const`、`module-const` 和临时值都为 false |

`name` 是调试器读取运行时值的键；`sourceName` 是显示给用户的键。两者分开，才能同时处理：

- 合并模块后的前缀名，例如 `a__base`；
- 编译器临时值 `_tmp_3`；
- 用户源码名和 NIR 名不同的情况。

同一个作用域内不允许两个 local 使用同一个 `name`；不同作用域可以同名，表示遮蔽。

## 5. 调试段形状

`Module` 增加可选字段：

```json
{
  "schemaVersion": 2,
  "entry": "main",
  "functions": [],
  "debug": {
    "functions": [
      {
        "function": "count",
        "scopes": [
          {"id": 1, "parent": 0, "path": "function", "kind": "function",
           "start": {"file": "count.ts", "line": 1, "column": 1},
           "end": {"file": "count.ts", "line": 11, "column": 2}},
          {"id": 2, "parent": 1, "path": "function/loop#1", "kind": "loop",
           "start": {"file": "count.ts", "line": 3, "column": 16},
           "end": {"file": "count.ts", "line": 9, "column": 4}},
          {"id": 3, "parent": 2, "path": "function/loop#1/branch#1", "kind": "branch",
           "start": {"file": "count.ts", "line": 4, "column": 18},
           "end": {"file": "count.ts", "line": 7, "column": 6}}
        ],
        "locals": [
          {"name": "n", "sourceName": "n", "kind": "parameter",
           "type": "number", "scope": 1,
           "pos": {"file": "count.ts", "line": 1, "column": 16}},
          {"name": "total", "sourceName": "total", "kind": "local",
           "type": "number", "scope": 1, "mutable": true,
           "pos": {"file": "count.ts", "line": 2, "column": 7}},
          {"name": "x", "sourceName": "x", "kind": "local",
           "type": "number", "scope": 3,
           "pos": {"file": "count.ts", "line": 5, "column": 11}}
        ]
      }
    ]
  }
}
```

对应源码：

```typescript
function count(n: number): number {
  let total: number = 1;
  while (n > 0) {
    if (n === 1) {
      let x: number = 2;
      total = total + x;
    }
    n = n - 1;
  }
  return total;
}
```

`debug.functions` 按 `functions` 的顺序输出，便于稳定编码和比较。`scopes` 按 ID 升序；`locals` 按作用域和声明顺序输出。消费者应按 `function` 名字和 ID 查找，不依赖数组下标。

### 5.1 函数名的对应

`debug.functions[].function` 必须是 `Module.functions[].name` 之一。合并模块后的函数名带模块前缀，调试段使用同一个名字；`sourceName` 保留用户源码名。

### 5.2 模块常量和临时值

- 模块级 `const` 被降进入口或 `__init` 的 `let`，调试段记为 `kind: "module-const"`，`sourceName` 是源码名，`name` 是 NIR 名（合并时可能带前缀）。
- 短路、三元式、实参物化和条件准备块产生的 `let` 记为 `kind: "temporary"`，`sourceName` 为空。临时值默认包含在调试段里，调试器可以按 `kind` 隐藏；不包含会让「每个 `let` 都有调试条目」的生成规则复杂化。

### 5.3 位置与范围

`Pos` 继续使用文件、行、列，列按 Unicode 码点计数。作用域 `start` 闭、`end` 开；单行作用域允许 `start` 与 `end` 在同一行。多文件合并时，一个函数的所有作用域仍在同一源文件内；跨文件函数不会共享作用域。

不把字节偏移加进核心 `Pos`。需要字节偏移时，用 `source.Snapshot.Offset` 从行列换算；这是已经验证过的映射。这样执行核心保持轻量，调试段只承担显示和查询。

## 6. 校验规则

`debug` 可选。缺失时跳过所有调试校验；存在时按下面规则检查。调试段错误和现有 NIR 格式/语义错误一样属于输入内容错误，按 `源码错误` 报告（退出 1），不是内部故障。

| 检查 | 规则 | 错误码 |
| --- | --- | --- |
| 函数 | `function` 必须匹配一个核心函数名，且不能重复 | `nir.debug-shape` |
| 作用域 ID | 正整数、同函数内唯一 | `nir.debug-shape` |
| 作用域根 | 恰好一个 `parent == 0` 的作用域，且 ID 为 1 | `nir.debug-shape` |
| 作用域父链 | `parent` 必须存在且小于当前 ID，不能成环 | `nir.debug-shape` |
| 作用域 path | 非空、同函数内唯一；根为 `function`，子作用域以父 path 加 `/` 开头 | `nir.debug-shape` |
| 作用域种类 | 只能是 `function`、`branch`、`loop`、`prelude` | `nir.debug-shape` |
| 作用域范围 | `start` / `end` 字段合法，`end` 不早于 `start` | `nir.debug-span` |
| 局部变量名 | `name` 非空；同一作用域内不重复 | `nir.debug-local` |
| 局部变量作用域 | `scope` 必须存在 | `nir.debug-local` |
| 局部变量种类 | 只能是 `parameter`、`local`、`module-const`、`temporary` | `nir.debug-local` |
| 局部变量类型 | 当前只接受 `number` / `bool` | `nir.debug-local` |
| 源码名 | `kind != temporary` 时 `sourceName` 非空；`temporary` 必须为空 | `nir.debug-local` |
| 位置 | 出现时必须合法 | `nir.debug-span` |

完整生成由生产者保证，不由验证器强制。降低器应为每个参数和每个 `let` 写一条调试记录；手写 NIR 可以省略部分或全部 `debug`，但不能写自相矛盾的调试段。

## 7. 迁移与兼容

### 7.1 版本规则

| 输入版本 | 动作 | 输出 |
| --- | --- | --- |
| 1 | 设 `SchemaVersion = 2`，`Debug = nil` | 2 |
| 2 | 原样保留 | 2 |
| 其他 | 拒绝 | `nir.version` |

`CanRead` 接受 1 和 2。编码器写 2。`OriginalSchemaVersion` 继续记录迁移前版本；构建器只在发生迁移时把结果重新编码后落盘，保证后端读到当前版本。

### 7.2 旧文件与旧二进制

- 新编译器能读 v1，迁移后没有调试段。
- 旧编译器只认 v1，会拒绝 v2。这是协议升级的预期行为，不承诺旧二进制读新文件。
- 需要交换 NIR 的场景应使用当前编译器重新编码。

### 7.3 后端

- Go 桥接包接受 v1 和 v2，执行时完全忽略 `debug`；调试段不参与函数签名、类型布局或代码生成。
- 构建器把 v1 迁移成 v2 后写快照；桥接包读到的是当前版本。
- 同一份程序用 v1（无 debug）和 v2（带 debug）构建，运行行为应一致。产物哈希可能因构建身份包含 NIR 输入而不同，不能把逐字节相同当作验收条件。

## 8. 降低器怎样生成

降低器需要把当前隐式的作用域变成显式记录：

1. 函数根作用域 ID 1，范围是函数声明。
2. `if` 的每个实际存在的分支创建 `branch` 子作用域，范围是分支节点。
3. `while` 的循环体创建 `loop` 子作用域；`condPre` 创建 `prelude` 子作用域。
4. `short` 的右侧准备块创建 `prelude` 子作用域；左侧仍在当前作用域。
5. `select` 的 `then` / `else` 准备块各创建一个 `prelude` 子作用域；条件仍在当前作用域。
6. 参数记录在根作用域，`kind: parameter`。
7. 局部 `let` / `const` 记录在当前作用域，`kind: local`。
8. 模块级 `const` 记录在入口或 `__init` 的根作用域，`kind: module-const`。
9. 所有临时 `let` 记录在它们实际所在的作用域，`kind: temporary`，`sourceName` 为空。
10. 用一个统一的 `emitLet` 帮助函数同时写 NIR `let` 和调试局部值，避免两条路径漂移。

作用域创建必须和验证器的词法规则一致：验证器把 `if` 分支、`while` 体和准备块当作子作用域；降低器不能只按源码花括号创建，否则调试器看到的可见变量会与执行语义不一致。

## 9. 备选方案

| 方案 | 为什么没有选 |
| --- | --- |
| 每条语句带 `scope` | 核心变大，且与可选调试段目标冲突；作用域范围足以恢复 |
| 独立的 `.debug.json` 边车文件 | 构建身份、发行清单和回放都要多管一份文件；NIR 自身更适合自包含 |
| 用字符串路径做 ID | 人类可读，但更长、需要转义，数组比较也更复杂；ID 加树足够 |
| 直接把 DWARF 写进 NIR | 绑定具体后端和目标平台，违背 NIR 与后端解耦 |
| 把完整结构化 TypeScript 类型放进调试段 | 当前 NIR 没有类型系统，先只记录 `number` / `bool`，等类型层成熟再扩展 |

## 10. 实施清单

| 步骤 | 文件 | 内容 |
| --- | --- | --- |
| 1 | `compiler/nir/nir.go` | 增加 `DebugInfo`、`DebugFunction`、`DebugScope`、`DebugLocal` 和 `Module.Debug` |
| 2 | `compiler/nir/version.go` | `SchemaVersion = 2`，`CanRead` 接受 1/2，迁移 v1 时清空调试段 |
| 3 | `compiler/nir/verify.go` | 增加第 6 节的调试段校验和错误码 |
| 4 | `compiler/lower/lower.go` | 作用域 ID、path、调试局部值、统一 `emitLet` |
| 5 | `bridge/go/gc/tynel_bridge.go` | 接受 v2，忽略 `debug` |
| 6 | `compiler/nir/testdata/` | 增加 v2 调试夹具；保留 v1 回放夹具 |
| 7 | `bridge/tsgo/consumer/lower_test.go` | 断言作用域树、局部变量和源码范围 |
| 8 | `docs/reference/nir.md` | 更新版本、字段和迁移说明 |

验收条件：

- v1 文件迁移到 v2 后能通过验证和构建；没有调试段。
- v2 调试段能编解码往返，非法调试段按第 6 节拒绝。
- 真实源码降低后，作用域树与 NIR 验证器的词法作用域一致。
- 每个参数和 `let` 都有调试记录；临时值标记为 `temporary`。
- v1 无调试段与 v2 带调试段构建出的程序运行行为一致；不要求产物逐字节相同。
- `go test ./compiler/... ./cmd/... ./internal/... ./tests/...` 与 `./bridge/tsgo/check` 全部通过。

## 11. 待定问题

1. 调试局部类型是否需要跟随结构化 Facts？等 NIR 有自己的类型表示后再扩展，不在这里提前绑定 TypeScript 类型。
2. 作用域范围是否需要字节偏移？当前用行列加 `source.Offset`；如果真实调试器反复换算成为瓶颈，再考虑给调试段加 `startOffset` / `endOffset`。
3. 是否要求手写 NIR 的调试段完整覆盖所有 `let`？当前只要求自洽，不要求完整；降低器产物必须完整。
