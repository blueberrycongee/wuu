# 本地 Harness 实验

`wuu eval` 使用产品引擎和独立的内置评分器，对比单文件策略。控制器持有评测规范和证据目录，候选 runtime 持有可清理的独立状态。本地记录与[公开结果发布契约](../../../evals/README.md)分开。

```bash
wuu eval --provider authorized-api --model MODEL --task test_failure_fix \
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

## 发现接口、扩展实验并保留判定

`wuu plugin describe --workdir /project --types-dir ./sdk-snapshot` 导出当前二进制
内嵌的公开 SDK，报告选中服务的完整描述、实际依赖边、未满足需求、插件源码目录与
指纹、模块故障和工具定义。它按项目配置打开**新的 CLI 状态 generation**，不是连接
已有桌面会话。`graph_generation` 标识实际保留的图，与目录 epoch 分开。导出目录必须不存在。

只使用已授权的 API 连接和预先声明的资源预算；这些命令不授权复用个人订阅。
计划固定任务数、单任务步数／超时和并发度。运行器不计算金额，费用未知不代表免费。

冻结时的 `--experiment-policy policy.json` 可指定并发度及内置判定规则，例如：

```json
{"concurrency":2,"acceptance":{"require_all_pass":true,"improvement":"tokens","max_duration_ratio":1.25}}
```

并发度为 1–256。结果始终按冻结顺序保存，每个任务使用独立运行状态，并在任务配置前
复制配置。`improvement` 支持 `turns-or-tokens`、`turns`、`tokens`、`quality`、
`non-regression`；这些便利策略不限制外部实验实现。

冻结时用 `--experiment-controller controller.json` 替换任务源、评分、调度和判定。
公开 SDK 的 `ExperimentController` 与 `dispatchExperimentController` 实现协议 1：
stdin 输入一个 `{version:1,method,params}` JSON 请求，stdout 输出一个 JSON 响应；
诊断写 stderr。方法包括 `tasks`、`schedule`、`setup`、`score`、`accept`。
判定为 `accept`、`reject` 或 `revise`。调度可重排任务、指定并发度，但必须恰好保留每个
任务一次；调度结果被冻结，候选不会重新选择任务。

控制器身份格式为 `{ "command": ["/absolute/node", "/absolute/controller.mjs"],
"files": {"/absolute/node":"sha256:…", "/absolute/controller.mjs":"sha256:…"} }`。
固定全部传递执行／脚本／数据输入的摘要。把
[`experiment-controller.ts`](../../../examples/plugins/experiment-controller.ts)
基于公开 SDK 构建为独立 bundle，再计算最终文件（包括 Node 可执行文件）的摘要。
无需内部 import 或安装插件。每次调用前后检查输入摘要，尝试证据中保存输入副本。
每次调用限时一分钟，stdout 上限 8 MiB、stderr 上限 256 KiB；超限明确失败，并清理 POSIX 进程组。示例文本评分器有明确局限，不是已验证的语义提升。

控制器、评分数据和证据放在候选工作区之外。受信任扩展体系不是恶意代码沙箱：同一
用户的代码仍可能访问操作系统允许的文件。摘要检查检测输入变化，不证明恶意候选
无法干预评分。控制器输入变更必须新开计划并重测基线。外部控制器计划不绑定内置
评分源码，因此可比较宿主代码变化而不同时改变独立评分器。

```bash
wuu eval --provider authorized-api --model MODEL --max-steps 6 --timeout 2m \
  --experiment-controller controller.json --experiment-policy policy.json --freeze plan.json
wuu eval --plan plan.json --artifacts baseline
wuu eval --plan plan.json --candidate candidate.ts --artifacts candidate
wuu eval --compare baseline/report.json,candidate/report.json --decision-output decision.json
```

比较会核验保留的计划摘要、有序任务分母和非负用量。外部控制器还会重新读取完整执行、
逐任务结果和保留的工作区，在比较控制器进程中调用固定评分器。候选的成功字段不是独立
判定：缺失证据会拒绝比较，独立评分失败不能由候选报告覆盖。这些观察并非真实模型调用的
密码学证明；后者需要可信 provider 回执。外部控制器尝试会保留工作区供此验证。`--decision-output` 不覆盖已有
判定。接受后通过 `wuu plugin dev ./candidate.ts` 选择已记录源码用于后续工作；拒绝时
保留上一版。下一轮创建新源码文件和证据目录。评测规范改变时必须重测基线，不能
复用有利结果。

## 重新构建宿主候选

从保留的源码归档构建独立可执行文件后，运行
`wuu eval --plan plan.json --candidate-host ./candidate-wuu --artifacts host-attempt`。
该入口要求计划包含独立固定的控制器。原控制器在 `host-attempt.host/` 保存新任务边界
checkpoint、二进制／计划摘要、stdout/stderr 和退出状态；候选崩溃后证据仍在。
报告必须标识真正启动的二进制及完整任务集。超时和退出都会清理所属进程组，原宿主
保持不变。此边界启动新评测任务，**不迁移任意执行中的调用栈，也不撤销外部副作用**。
工具结果不确定时保留状态，不盲目重试。已有 `run-pair.py` 还会快照源码归档，并在
控制器中断时保留逐任务恢复账本。

比较宿主代码时，基线和候选都应通过 `--candidate-host` 运行。比较器读取原控制器保存并核对身份的 `process.json` 耗时，不信任候选自报耗时；不接受一侧受监督、另一侧自行计时的混合比较。`run-pair.py --candidate-host` 会对两侧使用相同监督方式。该指标是整个尝试的墙钟时间，逐任务观察不是独立计时证明。

为示例控制器冻结计划后，可不调用模型地验证公开协议：

```bash
python3 examples/harness/check-controller.py --wuu ./bin/wuu --plan plan.json --output ./controller-check
```

此确定性检查写入合成执行观察，验证伪造成功报告不能覆盖独立评分，再修改第二轮
候选并保存新判定。保留的 `result.json` 明确标注框架验证，不是真实模型实验或自我提升结果。
