<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount } from 'vue';
import MicrophoneIcon from './MicrophoneIcon.vue';
import type { Attachment } from './types';
const props = defineProps<{ attachment: Attachment; load: (id: string) => Promise<Response> }>();
const emit = defineEmits<{ preview: [id: string, url: string, name: string, text?: string] }>();
const url = ref(''), error = ref(''), busy = ref(false), player = ref<HTMLAudioElement>();
let alive = true;
async function fetchMedia() {
  if (url.value || busy.value) return;
  busy.value = true; error.value = '';
  try {
    const res = await props.load(props.attachment.id);
    if (props.attachment.kind === 'file') {
      const data = await res.json();
      if (alive) emit('preview', props.attachment.id, '', props.attachment.name, data.text);
    } else {
      const blob = await res.blob();
      if (alive) url.value = URL.createObjectURL(blob);
    }
  } catch(e) { if (alive) error.value = (e as Error).message; }
  finally { busy.value = false; }
}
onMounted(() => { if (props.attachment.kind !== 'file') void fetchMedia(); });
onBeforeUnmount(() => { alive = false; player.value?.pause(); if (url.value) URL.revokeObjectURL(url.value); });
</script>
<template>
  <div class="attachment">
    <button v-if="attachment.kind === 'image' && url" class="image-button" @click="emit('preview', attachment.id, url, attachment.name)"><img :src="url" :alt="attachment.name" draggable="false"></button>
    <div v-else-if="attachment.kind === 'voice'" class="voice"><span class="voice-label"><MicrophoneIcon /> 语音 · {{ Math.ceil(attachment.duration) }}″</span><audio v-if="url" ref="player" :src="url" controls controlslist="nodownload noplaybackrate" preload="none" /></div>
    <button v-else-if="attachment.kind === 'file'" class="file-card" @click="fetchMedia"><span class="file-icon">↗</span><span><b>{{ attachment.name }}</b><small>{{ (attachment.size / 1024).toFixed(1) }} KB · 点击预览</small></span></button>
    <small v-if="busy">正在读取…</small><button v-if="error" class="media-error" @click="fetchMedia">{{ error }} · 重试</button>
  </div>
</template>
