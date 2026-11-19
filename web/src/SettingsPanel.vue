<script setup lang="ts">
import { onMounted, onBeforeUnmount, ref } from 'vue';
import type { AppSettings, Connection } from './types';
const props=defineProps<{request:(path:string,init?:RequestInit)=>Promise<Response>}>();
const emit=defineEmits<{close:[];saved:[]}>();
const tab=ref<'display'|'connection'>('display'),settings=ref<AppSettings|null>(null),connection=ref<Connection|null>(null);
const busy=ref(false),error=ref(''),notice=ref(''),serverUrl=ref(''),password=ref(''),dataReady=ref(false),discard=ref(false);
const currentOrigin=location.origin;
let original='',alive=true;
async function api<T>(path:string,init:RequestInit={}):Promise<T>{return(await props.request(path,init)).json()}
async function load(){busy.value=true;error.value='';try{const result=await api<AppSettings>('/admin/settings');if(!alive)return;settings.value=result;original=JSON.stringify(result);await refreshConnection()}catch(e){if(alive)error.value=(e as Error).message}finally{if(alive)busy.value=false}}
async function refreshConnection(){try{const result=await api<Connection>('/admin/connection');if(!alive)return;connection.value=result;serverUrl.value=result.route?.serverUrl||location.origin;password.value='';dataReady.value=false}catch(e){if(alive)error.value=(e as Error).message}}
function close(){if(busy.value)return;if(settings.value&&JSON.stringify(settings.value)!==original){discard.value=true;return};emit('close')}
async function save(){if(!settings.value||busy.value)return;busy.value=true;error.value='';notice.value='';try{const result=await api<AppSettings>('/admin/settings',{method:'PUT',body:JSON.stringify(settings.value)});if(alive){settings.value=result;original=JSON.stringify(result);notice.value='显示设置已发布，客户页面会自动读取';emit('saved')}}catch(e){if(alive)error.value=(e as Error).message}finally{if(alive)busy.value=false}}
async function publish(){if(busy.value||!connection.value?.configured)return;busy.value=true;error.value='';notice.value='';try{const result=await api<Connection>('/admin/connection',{method:'PUT',body:JSON.stringify({serverUrl:serverUrl.value.trim(),expectedRevision:connection.value.route?.revision||0,password:password.value,dataReady:dataReady.value})});if(alive){connection.value=result;notice.value='业务地址已发布。App 下次打开或检查更新时会验证新服务并重新登录。';dataReady.value=false}}catch(e){if(alive)error.value=(e as Error).message}finally{password.value='';if(alive)busy.value=false}}
onMounted(load);onBeforeUnmount(()=>{alive=false;password.value=''});
</script>
<template>
 <div class="library-backdrop" @click.self="close"><section class="library-panel settings-panel" role="dialog" aria-modal="true" aria-labelledby="settings-title">
  <header class="library-header"><div><span class="eyebrow">客服工作台 / 应用管理</span><h2 id="settings-title">应用设置</h2></div><button class="icon-button" aria-label="关闭应用设置" :disabled="busy" @click="close">×</button></header>
  <div v-if="discard" class="library-confirm"><p>放弃未保存的显示设置？</p><button class="secondary" @click="discard=false">继续编辑</button><button class="danger-button" @click="emit('close')">放弃修改</button></div>
  <div v-if="error" class="library-error" role="alert">{{ error }}</div><div v-if="notice" class="library-notice" role="status">{{ notice }}</div>
  <div class="library-body" :inert="busy||discard">
   <div class="library-tabs" role="tablist"><button role="tab" :aria-selected="tab==='display'" :class="{active:tab==='display'}" @click="tab='display'">显示与公告</button><button role="tab" :aria-selected="tab==='connection'" :class="{active:tab==='connection'}" @click="tab='connection'">连接与更新</button></div>
   <template v-if="tab==='display'&&settings"><div class="library-metadata"><label>应用内名称<input v-model="settings.brandName" maxlength="24" placeholder="例如：专属服务"></label><label>客服显示名称<input v-model="settings.agentName" maxlength="24" placeholder="例如：专属顾问"></label></div><label>客户公告<textarea v-model="settings.announcement" rows="3" maxlength="500" placeholder="例如：本周服务时间调整；留空则不显示"></textarea></label><label class="theme-picker">主题色<input type="color" v-model="settings.primaryColor" aria-label="主题色"><span>{{ settings.primaryColor }}</span></label><p class="library-help">设置会影响登录页和聊天页面；手机桌面的 App 名称、图标等原生内容需要新版安装包。</p></template>
   <template v-else-if="tab==='connection'"><div class="settings-connection-card"><b>当前页面版本</b><p>{{ connection?.webVersion||'正在读取…' }}</p><b>当前业务地址</b><p>{{ currentOrigin }}</p><p class="library-help">小型页面更新发布到服务器后即可使用；已打开页面会提示更新，更新后需重新登录。</p></div><div v-if="connection?.configured" class="settings-connection-card"><b>独立固定入口</b><p>{{ connection.directoryUrl }}</p><b>目前发布的业务地址</b><p>{{ connection.route?.serverUrl||'尚未发布' }}</p><p class="library-help">地址版本 {{ connection.route?.revision||0 }}。固定入口应长期保留，并与业务服务器分开部署。</p><button class="secondary" @click="refreshConnection">重新读取当前地址</button></div><p v-else class="library-help">尚未连接独立固定入口。先完成一次入口服务初始化，之后即可在此发布新业务地址；日常改地址不需要重装 App。</p>
    <form v-if="connection?.configured" @submit.prevent="publish" autocomplete="off"><label>新的业务地址<input v-model="serverUrl" type="url" placeholder="https://chat-new.example.com" required></label><p class="library-help">先在新服务器恢复账号、OpenIM 数据和附件，并确认服务正常。这里发布连接地址，不会自动搬运数据。</p><label class="library-checkbox"><input v-model="dataReady" type="checkbox" required>我已完成数据迁移并核对新服务</label><label>客服密码确认<input v-model="password" type="password" autocomplete="off" required maxlength="72" placeholder="输入当前客服密码"></label><button class="primary full" :disabled="busy||!dataReady||!password">验证新服务并发布地址</button></form>
   </template>
  </div><footer class="library-footer"><button class="secondary" :disabled="busy" @click="close">关闭</button><button v-if="tab==='display'" class="primary" :disabled="busy||!settings" @click="save">{{ busy?'处理中…':'发布显示设置' }}</button><span v-else-if="busy">正在验证与发布…</span></footer>
 </section></div>
</template>
