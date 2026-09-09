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
