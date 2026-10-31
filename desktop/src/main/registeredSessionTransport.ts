import { randomUUID } from 'node:crypto';
import type { ServerEvent } from '../shared/protocol';
import { AppServerClientPool, type BoundAppServerRequest } from './appServerClients';
import { requestTrustedRemoteOnConnection, trustedRemotePeer, type TrustedRemotePeer } from './trustedRemoteTransport';
import { validateSessionRoute, type RegisteredSessionTargetsV1, type SessionRouteRequest, type SessionRouteResult } from '../../../packages/plugin-sdk/src/session-transport';

export const REGISTERED_SESSION_REVERSE_METHODS = ['session/transport.dispatch', 'session/transport.list-targets', 'session/transport.release'];
function record(value:unknown):value is Record<string,unknown>{return value!==null&&typeof value==='object'&&!Array.isArray(value);}
function unknown(request:SessionRouteRequest):SessionRouteResult{return {version:1,operation_id:request.operation_id,locator:{...request.locator},status:'unknown',error:{code:'session_delivery_unknown',message:'Delivery may have been accepted; reconcile the same operation ID'}};}

/** Source authority arrives only from a negotiated, existing local core. The
 * target is an already registered receiver with its own federation policy. */
export class RegisteredSessionTransport {
 private readonly peers = new Map<string, {source:ServerEvent; release:()=>Promise<void>; releases:Map<BoundAppServerRequest,()=>Promise<void>>} >();
 constructor(private readonly pool:AppServerClientPool){}
 handleConnectionEvent(event:ServerEvent):void{
  if(event.kind!=="server-exit"&&event.kind!=="connection-reset")return;
  for(const owned of this.peers.values())if(this.pool.sameEventSource(owned.source,event))void owned.release().catch(()=>{});
 }
 private async releasePeer(id:string,source:ServerEvent):Promise<void>{
  const owned=this.peers.get(id);if(!owned)return;
  if(!this.pool.sameEventSource(owned.source,source))throw new Error('Transport peer belongs to a different source connection');
  await owned.release();
 }

 async handleServerRequest(event:ServerEvent):Promise<void>{
  if(event.kind!=='server-request')throw new Error('Session transport requires a source request');
  const id=event.message.id;
  try{
   this.pool.bindEventRequest(event,{localOnly:true});
   const input=event.message.params;
   if(event.message.method==='session/transport.release'){
    if(!record(input)||typeof input.peer_id!=='string')throw new Error('Invalid transport release');
    await this.releasePeer(input.peer_id,event);this.pool.respondToServerRequest(id,{released:true});return;
   }
   if(!record(input)||input.version!==1||!trustedRemotePeer(input.peer)||input.peer.owner)throw new Error('Authenticated nonowner source transport required');
   const result=event.message.method==='session/transport.dispatch'
    ?await this.dispatch(input.peer,input.request as SessionRouteRequest,event)
    :event.message.method==='session/transport.list-targets'?await this.listTargets(input.peer):undefined;
   if(result===undefined)throw new Error('Unsupported session transport operation');
   this.pool.respondToServerRequest(id,result);
  }catch(error){this.pool.rejectServerRequest(id,error instanceof Error?error.message:'Session transport unavailable');}
 }
 private async call(call:BoundAppServerRequest,peer:TrustedRemotePeer,request:SessionRouteRequest,source?:ServerEvent):Promise<SessionRouteResult>{
  let readCleanup:(()=>Promise<void>)|undefined;
  try{
   let owned=source?this.peers.get(peer.peer_id):undefined;
   if(source && owned && !this.pool.sameEventSource(owned.source,source))throw new Error('Transport peer source changed');
   if(source && !owned){
    let forget=()=>{};
    const entry={source,releases:new Map<BoundAppServerRequest,()=>Promise<void>>(),release:async()=>{if(this.peers.get(peer.peer_id)!==entry)return;this.peers.delete(peer.peer_id);forget();await Promise.allSettled([...entry.releases.values()].map(release=>release()));entry.releases.clear();}};
    this.peers.set(peer.peer_id,entry);forget=this.pool.ownEventCleanup(source,entry.release);owned=entry;
   }
   const response=await requestTrustedRemoteOnConnection(this.pool,call,peer,'session/federation',request,undefined,owned?(release,forget)=>{if(owned!.releases.has(call))forget();else owned!.releases.set(call,release);}:(release)=>{readCleanup=release;});
   await response.receipt?.check();

   if(response.error)return {version:1,operation_id:request.operation_id,locator:{...request.locator},status:'completed',error:{code:response.error.code??'session_request_denied',message:response.error.message}};
   const value=response.result;
   if(!record(value)||value.version!==1||value.operation_id!==request.operation_id||!record(value.locator)||value.locator.execution_host!==request.locator.execution_host||value.locator.session_id!==request.locator.session_id||!['completed','pending','unknown'].includes(String(value.status)))return unknown(request);
   return value as unknown as SessionRouteResult;
  }catch{return unknown(request);}finally{await readCleanup?.().catch(()=>{});}
 }
 async dispatch(peer:TrustedRemotePeer,request:SessionRouteRequest,source?:ServerEvent):Promise<SessionRouteResult>{
  validateSessionRoute(request);
  if(!trustedRemotePeer(peer)||peer.owner||!['send','inspect','cancel'].includes(request.operation)||request.locator.execution_host==='local')throw new Error('Bound existing-session transport required');
  const matches=this.pool.registeredSessionReceivers().filter(receiver=>receiver.locator?.execution_host===request.locator.execution_host);
  if(matches.length!==1)return {version:1,operation_id:request.operation_id,locator:request.locator,status:'completed',error:{code:'session_host_unavailable',message:'Exact registered session receiver is unavailable or ambiguous'}};
  const target=matches[0];
  if(request.locator.storage_host && request.locator.storage_host!==target.locator?.storage_host)return {version:1,operation_id:request.operation_id,locator:request.locator,status:'completed',error:{code:'session_host_unavailable',message:'Registered storage authority differs'}};
  return this.call(target.request,peer,request,source);
 }
 async listTargets(peer:TrustedRemotePeer):Promise<RegisteredSessionTargetsV1>{
  if(!trustedRemotePeer(peer)||peer.owner)throw new Error('Authenticated nonowner source transport required');
  const result:RegisteredSessionTargetsV1={version:1,targets:[],unavailable:[]};
  const receivers=this.pool.registeredSessionReceivers();
  for(const receiver of receivers){
   const locator=receiver.locator;
   if(!locator){result.unavailable.push({reason:'Receiver federation is not configured'});continue;}
   if(receivers.filter(candidate=>candidate.locator?.execution_host===locator.execution_host).length!==1){result.unavailable.push({execution_host:locator.execution_host,reason:'Receiver identity is ambiguous'});continue;}
   const receipt=await this.call(receiver.request,peer,{version:1,operation:'list',operation_id:randomUUID(),locator,params:{scope:'shared'}});
   if(receipt.status!=='completed'||receipt.error||!record(receipt.result)||!Array.isArray(receipt.result.sessions)){result.unavailable.push({execution_host:locator.execution_host,reason:'Receiver is unavailable or its policy does not permit discovery'});continue;}
   for(const session of receipt.result.sessions){if(record(session)&&typeof session.session_id==='string'){result.targets.push({locator:{...locator,session_id:session.session_id},...(typeof session.name==='string'?{title:session.name}:{})});}}
  }
  return result;
 }
}
