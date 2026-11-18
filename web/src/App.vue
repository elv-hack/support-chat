<script setup lang="ts">
import { ref, computed, nextTick, onMounted, onBeforeUnmount } from 'vue';
import AttachmentView from './AttachmentView.vue';
import LibraryPanel from './LibraryPanel.vue';
import SettingsPanel from './SettingsPanel.vue';
import './library.css';
import type { User, Message, Attachment, History, ClientConfig, AppSettings, Permissions } from './types';
const display=ref<AppSettings>({brandName:'一对一',agentName:'专属客服',welcomeTitle:'你好，很高兴为你服务',welcomeText:'文字、图片或语音，选择你喜欢的方式。',announcement:'',primaryColor:'#275b4c',revision:0});
declare const __WEB_VERSION__:string;
const initialWebVersion=ref(__WEB_VERSION__),latestWebVersion=ref(''),showSettings=ref(false),showUpdateConfirm=ref(false);
const updateAvailable=computed(()=>!!initialWebVersion.value&&latestWebVersion.value!==initialWebVersion.value);
const permissionsDraft=ref<Permissions>({text:true,image:true,voice:true,file:true}),customerNameDraft=ref('');
let configTimer:ReturnType<typeof setInterval>|undefined,lastConfigCheck=0,configLoading=false;
async function loadConfig(){if(configLoading)return;configLoading=true;try{const response=await fetch('/api/client/config',{cache:'no-store',credentials:'omit'});if(!response.ok)return;const result:ClientConfig=await response.json();display.value=result.settings;document.title=result.settings.brandName;document.documentElement.style.setProperty('--green',result.settings.primaryColor);latestWebVersion.value=result.webVersion;lastConfigCheck=Date.now()}catch{}finally{configLoading=false}}
function applyUpdate(){void logout();if(navigator.userAgent.includes('SupportChatAndroid/'))location.href='supportchat://refresh';else location.reload()}
function openManage(){if(!active.value)return;permissionsDraft.value={...active.value.permissions};customerNameDraft.value=active.value.name;showManage.value=true;formError.value=''}
async function savePermissions(){if(!active.value)return;formBusy.value=true;formError.value='';try{await api(`/customers/${active.value.id}/permissions`,{method:'POST',body:body({name:customerNameDraft.value,permissions:permissionsDraft.value})});await refresh();showManage.value=false;notice.value='客户权限已更新'}catch(e){formError.value=(e as Error).message}finally{formBusy.value=false}}
const mode = ref<'agent' | 'customer'>(location.pathname === '/agent' ? 'agent' : 'customer');
const user = ref<User | null>(null), customers = ref<User[]>([]), active = ref<User | null>(null);
const username = ref(''), password = ref(''), loginBusy = ref(false), loginError = ref('');
const draft = ref(''), search = ref(''), messages = ref<Message[]>([]), error = ref(''), notice = ref('');
const busy = ref(false), loading = ref(false), online = ref(false), mobileChat = ref(false), nextBefore = ref(0), pages = ref(1);
const scrollbox = ref<HTMLElement>(), fileInput = ref<HTMLInputElement>();
const showLibrary=ref(false),libraryKind=ref<'reply'|'intro'>('reply');
function openLibrary(kind:'reply'|'intro'='reply'){if(busy.value||recording.value)return;libraryKind.value=kind;showLibrary.value=true}
const showCreate = ref(false), showManage = ref(false), confirmDelete = ref<number | 'all' | null>(null);
const newUsername = ref(''), newName = ref(''), newPassword = ref(''), resetPassword = ref('');
const formError = ref(''), formBusy = ref(false);
const preview = ref<{id:string;url:string;name:string;text?:string} | null>(null);
const recording = ref(false), recordSeconds = ref(0);
let bearer = '', epoch = 0, selection = 0, refreshing = false;
let eventsAbort: AbortController | null = null, timer: ReturnType<typeof setInterval> | undefined, recordTimer: ReturnType<typeof setInterval> | undefined;
let recorder: MediaRecorder | null = null, mediaStream: MediaStream | null = null, chunks: Blob[] = [], cancelRecord = false;
let sendAttempt: { id: string; text: string; attachmentId: string } | null = null;
const isAgent = computed(() => user.value?.role === 'agent');
const canSend=(kind:keyof Permissions)=>isAgent.value||!!user.value?.permissions?.[kind];
const filtered = computed(() => customers.value.filter(c => `${c.name} ${c.username}`.toLowerCase().includes(search.value.toLowerCase())).sort((a,b) => (b.unread>0?1:0)-(a.unread>0?1:0) || b.lastActive-a.lastActive));
const totalUnread = computed(() => customers.value.reduce((n,c)=>n+c.unread,0));
const title = computed(() => active.value?.name || '专属客服');
function wipe(reason = '') {
  epoch++; selection++; bearer = ''; eventsAbort?.abort(); eventsAbort = null; stopRecording(true);
  user.value = null; active.value = null; customers.value = []; messages.value = []; preview.value = null;
  username.value = ''; password.value = ''; draft.value = ''; resetPassword.value = ''; newPassword.value = '';newUsername.value='';newName.value='';
  showSettings.value=false;showLibrary.value=false;showCreate.value = false;showManage.value = false;confirmDelete.value = null; sendAttempt = null; search.value='';
  online.value=false;busy.value=false;loading.value=false;refreshing=false;error.value='';notice.value='';formError.value='';loginError.value=reason;
  if (timer) clearInterval(timer); timer=undefined;
}
async function request(path: string, init: RequestInit = {}): Promise<Response> {
  const e=epoch, t=bearer;
  const response = await fetch(`/api${path}`, { ...init, cache:'no-store', credentials:'omit', headers:{ ...(t?{Authorization:`Bearer ${t}`} : {}), ...(init.body && !(init.body instanceof FormData)?{'Content-Type':'application/json'}:{}), ...init.headers } });
  if(e!==epoch) throw new Error('会话已结束');
  if(!response.ok){let msg='请求失败，请稍后重试';try{msg=(await response.json()).error||msg}catch{};if(response.status===401&&t)wipe('登录已失效，请重新输入账号密码');throw new Error(msg)}
  return response;
}
async function api<T>(path:string, init:RequestInit={}):Promise<T>{return (await request(path,init)).json()}
const body=(data:unknown)=>JSON.stringify(data);
const query=()=>`customer=${encodeURIComponent(active.value?.id || user.value?.id || '')}`;
async function login(){loginBusy.value=true;loginError.value='';try{
 const result=await api<{token:string;user:User}>('/login',{method:'POST',body:body({username:username.value,password:password.value,role:mode.value})});
 password.value='';username.value='';epoch++;bearer=result.token;user.value=result.user;
 if(result.user.role==='customer'){active.value=result.user;mobileChat.value=true}else{active.value=null;mobileChat.value=false}
 await refresh();startEvents();timer=setInterval(()=>{if(document.visibilityState==='visible')void refresh()},4000);
 }catch(e){loginError.value=(e as Error).message;password.value=''}finally{loginBusy.value=false}}
