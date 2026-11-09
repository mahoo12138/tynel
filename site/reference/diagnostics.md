# 诊断与错误码

一条失败只有一个来源：源码、能力、环境、外部工具错误都由同一份诊断描述，人类输出与 JSON 输出是它的两种投影。本页给出信封字段、类别与阶段、能力错误码表。

## 诊断信封

`--diagnostics=json` 失败时，标准错误只有一个 JSON 文档：

```json
{
  "schemaVersion": 1,
  "ok": false,
  "exitCode": 1,
  "diagnostics": [
    {
      "category": "source",
      "categoryName": "源码错误",
      "code": "TS2322",
      "stage": "frontend",
      "message": "Type 'string' is not assignable to type 'number'.",
      "severity": "error",
      "file": "/…/math.ts",
      "line": 1,
      "column": 14
    }
  ]
}
```

| 字段 | 说明 |
| --- | --- |
| `category` | 稳定机器字段：`source` / `usage` / `capability` / `environment` / `internal` |
| `code` | 稳定标识，如 `TS2322`、`cap.any`、`build.output-unwritable` |
| `stage` | 失败发生在流水线哪一段 |
| `message` | 面向人的说明；TypeScript 诊断保留原文 |
| `file` / `line` / `column` | 有源码位置时填写；程序级诊断可以没有 |
| `severity` | TypeScript 的 `error` / `warning`；其他类别为空 |
| `ok` / `exitCode` | 只有警告时 `ok` 仍为 `true`，退出 0 |

类别决定退出状态，阶段只说明位置。一次失败一个信封，内部可有多条诊断，顺序与人类模式一致。

## 类别与退出状态

| 类别 | 退出 | 含义 | 恢复方向 |
| --- | --- | --- | --- |
| `source` | 1 | 代码本身有错 | 改源码 |
| `usage` | 2 | 命令用法错 | 查 `tynel help` |
| `capability` | 3 | 写法合法但后端不支持 | 换写法或等实现 |
| `environment` | 4 | 工具或环境没准备好 | 按消息准备工具链 |
| `internal` | 5 | 编译器自身故障 | 保留诊断反馈 |

把「代码有错」和「尚未支持」分开，使用者才知道该改代码还是该等实现。`run` 启动程序后不写编译器诊断：有信封表示编译失败，没有信封但非零退出表示程序自己退出。

## 阶段

| 阶段 | 负责的工作 |
| --- | --- |
| `cli` | 命令、选项和帮助解析 |
| `project` | 项目根、入口路径和源文件读取 |
| `graph` | 导入发现、依赖加载和环检测 |
| `modules` | 跨模块绑定、导出检查和初始化计划 |
| `frontend` | TypeScript 检查、声明事实和宿主进程 |
| `capability` | 原生子集判断 |
| `lower` | 事实降低为 NIR |
| `nir` | NIR 编码、解码与验证 |
| `build` | 构建事务、快照和输出发布 |
| `backend` | 修改版 Go 编译器的调用 |
| `publish` | 产物改名与记录写入 |
| `run` | 启动刚构建的程序 |
| `toolchain` | SDK 与工具定位 |
| `target` | 目标平台检查 |

阶段默认从错误码前缀推导；跨进程边界可显式覆盖。

## 能力错误码

能力检查遍历每一个函数体和常量初始化表达式，包括未被调用的函数与不可达语句。

| 代码 | 触发条件 |
| --- | --- |
| `cap.any` | 参数、返回值或常量声明为 `any` / `unknown` |
| `cap.type-escape` | `as`、`<T>x`、非空断言、`satisfies` 等绕过检查的写法 |
| `cap.unsupported-syntax` | 对象、数组、字符串、`for` / `do`、未初始化 `let`、`var`、`new`、嵌套/匿名/默认导出函数等 |
| `cap.unknown-callee` | 调用目标不是已声明函数、`console.log` 或值导入绑定的导出 |
| `cap.forbidden-global` | 调用 `eval` |
| `cap.shadowed-console` | `console` 被本文件声明遮蔽 |
| `cap.bad-console-call` | `console.log` 参数不是恰好一个 `number` 或 `boolean` |
| `cap.bad-operator` | 运算符不在支持范围，包括宽松 `==` / `!=`、取余、位运算 |
| `cap.bad-unary` | 一元 `-` / `+` 作用于非数字字面量 |
| `cap.entry-name` | 用户函数占用固定入口名 `main` |
| `cap.module-let` | 模块级 `let` |
| `cap.assign-immutable` | 给 `const`、参数或模块常量赋值 |
| `cap.assign-target` | 赋值目标不是直接名字 |
| `cap.assignment-value` | 赋值出现在表达式里 |
| `cap.compound-assignment` | 复合赋值 |
| `cap.inc-dec` | 自增自减 |
| `cap.labeled-jump` | 带标签的 `break` / `continue` 或带标签语句 |
| `cap.loop-target` | `break` / `continue` 不在循环里 |
| `cap.unknown-name` | 引用未绑定的外部名称 |
| `cap.unsupported-parameter` | 参数带默认值或可选标记 |
| `cap.arg-count` | 实参个数不在允许范围 |
| `cap.void-value` | 把 void 调用当作值使用 |
| `cap.bad-type` | 类型越界：`string` 参数、`!1`、非布尔条件、混型三元式等 |
| `cap.interface` | 接口声明 |
| `target.unsupported` | 请求了 `darwin/arm64` 之外的目标 |

消息里带具体写法名（如「对象字面量」），因为「不支持」三个字本身不提供线索。

## 常见模块与环境错误

| 代码 | 含义 |
| --- | --- |
| `graph.cycle` | 依赖环，消息打印完整路径 |
| `module.missing-export` | 目标模块没有该导出，消息带两个文件路径 |
| `module.captured-variable` | 函数读取模块级运行时变量 |
| `build.output-busy` | 同一输出已有构建持锁，退出 4 |
| `build.output-unwritable` | 输出目录无法创建，退出 4 |
| `build.lock-unsupported` | 当前平台没有实现构建锁（非 Unix），退出 4 |

## 跨文件位置

入口与显式导入的依赖都读入快照；前端诊断按文件名选择快照，再把 UTF-8 字节区间换算为行列。依赖文件里的错误与入口一样有准确位置。缺导出与依赖环的诊断写出完整文件路径。