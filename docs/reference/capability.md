# 原生能力白名单

本页说明能力检查负责什么、当前支持哪些写法，以及每个错误码的含义。

## TypeScript 接受，Tynel 还不支持

```typescript
function identity(value: any): any {
  return value;
}
```

这段代码没有任何类型错误，`tsc` 会痛快地接受它。Tynel 不行：`any` 在机器码里没有对应表示——它能装数字、字符串、对象，而原生后端只有 `float64` 的槽位。要支持它就得先有一套装箱表示，那是另一个阶段的事。

所以编译器要回答两个不同的问题：

| 问题 | 谁回答 | 失败时退出 |
| --- | --- | --- |
| 这段代码本身有没有错？ | 类型检查（前端） | 1，源码错误 |
| 这段代码能不能变成机器码？ | 能力检查（本包） | 3，能力限制 |

把两者混成一个「编译失败」会让使用者分不清该改代码还是该等实现。上面那段 `identity` 的诊断长这样：

```json
{
  "line": 1,
  "column": 1,
  "category": "能力限制",
  "code": "cap.any",
  "message": "函数 \"identity\" 的返回类型是 any 或 unknown；本阶段只支持标量、对象、数组、元组和 void"
}
```

## 检查什么时候跑，跑哪些地方

顺序是固定的：先做源码检查，源码有错就直接报错退出 1——类型都没定下来，「这个类型有没有原生表示」无从谈起。源码干净之后才进入能力检查。

能力检查遍历事实里的**每一个**函数体和常量初始化表达式，包括没有被调用的函数。它不做可达性分析：

```typescript
function neverCalled(n: number): number {
  while (n < 3) {
    console.log(n);
  }
  return n;
}
```

这个函数没人调用，`while` 照样被拒绝。`return` 之后的不可达语句也一样：它们不会执行，但能力检查仍然遍历。理由是「今天不可达」和「以后也不会被调用」是两件事，而按可达性跳过检查，等到有人接上调用时才会在更靠后的阶段炸掉——那时离原因已经很远了。

## 当前支持什么

| 类别 | 支持 | 说明 |
| --- | --- | --- |
| 声明 | 顶层具名 `function`、必填参数、顶层 `const`、函数体内 `const` 与已初始化的 `let` | 参数、常量与局部值限 `number`、`boolean`、`string`、静态对象、同质数组或定长元组，返回值再加 `void`；默认参数、可选参数、模块级 `let` 与 `var` 拒绝；匿名、默认导出与嵌套函数声明按 `cap.unsupported-syntax` 拒绝；固定入口名 `main` 不能由用户占用 |
| 运算 | `+` `-` `*` `/`，数字字面量上的一元 `-` `+`、`!`，比较 `<` `<=` `>` `>=` `===` `!==`，短路 `&&` `\|\|`，三元式 `? :` | 有序比较只接受 `number`；严格相等要求两侧同为 `number`、`boolean`、`string` 或同一个对象形状；`+` 只接受两个 `number` 或两个 `string`；`&&` / `\|\|` 两侧都必须是 `boolean`；三元式的条件必须是 `boolean`，两臂同类型；一元 `-` / `+` 不接受变量或一般表达式 |
| 调用 | 本文件内声明的函数，或经值导入绑定到项目模块导出的函数；实参个数与形参一致 | 不支持函数值、方法调用，也不支持标准库全局函数 |
| 语句 | `return`、表达式语句、语句块、`if` / `else` / `else if`、`while`、`break` / `continue`、分支内 `const` 与已初始化的 `let`、独立的简单赋值 | 表达式语句的结果被丢弃；`if` / `while` 的条件必须是 `boolean`；`break` / `continue` 只能作用于最近循环 |
| 输出 | `console.log(<number 或 boolean 或 string>)` | 必须是内置 `console`，见下 |
| 字符串 | 字面量、无插值模板、`+` 拼接、`===` / `!==`、`s.length` | 索引、`slice`、模板插值和字符串方法仍拒绝；`s.length` 的结果是 `number`，按 UTF-16 code unit 计数 |
| 对象 | 静态形状的对象字面量、字段读取、对象引用相等、对象参数/返回值/常量/局部值 | 只支持必填属性；可选属性、字段写入、对象输出和结构性放宽仍拒绝；对象 `===` 比较引用身份 |
| 数组与元组 | 同质数组字面量、索引读取、`length`、数组参数/返回值/局部值；元组字面量与常量索引 | 数组越界是运行时数据错误；数组方法、展开、赋值、扩容和身份比较仍拒绝；元组索引必须是编译期整数，空元组拒绝 |

