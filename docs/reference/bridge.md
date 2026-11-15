# bridge：从 NIR 到机器码

本页说明 Tynel 的 Go bridge 在整条链路里的位置：谁建立包入口，bridge 在什么时候接手，构造出的内部节点又怎么走到指令选择和链接。

## 一次构建由谁做

```text
bridge/go/run add.nir.json
   │
   ├─ 固定支持包 bridge/go/support（package main，提供打印与按位构造 number 的函数）
   ├─ 修改版 compile（gc 包 + 一个注入钩子）
   └─ Go 的 link
```

`run` 用隔离 SDK 的 `go build` 编译支持包，通过 `-toolexec` 把 `compile` 换成修改版，并通过环境变量 `TYNEL_NIR` 告诉它读哪份 NIR。链接仍然由 Go 自己的 `link` 完成，Tynel 不碰机器码生成。

## `-toolexec` 怎么把 compile 换掉

`go build` 默认直接执行 `$GOROOT/pkg/tool/<goos>_<goarch>/compile`。加 `-toolexec` 之后，它改为先执行给定的包装程序，再把真正的工具路径作为第一个参数传进去。`go help build` 的原话是：

> a program to use to invoke toolchain programs like vet and asm. For example, instead of running asm, the go command will run 'cmd args /path/to/asm <arguments for asm>'.

也就是说包装程序收到的参数形状固定为「工具路径 + 工具原本的参数」：

```text
./bridge/go/toolexec  ./.work/.../pkg/tool/darwin_arm64/compile  -o $WORK/b001/_pkg_.a  -p main  -lang=go1.27 …
     包装程序                    真正的工具                              工具原本的参数，原样不动
```

`go` 还会设置 `TOOLEXEC_IMPORTPATH`，值等于 `go list -f {{.ImportPath}}`，即当前正在构建的包。包装脚本只做一件事：认出 `compile` 就换成修改版，其余原样转交。

```sh
tool=$1
shift
case "$tool" in
*/compile) exec "${TYNEL_COMPILE:?}" "$@" ;;
esac
exec "$tool" "$@"
```

这个机制有三个后果：

- **每个工具都会被包装**。`compile` 和 `link` 都会经过这里（`go build -x` 能看到两行），有汇编时 `asm` 也会；所以默认分支必须原样转交，不能只处理 `compile` 就结束。
- **不需要复制工具链**。替换发生在构建期，`$GOROOT` 下的文件一个都不改；去掉 `-toolexec` 就回到标准编译器。这比复制 271 MB 的 SDK 再替换 `pkg/tool` 里的二进制更轻，也不会破坏 01.01 校验过的副本。
- **判定入口包不看 `TOOLEXEC_IMPORTPATH`**。注入要回答的是「这个包是不是程序入口」，而不是「它的导入路径是什么」，所以 bridge 看的是 `types.LocalPkg.Name == "main"`。用导入路径的话，每个调用方都要在 bridge 里维护一张表，而包名是编译器自己已经知道的事实。

代价是每次工具调用多一个进程和一层 shell 转发。对本项目几十个包的构建可以忽略；这也是 06.x 打包时仍要保留真实工具路径、而不是把整个工具链塞进一个文件的原因。

## NIR 版本

bridge 在 `TYNEL_NIR` 指向的文件里直接读 `schemaVersion`，当前接受 1、2 和 3，并忽略可选的 `debug` 段。CLI 的迁移发生在写快照之前：`build-ir` 和 `tynel build` 交给后端的输入已经是当前版本。schema 3 的类型字段是整数 ID；bridge 的类型解析器同时接受旧格式的 `number` / `bool` / `void` 字符串，这样直接回放版本 1/2 夹具时不需要先经过 CLI 迁移。升级 NIR 协议时，除了在 `compiler/nir` 增加迁移，还要同步 bridge 的本地 `module` 结构和版本判断；否则会出现 CLI 接受、后端拒绝的分叉。

## 固定支持文件建立了什么

`bridge/go/support/support.go` 是一个对所有输入都相同的 `package main`，提供当前值类型所需的最小宿主支持：

