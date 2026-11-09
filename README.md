# Tynel

Tynel 是一个学习型 TypeScript-to-native 编译器项目。目标是复用 tsgo 前端和 Go 编译器后端，将受限 TypeScript 编译为使用 Go 运行时的原生程序。

MVP 的交付物是编译器本身：通过 `tynel build input.ts -o out/program` 将约定子集的 TypeScript 编译为 `darwin/arm64` 原生二进制，并提供类型与能力检查、错误诊断和可重复构建。应用服务不是验收条件。

节点 01 已完成（`mvp/01-native-ir`）：`tools/toolchain` 能准备隔离工具链，`compiler/nir` 定义并验证最小中间表示，`bridge/go` 把手写 NIR 经修改版 Go 编译器编成原生程序，`tynel build-ir` 负责输入快照、临时输出后发布和构建记录，`tests/native` 用原生回归核对数值与反汇编——`add(40, 2)` 输出 42，改成 41 得到 43。第一篇笔记见[一份加法描述怎样成为二进制](docs/notes/01-native-ir.md)。

02.01 已完成：`tools/toolchain` 现在也按锁文件解压 TypeScript 前端源码并校验身份，`bridge/tsgo/_adapter` 在上游模块树内提供稳定的检查接口，外部宿主 `bridge/tsgo/consumer` 能检查单个 `.ts` 文件并拿到带错误码和字节位置的诊断。

02.02 已完成：`compiler/source` 把源文件固定成不可变快照，负责字节偏移与行列的换算、输入预算和诊断映射。中文注释之后的错误仍然指向正确的字符，检查期间不会重新读磁盘。

02.03 已完成：`compiler/frontend` 定义声明、引用、调用和表达式类型的事实，并校验它们是否自洽。同名参数拿到不同 ID，前向调用解析到后声明的函数，事实在会话关闭后仍可读取。

02.04 已完成：`compiler/capability` 按白名单检查所有函数体，包括没有被调用的函数。`any`、类型断言、对象、`eval`、动态 `Function`、未知库调用都按能力限制退出 3；`console.log` 只有在内置 `console` 上才被接受。

02.05 已完成：`compiler/lower` 把事实降低成 NIR，`consumer -nir` 是它的开发接口。`add.ts` 生成的 NIR 与手写版本运行结果相同（42），实参从左到右求值，一元负号折成负零的位模式。

02.06 已完成：void 函数、无值 `return`、正常结束和丢弃结果的表达式语句都落到 NIR 的 `discard` 上，`answer;` 不打印任何东西。打印器有明确的格式：有限值最短可回读，负零、NaN、正负无穷各有固定拼写。

02.07 已完成：主命令行的 `check`、`build`、`run`、`dump` 走同一条流程。`tynel build foo.ts -o out/app` 直接得到原生程序，`tynel run foo.ts` 编译并运行本次产物，`check` 不生成机器码也不需要后端。前端以子进程方式接入，构建记录串起源码、NIR 与产物的哈希。

02.08 已完成：`tests/source` 把 14 个源码用例编成原生程序并逐字节比较输出，覆盖加法、求值顺序、void、丢弃结果、特殊值与五类失败。参考输出与 Node v22.22.2 对照，唯一差异是无穷的拼写，且写进了 `reference.json`。计划分为 6 个主节点、48 个开发子节点和 6 篇主笔记：原生后端、真实源码、条件选择、局部状态与循环、静态模块、编译器交付。首版以 `number`、`boolean` 和结构化控制流为语言边界；字符串、对象和数组作为后续语言扩展，不要求先完成某个应用。

02.09 已完成：节点 02 收尾。`examples/add.ts` 是一条命令走完的示例，`tynel build` 得到 42，改实参得到 43，存下 NIR 回放得到逐字节相同的产物；能力诊断的位置改为指向记号本身，不再指到上一行的空白。第二篇笔记见[从一份 TypeScript 源文件到二进制](docs/notes/02-source.md)，阶段验收见[节点 02 累计验证记录](docs/validation/02-source.md)。

03.01 已完成：`boolean` 成为完整的标量类型。常量、参数、返回值和 `console.log` 都接受 `boolean`，`!` 降成 NIR 的 `not`，`false` 用指针与「缺字段」区分；后端用 Go 的 `bool` 与 `ONOT`，不经过数字真假转换。`invert(false)` 输出 `true`，`!1` 按能力限制拒绝。验收记录见[03.01 验收记录](docs/validation/03-01-boolean.md)。

