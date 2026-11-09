<script setup lang="ts">
</script>

<template>
  <div class="specimen" role="img" aria-label="编译示例：add.ts 经 tynel build 生成原生程序 out/add，运行输出 42">
    <div class="specimen-source">
      <div class="specimen-bar">
        <span class="specimen-file">add.ts</span>
        <span class="lang-tag lang-ts">TypeScript</span>
      </div>
      <pre class="specimen-code"><code><span class="kw">function</span> add(a: number, b: number): number {
  <span class="kw">return</span> a + b;
}

console.log(add(<span class="num">40</span>, <span class="num">2</span>));</code></pre>
    </div>
    <div class="specimen-step">
      <code class="specimen-cmd"><span class="specimen-prompt">$</span>tynel build add.ts -o out/add</code>
      <span class="specimen-target">darwin/arm64</span>
    </div>
    <div class="specimen-run">
      <div class="specimen-bar">
        <span class="specimen-file">out/add</span>
        <span class="lang-tag lang-go">原生程序</span>
      </div>
      <div class="specimen-output">
        <span class="specimen-prompt">$</span>./out/add<br>
        <span class="specimen-result">42</span>
      </div>
    </div>
    <p class="specimen-note">产物内置 Go 运行时，不需要 Node、源码或编译器目录。</p>
  </div>
</template>