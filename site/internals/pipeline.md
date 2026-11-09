# 编译流水线

一次 `check`、`build` 或 `run` 共用同一条前半程。本页按阶段说明谁读什么、产出什么、在哪一步停下。

## 七个步骤

1. **解析入口**：按项目根把入口固定成绝对路径；没有 `--root` 时项目根是仓库根。
2. **准备前端宿主**：宿主是 `bridge/tsgo/consumer`，一个独立模块里的子进程。缺失或与源码不一致时，命令行用隔离工具链重建它，进度写标准错误。
3. **源码检查**：tsgo 把 TypeScript 诊断连同 UTF-8 字节区间与行列交回来。
4. **能力检查**：判断程序是否落在原生子集内，结论与源码错误分开。
5. **降低与验证**：事实降低成 NIR，再由独立验证器检查引用、实参、类型与返回路径。

`check` 到这里就停。`build` 与 `run` 继续：

6. **后端**：NIR 写成快照，交给修改版 Go 编译器，经 `-toolexec` 包装的 `compile` 生成目标文件，再由 Go 自己的 `link` 链接。
7. **发布**：产物先写临时文件，成功后才改名；构建记录同时发布。`run` 在发布后立即执行本次产物。

## 事实：前端的输出边界

tsgo 的内部句柄只在 facade 会话内使用。会话关闭后，留下的是 Tynel 自有的「事实」：声明身份、源位置、引用、调用与表达式类型。降低阶段只消费事实，不回头依赖 TypeScript 对象。

```zsh
# 观察一个文件的事实（JSON）。
./bin/tynel dump facts examples/add.ts
```

事实先自洽校验：ID 重复或引用悬空按内部故障处理，不基于坏数据给出能力结论。

## NIR：独立于两端

NIR 保存程序结构：函数、参数语义、局部存储、操作类型与执行顺序。数字字面量是 64 位位模式，负零、NaN 和无穷不被改写。NIR 及其验证器不依赖 tsgo 或 Go 内部对象。

```zsh
# 导出通过验证的 NIR，再单独回放；两次产物逐字节相同。
./bin/tynel dump nir examples/add.ts > out/add.nir.json
./bin/tynel build-ir out/add.nir.json -o out/add-replay
```

验证器检查格式、操作、引用、类型、返回路径与资源预算。错误输入不会变成运行时的「编译器尚未实现」分支。

## 多文件合并

入口有值导入时，依赖图里的模块合并成一个 NIR：函数名带模块前缀，模块体变成 `__init$N`，入口 `main` 按初始化顺序调用。类型边只参与检查，不产生运行时调用。

```zsh
# 分别观察图、符号绑定与初始化计划。
./bin/tynel dump graph --root examples/quote main.ts
./bin/tynel dump modules --root examples/quote main.ts
./bin/tynel dump init --root examples/quote main.ts
```

## 哈希链与构建身份

构建记录把源码、依赖、NIR、配置、工具链与产物串成一条哈希链：`source.sha256` 对入口，`identity.files` 对全部依赖，`input.sha256` 对降低结果，`output.sha256` 对产物。构建前后复核依赖与工具内容；改动任何输入都会反映在身份里。

修改版编译器带戳记。bridge 源码或 SDK 源码变了而编译器没重编时，构建按「编译器已过期」退出 4，不产出任何文件。

## 职责边界

命令行只做四件事：解析参数、准备前端宿主、按结果选择诊断类别、把结果写到标准输出或标准错误。它不解析 TypeScript，也不解析 Go 的内部数据结构。外部工具通过统一的 Runner 接口调用，测试通过固定工具表替换真实进程，错误路径不依赖本机装了什么。