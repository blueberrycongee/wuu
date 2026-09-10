import { describe, expect, it } from 'vitest';
import { accountCredentials, accountOrigin } from '../src/account';

describe('account connection boundary',()=>{
 it('refuses credentials in URLs, plaintext remote endpoints and path substitution',()=>{
  for(const url of ['http://computer.example','https://user:secret@example.com','https://example.com/path','https://example.com?token=abc','https://example.com#frag','file:///tmp/app'])expect(()=>accountOrigin(url)).toThrow();
  expect(accountOrigin('https://example.com/')).toBe('https://example.com');
  expect(accountOrigin('http://127.0.0.1:8787')).toBe('http://127.0.0.1:8787');
 });
 it('never routes a selected device outside the signed-in account or to a phone',()=>{
  const session={server:'https://example.com',username:'alice',pub:'phone',device_seed:'seed',token:'token'};
  const host={pub:'host',name:'Laptop',account:'alice',role:'host' as const,added_at:0,online:true};
  expect(accountCredentials(session,host).relay_url).toBe('wss://example.com/v1/connect');
  expect(()=>accountCredentials(session,{...host,account:'bobby'})).toThrow();
  expect(()=>accountCredentials(session,{...host,role:'phone'})).toThrow();
 });
});

it('starts GitHub with a private verifier and rejects authorization redirects to another origin', async () => {
 const { startGitHubLogin } = await import('../src/account');
 const { vi } = await import('vitest');
 const original = globalThis.fetch;
 const requests: any[] = [];
 const requestID = 'a'.repeat(43);
 try {
  globalThis.fetch = vi.fn(async (_url, init) => {
   const body = JSON.parse(String(init?.body)); requests.push(body);
   return new Response(JSON.stringify({ request_id: requestID, authorize_url: 'https://example.com/v1/account/github/authorize?state=' + requestID, expires_in: 600 }));
  });
  const pending = await startGitHubLogin('https://example.com', true);
  expect(pending.verifier).toHaveLength(43); expect(requests[0].challenge).toHaveLength(43);
  expect(requests[0]).not.toHaveProperty('verifier'); expect(requests[0].native).toBe(true);
  globalThis.fetch = vi.fn(async () => new Response(JSON.stringify({ request_id: 'request', authorize_url: 'https://other.example/v1/account/github/authorize?state=request' })));
  await expect(startGitHubLogin('https://example.com')).rejects.toThrow('Invalid authorization URL');
 } finally { globalThis.fetch = original; }
});
