// Uses the dedicated acceptance project; never prints saved test credentials.
const { chromium } = require(require('path').resolve('.cache/browser/node_modules/playwright'));
const fs = require('fs');
const assert = require('assert/strict');
const env = Object.fromEntries(fs.readFileSync('.cache/2d-acceptance.env','utf8').split(/\r?\n/).filter(x=>x.includes('=')&&!x.startsWith('#')).map(x=>{const i=x.indexOf('=');return[x.slice(0,i),x.slice(i+1)];}));
const local=d=>new Date(d.getTime()-d.getTimezoneOffset()*60000).toISOString().slice(0,16);
const baseline=process.argv.includes('--baseline');
(async()=>{
 const browser=await chromium.launch({executablePath:'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe',headless:true});
 try{
  const context=await browser.newContext({viewport:{width:1440,height:1000}}),page=await context.newPage(),errors=[];
  page.on('pageerror',e=>errors.push(e.message));
  await page.goto(env.PUBLIC_URL+'/login');
  await page.locator('#login-username').fill('admin');await page.locator('#login-password').fill(env.BIUE_SMOKE_PASSWORD);
  await page.locator('#login-form button').click();await page.waitForURL('**/me');
  if(baseline){
   assert(await page.locator('nav a[href="/login"]').isVisible());
   await page.goto(env.PUBLIC_URL+'/problems');assert.equal(await page.getByRole('link',{name:'＋ 创建题目',exact:true}).count(),0);
   await page.goto(env.PUBLIC_URL+'/contests');await page.locator('#contest-create').waitFor({state:'visible'});
   const form=page.locator('#contest-create-form');await form.locator('[name=title]').fill('Baseline '+Date.now());
   await form.locator('[name=starts_at]').fill(local(new Date()));await form.locator('[name=ends_at]').fill(local(new Date(Date.now()+7200000)));
   const response=page.waitForResponse(r=>r.url().endsWith('/api/contests')&&r.request().method()==='POST');await form.locator('button').click();
   const invalid=await response;assert.equal(invalid.status(),400);assert.equal((await invalid.json()).message,'参数不合法');
   console.log('Baseline reproduced: static guest navigation, missing library create link, current-minute start returns generic 400.');return;
  }
  await page.locator('#nav-username').filter({hasText:'admin'}).waitFor();
  assert.equal(await page.locator('nav a[href="/login"]').isVisible(),false);
  await page.goto(env.PUBLIC_URL+'/problems');await page.getByRole('link',{name:'＋ 创建题目',exact:true}).click();
  await page.locator('#problem-admin').waitFor({state:'visible'});
  assert.equal(await page.locator('#problem-upload-form button').isDisabled(),true);
  assert.match(await page.locator('#problem-upload-state').innerText(),/先创建/);
  await page.locator('#edit-title').fill('Usability '+Date.now());await page.locator('#edit-description').fill('前五项验收题目');
  await page.locator('#problem-save-for-upload').click();await page.waitForFunction(()=>!document.querySelector('#problem-upload-fields').disabled);
  const title=await page.locator('#edit-title').inputValue();
  await page.locator('#edit-title').fill(title+' changed');assert.equal(await page.locator('#problem-upload-form button').isDisabled(),true);
  assert.match(await page.locator('#problem-upload-state').innerText(),/未保存/);
  await page.locator('#edit-title').fill(title);assert.equal(await page.locator('#problem-upload-form button').isDisabled(),false);
  await page.locator('#edit-title').fill(title+' saved');await page.locator('#problem-save-for-upload').click();await page.waitForFunction(()=>!document.querySelector('#problem-upload-fields').disabled);
  // A real ZIP of one matching pair, generated without external dependencies.
  const {execFileSync}=require('child_process');const zipPath=require('path').resolve('.cache/usability-data.zip');
  execFileSync('python',['-c',"import zipfile,sys;z=zipfile.ZipFile(sys.argv[1],'w');z.writestr('1.in','1 2\\n');z.writestr('1.out','3\\n');z.close()",zipPath]);
  await page.locator('#data-archive').setInputFiles(zipPath);await page.locator('#data-scores').fill('{"1":100}');
  await page.locator('#problem-upload-form button').click();await page.waitForFunction(()=>document.querySelector('#problem-current-data').textContent.includes('v1'));
  await page.goto(env.PUBLIC_URL+'/contests');await page.locator('#contest-create').waitFor({state:'visible'});
  const form=page.locator('#contest-create-form');
  await form.locator('[name=starts_at]').fill(local(new Date()));await form.locator('[name=title]').fill('Invalid preserved');
  await form.locator('[type=submit]').click();await page.waitForFunction(()=>!document.querySelector('#contest-form-error').hidden);
  assert.match(await page.locator('#contest-form-error').innerText(),/开始时间/);assert.equal(await form.locator('[name=title]').inputValue(),'Invalid preserved');
  // Real server/DB creation via the actual form, all scoring/approval/team modes.
  for(const mode of ['OI','ACM','IOI'])for(const team of [false,true])for(const approval of [false,true]){
   const title=`Usability ${mode} ${team} ${approval} ${Date.now()}`;
   await form.locator('[name=title]').fill(title);await form.locator('[name=scoring_mode]').selectOption(mode);
   await form.locator('[name=enrollment_policy]').selectOption(approval?'admin_add':'open');
   await form.locator('[name=team_mode]').setChecked(team);await form.locator('[name=approval_required]').setChecked(approval);
   await form.locator('[name=starts_at]').fill(local(new Date(Date.now()+3600000)));await form.locator('[name=duration_minutes]').fill('120');
   const response=page.waitForResponse(r=>r.url().endsWith('/api/contests')&&r.request().method()==='POST');await form.locator('[type=submit]').click();
   const result=await response;assert.equal(result.status(),200,await result.text());const payload=result.request().postDataJSON();
   assert.equal(payload.team_mode,team);assert.equal(payload.approval_required,approval);assert.equal(new Date(payload.ends_at)-new Date(payload.starts_at),7200000);
   assert(!('duration_minutes'in payload));await page.locator('#contest-title').filter({hasText:title}).waitFor();
  }
  let repeatedPosts=0;const countPost=request=>{if(request.url().endsWith('/api/contests')&&request.method()==='POST')repeatedPosts++;};page.on('request',countPost);
  await form.locator('[type=submit]').click();await page.waitForFunction(()=>document.querySelector('#contest-message').textContent==='操作完成');
  assert.equal(repeatedPosts,0,'successful creation must be reused on repeated submit');page.off('request',countPost);
  await page.locator('#contest-edit-current').check();assert.equal(await form.locator('[name=duration_minutes]').inputValue(),'120');
  await form.locator('[name=end_mode]').selectOption('manual');const start=await form.locator('[name=starts_at]').inputValue();
  await form.locator('[name=ends_at]').fill(local(new Date(new Date(start).getTime()+180*60000)));
  await form.locator('[type=submit]').click();await page.waitForFunction(()=>document.querySelector('#contest-message').textContent==='操作完成');
  await page.locator('#contest-edit-current').uncheck();await form.locator('[name=end_mode]').selectOption('duration');
  await form.locator('[name=starts_at]').fill('2030-12-31T23:30');await form.locator('[name=duration_minutes]').fill('120');
  assert.equal(await form.locator('[name=ends_at]').inputValue(),'2031-01-01T01:30');
  await form.locator('[name=starts_at]').fill('2028-02-28T23:30');assert.equal(await form.locator('[name=ends_at]').inputValue(),'2028-02-29T01:30');
  const dstContext=await browser.newContext({storageState:await context.storageState(),timezoneId:'America/New_York'}),dst=await dstContext.newPage();
  await dst.goto(env.PUBLIC_URL+'/contests');await dst.locator('#contest-create').waitFor({state:'visible'});
  const dstForm=dst.locator('#contest-create-form');await dstForm.locator('[name=title]').fill('DST '+Date.now());await dstForm.locator('[name=starts_at]').fill('2027-11-07T00:30');await dstForm.locator('[name=duration_minutes]').fill('120');
  const dstResponse=dst.waitForResponse(r=>r.url().endsWith('/api/contests')&&r.request().method()==='POST');await dstForm.locator('[type=submit]').click();
  const dstResult=await dstResponse;assert.equal(dstResult.status(),200);assert.equal(dstResult.request().postDataJSON().ends_at,'2027-11-07T06:30:00.000Z');await dstContext.close();
  async function post(target,path,data){return target.evaluate(async({path,data})=>{const t=await(await fetch('/api/auth/csrf')).json();const r=await fetch(path,{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':t.data.csrf_token},body:JSON.stringify(data)});return{status:r.status,body:await r.json()};},{path,data});}
  const invalidServer=await post(page,'/api/contests',{title:'Server validation',scoring_mode:'OI',enrollment_policy:'open',starts_at:'2020-01-01T00:00:00Z',ends_at:'2020-01-01T01:00:00Z'});
  assert.equal(invalidServer.status,400);assert(invalidServer.body.data.fields.starts_at);
  const password='Ui-check-'+Date.now(),ordinary='opt_user_'+Date.now(),temporary='opt_temp_'+Date.now();
  assert.equal((await post(page,'/api/auth/register',{username:ordinary,password,password_confirmation:password,student_no:''})).status,201);
  const contestID=await page.evaluate(()=>JSON.parse(sessionStorage.getItem('biue-contest-creation')).id);
  assert.equal((await post(page,`/api/contests/${contestID}/temporary-accounts`,{username:temporary,password,display_name:'临时导航验收'})).status,200);
  for(const username of [ordinary,temporary]){
   const roleContext=await browser.newContext(),rolePage=await roleContext.newPage();await rolePage.goto(env.PUBLIC_URL+'/login');
   await rolePage.locator('nav a[href="/login"]').waitFor({state:'visible'});await rolePage.locator('#login-username').fill(username);await rolePage.locator('#login-password').fill(password);await rolePage.locator('#login-form button').click();await rolePage.waitForURL('**/me');
   await rolePage.locator('#nav-username').filter({hasText:username}).waitFor();assert.equal(await rolePage.locator('nav a[href="/login"]').isVisible(),false);
   await rolePage.goto(env.PUBLIC_URL+'/problems');await rolePage.locator('#nav-username').filter({hasText:username}).waitFor();assert.equal(await rolePage.getByRole('link',{name:'＋ 创建题目',exact:true}).isVisible(),false);
   assert.equal((await post(rolePage,'/api/admin/problems',{title:'Forbidden'})).status,403);await roleContext.close();
  }
  // Simulate a lost response after the real server has committed a creation.
  await form.locator('[name=title]').fill('Unknown outcome '+Date.now());let uncertainPosts=0;
  await page.route('**/api/contests',async route=>{if(route.request().method()!=='POST')return route.continue();uncertainPosts++;const r=await route.fetch();assert.equal(r.status(),200);await route.abort();});
  await form.locator('[type=submit]').click();await page.waitForFunction(()=>document.querySelector('#contest-message').textContent.includes('核对比赛列表'));
  page.once('dialog',dialog=>dialog.dismiss());await form.locator('[type=submit]').click();await page.waitForFunction(()=>document.querySelector('#contest-message').textContent.includes('保留表单'));assert.equal(uncertainPosts,1);await page.unroute('**/api/contests');
  await page.screenshot({path:'.cache/opts-1-5-desktop.png',fullPage:true});await page.setViewportSize({width:390,height:844});
  assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));await page.screenshot({path:'.cache/opts-1-5-mobile.png',fullPage:true});
  // Change visible tabs after logout: the storage signal invalidates the other tab.
  const other=await context.newPage();await other.goto(env.PUBLIC_URL+'/problems');await other.locator('#nav-username').filter({hasText:'admin'}).waitFor();
  await page.locator('#nav-logout').click();await page.waitForURL('**/login?logged_out=1');await other.bringToFront();
  await other.locator('nav a[href="/login"]').waitFor({state:'visible'});assert.equal(await other.getByRole('link',{name:'＋ 创建题目',exact:true}).isVisible(),false);
  assert.deepEqual(errors,[]);
  console.log('Edge desktop/mobile: guest/user/temporary/admin navigation and permissions, save/upload/dirty restoration, real ZIP, 12 contest form combinations, server field errors, success/lost-response repeat protection, duration/manual edit, leap/year/DST boundaries and cross-tab logout passed.');
 }finally{await browser.close();}
})().catch(e=>{console.error(e.stack);process.exit(1);});
