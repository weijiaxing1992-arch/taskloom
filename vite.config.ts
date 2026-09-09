import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'
import { fileURLToPath, URL } from 'node:url'
import { themePalettePlugin } from './scripts/theme-palette.mjs'
import { uiTypographyPlugin } from './scripts/ui-typography.mjs'
import { pdfAssetsPlugin } from './scripts/pdf-assets.mjs'
export default defineConfig({
  // 独立门户作为静态资源发布，不经过业务界面的样式变换，也不替换业务路由入口。
  publicDir:false,
  plugins:[vue(),tailwindcss(),pdfAssetsPlugin()],
  // Open tabs still import older content-hashed route chunks after an update.
  // Cross-directory releases must also run scripts/retain-web-assets.mjs.
  build:{emptyOutDir:false},
  resolve:{alias:{'@':fileURLToPath(new URL('./src',import.meta.url))}},
  css:{postcss:{plugins:[themePalettePlugin(),uiTypographyPlugin()]}},
  server:{port:5173,proxy:{'/api':'http://127.0.0.1:8080'}},
})
