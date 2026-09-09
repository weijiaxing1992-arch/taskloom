import assert from 'node:assert/strict'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import {spawnSync} from 'node:child_process'
import {fileURLToPath} from 'node:url'
const dir=fs.mkdtempSync(path.join(os.tmpdir(),'devflow-local-guard-'))
const db=path.join(dir,'fixture.db'),script=fileURLToPath(new URL('../deploy/start-local-service.sh',import.meta.url))
const run=(database,server='/usr/bin/true',preflight='/usr/bin/true')=>spawnSync('/bin/sh',[script,server,preflight],{env:{...process.env,DEVFLOW_DB:database},encoding:'utf8'})
try{
 assert.notEqual(run('relative.db').status,0)
 assert.notEqual(run(db).status,0)
 fs.writeFileSync(db,'')
 assert.notEqual(run(db).status,0)
 fs.writeFileSync(db,'isolated guard fixture')
 assert.notEqual(run(db,'/usr/bin/true','/usr/bin/false').status,0)
 assert.equal(run(db).status,0)
 assert.notEqual(run(db,'/usr/bin/false').status,0)
 console.log('Passed local service absolute-path, missing/empty database, preflight failure and exec guards.')
}finally{fs.rmSync(dir,{recursive:true,force:true})}
