// Optional: npm install --no-save --package-lock=false playwright
// Run with Vite at :5173 and backend at :8080: node tests/device-panels.browser.mjs
import { chromium } from 'playwright';
import { readFileSync, mkdirSync } from 'node:fs';
import assert from 'node:assert/strict';
import { fileURLToPath } from 'node:url';
const env = Object.fromEntries(readFileSync(new URL('../../.env', import.meta.url), 'utf8').split(/\r?\n/).filter(l => l.includes('=') && !l.startsWith('#')).map(l => {const i=l.indexOf('=');return [l.slice(0,i).trim(),l.slice(i+1).trim().replace(/^"|"$/g,'')];}));
async function api(path,token,method='GET',data) {
  const r=await fetch('http://localhost:8080'+path,{method,headers:{'Content-Type':'application/json',...(token?{Authorization:`Bearer ${token}`}:{})},body:data?JSON.stringify(data):undefined});
  assert.ok(r.ok,`${method} ${path}: ${r.status}`);return r.json();
}
const login=async prefix=>(await api('/api/auth/login',null,'POST',{username:env[`${prefix}_USERNAME`]??prefix.toLowerCase(),password:env[`${prefix}_PASSWORD`]})).token;
const token=await login('OPERATOR'), original=await api('/api/config/control',token), initialSpeed=await api('/api/fan/speed',token);
const browser=await chromium.launch({headless:true,channel:'chrome'});
const page=await browser.newPage({viewport:{width:1440,height:1100}}), errors=[];
page.on('pageerror',e=>errors.push(e.message));
page.on('dialog',async d=>{errors.push('Unexpected browser dialog');await d.dismiss();});
try {
  await page.goto('http://127.0.0.1:5173');await page.evaluate(t=>localStorage.setItem('hwm_token',t),token);await page.reload();
  const pwm=page.locator('article').filter({has:page.getByRole('heading',{name:'PWM fan speed',exact:true})});
  await pwm.getByRole('button',{name:'MANUAL',exact:true}).click();
  const slider=pwm.getByRole('slider',{name:'Manual PWM speed'});
  await page.waitForFunction(()=>document.getElementById('pwm-speed') && !document.getElementById('pwm-speed').disabled);
  await slider.fill('37');await pwm.getByRole('button',{name:'Apply speed',exact:true}).click();
  await pwm.getByText('Speed set to 37%. Device read-back confirmed.').waitFor();
  assert.equal((await api('/api/fan/speed',token)).command_pct,37);
  await page.waitForTimeout(2500);assert.equal(await slider.inputValue(),'37');
  console.log('PASS: MANUAL switch, slider, real Modbus command read-back, polling stability');
  await page.route('**/api/fan/speed', async route => {
    if (route.request().method() === 'POST') await route.fulfill({status:502,contentType:'application/json',body:'{"error":"speed_write_failed"}'});
    else await route.continue();
  });
  await slider.fill('44');await pwm.getByRole('button',{name:'Apply speed',exact:true}).click();
  await pwm.getByText('The speed command could not be verified. Check the device connection and try again.').waitFor();
  assert.equal((await api('/api/fan/speed',token)).command_pct,37);
  await page.unroute('**/api/fan/speed');
  console.log('PASS: failed write gives inline error and never assumes the requested physical state');

  await pwm.getByRole('button',{name:'AUTO',exact:true}).click();
  await pwm.getByText('AUTO mode enabled. Temperature now controls fan speed.').waitFor();assert.ok(await slider.isDisabled());
  console.log('PASS: AUTO locks manual output with inline guidance');
  let resets=0;
  await page.route('**/api/reset',async route=>{resets++;assert.equal(route.request().postDataJSON().target,'thermocouple');await route.fulfill({status:202,contentType:'application/json',body:'{"target":"thermocouple","state":"recovering"}'});});
  const card=page.locator('.reset-device').filter({has:page.getByRole('heading',{name:'Thermocouple',exact:true})});
  await card.getByRole('button',{name:'Reset device'}).click();const confirm=page.getByRole('group',{name:'Confirm device reset'});await confirm.waitFor();assert.equal(resets,0);
  await confirm.getByRole('button',{name:'Cancel',exact:true}).click();assert.equal(resets,0);
  await card.getByRole('button',{name:'Reset device'}).click();
  const artifacts=new URL('../../artifacts/',import.meta.url);mkdirSync(artifacts,{recursive:true});
  await page.screenshot({path:fileURLToPath(new URL('device-panels-desktop.png',artifacts)),fullPage:true});
  await confirm.getByRole('button',{name:'Confirm reset',exact:true}).click();await page.getByText('Reset accepted for thermocouple. Live values will resume after verified recovery.').waitFor();assert.equal(resets,1);
  console.log('PASS: inline reset confirmation/cancel/single dispatch (mock reset endpoint); no browser dialogs');
  await page.setViewportSize({width:390,height:844});await page.screenshot({path:fileURLToPath(new URL('device-panels-mobile.png',artifacts)),fullPage:true});
  assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'Mobile overflow');assert.deepEqual(errors,[]);
  console.log('PASS: mobile layout, no browser runtime errors');
  const viewer=await login('VIEWER');await page.evaluate(t=>localStorage.setItem('hwm_token',t),viewer);await page.reload();await page.getByRole('heading',{name:'PWM fan speed',exact:true}).waitFor();
  assert.equal(await page.getByRole('button',{name:'Apply speed',exact:true}).count(),0);assert.equal(await page.getByRole('heading',{name:'Remote reset',exact:true}).count(),0);
  console.log('PASS: viewer controls hidden');
} finally {
  await api('/api/config/control',token,'PUT',{enabled:false});await api('/api/fan/speed',token,'POST',{percent:initialSpeed.command_pct});await api('/api/config/control',token,'PUT',original.config);await browser.close();
}
