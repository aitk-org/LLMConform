# LLMConform

LLMConform 是一个面向 LLM 网关的协议兼容性检查工具，当前覆盖：

- OpenAI Chat Completions：`POST /v1/chat/completions`
- OpenAI Responses：`POST /v1/responses`
- Claude Messages：`POST /v1/messages`

测试不再把一整类能力压缩成单个 PASS/FAIL。运行器会先生成测试计划，再按“路由 → 能力 → 场景 → 原子断言”执行；基础场景失败时，依赖它的场景会标记为 `BLOCKED`，不会继续制造重复失败或无意义费用。

测试深度：

- `quick`：路由存在、最小非流式响应与 Usage；
- `standard`：增加 SSE 状态机、强制工具调用和 4 类参数错误；
- `full`：再增加系统指令与多轮上下文兼容性。

详细测试矩阵、字段映射、兼容性差异和 P0/P1/P2 用例见：[兼容性测试用例](docs/compatibility-test-cases.md)

## CLI

```bash
LLMCONFORM_API_KEY="$API_KEY" \
LLMCONFORM_BASE_URL="https://gateway.example.com" \
LLMCONFORM_MODEL="model-name" \
go run . check --routes all --format table
```

只测 OpenAI 原生路由：

```bash
go run . check \
  --base-url "https://api.openai.com/v1" \
  --model "gpt-5" \
  --profile openai \
  --level standard \
  --routes chat,responses
```

只测 Claude 原生 Messages：

```bash
go run . check \
  --base-url "https://api.anthropic.com/v1" \
  --model "claude-sonnet-4-6" \
  --profile claude \
  --routes messages
```

## Web

```bash
go run . serve --addr 127.0.0.1:8080
```

浏览器打开 `http://127.0.0.1:8080`。界面按四个阶段工作：

1. 验证目标地址、鉴权和可选的 `GET /v1/models`；
2. 选择模型、服务类型、测试深度与路由，并审阅实际测试计划和调用预算；
3. 实时查看场景和断言进度；
4. 按路由和能力查看结论，下钻到每条断言及请求/响应证据。

只有运行完成后才能下载 JSON 报告。API Key 只用于当前运行，不写入报告或磁盘。

## 判定重点

- 非流式响应严格验证对象类型、ID、角色、终止原因和整数 Usage；Chat/Responses 还验证 token 合计；
- Chat 流验证稳定 ID、文本 delta、finish reason、最终 `[DONE]` 和 Usage；
- Responses 流验证 created、item/content 生命周期、sequence、completed 与失败终态；
- Messages 流验证 block index 生命周期、text delta、usage、message stop 与流内 error；
- 工具调用验证调用类型、ID、名称、参数 JSON 和 `city` Schema；
- 参数反例分别覆盖缺少 model、缺少输入、非法角色和非法 token 上限。

## 开发验证

```bash
GOCACHE=/tmp/llmconform-go-cache go test ./...
GOCACHE=/tmp/llmconform-go-cache go vet ./...
```

测试使用会严格检查请求方法、鉴权头和请求字段的本地 mock HTTP 服务，覆盖三路由正向流程、错误矩阵与流式负例，不需要真实 API Key。
