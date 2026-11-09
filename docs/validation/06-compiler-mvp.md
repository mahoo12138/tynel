# 06.08 验收记录：最终交付实验与第六篇

状态：**passed**，06.08 标为 `verified`，06 主节点完成。最终交付实验在 2026-11-09 13:15–13:25（CST）执行：从当前源码打包 15,674 个文件，搬到含空格和中文的路径后复核清单，用包内编译器构建独立四文件项目，只把 exe 复制到空目录并在 `env -i` 下运行；随后复现类型错误、确认旧产物未被覆盖并成功恢复。

## 环境与身份

| 项目 | 实际值 |
| --- | --- |
| 系统 | macOS 26.6.2，Darwin 25.6.0，`arm64`，Apple M4 |
| 打包源码 | `3c3ce69`；06.08 文档提交不改变编译器代码 |
| 编译器版本 | `tynel dev (commit 3c3ce69)` |
| 工具链 | go1.27.3；typescript-go `typescript/v7.0.2` |
| 原始发行目录 | `out/release/tynel-3c3ce69-darwin-arm64`，339 MB，15,674 个文件 |
| 搬移后目录 | `.work/article/搬移 后的编译器` |
| 独立项目 | `.work/article/项目`，由 `examples/quote` 复制 |
| Node 对照 | v22.22.2 |

关键内容身份：

| 对象 | sha256 | 字节 |
| --- | --- | ---: |
| `release.json` | `deb30401e62c4e744710d2fe123fae618ab302a8e796a742affe6c27d46fed76` | 清单 |
| `bin/tynel` | `a4d9630b1d9c7359f26d134ec2dad010bef9b9c05a24dfcee330dad19d71ca8f` | 5,137,682 |
| 初始四文件产物 | `ec73bd13370aebbd1343d6f69b55d9795b7a560351ce7afd97e1e18653b6d577` | 2,446,626 |
| 修正后的 `bad.ts` 产物 | `d016a3191a32952683da5cbfedb28cc017127c2cf47995c158468a9d2a22d54e` | 2,446,530 |

搬移前清单校验登记 15,674 个文件。构建用户程序后，搬移目录里另有 `.work/build` 下的运行时快照，因此直接统计文件数会多于清单；清单只覆盖发行内容，不登记运行期新增文件。

## 取得工具与搬移

```zsh
# 从当前提交重建 CLI、组装发行目录并自测示例。
./tools/package

# 复制到带空格和中文的新路径。
PACKAGE="out/release/tynel-$(git rev-parse --short HEAD)-darwin-arm64"
WORK="$PWD/.work/article"
MOVED="$WORK/搬移 后的编译器"
mkdir -p "$WORK"
cp -R "$PACKAGE" "$MOVED"

# 从仓库复核搬移后的发行清单。
./tools/package --verify "$MOVED"
```

```text
已生成 /Users/mahoo/Projects/tynel/out/release/tynel-3c3ce69-darwin-arm64（15674 个文件，darwin/arm64）
清单有效：/…/.work/article/搬移 后的编译器（15674 个文件，darwin/arm64）
```

在搬移目录中运行 `./bin/tynel version` 与 `./bin/tynel toolchain`，显示的 SDK、`go`、`compile`、`link`、`asm` 都位于搬移目录内。独立项目构建记录的 `tools.go`、`tools.compiler`、`tools.toolexec` 也全部以搬移目录为前缀，没有回落到开发仓库的 `.work`。

## 编译独立项目

```zsh
# 把四个源文件的报价项目复制到发行目录之外。
PROJECT="$WORK/项目"
cp -R examples/quote "$PROJECT"

# 用包内编译器构建，产物写到包外。
OUTPUT="$WORK/产品/quote"
(cd "$MOVED" && ./bin/tynel build --root "$PROJECT" main.ts -o "$OUTPUT")
```

```text
已生成 /…/.work/article/产品/quote（2446626 字节）
  源码     main.ts（fd9cc3cbd795）
  输入     c1c6f489c2be
  身份     4e1f36deeb17
  编译器   7a75b4acf10b
```

