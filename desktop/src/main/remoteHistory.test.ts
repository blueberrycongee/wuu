import { expect, it } from "vitest";
import { projectRemoteHistory } from "./remoteHistory";

it("keeps only the recent notification history and addresses older turns at the source", () => {
  const turns = Array.from({length:63}, (_, i) => ({id:String(i), text:"x".repeat(20_000)}));
  const projected = projectRemoteHistory({id:"t",turns}) as {turns:typeof turns;history_cursor:string};
  expect(JSON.stringify(projected).length).toBeLessThan(270_000);
  expect(projected.history_cursor).toBe(`turn:${projected.turns[0].id}`);
  expect(projected.turns).toEqual(turns.slice(turns.length-projected.turns.length));
  expect(turns).toHaveLength(63);
});

it("preserves source paging cursors and leaves unrelated arrays intact", () => {
  const paged = {id:"t",turns:[{id:"last"}],history_cursor:"item:boundary",history_paged:true};
  expect(projectRemoteHistory(paged)).toEqual(paged);
  expect(projectRemoteHistory({turns:[{id:"one"}]})).toEqual({turns:[{id:"one"}]});
});
