import {TITLE_SERVICE_DESCRIPTOR, RuntimeHostServiceError, type RuntimePlugin, type TitlePlan} from '@wuu/plugin-sdk';

export const independentTitle = 'independent-title: complete policy output survives the native forty-character limit';

/** Direct title policy: no model inference and no inherited output cleanup. */
export function createTitleProvider(): RuntimePlugin {
  let active = false;
  return {
    initialize() {return {protocol_version:3,provided_services:[TITLE_SERVICE_DESCRIPTOR]};},
    activate() {active = true;},
    invokeService({service,method,params}) {
      if (!active) throw new RuntimeHostServiceError({code:'service_unavailable',message:'title provider is not active'});
      if (service !== TITLE_SERVICE_DESCRIPTOR.name) throw new RuntimeHostServiceError({code:'method_not_found',message:'unknown title service'});
      if (method === 'plan') return {action:'complete',title:independentTitle} satisfies TitlePlan;
      if (method === 'resolve') {
        const {text} = params as {text:string};
        if (typeof text !== 'string') throw new Error('title text is required');
        return {title:`independent-resolved:${text}`};
      }
      throw new RuntimeHostServiceError({code:'method_not_found',message:'unknown title method'});
    },
    shutdown() {active = false;},
  };
}
export default createTitleProvider();
