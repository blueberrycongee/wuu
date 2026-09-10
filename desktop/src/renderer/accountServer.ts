// Official builds inject their account origin. Community builds need no Wuu cloud.
declare const __WUU_ACCOUNT_SERVER__: string;
export const defaultAccountServer = typeof __WUU_ACCOUNT_SERVER__ === 'string' ? __WUU_ACCOUNT_SERVER__ : '';
