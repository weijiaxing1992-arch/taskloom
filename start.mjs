// 本机演示启动器：持久保存随机会话密钥，不复用任何商业版数据库或环境配置。
import fs from 'node:fs'
import path from 'node:path'
import crypto from 'node:crypto'
import {spawn} from 'node:child_process'
import {fileURLToPath} from 'node:url'
const root=path.dirname(fileURLToPath(import.meta.url)),data=path.join(root,'data')
fs.mkdirSync(data,{recursive:true,mode:0o700})
const secret=path.join(data,'session-secret')
if(!fs.existsSync(secret))fs.writeFileSync(secret,crypto.randomBytes(48).toString('base64url'),{mode:0o600,flag:'wx'})
const env={...process.env,DEVFLOW_ADDR:'127.0.0.1:8080',DEVFLOW_DB:path.join(data,'community.db'),DEVFLOW_WEB_DIR:path.join(root,'web'),DEVFLOW_SESSION_SECRET:fs.readFileSync(secret,'utf8'),DEVFLOW_COOKIE_SECURE:'false',DEVFLOW_SQLITE_SYNCHRONOUS:'FULL',DEVFLOW_WECOM_MODE:'mock',DEVFLOW_WECOM_KEY_FILE:path.join(data,'community.db.wecom-key')}
delete env.DEVFLOW_DEVELOPER_MODE;delete env.DEVFLOW_DEVELOPER_PASSWORD
const child=spawn(path.join(root,'server'),[],{cwd:root,env,stdio:'inherit'})
child.on('error',e=>{console.error('启动失败：',e.message);process.exitCode=1})
child.on('exit',code=>{process.exitCode=code||0})
for(const signal of ['SIGINT','SIGTERM'])process.on(signal,()=>child.kill(signal))
console.log('TaskLoom: http://127.0.0.1:8080；首次登录 Admin / 123456，请立即改密。')
