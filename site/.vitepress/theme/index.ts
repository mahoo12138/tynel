import DefaultTheme from 'vitepress/theme'
import { h } from 'vue'
import HeroSpecimen from './components/HeroSpecimen.vue'
import PipelineStrip from './components/PipelineStrip.vue'
import HomeSections from './components/HomeSections.vue'
import '@fontsource/space-grotesk/500.css'
import '@fontsource/space-grotesk/700.css'
import '@fontsource/jetbrains-mono/400.css'
import '@fontsource/jetbrains-mono/600.css'
import './custom.css'

export default {
  extends: DefaultTheme,
  Layout() {
    return h(DefaultTheme.Layout, null, {
      'home-hero-image': () => h(HeroSpecimen),
      'home-features-before': () => h(PipelineStrip),
      'home-features-after': () => h(HomeSections),
    })
  },
}