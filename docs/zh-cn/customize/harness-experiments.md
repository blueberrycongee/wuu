# 本地 Harness 实验

`wuu eval` 使用产品引擎和独立的内置评分器，对比单文件策略。控制器持有评测规范和证据目录，候选 runtime 持有可清理的独立状态。本地记录与[公开结果发布契约](../../../evals/README.md)分开。

```bash
wuu eval --live-codex-oauth --model MODEL --task test_failure_fix \
  --max-steps 6 --timeout 2m --freeze plan.json
wuu eval --plan plan.json --artifacts baseline
wuu plugin validate --json ./candidate.ts
wuu eval --plan plan.json --candidate ./candidate.ts --artifacts candidate
wuu eval --compare baseline/report.json,candidate/report.json
```

冻结不会调用模型。它将 `all` 解析一次，记录有序任务 ID、编译进可执行文件的任务／初始化／评分器源码摘要、模型、推理设置、配置摘要、父组合包指纹、工具版本和预算。执行时不再筛选任务。初始化、认证、缺失工具、超时和未完成任务都计为失败。父配置、包集合、评分器或已记录的环境变化会拒绝执行，并保留固定失败分母。

评分身份覆盖 `internal/evalharness/tasks.go`、`trace.go` 以及命令端的 `eval.go`、`eval_experiment.go`，包括工具约束和接受规则。这是保守边界：这些文件的任何修改都需要重新建立基线，即使修改与评分无关。

每次执行需要新证据目录，其中保存原始规范、可执行文件摘要、Go 构建和依赖信息、父组合源码树、候选包及内嵌 SDK、实际服务绑定、模块和包指纹、完整返回消息、任务结果和会话记录。导出成功后才删除临时状态；导出错误会保留状态并报错。`--keep-workdirs` 另行保留任务工作目录。产物可能包含私有对话或工具数据，不会自动公开。

每个任务在调用模型前写入 `runtime-locations.json`，记录状态、会话和工作目录路径。可变状态位于该任务证据目录的 `runtime-state/` 中，导出成功后才清理。进程崩溃或被强制取消后，调查未完成运行时应保留该目录及记录的工作目录。provider 尚未报告的进行中用量仍然未知。

候选与 `wuu plugin dev` 使用相同的 TypeScript 快照适配器。显式模块选择仍然有效：若当前组合排除了候选，实际加载包检查会失败，不会改用替代实现。单文件可导入 Node 内置模块和 `@wuu/plugin-sdk`，其他依赖使用包目录。`plugin validate` 检查源码加载和包结构；评测还会检查激活和就绪状态。

内置选择规则要求双方所有任务通过，再要求模型轮数或总 tokens 减少，且耗时不超过基线的 1.25 倍。总 tokens 包含缓存 tokens。订阅 provider 不报告金额时，费用保持“不可得”。单次配对展示选择流程，不能证明普遍提升。运行前另行记录假设、实验总预算和重复计划；改变选择规则时另开实验并重测基线。

Go/runtime 候选使用独立构建的可执行文件，在独立进程中运行相同规范。构建前在报告旁保存准确的 checkout SHA、完整 patch 和未跟踪源码快照；仅有二进制摘要不能重建源码。配置值和外部凭证不会导出，复跑需要相同的选中配置和访问条件。工具版本检查不能冻结网络服务或全部机器状态；相关时另行记录外部依赖和硬件。

## 独立控制器

[`examples/harness/run-pair.py`](../../../examples/harness/run-pair.py) 在独立 POSIX 进程组中顺序运行双方。接纳前保存计划、可执行文件、源码归档和可选 TypeScript 文件。源码归档应来自准确的构建输入，包含未提交 patch 和未跟踪源码；控制器记录摘要，但无法证明二进制确实由所声明的源码构建。

```bash
python3 examples/harness/run-pair.py --plan plan.json \
  --baseline ./baseline-wuu --source-baseline ./baseline-source.tar \
  --candidate ./candidate.ts --output ./pair-001 --timeout 180
# 宿主代码改动另加：
# --candidate-host ./candidate-wuu --source-candidate ./candidate-source.tar
```

`--timeout` 是预先声明的单方总耗时限制。控制器保留 stdout、stderr、进程退出状态、报告，以及 `pair.json` 中的任务账本。崩溃、不完整报告和中断都保留原始分母。最终报告缺失时，账本仍恢复已写入的逐任务结果及用量；中断后也保留进程记录。缺失任务计为失败，进程失败禁止接受候选。完整报告交给基线可执行文件的固定规则比较。候选清理不会删除这些文件。控制器不重试、不调整预算、不复制凭证或修改配置；已存在的输出目录会被拒绝。进程组负责取消和失败清理，受信任插件仍拥有用户的文件系统访问权限。

不调用模型即可检查进程清理：

```bash
python3 examples/harness/check-process-cleanup.py --output ./cleanup-check
python3 examples/harness/check-pair-recovery.py --output ./recovery-check
```

清理检查覆盖正常退出、父进程成功或失败退出后遗留子进程，以及忽略终止信号时的超时清理。恢复检查使用合成进程验证崩溃或控制器取消后已完成任务的记录仍被保留，未完成任务仍计为失败。

## 步骤策略

`agent.step.policy` v1 属于 `decision` 能力。输入与 `agent.pre_step` 相同，是不可变的对话视图；调用发生在输入持久化之后、默认循环的压缩和模型请求之前。此时 provider、模型和 generation 已固定。策略按优先级降序、再按插件 ID 排序，第一个停止决定短路后续策略；继续时，请求变换仍可独立组合。

```ts
import type { RuntimePlugin, AgentStepPolicyInput } from "@wuu/plugin-sdk";

export default {
  initialize() {
    return { protocol_version: 3, capabilities: [
      { id: "agent.step.policy", kind: "decision", version: 1 },
    ] };
  },
  invokeCapability({ input }) {
    const step = input as AgentStepPolicyInput;
    return { output: step.step_index >= 4
      ? { stop: true, reason: "local_budget", content: "Stopped at the local policy budget; work may be incomplete." }
      : { stop: false } };
  },
} satisfies RuntimePlugin;
```

依赖轨迹内容的样本见 [`examples/plugins/repeated-observation.ts`](../../../examples/plugins/repeated-observation.ts)，可用 `wuu plugin dev ./examples/plugins/repeated-observation.ts` 加载。连续两次相同工具请求得到相同观察结果时，它在第三次模型请求前停止，并提供配套请求提示。只适用于不需要轮询的任务；终止说明明确表示任务可能未完成，这个样本没有已证实的 benchmark 收益。

停止必须返回非空 `reason` 和 `content`。内容成为带宿主标记插件来源的可见 assistant 记录，不计为模型轮次。停止不证明正确，不删除已接纳输入，也不撤销工具效果。错误遵循能力声明的策略，取消始终传播。默认循环采用这个决定；替换循环从 `admitInput()` 获取决定，自行定义算法响应。达到默认模型步数上限后不会再调用策略。
