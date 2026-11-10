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
it('F21 settings refresh preserves mid-flight edits',async()=>{
 const gate=deferred();let calls=0;const rest={request:async()=>({data:++calls===1?settings('old'):await gate.promise})} as unknown as RestClient
 const c=new SiteSettingsController({rest});await c.load();expect(c.getState().draft?.blogname).toBe('old');const load=c.load();c.edit({blogname:'new'});gate.resolve(settings('remote'));await load;result(c.getState().draft?.blogname,'new')
})
it.each(['success','failure'])('settings preserves edited fields on refresh %s',async(mode)=>{const gate=deferred();let n=0;const c=new SiteSettingsController({rest:{request:async()=>({data:++n===1?settings('old'):await gate.promise})} as unknown as RestClient});await c.load();const load=c.load();c.edit({blogname:''});if(mode==='success')gate.resolve({...settings('remote'),blogdescription:'new tag'});else gate.reject(new Error('offline'));await load;expect(c.getState().draft?.blogname).toBe('');expect(c.dirty).toBe(true);if(mode==='success')expect(c.getState().draft?.blogdescription).toBe('new tag');else expect(c.getState().error).toContain('offline')})
