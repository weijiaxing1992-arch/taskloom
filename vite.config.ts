import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'
import { fileURLToPath, URL } from 'node:url'
import { relative } from 'node:path'
import { themePalettePlugin } from './scripts/theme-palette.mjs'
import { uiTypographyPlugin } from './scripts/ui-typography.mjs'
import { pdfAssetsPlugin } from './scripts/pdf-assets.mjs'
const applicationRoot=fileURLToPath(new URL('.',import.meta.url))
// Only application files participate in development updates. Historical builds,
// native clients, databases and delivery archives can contain millions of files.
const ignoredDevelopmentFile=(file:string)=>{
  const path=relative(applicationRoot,file).replaceAll('\\','/')
  return /^(?:work|outputs|交付文件夹|开源交付|clients|data|dist|dist-developer|build|archives?|backups?|releases?)(?:\/|$)/.test(path)
    || /^cmd\/server\/assets(?:\/|$)/.test(path)
    || /\.(?:db|sqlite|sqlite3)(?:-(?:wal|shm|journal))?$/i.test(path)
    || /\.(?:zip|7z|rar|tar(?:\.(?:gz|bz2|xz|zst))?|tgz|dmg|bak)$/i.test(path)
}
export default defineConfig({
  // 社区业务应用独立发布，不包含商业门户与安装包。
  publicDir:false,
  plugins:[vue(),tailwindcss(),pdfAssetsPlugin()],
  // Build into a clean release directory; retain old hashed assets during deployment if needed.
  // 桌面 Vue 应用和移动 React 工作台独立出包；两者使用同一个受鉴权 API。
  build:{emptyOutDir:true,rollupOptions:{input:{main:fileURLToPath(new URL('./index.html',import.meta.url)),mobile:fileURLToPath(new URL('./mobile.html',import.meta.url))}}},
  // Vite's default recursive HTML discovery also finds archived release pages.
  optimizeDeps:{entries:['index.html']},
  resolve:{alias:{'@':fileURLToPath(new URL('./src',import.meta.url))}},
  css:{postcss:{plugins:[themePalettePlugin(),uiTypographyPlugin()]}},
  server:{port:5173,proxy:{'/api':'http://127.0.0.1:8080'},watch:{ignored:ignoredDevelopmentFile}},
})
