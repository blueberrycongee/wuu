import assert from "node:assert/strict";
import test from "node:test";
import { composeAgentPolicies } from "./agent-policy.js";

test("ordered strategies short circuit and fail closed", async () => {
 const seen: number[]=[];
 const policy=composeAgentPolicies([
  async () => {seen.push(1);return undefined},
  async () => {seen.push(2);return {action:"serial"}},
  async () => {seen.push(3);return {action:"parallel_safe"}},
 ]);
 assert.deepEqual(await policy({phase:"schedule",step_index:0,attempt:0}),{action:"serial"});
 assert.deepEqual(seen,[1,2]);
 await assert.rejects(composeAgentPolicies([])({phase:"schedule",step_index:0,attempt:0}),/no strategy/);
 const canceled=new AbortController();canceled.abort(new Error("canceled"));
 await assert.rejects(policy({phase:"schedule",step_index:0,attempt:0},canceled.signal),/canceled/);
});

test("a retry decision cannot erase accepted effects", async()=>{
 const policy=composeAgentPolicies([async()=>({action:"retry",max_attempts:2})]);
 await assert.rejects(policy({phase:"retry",step_index:0,attempt:0,tool_work_started:true}),/unknown|effects/);
});

test("a policy cannot renew an exhausted inference budget", async()=>{
 const policy=composeAgentPolicies([async()=>({action:"retry",max_attempts:2})]);
 await assert.rejects(policy({phase:"retry",step_index:0,attempt:0,failure:{message:"budget exhausted",origin:"host",category:"budget",context_overflow:false,recovery:{operation_id:"op",attempt_count:3,retry_count:2,max_attempts:3,submission_count:3,stop_reason:"retry_limit"}}}),/budget/);
});

test("parallel schedule must declare a bounded leaf capacity", async()=>{
 await assert.rejects(composeAgentPolicies([()=>({action:"parallel_safe",max_parallel:0})])({phase:"schedule",attempt:0,step_index:0}),/capacity/);
 assert.deepEqual(await composeAgentPolicies([()=>({action:"parallel_safe",max_parallel:2})])({phase:"schedule",attempt:0,step_index:0}),{action:"parallel_safe",max_parallel:2});
});

test("transport policy retains submission evidence without inventing a zero-cost outcome",async()=>{
 const policy=composeAgentPolicies([()=>({action:"recover",recovery_action:"wait_then_replay"})]);
 const input={phase:"transport_retry",attempt:1,step_index:0,failure:{message:"rejected",origin:"provider",category:"server",http_status:503,context_overflow:false,recovery:{attempt_count:1,retry_count:0,max_attempts:3,submission_count:1,stop_reason:""}}} as const;
 assert.equal((await policy(input)).action,"recover");
 assert.equal((await policy({...input,failure:{...input.failure,http_status:0}})).action,"recover");
 await assert.rejects(policy({...input,partial_output:true}),/partial output/);
 assert.equal((await policy({...input,partial_output:true,partial_output_resettable:true})).action,"recover");
 await assert.rejects(policy({...input,partial_output:true,partial_output_resettable:true,tool_work_started:true}),/effects/);
 await assert.rejects(policy({...input,tool_work_started:true}),/effects/);
});

test("checkpoint sampling is distinct from replay and retains budget/effect fences",async()=>{
 const policy=composeAgentPolicies([()=>({action:"resume"})]);
 const input={phase:"checkpoint_resume",attempt:0,step_index:0,max_attempts:3,completed_messages:1,partial_output:true,failure:{message:"saved checkpoint",origin:"host",category:"replay_unsafe",context_overflow:false,recovery:{attempt_count:1,retry_count:0,max_attempts:3,submission_count:1,stop_reason:"replay_unsafe"}}} as const;
 assert.equal((await policy(input)).action,"resume");
 for(const override of [{tool_work_started:true},{completed_messages:0},{attempt:2},{max_attempts:1}]) await assert.rejects(policy({...input,...override}),/checkpoint resume/);
 await assert.rejects(composeAgentPolicies([()=>({action:"retry",max_attempts:3})])(input),/invalid checkpoint_resume/);
});
