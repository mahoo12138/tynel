# S2.2 验收：静态对象第一个切片

> 验证日期：2026-11-15（本地实际运行时间，不是 Git 提交时间）
> 目标：darwin/arm64
> 关联：[对象与数组 RFC](../language/semantics/objects-arrays.md)、[NIR 格式](../reference/nir.md)、[bridge 说明](../reference/bridge.md)

## 1. 范围

本轮实现覆盖：

- 静态形状对象类型：接口、对象类型别名、内联对象类型；
- 必填属性、对象字面量、属性读取、嵌套对象；
- 对象参数、返回值、`const`、`let` 和三元式结果；
- 对象引用身份：`===` / `!==` 比较指针，不比较字段内容；
- 类型表按结构去重，忽略类型名；对象布局按属性名排序；
- 负向能力限制：可选属性、字段写入、对象输出、结构性放宽。

不在本轮：可选属性、字段写入、对象展开、动态键、数组/元组、对象打印、递归对象类型。

## 2. NIR 与验证器

```zsh
# 类型表、对象语句、字段读取、对象身份和错误码。
go test ./compiler/nir
```

结果：通过。覆盖：

- `object` 语句的字段覆盖、未知字段、重复字段、字段类型不符；
- `field` 读取不存在的属性；
- 可选属性按 `nir.object-field` 拒绝；
- 对象不能作为 `print` 参数；
- 对象引用 `===` 通过验证；
- 对象语句和字段读取的编解码往返。

## 3. 原生输出

```zsh
# 手写 NIR：Point 对象、sum(p)、p === p、p === q。
go test ./tests/native -run 'TestFixtureOutputs/对象字段与身份' -v
```

实际输出：

```text
42
true
false
```

`p` 和 `q` 字段内容相同但分别构造，引用比较为 false。

```zsh
# 全部原生夹具，包括 v1/v2 回放、字符串和对象。
go test ./tests/native
```

结果：通过。

## 4. 源码到原生

```zsh
# 源码回归；object 用例同时与 Node 22 的 shared 输出逐字节对照。
go test ./tests/source
```

`object.ts` 实际输出：

```text
1
2
3
true
false
50
true
true
true
```

覆盖：

- `mark(1)`、`mark(2)` 按字段源码顺序执行；
- `sum(p)` 读取两个字段；
- `p === p` 为 true，`p === q` 为 false；
- 嵌套对象 `s.start.x + s.end.y` 为 50；
- 空对象 `{}` 的身份比较；
- `type Pair = { x: number; y: number }` 与 `interface Point` 结构相同，复用同一个类型 ID，`r === p` 为 true；
- 三元式 `p.x > 0 ? p : q` 选择对象引用，`chosen === p` 为 true。

负向用例：

| 用例 | 结果 |
| --- | --- |
| `errors/object-optional.ts` | `cap.bad-type`，可选属性等待 `undefined` |
| `errors/object-print.ts` | `cap.bad-console-call`，对象没有输出格式 |
| `errors/object-field-write.ts` | `cap.assign-target`，字段写入属于第二个对象切片 |
| `errors/object-shape.ts` | `cap.object-shape`，结构性放宽需要对象转换 |
| `errors/object-computed.ts` | `cap.unsupported-syntax`，计算属性名不是静态字段 |

## 5. 后端与前端检查

```zsh
./bridge/go/build
./bridge/go/check
```

结果：bridge 构建成功；16 项检查全部通过，旧 schema 回放不受对象支持影响。

```zsh
./bridge/tsgo/check
```

结果：全部通过，包括外部消费者测试、适配包测试、源码到原生和命令行走查。

## 6. 实现边界

- 对象值在 bridge 里是 `*struct{...}`，字段按类型表属性顺序排列，指针身份就是对象身份；
- 当前只支持必填属性；可选属性需要先有 `undefined` 的运行时表示（S2.4）；
- 字段写入不在本切片；`p.x = 3` 明确按能力限制拒绝；
- 计算属性名 `{ ["x"]: 1 }`、方法、访问器与展开不是静态字段，按能力限制拒绝，前端事实保留空名字让检查层给出诊断；
- TypeScript 允许的结构性放宽（把更宽的对象变量传给更窄的参数）当前按 `cap.object-shape` 拒绝，因为需要字段复制或截断；
- 递归对象类型在 bridge 里会报错，当前切片没有命名占位类型；源码层由类型表深度预算拒绝。
