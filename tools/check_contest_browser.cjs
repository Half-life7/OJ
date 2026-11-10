const { chromium } = require(require('path').resolve('.cache/browser/node_modules/playwright'));
const fs = require('fs');
const assert = require('assert/strict');
(async()=>{
 const env=Object.fromEntries(fs.readFileSync('.cache/2d-acceptance.env','utf8').split(/\r?\n/).filter(x=>x.includes('=')&&!x.startsWith('#')).map(x=>{const i=x.indexOf('=');return [x.slice(0,i),x.slice(i+1)];}));
 const base=env.PUBLIC_URL;
 const browser=await chromium.launch({executablePath:'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe',headless:true});
 try{
  const context=await browser.newContext({viewport:{width:1440,height:1000}}),page=await context.newPage();const errors=[];page.on('pageerror',e=>errors.push(e.message));
  await page.goto(base+'/login');await page.locator('#login-username').fill('admin');await page.locator('#login-password').fill(env.BIUE_SMOKE_PASSWORD);await page.locator('#login-form button').click();await page.waitForURL('**/me');
  await page.goto(base+'/contests');await page.locator('#contest-create').waitFor({state:'visible'});
  const form=page.locator('#contest-create-form'),suffix=Date.now().toString();
  await form.locator('[name=title]').fill('Browser '+suffix);
  const local=d=>new Date(d.getTime()-d.getTimezoneOffset()*60000).toISOString().slice(0,16);
  await form.locator('[name=starts_at]').fill(local(new Date(Date.now()+3600000)));await form.locator('[name=ends_at]').fill(local(new Date(Date.now()+7200000)));
  await form.locator('button').click();await page.locator('#contest-title').filter({hasText:'Browser '+suffix}).waitFor();
  await page.locator('#contest-edit-current').check();await form.locator('[name=scoring_mode]').selectOption('IOI');await form.locator('button').click();await page.waitForFunction(()=>document.querySelector('#contest-info').textContent.startsWith('IOI'));
  const result=await page.evaluate(async()=>{const r=await fetch('/api/admin/problems');return (await r.json()).data;});assert(result.items.length>0);
  const manage=page.locator('#contest-manage-form');await manage.locator('[name=problem_id]').fill(String(result.items[0].id));await manage.locator('button').click();await page.locator('#contest-problem-select option').first().waitFor({state:'attached'});
  const temp=page.locator('#contest-temporary-form');await temp.locator('[name=username]').fill('browser_'+suffix);await temp.locator('[name=display_name]').fill('浏览器验收选手');await temp.locator('[name=password]').fill('Browser-'+suffix+'-password');await temp.locator('button').click();await page.locator('#contest-roster tr').first().waitFor();
  await page.locator('#contest-search').fill('不存在的人员');assert.equal(await page.locator('#contest-roster tr').count(),0);await page.locator('#contest-search').fill('浏览器验收');assert.equal(await page.locator('#contest-roster tr').count(),1);
  await page.locator('#contest-filter-account').selectOption('permanent');assert.equal(await page.locator('#contest-roster tr').count(),0);await page.locator('#contest-filter-account').selectOption('contest');assert.equal(await page.locator('#contest-roster tr').count(),1);await page.screenshot({path:'.cache/3-contests-desktop.png',fullPage:true});await page.setViewportSize({width:390,height:844});await page.screenshot({path:'.cache/3-contests-mobile.png',fullPage:true});
  await page.goto(base+'/admin/identities');await page.locator('#identity-list button').first().waitFor();await page.locator('#identity-list button').filter({hasText:'browser_'+suffix}).click();assert.equal(await page.locator('#identity-form [name=display_name]').inputValue(),'浏览器验收选手');assert((await page.locator('#identity-list').innerText()).includes('browser_'+suffix));
  assert.deepEqual(errors,[]);console.log('Edge desktop/mobile: login, contest create/edit, pinned problem, temporary entrant, roster search, identity selection; no JavaScript exceptions');
 }finally{await browser.close();}
})().catch(e=>{console.error(e.message);process.exit(1);});
