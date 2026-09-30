import { type AXNode, type Send, property } from "./metadata.ts";

export function signature(node: AXNode): string {
  return JSON.stringify([node.role?.value ?? null, node.name?.value ?? null, node.value?.value ?? null,
    ...["checked", "selected", "expanded", "disabled", "readonly", "url"].map(key => property(node, key) ?? null)]);
}
const viewportText = `(() => {
  if(!document.body)return '';
  const walker=document.createTreeWalker(document.body,NodeFilter.SHOW_TEXT),range=document.createRange();
  const parts=[];let node,total=0,visited=0;
  while((node=walker.nextNode())&&visited++<5000&&total<4000){
    const text=node.textContent.trim(),parent=node.parentElement;
    if(!text||!parent||parent.closest('script,style,noscript,template,input,textarea,select,[contenteditable],[role="textbox"],[role="combobox"]')||!parent.checkVisibility({checkOpacity:true,checkVisibilityCSS:true}))continue;
    range.selectNodeContents(node);const r=range.getBoundingClientRect();
    if(r.width&&r.height&&r.bottom>0&&r.top<innerHeight&&r.right>0&&r.left<innerWidth){parts.push(text);total+=text.length;}
  }
  return parts.join('\\n').slice(0,4000);
})()`;
export async function pageState(send: Send, includeText = false) {
  const expression="({documentKey:String(performance.timeOrigin)+'|'+location.href,documentURL:location.href,title:document.title,readyState:document.readyState,scrollY:scrollY,scrollMax:Math.max(0,document.documentElement.scrollHeight-innerHeight),viewportHeight:innerHeight"+(includeText?",text:"+viewportText:"")+"})";
  const r = await send("Runtime.evaluate", { expression, returnByValue: true }) as { result?: { value?: Record<string, unknown> }; exceptionDetails?: unknown };
  if (r.exceptionDetails || typeof r.result?.value?.documentKey !== "string") throw new Error("STALE_SNAPSHOT: document is not ready");
  return r.result.value;
}