- `tynelPrintFloat64(float64)`：NIR 里 `print` 一个 `number` 时的目标。它用 `fmt.Fprintln` 输出，42 打印成 `42`。
- `tynelPrintBool(bool)`：`print` 一个 `boolean` 时的目标，固定输出 `true` 或 `false`。
- `tynelPrintString(tynelString)`：`print` 一个 `string` 时的目标，在 I/O 边界把 UTF-16 转成 UTF-8，每次写一行。
- `tynelStringFromUTF8(string)`、`tynelStringConcat`、`tynelStringLen`、`tynelStringEqual`：字符串字面量、拼接、长度和相等。
- `tynelNumberFromBits(uint64) float64`：按位构造 number，供无法用字面量表达的值使用（见下一节）。
- 包本身是 `main`：这样注入进来的用户函数与它在同一个包里，符号前缀是 `main.`。

### 字符串的 Runtime 表示

Tynel 字符串在 Runtime 里是 UTF-16 code unit 序列：

```text
tynelString {
    data   *uint16
    length int32
}
```

`data` 指向由 GC 管理的 `uint16` 数组，`length` 是 code unit 数；空字符串的 `data` 为 nil、`length` 为 0。`length`、索引、切片和相等都按 code unit 计数，因此 `"😀"` 的长度是 2，`"世界"` 的长度也是 2。源码、JSON 和打印边界使用 UTF-8，只有 Runtime 内部保存 UTF-16；Go 的 `len(string)` 或字节索引不会暴露成 Tynel 语义。

bridge 把类型 ID 4 映射到支持文件声明的 `tynelString` 结构体，把 `string` 字面量构造成一次 `tynelStringFromUTF8` 调用，把 `concat`、`strlen` 和字符串 `===` / `!==` 映射到对应支持函数。字符串比较逐 code unit 进行，不比较结构体里的指针。

### 对象的 Runtime 表示

schema 3 的 `object` 类型在 bridge 里变成 Go 的 `*struct{...}`：字段按类型表里的属性顺序排列，字段类型可以是 `float64`、`bool`、`tynelString`、另一个对象/元组指针或数组切片。对象值因此是一个 GC 可见的指针，`===` 直接比较指针，引用身份成立。

`object` 语句生成一个 `ir.BlockStmt`：先用 `ONEW` 分配结构体并赋给局部值，再按 `fields` 顺序把每个字段值赋给选择器。`field` 表达式生成 `ODOT` 选择器。嵌套对象在类型表里是另一个类型 ID，bridge 先递归构造它的结构体，再把字段类型设成它的指针。当前切片拒绝可选属性和递归对象类型；可选属性需要 `undefined` 表示，递归对象需要先建立命名占位类型。

### 数组与元组的 Runtime 表示

`array` 类型映射为 Go 切片 `[]T`：切片头对 GC 可见，元素中的指针保持可见。`array` 语句生成 `OCOMPLIT`/`OSLICELIT`，元素按 `items` 顺序求值；`len` 生成 `OLEN` 再转成 `float64`。NIR 不暴露 Go 切片头的字段，长期 ABI 仍按 Tynel 值模型收敛。

`tuple` 类型映射为 `*struct{f0 T0; f1 T1; ...}`，布局按位置固定。`tuple` 语句用 `ONEW` 分配结构体并按位置赋值；`index` 在元组上解析成 `ODOT` 选择器。元组索引在 NIR 里已经限定为编译期整数，bridge 不生成运行时边界检查。

数组索引先调用 `tynelIndexToInt` 把 `number` 转成整数，再用 `tynelCheckBounds` 检查边界；这两步都在生成的 `ir.BlockStmt` 里。非整数、NaN、无穷和越界都写到标准错误并以状态 6 退出，不是 Go panic。检查通过后才生成 Go 的 `OINDEX`。

数据错误的退出状态 6 与编译失败的 1–5 分开。`tynel run` 原样传出程序状态；没有编译诊断信封，只有程序自己的标准错误。

### 可空联合的 Runtime 表示

`union` 类型映射为 `*struct{tag int32; v0 ...; v1 ...}`：标签是成员在类型表里的位置，空值成员只写标签，非空成员各有一个 payload 字段。`wrap` 用 `ONEW` 分配结构体，写入标签和成员值；`unwrap` 先调用 `tynelCheckUnionTag` 检查标签，再读对应字段；`is` 直接比较标签字段。

