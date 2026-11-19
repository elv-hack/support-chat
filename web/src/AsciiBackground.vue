<script setup lang="ts">
import { ref,onMounted,onBeforeUnmount } from 'vue';
const canvas=ref<HTMLCanvasElement>();
let timer:ReturnType<typeof setInterval>|undefined,frame=0,media:MediaQueryList|undefined;
const glyphs='01ABCDEF{}[]<>:/+=*.-';
function draw(){
 const el=canvas.value,ctx=el?.getContext('2d');if(!el||!ctx)return;
 ctx.clearRect(0,0,el.width,el.height);ctx.font='12px monospace';ctx.textBaseline='top';
 const columns=Math.min(38,Math.ceil(el.width/26)),rows=Math.ceil(el.height/22);
 for(let col=0;col<columns;col++){
  const head=(Math.floor(frame/(2+col%3))+col*7)% (rows+8);
  for(let tail=0;tail<6;tail++){
   const row=head-tail;if(row<0||row>=rows)continue;
   ctx.fillStyle=tail===0?'rgba(113,246,177,.24)':`rgba(65,173,115,${.13-tail*.017})`;
   ctx.fillText(glyphs[(col*13+row*7+Math.floor(frame/6))%glyphs.length]!,col*(el.width/columns),row*22);
  }
 }
 frame++;
}
function sync(){if(timer)clearInterval(timer);timer=undefined;if(document.hidden)return;draw();if(!media?.matches)timer=setInterval(draw,250)}
function resize(){const el=canvas.value;if(!el)return;el.width=Math.min(960,window.innerWidth);el.height=Math.min(900,window.innerHeight);draw()}
onMounted(()=>{media=matchMedia('(prefers-reduced-motion: reduce)');resize();sync();media.addEventListener('change',sync);window.addEventListener('resize',resize);document.addEventListener('visibilitychange',sync)});
onBeforeUnmount(()=>{if(timer)clearInterval(timer);media?.removeEventListener('change',sync);window.removeEventListener('resize',resize);document.removeEventListener('visibilitychange',sync)});
</script>
<template><canvas ref="canvas" class="ascii-background" aria-hidden="true"></canvas></template>
