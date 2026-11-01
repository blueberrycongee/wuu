import type { RuntimeHost, ServiceRequirement } from './index.js';
import type { PrincipalIdentity } from './identity.js';

/** Enrollment is approved by the authenticated local owner. Neither a source
 * event nor possession of this ID establishes owner authority by itself. */
export interface PersonalChannelBinding {
  readonly platform: 'slack' | 'feishu';
  readonly tenant_id: string;
  readonly sender_id: string;
  readonly conversation_id: string;
  readonly enrollment_id: string;
}
export interface PersonalChannelSource {
  readonly platform: string;
  readonly app_id: string;
  readonly tenant_id: string;
  readonly sender_id: string;
  readonly conversation_id: string;
  readonly thread_id: string;
  readonly message_id: string;
  readonly event_id: string;
  readonly text: string;
  readonly attachments?: readonly {readonly id:string;readonly message_id?:string;readonly kind?:string;readonly name:string;readonly mime_type:string;readonly url?:string;readonly size:number}[];
}
export interface PersonalOwnerEnrollment {
  readonly id: string;
  readonly principal: PrincipalIdentity;
  readonly user_space_id: string;
  readonly product_domain: 'personal';
  readonly conversation_id: string;
  readonly revoked: boolean;
}
export function requirePersonalMessagingChannel(): ServiceRequirement[] {
  return ['host.personal','host.execution.authorization'].map(name=>({name,major_version:1,required:true}));
}

/** For the CURRENT selected personal.messaging provider only. The adapter must
 * authenticate each native event and retain its original channel_source. The
 * host matches the immutable enrollment, native event/request identity and
 * literal text before admitting a Personal user input. Other plugins cannot
 * manufacture this admission by copying a binding or setting owner=true. */
export function personalMessagingChannel(host: RuntimeHost) {
  const owner = (operation:string, params:unknown) => host.call('host.service.call', {
    service:'host.execution.authorization',method:'call',params:{operation,params},
  }) as Promise<PersonalOwnerEnrollment>;
  return {
    enrollOwner: (input: PersonalOwnerEnrollment) => owner('enroll-owner',input),
    readOwner: (id:string) => owner('read-owner',{id}),
    revokeOwner: (id:string) => owner('revoke-owner',{id}),
    async call<T=unknown>(binding:PersonalChannelBinding, method:string, input:Record<string,unknown> & {channel_source?:PersonalChannelSource}):Promise<T> {
      const result = await host.call('host.service.call',{service:'host.personal',method:'call',params:{operation:'channel.call',input:{binding,method,input}}}) as {ok:boolean;value?:T;error?:{message?:string}};
      if (!result?.ok || result.value === undefined) throw new Error(result?.error?.message || 'Personal channel admission failed');
      return result.value;
    },
  };
}