export async function onNode<T>(send: Send, backendNodeId: number, declaration: string, values: unknown[] = []): Promise<T> {
  const r = await send("DOM.resolveNode", { backendNodeId }) as { object?: { objectId?: string } };
  const objectId = r.object?.objectId;
  if (!objectId) throw new Error("STALE_SNAPSHOT: target no longer exists");
  try {
    const result = await send("Runtime.callFunctionOn", { objectId, functionDeclaration: declaration, arguments: values.map(value => ({ value })), returnByValue: true, silent: true }) as { result?: { value?: T }; exceptionDetails?: unknown };
    if (result.exceptionDetails || result.result?.value === undefined) throw new Error("STALE_SNAPSHOT: target observation was interrupted");
    return result.result.value;
  } finally { await send("Runtime.releaseObject", { objectId }).catch(() => {}); }
}
export const hitPointSource = `function(e) {
  if(e?.nodeType===3)e=e.parentElement;
  if(!e?.isConnected||e.matches(':disabled')||e.closest('[aria-disabled="true"],[inert],[aria-hidden="true"]'))return null;
  const interactive='a[href],button,input,textarea,select,[role="button"],[role="link"],[role="checkbox"],[role="radio"],[role="switch"],[role="tab"],[role="menuitem"],[role="option"],[role="gridcell"],[role="combobox"],[role="textbox"],[role="searchbox"]';
  const candidates=[e,...e.querySelectorAll('h1,h2,h3,span,img,svg')].slice(0,16);let probes=0;
  for(const candidate of candidates){
    if(!candidate.checkVisibility({checkOpacity:true,checkVisibilityCSS:true}))continue;
    for(const r of [...candidate.getClientRects()].slice(0,8)){
      if(!r.width||!r.height)continue;
      for(const [fx,fy] of [[0.5,0.5],[0.25,0.25],[0.75,0.75],[0.1,0.5],[0.5,0.8]]){
        if(++probes>64)return null;
        const x=r.x+r.width*fx,y=r.y+r.height*fy;
        if(x<0||y<0||x>=innerWidth||y>=innerHeight)continue;
        const hit=document.elementFromPoint(x,y);if(!hit||!e.contains(hit))continue;
        const owner=hit.closest(interactive);
        if(owner&&owner!==e&&e.contains(owner))continue;
        return {x,y};
      }
    }
  }
  return null;
}`;
export type SelectOption = { index: number; label: string; value: string; selected: boolean; disabled: boolean };
export type ControlDetails = {
  visible: boolean; occluded?: boolean; editable: boolean; inputType: string; disabled: boolean;
  href?: string; opensNewTab?: boolean; checked?: boolean; selected?: string;
  options?: SelectOption[]; optionsTruncated?: boolean;
};
export async function controlDetails(send: Send, backendNodeId: number): Promise<ControlDetails> {
  return onNode<ControlDetails>(send, backendNodeId, `function() {
    const e=this, r=e.getBoundingClientRect();
    const disabled=e.matches(':disabled')||Boolean(e.closest('[aria-disabled="true"],[inert]'));
    const visible=e.isConnected && !e.closest('[aria-hidden="true"]') && e.checkVisibility({checkOpacity:true,checkVisibilityCSS:true}) && r.width>0&&r.height>0&&r.bottom>0&&r.top<innerHeight&&r.right>0&&r.left<innerWidth;
    const tag=e.tagName, type=tag==='TEXTAREA'?'textarea':tag==='SELECT'?'select':tag==='INPUT'?(e.type||'text'):'unknown';
    const editable=['text','search','email','tel','url','textarea'].includes(type)&&!disabled&&!e.readOnly&&e.getAttribute('aria-readonly')!=='true';
    const point=(${hitPointSource})(e);
    const occluded=visible&&!point;
    const result={visible:Boolean(point),occluded,disabled,editable,inputType:type};
    if(tag==='A'){result.href=e.href;result.opensNewTab=e.target==='_blank';}
    if(['checkbox','radio'].includes(e.type))result.checked=Boolean(e.checked);
    if(tag==='SELECT'){
      result.options=[...e.options].slice(0,64).map((o,index)=>({index,label:o.label,value:o.value,selected:o.selected,disabled:o.disabled||Boolean(o.closest('optgroup[disabled]'))}));
      result.optionsTruncated=e.options.length>64;result.selected=e.value;
    }
    return result;
  }`);
}
export async function assertGuard(send: Send, backendNodeId: number, documentKey: string, expected: string) {
  if ((await pageState(send)).documentKey !== documentKey) throw new Error("STALE_SNAPSHOT: document changed before execution");
  let result: { nodes?: AXNode[] };
  try { result = await send("Accessibility.getPartialAXTree", { backendNodeId, fetchRelatives: false }) as typeof result; }
  catch { throw new Error("STALE_SNAPSHOT: observed target was removed"); }
  const node = result.nodes?.find(n => n.backendDOMNodeId === backendNodeId);
  if (!node || node.ignored || signature(node) !== expected) throw new Error("STALE_SNAPSHOT: target meaning or value changed before execution");
}
export async function hitPoint(send: Send, backendNodeId: number): Promise<{ x: number; y: number }> {
  const point = await onNode<{ x: number; y: number } | null>(send, backendNodeId, `function() { return (${hitPointSource})(this); }`);
  if (!point) throw new Error("STALE_SNAPSHOT: target is hidden, moved out of view, or covered");
  return point;
}
export async function selectObserved(send: Send, backendNodeId: number, option: SelectOption): Promise<{ verified: boolean }> {
  await hitPoint(send, backendNodeId);
  try {
    const result = await onNode<{ stale?: boolean; verified?: boolean }>(send, backendNodeId, `function(option) {
      const e=this,o=e.options?.[option.index];
      if(e.tagName!=='SELECT'||e.disabled||!o||o.disabled||o.closest('optgroup[disabled]')||o.value!==option.value||o.label!==option.label)return {stale:true};
      e.selectedIndex=option.index;e.dispatchEvent(new Event('input',{bubbles:true}));e.dispatchEvent(new Event('change',{bubbles:true}));
      return {verified:e.selectedIndex===option.index&&e.value===option.value};
    }`, [option]);
    if (result.stale) throw new Error("STALE_SNAPSHOT: dropdown option changed before execution");
    return { verified: result.verified === true };
  } catch (error) {
    if (String(error).includes("dropdown option changed before execution")) throw error;
    throw new Error("OUTCOME_UNKNOWN: dropdown mutation was interrupted; inspect before continuing");
  }
}
