# Projects

Project Agent is experimental and disabled in release builds. There is no setting
to enable it. Existing project conversations remain readable but do not run or
resume queued work. This guide applies to builds that explicitly enable the
feature; see [development](../project/development.md).

A project gives you one conversation for goals, priorities, corrections and
results. Its coordinator delegates technical work and finishes its own turn so
you can continue talking while investigation, implementation and review run in
background sessions. The coordinator's tool surface is limited to coordination
and notes; it cannot run implementation tools or block waiting for a member.
Provider latency and rate limits can still delay replies.

Coordination, durable work records, dispatch, results, controls and recovery are
core Wuu behavior. No plugin is required.

## Start and organize work

Use **+** in the sidebar's **Projects** group, or **New project** in a workspace
menu. Describe the outcome, constraints and authorized delivery:

```text
Switch catalog search to server-side pagination.
Preserve the public API and leave database indexes alone.
Implement and test locally; do not commit or push.
```

The coordinator records a work item with a stable identity, a versioned user
brief, acceptance criteria, authorization and source references. Follow-ups to
the same outcome reuse the work and its sessions. Independent outcomes can have
separate workstreams. Cross-work technical integration belongs to an assigned
technical lead; the coordinator resolves user priorities and scope.

Each workstream has a **technical lead** responsible for investigation, design,
evidence and technical acceptance. It can complete a short task directly, or
create a **persistent executor** for sustained exploration, edits and checks.
The executor retains its context and useful processes across phases. It submits
an exact code/content reference and accessible verification evidence. The lead
reviews that result before accepting it. The host records who accepted what;
it does not independently prove that a model's evidence is correct.

In Git projects, a workstream gets an isolated worktree by default. Its technical
lead and executor share that directory. The host prevents the lead from writing
while its executor runs and serializes mutating tools across participants.
Non-Git workstreams share the workspace and a project write lock. File isolation
does not resolve interface dependencies or authorize integration.

## Models, usage and responsiveness

The coordinator uses its conversation model. **Settings → Runtime → Project
Agent models** selects defaults for the technical lead and persistent executor.
An unset role inherits the coordinator model; an unset executor also honors an
existing worker default. Existing sessions retain their model selections.
Choose sufficient technical judgment for the lead and an economical model that
can execute the work reliably. A model alias can override a newly created
session's default.

Executor reports go to their technical lead. Only acceptance and actionable
workstream blockers wake the coordinator. Tool logs and ordinary status changes
update the UI without generating a coordinating model turn. Background sessions
share the project concurrency limit; the coordinator is outside that pool.
An execution timeout does not cancel the background work.

The project panel shows persisted input, output and cached input tokens by role,
provider and model, including legacy members. Cached tokens are not additional
output tokens. These are usage observations, not a cost or savings estimate.
Evaluate all roles, failed attempts and retries at the same acceptance standard
before concluding that a model mix saves money.

## Follow progress and deliver

The project status control above the composer opens its right panel. Work items
show investigation, implementation, verification, technical review, acceptance,
delivery, blockers or stopped state. Expand an item to inspect its requirements,
authorization, code reference, evidence and member conversations. Dispatch
receipts distinguish recorded input, pending steering and a specific consuming
turn. A finished conversation turn never marks the work delivered.

Acceptance and delivery are separate. After technical acceptance, the
coordinator records delivery only when the result actually reaches its
user-authorized destination. A local worktree can be the requested destination;
it is not evidence of a commit, merge, push, PR or deployment. Those actions each
require user authorization or an established workflow for that action.

## Correct, stop and resume

Change requirements in the main conversation. The coordinator updates the whole
contract, increments its requirement revision and invalidates earlier acceptance.
Old queued input is fenced at consumption, stale execution is interrupted, and
the technical lead receives the new brief. Write ownership remains with a live
tool until it really exits. A receipt that says “recorded” does not mean the
executor has adopted the change yet.

You can also write directly to a work member. Wuu retains that input in the work
contract, invalidates prior acceptance and routes it to the technical lead.
Use the main conversation for status questions that should not revise the work.

**Stop** in the work panel revokes pending work and interrupts both members.
Stopping a work member also stops its workstream. Stopping the coordinator's
current turn leaves independent background work running. **Resume** explicitly
reissues the current contract in the same sessions; stopped work is never
restarted by a late completion report.

On restart Wuu retries persisted dispatches with the same client identity and
recovers known terminal results without duplicating them. An execution without
terminal evidence becomes a blocker requiring inspection and explicit resume;
it is not assumed successful. Wuu's process must remain running for background
execution. Closing the computer is not remote execution.

## Existing projects and limits

Existing Side Agent and Worker conversations remain accessible as legacy
sessions. Their ordinary messaging and adoption/removal controls continue to
work; they are not silently relabeled as reviewed work items. New work uses the
versioned workflow. Removing a work-bound member stops its work before removing
membership; its local files remain available.

Project Agent does not provide timers, external triggers or cloud execution.
Archiving a project does not stop its sessions. Mobile clients can open member
conversations but do not yet expose this work panel. See [subagents](subagents.md)
for a bounded task inside an ordinary conversation.
