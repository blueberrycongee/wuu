import { mkdtemp, writeFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { it, expect } from 'vitest';
import { readCatalogSkill } from './remoteSkills';
import type { SkillListResult } from '../shared/protocol';
it('reads only the selected catalog skill and refuses arbitrary paths',async()=>{
 const root=await mkdtemp(join(tmpdir(),'wuu-skill-read-'));
 try {
  const path=join(root,'SKILL.md');await writeFile(path,'# Installed skill');
  const catalog={skills:[{name:'example',source:'user',path}]} as SkillListResult;
  expect(await readCatalogSkill(catalog,{name:'example',source:'user'})).toEqual({content:'# Installed skill'});
  await expect(readCatalogSkill(catalog,{name:path,source:'user'})).rejects.toThrow('unavailable');
  await expect(readCatalogSkill(catalog,{name:'example',source:'project'})).rejects.toThrow('unavailable');
  await writeFile(path,'x'.repeat(512*1024+1));
  await expect(readCatalogSkill(catalog,{name:'example',source:'user'})).rejects.toThrow('unavailable');
 }finally{await rm(root,{recursive:true,force:true});}
});
