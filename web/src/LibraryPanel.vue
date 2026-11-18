<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import AttachmentView from './AttachmentView.vue';
import type { Attachment, LibraryBlock, LibraryDelivery, LibraryEntry, LibrarySection, User } from './types';
const props=defineProps<{request:(path:string,init?:RequestInit)=>Promise<Response>;customer:User|null;initialKind:'reply'|'intro'}>();
const emit=defineEmits<{close:[];sending:[value:boolean];sent:[]}>();
const kind=ref(props.initialKind),view=ref<'list'|'edit'|'send'>('list');
const entries=ref<LibraryEntry[]>([]),deliveries=ref<LibraryDelivery[]>([]),search=ref(''),category=ref('');
const busy=ref(false),loading=ref(false),error=ref(''),notice=ref(''),discard=ref<'close'|'back'|null>(null),deleting=ref<LibraryEntry|null>(null);
const editor=ref<LibraryEntry|null>(null),picked=ref<boolean[]>([]),imagePreview=ref<{url:string;name:string}|null>(null);
const attempt=ref<{id:string;customer:string;title:string;sections:LibrarySection[]}|null>(null);
const cancelling=ref<LibraryDelivery|null>(null);
let original='',alive=true;
const keys=new WeakMap<object,string>();
function keyFor(value:object){let key=keys.get(value);if(!key){key=crypto.randomUUID();keys.set(value,key)}return key}
const clone=<T,>(value:T):T=>JSON.parse(JSON.stringify(value));
const textBlock=():LibraryBlock=>({kind:'text',text:''});
const section=():LibrarySection=>({title:'',blocks:[textBlock()]});
const dirty=computed(()=>editor.value!==null&&JSON.stringify(editor.value)!==original);
const categories=computed(()=>{
 const paths=new Set<string>();for(const e of entries.value.filter(e=>e.kind===kind.value)){const parts=e.category.split('/');for(let i=1;e.category&&i<=parts.length;i++)paths.add(parts.slice(0,i).join('/'))}return [...paths].sort();
});
const filtered=computed(()=>entries.value.filter(e=>e.kind===kind.value&&(!category.value||e.category===category.value||e.category.startsWith(category.value+'/'))&&[e.title,e.category,...e.sections.flatMap(s=>[s.title,...s.blocks.map(b=>b.text||'')])].join(' ').toLowerCase().includes(search.value.trim().toLowerCase())));
const pending=computed(()=>deliveries.value.filter(d=>d.sent<d.total));
const selectedSections=computed(()=>editor.value?.sections.filter((_,i)=>picked.value[i])||[]);
const messageCount=computed(()=>selectedSections.value.reduce((n,s)=>n+s.blocks.length+(s.title.trim()?1:0),0));
const canSend=computed(()=>!!props.customer&&!props.customer.disabled&&!busy.value);
const imageAttachment=(id:string):Attachment=>({id,name:'业务介绍图片',kind:'image',mime:'image/png',size:0,duration:0});
async function api<T>(path:string,init:RequestInit={}):Promise<T>{return (await props.request(path,init)).json()}
async function load(){loading.value=true;error.value='';try{const result=await api<LibraryEntry[]>('/library/entries');if(alive)entries.value=result;await loadDeliveries()}catch(e){if(alive)error.value=(e as Error).message}finally{if(alive)loading.value=false}}
async function loadDeliveries(){if(!props.customer)return;const result=await api<LibraryDelivery[]>(`/library/deliveries?customer=${encodeURIComponent(props.customer.id)}`);if(alive)deliveries.value=result}
function switchKind(next:'reply'|'intro'){kind.value=next;search.value='';category.value='';notice.value=''}
function edit(entry?:LibraryEntry,duplicate=false){
 editor.value=entry?clone(entry):{id:'',kind:kind.value,title:'',category:category.value,sections:[section()],version:0,updated:0};
 if(duplicate){editor.value.id='';editor.value.version=0;editor.value.title=editor.value.title.slice(0,76)+' 副本'}
 original=JSON.stringify(editor.value);view.value='edit';error.value='';notice.value='';discard.value=null;
}
function preview(entry:LibraryEntry){editor.value=clone(entry);picked.value=entry.sections.map(()=>true);original=JSON.stringify(editor.value);view.value='send';error.value='';notice.value='';attempt.value=null;discard.value=null}
function leave(action:'close'|'back'){if(busy.value)return;if(dirty.value&&view.value!=='list'){discard.value=action;return}finishLeave(action)}
function finishLeave(action:'close'|'back'){discard.value=null;editor.value=null;attempt.value=null;if(action==='close')emit('close');else{view.value='list';error.value='';void load()}}
function move<T>(list:T[],index:number,offset:number){const to=index+offset;if(to<0||to>=list.length)return;[list[index],list[to]]=[list[to]!,list[index]!];if(editor.value&&list===editor.value.sections){[picked.value[index],picked.value[to]]=[picked.value[to]!,picked.value[index]!]}}
function addSection(){if(!editor.value||editor.value.sections.length>=20)return;editor.value.sections.push(section());picked.value.push(true)}
function removeSection(index:number){editor.value?.sections.splice(index,1);picked.value.splice(index,1)}
async function addImage(s:LibrarySection,event:Event){
 const input=event.target as HTMLInputElement,file=input.files?.[0];input.value='';if(!file||busy.value)return;
 if(file.size>5*1024*1024){error.value='图片不能超过 5 MB';return}
 busy.value=true;error.value='';try{const form=new FormData();form.append('file',file);const image=await api<Attachment>('/library/images',{method:'POST',body:form});if(alive)s.blocks.push({kind:'image',imageId:image.id})}catch(e){if(alive)error.value=(e as Error).message}finally{if(alive)busy.value=false}
}
async function save(){
 if(!editor.value||busy.value)return;busy.value=true;error.value='';
 try{const e=editor.value;await api('/library/entries'+(e.id?'/'+e.id:''),{method:e.id?'PUT':'POST',body:JSON.stringify(e)});if(!alive)return;editor.value=null;view.value='list';notice.value='模板已保存';await load()}catch(e){if(alive)error.value=(e as Error).message}finally{if(alive)busy.value=false}
}
async function remove(){
 if(!deleting.value||busy.value)return;busy.value=true;error.value='';
 try{const e=deleting.value;await api(`/library/entries/${e.id}?version=${e.version}`,{method:'DELETE'});if(!alive)return;deleting.value=null;notice.value='模板已删除，已发送的消息保留';await load()}catch(e){if(alive)error.value=(e as Error).message}finally{if(alive)busy.value=false}
}
async function send(entry?:LibraryEntry,resumeId?:string){
 if(!canSend.value)return;const customer=props.customer!.id;
 if(!resumeId&&!attempt.value){const sections=entry?clone(entry.sections):clone(selectedSections.value);if(!sections.length){error.value='请至少选择一个章节';return};attempt.value={id:crypto.randomUUID(),customer,title:entry?.title||editor.value!.title,sections}}
 if(attempt.value&&attempt.value.customer!==customer){error.value='收件客户已变化，请关闭后重新选择';return}
 busy.value=true;emit('sending',true);error.value='';notice.value='';
 const payload=resumeId?{id:resumeId}:{id:attempt.value!.id,title:attempt.value!.title,sections:attempt.value!.sections};
 try{const result=await api<{sent:number;total:number}>(`/library/send?customer=${encodeURIComponent(customer)}`,{method:'POST',body:JSON.stringify(payload)});if(!alive)return;notice.value=`已向 ${props.customer!.name} 发送 ${result.sent} 条消息`;attempt.value=null;editor.value=null;view.value='list';emit('sent');await loadDeliveries()}
 catch(e){if(alive){error.value=(e as Error).message;emit('sent');try{await loadDeliveries();if(attempt.value&&!deliveries.value.some(d=>d.id===attempt.value!.id))attempt.value=null}catch{}}}
 finally{if(alive){busy.value=false;emit('sending',false)}}
}
async function cancelDelivery(){
 if(!cancelling.value||!props.customer||busy.value)return;
 busy.value=true;error.value='';try{await api(`/library/deliveries/${cancelling.value.id}?customer=${encodeURIComponent(props.customer.id)}`,{method:'DELETE'});if(alive){cancelling.value=null;notice.value='未完成的发送已结束，已经发出的消息保留';await loadDeliveries()}}catch(e){if(alive)error.value=(e as Error).message}finally{if(alive)busy.value=false}
}
function resetAttempt(){attempt.value=null;editor.value=null;view.value='list';error.value='';void loadDeliveries().catch(e=>{error.value=(e as Error).message})}
onMounted(load);onBeforeUnmount(()=>{alive=false;emit('sending',false)});
</script>
<template>
 <div class="library-backdrop" role="presentation" @click.self="leave('close')">
  <section class="library-panel" role="dialog" aria-modal="true" aria-labelledby="library-title">
   <header class="library-header"><div><span class="eyebrow">客服工作台 / 内容管理</span><h2 id="library-title">{{ view==='list'?'回复与业务资料':view==='edit'?(editor?.id?'编辑模板':'新建模板'):'编辑本次发送' }}</h2><p v-if="customer">发送给 <strong>{{ customer.name }}</strong> · {{ customer.username }}<span v-if="customer.disabled"> · 已停用</span></p><p v-else>先整理资料，选择客户后即可发送</p></div><button class="icon-button" aria-label="关闭资料库" :disabled="busy" @click="leave('close')">×</button></header>
   <div v-if="discard" class="library-confirm" role="alert"><p>放弃尚未保存的修改？</p><button class="secondary" @click="discard=null">继续编辑</button><button class="danger-button" @click="finishLeave(discard!)">放弃修改</button></div>
   <div v-if="deleting" class="library-confirm" role="alert"><p>删除“{{ deleting.title }}”？已经发给客户的内容不会改变。</p><button class="secondary" :disabled="busy" @click="deleting=null">取消</button><button class="danger-button" :disabled="busy" @click="remove">确认删除模板</button></div>
   <div v-if="cancelling" class="library-confirm" role="alert"><p>结束“{{ cancelling.title }}”的发送？已提交的消息保留，剩余内容不再发送。</p><button class="secondary" :disabled="busy" @click="cancelling=null">返回</button><button class="danger-button" :disabled="busy" @click="cancelDelivery">结束本次发送</button></div>
   <div v-if="error" class="library-error" role="alert">{{ error }}<button v-if="view==='list'&&!attempt" class="text-button" :disabled="busy" @click="load">刷新列表</button></div>
   <div v-if="notice" class="library-notice" role="status">{{ notice }}</div>
   <div v-if="attempt&&!busy" class="library-retry"><b>上一次发送尚未确认完成</b><p>继续时会核对进度，已成功的消息不会重复发送。结果待确认的消息需要先在聊天记录中核实。</p><button class="primary" :disabled="!canSend" @click="send()">继续这次发送</button><button class="text-button" @click="resetAttempt">返回列表查看发送记录</button></div>
   <div v-if="busy" class="library-progress" role="status">正在处理，请稍候。发送过程中请保持此页面打开。</div>
   <div class="library-body" :inert="busy||!!attempt||!!discard||!!deleting||!!cancelling">
    <template v-if="view==='list'">
     <div class="library-tabs" role="tablist" aria-label="资料类型"><button role="tab" :aria-selected="kind==='reply'" :class="{active:kind==='reply'}" @click="switchKind('reply')">快捷回复</button><button role="tab" :aria-selected="kind==='intro'" :class="{active:kind==='intro'}" @click="switchKind('intro')">业务介绍</button></div>
     <div class="library-toolbar"><input v-model="search" type="search" aria-label="搜索资料" placeholder="搜索标题、分类、正文…"><select v-model="category" aria-label="筛选资料分类"><option value="">全部分类</option><option v-for="c in categories" :key="c" :value="c">{{ c }}</option></select><button class="primary" @click="edit()">＋ {{ kind==='reply'?'新建回复':'新建介绍' }}</button></div>
     <p class="library-help">{{ kind==='reply'?'提前写好常见问答，点击“一键发送”即可回复当前客户。':'把复杂业务拆成章节，搭配图片；发送时可选择整套或部分章节。' }}</p>
     <details v-if="pending.length" class="library-pending"><summary>未完成的发送 · {{ pending.length }}</summary><div v-for="d in pending" :key="d.id"><span><b>{{ d.title }}</b><small>已提交 {{ d.sent }} / {{ d.total }} 条{{ d.uncertain?' · 有消息结果待确认':'' }}</small></span><button class="secondary" :disabled="!canSend" @click="send(undefined,d.id)">核对并继续</button><button class="text-button" @click="cancelling=d">结束</button></div></details>
     <p v-if="loading" class="library-empty">正在读取资料…</p>
     <div v-else-if="!filtered.length" class="library-empty"><span>✳</span><h3>{{ search||category?'没有找到匹配的内容':kind==='reply'?'把常用的话，提前准备好':'为你的业务，整理一份清晰的介绍' }}</h3><p>{{ kind==='reply'?'例如：服务时间、收费说明、办理流程。':'例如：业务总览 / 服务方案 / 办理流程 / 常见问题。' }}</p><button class="secondary" @click="edit()">{{ kind==='reply'?'写一条快捷回复':'创建第一份业务介绍' }}</button></div>
     <div v-else class="library-grid"><article v-for="entry in filtered" :key="entry.id" class="library-card"><div class="library-card-label">{{ entry.category||'未分类' }}<span>{{ entry.kind==='reply'?'常见问答':`${entry.sections.length} 个章节` }}</span></div><h3>{{ entry.title }}</h3><p class="library-excerpt">{{ entry.sections.flatMap(s=>[s.title,...s.blocks.map(b=>b.kind==='text'?b.text:'[图片]')]).filter(Boolean).join('\n') }}</p><div class="library-card-send"><button v-if="kind==='reply'" class="primary" :disabled="!canSend" @click="send(entry)">一键发送</button><button class="secondary" :disabled="!customer||customer.disabled" @click="preview(entry)">{{ kind==='reply'?'编辑后发送':'选择章节并发送' }}</button></div><div class="library-card-edit"><button @click="edit(entry)">编辑模板</button><button @click="edit(entry,true)">复制</button><button @click="deleting=entry">删除</button></div></article></div>
    </template>
    <template v-else-if="editor">
     <p class="library-help" v-if="view==='send'">本次修改只影响发给 {{ customer?.name }} 的内容，不会改动原模板。章节标题、文字和图片按顺序发送。</p>
     <div v-if="view==='edit'" class="library-metadata"><label>标题<input v-model="editor.title" maxlength="80" placeholder="例如：收费标准 / 标准服务方案" required></label><label>分类（可选）<input v-model="editor.category" maxlength="120" placeholder="例如：业务介绍/企业服务/标准方案" list="library-categories"></label><datalist id="library-categories"><option v-for="c in categories" :key="c" :value="c" /></datalist></div>
     <div v-if="view==='send'&&editor.kind==='intro'" class="library-select-all"><b>{{ editor.title }}</b><button class="text-button" @click="picked=editor!.sections.map(()=>true)">全选</button><button class="text-button" @click="picked=editor!.sections.map(()=>false)">清空选择</button></div>
     <section v-for="(s,index) in editor.sections" :key="keyFor(s)" class="library-section" :class="{unselected:view==='send'&&!picked[index]}">
      <div class="library-section-head"><label v-if="view==='send'&&editor.kind==='intro'" class="library-checkbox"><input type="checkbox" v-model="picked[index]">发送本章</label><b v-else>{{ editor.kind==='reply'?'回复正文':`章节 ${index+1}` }}</b><div v-if="editor.kind==='intro'" class="library-order"><button :disabled="index===0" :aria-label="`上移章节 ${index+1}`" @click="move(editor!.sections,index,-1)">↑</button><button :disabled="index===editor.sections.length-1" :aria-label="`下移章节 ${index+1}`" @click="move(editor!.sections,index,1)">↓</button><button :disabled="editor.sections.length===1" :aria-label="`删除章节 ${index+1}`" @click="removeSection(index)">删除章节</button></div></div>
      <label v-if="editor.kind==='intro'" class="library-section-title">章节标题<input v-model="s.title" maxlength="80" placeholder="例如：方案特点（留空则不发送标题）"></label>
      <div v-for="(block,bi) in s.blocks" :key="keyFor(block)" class="library-block"><div v-if="editor.kind==='intro'" class="library-block-head"><small>{{ block.kind==='text'?'文字':'图片' }} {{ bi+1 }}</small><div class="library-order"><button :disabled="bi===0" aria-label="上移内容" @click="move(s.blocks,bi,-1)">↑</button><button :disabled="bi===s.blocks.length-1" aria-label="下移内容" @click="move(s.blocks,bi,1)">↓</button><button :disabled="s.blocks.length===1" aria-label="移除内容" @click="s.blocks.splice(bi,1)">移除</button></div></div><template v-if="block.kind==='text'"><textarea v-model="block.text" :aria-label="editor.kind==='reply'?'快捷回复正文':`章节 ${index+1} 文字 ${bi+1}`" rows="5" maxlength="4000" placeholder="填写要发给客户的内容，可换行…" /><small class="library-count">{{ [...(block.text||'')].length }} / 4000</small></template><AttachmentView v-else-if="block.imageId" :attachment="imageAttachment(block.imageId)" :load="id=>request(`/library/images/${id}`)" @preview="(_id,url,name)=>imagePreview={url,name}" /></div>
      <div v-if="editor.kind==='intro'" class="library-add"><button class="secondary" @click="s.blocks.push(textBlock())">＋ 文字</button><label class="secondary library-upload">＋ 图片<input type="file" accept="image/png,image/jpeg,image/gif" aria-label="添加业务图片" @change="addImage(s,$event)"></label></div>
     </section>
     <button v-if="editor.kind==='intro'" class="secondary library-add-section" :disabled="editor.sections.length>=20" @click="addSection">＋ 添加章节</button><p v-if="editor.kind==='intro'" class="library-help">最多 20 个章节、40 段内容；每张图片最大 5 MB。使用 ↑ ↓ 调整内容顺序。</p>
    </template>
   </div>
   <footer v-if="view!=='list'" class="library-footer"><button class="secondary" :disabled="busy||!!attempt" @click="leave('back')">返回列表</button><span v-if="view==='send'">已选 {{ selectedSections.length }} 章 · {{ messageCount }} 条消息</span><button v-if="view==='edit'" class="primary" :disabled="busy||!!attempt" @click="save">保存模板</button><button v-else class="primary" :disabled="!canSend||!messageCount||!!attempt" @click="send()">{{ busy?'发送中…':`发送给 ${customer?.name||'客户'}` }}</button></footer>
  </section>
  <div v-if="imagePreview" class="library-image-preview" @click.self="imagePreview=null"><section><button class="secondary" aria-label="关闭业务图片预览" @click="imagePreview=null">关闭预览 ×</button><img :src="imagePreview.url" :alt="imagePreview.name"></section></div>
 </div>
</template>
