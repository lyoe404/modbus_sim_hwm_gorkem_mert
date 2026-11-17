import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import ts from 'typescript';
const { outputText } = ts.transpileModule(readFileSync(new URL('../src/api.ts', import.meta.url), 'utf8'), {compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ES2022}});
const {api,ApiError}=await import(`data:text/javascript;base64,${Buffer.from(outputText).toString('base64')}`);
test('Extended polling passes cancellation; writes preserve target and percent contracts',async()=>{
 const original=globalThis.fetch; const calls=[];
 globalThis.fetch=async(path,init)=>{calls.push({path,init});return new Response('{}',{status:200});};
 try{
  const abort=new AbortController();await api.thermocouple('token',abort.signal);await api.speed('token',abort.signal);
  abort.abort();assert.ok(calls[0].init.signal.aborted);assert.ok(calls[1].init.signal.aborted);
  await api.reset('token','thermocouple');await api.setSpeed('token',42);
  assert.equal(calls[2].path,'/api/reset');assert.deepEqual(JSON.parse(calls[2].init.body),{target:'thermocouple'});
  assert.equal(calls[3].init.headers.Authorization,'Bearer token');assert.deepEqual(JSON.parse(calls[3].init.body),{percent:42});
  globalThis.fetch=async()=>new Response('{"error":"fan_in_auto_mode_switch_to_manual_first"}',{status:409});
  await assert.rejects(api.setSpeed('token',42),err=>err instanceof ApiError && err.status===409);
 }finally{globalThis.fetch=original;}
});