03.02 已完成：比较与严格相等。`<` `<=` `>` `>=` 只接受 `number`，`===` `!==` 要求两侧同为 `number` 或同为 `boolean`；结果都是 `boolean`，左右操作数按源码顺序求值。NaN、负零和无穷遵循 IEEE 754，`NaN < 1` 与 `NaN >= 1` 都是 `false`。宽松 `==` / `!=` 与 `true < false` 按能力限制拒绝。比较语义见[比较语义](docs/reference/comparison.md)，验收记录见[03.02 验收记录](docs/validation/03-02-compare.md)。

03.03 已完成：`if` / `else` / `else if` 与分支作用域。条件必须是 `boolean`，只求值一次；两条分支各自保留语句、各自开作用域，分支里的 `const` 降成 NIR 的 `let`。未选中的分支照样检查、照样保留，运行时只执行一条。`pick(true)` 与 `pick(false)` 分别输出 `1` 和 `2`，两边同名的 `x` 互不影响。双路径图见[分支的两条路径](docs/reference/branches.md)，验收记录见[03.03 验收记录](docs/validation/03-03-branch.md)。

03.04 已完成：提前返回与完整返回路径。有值函数不再要求最后一条语句是 `return`，而是检查每条路径是否结束：一条 `return` 直接结束，`if` 只有两臂都存在且都结束才算覆盖。`sign` 两臂都返回、`pick` 提前返回后补 `return`、嵌套分支和 void 正常结束都能构建；缺路径在 TypeScript、降低和 NIR 验证三处分别被拒绝。`return` 之后的不可达语句照样过能力检查，NIR 保留它们，bridge 生成时丢弃。规则见[返回路径](docs/reference/returns.md)，验收记录见[03.04 验收记录](docs/validation/03-04-returns.md)。

03.05 已完成：布尔短路。`&&` / `||` 降成 NIR 的 `short` 语句，左侧只求值一次，右侧的准备工作与右值留在条件内部，结果绑定到临时值后供 const、条件、实参和 return 使用。`false && mark(2)` 与 `true || mark(4)` 不打印 `2`、`4`；嵌套短路与实参顺序保持源码顺序。两侧都必须是 `boolean`，未选中的右侧照样过能力检查。规则见[短路求值](docs/reference/short-circuit.md)，验收记录见[03.05 验收记录](docs/validation/03-05-short.md)。

03.06 已完成：三元式。`cond ? a : b` 降成 NIR 的 `select` 语句，条件求值一次，只有选中的分支执行；两条分支各自带准备语句，结果绑定到临时值后可用于 const、条件、实参和 return。`true ? pick(1)` 与 `false ? pick(3) : pick(4)` 只调用被选中的分支；嵌套三元式与分支内部的短路保持各自的作用域。条件必须是 `boolean`，两臂必须同为 `number` 或同为 `boolean`。规则见[三元式](docs/reference/ternary.md)，验收记录见[03.06 验收记录](docs/validation/03-06-ternary.md)。

03.07 已完成：条件表达式的组合行为。优先级、括号、多层选择、实参顺序与输出顺序用源码差分和 NIR 回放核对；`f(7) + (条件 ? ...)` 暴露并修正了「右侧准备语句跑到左侧调用前面」的顺序问题。协议版本、深度预算、错误作用域和坏 NIR 直接交给 `build-ir` 的拒绝路径也纳入记录。完整组合表见[03.07 验收记录](docs/validation/03-07-combine.md)。

03.08 已完成：选择实验与第三篇。`examples/choices.ts` 从比较、分支、短路和三元式得到七行输出，`build` 与 `build-ir` 回放的产物逐字节相同；缺返回路径的 `TS2366` 在正文里可复现。[操作支持表](docs/reference/support.md)单独列出支持、拒绝与计划状态，第三篇笔记见[让程序自己选一条路](docs/notes/03-choices.md)，阶段验收见[03.08 验收记录](docs/validation/03-choices.md)。节点 03 建立完成标签 `mvp/03-choices`。

04.01 已完成：声明身份与可变存储。块内 `let` 带初始值声明，类型限 `number` / `boolean`，可读取、可遮蔽外层；事实和 NIR 的 `let` 都带 `mutable` 标记，为下一步赋值做准备。未初始化的 `let`、初始化读自身、越界作用域、模块级 `let` 与 `var` 都被拒绝。规则见[声明身份与可变存储](docs/reference/mutability.md)，验收记录见[04.01 验收记录](docs/validation/04-01-mutability.md)。

