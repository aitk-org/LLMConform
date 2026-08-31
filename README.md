# LLMConform

LLMConform 是一个面向 LLM 网关的协议兼容性检查工具，当前覆盖：

- OpenAI Chat Completions：`POST /v1/chat/completions`
- OpenAI Responses：`POST /v1/responses`
- Claude Messages：`POST /v1/messages`

每个路由会执行基础请求、SSE 流式响应、工具调用、Usage 和错误格式 5 项检查，并输出表格或 JSON 报告。

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
  --routes chat,responses
```

只测 Claude 原生 Messages：

```bash
go run . check \
  --base-url "https://api.anthropic.com/v1" \
  --model "claude-sonnet-4-6" \
  --routes messages
```

## Web

```bash
go run . serve --addr 127.0.0.1:8080
```

浏览器打开 `http://127.0.0.1:8080`，填写服务地址、模型和 API Key 即可运行检查，并下载 JSON 报告。模型既可以直接手填，也可以通过目标服务的 `GET /v1/models` 获取候选列表。

页面会在运行前展示实际执行的 5 个测试用例：基础文本、SSE 流式响应、工具调用、Usage 计量和错误响应。每个选中的协议路由都会执行完整的一组用例。

## 开发验证

```bash
GOCACHE=/tmp/llmconform-go-cache go test ./...
GOCACHE=/tmp/llmconform-go-cache go vet ./...
```

测试使用本地 mock HTTP 服务验证三路由、鉴权、SSE、工具调用、Usage 和错误结构，不需要真实 API Key。
