import { expect, it } from "vitest";
import { RemoteAttachments, threadAttachmentParams } from "./remoteAttachments";
it("defers images, reads bounded chunks and hydrates edits without changing history", () => {
  const store = new RemoteAttachments();
  const image = { media_type: "image/png", data: "a".repeat(400_000) };
  const original = { images: [image], text: "keep text" };
  const projected = store.project(original) as {images: Array<typeof image & {remote_ref:string}>};
  expect(JSON.stringify(projected).length).toBeLessThan(500);
  expect(image.data.length).toBe(400_000);
  expect(store.hydrate(projected)).toEqual(original);
  const ref = projected.images[0].remote_ref;
  const parts: string[] = [];
  for (let offset = 0; offset < image.data.length;) {
    const part = store.read({ ref, offset });
    expect(part.data.length).toBeLessThanOrEqual(128 * 1024);
    parts.push(part.data); offset += part.data.length;
  }
  expect(parts.join("")).toBe(image.data);
  expect(() => store.read({ref, offset:-1})).toThrow();
  expect(() => store.read({ref, offset:0.5})).toThrow();
  expect(() => store.read({ref:"unknown"})).toThrow("expired");
  store.clear();
  expect(() => store.hydrate(projected)).toThrow("expired");
});

it("keeps history attachment addresses independent of the transient image cache", async () => {
  const store = new RemoteAttachments();
  const source = { id:"thread", turns:[{id:"turn",items:[{id:"item",images:[{media_type:"image/png",data:"a".repeat(20_000)}]}]}] };
  const projected = store.project(source) as typeof source & { turns: Array<{ items: Array<{ images: Array<{remote_ref: string}> }> }> };
  const ref = projected.turns[0].items[0].images[0].remote_ref;
  store.clear();
  expect(threadAttachmentParams(ref)).toMatchObject({thread_id:"thread",turn_id:"turn",item_id:"item",index:0});
  const read = async (value:string) => { expect(value).toBe(ref); return source.turns[0].items[0].images[0].data; };
  expect(await store.hydrateRemote(projected, read)).toEqual(source);
});
it("addresses structured tool-result images by their original content position", () => {
  const store = new RemoteAttachments();
  const result = store.project({thread_id:"thread",turn_id:"turn",item:{id:"tool",type:"tool_call",result_detail:{content:[{type:"text",text:"caption"},{type:"image",mime_type:"image/png",data:"a".repeat(20_000)}]}}}) as {item:{result_detail:{content:Array<{data?:string;remote_ref?:string}>}}};
  const image = result.item.result_detail.content[1];
  expect(image.data).toBe("");
  expect(threadAttachmentParams(image.remote_ref!)).toMatchObject({thread_id:"thread",turn_id:"turn",item_id:"tool",index:1,kind:"result"});
});
