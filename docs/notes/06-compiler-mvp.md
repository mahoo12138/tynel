# 把编译器交给下一位使用者

前五章从一份手写 NIR 走到 TypeScript 源码、条件、循环和多文件。它们回答的是“编译器内部怎样把程序变成机器码”。这一章换一个视角：把已经能工作的编译器装进一个目录，交给一个不打开仓库源码的人使用。

输入是一个四文件报价项目，输出是一个可以脱离编译器运行的 exe。实验要确认三件事：发行目录搬走后仍能编译；只移走 exe 后仍能运行；写错源码时构建失败，但上一份成功产物不被覆盖，修正后能重新构建。

这一章不增加语言语法。它验收的是编译器本身的交付方式。

## 先得到编译器，而不是先编译程序

开发仓库里的 `./bin/tynel` 依赖仓库的 `.work` 工具链，适合开发者，不适合交给别人。发行命令会把这些运行编译器必需的东西收进一个目录：

```zsh
# 在仓库根目录重新构建 CLI，并组装当前提交的发行目录。
./tools/package
```

```text
已生成 /Users/mahoo/Projects/tynel/bin/tynel（commit 3c3ce69）
已生成 /Users/mahoo/Projects/tynel/out/release/tynel-3c3ce69-darwin-arm64（15674 个文件，darwin/arm64）
  版本       tynel dev（commit 3c3ce69）
  清单       /Users/mahoo/Projects/tynel/out/release/tynel-3c3ce69-darwin-arm64/release.json
```

这里生成的是本机 `darwin/arm64` 工具，不是压缩包，也没有签名。`release.json` 记录每个文件的 sha256、大小和权限；它能发现单方面改动，不能防止文件和清单一起被改。

把目录搬到另一个带空格和中文的路径，再复核清单：

```zsh
# 本章的临时实验目录；一次运行使用一个新目录。
PACKAGE="out/release/tynel-$(git rev-parse --short HEAD)-darwin-arm64"
WORK="$PWD/.work/article"
MOVED="$WORK/搬移 后的编译器"
PROJECT="$WORK/项目"
OUTPUT="$WORK/产品/quote"

mkdir -p "$WORK"

# 复制发行目录，而不是继续使用 out/release 下的原目录。
cp -R "$PACKAGE" "$MOVED"

# 从清单复核搬移后的 15,674 个文件。
./tools/package --verify "$MOVED"
```

```text
清单有效：/…/.work/article/搬移 后的编译器（15674 个文件，darwin/arm64）
```

目录里有 `bin/tynel`、预构建的 TypeScript 前端宿主、修改版 Go 编译器、Go SDK、固定支持包和工具包装。它们共同满足“编译下一份源码”的需要；它们不是运行用户程序的需要。

## 编译一个独立项目

报价项目原本在 `examples/quote`。把它复制到发行目录之外，保持四个文件不变：

```zsh
# 入口、折扣、运费和纯类型模块放进独立项目目录。
cp -R examples/quote "$PROJECT"
```

现在从搬移后的编译器目录执行构建。`--root` 指向项目根，`main.ts` 相对这个根解释：

```zsh
# 用发行目录内的编译器编译外部项目，产物写到编译器目录之外。
(cd "$MOVED" && ./bin/tynel build --root "$PROJECT" main.ts -o "$OUTPUT")
```

```text
已生成 /…/.work/article/产品/quote（2446626 字节）
  源码     main.ts（fd9cc3cbd795）
  输入     c1c6f489c2be
  身份     4e1f36deeb17
  编译器   7a75b4acf10b
  构建记录 /…/.work/article/产品/quote.build.json
```

运行产物，并与 Node 对照：

```zsh
# 原生程序输出三行报价。
"$OUTPUT"

# 使用同一份四个源文件运行参考实现。
node --experimental-strip-types examples/quote/main.ts
```

```text
90
10
100
```

两边逐字节一致。构建记录里的 `go`、编译器、工具包装都来自搬移后的发行目录，没有回到开发仓库的 `.work`。因此这次成功不是“恰好还能找到旧工具”。

## 只把 exe 带走

