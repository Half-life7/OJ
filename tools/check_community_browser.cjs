const {chromium}=require(require('path').resolve('.cache/browser/node_modules/playwright'));
const fs=require('fs'),assert=require('assert/strict');
(async()=>{
 const env=Object.fromEntries(fs.readFileSync('.cache/2d-acceptance.env','utf8').split(/\r?\n/).filter(s=>s.includes('=')&&!s.startsWith('#')).map(s=>{const i=s.indexOf('=');return [s.slice(0,i),s.slice(i+1)];}));
 const browser=await chromium.launch({executablePath:'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe',headless:true});
 try{
  const context=await browser.newContext({viewport:{width:1440,height:1000}}),page=await context.newPage();const errors=[];page.on('pageerror',e=>errors.push(e.message));
  await page.goto(env.PUBLIC_URL+'/login');await page.locator('#login-username').fill('admin');await page.locator('#login-password').fill(env.BIUE_SMOKE_PASSWORD);await page.locator('#login-form button').click();await page.waitForURL('**/me');
  const data=await page.evaluate(async()=>{const r=await fetch('/api/problems');return (await r.json()).data;});assert(data.items.length>=2);const ids=data.items.slice(0,2).map(p=>p.id);
  await page.goto(env.PUBLIC_URL+'/learning');await page.waitForFunction(()=>document.querySelector('#learning-message').textContent.includes('请选择'));
  const form=page.locator('#learning-form'),title='Browser learning '+Date.now();await form.locator('[name=title]').fill(title);await form.locator('[name=body]').fill('# Learning\n\n**Safe Markdown**\n\n```python\nprint(1)\n```\n\n<script>window.communityXSS=1</script>');await form.locator('[name=problems]').fill(ids.join(','));await form.locator('button').click();await page.locator('#learning-title').filter({hasText:title}).waitFor();
  assert.equal(await page.locator('#learning-html h1').innerText(),'Learning');assert.equal(await page.locator('#learning-html script').count(),0);assert.equal(await page.evaluate(()=>window.communityXSS),undefined);assert.equal(await page.locator('#learning-problems li').count(),2);
  await page.locator('#learning-favorite').click();await page.waitForFunction(()=>document.querySelector('#learning-message').textContent==='操作完成');
  await page.locator('#learning-edit').click();await form.locator('[name=problems]').fill(ids.slice().reverse().join(','));await form.locator('button').click();await page.waitForFunction(()=>document.querySelector('#learning-meta').textContent.includes('修订 2'));
  assert((await page.locator('#learning-problems a').first().getAttribute('href')).endsWith('/'+ids[1]));
  await page.screenshot({path:'.cache/4-learning-desktop.png',fullPage:true});await page.setViewportSize({width:390,height:844});await page.screenshot({path:'.cache/4-learning-mobile.png',fullPage:true});
  await page.goto(env.PUBLIC_URL+'/rankings');await page.locator('#board-meta').filter({hasText:'参与者'}).waitFor();for(const kind of ['practice','contest','solutions']){await page.locator('#board-form [name=kind]').selectOption(kind);await page.locator('#board-form button').click();await page.waitForFunction(()=>document.querySelector('#learning-message').textContent==='操作完成');}
  await page.goto(env.PUBLIC_URL+'/problems/'+ids[0]);await page.locator('#practice-statistics').waitFor();assert((await page.locator('a[href*="kind=discussion"]').count())>0);
  await page.goto(env.PUBLIC_URL+'/problems?category=graph');assert.equal(await page.locator('[name=category]').inputValue(),'graph');
  assert.deepEqual(errors,[]);console.log('Edge desktop/mobile: Markdown safety, ordered list create/edit/favorite, three boards, practice stats and category passed');
 }finally{await browser.close();}
})().catch(e=>{console.error(e.message);process.exit(1);});
