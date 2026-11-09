<script setup lang="ts">
const steps = [
  { name: 'TypeScript 源码', desc: '显式相对 .ts 的项目文件', tone: 'is-ts' },
  { name: 'tsgo 类型检查', desc: '完整 TypeScript 诊断与位置', tone: '' },
  { name: '声明事实', desc: 'Tynel 自有的声明与引用数据', tone: '' },
  { name: 'NIR', desc: '独立验证的中间表示', tone: '' },
  { name: 'Go bridge', desc: '在内存中构造 Go 编译器节点', tone: '' },
  { name: '原生二进制', desc: '指令选择与链接交给 Go', tone: 'is-go' },
]
</script>

<template>
  <section class="tynel-section" aria-labelledby="pipeline-title">
    <h2 class="tynel-section-title" id="pipeline-title">编译流水线</h2>
    <p class="tynel-section-lede">
      从左到右，每一步都有明确职责。前端句柄不出 facade 会话，NIR 之后不依赖
      TypeScript；机器码由 fork 的 Go 编译器生成，不另造后端。
    </p>
    <ol class="pipeline">
      <li v-for="step in steps" :key="step.name" class="pipeline-step" :class="step.tone">
        <span class="step-name">{{ step.name }}</span>
        <span class="step-desc">{{ step.desc }}</span>
      </li>
    </ol>
  </section>
</template>