async function logout(){const t=bearer;wipe();if(t)void fetch('/api/logout',{method:'POST',headers:{Authorization:`Bearer ${t}`},credentials:'omit',cache:'no-store'}).catch(()=>{})}
async function refresh(){
 if(!bearer||refreshing)return;refreshing=true;const e=epoch,s=selection;
 try{
  if(isAgent.value){const list=await api<User[]>('/customers');if(e!==epoch)return;customers.value=list;if(active.value){const c=list.find(x=>x.id===active.value!.id);if(c)active.value=c}}
  else{const me=await api<User>('/me');if(e!==epoch)return;user.value=me;active.value=me;if(!me.permissions.voice)stopRecording(true)}
  if(Date.now()-lastConfigCheck>10000)void loadConfig();
  if(!active.value){online.value=true;return}
  const q=query();const result=await api<History>(`/messages?${q}`);let list=result.messages, before=result.before;
  for(let p=1;p<pages.value&&before;p++){const older=await api<History>(`/messages?${q}&before=${before}`);list=[...older.messages,...list];before=older.before}
  if(e!==epoch||s!==selection)return;
  const nearBottom=!scrollbox.value||scrollbox.value.scrollHeight-scrollbox.value.scrollTop-scrollbox.value.clientHeight<130;
  const changed=messages.value.at(-1)?.seq!==list.at(-1)?.seq;
  messages.value=list;nextBefore.value=before;online.value=true;
  if(preview.value&&!list.some(m=>m.attachment?.id===preview.value!.id))preview.value=null;
  if(document.visibilityState==='visible'&&(mobileChat.value||window.innerWidth>=800)){
   await api(`/read?${q}`,{method:'POST',body:body({seq:result.maxSeq})});if(active.value)active.value.unread=0;
  }
  if(changed&&nearBottom){await nextTick();scrollbox.value?.scrollTo({top:scrollbox.value.scrollHeight,behavior:'smooth'})}
 }catch(err){if(e===epoch){online.value=false;error.value=(err as Error).message}}
 finally{if(e===epoch){refreshing=false;loading.value=false}}
}
async function startEvents(){
 eventsAbort?.abort();eventsAbort=new AbortController();const signal=eventsAbort.signal,e=epoch;
 while(bearer&&e===epoch&&!signal.aborted){try{
  const response=await request('/events',{signal});const reader=response.body!.getReader(),decoder=new TextDecoder();let buffer='';
  while(true){const part=await reader.read();if(part.done)break;buffer+=decoder.decode(part.value,{stream:true});let at:number;while((at=buffer.indexOf('\n\n'))>=0){const event=buffer.slice(0,at);buffer=buffer.slice(at+2);if(event.includes('event: logout')){wipe('账号状态发生变化，请重新登录');return}if(event.includes('event: change'))void refresh()}}
 }catch{if(signal.aborted||e!==epoch)return}
 if(e===epoch)await new Promise(resolve=>setTimeout(resolve,3000));
 }
}
async function selectCustomer(c:User){selection++;active.value=c;messages.value=[];pages.value=1;nextBefore.value=0;mobileChat.value=true;draft.value='';sendAttempt=null;preview.value=null;error.value='';loading.value=true;stopRecording(true);while(refreshing&&bearer)await new Promise(resolve=>setTimeout(resolve,50));await refresh()}
async function older(){if(loading.value||!nextBefore.value)return;loading.value=true;const h=scrollbox.value?.scrollHeight||0;pages.value++;await refresh();await nextTick();if(scrollbox.value)scrollbox.value.scrollTop+=scrollbox.value.scrollHeight-h;loading.value=false}
async function submit(attachment?:Attachment){
 if(busy.value||!active.value||!canSend(attachment?.kind||'text'))return;const text=attachment?'':draft.value.trim();if(!text&&!attachment)return;
 busy.value=true;error.value='';notice.value='';const e=epoch,s=selection;
 if(!sendAttempt||sendAttempt.text!==text||sendAttempt.attachmentId!==(attachment?.id||''))sendAttempt={id:crypto.randomUUID(),text,attachmentId:attachment?.id||''};
 try{await api(`/messages?${query()}`,{method:'POST',body:body(sendAttempt)});if(e!==epoch||s!==selection)return;draft.value='';sendAttempt=null;notice.value='已提交';await refresh();setTimeout(()=>{if(e===epoch)notice.value=''},2500)}catch(err){if(e===epoch)error.value=(err as Error).message}finally{if(e===epoch)busy.value=false}
}
async function upload(file:File|Blob,kind:string,name:string){
 if(busy.value||!active.value)return;if(!canSend(kind as keyof Permissions)){error.value='客服已关闭此类附件的发送权限';return};busy.value=true;error.value='';const e=epoch,s=selection;
 try{const data=new FormData();data.append('file',file,name);data.append('kind',kind);const attachment=await api<Attachment>(`/attachments?${query()}`,{method:'POST',body:data});if(e!==epoch||s!==selection)return;busy.value=false;await submit(attachment)}catch(err){if(e===epoch)error.value=(err as Error).message}finally{if(e===epoch)busy.value=false}
}
function pickFile(event:Event){const input=event.target as HTMLInputElement;const f=input.files?.[0];if(f)void upload(f,/^image\/(png|jpeg|gif)$/.test(f.type)?'image':'file',f.name);input.value=''}
async function record(){
 if(!canSend('voice'))return;if(recording.value){stopRecording(false);return};if(!navigator.mediaDevices?.getUserMedia){error.value='录音需要 HTTPS 或本机开发地址';return}
 const e=epoch,s=selection;try{const stream=await navigator.mediaDevices.getUserMedia({audio:true});if(e!==epoch||s!==selection){stream.getTracks().forEach(t=>t.stop());return};mediaStream=stream;
 const mime=['audio/webm;codecs=opus','audio/webm','audio/mp4'].find(t=>MediaRecorder.isTypeSupported(t));recorder=new MediaRecorder(stream,mime?{mimeType:mime}:{});chunks=[];cancelRecord=false;recordSeconds.value=0;
 recorder.ondataavailable=event=>{if(event.data.size)chunks.push(event.data)};
 recorder.onstop=()=>{const data=new Blob(chunks,{type:recorder?.mimeType||'audio/webm'});chunks=[];mediaStream?.getTracks().forEach(t=>t.stop());mediaStream=null;recording.value=false;if(recordTimer)clearInterval(recordTimer);if(!cancelRecord&&e===epoch&&s===selection&&data.size)void upload(data,'voice','voice.webm');recorder=null};
 recorder.start(250);recording.value=true;const start=performance.now();recordTimer=setInterval(()=>{recordSeconds.value=Math.floor((performance.now()-start)/1000);if(performance.now()-start>=59900)stopRecording(false)},100);
 }catch{error.value='无法使用麦克风，请在系统设置中允许录音权限';mediaStream?.getTracks().forEach(t=>t.stop())}
}
function stopRecording(cancel:boolean){cancelRecord=cancel;if(recordTimer)clearInterval(recordTimer);if(recorder&&recorder.state!=='inactive')recorder.stop();else mediaStream?.getTracks().forEach(t=>t.stop());recording.value=false}
async function createCustomer(){formBusy.value=true;formError.value='';try{const c=await api<User>('/customers',{method:'POST',body:body({username:newUsername.value,name:newName.value,password:newPassword.value})});showCreate.value=false;newUsername.value='';newName.value='';newPassword.value='';await selectCustomer(c)}catch(e){formError.value=(e as Error).message}finally{formBusy.value=false}}
async function manage(action:string){if(!active.value)return;formBusy.value=true;formError.value='';try{await api(`/customers/${active.value.id}/${action}`,{method:'POST',body:action==='password'?body({password:resetPassword.value}):undefined});resetPassword.value='';notice.value='账号设置已更新';await refresh();showManage.value=false}catch(e){formError.value=(e as Error).message}finally{formBusy.value=false}}
async function deleteMessages(){if(confirmDelete.value===null)return;formBusy.value=true;formError.value='';try{await api(`/messages?${query()}`,{method:'DELETE',body:body(confirmDelete.value==='all'?{all:true}:{seq:confirmDelete.value})});confirmDelete.value=null;preview.value=null;await refresh()}catch(e){formError.value=(e as Error).message}finally{formBusy.value=false}}
function showPreview(id:string,url:string,name:string,text?:string){preview.value={id,url,name,text}}
function timeLabel(n:number){return new Intl.DateTimeFormat('zh-CN',{month:'2-digit',day:'2-digit',hour:'2-digit',minute:'2-digit',hour12:false}).format(n)}
function visibility(){if(document.visibilityState==='hidden'){preview.value=null;stopRecording(true);document.querySelectorAll('audio').forEach(el=>el.pause())}else{void loadConfig();if(bearer)void refresh()}}
onMounted(()=>{void loadConfig();configTimer=setInterval(()=>{if(document.visibilityState==='visible')void loadConfig()},60000);document.addEventListener('visibilitychange',visibility);window.addEventListener('pagehide',()=>{void logout()});window.addEventListener('pageshow',e=>{if(e.persisted)wipe()})});
onBeforeUnmount(()=>{if(configTimer)clearInterval(configTimer);wipe();document.removeEventListener('visibilitychange',visibility)});
</script>

