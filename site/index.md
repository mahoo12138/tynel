---
layout: home

hero:
  name: Tynel
  text: |-
    TypeScript，
    编译为原生二进制
  tagline: 类型检查来自 TypeScript，机器码来自 Go。一个学习导向的编译器：MVP 已验收，产物可以脱离 Node 与源码独立运行。
  actions:
    - theme: brand
      text: 快速开始
      link: /guide/getting-started
    - theme: alt
      text: 语言特性
      link: /language/features
    - theme: alt
      text: GitHub
      link: https://github.com/Mahoo12138/tynel

features:
  - title: 类型安全，两道检查
    details: tsgo 类型检查之后还有原生能力白名单。any、隐式转换和未实现的写法在编译期拒绝；错误码区分「代码有错」和「尚未支持」，不混成一个编译失败。
  - title: 独立原生二进制
    details: 产物是单个可执行文件，指令选择、寄存器分配与链接由 Go 工具链完成。验收时在空目录、清空环境下运行成功，不需要 Node、源码或编译器。
  - title: Go 运行时基座
    details: GC 与调度器随二进制分发，为后续能力打底。当前子集专注数值与布尔计算；用户态并发与 Windows 目标明确列在路线图中，不算已实现。
---