`boolean` 现在是完整的标量：可以作为常量、参数、返回值和输出。`!` 只对 `boolean` 取反；TypeScript 允许 `!1`，但本阶段不把数字的真假转换带进原生后端，`!1` 按 `cap.bad-type` 拒绝。

比较分两类：`<` `<=` `>` `>=` 只接受两个 `number`；`===` `!==` 要求两侧同为 `number`、同为 `boolean`、同为 `string` 或同一个对象形状。字符串相等比较内容，对象相等比较引用身份；数组和元组暂不支持身份比较。`==` 与 `!=` 是合法 TypeScript，但会做隐式类型转换，本阶段按 `cap.bad-operator` 拒绝。`true < false` 在 TypeScript 里能通过，能力检查按 `cap.bad-type` 拒绝——布尔值没有原生顺序。混合类型比较（`1 === true`）由 TypeScript 直接拒绝。

`+` 在两个操作数都是 `string` 时降低为 `concat`；两个都是 `number` 时是浮点加法。`"a" + 1` 在 TypeScript 里合法（结果是 string，会隐式转换），本阶段按 `cap.bad-type` 拒绝，不生成 `toString` 调用。字符串的 `===` / `!==` 按 UTF-16 code unit 内容比较，不做隐式转换。

对象第一个切片只支持静态形状和必填属性。`{ x: 1 }` 和 `{ x: 1, y: 2 }` 是不同形状；把一个更宽的对象变量传给只要求窄形状的参数时，TypeScript 允许，但本阶段按 `cap.object-shape` 拒绝，因为需要字段复制或截断。两个分别构造但内容相同的对象 `===` 是 false，`p === p` 是 true。`p.x = 3` 属于字段写入，按 `cap.assign-target` 拒绝；`console.log(p)` 按 `cap.bad-console-call` 拒绝。

数组只支持同质元素和静态形状。数组字面量按源码顺序求值，`length` 返回当前元素个数；索引先求值数组再求值下标，运行时检查整数性和边界，越界按数据错误退出 6，而不是返回 `undefined`。`console.log(xs)`、`xs === xs`、`xs.push(3)`、`[...xs]` 和把数组当可变存储写回都不在子集内。元组索引必须是编译期非负整数；变量下标、越界和空元组都在能力检查阶段拒绝。

`&&` 与 `||` 的两个操作数都必须是 `boolean`。TypeScript 允许 `true && 1`，结果类型是 `1`；本阶段按 `cap.bad-type` 拒绝，不做数字真假转换。右侧即使被短路跳过也要检查：`false && (1 % 2 === 0)` 仍报 `cap.bad-operator`。

三元式的条件必须是 `boolean`，两条分支必须同为 `number`、同为 `boolean`、同为 `string`、同一个对象形状、同一个数组类型或同一个元组类型。TypeScript 允许 `true ? 1 : false` 得到 `number | false`；本阶段按 `cap.bad-type` 拒绝，不把联合类型带进原生后端。未选中的分支同样遍历：`false ? (1 % 2) : 3` 仍报 `cap.bad-operator`。

分支支持 `if`、`else`、`else if` 和嵌套分支。条件必须是 `boolean`；`if (1)` 在 TypeScript 里合法，能力检查按 `cap.bad-type` 拒绝。函数体内可以声明 `const` 和带初始值的 `let`，作用域是所在的块；未初始化的 `let` 按 `cap.unsupported-syntax` 拒绝，模块级 `let` 按 `cap.module-let` 拒绝，`var` 按 `cap.unsupported-syntax` 拒绝。能力检查会遍历未选中的分支，`if (false) { ... }` 里的未实现写法同样拒绝。

简单赋值只支持 `x = value;` 这种独立语句，目标必须是可变的 `let`，右值与目标同类型。给 `const`、参数或模块常量赋值报 `cap.assign-immutable`；赋值出现在表达式里报 `cap.assignment-value`；复合赋值报 `cap.compound-assignment`；`++` / `--` 报 `cap.inc-dec`。

`while` 的条件必须是 `boolean`，循环体在函数体内，可以包含 `if`、赋值和嵌套 `while`。条件每轮重新求值，循环可能零次执行；模块级 `while` 报 `cap.unsupported-syntax`。`break` / `continue` 只作用于最近循环，带标签的写法报 `cap.labeled-jump`，循环外的跳转由 TypeScript 先报 `TS1107`。`for` 与 `do` 仍不在子集内。

## 参数与实参数量

