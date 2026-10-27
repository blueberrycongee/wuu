import { createHash } from "node:crypto";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { expect, it } from "vitest";
import { ArtifactSources } from "./artifactSources";
import { exportArtifactCopy, canOpenArtifactExternally } from "./artifactExternalFile";

it("exports selected opaque artifacts only on explicit actions and binds the complete copy digest", async () => {
  const home = mkdtempSync(join(tmpdir(), "wuu-present-artifact-"));
  try {
    const content = '<svg xmlns="http://www.w3.org/2000/svg"><rect width="10" height="10"/></svg>';
    const bytes = Buffer.from(content);
    const sha256 = createHash("sha256").update(bytes).digest("hex");
    const sources = new ArtifactSources();
    const uri = "cloud+fixture:tenant/object/opaque";
    let changed = false;
    const source = await sources.acquire({uri,threadId:"thread",sha256},async<T>(method:string,params:unknown):Promise<T>=>{
      if(method==="thread/artifact/resolve")return {generation_id:"generation",artifact:{id:"opaque ID",uri,name:"图表.svg",mime_type:"image/svg+xml",size:bytes.length,sha256}} as T;
      const range=params as {offset:number;max_bytes:number};
      const data = changed ? Buffer.from(content.replace('width="10"','width="20"')) : bytes;
      return {data:data.subarray(range.offset,range.offset+range.max_bytes).toString("base64"),mime_type:"image/svg+xml",offset:range.offset,total:bytes.length,sha256} as T;
    },()=>true);
    const copy = await exportArtifactCopy(source, sources);
    try {
      expect(readFileSync(copy.path, "utf8")).toBe(content);
      expect(await canOpenArtifactExternally(copy.path)).toBe(true);
      writeFileSync(copy.path,"edited externally");
      expect(await (await sources.response(new Request(source.url))).text()).toBe(content);
    } finally { await copy.dispose(); }
    changed=true;
    await expect(exportArtifactCopy(source,sources)).rejects.toThrow("contents changed");
    sources.release(source.leaseId);
    await expect(exportArtifactCopy(source,sources)).rejects.toThrow();
    for (const name of ["changes.patch", "changes.diff"]) {
      const document=join(home,name);writeFileSync(document,"--- before\n+++ after\n");expect(await canOpenArtifactExternally(document)).toBe(true);
    }
    for (const name of ["run.command","run.exe","run.bat","run.desktop","run.lnk","run.url","run.scpt","run.py"]) {
      const executable=join(home,name);writeFileSync(executable,"script");expect(await canOpenArtifactExternally(executable)).toBe(false);
    }
    for (const bytes of [Buffer.from("#! /bin/sh\n"),Buffer.from("MZ"),Buffer.from([0x7f,0x45,0x4c,0x46]),Buffer.from([0xcf,0xfa,0xed,0xfe])]) {
      const disguised=join(home,"disguised.pdf");writeFileSync(disguised,bytes);expect(await canOpenArtifactExternally(disguised)).toBe(false);
    }
  } finally {rmSync(home,{recursive:true,force:true});}
});