<template>
  <main v-if="!user" class="login-shell">
    <section class="login-story"><a class="brand" href="/customer"><span class="brand-mark">◌</span><span>{{ display.brandName }} <small>PRIVATE CONVERSATION</small></span></a><div class="story-content"><span class="eyebrow">始终为你留一席</span><h1>每一条消息，<br>都有人回应。</h1><p>一段专属的对话，从这里开始。</p><div class="story-art"><div class="art-bubble art-one">你好，今天有什么可以帮你？<i></i></div><div class="art-bubble art-two">有几个问题，想和你聊聊。<i></i></div><span class="art-orbit"></span></div></div><footer>简单沟通 · 专属服务</footer></section>
    <section class="login-panel"><div class="mobile-brand"><span class="brand-mark">◌</span> {{ display.brandName }}</div><div class="login-form"><span class="eyebrow">{{ mode==='agent'?'工作台 / SERVICE DESK':'欢迎回来 / WELCOME BACK' }}</span><h2>{{ mode==='agent'?'进入客服工作台':'登录，开始对话' }}</h2><p>{{ mode==='agent'?'查看客户消息，管理你的专属服务。':'请使用客服提供的账号和密码登录。' }}</p><form @submit.prevent="login" autocomplete="off"><label for="username">账号</label><input id="username" v-model="username" autocomplete="off" autocapitalize="none" spellcheck="false" placeholder="请输入账号" required maxlength="32"><label for="password">密码</label><input id="password" v-model="password" type="password" autocomplete="off" placeholder="请输入密码" required maxlength="72"><p v-if="loginError" class="form-error" role="alert">{{ loginError }}</p><button class="primary full" :disabled="loginBusy">{{ loginBusy?'正在连接…':'登录' }} <span>↗</span></button></form><div class="login-note"><span>◇</span><p>每次打开重新验证身份<br><small>聊天记录从服务器读取，不提供离线浏览</small></p></div></div><div class="entry-switch"><button @click="showUpdateConfirm=true">检查页面更新</button><button @click="mode=mode==='agent'?'customer':'agent';loginError=''">{{ mode==='agent'?'切换到客户登录':'客服入口' }} <span>→</span></button></div></section>
  </main>
  <main v-else class="workspace" :class="{'show-chat':mobileChat, 'customer-workspace':!isAgent}">
    <aside v-if="isAgent" class="sidebar"><div class="sidebar-brand"><span class="brand-mark">◌</span><div>{{ display.brandName }}<small>客服工作台</small></div><button class="icon-button logout" title="退出登录" aria-label="退出登录" @click="logout">⇥</button></div><div class="sidebar-heading"><h2>会话 <span>{{ customers.length }}</span></h2><button class="new-button" @click="showCreate=true;formError=''">＋ 新建客户</button></div><button class="library-sidebar-button" @click="showSettings=true">应用设置 <span>⚙</span></button><button class="library-sidebar-button" @click="openLibrary()">回复与业务资料 <span>↗</span></button><div class="search"><span>⌕</span><input v-model="search" placeholder="搜索姓名或账号" aria-label="搜索客户"></div><div class="list-label">所有客户 <span v-if="totalUnread">{{ totalUnread }} 条未读</span></div><div class="customer-list"><button v-for="c in filtered" :key="c.id" class="customer-row" :class="{selected:active?.id===c.id}" @click="selectCustomer(c)"><span class="avatar" :class="{muted:c.disabled}">{{ c.name.slice(0,1) }}</span><span class="customer-summary"><b>{{ c.name }} <em v-if="c.disabled">已停用</em></b><small>{{ c.username }}</small></span><span v-if="c.unread" class="badge">{{ c.unread>99?'99+':c.unread }}</span><span v-else class="row-arrow">›</span></button><div v-if="!filtered.length" class="list-empty">{{ search?'没有找到这个客户':'创建第一个客户，开始服务。' }}</div></div><div class="sidebar-foot"><span class="status-dot" :class="{offline:!online}"></span>{{ online?'服务已连接':'正在连接服务' }}<small>账号由你统一管理</small></div></aside>
    <section class="chat-panel"><template v-if="active"><header class="chat-header"><button v-if="isAgent" class="icon-button mobile-back" aria-label="返回会话列表" @click="mobileChat=false">‹</button><span class="avatar header-avatar">{{ isAgent?title.slice(0,1):'客' }}</span><div class="chat-person"><h2>{{ isAgent?title:display.agentName }}</h2><small><i class="status-dot" :class="{offline:!online}"></i>{{ online?'对话已连接':'正在重新连接' }}<span v-if="active.disabled"> · 账号已停用</span></small></div><button v-if="isAgent" class="text-button" @click="openManage">账号管理</button><button v-if="isAgent" class="icon-button" title="清空聊天" aria-label="清空聊天" @click="confirmDelete='all';formError=''">⌫</button><button class="text-button" @click="showUpdateConfirm=true">更新</button><button v-if="!isAgent" class="text-button" @click="logout">退出</button></header><div v-if="display.announcement" class="client-announcement">{{ display.announcement }}</div><div class="conversation-banner"><span>◇</span> 只属于你和客服的对话</div>
    <div ref="scrollbox" class="messages" aria-live="polite"><button v-if="nextBefore" class="load-more" :disabled="loading" @click="older">{{ loading?'正在读取…':'查看更早的消息' }}</button><div v-if="loading&&!messages.length" class="empty-chat"><span class="empty-symbol">◌</span><h3>正在读取聊天记录</h3><p>从服务器获取最新对话…</p></div><div v-else-if="!messages.length" class="empty-chat"><span class="empty-symbol">✳</span><h3>{{ isAgent?'从一句问候开始':display.welcomeTitle }}</h3><p>{{ isAgent?'发送第一条消息，开启这段对话。':display.welcomeText }}</p></div><article v-for="m in messages" :key="m.id" class="message-row" :class="{own:m.sender===user.id}"><span class="avatar message-avatar">{{ m.sender===user.id?(isAgent?'客':user.name.slice(0,1)):(isAgent?title.slice(0,1):'客') }}</span><div class="message-content"><div class="bubble" :class="{media:!!m.attachment}"><p v-if="m.kind==='text'">{{ m.text }}</p><AttachmentView v-else-if="m.attachment" :attachment="m.attachment" :load="id=>request(`/attachments/${id}`)" @preview="showPreview" /></div><div class="message-meta"><time>{{ timeLabel(m.sentAt) }}</time><button v-if="isAgent" @click="confirmDelete=m.seq;formError=''">删除</button></div></div></article></div>
    <div v-if="error" class="inline-alert" role="alert">{{ error }}<button @click="error='';refresh()">重试</button></div><div v-if="notice" class="notice" role="status">{{ notice }}</div>
    <p v-if="!isAgent&&!canSend('text')&&!canSend('image')&&!canSend('voice')&&!canSend('file')" class="readonly-notice">当前为只读权限，可查看消息和客服回复。</p><form class="composer" @submit.prevent="submit()"><div v-if="isAgent&&!recording" class="library-composer-shortcuts"><button type="button" :disabled="busy" @click="openLibrary('reply')">☷ 快捷回复</button><button type="button" :disabled="busy" @click="openLibrary('intro')">▤ 业务介绍</button></div><div v-if="recording" class="recording-strip"><span class="record-dot"></span> 正在录音 {{ recordSeconds }} / 60 秒<button type="button" @click="stopRecording(true)">取消</button><button type="button" @click="stopRecording(false)">结束并发送 ↑</button></div><template v-else><textarea v-model="draft" placeholder="说点什么…" rows="2" maxlength="4000" aria-label="消息内容" :disabled="busy||active.disabled||!canSend('text')" @keydown.enter.exact.prevent="submit()"></textarea><div class="composer-bottom"><div class="composer-tools"><button type="button" class="tool-button" :disabled="busy||active.disabled||(!canSend('image')&&!canSend('file'))" @click="fileInput?.click()" aria-label="发送图片或文件">＋ <span>图片 / 文件</span></button><button type="button" class="tool-button" :disabled="busy||active.disabled||!canSend('voice')" @click="record" aria-label="录制语音">◖ <span>语音</span></button><input ref="fileInput" type="file" :accept="!canSend('file')?'image/png,image/jpeg,image/gif':undefined" hidden @change="pickFile"></div><span class="composer-hint">Enter 发送 · Shift + Enter 换行</span><button class="send-button" :disabled="busy||!draft.trim()||active.disabled||!canSend('text')">{{ busy?'处理中…':'发送' }} ↑</button></div></template></form></template>
    <div v-else class="desk-empty"><span class="eyebrow">你的专属服务空间</span><span class="desk-symbol">◌</span><h1>让每次沟通<br>都有回应。</h1><p>从左侧选择一位客户，查看对话。<br>也可以创建新账号，邀请客户加入。</p><button class="primary" @click="showCreate=true;formError=''">＋ 创建客户账号</button><p v-if="error" class="form-error">{{ error }}</p></div></section>
  </main>
  <div v-if="updateAvailable&&!showUpdateConfirm" class="update-banner" role="status">发现新页面版本 <button @click="showUpdateConfirm=true">更新</button></div>
  <SettingsPanel v-if="showSettings&&isAgent" :request="request" @close="showSettings=false" @saved="loadConfig" />
  <div v-if="showUpdateConfirm" class="modal-backdrop"><section class="modal small-modal" role="dialog" aria-modal="true" aria-label="更新应用"><span class="eyebrow">应用更新</span><h2>{{ updateAvailable?'有新版本可用':'检查页面更新' }}</h2><p>重新读取最新页面和连接配置，并重新登录。未发送的消息和未保存的编辑会丢弃。</p><p>当前版本：{{ initialWebVersion||'读取中' }}<span v-if="updateAvailable"> · 最新：{{ latestWebVersion }}</span></p><div class="manage-actions"><button class="secondary" @click="showUpdateConfirm=false">取消</button><button class="primary" @click="applyUpdate">更新并重新登录</button></div></section></div>
  <LibraryPanel v-if="showLibrary&&isAgent" :request="request" :customer="active" :initial-kind="libraryKind" @close="showLibrary=false" @sending="busy=$event" @sent="refresh()" />
  <div v-if="showCreate" class="modal-backdrop" @click.self="showCreate=false;newPassword=''" role="presentation"><section class="modal" role="dialog" aria-modal="true" aria-labelledby="create-title"><button class="modal-close" @click="showCreate=false;newPassword=''" aria-label="关闭">×</button><span class="eyebrow">客户管理</span><h2 id="create-title">创建客户账号</h2><p>请记下密码，通过你信任的方式交给客户。</p><form autocomplete="off" @submit.prevent="createCustomer"><label>客户称呼<input v-model="newName" required maxlength="40" placeholder="例如：陈先生"></label><label>登录账号<input v-model="newUsername" required pattern="[a-zA-Z0-9_.-]{3,32}" placeholder="3–32 位字母、数字或 ._-" autocomplete="off"></label><label>初始密码<input v-model="newPassword" type="password" required minlength="10" maxlength="72" placeholder="至少 10 位" autocomplete="new-password"></label><p v-if="formError" class="form-error">{{ formError }}</p><button class="primary full" :disabled="formBusy">{{ formBusy?'创建中…':'创建账号' }}</button></form></section></div>
  <div v-if="showManage&&active" class="modal-backdrop" @click.self="showManage=false;resetPassword=''"><section class="modal" role="dialog" aria-modal="true" aria-labelledby="manage-title"><button class="modal-close" @click="showManage=false;resetPassword=''" aria-label="关闭">×</button><span class="eyebrow">账号管理</span><h2 id="manage-title">{{ active.name }}</h2><p>{{ active.username }} · {{ active.disabled?'已停用':'使用中' }}</p><form @submit.prevent="savePermissions"><label>客户称呼<input v-model="customerNameDraft" required maxlength="40"></label><div class="permission-grid"><label v-for="(label,key) in {text:'文字',image:'图片',voice:'语音',file:'文件'}" :key="key"><input type="checkbox" v-model="permissionsDraft[key]">允许发送{{ label }}</label></div><p class="library-help">全部关闭即为只读，客户仍可接收客服回复。</p><button class="secondary full" :disabled="formBusy">保存称呼与权限</button></form><form @submit.prevent="manage('password')"><label>重置登录密码<input v-model="resetPassword" type="password" required minlength="10" maxlength="72" autocomplete="new-password" placeholder="设置新的密码"></label><button class="secondary full" :disabled="formBusy">重置密码并下线</button></form><div class="manage-actions"><button class="secondary" :disabled="formBusy" @click="manage('kick')">强制下线</button><button :class="active.disabled?'secondary':'danger-button'" :disabled="formBusy" @click="manage(active.disabled?'enable':'disable')">{{ active.disabled?'恢复账号':'停用账号' }}</button></div><p v-if="formError" class="form-error">{{ formError }}</p></section></div>
  <div v-if="confirmDelete!==null" class="modal-backdrop" @click.self="confirmDelete=null"><section class="modal small-modal" role="dialog" aria-modal="true" aria-labelledby="delete-title"><span class="eyebrow danger-text">删除记录</span><h2 id="delete-title">{{ confirmDelete==='all'?'清空这段对话？':'删除这条消息？' }}</h2><p>双方将无法再查看这些内容，相关附件也会一并移除。此操作无法在应用内撤销。</p><p v-if="formError" class="form-error">{{ formError }}</p><div class="manage-actions"><button class="secondary" @click="confirmDelete=null">取消</button><button class="danger-button" :disabled="formBusy" @click="deleteMessages">{{ formBusy?'处理中…':'确认删除' }}</button></div></section></div>
  <div v-if="preview" class="modal-backdrop preview-backdrop" @click.self="preview=null"><section class="preview-modal" role="dialog" aria-modal="true" aria-label="附件预览"><header><b>{{ preview.name }}</b><button aria-label="关闭预览" @click="preview=null">×</button></header><img v-if="preview.url" :src="preview.url" :alt="preview.name" draggable="false"><pre v-else>{{ preview.text }}</pre></section></div>
</template>