04.02 已完成：局部简单赋值。`x = value;` 降成 NIR 的 `set`，目标必须是可变的 `let`，右值先求值再写入；`x = x + 1` 读到写入前的值。数值累加与布尔改写都能构建，给 `const`、参数、未声明目标或不同类型赋值被拒绝，赋值表达式、复合赋值、自增自减也拒绝。规则见[从源码到 NIR](docs/reference/lowering.md)，验收记录见[04.02 验收记录](docs/validation/04-02-assignment.md)。

04.03 已完成：while 循环。`while (cond) { body }` 降成 NIR 的 `while`，条件的准备语句每轮重新执行，循环可能零次执行；`sumTo(0)`、`sumTo(1)`、`sumTo(10)` 分别得到 `0`、`1`、`55`，条件函数的打印次数与检查次数一致。循环体里的 `let` 不泄漏，只有循环体 `return` 的函数仍被判为返回路径不完整；原生测试用 5 秒超时防止错误实现挂住。验收记录见[04.03 验收记录](docs/validation/04-03-while.md)。

04.04 已完成：break、continue 与嵌套循环。NIR 的 `while` 带循环 id，`break` / `continue` 用 `target` 指向最近循环，验证器检查目标身份；内层 `break` 不结束外层，`continue` 回到条件检查，`return` 仍退出整个函数。带标签的跳转与循环外跳转被拒绝。跳转图见[循环跳转](docs/reference/jumps.md)，验收记录见[04.04 验收记录](docs/validation/04-04-jumps.md)。

04.05 已完成：直接递归与调用层。阶乘 `fact(7)` 得到 `5040`，相互递归的 `isEven` 可以调用后面声明的 `isOdd`，`sum(4)` 的每层局部值互不覆盖。降低器、NIR 验证器和 bridge 都先登记全部签名再生成函数体；编译器不执行递归，无终止程序只构建、不运行。规则见[直接递归与调用层](docs/reference/recursion.md)，验收记录见[04.05 验收记录](docs/validation/04-05-recursion.md)。

04.06 已完成：状态与控制组合。`sumTo(0/1/10)` 得到 `0/1/55`，迭代与递归阶乘在 `6`、`7` 上一致，分支赋值只走选中路径，`early(5)` 在循环内提前返回 `3`，短路与三元式组合的 `score` 分别得到 `15`、`6`。组合没有改变任何求值顺序或作用域规则；观察表见[状态与控制组合](docs/reference/combinations.md)，验收记录见[04.06 验收记录](docs/validation/04-06-combinations.md)。

04.07 已完成：状态实验与第四篇。设计记录只整理已验证的边界：可变 `let`、赋值先算右值、`while` 每轮检查、循环体作用域、无标签跳转、循环目标身份、递归签名预登记与调用层局部值，未实现的 `for` / `do` / 标签跳转和未知的栈深度、尾调用单独标出，见[状态、循环与调用](docs/design/state-and-loops.md)。真实源码实验 `examples/collatz.ts` 用循环和递归两种方式计算 Collatz 步数，`27`、`6`、`1` 分别得到 `111`、`8`、`0`，与 Node 逐行一致，NIR 可重放。失败记录保留在 `examples/collatz-mod.ts`：`%` 被 `cap.bad-operator` 拒绝；另一次用除法判断奇偶的写法虽然编译通过，却输出了错误的 `1075`，最后改用减法递归。叙述见[用一个小工具试状态与循环](docs/notes/04-loops.md)，验收记录见[04.07 验收记录](docs/validation/04-loops.md)。

05.01 已完成：项目根与路径解析。`--root` 固定项目根，入口按根解释而不是按工作目录解释；同一个根从仓库和 `/tmp` 构建出逐字节相同的产物。解析器只接受根内的显式 `.ts` 路径，大小写必须精确，符号链接目标不能越界；裸模块、目录遍历、绝对导入和错误大小写都拒绝，失败后不搜索别的文件。规则见[项目根与路径解析](docs/reference/project-paths.md)，验收记录见[05.01 验收记录](docs/validation/05-01-paths.md)。

05.02 已完成：依赖图与源码快照。`tynel dump graph` 从入口出发，用 tsgo 的语法树发现导入，把显式相对 `.ts` 依赖读成快照，记录类型边和值边，并输出稳定图哈希。线性依赖 3 个文件、2 条边，菱形依赖 5 个文件、5 条边，`shared.ts` 只保留一份；环报 `graph.cycle`，缺失目标报 `project.missing`。构建记录新增 `graphHash`。图与日志见[模块图与依赖快照](docs/reference/module-graph.md)，验收记录见[05.02 验收记录](docs/validation/05-02-graph.md)。

