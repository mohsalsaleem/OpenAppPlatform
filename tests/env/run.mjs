import { spawn } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import { readFile, writeFile, readdir, copyFile, mkdir } from 'node:fs/promises';
import { createReadStream as streamFile } from 'node:fs';
import http from 'node:http';

const report={status:'running',startedAt:new Date().toISOString(),steps:[],expectedSkips:['TestLiveCoolifyLifecycle','TestLiveCoolifyRuntimeConfiguration','TestLiveCoolifyObserveOnlyAssembly','TestLiveCoolifyGitHubAppPinnedCommit','TestLiveSignedServerBuildCoolifyReplicas']};
const env={...process.env,OAP_API_TOKEN:randomBytes(32).toString('base64url'),OAP_ADDR:'127.0.0.1:8787',OAP_AUTH_MODE:'preview'};
delete env.COOLIFY_TOKEN;delete env.COOLIFY_URL;delete env.OAP_LIVE_COOLIFY;
let platform;let operatorMock;let operatorWrites=0;
async function persist(){await writeFile('/report/suite.json',JSON.stringify(report,null,2)+'\n');}
async function run(label,command,args,cwd='/workspace',timeout=180000){
 console.log(`\n[local-test] ${label}`);
 const started=Date.now();let text='';
 const child=spawn(command,args,{cwd,env,stdio:['ignore','pipe','pipe']});
 child.stdout.on('data',b=>{text+=b;process.stdout.write(b)});child.stderr.on('data',b=>{text+=b;process.stderr.write(b)});
 const timer=setTimeout(()=>child.kill('SIGKILL'),timeout);
 const code=await new Promise((resolve,reject)=>{child.on('error',reject);child.on('close',resolve)});clearTimeout(timer);
 await writeFile(`/report/${label}.log`,text);
 const step={name:label,exitCode:code,durationMs:Date.now()-started};report.steps.push(step);await persist();
 if(code!==0)throw Error(`${label} failed with exit code ${code}`);
 return text;
}
async function loadImages(){
 await new Promise((resolve,reject)=>{
  const req=http.request({socketPath:env.OAP_TEST_DOCKER_SOCKET,path:'/v1.45/images/load?quiet=1',method:'POST',headers:{'Content-Type':'application/x-tar'}},res=>{
   let body='';res.on('data',b=>body+=b);res.on('end',()=>{try{if(res.statusCode!==200)throw Error(`image load HTTP ${res.statusCode}`);for(const line of body.trim().split('\n'))if(line&&JSON.parse(line).error)throw Error('fixture image load failed');resolve()}catch(e){reject(e)}});
  });req.on('error',reject);streamFile('/report/fixtures.tar').pipe(req);
 });
}
async function waitReady(){for(let i=0;i<60;i++){try{const response=await fetch(env.OAP_BASE_URL+'/healthz',{signal:AbortSignal.timeout(1000)});if(response.ok)return}catch{};await new Promise(r=>setTimeout(r,500))}throw Error('platform did not become ready')}
try{
 await persist();await loadImages();
 const packages=(await readFile('/opt/oap/packages.txt','utf8')).trim().split('\n');
 for(let index=0;index<packages.length;index++){
  const relative=packages[index].replace('github.com/mohsalsaleem/OpenAppPlatform','').replace(/^\//,'');
  const text=await run(`core-${index}`,`/opt/oap/tests/${index}.test`,['-test.v','-test.timeout=180s'],`/workspace/${relative}`,240000);
  const skipped=[...text.matchAll(/--- SKIP: ([^ ]+)/g)].map(m=>m[1]);
  report.steps.at(-1).package=packages[index];report.steps.at(-1).skipped=skipped;await persist();
  if(skipped.some(name=>!report.expectedSkips.includes(name)))throw Error(`Unexpected test skip: ${skipped.join(', ')}`);
 }
 async function dockerRequest(path,body) {
  return await new Promise((resolve,reject)=>{ const req=http.request({socketPath:env.OAP_TEST_DOCKER_SOCKET,path:'/v1.45'+path,method:'POST',headers:{'Content-Type':'application/json'}},res=>{let raw='';res.on('data',b=>raw+=b);res.on('end',()=>{if(res.statusCode<200||res.statusCode>=300)return reject(Error('observation fixture HTTP '+res.statusCode));resolve(raw?JSON.parse(raw):{})})});req.on('error',reject);if(body)req.write(JSON.stringify(body));req.end(); });
 }
 const observed=await dockerRequest('/containers/create?name=existing-observation-fixture',{Image:env.OAP_TEST_DOCKER_IMAGE,ExposedPorts:{'8080/tcp':{}},Env:['OAP_TEST_MESSAGE=observe-me']});
 await dockerRequest('/containers/'+observed.Id+'/start');
 const targets=JSON.parse(await readFile('/workspace/tests/env/targets.json','utf8'));
 targets.find(t=>t.id==='test-docker').settings.observeContainers=[observed.Id];
 await mkdir('/workspace/.local',{recursive:true});
 await writeFile('/workspace/.local/observed-targets.json',JSON.stringify(targets));
 env.OAP_TEST_OBSERVED_RESOURCE=observed.Id;
 platform=spawn('/opt/oap/platform',['-targets','/workspace/.local/observed-targets.json','-web','/workspace/web/dist'],{env,stdio:['ignore','pipe','pipe']});
 let platformLog='';platform.stdout.on('data',b=>platformLog+=b);platform.stderr.on('data',b=>platformLog+=b);
 await waitReady();
 await run('browser','node',['node_modules/@playwright/test/cli.js','test','tests/workspace.spec.ts','tests/ui-audit.spec.ts'],'/workspace/web');
 for(const name of await readdir('/workspace/.local'))if(name.endsWith('.png'))await copyFile('/workspace/.local/'+name,'/report/'+name);
 const input=[{jsonrpc:'2.0',id:1,method:'initialize',params:{protocolVersion:'2024-11-05',capabilities:{},clientInfo:{name:'local-test',version:'1'}}},{jsonrpc:'2.0',id:2,method:'tools/list'},{jsonrpc:'2.0',id:3,method:'tools/call',params:{name:'list_applications',arguments:{}}}].map(v=>JSON.stringify(v)+'\n').join('');
 const mcp=spawn('/opt/oap/oap-mcp',['-url',env.OAP_BASE_URL],{env,stdio:['pipe','pipe','pipe']});let output='';mcp.stdout.on('data',b=>output+=b);mcp.stdin.end(input);const mcpCode=await new Promise((r,j)=>{mcp.on('error',j);mcp.on('close',r)});const messages=output.trim().split('\n').map(JSON.parse);
 if(mcpCode!==0||messages.length!==3||messages[0].result.protocolVersion!=='2024-11-05'||messages[1].result.tools.length!==6||messages[2].result.isError)throw Error('MCP handshake/read smoke failed');
 report.steps.push({name:'mcp-smoke',exitCode:0});
 platform.kill('SIGTERM');await new Promise(r=>platform.on('close',r));platform=null;
 await writeFile('/report/platform.log',platformLog);
 env.OAP_AUTH_MODE='owner';env.OAP_SETUP_TOKEN=randomBytes(32).toString('base64url');env.OAP_COOKIE_SECURE='false';delete env.OAP_API_TOKEN;
 env.COOLIFY_MOCK_TOKEN=randomBytes(32).toString('base64url');
 operatorMock=http.createServer((req,res)=>{if(req.method!=='GET'){operatorWrites++;res.writeHead(405).end();return}if(req.headers.authorization!=='Bearer '+env.COOLIFY_MOCK_TOKEN){res.writeHead(401).end();return}if(!['/api/v1/projects/mock-project/staging','/api/v1/projects/mock-project/preview'].includes(req.url)){res.writeHead(404).end();return}res.setHeader('Content-Type','application/json');res.end(JSON.stringify({applications:[]}))});await new Promise(r=>operatorMock.listen(8792,'127.0.0.1',r));
 const ownerTargets=JSON.parse(await readFile('/workspace/.local/observed-targets.json','utf8'));ownerTargets.push({...ownerTargets[0],id:'docker-production',name:'Local production fixture',environment:'production'});ownerTargets.push({id:'coolify-local-mock',name:'Local Coolify fixture',operator:'coolify',url:'http://127.0.0.1:8792',projectId:'mock-project',serverId:'mock-server',environment:'staging',tokenEnv:'COOLIFY_MOCK_TOKEN'});await writeFile('/workspace/.local/observed-targets.json',JSON.stringify(ownerTargets));
 platform=spawn('/opt/oap/platform',['-targets','/workspace/.local/observed-targets.json','-web','/workspace/web/dist'],{env,stdio:['ignore','pipe','pipe']});
 let ownerLog='';platform.stdout.on('data',b=>ownerLog+=b);platform.stderr.on('data',b=>ownerLog+=b);
 await waitReady();
 await run('owner-browser','node',['node_modules/@playwright/test/cli.js','test','tests/auth.spec.ts'],'/workspace/web');
 platform.kill('SIGTERM');await new Promise(r=>platform.on('close',r));platform=null;
 await writeFile('/report/owner-platform.log',ownerLog);
 for(const name of await readdir('/workspace/.local'))if(name.endsWith('.png'))await copyFile('/workspace/.local/'+name,'/report/'+name);
 if(operatorWrites!==0)throw Error('Connection setup mutated the operator');report.status='passed';
}catch(error){report.status='failed';report.error=error.message;console.error(error);process.exitCode=1}
finally{if(operatorMock)operatorMock.close();try{for(const name of await readdir('/workspace/.local'))if(name.endsWith('.png'))await copyFile('/workspace/.local/'+name,'/report/'+name)}catch{};if(platform)platform.kill('SIGTERM');report.finishedAt=new Date().toISOString();await persist();console.log(`\n[local-test] ${report.status}`)}
