# LLMConform 测试体系与界面流程重构方案

> 状态：第一阶段已实现（测试目录、原子断言、依赖阻断、三协议流式状态机、四阶段 Web 界面）
> 日期：2026-08-31
> 范围：测试目录、Runner、报告模型和 Web 界面。官方 SDK 判定器、工具结果回传、流式工具和长上下文仍属于后续阶段。

## 1. 结论先行

当前版本适合作为“链路是否能跑通”的 smoke test，但还不足以支撑“协议兼容性”结论。主要原因不是用例数量少，而是测试模型过粗：每条路由固定执行 5 个大检查，每个检查只有一个 happy path，最终用一个 PASS/FAIL 隐藏了其中多个协议断言。

建议采用下面的产品和实现模型：

1. **场景（scenario）和断言（assertion）分离。** 一次网络请求可以产出多个原子断言，既减少模型调用和费用，又能准确说明是哪一个字段或事件失败。
2. **分阶段探测。** 先确认连接、路由和鉴权，再执行响应契约，最后执行工具回传等行为测试。前置失败时，下游用例标记为 `BLOCKED`，不能制造一串重复 FAIL。
3. **按声明测试，不默认全选三种协议。** 用户要先说明目标是 OpenAI、Claude，还是声称三路由兼容的网关；界面据此生成测试计划。
4. **流式协议使用状态机验证。** 不只检查“见过开始、文本、结束”，还要验证顺序、索引、终态、增量重建和流内错误。
5. **官方 SDK 作为第二个判定器。** 自研解析器验证详细原因，官方 OpenAI/Anthropic SDK 验证真实客户端能否消费响应。
6. **界面从固定表格改为四阶段流程：目标配置 → 测试计划 → 运行 → 结果。** 结果首先回答“兼容什么、哪里不兼容、下一步怎么处理”，再展示原始报文。

第一版重构不应直接把现有 15 个格子扩成几十个格子。更合适的结构是：少量场景请求、较多原子断言、按能力域折叠展示。

## 2. 调研：成熟项目怎么测试

### 2.1 `am-i-openai-compatible`：分阶段、声明式目录、明确等级