05.03 已完成：类型别名与纯类型导入。`type Amount = number` / `boolean` 只活在类型空间，不产生运行时值；`import type` 让两个文件共享同一别名，`main.ts` 输出 `42`，NIR 里没有导入动作。把别名改成 `string` 后使用方报 `TS2322`；联合、接口、`any`、类型名当值、纯类型导入被调用和循环别名都按预期拒绝。规则见[类型别名与纯类型导入](docs/reference/type-aliases.md)，验收记录见[05.03 验收记录](docs/validation/05-03-aliases.md)。

05.04 已完成：跨模块名称绑定。`tynel dump modules` 用模块绝对路径加模块内声明 ID 建立符号表；`a.ts` 和 `b.ts` 都导出 `helper`，`main.ts` 的 `h`、`h2` 分别绑定到两个模块，`base` 绑定到 `a.ts` 的顶层常量。缺导出报 `module.missing-export`，函数读取模块级 `const` 报 `module.captured-variable`。规则见[跨模块名称绑定](docs/reference/module-bindings.md)，验收记录见[05.04 验收记录](docs/validation/05-04-bindings.md)。

05.05 已完成：模块初始化顺序。`tynel dump init` 从入口按源码导入顺序深度优先，先值依赖、后当前模块，共享依赖只执行一次；`main.ts` 先导入 `b` 再导入 `a`，打印顺序是 `1 3 2 4`。纯类型依赖进入 `typeEdges`，不进入执行顺序，`types.ts` 里的 `console.log(9)` 不打印。规则见[模块初始化顺序](docs/reference/init-order.md)，验收记录见[05.05 验收记录](docs/validation/05-05-init.md)。

05.06 已完成：合并模块生成一个原生程序。拆分后的 `sumTo(10)` 输出 `55`；两个模块都导出 `helper` 时，`h` / `h2` 分别输出 `41`、`42`；初始化顺序 `1 3 2 4 23`，`shared.ts` 只执行一次。NIR 里的函数名带模块前缀，模块体变成 `__init$N`，入口 `main` 按顺序调度；`dump nir` 的结果可以脱离源码回放。规则见[合并模块与入口调度](docs/reference/merged-program.md)，验收记录见[05.06 验收记录](docs/validation/05-06-merge.md)。

05.07 已完成：依赖变化与构建身份。构建记录新增 `identity`：入口、全部类型和值依赖的路径与哈希、NIR、配置和工具链内容共同组成完整身份；产物输入身份单独记录并用作完整 build tag。改辅助函数后身份从 `b867…` 变为 `6c0a…`，输出从 `55` 变为 `56`；改类型或导出按 `TS2322`、`TS2724` 拒绝且保留旧产物；移动 `math.ts` 后身份记录新路径；从 `/tmp` 用同一项目根构建得到相同身份、标签和产物。构建前后还会复核依赖、NIR 快照、编译器、工具包装和支持目录。规则见[构建身份与依赖变化](docs/reference/build-identity.md)，验收记录见[05.07 验收记录](docs/validation/05-07-identity.md)。

05.08 已完成：多文件实验与第五篇。`examples/quote` 把报价程序拆成入口、折扣、运费和共享类型四个文件；`tynel build --root examples/quote main.ts -o out/quote` 得到一个 exe，输出 `90`、`10`、`100`。`dump init` 的执行顺序是 `discount.ts`、`shipping.ts`、`main.ts`，纯类型模块 `types.ts` 只出现在类型边里。把运费从 `5` 改成 `6` 后输出 `90`、`12`、`102`，完整身份从 `b0eb…` 变为 `605c…`；改回后身份恢复。合并 NIR 可以脱离源码回放，产物逐字节相同。缺导出和依赖环的诊断现在都给出完整文件路径。叙述见[把一个报价程序拆成四个文件](docs/notes/05-modules.md)，查询表见[多文件程序参考](docs/reference/modules.md)，阶段验收见[05.08 验收记录](docs/validation/05-modules.md)。节点 05 建立完成标签 `mvp/05-modules`。