标签不一致会按运行时数据错误退出 6。前端负责 TypeScript 的控制流缩窄，NIR 里只出现已经明确的 `wrap` / `unwrap`；bridge 不知道源码里的 `if` 条件。

它**不**提供 `main` 函数。NIR 的入口叫 `main` 时，入口函数本身就是 `main.main`；入口叫别的名字时，bridge 额外生成一个 `main` 包装函数——没有参数、没有返回，函数体只有对入口的一次调用——Go 可执行程序要求的 `main.main` 由此成立，`entry` 字段真正决定程序从哪里开始。包装函数和用户函数一样直接建成 `ir.Func`，不生成 Go 源码。如果包里已经有 `main` 声明（比如 NIR 里有个叫 `main` 的函数而入口另有其名），bridge 会因为无法生成包装而拒绝；支持文件自己写 `main` 也会因符号冲突被拒绝。

## 特殊数值怎么生成

`go/constant` 表示不了 NaN 和正负无穷，也会把负零归一成正零。如果所有 `number` 都走 `ir.NewBasicLit`，`print(-0)` 会悄悄变成 `print(0)`，`print(NaN)` 根本编不过。

所以 bridge 分两种情况：

- 有限且不是负零的值，仍然用 `ir.NewBasicLit` 生成字面量。`add(40, 2)` 的反汇编因此是两个字面量加一条 `FADDD`。
- 负零、NaN、正负无穷改为调用 `tynelNumberFromBits`，把 64 位整数传进去，由 `math.Float64frombits` 在运行时还原位模式。

判断条件是 `math.IsNaN`、`math.IsInf` 或「等于零且符号位为负」。这样 NIR 里保存的位模式与实际执行的数值一致，而不是「看起来是零」。

## bridge 何时接手

注入点在 `gc.Main` 里、`symABIs.GenABIWrappers()` 之后、`deadlocals` 之前：

```text
noder.LoadPackage       ← Go 前端读完支持包，得到类型信息
bloop / inlining        ← 编译器自己的优化
MakeWrappers            ← 编译器自己生成包装函数
GenABIWrappers
tynelInject(target)     ← bridge 在这里加入 NIR 函数
deadlocals / escape / walk / SSA / obj / link
```

位置是试出来的，有两个硬约束：

- **不能早于内联**。`inlining` 会为每个函数找 unified IR 的函数体序列化位置（`bodyReader`）。新建的函数没有这份数据，早注入会触发 `must have bodyReader` 的内部断言。
- **不能晚于 `deadlocals`**。之后的逃逸分析、walk、SSA 都按 `typecheck.Target.Funcs` 遍历，晚于它们就没人处理新函数了。

注入本身只做四件事：读 NIR、建签名、建函数体、交给 `typecheck.Stmts`。真正的类型检查、逃逸分析、指令选择、寄存器分配和链接全部沿用 Go 原有流程。

签名先于函数体建立：`inject` 第一遍为所有 NIR 函数调用 `ir.NewFunc` 并登记到 `funcs`，第二遍才构造函数体。自调用和相互递归因此能直接找到目标，不需要额外的占位声明。

## 内部节点怎么来的

