import assert from 'node:assert/strict';
import test from 'node:test';
import { assertGuard, signature, hitPoint, selectObserved } from './guards.ts';
import { type Send } from './metadata.ts';

test('semantic guard rejects changed field values before any input', async () => {
  const observed={backendDOMNodeId:7,role:{value:'textbox'},name:{value:'City'},value:{value:'Lisbon'}};
  const send:Send=async method=>{
    if(method==='Runtime.evaluate')return {result:{value:{documentKey:'same-document'}}};
    if(method==='Accessibility.getPartialAXTree')return {nodes:[{...observed,value:{value:'Porto'}}]};
    throw new Error('unexpected mutation');
  };
  await assert.rejects(assertGuard(send,7,'same-document',signature(observed)),/STALE_SNAPSHOT/);
});

test('semantic guard tolerates unrelated page changes when target meaning is stable', async () => {
  const target={backendDOMNodeId:7,role:{value:'button'},name:{value:'Search'}};
  const send:Send=async method=>method==='Runtime.evaluate'?{result:{value:{documentKey:'same-document'}}}:{nodes:[target]};
  await assertGuard(send,7,'same-document',signature(target));
  await assert.rejects(assertGuard(send,7,'old-document',signature(target)),/document changed/);
});

test('covered controls are rejected before click coordinates are used', async () => {
  const send:Send=async method=>{
    if(method==='DOM.resolveNode')return {object:{objectId:'node'}};
    if(method==='Runtime.callFunctionOn')return {result:{value:null}};
    return {};
  };
  await assert.rejects(hitPoint(send,7),/covered/);
});

test('interrupted dropdown mutation is not reported as retryable stale state', async () => {
  let calls=0;
  const send:Send=async method=>{
    if(method==='DOM.resolveNode')return {object:{objectId:'node'}};
    if(method==='Runtime.callFunctionOn'){
      if(++calls===1)return {result:{value:{x:10,y:10}}};
      throw new Error('execution context destroyed');
    }
    return {};
  };
  await assert.rejects(selectObserved(send,7,{index:1,label:'Design',value:'design',selected:false,disabled:false}),/OUTCOME_UNKNOWN/);
  assert.equal(calls,2);
});

test('hit testing finds a valid fragment without clicking a nested secondary link', async () => {
  const { hitPointSource } = await import('./guards.ts');
  const { runInNewContext } = await import('node:vm');
  const nested={closest:()=>nested};
  const link={isConnected:true,matches:()=>false,closest:()=>link,checkVisibility:()=>true,getClientRects:()=>[{x:0,y:0,width:100,height:40}],querySelectorAll:()=>[],contains:(n:unknown)=>n===link||n===nested};
  const closest=link.closest;
  link.closest=(selector?:string)=>selector?.startsWith('[aria-disabled')?null as unknown as typeof link:closest();
  const pick=runInNewContext('('+hitPointSource+')',{innerWidth:200,innerHeight:100,document:{elementFromPoint:(x:number,y:number)=>x===50&&y===20?nested:link}});
  const point=pick(link);
  assert.ok(point && !(point.x===50&&point.y===20));
});

test('hit testing supports a boxless link with a visible child', async () => {
  const { hitPointSource } = await import('./guards.ts');
  const { runInNewContext } = await import('node:vm');
  const link={isConnected:true,matches:()=>false,closest:()=>null,checkVisibility:()=>false,getClientRects:()=>[],querySelectorAll:()=>[child],contains:(n:unknown)=>n===child};
  const child={checkVisibility:()=>true,getClientRects:()=>[{x:10,y:10,width:100,height:30}],closest:()=>link};
  const pick=runInNewContext('('+hitPointSource+')',{innerWidth:200,innerHeight:100,document:{elementFromPoint:()=>child}});
  assert.ok(pick(link));
});
