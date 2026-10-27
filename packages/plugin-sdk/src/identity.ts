import type { ServiceDescriptor, ServiceRequirement } from "./index.js";

/** Namespace-qualified identity claims are not proof of authentication. Only the
 * host's configured identity provider can establish a connection principal. */
export interface PrincipalIdentity {
 readonly kind: "user" | "device" | "agent";
 readonly issuer: string;
 readonly tenant: string;
 readonly subject: string;
}
/** Transport-supplied input to identity.authentication@1. Audience is configured
 * by the receiving host, never selected by the untrusted handshake. */
export interface AuthenticationCredential {
 readonly scheme: string;
 readonly token: string;
 readonly audience: string;
}
export interface AuthenticatedIdentity {
 readonly identity: PrincipalIdentity;
 /** RFC3339 expiry; required and finite. The host reauthenticates the original
  * credential for requests and output/replay and rejects identity changes. */
 readonly expires_at: string;
}
export const AUTHENTICATION_SERVICE = "identity.authentication";
export function identityAuthenticationDescriptor(): ServiceDescriptor {
 return {name:AUTHENTICATION_SERVICE,version:"1.0.0",methods:[{name:"authenticate",input_schema:"identity.authentication.credential.v1",output_schema:"identity.authentication.result.v1"}]};
}
export function requireIdentityAuthentication(): ServiceRequirement {
 return {name:AUTHENTICATION_SERVICE,major_version:1,required:true};
}
/** First frame for `wuu app-server --authentication=provider
 * --authentication-audience=<host audience>`. Send over the private JSONL input,
 * not argv, logs or a browser URL. The receiving process must already have been
 * configured in provider mode; no wire request can enable local-owner mode.
 * Identity providers must verify the credential's signature, issuer, audience,
 * expiry and revocation as applicable. Decoding a token is not verification. */
export function authenticationHandshake(scheme: string, credential: string, id: string | number = "authentication") {
 if (!scheme.trim() || !credential || credential.length > 48 * 1024) throw new Error("authentication credential is required and must be at most 48 KiB");
 return {id,method:"authenticate",params:{version:1 as const,scheme,credential}};
}