参数语义记在事实里：`optional` 是 `a?: number` 的 `?`，`hasDefault` 是 `b: number = 10` 的初始化表达式，见[声明身份与事实](facts.md)。两者都让参数在调用时可以省略，但本阶段后端还不会合成缺省值，所以只要声明里出现其中任一种，就按 `cap.unsupported-parameter` 拒绝，诊断落在参数自己的位置上。策略落在声明而不是调用上：即使 `add(5, 2)` 把两个实参都给齐，带默认值的 `b` 仍然不支持。

实参数量规则只有一套：`frontend.Arity` 给出 `[必填参数个数, 形参总数]`，`Facts.Validate()` 与能力检查都用它。少一个实参的 `add(5)` 因此先通过事实自洽校验，再由能力检查以 `cap.unsupported-parameter` 退出 3；不会再被 `facts.arg-count` 当成内部故障。参数全部必填时，区间退化成一个数，与原来的固定个数检查一致。

跨模块调用在入口事实里没有目标函数的声明，个数由合并降低阶段的调用节点核对；依赖模块里的默认参数由命令行在合并前对每个模块各跑一次能力检查，同样按 `cap.unsupported-parameter` 拒绝，不会落到 `lower.bad-arity`。

## void 与丢弃结果

`void` 只描述「没有值」，所以它只能出现在两个位置：作为函数的返回类型，以及作为一条表达式语句的完整表达式。

```typescript
function announce(n: number): void {
  console.log(n);
}

announce(1);        // 可以：调用语句，结果丢弃
console.log(announce(1));  // 拒绝：void 没有值可以传
const x = announce(1);     // 拒绝：void 没有值可以绑
```

`announce(1)` 作为参数、操作数、返回值或常量初始值时，报 `cap.void-value`。TypeScript 的类型检查能拦住其中一部分，但 `console.log` 的形参是 `any[]`，把 void 传进去它不会吭声——这条必须由能力检查补上。

表达式语句的规则相反：**结果被丢弃，不打印**。`answer;` 与 `add(1, 2);` 都是合法写法，求值照做，值不进输出。只有 `console.log(x)` 会写一行。

## 错误码

| 代码 | 触发条件 |
| --- | --- |
| `cap.any` | 参数、返回值或常量声明为 `any` / `unknown` |
| `cap.type-escape` | `as`、`<T>x`、后缀 `!`（非空断言）、`satisfies` 这类绕过检查的写法 |
| `cap.unsupported-syntax` | 数组展开与空槽、字段形式不是 `name: value` 的对象字面量、变量元组索引、`for` / `do`、未初始化的 `let`、`var`、`new`、嵌套函数、匿名或默认导出的函数声明等本阶段不支持的语法 |
| `cap.unknown-callee` | 调用目标不是本文件声明的函数、不是 `console.log`，也没有对上一条值导入绑定的项目模块导出 |
| `cap.forbidden-global` | 调用 `eval` |
| `cap.shadowed-console` | `console` 被本地声明或导入绑定遮蔽，不是内置全局对象 |
| `cap.bad-console-call` | `console.log` 的参数不是恰好一个 `number`、`boolean` 或 `string`（对象、数组和元组没有输出格式） |
| `cap.bad-operator` | 运算符不在算术、一元 `- +`、`!` 与比较之内，包括宽松 `==` / `!=` |
| `cap.bad-unary` | 一元 `-` / `+` 作用于非数字字面量；变量取负需要额外的取负操作 |
| `cap.entry-name` | 用户函数占用了固定入口名 `main` |
| `cap.unsupported-declaration` | 只有声明没有运行时实现：ambient `declare function`、重载签名（无函数体）或 `declare const`（无初始化表达式） |
| `cap.module-let` | 模块级 `let`；模块体只接受 `const` |
| `cap.assign-immutable` | 给 `const`、参数或模块常量赋值 |
| `cap.assign-target` | 赋值目标不是直接名字 |
| `cap.assignment-value` | 赋值出现在表达式里，例如 `const y = (x = 1)` |
| `cap.compound-assignment` | 复合赋值 `+=`、`-=` 等 |
| `cap.inc-dec` | 自增自减 `++` / `--` |
| `cap.labeled-jump` | 带标签的 `break` / `continue` 或带标签语句 |
| `cap.loop-target` | `break` / `continue` 不在循环里 |
| `cap.unknown-name` | 引用了本文件之外的名称，且没有对上一条值导入绑定的项目模块导出 |
| `cap.unsupported-parameter` | 参数带默认值或 `?` 可选标记；本阶段只支持必填参数 |
| `cap.arg-count` | 实参个数不在 `Arity` 允许的范围内（必填参数个数到形参总数） |
| `cap.void-value` | 把 void 调用当作值使用：实参、操作数、返回值、常量初始值 |
| `cap.bad-type` | 声明的类型不在本阶段范围内，例如联合、函数类型、带可选属性的对象或元素不可执行的数组；或 `!` 的操作数不是 `boolean`、`if` 的条件不是 `boolean`、`&&` / `\|\|` 的操作数不是 `boolean`、`+` 的两侧不是同类标量 |
| `cap.object-shape` | 对象形状不同但 TypeScript 允许结构性赋值；当前切片要求形状完全相同 |
| `cap.unknown-field` | 属性访问引用了对象类型里不存在的字段 |
| `cap.unsupported-optional-property` | 读取可选属性；需要先有 `undefined` 表示 |
| `cap.duplicate-field` | 对象字面量里同一个字段出现两次 |
| `cap.index-bounds` | 元组索引超出元素个数 |

