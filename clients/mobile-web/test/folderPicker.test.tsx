// @vitest-environment jsdom
import { act } from 'react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { pickComputerFolder } from '../src/lib/folderPicker';

beforeEach(() => vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true));
afterEach(async () => {
  await act(async () => { window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' })); });
  vi.unstubAllGlobals();
});

function listingRequests() {
  const pending: Array<{
    method: string;
    path?: string;
    complete: (path: string, folders?: Array<{ name: string; path: string }>) => void;
    fail: (error: Error) => void;
  }> = [];
  const request = <T,>(method: string, params?: unknown): Promise<T> => {
    return new Promise<T>((resolve, reject) => pending.push({
      method,
      path: (params as { path?: string }).path,
      complete: (path, folders = []) => resolve({ path, parent: '/', folders, truncated: false } as T),
      fail: reject,
    }));
  };
  return { request, pending };
}

async function openPicker(request: Parameters<typeof pickComputerFolder>[0], create = false) {
  let selection!: Promise<string | null>;
  await act(async () => { selection = pickComputerFolder(request, create); });
  const dialog = document.querySelector<HTMLElement>('[role="dialog"]')!;
  const input = dialog.querySelector<HTMLInputElement>('input')!;
  const button = (text: string) => [...dialog.querySelectorAll('button')].find(item => item.textContent === text)!;
  return {
    dialog, input, button, selection,
    async edit(value: string, target = input) {
      await act(async () => {
        Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(target, value);
        target.dispatchEvent(new Event('input', { bubbles: true }));
      });
    },
    async submit() {
      await act(async () => {
        input.form!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
      });
    },
  };
}

it('preserves early path input across initial listing and selects the explicitly loaded canonical folder', async () => {
  const { request, pending } = listingRequests();
  const ui = await openPicker(request, true);
  expect(ui.input.disabled).toBe(false);
  await ui.edit('/second-workspace');
  await act(async () => pending[0].complete('/home'));
  expect(ui.input.value).toBe('/second-workspace');
  expect(ui.button('使用此文件夹').disabled).toBe(true);
  expect(ui.button('创建文件夹').disabled).toBe(true);
  await act(async () => ui.button('前往').click());
  expect(pending[1].path).toBe('/second-workspace');
  await ui.edit('/newer-draft');
  await act(async () => pending[1].complete('/canonical/second-workspace'));
  expect(ui.input.value).toBe('/newer-draft');
  expect(ui.button('使用此文件夹').disabled).toBe(true);
  await act(async () => ui.button('前往').click());
  expect(pending[2].path).toBe('/newer-draft');
  await act(async () => pending[2].complete('/canonical/newer', [{ name: 'child', path: '/canonical/newer/child' }]));
  expect(ui.input.value).toBe('/canonical/newer');
  expect(ui.button('使用此文件夹').disabled).toBe(false);
  expect(ui.button('创建文件夹').disabled).toBe(false);
  await act(async () => ui.button('child ›').click());
  expect(pending[3].path).toBe('/canonical/newer/child');
  await act(async () => pending[3].complete('/canonical/child'));
  await act(async () => ui.button('使用此文件夹').click());
  expect(await ui.selection).toBe('/canonical/child');
  expect(document.querySelector('[role="dialog"]')).toBeNull();
});

it('keeps the latest submitted path, loading state and error under out-of-order replies', async () => {
  const { request, pending } = listingRequests();
  const ui = await openPicker(request);
  await act(async () => pending[0].complete('/home'));
  await ui.edit('/first');
  await ui.submit();
  await ui.edit('/second');
  await ui.submit();
  await act(async () => pending[1].complete('/canonical/first'));
  expect(ui.input.value).toBe('/second');
  expect(ui.button('前往').disabled).toBe(true);
  expect(ui.button('使用此文件夹').disabled).toBe(true);
  await act(async () => pending[2].complete('/canonical/second'));
  expect(ui.input.value).toBe('/canonical/second');
  expect(ui.button('前往').disabled).toBe(false);

  await ui.edit('/third');
  await ui.submit();
  await ui.edit('/fourth');
  await ui.submit();
  await act(async () => pending[4].complete('/canonical/fourth'));
  await act(async () => pending[3].fail(new Error('obsolete listing failure')));
  expect(ui.dialog.querySelector('[role="alert"]')).toBeNull();
  expect(ui.input.value).toBe('/canonical/fourth');
  await act(async () => ui.button('使用此文件夹').click());
  expect(await ui.selection).toBe('/canonical/fourth');
});

it('discards replies from a closed picker without changing the reopened picker draft or selection', async () => {
  const { request, pending } = listingRequests();
  const old = await openPicker(request);
  await act(async () => { window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' })); });
  expect(await old.selection).toBeNull();
  const current = await openPicker(request);
  await current.edit('/new-workspace');
  await act(async () => pending[0].fail(new Error('closed picker failure')));
  expect(current.dialog.querySelector('[role="alert"]')).toBeNull();
  expect(current.input.value).toBe('/new-workspace');
  await act(async () => pending[1].complete('/home'));
  expect(current.input.value).toBe('/new-workspace');
  await act(async () => current.button('前往').click());
  await act(async () => pending[2].complete('/new-workspace'));
  await act(async () => current.button('使用此文件夹').click());
  expect(await current.selection).toBe('/new-workspace');
});

it('does not navigate on an old creation result after newer navigation or closure', async () => {
  const { request, pending } = listingRequests();
  const ui = await openPicker(request, true);
  await act(async () => pending[0].complete('/home'));
  await ui.edit('child', ui.dialog.querySelectorAll('input')[1]);
  await act(async () => ui.button('创建文件夹').click());
  expect(pending[1].method).toBe('desktop/projects/mkdir');
  await ui.edit('/other');
  await ui.submit();
  await act(async () => pending[2].complete('/canonical/other'));
  await act(async () => pending[1].complete('/home/child'));
  expect(pending).toHaveLength(3);
  expect(ui.input.value).toBe('/canonical/other');

  await act(async () => ui.button('创建文件夹').click());
  expect(pending[3].method).toBe('desktop/projects/mkdir');
  await act(async () => ui.button('取消').click());
  expect(await ui.selection).toBeNull();
  const current = await openPicker(request);
  await act(async () => pending[3].complete('/canonical/other/child'));
  expect(pending).toHaveLength(5);
  await act(async () => pending[4].complete('/home'));
  await act(async () => current.button('使用此文件夹').click());
  expect(await current.selection).toBe('/home');
});