[`am-i-openai-compatible`](https://github.com/heiervang-technologies/am-i-openai-compatible) 把探测拆成三层：

- Phase A：用最小请求判断路由是否存在；
- Phase B：发送最小合法请求，用 schema 校验响应契约；
- Phase C：复用之前的响应，做跨接口一致性检查。

它还为能力标注 `core`、`optional`、`ext` 等等级，并分别定义 PASS、WARN、FAIL、SKIP。这种做法值得直接吸收：**先定义目标声称支持什么，再判断不支持是失败、警告还是跳过。**

对 LLMConform 的启发：

- 路由存在、鉴权正确、协议契约、行为能力不能混成一个检查；
- 测试目录应是数据，而不是散落在 `switch checkID` 中；
- `/v1/models` 的发现结果应复用，不能只作为一个与运行计划无关的模型下拉框数据源；
- 核心能力和可选能力必须有明确严重级别。

### 2.2 llama.cpp：参数化反例、流式不变量、真实 SDK

[llama.cpp 的 Chat Completions 测试](https://github.com/ggml-org/llama.cpp/blob/master/tools/server/tests/unit/test_chat_completion.py) 不是只检查一次非空文本。它包含：

- 参数化 system/user 内容、模板、`max_tokens` 和 `finish_reason`；
- 一组非法 `messages` 类型和结构；
- 流式首块 role、后续 delta、同一流中 ID 一致、结束原因、最终 usage；
- 上下文超限的非流式和流式错误；
- 直接用官方 OpenAI SDK 调用兼容端点。

对 LLMConform 的启发：

- “能读到一个文本 delta”不等于流兼容；同一响应 ID、首块/末块、finish reason 和 usage 所在位置都是兼容性的一部分；
- 无效输入应该做成参数化矩阵，而不是只发送一个 `{}`；
- 自己能解析 JSON 还不够，官方 SDK 能否完成调用才是用户真正关心的结果。

### 2.3 Anthropic Go SDK：用事件序列测试聚合器

[Anthropic Go SDK 的 Message 测试](https://github.com/anthropics/anthropic-sdk-go/blob/main/message_test.go) 使用表驱动事件序列验证最终聚合结果，覆盖：

- 文本块的 start/delta/stop；
- `input_json_delta` 分片拼接成工具参数；
- 多内容块和交错内容块；
- content block 索引间隙、负索引、未 start 就 delta/stop 等错误序列。

Anthropic 官方流式说明还明确规定了事件生命周期、允许任意数量 `ping`、流内 `error` 事件，以及 `input_json_delta` 应在 block stop 后拼接并解析。参见 [Streaming messages](https://platform.claude.com/docs/en/build-with-claude/streaming)。

对 LLMConform 的启发：

- SSE 解析和协议事件聚合应分成两层测试；
- Messages 流必须按 block index 建状态，不能只用三个布尔值；
- 未知事件要向前兼容，`ping` 要忽略，但 `error` 绝不能忽略。

### 2.4 OpenAI Responses：完整生命周期而非三个事件名

官方 Responses 流示例依次包含 `response.created`、output item/content part 的 added/delta/done，最后是 `response.completed`；Responses 还存在 `failed`、`incomplete`、`cancelled` 等非成功状态。参见 [OpenAI Responses API](https://developers.openai.com/api/reference/typescript/resources/beta/subresources/responses/methods/create)。

OpenAI 官方客户端的流式实现也要求先出现 `response.created` 才能建立快照，并按 `output_index`、`content_index` 累积文本或工具参数。参见 [openai-python Responses streaming implementation](https://github.com/openai/openai-python/blob/main/src/openai/lib/streaming/responses/_responses.py)。

对 LLMConform 的启发：

- `created + 任意 delta + completed` 只是最弱检查；
- 必须识别并拒绝失败终态，验证事件顺序和索引，必要时验证 `sequence_number` 单调递增；
- 完成事件中的最终 Response 应与增量重建结果一致。

### 2.5 工具调用：必须验证完整关联链

OpenAI Chat Completions 的工具调用对象包含 `id`、`type`、`function`，指定 named tool choice 可以强制调用具体函数。参见 [OpenAI Chat Completions API](https://developers.openai.com/api/reference/cli/resources/chat/subresources/completions)。

Claude 的 `tool_use` 包含唯一 `id`、名称和符合 `input_schema` 的输入；回传时 `tool_result.tool_use_id` 必须关联该 ID，并紧跟对应的 assistant tool use。参见 [Claude Handle tool calls](https://platform.claude.com/docs/en/agents-and-tools/tool-use/handle-tool-calls)。

因此，工具测试至少需要验证：

- 调用类型、调用 ID、工具名；
- 参数是对象，并满足测试定义的 JSON Schema；
- 协议规定的 finish/stop reason；
- 工具结果回传能用同一 call ID 完成第二轮请求；
- 流式工具参数能按调用/内容索引分别拼接。

当前实现只验证工具名和“参数能解析成 JSON 对象”，所以 `{}`、缺少 call ID、缺少 `type=function` 等响应都可能误报 PASS。

## 3. 当前实现的具体问题

### 3.1 测试代码问题

当前 `runner_test.go` 的主测试由一个 `mockProvider` 同时模拟三种协议，然后断言 15 项全部通过。这个测试存在几个根本问题：

1. mock 几乎不校验收到的请求体。即使 `buildRequest` 字段名写错，mock 仍可能按路径返回正确响应，测试继续通过。
2. mock 返回值和生产校验器由同一份实现假设写成，容易形成“自己证明自己”。
3. 没有针对 `validateBasic`、`validateTools`、`validateUsage`、`validateError` 的负例测试。
4. `parseSSE` 只有一个多行 data 用例，没有 CRLF、注释、未知字段、大事件、缺少空行、畸形 JSON 等边界测试。
5. 只有 happy path，没有验证“错误响应一定失败”，因此无法证明探针具备发现问题的能力。
6. 部分测试服务器依赖系统默认监听地址；在 IPv6 或受限环境中可能无法运行。应统一使用显式 IPv4 listener 的测试 helper。

### 3.2 Runner 判定问题

当前生产逻辑存在以下误报风险：

- Chat SSE 只要任意位置出现 chunk、文本和 `[DONE]` 就通过，不验证顺序、chunk 对象类型、ID 一致或结束原因；
- Responses SSE 不检查 `response.failed` / `response.incomplete`，也不检查 output item/content part 生命周期；
- Messages SSE 即使 `message_stop` 出现在 `message_start` 前也可能通过，不检查 block index 和流内 error；
- Responses 基础响应不检查最终 `status`，与现有设计文档的规则不一致；
- Usage 接受小数，只检查非负，不检查 Chat/Responses 的 `total = input + output`；
- 工具参数只检查“是 map”，不检查 `city` 必填字段和 `additionalProperties: false`；
- 错误测试把所有 4xx 当成同一类通过，无法区分 400 参数错误、401 鉴权失败、404 路由不存在和 429 限流；
- Usage 用例重复发送基础请求，产生额外模型调用，却没有复用基础响应中的 usage。

### 3.3 界面流程问题

当前界面的问题不是样式，而是用户决策顺序和结果信息架构：

1. **默认勾选三个路由。** 对 OpenAI 官方服务或 Claude 原生服务，这会必然制造不适用路由的失败。
2. **没有“我要验证什么声明”。** 用户无法选择 OpenAI 原生、Claude 原生、三路由网关或自定义能力集。
3. **测试用例区域是静态文案。** 无论选了什么路由、将来选择什么测试档位，它永远显示固定 5 项。
4. **模型选择和目标地址未绑定。** 获取过模型后再修改 Base URL，旧候选仍保留，可能把错误模型提交给新目标。
5. **自动写入第一个模型。** 获取列表后未经用户确认就选择第一项，容易执行到非预期或高成本模型。
6. **运行中配置仍可编辑。** 只有运行和获取模型按钮被禁用，页面显示的地址、模型、路由可能和正在执行的 run 不一致。
7. **结果矩阵颗粒度错误。** 一个 `STREAM PASS` 看不出是 Content-Type、事件 JSON、事件顺序、终态还是内容重建通过。
8. **没有阻塞态。** 鉴权失败后其他用例继续显示 FAIL，会把一个根因放大成多项故障。
9. **缺少行动建议。** 详情只有 summary、expected 和原始报文，没有稳定的 reason code、observed 值和建议修复项。
10. **下载入口过早出现。** 运行开始就可下载，用户可能拿到尚未完成的报告而不自知。

## 4. 新的核心概念

### 4.1 Target Profile：先说明兼容性声明

建议提供四种目标类型：

| Profile | 默认协议 | 判定口径 |
| --- | --- | --- |
| OpenAI API | Chat、Responses | 对应 OpenAI 核心契约 |
| Claude API | Messages | 对应 Claude 核心契约 |
| Multi-protocol Gateway | 用户声明的多条路由 | 声明支持的路由缺失即 FAIL |
| Custom | 用户逐项选择 | 未选择能力不执行，显示为未纳入计划 |

“自动识别”可以作为辅助建议，但不能悄悄替用户决定。识别依据可能只是 `/v1/models` 或错误 envelope，不足以证明服务类型。

### 4.2 Test Profile：控制深度、费用和时间

| 档位 | 目的 | 建议场景 |
| --- | --- | --- |
| Quick | 确认路由、鉴权、最小非流式响应 | 首次接入、排查连接 |
| Standard | 基础、流式、Usage、强制工具、错误矩阵 | 默认档位、CI 门禁 |
| Full | 多轮、工具回传、流式工具、参数边界、Unicode、长上下文 | 发布前认证、回归测试 |

计划页必须在运行前显示：场景数、预计产生模型输出的请求数、最大输出 token、预计耗时区间。不要只显示“5 项/路由”。

### 4.3 Scenario 与 Assertion

一次场景请求可能产生多个断言。例如 `chat.basic` 只发送一次请求，但产出：

- HTTP 状态为 2xx；
- Content-Type 为 JSON；
- 官方 SDK 可反序列化；
- `object == chat.completion`；
- `id` 非空；
- `choices` 非空；
- assistant role 正确；
- content 或 refusal/tool call 满足请求场景；
- finish reason 合法；
- usage 字段为非负整数且合计一致。

报告应分别记录这些断言，但界面默认折叠到“基础响应 8/9 通过”。这比再发一个独立 Usage 请求更准确、更便宜。

### 4.4 状态语义

建议使用：

| 状态 | 含义 |
| --- | --- |
| PASS | 已执行，所有强制断言满足 |
| FAIL | 已执行，核心契约不满足 |
| WARN | 已执行，可选能力或宽松兼容项不满足 |
| SKIP | 不在所选 profile、服务明确不声明该能力 |
| BLOCKED | 因连接、鉴权或前置用例失败而未执行 |
| ERROR | 测试器自身错误、超时、报告无法解析等非被测契约失败 |

必须区分 FAIL、BLOCKED 和 ERROR。否则用户无法判断应该修网关、改配置，还是修 LLMConform。

## 5. 建议测试目录

### 5.1 公共前置场景

| 场景 ID | 档位 | 核心断言 |
| --- | --- | --- |
| `target.url` | Quick | URL 合法、可连接、TLS/重定向策略明确 |
| `models.list` | Quick | 可选路由存在；若成功则 data 中有非空 model id |
| `route.exists` | Quick | 声明支持的 POST 路由不是 404/405 |
| `auth.valid` | Quick | 有效凭据不会得到 401/403 |
| `auth.invalid` | Standard | 错误凭据得到 401/403 和协议错误 envelope |

`models.list` 不应成为所有运行的硬前置：部分合法网关可能不暴露模型列表。此时可 WARN，并允许手填模型继续。

### 5.2 每条协议的 Standard 场景

| 能力域 | 场景 | 说明 |
| --- | --- | --- |
| Basic | `*.basic` | 最小请求；同时验证非流式结构和 usage |
| Stream | `*.stream.text` | 文本流状态机、终态、聚合结果 |
| Tools | `*.tools.forced` | 强制指定工具，验证调用结构和 JSON Schema |
| Errors | `*.error.missing_model` | 缺少 model |
| Errors | `*.error.missing_input` | 缺少 messages/input |
| Errors | `*.error.invalid_role` | 非法角色或消息结构 |
| Errors | `*.error.invalid_limit` | 0、负数或错误类型，按协议定义选择 |

错误矩阵一般不会产生模型输出，成本很低，应该参数化执行并分别报告，而不是合并成一个 `{}`。

### 5.3 Full 场景

- system/developer 与多轮角色顺序；
- 中文、Emoji、换行和组合字符；
- tool choice 为 none/auto/required/指定工具；
- 工具结果回传和 call ID 关联；
- 多个/并行工具调用；
- 流式工具参数拼接；
- streaming usage；
- 超长上下文的明确错误；
- 429、413、5xx/529 和流内错误（能安全构造时执行，否则使用 fixture 单测）；
- 官方 SDK 端到端调用。

Full 里的限流、过载等用例不应通过主动压测生产服务来制造。默认只在 fixture/受控测试服务中执行；对真实目标可通过显式注入或历史响应验证。

## 6. 三种流式状态机的最低要求

### 6.1 Chat Completions

- 每个非 `[DONE]` data 块必须是合法 JSON；
- chunk 的 `object`、`id`、`model` 类型正确，ID 在同一流中稳定；
- delta 按 choice index 聚合；
- 工具调用参数按 choice/tool index 聚合；
- finish reason 出现在合法位置；
- `[DONE]` 是终止标记，之后不能再出现业务事件；
- 若请求 `stream_options.include_usage`，usage 块位置和结构符合契约。

### 6.2 Responses

- 首个建模事件必须是 `response.created`；
- output item 和 content part 的 index 生命周期合法；
- delta 只能作用于已创建的 item/part；
- `sequence_number` 存在时单调递增；
- `response.completed` 才是成功；`failed`、`incomplete`、`cancelled` 均不能通过；
- 最终 Response 与事件聚合的文本、工具参数和 usage 一致。

### 6.3 Messages

- `message_start` 先于内容块；
- 每个 block index 必须 start → delta* → stop；
- `ping` 可出现任意次；未知事件保留并忽略，不能导致崩溃；
- `error` 事件立即把场景标为失败；
- `input_json_delta.partial_json` 在对应 block stop 时拼成合法对象；
- `message_delta` 的 usage 按累计值解释；
- `message_stop` 只能在所有活动 block 关闭后出现。

## 7. 内部自动化测试重写方式

### 7.1 Fixture 优先，表驱动覆盖每个判定分支

建议新增稳定 fixture：

```text
testdata/
  chat/
    basic_valid.json
    basic_missing_object.json
    tools_valid.json
    tools_missing_call_id.json
    stream_valid.sse
    stream_missing_done.sse
  responses/
    ...
  messages/
    ...
```

每个 validator 测试采用表驱动：fixture、期望状态、reason code。每个“应该 PASS”的 fixture 至少配一个单字段 mutation 的“应该 FAIL”用例，证明断言真正有效。

### 7.2 测试层次

1. **纯函数单测**：request builder、JSON validator、usage、URL、脱敏；
2. **SSE parser 单测**：SSE 语法，不包含协议判断；
3. **stream accumulator 单测**：三协议状态机和最终聚合对象；
4. **transport 单测**：header、超时、body 上限、取消、HTTP 状态；
5. **runner 集成测试**：受控 provider，验证依赖、BLOCKED、进度和报告；
6. **SDK contract 测试**：让官方 SDK 消费受控响应或真实目标；
7. **真实服务 smoke**：凭据存在时才运行，不进入默认单测。

### 7.3 mock 设计原则

- mock 必须先严格校验请求，错误请求直接让测试失败；
- 不再使用一个无状态函数覆盖所有协议和场景；
- handler 按 scenario 注册，未预期请求应失败；
- 保存收到的 method/path/header/body，断言调用次数；
- 所有测试服务器统一通过显式 IPv4 listener 创建；
- 流式测试要真实 flush 多个 chunk，另设纯 fixture 测试完整事件状态机。

## 8. 新界面信息架构

### 8.1 第一步：目标配置

显示：

- Base URL；
- 凭据；
- 目标类型（OpenAI / Claude / Multi-protocol / Custom）；
- “验证连接”按钮。

连接验证成功后再展示模型选择。Base URL、凭据或目标类型变化时，必须清空之前加载的模型和预检结果。

不要自动选择第一个模型。可以高亮推荐项，但必须由用户确认或明确接受默认值。

### 8.2 第二步：测试计划

显示：

- 协议路由；
- Quick / Standard / Full；
- 将执行的场景，按连接、基础、流式、工具、错误分组；
- 场景总数、预计有效模型调用数、最大输出 token；
- 不支持/未选择能力会怎样计分。

用户点击“开始检查”后冻结一份 `RunPlan` 快照。运行过程中配置区变为只读摘要，修改配置等同于创建下一次运行。

### 8.3 第三步：运行

显示当前场景，而不是固定 `7 / 15`：

```text
Responses · 流式文本
正在验证：output item 生命周期
12 / 27 个场景完成 · 34 / 61 个断言通过
```

支持取消。连接或鉴权失败时，立即把依赖场景标为 BLOCKED，并给出“修改配置后重新运行”的主操作。

### 8.4 第四步：结果

结果页首先展示结论：

```text
部分兼容
OpenAI Chat：通过
OpenAI Responses：流式协议不兼容
Claude Messages：未纳入本次计划
```

随后按能力域展示，而不是按 5 个固定大格子展示：

```text
Responses
  基础响应       9 / 9 通过
  流式响应       7 / 10 通过   FAIL
    ✓ Content-Type
    ✓ response.created
    ✕ output item 生命周期
    ✕ sequence_number 单调递增
    ✕ response.completed 最终对象
  工具调用       6 / 6 通过
```

默认过滤并展开 FAIL/WARN；PASS 折叠。每个失败断言展示：

- 稳定 reason code；
- 期望值；
- 实际观察值；
- 影响（哪些 SDK/客户端可能失败）；
- 建议修复；
- 脱敏后的请求、响应和事件时间线。

完整报告生成后才启用下载，并明确显示报告 schema 版本、测试目录版本和完成状态。

## 9. 建议数据模型

```go
type TestCaseDef struct {
    ID           string
    Protocol     string
    Capability   string
    Level        string
    Profiles     []string
    DependsOn    []string
    Request      RequestTemplate
    Assertions   []AssertionDef
    MaxCalls     int
    MaxTokens    int
}

type RunPlan struct {
    CatalogVersion string
    Target         TargetSnapshot
    Profile        string
    Cases          []PlannedCase
}

type CaseResult struct {
    ID         string
    Status     string
    ReasonCode string
    Assertions []AssertionResult
    Evidence   Evidence
}
```

关键点：报告保存 `RunPlan` 快照，而不是只保存 Base URL、模型和 routes。这样未来测试目录升级后，旧报告仍可解释和复现。

建议 API 逐步演进为：

- `POST /api/preflight`：连接、路由、模型发现；
- `GET /api/test-catalog`：界面动态渲染测试目录；
- `POST /api/run-plans`：根据 target/profile 生成并冻结计划；
- `POST /api/runs`：执行指定 plan；
- SSE 发送 `case_started`、`assertion_finished`、`case_finished`、`run_finished` 增量事件，不再每次传完整 Report。

## 10. 落地顺序

### Phase 1：先让测试器本身可信

- 增加协议响应 fixture 和表驱动负例；
- 把三种流式校验改为状态机并完整单测；
- 给所有失败产生稳定 reason code；
- mock 严格验证请求；
- 修复测试服务器的监听可移植性。

完成标志：每条生产判定分支都有正例和负例；人为删除一个必填字段时，至少一个测试稳定失败。

### Phase 2：重构测试目录和报告

- 引入 TestCaseDef、Scenario、Assertion、RunPlan；
- 合并基础与非流式 Usage 请求；
- 增加前置依赖和 BLOCKED/ERROR；
- 加入 Standard 错误矩阵；
- 报告包含目录版本和计划快照。

完成标志：测试计划不依赖硬编码的 `allCheckIDs()`，CLI 与 Web 使用同一目录。

### Phase 3：重做界面流程

- 目标 profile 和 preflight；
- 动态计划预览；
- 运行时冻结配置；
- 能力域/断言式结果页；
- 完成后下载和失败修复建议。

完成标志：用户不看原始 JSON，也能回答“目标声明了什么、实际通过了什么、根因是什么”。

### Phase 4：SDK 与真实目标验证

- OpenAI/Anthropic 官方 SDK contract runner；
- 可选真实服务 smoke；
- 基准报告与回归差异；
- Full 档位的工具回传、流式工具和长上下文。

## 11. 第一轮实现建议范围

第一轮不要同时实现文档中全部 Full 用例。建议把目标控制为：

1. 建立声明式测试目录和新结果模型；
2. Standard 覆盖三协议的 basic、stream text、forced tool、4 个错误输入；
3. basic 响应顺带验证非流式 usage，删除重复 Usage 请求；
4. 三个流式状态机都有完整 fixture 单测；
5. Web 完成“目标配置 → 计划 → 运行 → 结果”的骨架；
6. 结果能区分 FAIL、BLOCKED、ERROR，并展示原子断言。

这个范围已经能显著降低误报，同时把真实模型请求数控制在每条协议约 3 次，而不是为了增加用例数量同步增加费用。

## 12. 验收标准

- 一个缺少 Chat tool call ID 的响应必须失败，并给出固定 reason code；
- 一个先 `response.completed` 后 delta 的 Responses 流必须失败；
- 一个 Messages `content_block_delta(index=1)` 在 block 1 未 start 时必须失败；
- 一个流内 error 不能因之后存在 stop/completed 而通过；
- 鉴权失败只产生一个根因 FAIL，其依赖场景为 BLOCKED；
- OpenAI profile 不会默认测试 Messages；Claude profile 不会默认测试 Chat/Responses；
- 修改 Base URL 后，旧模型候选和预检结果立即失效；
- 运行中的配置不可与 RunPlan 快照产生视觉歧义；
- 结果页无需打开原始报文即可定位失败字段/事件；
- 下载的报告明确为完成态，并包含 catalog/schema 版本。

---

现有 [`compatibility-test-cases.md`](./compatibility-test-cases.md) 可以继续作为能力清单；本文定义的是如何把该清单转化为可信、低误报、可解释且界面可承载的测试系统。实现时应先按本文重构测试模型，再逐批把原清单中的 P1/P2 能力加入目录。
