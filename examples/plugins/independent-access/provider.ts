/** Independent public-contract acceptance provider. This pinned-JWK JWT fixture
 * is not a production OIDC discovery, login, key rotation or SAML implementation. */
import {readFile} from 'node:fs/promises';
import {createPublicKey, verify, type JsonWebKey} from 'node:crypto';
import {
 identityAuthenticationDescriptor, resourceAuthorizationProvider,
 type AuthenticationCredential, type AuthenticatedIdentity, type PrincipalIdentity,
 type ResourceAuthorizationRequest, type RuntimePlugin,
} from '@wuu/plugin-sdk';

interface State {
 fail_policy?: boolean;
 revoked_tokens?: string[];
 grants: {principal: PrincipalIdentity; actions: string[]; resources: {kind: string; id?: string}[]; permission_modes?: string[]; models?: string[]; model_providers?: string[]; tool_ids?: string[]}[];
}
export function createAccessProvider(options:{issuer:string; jwksPath:string; statePath:string}):RuntimePlugin {
 const state=async()=>JSON.parse(await readFile(options.statePath,'utf8')) as State;
 const authorize=resourceAuthorizationProvider(async(request:ResourceAuthorizationRequest)=>{
  const current=await state();
  if(current.fail_policy)throw new Error('fixture policy unavailable');
  if(request.owner)return {allow:true};
  const identity=request.principal;
  const matching=current.grants.filter(grant=>grant.principal.kind===identity.kind&&grant.principal.issuer===identity.issuer&&grant.principal.tenant===identity.tenant&&grant.principal.subject===identity.subject&&grant.actions.includes(request.action));
  const mode=request.attributes?.permission_mode;
  const model=request.attributes?.model, provider=request.attributes?.provider, tools=request.attributes?.tool_allow;
  return {allow:request.resources.every(resource=>matching.some(grant=>
   grant.resources.some(candidate=>candidate.kind===resource.kind&&(candidate.id??'')===(resource.id??''))&&
   (!grant.permission_modes||(typeof mode==='string'&&grant.permission_modes.includes(mode)))&&
   (!grant.models||(typeof model==='string'&&grant.models.includes(model)))&&
   (!grant.model_providers||(typeof provider==='string'&&grant.model_providers.includes(provider)))&&
   (!grant.tool_ids||(Array.isArray(tools)&&tools.every(tool=>typeof tool==='string'&&grant.tool_ids!.includes(tool))))
  )),reason:'no-matching-grant'};
 });
 let active=false;
 return {
  initialize:()=>({protocol_version:3,lifecycle_version:2,provided_services:[identityAuthenticationDescriptor(),authorize.descriptor]}),
  activate(){active=true;},shutdown(){active=false;},
  async invokeService(request) {
   if(!active)throw new Error('fixture provider is not active');
   if(request.service===authorize.descriptor.name)return authorize.invoke(request);
   if(request.service!=='identity.authentication'||request.method!=='authenticate')throw new Error('unknown access service');
   const credential=request.params as AuthenticationCredential;
   if(credential.scheme!=='Bearer'||!credential.audience||typeof credential.token!=='string')throw new Error('invalid credential');
   const parts=credential.token.split('.');if(parts.length!==3)throw new Error('invalid signed token');
   const header=JSON.parse(Buffer.from(parts[0]!, 'base64url').toString('utf8')) as {alg?:string;kid?:string};
   if(header.alg!=='EdDSA'||typeof header.kid!=='string')throw new Error('unsupported token');
   const jwks=JSON.parse(await readFile(options.jwksPath,'utf8')) as {keys:(JsonWebKey&{kid:string})[]};
   const jwk=jwks.keys.find(key=>key.kid===header.kid&&key.kty==='OKP'&&key.crv==='Ed25519');
   if(!jwk||!verify(null,Buffer.from(parts[0]+'.'+parts[1]),createPublicKey({key:jwk,format:'jwk'}),Buffer.from(parts[2]!, 'base64url')))throw new Error('invalid signature');
   const claims=JSON.parse(Buffer.from(parts[1]!, 'base64url').toString('utf8')) as {iss?:string;aud?:string|string[];sub?:string;tenant?:string;kind?:string;exp?:number;nbf?:number;jti?:string};
   const now=Date.now()/1000;
   if(claims.iss!==options.issuer||!(Array.isArray(claims.aud)?claims.aud.includes(credential.audience):claims.aud===credential.audience)||!Number.isFinite(claims.exp)||claims.exp!<=now||claims.nbf!==undefined&&claims.nbf>now||typeof claims.sub!=='string'||!claims.sub||typeof claims.tenant!=='string'||!claims.tenant||!['user','device','agent'].includes(claims.kind??'')||typeof claims.jti!=='string'||(await state()).revoked_tokens?.includes(claims.jti))throw new Error('invalid or revoked claims');
   return {identity:{kind:claims.kind as PrincipalIdentity['kind'],issuer:claims.iss,tenant:claims.tenant,subject:claims.sub},expires_at:new Date(claims.exp!*1000).toISOString()} satisfies AuthenticatedIdentity;
  },
 };
}
