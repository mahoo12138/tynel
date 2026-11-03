package main

import (
	"fmt"
	"io"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
)

const usageText = `tynel 是 TypeScript 到原生二进制的编译器，首版目标为 darwin/arm64。

用法：
  tynel <命令> [参数]

用户命令：
  version                       显示程序、目标平台和配套工具身份
  check <file.ts> [--root <目录>] [--json]
                                检查类型与原生能力，验证 NIR，不生成机器码
  build <file.ts> -o <路径> [--root <目录>] [--target <平台>]
                                编译成原生程序，不运行
  run <file.ts> [--root <目录>] [--target <平台>]
                                编译并运行本次产物

开发与调试命令（不属于用户构建流程）：
  dump facts|nir|graph|modules|init <file.ts> [--root <目录>]
                                输出事实、NIR、依赖图、符号表或初始化计划（JSON）
  build-ir <NIR> -o <路径> [--target <平台>]
                                从固定 NIR 回放，不读 TypeScript
  toolchain                     显示锁定工具链和各工具的实际路径
  toolchain which <名>          只解析一个工具，输出绝对路径
  help [命令]                   显示总体帮助或单个命令说明

路径与诊断：
  源文件相对 --root 解释；省略 --root 时使用编译器检出根。
  构建输出和 build-ir 的输入、输出相对当前工作目录解释。
  --target 省略时使用锁文件的主目标，目前只支持 darwin/arm64。
  --diagnostics=json 把失败写成 JSON 信封到标准错误，并隐藏进度日志；
  默认为 human。选项可以放在命令前后；程序自己的标准输出和标准错误不变。

退出状态：
  0  成功
  1  源码错误
  2  用法错误
  3  能力限制
  4  环境缺失
  5  内部故障

run 的退出状态是被运行程序的状态；编译失败时仍用 1–5。
`

