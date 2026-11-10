import { expect, it } from 'vitest'
import { ThemeLibraryController, type ThemesResponse } from '../../host/theme-library'
import type { RestClient } from '../../host/rest'
const deferred=()=>{let resolve!:(x:ThemesResponse)=>void;let reject!:(e:Error)=>void;const promise=new Promise<ThemesResponse>((a,b)=>{resolve=a;reject=b});return{promise,resolve,reject}}
const data=(name:string):ThemesResponse=>({themes:[{slug:name,name,description:'',version:'1',screenshot:'',templates:[],pages:[],menus:0,images:0,store:false,imported:false}],existing:{},source:''})
it.each(['old-success','old-error'])('ignores stale theme-library completion: %s',async(mode)=>{
	const old=deferred(),newer=deferred();let calls=0;const c=new ThemeLibraryController({rest:{request:async()=>({data:await(++calls===1?old.promise:newer.promise)})} as unknown as RestClient,boot:{links:{site:'https://example.test'},rendererVersion:1,rest:{root:'/wp-json/'}}})
	const a=c.load(),b=c.load();newer.resolve(data('new'));await b;const current=c.getState();if(mode==='old-error')old.reject(new Error('old failed'));else old.resolve(data('old'));await a;expect(c.getState()).toBe(current);expect(c.getState().data?.themes[0].name).toBe('new');expect(c.getState().loading).toBe(false)
})