| NIR | Go 内部节点 |
| --- | --- |
| 函数 | `ir.NewFunc` + `fn.DeclareParams(true)`，签名由 `types.NewSignature` 构造 |
| 参数 | `DeclareParams` 按签名生成的 `*ir.Name`，按顺序对应 NIR 的 `params` |
| `number` | `ir.NewBasicLit`，值是 `constant.MakeFloat64` |
| `bool` | `ir.NewBasicLit`，类型 `TBOOL`，值是 `constant.MakeBool`；`false` 也照常写出来 |
| `string` | `ir.NewBasicLit` 造 UTF-8 字符串常量，再调用 `tynelStringFromUTF8` 转成 UTF-16 结构 |
| `concat` / `strlen` | 调用支持函数 `tynelStringConcat` / `tynelStringLen` |
| `object` | `ir.BlockStmt`：`ONEW` 分配结构体，再按字段顺序生成 `ODOT` 赋值 |
| `array` | `ir.CompLitExpr`（`OCOMPLIT`/`OSLICELIT`），元素按 `items` 顺序求值 |
| `tuple` | `ir.BlockStmt`：`ONEW` 分配结构体，再按位置生成 `ODOT` 赋值 |
| `index` | 数组：边界检查后 `OINDEX`；元组：常量位置对应的 `ODOT` |
| `field` | `ir.NewSelectorExpr(pos, ir.ODOT, base, fieldSym)` |
| `len` | `ir.NewUnaryExpr(pos, ir.OLEN, array)` 再转成 `float64` |
| `wrap` | `ir.BlockStmt`：`ONEW` 分配联合结构，写标签和成员 payload |
| `unwrap` | 检查标签后读取成员字段 |
| `is` | 比较联合标签与目标成员位置 |
| `func` | 直接引用 `ir.Func` 的 `Nname`，作为 Go func 值 |
| `invoke` | `ir.NewCallExpr`，被调用者是函数值表达式 |
| `param` | 直接用参数的 `*ir.Name` |
| `binary` | `ir.NewBinaryExpr`，`+ - * /` 对应 `OADD OSUB OMUL ODIV` |
| `not` | `ir.NewUnaryExpr`，对应 `ONOT`，操作数与结果都是 `bool` |
| `compare`（string） | 调用 `tynelStringEqual` 逐 code unit 比较；`!==` 在外面套一层 `ONOT` |
| `call` | `ir.NewCallExpr(pos, OCALL, callee.Nname, args)` |
| `return` | `ir.NewReturnStmt` |
| `if` | `ir.NewIfStmt`，`then` 与 `else` 各自构造成 `[]ir.Node` |
| `short` | 一个 `ir.BlockStmt`：先把左侧赋给 bool 局部值，再用 `ir.NewIfStmt` 决定是否执行右侧准备块并把结果写回 |
| `select` | 一个 `ir.BlockStmt`，里面是带 `then` / `else` 的 `ir.NewIfStmt`；两个分支各自执行准备块，再把结果赋给同一个局部值 |
| `let` | `fn.NewLocal` 造一个自动变量，再生成一条赋值；类型跟着值走。`mutable` 只影响验证，生成方式与 `const` 相同 |
| `set` | 在作用域里找到已有的局部变量，生成一条 `ir.NewAssignStmt`；右值先构造成 Go 表达式 |
| `while` | 生成 `ir.NewForStmt` 的无限循环：循环体开头放条件准备，条件为假时 `break`，否则执行原循环体 |
| `break` / `continue` | 生成 `ir.NewBranchStmt`，分别用 `ir.OBREAK` 与 `ir.OCONTINUE`；Go 的最近循环与 NIR 的 target 一致 |
| `print` | 调用支持函数的 `OCALL`，作为语句直接放进函数体；`number`、`bool` 与 `string` 各有一个目标函数 |

节点用 `base.AutogeneratedPos` 定位，反汇编里因此显示 `<autogenerated>:1`。NIR 自带的 `pos` 还没有映射到 Go 的 `src.XPos`，这一步留给后续节点。

函数值在 bridge 里直接用 Go func 值表示：`func` 返回目标 `ir.Func` 的 `Nname`，`invoke` 以它为被调用者生成 `OCALL`。无捕获函数值相当于「代码入口加空环境」；闭包环境留到后续切片。Go 的 func 类型只是后端表示，不出现在 NIR 或公共 API 里。

## 空函数体是一个坑

`gc.compile` 里有 `if len(fn.Body) == 0`：编译器把没有函数体的函数当成外部声明，只生成符号、不生成代码。NIR 的空 `void` 入口如果原样传进去，链接会报 `function main is undeclared in the main package`。

所以 bridge 给空的 `void` 函数补一条空 `return`。这不是为了绕过检查，而是语义等价：函数正常结束就是返回。补上之后，空入口才是可执行函数而不是外部声明。

## 被丢弃的表达式怎么落地

`discard` 要求「求值，但不要结果」。Go 的 IR 对这件事的接受程度取决于表达式形状：

- **void 调用**可以直接作为语句。`f()` 没有结果，放进函数体就是一条调用语句，编译器和 SSA 都认。对函数值的 `invoke` 只要结果类型是 `void`，也走同一条路径。
- **number、boolean 或 string 表达式**不行。把 `answer` 这样一个 `*ir.Name` 直接当语句放进去，编译器内部报 `internal compiler error: unexpected stmt: answer`——Go 的语法层从不允许这种写法，IR 层也没有对应的处理分支。

