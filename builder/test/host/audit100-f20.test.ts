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
it('F20 failed SEO refresh preserves new draft',async()=>{
 const gate=deferred();let calls=0;const rest={request:async (_m:string,r:string)=>({data:r.endsWith('/plugins')?{seo:true,seoEdit:true,seoAdvanced:true}:++calls===1?{id:42,fields:{title:'old'}}:await gate.promise})} as unknown as RestClient
 const c=new PageToolsController({rest,postId:42});await c.probe();await c.loadSeo();expect(c.getState().seo.draft?.title).toBe('old');const load=c.loadSeo();c.editSeo({title:'new'});gate.reject(new Error('offline'));await load;result(c.getState().seo.draft?.title,'new')
})
it.each(['success','failure'])('SEO refresh edge: %s',async(mode)=>{const gate=deferred();let n=0;const c=new PageToolsController({postId:42,rest:{request:async(_m:string,r:string)=>({data:r.endsWith('/plugins')?{seo:true,seoEdit:true}:++n===1?{id:42,fields:{title:'base'}}:await gate.promise})} as unknown as RestClient});await c.probe();await c.loadSeo();const load=c.loadSeo();if(mode==='success'){c.editSeo({title:''});gate.resolve({id:42,fields:{title:'remote'}})}else gate.reject(new Error('offline'));await load;if(mode==='success')expect(c.getState().seo.draft?.title).toBe('');else expect(c.getState().seo.load).toBe('error')})