`"$OUTPUT"` 输出 `90,10,100`，与 `node --experimental-strip-types examples/quote/main.ts` 逐字节一致。使用的入口、三个依赖、类型边、初始化顺序都与 06.07 的源码矩阵一致。

## 只移动 exe 后运行

```zsh
# 独立运行目录不复制源码、NIR、构建记录或编译器。
ONLY="$WORK/独立运行"
mkdir -p "$ONLY"
cp "$OUTPUT" "$ONLY/quote"

# 清空继承环境，只运行这个文件。
(cd "$ONLY" && env -i ./quote)
```

```text
90
10
100
```

目录里只有 `quote`。程序不需要编译器目录、Node、Go 或 TypeScript 源码；它仍需要 macOS 的 `libSystem` 和 `libresolv`，与 06.06 的 Mach-O 检查一致。这个结果不扩展到其他平台。

## 可恢复错误

`bad.ts` 故意把 `boolean` 传给 `number` 参数：

```typescript
function add(a: number, b: number): number {
  return a + b;
}

console.log(add(40, true));
```

```zsh
# 构建错误源码到同一输出路径。
(cd "$MOVED" && ./bin/tynel build --root "$PROJECT" bad.ts -o "$OUTPUT")
```

```text
/…/.work/article/项目/bad.ts:6:21: error TS2345: Argument of type 'boolean' is not assignable to parameter of type 'number'.
```

实际退出状态为 1。构建前后的产物 sha256 都是 `ec73bd13…`，构建记录也保持不变；`"$OUTPUT"` 仍输出 `90,10,100`。失败没有把旧成功产物替换成半成品。

把 `true` 改成 `2` 后重建同一路径：

```zsh
# 修正源码后重新构建并运行。
(cd "$MOVED" && ./bin/tynel build --root "$PROJECT" bad.ts -o "$OUTPUT")
"$OUTPUT"
```

```text
42
```

新产物 sha256 是 `d016a319…`。错误、恢复和旧产物保留都有实际文件哈希与运行输出作证。

## 六篇与阶段标签

| 章节 | 笔记 | 阶段验收 | 标签 |
| --- | --- | --- | --- |
| 01 | `docs/notes/01-native-ir.md` | `docs/validation/01-native-ir.md` | `mvp/01-native-ir` |
| 02 | `docs/notes/02-source.md` | `docs/validation/02-source.md` | `mvp/02-source` |
| 03 | `docs/notes/03-choices.md` | `docs/validation/03-choices.md` | `mvp/03-choices` |
| 04 | `docs/notes/04-loops.md` | `docs/validation/04-loops.md` | `mvp/04-loops` |
| 05 | `docs/notes/05-modules.md` | `docs/validation/05-modules.md` | `mvp/05-modules` |
| 06 | `docs/notes/06-compiler-mvp.md` | 本文件 | `mvp/06-compiler-mvp` |

前五个标签已经存在并保持不动。第六个标签在本验收提交通过最终回归后创建，指向包含本文件、第六篇最终版本和 `docs/stages.json` 完成状态的提交。

## 最终回归

06.07 的最终矩阵全部通过：20 个含测试的 Go 包、517 项测试、前端 96 项、后端 13 项、Node 差分 49/49、CLI 故障边界和 15 秒模糊测试，均无跳过。详细记录见[06.07 完整能力验收](06-07-mvp.md)。

06.08 在完成文章与状态索引后再次运行 `./tools/test`，20 个含测试的包通过；`go vet`、JSON 索引解析和六篇链接检查也通过。所有 48 个子节点的最终状态由 `docs/stages.json` 逐项登记。

## 边界

- 交付物是本地 `darwin/arm64` 目录，不是签名安装包或远程发行物；清单不是数字签名。
- 编译器目录需要 Go SDK、修改版编译器、前端宿主和支持文件；用户程序不需要它们，但仍依赖 macOS 系统库。
- 只验证 `darwin/arm64`。没有 Windows、其他架构、完整 TypeScript、Node 标准库或任意 Go 包兼容性。
- 没有把完整哈希、标签维护和发布操作写进第六篇的实验叙事；这些内容在本记录与发行参考中。
- 本轮没有推进应用服务、数据库、前端页面或其他平台，也没有推送远程仓库。
