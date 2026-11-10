import { expect, it } from 'vitest'
import { TemplateController, readTemplate } from '../../host/template'
import { DynStore } from '../../host/dynamic'
import type { RestClient } from '../../host/rest'
import type { TemplateConflict } from '../../host/contract'
import { makeTemplateBoot } from './template-fixtures'
const deferred=()=>{let resolve!:(x:TemplateConflict[])=>void;let reject!:(e:Error)=>void;const promise=new Promise<TemplateConflict[]>((a,b)=>{resolve=a;reject=b});return{promise,resolve,reject}}
const conflict=(id:number):TemplateConflict=>({id,title:`T${id}`,type:'single-post',conditions:['include/general'],priority:0,editUrl:`/edit/${id}`})
it.each(['old-success','old-error'])('ignores stale conflicts completion: %s',async(mode)=>{const old=deferred(),newer=deferred();let calls=0;const rest={request:async()=>({data:await(++calls===1?old.promise:newer.promise)})} as unknown as RestClient;const boot=makeTemplateBoot();const c=new TemplateController(rest,new DynStore(rest,boot),boot,readTemplate(boot)!);const a=c.loadConflicts(),b=c.loadConflicts();newer.resolve([conflict(2)]);await b;const current=c.getState();if(mode==='old-error')old.reject(new Error('old'));else old.resolve([conflict(1)]);await a;expect(c.getState()).toBe(current);expect(c.getState().conflicts?.[0].id).toBe(2)})
