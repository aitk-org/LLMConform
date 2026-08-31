# LLMConform 兼容性测试用例

## 1. 目标

LLMConform 用于检查一个 LLM 网关或 API 服务是否同时兼容以下三类协议：

| 路由 ID | 协议 | HTTP 路径 | 典型客户端 |
| --- | --- | --- | --- |
| `chat` | OpenAI Chat Completions | `POST /v1/chat/completions` | OpenAI SDK `chat.completions.create` |
| `responses` | OpenAI Responses | `POST /v1/responses` | OpenAI SDK `responses.create` |
| `messages` | Claude Messages | `POST /v1/messages` | Anthropic SDK `messages.create` |

OpenAI 将 Chat Completions 和 Responses 定义为两套不同的 API 响应模型；Claude 原生 API 使用 Messages 模型。因此，三条路由必须分别解析，不能用一个通用 JSON 结构强行判断。

官方参考：

- [OpenAI Chat Completions API](https://developers.openai.com/api/reference/resources/chat)
- [OpenAI Responses API](https://developers.openai.com/api/reference/cli/resources/responses/methods/create)
- [Claude Messages API](https://platform.claude.com/docs/en/api/messages/create)

## 2. “完全兼容”的判定口径

完全兼容不是要求 GPT 和 Claude 生成逐字相同的答案。模型、采样策略和安全策略不同，文本内容本身不能作为跨模型的唯一判定依据。

LLMConform 应判定以下内容：

1. 路由、HTTP 方法、鉴权和 Content-Type 正确。
2. 请求字段被正确接受或明确拒绝，不得静默丢失有语义的字段。
3. 响应 JSON 可以被对应官方 SDK 解析。
4. 文本、工具调用、终止原因和 token usage 能被正确提取。
5. SSE 流能正确解析、重建，并且有明确终止事件。
6. 错误状态码和错误对象结构符合协议约定。
7. 不支持的厂商专属能力必须标为 `FAIL`、`WARN` 或 `N/A`，不能误报为 `PASS`。

跨模型比较时，应比较归一化结果：

```text
normalized_response {
  text
  tool_calls[] { id, name, arguments }
  finish_reason
  usage { input_tokens, output_tokens, total_tokens }
  error { type, message }
}
```

## 3. 路由支持矩阵

直接访问官方服务时，不是每个服务都天然提供三条路由：

| 路由 | OpenAI 原生 | Claude 原生 | Claude OpenAI 兼容层 | 网关若声明三路由全兼容 |
| --- | --- | --- | --- | --- |
| `chat` | 支持 | 不适用 | 支持核心 Chat Completions | 必须通过 |
| `responses` | 支持 | 不适用 | 不是自动等价实现 | 必须由网关单独适配 |
| `messages` | 不适用 | 支持 | 不适用 | 必须由网关单独适配 |

Anthropic 官方说明其 OpenAI SDK 兼容层主要用于测试和能力比较，并列出了部分不支持或会被忽略的字段。[OpenAI SDK 兼容性](https://platform.claude.com/docs/zh-CN/cli-sdks-libraries/libraries/openai-sdk)

所以：

- 测试 OpenAI 官方地址时，建议使用 `--routes chat,responses`。
- 测试 Claude 原生地址时，建议使用 `--routes messages`。
- 测试一个声称三路由全兼容的网关时，才使用 `--routes all`。
- 缺少某条路由时，基础检查必须失败；不能因为 404 就把整条路由标记为“跳过”。

## 4. 当前已实现的探针

当前 runner 会为每个选择的路由执行 5 个检查，共 15 个 P0 探针：

| 检查 ID | 中文名称 | 目的 |
| --- | --- | --- |
| `basic` | 基础请求 | 验证最小文本请求和成功响应结构 |
| `stream` | 流式响应 | 验证 SSE、关键事件和正常结束 |
| `tools` | 工具调用 | 验证函数工具声明、工具名和参数 JSON |
| `usage` | Usage | 验证输入/输出 token 字段 |
| `errors` | 错误格式 | 验证非法请求返回 4xx 和结构化错误 |

每一个检查都会保存脱敏后的请求、响应、HTTP 状态和耗时，报告不会保存 API Key。

## 5. P0 测试用例

### 5.1 路由与鉴权

| 编号 | 测试动作 | 通过条件 |
| --- | --- | --- |
| ROUTE-001 | `POST /v1/chat/completions` | 返回 2xx 且响应是 Chat Completion |
| ROUTE-002 | `POST /v1/responses` | 返回 2xx 且响应是 Response |
| ROUTE-003 | `POST /v1/messages` | 返回 2xx 且响应是 Message |
| ROUTE-004 | 正确 API Key | OpenAI 路由使用 Bearer；Messages 使用 `x-api-key` 和 `anthropic-version` |
| ROUTE-005 | 缺少或错误 API Key | 返回 401，不返回成功内容 |
| ROUTE-006 | 路由不存在 | 返回 404/405，基础检查失败 |
| ROUTE-007 | 非 JSON Content-Type | 返回结构化参数错误 |

### 5.2 基础请求

#### `chat`

请求至少包含：

```json
{
  "model": "MODEL",
  "messages": [
    {"role": "user", "content": "Reply with the single word pong."}
  ],
  "max_tokens": 32
}
```

响应必须满足：

- `object == "chat.completion"`
- `choices` 是非空数组
- `choices[0].message.role == "assistant"`
- assistant content 非空

#### `responses`

请求至少包含：

```json
{
  "model": "MODEL",
  "input": "Reply with the single word pong.",
  "max_output_tokens": 32
}
```

响应必须满足：

- `object == "response"`
- `id` 非空
- `output` 是数组
- output 中至少有一个 message 的文本内容
- `status` 不能是 `failed`

#### `messages`

请求至少包含：

```json
{
  "model": "MODEL",
  "max_tokens": 32,
  "messages": [
    {"role": "user", "content": "Reply with the single word pong."}
  ]
}
```

响应必须满足：

- `type == "message"`
- `role == "assistant"`
- `content` 是数组
- content 中至少有一个文本块
- `stop_reason` 存在或可以按协议处理

### 5.3 system/developer 消息

至少增加以下测试：

| 编号 | 请求 | 通过条件 |
| --- | --- | --- |
| BASIC-001 | system + user | system 指令生效 |
| BASIC-002 | developer + user | developer 指令生效 |
| BASIC-003 | 多个 system/developer | 顺序不变、内容不丢失 |
| BASIC-004 | system/developer 位于历史消息中间 | 网关按既定策略处理，不得静默丢弃 |
| BASIC-005 | 多轮 user/assistant | 角色顺序和上下文保留 |

Claude Messages 没有 `system` role，系统提示通过顶层 `system` 字段传递；Claude 的 OpenAI 兼容层会把 system/developer 消息提升并拼接到开头。这个转换必须单独测试。[Claude Messages 请求字段](https://platform.claude.com/docs/en/api/messages/create)

### 5.4 参数边界

| 编号 | 参数 | 测试内容 |
| --- | --- | --- |
| BASIC-006 | `max_tokens` | 1、32、最大值、0、负数 |
| BASIC-007 | `max_completion_tokens` | Chat 和兼容层是否接受 |
| BASIC-008 | `temperature` | 0、1、2 及非法值 |
| BASIC-009 | `top_p` | 0、1 及非法值 |
| BASIC-010 | `stop` | OpenAI 停止序列 |
| BASIC-011 | `stop_sequences` | Claude 停止序列 |
| BASIC-012 | Unicode | 中文、Emoji、换行、组合字符不乱码 |
| BASIC-013 | 长上下文 | 超限时返回明确 4xx |

### 5.5 流式响应

#### Chat Completions

必须验证：

- `Content-Type` 包含 `text/event-stream`
- 每个 `data:` 块是 JSON 或 `[DONE]`
- JSON 块包含 `choices`
- 至少收到一个 `choices[].delta.content`
- 最终收到 `data: [DONE]`

#### Responses

必须验证：

- 出现 `response.created`
- 出现至少一个 `response.output_text.delta`
- 出现 `response.completed`
- `sequence_number` 能被正常处理（如服务返回）
- `response.failed` 不能被误判为成功结束

#### Claude Messages

典型事件顺序：

```text
message_start
content_block_start
content_block_delta
content_block_stop
message_delta
message_stop
```

必须验证：

- `message_start` 和 `message_stop` 成对出现
- `content_block_delta.delta.type == "text_delta"` 可以拼接
- `ping` 不会破坏解析
- 流中的 `error` 事件会被识别为失败
- 工具参数的 `input_json_delta` 能拼接为合法 JSON

参考：[Claude Streaming](https://platform.claude.com/docs/en/build-with-claude/streaming)

### 5.6 工具调用

统一测试工具：

```json
{
  "name": "get_weather",
  "description": "Get the weather for a city.",
  "parameters": {
    "type": "object",
    "properties": {
      "city": {"type": "string"}
    },
    "required": ["city"]
  }
}
```

必须覆盖：

| 编号 | 测试内容 | 通过条件 |
| --- | --- | --- |
| TOOL-001 | 不提供 tools | 正常返回文本 |
| TOOL-002 | `tool_choice=none` | 不产生工具调用 |
| TOOL-003 | `tool_choice=auto` | 能自主选择是否调用 |
| TOOL-004 | `tool_choice=required` | 必须调用工具 |
| TOOL-005 | 强制指定 `get_weather` | 工具名准确 |
| TOOL-006 | 工具参数 | 参数是合法 JSON 对象 |
| TOOL-007 | 多个工具调用 | ID、顺序、参数分别对应 |
| TOOL-008 | 并行工具调用 | `parallel_tool_calls` 行为正确 |
| TOOL-009 | 工具结果回传 | 调用 ID 正确关联 |
| TOOL-010 | 工具返回错误 | 模型能继续或明确失败 |
| TOOL-011 | 流式工具参数 | 多个增量拼接后 JSON 完整 |

字段映射：

| OpenAI Chat | OpenAI Responses | Claude Messages |
| --- | --- | --- |
| `tool_calls[].id` | `call_id` | `tool_use.id` |
| `function.name` | `name` | `tool_use.name` |
| `function.arguments` | `arguments` | `tool_use.input` |
| `role=tool` | `function_call_output` | `tool_result` |
| `tool_call_id` | `call_id` | `tool_use_id` |

Claude 原生工具调用返回 `tool_use` 内容块，工具结果通过 `tool_result` 内容块回传。[Claude Tool Use](https://platform.claude.com/docs/en/api/messages/create)

### 5.7 Usage

| 路由 | 必须字段 |
| --- | --- |
| Chat | `prompt_tokens`、`completion_tokens`、`total_tokens` |
| Responses | `input_tokens`、`output_tokens`、`total_tokens` |
| Messages | `input_tokens`、`output_tokens`；总量由 runner 计算 |

验收规则：

- 字段必须是非负数。
- 流式 usage 只能累计一次。
- 工具调用产生的 token 也必须纳入统计。
- 多轮请求的 usage 按请求分别统计。
- 缓存字段必须保留厂商原始信息，不能伪造为普通 input token。

### 5.8 错误格式

至少执行：

| 编号 | 非法请求 | 预期 |
| --- | --- | --- |
| ERROR-001 | 缺少 model | 400 |
| ERROR-002 | 缺少 messages/input | 400 |
| ERROR-003 | 非法 role | 400 |
| ERROR-004 | 非法 max_tokens | 400 |
| ERROR-005 | 非法工具 schema | 400 |
| ERROR-006 | 错误 API Key | 401 |
| ERROR-007 | 未知模型 | 400/404 |
| ERROR-008 | 超过速率限制 | 429，读取 Retry-After |
| ERROR-009 | 请求过大 | 413 |
| ERROR-010 | 服务过载 | 5xx 或 Claude 529 |
| ERROR-011 | SSE 中途错误 | error event，不能当作正常完成 |

OpenAI 和 Claude 的错误 envelope 形状略有不同，但都必须能归一化为 `error.type` 和 `error.message`。错误测试比较状态码、类型和字段，不比较完整错误文本。[Claude API Errors](https://platform.claude.com/docs/en/api/errors)

## 6. 兼容性差异探针

如果被测服务声称“OpenAI SDK 可以直接切换到 Claude”，必须额外测试以下字段：

| 字段/能力 | Claude OpenAI 兼容层官方说明 | 处理要求 |
| --- | --- | --- |
| `strict` | 被忽略，工具 JSON 不保证严格符合 schema | 必须 FAIL/WARN，不能 PASS |
| 音频输入 | 不支持，会被忽略/剥离 | 必须明确标记不支持 |
| Prompt Cache | 不支持 | 必须明确标记不支持 |
| `response_format` | 被忽略 | 不能宣称结构化输出兼容 |
| `seed` | 被忽略 | 不能宣称确定性兼容 |
| `logprobs` | 被忽略 | 标记不支持 |
| `presence_penalty` | 被忽略 | 标记不支持 |
| `frequency_penalty` | 被忽略 | 标记不支持 |
| thinking 详细过程 | OpenAI SDK 不返回 | 使用 Claude 原生 Messages 测试 |
| PDF、引用、文档块 | Claude 原生能力 | 不纳入 OpenAI Chat 核心兼容 |

完全兼容的网关应在以下两种行为中选择一种：

1. 真正转换并保留语义；或
2. 返回明确的“不支持”错误。

直接忽略有语义的字段，属于兼容性缺陷。

## 7. P1/P2 扩展用例

### P1：建议在核心通过后执行

- 多个 system/developer 消息的顺序转换。
- 多轮工具调用和工具结果回传。
- 并行工具调用。
- 流式工具参数分片和断流。
- 图片 URL、Base64 图片、不同图片格式。
- JSON Schema 输出。
- 超长请求、413、429、529 和 Retry-After。
- 连接超时、服务端 5xx、客户端取消请求。
- 请求 ID 和限流响应头。
- OpenAI SDK、Anthropic SDK 双客户端解析回归。

### P2：厂商专属能力

- OpenAI Responses 内置 web search、file search、code interpreter。
- Claude thinking、redacted thinking 和 signature round-trip。
- Claude PDF、文档块、引用。
- Prompt caching。
- 音频输入输出。
- logprobs、moderation、background response。

这些能力不应被硬塞进跨厂商公共协议；应进入厂商能力矩阵，并有独立的 native route 测试。

## 8. 运行方式

### CLI

OpenAI 官方接口：

```bash
LLMCONFORM_API_KEY="$OPENAI_API_KEY" \
LLMCONFORM_BASE_URL="https://api.openai.com/v1" \
LLMCONFORM_MODEL="gpt-5" \
go run . check --routes chat,responses --format table
```

Claude 原生 Messages：

```bash
LLMCONFORM_API_KEY="$ANTHROPIC_API_KEY" \
LLMCONFORM_BASE_URL="https://api.anthropic.com/v1" \
LLMCONFORM_MODEL="claude-sonnet-4-6" \
go run . check --routes messages --format table
```

三路由网关：

```bash
go run . check \
  --base-url "https://gateway.example.com" \
  --model "gateway-model" \
  --routes all \
  --format json
```

### Web 服务

```bash
go run . serve --addr 127.0.0.1:8080
```

浏览器打开 `http://127.0.0.1:8080`，填写地址、模型和 API Key，即可执行检查并下载 JSON 报告。

服务接口：

| 方法 | 路径 | 作用 |
| --- | --- | --- |
| `GET` | `/healthz` | 健康检查 |
| `POST` | `/api/runs` | 创建异步检查 |
| `GET` | `/api/runs/{id}` | 获取当前报告 |
| `GET` | `/api/runs/{id}/events` | 订阅 SSE 进度 |
| `GET` | `/api/runs/{id}/report` | 下载 JSON 报告 |

## 9. 结果解释

| 状态 | 含义 |
| --- | --- |
| `PASS` | 协议检查通过 |
| `WARN` | 基本可用，但字段或能力不完整 |
| `FAIL` | 路由、协议结构或关键语义不兼容 |
| `SKIP` | 明确声明本次不测试，不代表兼容 |
| `COMPLETE` | 整次运行结束；是否通过看 `summary.fail` |

建议 CI 使用：

```text
summary.fail == 0  -> 允许合并
summary.fail > 0   -> 阻断发布
summary.warn > 0   -> 需要人工确认
```

## 10. 维护原则

1. 新增协议字段时，先更新本文件的路由矩阵和字段映射，再增加探针。
2. 每个探针都必须有成功样例和失败样例。
3. 所有真实响应都要限制保存大小，报告中不得出现 API Key。
4. 不比较不同厂商的完整自然语言答案；比较结构和归一化语义。
5. 官方文档新增字段时，未知 SSE 事件应被安全忽略，同时保留原始响应用于排查。
6. 兼容层静默忽略字段时，必须通过差异探针暴露出来。
