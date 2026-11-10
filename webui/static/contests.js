"use strict";
(() => {
 const page=document.body.dataset.page;
 if(!["contests","identities"].includes(page))return;
 const $=id=>document.getElementById(id); let selected=0,view=null,after=0,importToken="";
 const message=text=>{$("contest-message").textContent=text;};
 let serverClockOffset=0;
 const createForm=page==="contests"?$("contest-create-form"):null;
 function localTime(date){return new Date(date.getTime()-date.getTimezoneOffset()*60000).toISOString().slice(0,16);}
 function clearCreateErrors(){
  if(!createForm)return;
  for(const field of createForm.elements){field.removeAttribute("aria-invalid");field.setCustomValidity?.("");}
  $("contest-form-error").hidden=true;
 }
 function createErrors(fields){
  const messages=Object.values(fields);
  for(const [name,error] of Object.entries(fields)){const field=createForm.elements.namedItem(name);if(field){field.setAttribute("aria-invalid","true");field.setCustomValidity(error);}}
  $("contest-form-error").textContent=messages.join("；");$("contest-form-error").hidden=false;
  const first=createForm.querySelector('[aria-invalid="true"]');first?.focus();
 }
 function calculatedEnd(){const f=createForm.elements;return f.end_mode.value==="duration"?new Date(new Date(f.starts_at.value).getTime()+Number(f.duration_minutes.value)*60000):new Date(f.ends_at.value);}
 function previewEnd(){
  const f=createForm.elements,auto=f.end_mode.value==="duration",start=new Date(f.starts_at.value),minutes=Number(f.duration_minutes.value);
  f.ends_at.readOnly=auto;f.duration_minutes.disabled=!auto;$("contest-duration-label").hidden=!auto;
  if(auto){const end=new Date(start.getTime()+minutes*60000);f.ends_at.value=Number.isFinite(start.getTime())&&Number.isInteger(minutes)&&minutes>0&&minutes<=43200&&end.getFullYear()<=9999?localTime(end):"";}
  const end=calculatedEnd();
  $("contest-end-preview").textContent=Number.isFinite(end.getTime())?`预计结束：${end.toLocaleString()}；实际时长 ${(end-start)/60000} 分钟。`:"填写开始时间和有效时长后，自动显示结束时间。";
 }
 function contestInput(d){
  clearCreateErrors();previewEnd();
  const f=createForm.elements,start=new Date(f.starts_at.value),end=calculatedEnd(),fields={};
  if(!d.title.trim()||Array.from(d.title.trim()).length>200)fields.title="比赛名称需为 1～200 个字符";
  if(!Number.isFinite(start.getTime()))fields.starts_at="请填写有效开始时间";
  else if(start.getTime()<=Date.now()+serverClockOffset)fields.starts_at="开始时间必须晚于服务器当前时间，请至少预留几分钟";
  if(f.end_mode.value==="duration"&&(!Number.isInteger(Number(f.duration_minutes.value))||Number(f.duration_minutes.value)<=0||Number(f.duration_minutes.value)>43200))fields.duration_minutes="持续时间需为 1～43200 的整数分钟";
  if(!Number.isFinite(end.getTime())||end<=start||end.getFullYear()>9999)fields.ends_at="结束时间必须晚于开始时间，且年份不能超过 9999";
  else if(end-start>30*86400000)fields.ends_at="比赛持续时间不能超过 30 天";
  if(Object.keys(fields).length){createErrors(fields);throw Error(Object.values(fields).join("；"));}
  // Only API fields are serialized. Duration/mode/edit controls stay local.
  return {title:d.title.trim(),scoring_mode:d.scoring_mode,enrollment_policy:d.enrollment_policy,starts_at:start.toISOString(),ends_at:end.toISOString(),approval_required:f.approval_required.checked,team_mode:f.team_mode.checked};
 }
 async function api(path,method="GET",data){
  const headers={Accept:"application/json"};
  if(method!=="GET"){const csrf=await api("/api/auth/csrf");headers["X-CSRF-Token"]=csrf.csrf_token;headers["Content-Type"]="application/json";}
  const controller=new AbortController(),timer=setTimeout(()=>controller.abort(),20000);
  try{const r=await fetch(path,{method,headers,credentials:"same-origin",cache:"no-store",signal:controller.signal,body:data===undefined?undefined:JSON.stringify(data)});const clock=Date.parse(r.headers.get("Date"));if(Number.isFinite(clock))serverClockOffset=clock-Date.now();const b=await r.json();if(!r.ok||b.code){const error=Error(b.message||"请求失败");error.status=r.status;error.fields=b.data?.fields;throw error;}return b.data;}finally{clearTimeout(timer);}
 }
 function run(button,fn){button.disabled=true;message("正在处理…");return Promise.resolve().then(fn).then(()=>message("操作完成")).catch(e=>message(e.name==="AbortError"?"请求超时，请刷新状态后重试":e.message)).finally(()=>{button.disabled=false;});}
 function form(id,fn){let pending=false;$(id).addEventListener("submit",e=>{e.preventDefault();if(pending)return;const data=Object.fromEntries(new FormData(e.target));pending=true;run(e.submitter||e.target.querySelector("button"),()=>fn(data)).finally(()=>{pending=false;});});}
 function text(tag,value){const el=document.createElement(tag);el.textContent=String(value??"");return el;}
 function renderRoster(){const target=$("contest-roster");target.replaceChildren();if(!view)return;const q=$("contest-search").value.toLowerCase();for(const t of view.teams){const row=view.standings.find(r=>r.team_id===t.id);const members=view.members.filter(m=>m.team_id===t.id).map(m=>{const i=m.identity;return `${i.Username} (#${m.user_id}) / ${i.Name} / ${i.Student||"—"} / ${i.StudentStatus} / ${i.Organization||"—"} / ${i.Kind}`;}).join("; ");if(q&&!`${t.name} ${members}`.toLowerCase().includes(q))continue;if($("contest-filter-type").value&&t.participant_type!==$("contest-filter-type").value)continue;if($("contest-filter-status").value&&t.status!==$("contest-filter-status").value)continue;if((row?.submissions||0)<Number($("contest-filter-submissions").value||0))continue;if($("contest-filter-account").value&&!view.members.some(m=>m.team_id===t.id&&m.identity.Kind===$("contest-filter-account").value))continue;const tr=document.createElement("tr");for(const value of [row?.rank||"—",`${t.name} (#${t.id})`,members,`${t.participant_type} / ${t.status}`,row?.submissions||0,row?.solved||0,row?.score||0,row?.penalty||0,Object.entries(row?.problems||{}).map(([id,p])=>`${id}: ${p.score}`).join(" / ")])tr.append(text("td",value));target.append(tr);}}
 async function loadDetail(id){selected=id;view=await api(`/api/contests/${id}`);const c=view.contest;$("contest-detail").hidden=false;$("contest-title").textContent=c.title;$("contest-info").textContent=`${c.scoring_mode} · ${new Date(c.starts_at).toLocaleString()} — ${new Date(c.ends_at).toLocaleString()} · ${c.settled_at?"已结算":"未结算"} ${view.frozen?(view.manager?"已封榜（管理员实时榜）":"已封榜"):""}`;$("contest-export").href=`/api/contests/${selected}/export.csv`;$("contest-manage").hidden=!view.manager;if(view.manager)$("contest-create").hidden=false;$("contest-enroll").hidden=c.enrollment_policy!=="open"||c.team_mode;$("contest-problems").replaceChildren();$("contest-problem-select").replaceChildren();for(const p of view.problems){const article=document.createElement("article");article.append(text("h3",`${p.problem_id}. ${p.statement.title||"题目"}`));for(const [key,label]of [["description","题面"],["input_format","输入"],["output_format","输出"],["sample_input","样例输入"],["sample_output","样例输出"]]){if(p.statement[key]){article.append(text("h4",label),text("pre",p.statement[key]));}}$("contest-problems").append(article);const option=text("option",`${p.problem_id}. ${p.statement.title||"题目"}`);option.value=p.problem_id;$("contest-problem-select").append(option);}contestLanguageControls();renderRoster();}
 function contestLanguageControls(){if(!view)return;const id=Number($("contest-problem-select").value);const p=view.problems.find(p=>p.problem_id===id);const output=p?.statement.problem_type==="output_only";const language=$("contest-submit-form").elements.language;for(const option of language.options)option.disabled=output?option.value!=="text":option.value==="text";if(language.selectedOptions[0]?.disabled)language.value=output?"text":"cpp17";$("contest-submit-form").elements.source_code.placeholder=output?JSON.stringify(Object.fromEntries((p.output_cases||[]).map(id=>[id,""])),null,2):"";}
 async function list(){const items=await api(`/api/contests?after=${after}`);for(const c of items){const button=text("button",`${c.title} · ${c.scoring_mode} · ${new Date(c.starts_at).toLocaleString()}`);button.type="button";button.className="button secondary";button.addEventListener("click",()=>run(button,()=>loadDetail(c.id)));$("contest-list").append(button);after=c.id;}$("contest-more").hidden=items.length<100;}
 if(page==="contests"){
  $("contest-problem-select").onchange=contestLanguageControls;
  $("contest-more").onclick=e=>run(e.target,list);$("contest-refresh").onclick=e=>run(e.target,()=>loadDetail(selected));$("contest-search").oninput=renderRoster;for(const id of ["contest-filter-type","contest-filter-status","contest-filter-account","contest-filter-submissions"])$(id).oninput=renderRoster;
  $("contest-enroll").onclick=e=>run(e.target,async()=>{await api(`/api/contests/${selected}/enroll`,"POST",{});await loadDetail(selected);});
  $("contest-settle").onclick=e=>run(e.target,async()=>{await api(`/api/contests/${selected}/settle`,"POST",{});await loadDetail(selected);});
  $("contest-time-help").textContent=`当前浏览器时区：${Intl.DateTimeFormat().resolvedOptions().timeZone}。开始时间须晚于服务器当前时间，结束时间须晚于开始，最长 30 天。`;
  createForm.elements.starts_at.value=localTime(new Date(Date.now()+15*60000));
  createForm.addEventListener("input",()=>{clearCreateErrors();previewEnd();});
  createForm.addEventListener("change",()=>{clearCreateErrors();previewEnd();});
  previewEnd();
  let lastCreation=null;
  try{lastCreation=JSON.parse(sessionStorage.getItem("biue-contest-creation")||"null");}catch{}
  function rememberCreation(value){lastCreation=value;try{if(value)sessionStorage.setItem("biue-contest-creation",JSON.stringify(value));else sessionStorage.removeItem("biue-contest-creation");}catch{}}
  form("contest-create-form",async d=>{
   const input=contestInput(d),editing=createForm.elements.edit_current.checked;
   if(editing){if(!view)throw Error("请先选择比赛");input.revision=view.contest.revision;}
   const key=JSON.stringify(input);
   if(!editing&&lastCreation?.key===key){
    if(lastCreation.id){await loadDetail(lastCreation.id);return;}
    if(!window.confirm("上次创建结果尚未确认。请先检查比赛列表，确认没有创建成功后再重试，以免重复创建。已核对并仍要重试？"))throw Error("已保留表单，请先核对比赛列表。");
   }
   if(!editing)rememberCreation({key});
   try{
    const c=await api(editing?`/api/contests/${view.contest.id}`:"/api/contests",editing?"PUT":"POST",input);
    if(!editing)rememberCreation({key,id:c.id});
    await loadDetail(c.id);
   }catch(error){
    if(error.fields)createErrors(error.fields);
    if(!editing&&error.status>=400&&error.status<500&&!lastCreation?.id)rememberCreation(null);
    if(!editing&&!error.status)error.message+="；请先核对比赛列表，勿直接重复创建。";
    throw error;
   }
  });
  $("contest-edit-current").onchange=e=>{
   if(e.target.checked&&!view){e.target.checked=false;message("请先选择比赛，再编辑。");return;}
   createForm.querySelector('button[type="submit"]').textContent=e.target.checked?"保存比赛修改":"创建比赛";
   createForm.elements.team_mode.disabled=e.target.checked;
   if(!e.target.checked)return;
   for(const [k,v]of Object.entries(view.contest)){const field=createForm.elements.namedItem(k);if(!field)continue;if(field.type==="checkbox")field.checked=!!v;else if(field.type==="datetime-local")field.value=localTime(new Date(v));else field.value=v;}
   const minutes=(new Date(view.contest.ends_at)-new Date(view.contest.starts_at))/60000;
   createForm.elements.duration_minutes.value=minutes;
   createForm.elements.end_mode.value=Number.isInteger(minutes)?"duration":"manual";
   clearCreateErrors();previewEnd();
  };
  form("contest-history-form",async d=>{const records=await api(`/api/contests/${selected}/submissions?user_id=${encodeURIComponent(d.user_id)}`);$("contest-history").replaceChildren();for(const s of records){const a=text("a",`#${s.id} · ${s.kind} · ${s.status} · ${s.score} 分`);a.href=`/submissions/${s.id}`;const p=document.createElement("p");p.append(a);$("contest-history").append(p);}});
  form("contest-manage-form",async d=>{for(const k of ["problem_id","user_id","team_id"])d[k]=Number(d[k]||0);d.team_ids=d.team_ids.trim()?d.team_ids.split(",").map(v=>Number(v.trim())):[];d.members=d.members.trim()?d.members.split(",").map(v=>Number(v.trim())):[];await api(`/api/contests/${selected}/manage`,"POST",d);await loadDetail(selected);});
  $("contest-csv-file").onchange=async e=>{const file=e.target.files[0];if(!file)return;if(file.size>256*1024){message("CSV 最多 256 KiB");return;}$("contest-csv-form").elements.csv.value=await file.text();};
  form("contest-csv-form",async d=>{importToken="";$("contest-csv-confirm").disabled=true;const preview=await api(`/api/contests/${selected}/csv/preview`,"POST",d);$("contest-csv-preview").replaceChildren();for(const row of preview.rows)$("contest-csv-preview").append(text("p",`第 ${row.line} 行 · ${row.username} · ${row.team} · ${row.participant_type} · ${row.error||"可导入"}`));importToken=preview.token;$("contest-csv-confirm").disabled=!preview.valid;});
  $("contest-csv-confirm").onclick=e=>run(e.target,async()=>{await api(`/api/contests/${selected}/csv/confirm`,"POST",{token:importToken});importToken="";await loadDetail(selected);}).finally(()=>{$("contest-csv-confirm").disabled=!importToken;});
  form("contest-temporary-form",async d=>{await api(`/api/contests/${selected}/temporary-accounts`,"POST",d);$("contest-temporary-form").reset();await loadDetail(selected);});
  form("contest-submit-form",async d=>{d.contest_id=selected;d.problem_id=Number(d.problem_id);const s=await api("/api/submissions","POST",d);const link=text("a",`查看提交 #${s.id}`);link.href=`/submissions/${s.id}`;$("contest-submission-result").replaceChildren(link);});
  Promise.all([api("/api/users/me"),list()]).then(([u])=>{$("contest-create").hidden=u.global_role!=="super_admin";message("请选择比赛");}).catch(e=>message(e.message));
 }else{
  async function people(){const rows=await api(`/api/admin/persons?after=${after}`);for(const p of rows){const button=text("button",`#${p.id} ${p.display_name} · ${p.student_no||"无学号"} · ${p.student_no_status} · 修订 ${p.revision} · ${(p.accounts||[]).map(a=>`${a.username} (#${a.id})`).join(", ")}`);button.type="button";button.onclick=()=>{for(const [k,v]of Object.entries(p)){const input=$("identity-form").elements.namedItem(k);if(input)input.value=v??"";}};$("identity-list").append(button);after=p.id;}$("identity-more").hidden=rows.length<100;}
  $("identity-more").onclick=e=>run(e.target,people);
  form("identity-form",async d=>{const id=Number(d.id);delete d.id;d.revision=Number(d.revision);d.student_no=d.student_no||null;await api(`/api/admin/persons/${id}`,"PUT",d);after=0;$("identity-list").replaceChildren();await people();});
  form("identity-link-form",async d=>{for(const k of ["user_id","from_person_id","to_person_id"])d[k]=Number(d[k]);await api("/api/admin/identity-links","POST",d);});
  people().then(()=>message("请选择需要审核的身份")).catch(e=>message(e.message));
 }
})();
