import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

// Exercise the actual community launcher with in-memory filesystem/process adapters.
// The commercial LaunchAgent and its private existing-database paths are not shipped.
const source=readFileSync(new URL('../start.mjs',import.meta.url),'utf8')
const executable=source.replace(/^import .*\n/gm,'').replaceAll('import.meta.url',JSON.stringify('file:///taskloom-fixture/start.mjs'))
function launch(existing=false){
 const calls=[],signals=new Map(),handlers=new Map(),writes=[],logs=[]
 const secret='synthetic-persisted-session-secret'
 const fs={mkdirSync:(...args)=>calls.push(['mkdir',...args]),existsSync:()=>existing,writeFileSync:(...args)=>writes.push(args),readFileSync:()=>secret}
 const child={on:(name,fn)=>handlers.set(name,fn),kill:signal=>calls.push(['kill',signal])}
 const process={env:{DEVFLOW_DB:'/outside/private.db',DEVFLOW_ADDR:'0.0.0.0:8080',DEVFLOW_SESSION_SECRET:'outside-secret',DEVFLOW_WECOM_MODE:'live',DEVFLOW_DEVELOPER_MODE:'true',DEVFLOW_DEVELOPER_PASSWORD:'outside-password'},on:(name,fn)=>signals.set(name,fn)}
 const crypto={randomBytes:bytes=>{assert.equal(bytes,48);return{toString:encoding=>{assert.equal(encoding,'base64url');return'synthetic-new-session-secret'}}}}
 const spawn=(...args)=>{calls.push(['spawn',...args]);return child}
 new Function('fs','path','crypto','spawn','fileURLToPath','process','console',executable)(fs,path,crypto,spawn,fileURLToPath,process,{log:value=>logs.push(value),error:(...args)=>logs.push(args.join(' '))})
 return{calls,signals,handlers,writes,logs,process,secret}
}
for(const existing of [false,true]){
 const h=launch(existing),[,binary,args,options]=h.calls.find(call=>call[0]==='spawn')
 assert.equal(binary,'/taskloom-fixture/server');assert.deepEqual(args,[]);assert.equal(options.cwd,'/taskloom-fixture')
 assert.equal(options.env.DEVFLOW_ADDR,'127.0.0.1:8080');assert.equal(options.env.DEVFLOW_DB,'/taskloom-fixture/data/community.db')
 assert.equal(options.env.DEVFLOW_WEB_DIR,'/taskloom-fixture/web');assert.equal(options.env.DEVFLOW_SESSION_SECRET,h.secret)
 assert.equal(options.env.DEVFLOW_WECOM_MODE,'mock');assert.equal(options.env.DEVFLOW_SQLITE_SYNCHRONOUS,'FULL')
 assert.equal(options.env.DEVFLOW_WECOM_KEY_FILE,'/taskloom-fixture/data/community.db.wecom-key')
 assert.equal(options.env.DEVFLOW_DEVELOPER_MODE,undefined);assert.equal(options.env.DEVFLOW_DEVELOPER_PASSWORD,undefined)
 assert.equal(h.writes.length,existing?0:1)
 if(!existing){assert.equal(h.writes[0][0],'/taskloom-fixture/data/session-secret');assert.deepEqual(h.writes[0][2],{mode:0o600,flag:'wx'})}
 assert(h.logs.some(value=>value.includes('Admin / 123456')&&value.includes('改密')))
 h.handlers.get('error')(Error('missing binary'));assert.equal(h.process.exitCode,1)
 h.handlers.get('exit')(7);assert.equal(h.process.exitCode,7)
 h.signals.get('SIGTERM')();assert.deepEqual(h.calls.at(-1),['kill','SIGTERM'])
}
console.log('Passed community launcher isolation, loopback, persistent secret, mock notifications and process lifecycle checks.')