所以 bridge 对这类 `discard` 生成一条赋值：`fn.NewLocal` 按表达式的实际类型造一个名字形如 `_tynel_discard_1` 的局部变量，把表达式赋给它，之后从不读取。求值照做，结果没人用，死存储由 SSA 清掉。

这个差别是实测出来的，不是设计时想到的：第一版两种都直接当语句，`answer;` 立刻让编译器内部报错。判断依据是 NIR 里被丢弃表达式是不是对 `void` 函数的直接调用——只有这一种能省掉临时变量。

## 分支与局部变量的作用域

NIR 的 `if` 直接对应 `ir.NewIfStmt`，`then` 与 `else` 各自构造成 `[]ir.Node`。条件只构造一次，Go 的 `if` 在运行时决定走哪条分支；bridge 不做常量折叠，`if (false)` 也不会在编译期消失。

局部变量按 NIR 的块作用域分配。bridge 维护一个名字表栈：`let` 写进当前块，读取沿父链查找。两条分支可以各有一个同名 `x`，它们指向不同的 `*ir.Name`；Go 的符号名不能重复，后出现的一个会改成 `x$1`，NIR 里的名字仍然按作用域解析。分支里的局部变量不会提升到 `if` 之前，未选中的分支也不会求值。

一个块在遇到结束语句后停止生成：`return` 之后的语句不可达，NIR 保留它们供验证器检查，但 bridge 不把它们交给 Go 编译器。

## 短路的结果绑定

NIR 的 `short` 展开成一个 `ir.BlockStmt`：先把左侧赋给一个 bool 局部值；`&&` 用这个值作条件，`||` 用它的 `ONOT`；条件成立时执行右侧准备块，再把右值赋回同一个局部值。这个局部值就是后续 `local` 引用读到的结果。

右侧准备块用子作用域构造，里面的局部变量只在条件成立时执行，也不会泄漏到外层。bridge 不把 `&&` / `||` 直接交给 Go 的二元表达式，而是显式生成条件赋值——这样右侧的准备工作、临时值和赋值顺序都留在 `if` 内部，和 NIR 的形状一一对应。

## 三元式的结果绑定

NIR 的 `select` 也展开成一个 `ir.BlockStmt`：里面是一个带 `then` / `else` 的 `ir.IfStmt`。两个分支各自先用子作用域构造准备块，再把 `thenValue` / `elseValue` 赋给同一个局部值。局部值的类型取自 NIR 的 `type`，不依赖表达式节点在 typecheck 之前的空类型。

Go 看到的是 `if cond { ... } else { ... }`，没有三元表达式；两条分支的赋值在 Go 的 typecheck 里各自成立，结果在后续语句里读取。

## 构造结果长什么样

`examples/ir/add.nir.json` 生成的 `main.add` 只有两条指令：

```text
TEXT main.add(SB) <autogenerated>
  <autogenerated>:1  FADDD F1, F0, F0
  <autogenerated>:1  RET
```

`main.main` 先把两个常量装进浮点寄存器，再 `CALL main.add`、`CALL main.tynelPrintFloat64`。这说明加法是真实的浮点加法，不是编译期折叠的常量。

## 为什么不是生成 Go 源码

生成 `add.go` 再编译也能得到同样的机器码，但那样用户算法会经过一遍文本再解析，错误位置、类型身份和求值顺序都要在文本里重新表达一遍。bridge 直接构造节点，NIR 的类型和顺序就是编译器的输入，中间没有第二套语法。

## 缓存身份

`-toolexec` 不参与 Go 的缓存键：换掉包装程序，或者换掉它转交的 `compile`，同一份输入仍可能命中旧对象。这一点是实测撞到的——给空函数体补上隐式 `return` 并重编 bridge 之后，空入口用例仍然报旧错误，直到 build tag 变化才重新编译。

`run` 早期把 NIR 内容和编译器二进制的 sha256 前缀放进 build tag（`tynelapp tynel_<nir8>_<compile8>`）。05.07 起改用完整的产物输入身份 `tynel_<identity.artifactSha256>`：NIR、配置、工具链和固定支持内容变化都会换缓存键，且同一份 NIR 的回放仍能保持逐字节相同。规则见[构建身份与依赖变化](build-identity.md)。