var topicHelp = map[string]string{
	"check": `tynel check <file.ts> [--root <目录>] [--json]

读取一份 TypeScript 源文件，先做类型检查，再判断它是否落在原生子集内，
最后降低并验证 NIR。它不生成机器码，也不检查后端工具是否可用：check 通过
只说明这份程序可以进入后端，不保证 SDK 或修改版编译器已就绪。

参数：
  <file.ts>      入口源文件；相对 --root 解释。
  --root <目录>  项目根。省略时使用编译器检出根，而不是当前工作目录。
  --json         把完整结果写到标准输出；诊断、能力结论和摘要都在同一份
                 JSON 里，此时标准错误为空。选项也可以写成 --root=<目录>。

默认把诊断写到标准错误，成功时在标准输出打一行摘要。check 不接受 -o 和
--target：它不生成机器码，目标只影响 build、run 和 build-ir。

退出状态：源码错误 1，用法错误 2，能力限制 3，环境缺失 4，通过 0。
`,
	"build": `tynel build <file.ts> -o <路径> [--root <目录>] [--target <平台>]

从 TypeScript 源文件生成原生程序。流程是检查、能力判断、降低、验证、调用
修改版 Go 编译器，最后原子发布产物；构建成功也不运行程序。

参数：
  <file.ts>      入口源文件；相对 --root 解释。
  -o <路径>      必填。相对当前工作目录解释，不会写到项目根下；也可以写成
                 -o=<路径>。构建记录写在 <产物>.build.json。
  --root <目录>  项目根。省略时使用编译器检出根。
  --target <平台>
                 默认使用锁文件的主目标。目前只支持 darwin/arm64；其他值
                 以能力限制退出 3，不交叉编译，也不改用其他后端。

产物先写到目标目录的临时文件，成功后才改名；构建失败不会覆盖已有的产物。
构建记录写不进去时产物会被删除：没有记录的产物无法复核来源。
首次运行会按需构建前端宿主，进度写在标准错误，不混入标准输出。

退出状态：源码错误 1，用法错误 2，能力限制 3，环境缺失 4，内部故障 5。
`,
	"run": `tynel run <file.ts> [--root <目录>] [--target <平台>]

编译并立即运行本次产物，产物固定放在编译器检出目录的 .work/run/app。
它不执行任何先前存在的文件：构建失败就直接返回，即使目标位置留着上一次
的产物也不会被运行。

参数：
  <file.ts>      入口源文件；相对 --root 解释。
  --root <目录>  项目根。省略时使用编译器检出根。
  --target <平台>
                 默认使用锁文件的主目标；目前只支持 darwin/arm64。
                 run 不接受 -o，产物路径固定。

程序的标准输出与标准错误直接连到当前终端。程序退出状态原样作为本命令的
退出状态；编译失败时仍用 1–5 这几类。

退出状态：编译失败时 1–5；程序启动后原样传出它的状态。
`,
	"dump": `tynel dump facts|nir|graph|modules|init <file.ts> [--root <目录>]

输出前端事实、降低后的 NIR、依赖图、模块符号表或初始化计划，只有 JSON，
没有进度日志。源码有错时退出 1，超出原生子集时退出 3，两种情况都不输出
JSON。dump 是调试接口，不生成机器码，也不接受 -o、--json 或 --target。

参数：
  <内容名>       facts、nir、graph、modules 或 init，必须写在源文件前面。
  <file.ts>      入口源文件；相对 --root 解释。
  --root <目录>  项目根。省略时使用编译器检出根。

退出状态：源码错误 1，用法错误 2，能力限制 3，环境缺失 4，内部故障 5。
`,
	"version": `tynel version

显示程序版本、编译时记录的提交、目标平台、锁定的 Go 与 TypeScript 前端
身份，以及工具链准备状态。Go 和前端身份来自 tools/toolchain/go.lock.json，
准备状态来自 .work/toolchain/toolchain.json；文件缺失时如实标注，不猜造
身份。

version 不接受参数。不在项目目录内运行时仍输出程序版本，但把目标平台标为
未知并以 0 退出；需要项目的命令按环境缺失以 4 退出。
`,
	"help": `tynel help [命令]

不带参数时输出总体用法。带命令名时输出该命令的参数、路径和退出说明；
未知命令名按用法错误退出。可用主题为 version、check、build、run、dump、
build-ir、toolchain 和 help。
`,
	"build-ir": `tynel build-ir <NIR> -o <路径> [--target <平台>]

读取一份 NIR 文件，用修改版 Go 编译器编成原生程序。构建前先解码并验证
NIR；产物先写到目标目录的临时文件，成功后才改名发布，失败不会覆盖已有
的产物。构建本身不运行程序。

build-ir 是开发调试接口，不读 TypeScript，也不接受 --root：输入和输出
相对当前工作目录解释，工具链根由编译器自动定位。成功后在产物旁边写一份
<产物>.build.json，记录输入、工具和输出的哈希。

参数：
  <NIR>          一份 JSON 格式的 NIR 文件。
  -o <路径>      必填；也可以写成 -o=<路径>。
  --target <平台>
                 默认使用锁文件的主目标；目前只支持 darwin/arm64。

退出状态：坏 NIR 或源码类别错误 1，用法错误 2，能力限制 3，环境缺失 4，
内部故障 5。
`,
	"toolchain": `tynel toolchain
tynel toolchain which <名>

第一条命令读取 .work/toolchain/toolchain.json，列出 SDK 目录和
go、compile、link、asm 的实际路径，并确认每个工具都能解析。
第二条命令只解析一个工具，可用名称为 go、compile、link、asm。

两条命令都只做检查，不修改工具链；准备工具链请用 ./tools/toolchain/prep。
`,
}

func writeUsage(w io.Writer) {
	fmt.Fprint(w, usageText)
}

func (a *app) help(args []string) error {
	if len(args) == 0 {
		writeUsage(a.stdout)
		return nil
	}
	if len(args) > 1 {
		return diagnostic.New(diagnostic.Usage, "cli.unexpected-argument", "help 只接受一个命令名：%q", args[1])
	}
	text, ok := topicHelp[args[0]]
	if !ok {
		return diagnostic.New(diagnostic.Usage, "cli.unknown-topic", "没有 %q 的帮助；用 tynel help 查看命令列表", args[0])
	}
	fmt.Fprint(a.stdout, text)
	return nil
}