错误消息里带着具体的写法名（`对象字面量`、`控制流 IfStatement`），因为「不支持」这三个字本身不提供任何线索。

## 为什么 `console.log` 要核对来源

名字叫 `console.log` 不代表它就是内置的那个。如果文件里写了

```typescript
const console = { log: (x: number) => x };
```

那么 `console.log(42)` 调用的是用户的函数，不是输出。能力检查因此不看名字，而是看 `console` 这个标识符的符号身份：

- 解析到本文件里的声明（`Target` 非 0）就是本地遮蔽。
- 内置 `console` 的名称引用会解析到声明库（`TargetModule` 以 `bundled:` 开头）。命名导入、命名空间导入的 `console` 解析到项目模块，命名空间导入甚至没有目标模块——它们都不是内置对象。

以上任一情况都按 `cap.shadowed-console` 拒绝。仅凭 `Target == 0` 不够：导入绑定的 `console` 也没有本地 `Target`，必须再核对名称引用是否真的落在声明库上。降低阶段用同一判据，不会把导入的 `console` 降成 `print`。

此外，默认导入和命名空间导入本身就不在子集内：事实骨架的 `ImportDeclaration` 带 `importKind`（named / default / namespace / side-effect），能力检查对 default 与 namespace 按 `cap.unsupported-syntax` 拒绝，不让它们绕过绑定检查冒充内置对象。

这条检查与输出是否已实现无关：能力检查的职责是判断「这段程序能不能进后端」。输出本身在 02.06 已经可用，格式见[输出格式](output.md)。

## 跨模块引用怎么核对

前端事实里的 `CalleeModule` / `CalleeName` 和 `TargetModule` / `TargetName` 来自符号声明所在的文件。标准库也有声明文件，所以 `isFinite(1)` 的调用事实长这样：

```json
{"callee": 0, "calleeModule": "bundled:///libs/lib.es5.d.ts", "calleeName": "isFinite"}
```

只看「模块名非空」就把它当成跨模块调用放行，降低阶段会拿到一个 `Target` 为 0 的调用，最后报 `lower.unknown-callee` 内部故障——这是能力检查漏判，不该让后端背锅。反过来，真正能落到 NIR 的跨模块引用是 `import { helper as h } from "./lib.ts"; h(1)`，事实里的 `calleeName` 是导出名 `helper`，源码里写的是本地名 `h`。

能力检查因此只认一条规则：把前端解析出的 `(模块, 导出名)` 连同源码里的本地名，一起对本文件的**值导入**表核对，三者完全对上才放行。类型导入不算值绑定；模块名等于当前模块的，说明声明身份缺失，同样拒绝。`Identifier` 查 `References`、`CallExpression` 查 `Calls`，但两者最后都走这一套判定，不会出现「调用放行、名字引用拒绝」的分叉。导入指向的目标模块是否真的导出了这个名字，由[模块符号表](module-bindings.md)在合并之前用目标模块的事实再复核一次。

`isFinite`、`NaN`、`Infinity` 这类库符号因此按 `cap.unknown-callee` 或 `cap.unknown-name` 退出 3，和 `eval` 一样属于「写法合法但后端不支持」，而不是内部故障。

## 检查不做什么

- 不重新做类型检查。类型错误由前端报，能力检查信任已经通过检查的事实。
- 不做常量折叠、不做可达性分析、不判断某个分支会不会执行。
- 不生成任何代码。它只回答「能不能」，怎么生成是降低阶段的事。

能力检查之前会先校验事实本身是否自洽（见[声明身份与事实](facts.md)）：ID 重复或引用悬空时直接报内部故障，而不是基于坏数据给出能力结论。
