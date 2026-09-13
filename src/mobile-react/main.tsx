import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import MobileApp from './MobileApp'
import './mobile.css'
import './mobile-reading.css'

const root = document.getElementById('mobile-root')
if (!root) throw new Error('移动端根节点不存在')
createRoot(root).render(<StrictMode><MobileApp /></StrictMode>)