06.01 已完成：固定用户命令与帮助契约。用户命令是 `version`、`check`、`build`、`run`；`dump`、`build-ir`、`toolchain` 是开发调试接口。`--root` 决定源文件根，`-o` 相对当前工作目录，`--target` 只接受 `darwin/arm64`，其他目标明确退出 3。`version` 现在同时显示 Go 和 typescript-go 身份；重复或未知选项按用法错误退出 2。`build` 从 `.work/validation/06-01/cwd` 用绝对项目根构建成功，产物和记录落在该工作目录。规则见[命令行参考](docs/reference/cli.md)，验收见[06.01 验收记录](docs/validation/06-01-cli.md)。

06.02 已完成：统一诊断与外部进程故障。源码、能力、环境、工具和运行失败现在共用一份诊断描述，带类别、稳定代码、阶段、文件、行列和原始原因；`--diagnostics=json` 把失败写成标准错误上的单个信封，并隐藏宿主重建进度。依赖文件里的类型错误按 `math.ts:1:14` 报告，人类输出与 JSON 条目的位置和错误码一致；修复前同一错误会落到 `lower.bad-type`。工具崩溃按内部故障、启动超时和找不到工具按环境缺失、输出不可写按环境缺失，原始 stderr 保留在原因里；缺导出和依赖环都给出完整路径。规则见[诊断协议](docs/reference/diagnostics.md)，验收见[06.02 验收记录](docs/validation/06-02-diagnostics.md)。

06.03 已完成：加固构建事务、并发与缓存策略。同一输出现在由 `flock` 锁保护，第二个构建按 `build.output-busy` 退出 4；产物和记录先写临时文件，再经 `.pending` 标记、旧文件备份和最终哈希复核发布，记录失败会恢复旧的一对而不是删掉旧产物。快照按完整内容哈希原子写入，后端支持 context 取消，发布前会再次复核 `go`、编译器、工具包装和支持目录。两次相同构建得到相同产物哈希 `7e591c43…`，失败构建前后旧产物哈希不变。规则见[构建事务、并发与清理](docs/reference/build-transactions.md)，验收见[06.03 验收记录](docs/validation/06-03-transactions.md)。

06.04 已完成：组装编译器发行目录。`./tools/package` 现在把 CLI、预构建前端宿主、修改版编译器、Go SDK、支持文件、锁文件、示例和许可证收进一个目录，并生成记录每个文件 sha256、大小和权限的 `release.json`。发行报告使用相对路径；把 339 MB、15,674 个文件的目录搬移后，包内 `tynel build examples/add.ts` 仍输出 `42`，记录中的 go、compiler、toolexec 都来自搬移目录。删文件、改支持库、写回开发机绝对路径分别以 `release.missing`、`release.mismatch`、`toolchain.path-escape` 退出 4。规则见[编译器发行目录](docs/reference/release.md)，验收见[06.04 验收记录](docs/validation/06-04-release.md)。

06.05 已完成：在干净目录重建工具和示例。`./tools/clean-build --tests` 从提交 `236cbe6` 建立新的 detached worktree，拒绝已有输出和未提交的跟踪文件；在全新的 `.work`、Go 构建缓存和模块缓存中下载并核验 go1.27.3 SDK、Go 源码和 typescript-go 归档，重建 bridge 与 CLI，18 个 Go 测试包通过。四个示例和新打包的 15,674 文件发行目录都通过；把发行目录复制到 `搬移 后的编译器` 后，`tynel build examples/add.ts` 仍输出 `42`。缺网退出 5、同尺寸坏哈希退出 6、错误 SDK 退出 8，旧式 `.tynel/compile` 诱饵没有参与构建。步骤见[复现指南](docs/reproducing.md#在干净目录重建)，验收见[06.05 验收记录](docs/validation/06-05-clean.md)。

06.06 已完成：四个 06.05 产物在限制性 macOS Seatbelt 环境中脱离源码、NIR、Node 和编译器目录运行，`add`、`choices`、`collatz`、`quote` 全部退出 0 且输出逐字节匹配；源码与 Node 拒绝探针也实际生效。产物只动态依赖 `libSystem` 和 `libresolv`。完整结果见[06.06 验收记录](docs/validation/06-06-independent.md)。

06.07 已完成：最终源码通过 517 项 Go 测试、96 项前端检查、13 项后端检查、49/49 个 Node 差分、9 项 CLI 故障与边界检查和 15 秒 NIR 模糊测试。验收中修正了 `-x` 与用户函数 `main` 被误报为内部故障的问题，现在分别按 `cap.bad-unary` 和 `cap.entry-name` 退出 3。能力追踪见[06.07 验收记录](docs/validation/06-07-mvp.md)，下一步进入 06.08。
