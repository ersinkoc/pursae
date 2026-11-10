import { expect, it } from 'vitest'
import { createCmsHost, HostStorage, HostUi } from '../../host/cms-host'
import { PageToolsController } from '../../host/page-tools'
import { SiteSettingsController } from '../../host/site-settings'
import type { RestClient } from '../../host/rest'
import { makeBoot, mockFetch, MemoryStorage } from './helpers'
const deferred = () => { let resolve!: (x?: any) => void; let reject!: (x: Error) => void; const promise = new Promise<any>((a,b)=>{resolve=a;reject=b}); return {resolve,reject,promise} }
const clock = () => {const pending=new Map<number,()=>void>();let id=0;return {pending,set:(f:()=>void)=>{pending.set(++id,f);return id},clear:(id:unknown)=>{pending.delete(id as number)}}}
const settings=(blogname:string)=>({blogname,blogdescription:'tag',site_icon:null,custom_logo:null,supports_logo:true,show_on_front:'posts' as const,page_on_front:0,page_for_posts:0,custom_css:'',pages:[],can_upload:true})
function result(actual: unknown,expected:unknown){console.log('EXPECTED:',JSON.stringify(expected),'ACTUAL:',JSON.stringify(actual));const ok=JSON.stringify(actual)===JSON.stringify(expected);console.log(ok?'PROBLEM NOT REPRODUCED':'PROBLEM CONFIRMED');expect(actual).toEqual(expected)}
it('F22 preference flushes persist newest edit',async()=>{
 const controlRest={request:async()=>({data:{}})} as unknown as RestClient;const ct=clock();const control=new HostStorage({rest:controlRest,userId:7,postId:42,ui:new HostUi(ct),local:new MemoryStorage(),timers:ct});control.set('layout-width','control');await control.flushPrefs();expect(control.get('layout-width','')).toBe('control')
 const gate=deferred();let puts=0,remote:unknown;const rest={request:async(m:string,r:string,o?:{json?:any})=>{if(m==='GET')return {data:r.endsWith('/prefs')?{}:[]};if(++puts===1)await gate.promise;remote=o?.json['pursae-builder:layout-width'];return {data:{}}}} as unknown as RestClient
 const timers=clock(),s=new HostStorage({rest,userId:7,postId:42,ui:new HostUi(timers),local:new MemoryStorage(),timers});await s.init();s.set('layout-width','first');const first=s.flushPrefs();s.set('layout-width','latest');const second=s.flushPrefs();gate.resolve();await Promise.all([first,second]);result(remote,'latest');expect(s.get('layout-width','')).toBe('latest')
})
it.each([null,'third'])('concurrent preference writes converge: %s',async(value)=>{const gate=deferred();let calls=0;const remote:Record<string,unknown>={};const rest={request:async(m:string,r:string,o?:{json?:any})=>{if(m==='GET')return {data:r.endsWith('/prefs')?{}:[]};if(++calls===1)await gate.promise;Object.assign(remote,o?.json);return {data:{}}}} as unknown as RestClient;const timers=clock();const s=new HostStorage({rest,userId:7,postId:42,ui:new HostUi(timers),local:new MemoryStorage(),timers});await s.init();s.set('layout-width','one');const a=s.flushPrefs();s.set('layout-width','two');const b=s.flushPrefs();s.set('layout-width',value);const c=s.flushPrefs();gate.resolve();await Promise.all([a,b,c]);expect(remote['pursae-builder:layout-width']).toBe(value);expect(calls).toBe(2);await s.flushPrefs();expect(calls).toBe(2)})
