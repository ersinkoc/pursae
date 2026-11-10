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
it('F19 dispose during startup does not create heartbeat', async()=>{
 const normalClock=clock(), normal=createCmsHost(makeBoot(),{fetch:mockFetch({'GET web-builder/v2/prefs':{json:{}},'GET web-builder/v2/library':{json:[]}}).fetch,local:null,timers:normalClock});await normal.start();normal.dispose();expect(normalClock.pending.size).toBe(0)
 const gate=deferred(),timers=clock();const m=mockFetch({'GET web-builder/v2/prefs':async()=>{await gate.promise;return {json:{}}},'GET web-builder/v2/library':{json:[]}})
 const host=createCmsHost(makeBoot(),{fetch:m.fetch,local:null,timers});const loading=host.start();host.dispose();gate.resolve();await loading;result(timers.pending.size,0)
})
it.each(['success','failure'])('ignores lock completion after disposal: %s',async(mode)=>{const gate=deferred();const timers=clock();const m=mockFetch({'POST web-builder/v2/documents/42/lock':async()=>{await gate.promise;if(mode==='failure')return {status:409,json:{code:'wb_api_mismatch',message:'mismatch'}};return {json:{lock:{user:{name:'late'}}}}}});const host=createCmsHost(makeBoot(),{fetch:m.fetch,local:null,timers});const lock=host.refreshLock();host.dispose();host.dispose();gate.resolve();expect(await lock).toBeNull();await host.start();expect(timers.pending.size).toBe(0);expect(host.ui.getState().notices).toEqual([])})
