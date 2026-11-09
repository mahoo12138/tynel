import { defineConfig } from 'vitepress'

export default defineConfig({
  lang: 'zh-CN',
  title: 'Tynel',
  description:
    '把 TypeScript 编译为原生二进制的学习导向编译器。类型检查来自 TypeScript，机器码来自 Go。',
  outDir: '../out/site',
  head: [
    ['link', { rel: 'icon', type: 'image/svg+xml', href: '/logo.svg' }],
    ['meta', { name: 'theme-color', content: '#3178c6' }],
    ['meta', { property: 'og:title', content: 'Tynel — TypeScript，编译为原生二进制' }],
    [
      'meta',
      {
        property: 'og:description',
        content: '类型检查来自 TypeScript，机器码来自 Go。MVP 已验收，目标 darwin/arm64。',
      },
    ],
  ],
  markdown: {
    theme: { light: 'github-light', dark: 'github-dark' },
    lineNumbers: false,
  },
  themeConfig: {
    logo: '/logo.svg',
    siteTitle: 'Tynel',
    nav: [
      { text: '指南', link: '/guide/', activeMatch: '/guide/' },
      { text: '语言参考', link: '/language/features', activeMatch: '/language/' },
      { text: 'API 参考', link: '/reference/cli', activeMatch: '/reference/' },
      { text: '内幕', link: '/internals/pipeline', activeMatch: '/internals/' },
      { text: '路线图', link: '/project/roadmap', activeMatch: '/project/' },
    ],
    sidebar: [
      {
        text: '入门',
        items: [
          { text: '简介', link: '/guide/' },
          { text: '快速开始', link: '/guide/getting-started' },
          { text: 'CLI 使用', link: '/guide/cli' },
        ],
      },
      {
        text: '语言参考',
        items: [
          { text: '语言特性总览', link: '/language/features' },
          { text: '值与表达式', link: '/language/values' },
          { text: '语句与控制流', link: '/language/statements' },
          { text: '函数与模块', link: '/language/modules' },
        ],
      },
      {
        text: 'API 参考',
        items: [
          { text: '命令行 API', link: '/reference/cli' },
          { text: '诊断与错误码', link: '/reference/diagnostics' },
        ],
      },
      {
        text: '编译器内幕',
        items: [
          { text: '编译流水线', link: '/internals/pipeline' },
          { text: 'Go 桥接与运行时', link: '/internals/go-bridge' },
        ],
      },
      {
        text: '项目',
        items: [{ text: '路线图与边界', link: '/project/roadmap' }],
      },
    ],
    outline: { level: [2, 3], label: '本页目录' },
    docFooter: { prev: '上一篇', next: '下一篇' },
    returnToTopLabel: '回到顶部',
    sidebarMenuLabel: '菜单',
    darkModeSwitchLabel: '外观',
    lightModeSwitchTitle: '切换到亮色主题',
    darkModeSwitchTitle: '切换到暗色主题',
    search: {
      provider: 'local',
      options: {
        translations: {
          button: { buttonText: '搜索文档', buttonAriaLabel: '搜索文档' },
          modal: {
            noResultsText: '无法找到相关结果',
            resetButtonTitle: '清除查询',
            displayDetails: '显示详细结果',
            footer: { selectText: '选择', navigateText: '切换', closeText: '关闭' },
          },
        },
      },
    },
    socialLinks: [{ icon: 'github', link: 'https://github.com/Mahoo12138/tynel' }],
    footer: {
      message: '学习导向的 TypeScript 原生编译器，MVP 已验收。',
    },
  },
})