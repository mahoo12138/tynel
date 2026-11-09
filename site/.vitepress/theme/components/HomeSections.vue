<script setup lang="ts">
const entries = [
  {
    title: '快速开始',
    desc: '准备隔离工具链，把第一个 TypeScript 文件编译成原生程序。',
    link: '/guide/getting-started',
  },
  {
    title: '语言特性总览',
    desc: '逐项查看当前支持、明确拒绝和仍在计划中的写法。',
    link: '/language/features',
  },
  {
    title: '命令行 API',
    desc: '命令契约、选项、路径规则与退出状态。',
    link: '/reference/cli',
  },
]
</script>

<template>
  <section class="tynel-section" aria-labelledby="dual-title">
    <h2 class="tynel-section-title" id="dual-title">一个程序，两种语言各司其职</h2>
    <p class="tynel-section-lede">
      你写 TypeScript；Go 负责把守机器码这一侧。两侧的代码在编译期汇合，产物里不再有前端。
    </p>
    <div class="dual">
      <div class="dual-pane">
        <div class="dual-head">
          <span class="lang-tag lang-ts">TypeScript</span>
          <span>你写的程序</span>
        </div>
        <pre><code><span class="kw">export function</span> <span class="fn">discount</span>(price: number, pct: number): number {
  <span class="kw">return</span> price * (<span class="num">1</span> - pct / <span class="num">100</span>);
}</code></pre>
      </div>
      <div class="dual-pane">
        <div class="dual-head">
          <span class="lang-tag lang-go">Go</span>
          <span>编译器手写的固定宿主支持</span>
        </div>
        <pre><code><span class="kw">func</span> <span class="fn">tynelPrintFloat64</span>(v float64) {
    fmt.Fprintln(os.Stdout, tynelFormatNumber(v))
}</code></pre>
      </div>
    </div>
    <p class="dual-note">
      右侧的支持代码对所有输入都相同，只提供数值输出等最小宿主能力。用户函数由
      bridge 在内存中构造成 Go 编译器节点，不会生成业务 .go 源码，也不内嵌
      JavaScript 引擎。<a href="/internals/go-bridge">了解桥接边界</a>
    </p>
    <div class="facts" role="list">
      <span class="fact" role="listitem"><strong>MVP 已验收</strong>，2026-11-09</span>
      <span class="fact" role="listitem">目标 <strong>darwin/arm64</strong></span>
      <span class="fact" role="listitem"><strong>48/48</strong> 子节点通过验证</span>
      <span class="fact" role="listitem">独立产物约 <strong>2.4 MB</strong></span>
    </div>
  </section>

  <section class="tynel-section" aria-labelledby="docs-title">
    <h2 class="tynel-section-title" id="docs-title">从文档开始</h2>
    <p class="tynel-section-lede">
      文档按入门、语言参考、API 与内幕组织。能力边界与验证记录以仓库规格为准。
    </p>
    <div class="doc-entry">
      <a v-for="entry in entries" :key="entry.link" class="doc-card" :href="entry.link">
        <h3>{{ entry.title }}</h3>
        <p>{{ entry.desc }}</p>
      </a>
    </div>
  </section>
</template>