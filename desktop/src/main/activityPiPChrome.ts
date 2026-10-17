import appIcon from "../../../assets/app-icon-source.svg?raw";
import { iconSVG } from "../shared/iconArtwork";
import { getMainLocale, MAIN_TRANSLATION_RESOURCES } from "./i18n";
import { cursorRuntimeSource } from "./agentCursor";

export function browserPiPOverlayHTML(initialLabel: string): string {
  const translations = Object.fromEntries(Object.entries(MAIN_TRANSLATION_RESOURCES).map(([locale, catalog]) => [
    locale, Object.fromEntries(Object.entries(catalog).filter(([key]) => key.startsWith("activity"))),
  ]));
  const strings = JSON.stringify(translations).replace(/</g, "\\u003c");
  const language = JSON.stringify(getMainLocale());
  const label = JSON.stringify(initialLabel).replace(/</g, "\\u003c");
  const icon = `data:image/svg+xml,${encodeURIComponent(appIcon)}`;
  // Sample the reference's mass=1, stiffness=170, damping=18 completion
  // spring for the compositor; no renderer timer survives completion or hiding.
  const completionSpring = `linear(${Array.from({ length: 61 }, (_, i) => {
    const t = i / 100;
    const frequency = Math.sqrt(170 - 9 * 9);
    return i === 60 ? 1 : 1 - Math.exp(-9 * t)
      * (Math.cos(frequency * t) + 9 / frequency * Math.sin(frequency * t));
  }).join(",")})`;
  return `<!doctype html>
<html><head><meta charset="utf-8" />
<style>
*{box-sizing:border-box;margin:0;padding:0}
:root{color-scheme:light;--surface:#f8f8fa;--text:#131314;--muted:#5f5f61;--ui-size:15px}
:root.dark{color-scheme:dark;--surface:#242426;--text:#f4f4f5;--muted:#b5b5b7}
::-webkit-scrollbar{width:0;height:0;display:none}
html,body{width:100%;height:100%;overflow:hidden;scrollbar-width:none;
  background:rgba(0,0,0,0.004);
  font-family:-apple-system,BlinkMacSystemFont,"Helvetica Neue",sans-serif;
  font-size:var(--ui-size);color:#fff;user-select:none}
[hidden]{display:none!important}
#root{position:relative;width:100%;height:100%;cursor:grab;touch-action:none;
  background:rgba(0,0,0,0.004)}
#root.dragging{cursor:grabbing}
#ph{position:absolute;inset:0;display:grid;place-items:center;
  background:var(--surface);color:var(--muted);transition:opacity .2s ease}
#ph-content{display:flex;flex-direction:column;align-items:center;gap:12px;padding:40px 16px 16px;max-width:100%;text-align:center}
#ph-icon{display:flex}
@media(max-height:160px){#ph{overflow-y:auto;align-content:start}#ph-content{gap:8px;padding:38px 12px 12px}#ph-icon{display:none}}
#ph-status{font-size:var(--ui-size);overflow-wrap:anywhere}
#retry{border:1px solid var(--muted);border-radius:6px;padding:6px 10px;font:inherit;color:var(--text);background:transparent;cursor:pointer}
#retry:focus-visible{outline:2px solid var(--text);outline-offset:3px}
#ph.preparing #ph-icon{animation:ph-breathe 1.6s ease-in-out infinite alternate}
@keyframes ph-breathe{from{opacity:1}to{opacity:.35}}
#ph.gone{opacity:0;pointer-events:none}
#appear{position:absolute;z-index:1;pointer-events:none;display:block}
#completion{position:absolute;inset:0;z-index:1;display:grid;place-items:center;pointer-events:none;
  background:rgba(0,0,0,.17);opacity:0;transition:opacity .35s ease}
#completion[data-completed="true"]{opacity:1}
#completion-mark{position:relative;width:50px;height:50px}
#completion-icon{display:block;width:100%;height:100%;border-radius:12px;
  box-shadow:0 3px 12px rgba(0,0,0,.16)}
#completion-check{position:absolute;inset:0;display:grid;place-items:center;
  color:white;opacity:0;transform:translate(18px,18px)}
#completion-check::before{content:"";position:absolute;width:17.5px;height:17.5px;border-radius:50%;
  background:#00e62d;box-shadow:0 0 4px rgba(0,0,0,.8);opacity:0}
#completion-check svg{position:relative;width:18px;height:18px;transform:scale(.5)}
#completion[data-completed="true"] #completion-icon{animation:completion-icon 2.75s linear both}
#completion[data-completed="true"] #completion-check{
  animation:completion-fade .2s 1s linear both,completion-move .6s 2.5s ${completionSpring} both}
#completion[data-completed="true"] #completion-check svg{
  animation:completion-grow .6s 1s ${completionSpring} both,completion-mini .6s 2.5s ${completionSpring} forwards}
#completion[data-completed="true"] #completion-check::before{
  animation:completion-fade .2s 2.5s linear both,completion-grow .6s 2.5s ${completionSpring} both}
@keyframes completion-icon{
  0%{opacity:0;filter:brightness(1)}
  12.727%,36.364%{opacity:1;filter:brightness(1)}
  45.455%,90.909%{opacity:1;filter:brightness(.35)}
  100%{opacity:1;filter:brightness(1)}}
@keyframes completion-fade{from{opacity:0}to{opacity:1}}
@keyframes completion-grow{from{transform:scale(.01)}to{transform:scale(1)}}
@keyframes completion-mini{from{transform:scale(1)}to{transform:scale(.5)}}
@keyframes completion-move{from{transform:translate(0,0)}to{transform:translate(18px,18px)}}
#hover-shade{position:absolute;inset:0;pointer-events:none;opacity:0;
  background:rgba(0,0,0,.06);transition:opacity .12s ease-in-out}
#actions{position:absolute;top:6px;left:6px;z-index:6;display:flex;gap:2px;
  opacity:0;pointer-events:none;transition:opacity .12s ease-in-out}
#root:is(:focus-within,.hovered,.dragging,.resizing,.attention) :is(#actions,#hover-shade){opacity:1}
#root:is(:focus-within,.hovered,.dragging,.resizing,.attention) #actions{pointer-events:auto}
/* A sibling WebContentsView cannot sample the page with backdrop-filter.
   A local scrim keeps the controls legible over browser and native pixels. */
#actions::before{content:"";position:absolute;inset:-4px -3px;z-index:-1;
  border-radius:14px;background:rgba(0,0,0,.58);filter:blur(4px);pointer-events:none}
#actions button{width:28px;height:28px;flex:none;border:none;border-radius:6px;padding:0;
  display:grid;place-items:center;cursor:pointer;color:#fff;background:transparent;
  transition:background .12s ease-in-out}
#actions button:is(:hover,.hovered){background:rgba(255,255,255,.17)}
#actions button:active{background:rgba(255,255,255,.26)}
#actions button:focus-visible{outline:2px solid #fff;outline-offset:1px}
:root.reduce-motion #root *, :root.reduce-motion #root *::before{animation:none!important;transition:none!important}
:root.reduce-motion #completion[data-completed="true"] #completion-check,
:root.reduce-motion #completion[data-completed="true"] #completion-check::before{opacity:1}
@media(prefers-reduced-motion:reduce){
  #root *,#root *::before{animation:none!important;transition:none!important}
  #completion[data-completed="true"] #completion-check,
  #completion[data-completed="true"] #completion-check::before{opacity:1}
}
[data-resize]{position:absolute;z-index:4;touch-action:none;background:rgba(0,0,0,0.004)}
[data-resize="n"],[data-resize="s"]{left:18px;right:18px;height:12px;cursor:ns-resize}
[data-resize="n"]{top:0}[data-resize="s"]{bottom:0}
[data-resize="e"],[data-resize="w"]{top:18px;bottom:18px;width:12px;cursor:ew-resize}
[data-resize="e"]{right:0}[data-resize="w"]{left:0}
[data-resize="nw"],[data-resize="ne"],[data-resize="sw"],[data-resize="se"]{width:18px;height:18px}
[data-resize="nw"]{top:0;left:0;cursor:nwse-resize}
[data-resize="se"]{bottom:0;right:0;cursor:nwse-resize}
[data-resize="ne"]{top:0;right:0;cursor:nesw-resize}
[data-resize="sw"]{bottom:0;left:0;cursor:nesw-resize}
</style></head>
<body>
<div id="root" role="group">
  <div id="ph"><div id="ph-content"><span id="ph-icon" aria-hidden="true">${iconSVG("Globe", 28)}</span><span id="ph-status" role="status"></span><button id="retry" type="button" hidden></button></div></div>
  <img id="appear" alt="" aria-hidden="true" draggable="false" hidden />
  <div id="completion" role="status" aria-hidden="true" data-completed="false">
    <div id="completion-mark">
      <img id="completion-icon" src="${icon}" alt="" draggable="false" />
      <div id="completion-check">${iconSVG("Check", 34)}</div>
    </div>
  </div>
  <div id="hover-shade"></div>
  <div id="actions">
    <button id="close" type="button">${iconSVG("X", 16)}</button>
    <button id="expand" type="button">${iconSVG("ExternalLink", 16)}</button>
  </div>
  <div data-resize="nw"></div><div data-resize="n"></div><div data-resize="ne"></div>
  <div data-resize="w"></div><div data-resize="e"></div>
  <div data-resize="sw"></div><div data-resize="s"></div><div data-resize="se"></div>
</div>
<script>
(function(){
  var ph=document.getElementById("ph");
  var cursor=(${cursorRuntimeSource})();
  var root=document.getElementById("root");
  var completion=document.getElementById("completion");
  var label=${label},locale=${language},translations=${strings};
  var activity={kind:"browser",state:"starting",controller:"none"};
  var mounted=false,capture={status:"idle",message:""};
  function text(key){return translations[locale][key];}
  function buttonLabel(id,key){var button=document.getElementById(id);button.title=text(key);button.setAttribute("aria-label",text(key));}
  function render(){
    var terminal=activity.state==="stopped",manual=activity.controller==="user";
    // Stopping capture leaves the last mounted frame valid, but no longer live.
    var capturePaused=capture.status==="stopped";
    var captureBlocked=["blank","suspended","error"].includes(capture.status);
    var placeholder=!mounted||captureBlocked;
    document.getElementById("ph-status").textContent=text(captureBlocked?"activityPreviewUnavailable":capturePaused||terminal||activity.controller==="none"?"activityPreviewPaused":"activityPreviewLoading");
    var retry=document.getElementById("retry");
    retry.textContent=text("activityPreviewRetry");
    retry.hidden=!captureBlocked||terminal||activity.controller==="none";
    ph.classList.toggle("gone",!placeholder);
    ph.classList.toggle("preparing",placeholder&&!captureBlocked&&!capturePaused&&!terminal);
    // States that ask for a decision keep the controls visible.
    var notable=terminal||activity.state==="waiting_confirmation"||
      capturePaused||manual||(activity.controller==="none"&&activity.state!=="starting");
    root.classList.toggle("attention",Boolean(placeholder||notable));
    root.setAttribute("aria-label",text(activity.kind==="cua"?"activityComputer":"activityBrowser")+(label?": "+label:""));
    document.getElementById("expand").hidden=activity.kind!=="browser";
    var labels={close:"activityHide",expand:"activityExpand"};
    Object.keys(labels).forEach(function(id){buttonLabel(id,labels[id]);});
    completion.setAttribute("aria-label",text("activityCompleted"));
  }
  document.addEventListener("keydown",function(e){
    if(e.key==="Escape"){e.preventDefault();window.location.href="wuu-pip://close";}
  });
  var dragAck=true,dragQueued=null,ackTimer=0,grabbing=false,resizing=false,resizeEdge="",last=null,velocity={x:0,y:0};
  document.getElementById("retry").addEventListener("click",function(e){
    e.stopPropagation();
    window.location.href="wuu-pip://retry";
  });
  document.getElementById("close").addEventListener("click",function(e){
    e.stopPropagation();
    window.location.href="wuu-pip://close";
  });
  document.getElementById("expand").addEventListener("click",function(e){
    e.stopPropagation();
    window.location.href="wuu-pip://expand";
  });
  function postDrag(phase,x,y,vx,vy){
    postUrl("wuu-pip://drag?phase="+phase+"&x="+x+"&y="+y+"&vx="+vx+"&vy="+vy);
  }
  function postResize(phase,edge,x,y){
    postUrl("wuu-pip://resize?phase="+phase+"&edge="+edge+"&x="+x+"&y="+y);
  }
  function postUrl(url){
    if(!dragAck){dragQueued=url;return;}
    dragAck=false;
    clearTimeout(ackTimer);
    ackTimer=setTimeout(function(){window.wuuPipDragAck&&window.wuuPipDragAck();},80);
    window.location.href=url;
  }
  window.wuuPipDragAck=function(){
    dragAck=true;
    clearTimeout(ackTimer);
    if(!dragQueued)return;
    var url=dragQueued;dragQueued=null;
    postDragUrl(url);
  };
  function postDragUrl(url){postUrl(url);}
  root.addEventListener("pointerdown",function(e){
    if(e.button!==0||(e.target&&e.target.closest&&(e.target.closest("button")||e.target.closest("[data-resize]"))))return;
    grabbing=true;
    root.classList.add("dragging");
    root.setPointerCapture(e.pointerId);
    last={x:e.screenX,y:e.screenY,t:performance.now()};
    velocity={x:0,y:0};
    postDrag("start",e.screenX,e.screenY,0,0);
  });
  Array.prototype.forEach.call(root.querySelectorAll("[data-resize]"),function(el){
    el.addEventListener("pointerdown",function(e){
      if(e.button!==0)return;
      e.stopPropagation();
      resizing=true;
      grabbing=false;
      resizeEdge=el.getAttribute("data-resize")||"";
      root.classList.add("resizing");
      el.setPointerCapture(e.pointerId);
      postResize("start",resizeEdge,e.screenX,e.screenY);
    });
  });
  root.addEventListener("pointermove",function(e){
    if(resizing){postResize("move",resizeEdge,e.screenX,e.screenY);return;}
    if(!grabbing||!last)return;
    var now=performance.now(),dt=Math.max(1000/240,now-last.t);
    velocity={x:velocity.x*.72+(e.screenX-last.x)/dt*1000*.28,y:velocity.y*.72+(e.screenY-last.y)/dt*1000*.28};
    last={x:e.screenX,y:e.screenY,t:now};
    postDrag("move",e.screenX,e.screenY,velocity.x,velocity.y);
  });
  function endDrag(e){
    if(resizing){
      resizing=false;
      root.classList.remove("resizing");
      postResize("end",resizeEdge,e.screenX,e.screenY);
      return;
    }
    if(!grabbing)return;
    grabbing=false;
    root.classList.remove("dragging");
    var decay=last?Math.pow(.72,Math.max(0,performance.now()-last.t)/(1000/60)):0;
    postDrag("end",e.screenX,e.screenY,velocity.x*decay,velocity.y*decay);
  }
  root.addEventListener("pointerup",endDrag);
  root.addEventListener("pointercancel",endDrag);
  window.wuuPipMount=function(m){
    mounted=!!(m&&m.mounted);
    if(!mounted)cursor.hide();
    render();
  };
  window.wuuPipHost=function(h){
    label=(h&&h.label)||"";
    render();
  };
  window.wuuPipViewport=function(viewport){cursor.setViewport(viewport);};
  window.wuuPipState=function(state){
    if(state.kind!==activity.kind)capture={status:"idle",message:""};
    activity=state;
    document.getElementById("ph-icon").innerHTML=state.kind==="cua"?${JSON.stringify(iconSVG("Monitor", 28))}:${JSON.stringify(iconSVG("Globe", 28))};
    if(state.controller!=="agent"||state.state==="stopped")cursor.hide();
    render();
  };
  window.wuuPipCapture=function(next){capture=next;render();};
  window.wuuPipHover=function(point){
    root.classList.toggle("hovered",!!point);
    ["close","expand"].forEach(function(id){
      var button=document.getElementById(id),r=button.getBoundingClientRect();
      button.classList.toggle("hovered",!!point&&point.x>=r.left&&point.x<r.right&&point.y>=r.top&&point.y<r.bottom);
    });
  };
  window.wuuPipAppearance=function(a){
    var html=document.documentElement;
    html.classList.toggle("dark",a.dark);
    if(typeof a.reducedMotion==="boolean"){
      html.classList.toggle("reduce-motion",a.reducedMotion);cursor.setReducedMotion(a.reducedMotion);
    }
    if(a.fontSize)html.style.setProperty("--ui-size",a.fontSize+"px");
    if(a.fontFamily)document.body.style.fontFamily=a.fontFamily;
    if(a.surface)html.style.setProperty("--surface",a.surface);
    if(a.text)html.style.setProperty("--text",a.text);
    if(a.muted)html.style.setProperty("--muted",a.muted);
    if(translations[a.locale])locale=a.locale;
    html.lang=locale;render();
  };
  window.wuuPipCompleted=function(done){
    if(completion.dataset.completed===String(done))return;
    completion.dataset.completed=String(done);
    completion.setAttribute("aria-hidden",String(!done));
    if(done)cursor.hide();
    render();
  };
  // Appearance stand-in: a content snapshot placed over the live content while
  // the host scales and fades the window. It carries the focus blur.
  var appear=document.getElementById("appear");
  window.wuuPipAppear=function(a){
    if(!a){appear.hidden=true;appear.removeAttribute("src");appear.style.transition="none";appear.style.filter="none";return;}
    if(a.src){
      appear.src=a.src;
      appear.style.left=a.rect.x*100+"%";appear.style.top=a.rect.y*100+"%";
      appear.style.width=a.rect.width*100+"%";appear.style.height=a.rect.height*100+"%";
      appear.style.transition="none";appear.style.filter="blur("+a.blur+"px)";appear.hidden=false;
      return;
    }
    void appear.offsetWidth;
    appear.style.transition="filter "+a.duration+"ms "+a.easing;
    appear.style.filter="blur("+a.blur+"px)";
  };
  window.wuuPipInteract=function(it){
    if(it.kind==="clear"){cursor.hide();return;}
    if(it.kind==="move")return cursor.moveTo(it.x,it.y);
    cursor.feedback(it);
  };
  document.documentElement.lang=locale;
  render();
})();
</script>
</body></html>`;
}