先把 exe 复制到一个新目录，目录里不复制 `.ts`、NIR、构建记录或任何编译器文件：

```zsh
# 只复制一个文件到独立运行目录。
ONLY="$WORK/独立运行"
mkdir -p "$ONLY"
cp "$OUTPUT" "$ONLY/quote"
```

从该目录运行，并用 `env -i` 清空继承的环境变量：

```zsh
# 当前目录只有 quote；没有 PATH、Node 或工具链环境。
(cd "$ONLY" && env -i ./quote)
```

```text
90
10
100
```

程序仍然运行。它需要 macOS 的 `libSystem` 和 `libresolv` 系统库，但不需要源码、Node、Go 工具链或发行目录。这里要区分两件事：

| 对象 | 运行需要什么 |
| --- | --- |
| 编译器 `bin/tynel` | 发行目录里的 Go SDK、修改版编译器、前端宿主和支持文件 |
| 编译出的 `quote` | `darwin/arm64` 的 macOS 系统库；不需要编译器目录 |

“把 exe 移走还能运行”不等于“exe 能在任何平台运行”。本章的产物只面向 `darwin/arm64`。

## 写错一次，再改回来

编译器不只要在顺利时工作。先用一个错误入口覆盖同一个输出路径：

```typescript
// bad.ts：第二个实参应该是 number。
function add(a: number, b: number): number {
  return a + b;
}

console.log(add(40, true));
```

```zsh
# 构建错误源码；预期在源码检查阶段失败。
(cd "$MOVED" && ./bin/tynel build --root "$PROJECT" bad.ts -o "$OUTPUT")
```

```text
/…/.work/article/项目/bad.ts:6:21: error TS2345: Argument of type 'boolean' is not assignable to parameter of type 'number'.
```

这次失败没有覆盖上一份成功产物。`"$OUTPUT"` 仍输出 `90,10,100`，构建记录也还是四文件入口的记录。目录里的文件不是“最后一条命令碰过的文件”，而是“最后一次成功发布的一对产物和记录”。

修正实参，再构建同一路径：

```diff
- console.log(add(40, true));
+ console.log(add(40, 2));
```

```zsh
# 修正后重新构建；成功才替换原产物。
(cd "$MOVED" && ./bin/tynel build --root "$PROJECT" bad.ts -o "$OUTPUT")

# 运行修正后的程序。
"$OUTPUT"
```

```text
42
```

这就是交付后的恢复路径：错误来源和位置可读，旧成功产物保留，修正源码后重新构建即可。编译器不会执行旧文件，也不会把上一次结果当成这一次成功。

## 六篇放在一起

六篇文章对应编译器的六个能力层：

1. [一份加法描述怎样成为二进制](01-native-ir.md)：手写 NIR、bridge、原生 42。
2. [从一份 TypeScript 源文件到二进制](02-source.md)：真实前端、能力检查和源码降低。
3. [让程序自己选一条路](03-choices.md)：boolean、分支、短路和三元式。
4. [用一个小工具试状态与循环](04-loops.md)：赋值、while、跳转、递归和 Collatz。
5. [把一个报价程序拆成四个文件](05-modules.md)：项目根、依赖图、绑定和初始化。
6. 本章：发行目录、独立运行和失败恢复。

每一层都能单独回放，也共同进入最终矩阵：[06.07 能力验收](../validation/06-07-mvp.md)逐项列出源码夹具、边界、错误分类和差分结果。本章的发行、运行和恢复结果见[06.08 交付验收](../validation/06-compiler-mvp.md)。

## 这一章之后

现在可以把一个 `darwin/arm64` 编译器目录交给别人，在不打开开发仓库的情况下编译约定子集的 TypeScript，并把得到的 exe 单独移走运行。支持范围、明确拒绝的写法和平台限制见[操作支持表](../reference/support.md)；命令参数与退出码见[命令行参考](../reference/cli.md)；发行目录的完整清单见[编译器发行目录](../reference/release.md)。

这一章对应完成标签 `mvp/06-compiler-mvp`。它不声称支持完整 TypeScript、Node 标准库、任意 Go 包或其他平台。
