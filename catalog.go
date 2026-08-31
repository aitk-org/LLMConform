package main

import "fmt"

const catalogVersion = "2026-08-31.1"

type caseKind string

const (
	caseRoute        caseKind = "route"
	caseBasic        caseKind = "basic"
	caseSystem       caseKind = "system"
	caseMultiTurn    caseKind = "multi_turn"
	caseStreamText   caseKind = "stream_text"
	caseToolsForced  caseKind = "tools_forced"
	caseMissingModel caseKind = "missing_model"
	caseMissingInput caseKind = "missing_input"
	caseInvalidRole  caseKind = "invalid_role"
	caseInvalidLimit caseKind = "invalid_limit"
)

type assertionDefinition struct {
	ID       string
	Name     string
	Severity string
}

type caseDefinition struct {
	ID              string
	Name            string
	Description     string
	Capability      string
	Level           string
	Kind            caseKind
	DependsOn       []string
	Assertions      []assertionDefinition
	ModelCalls      int
	MaxOutputTokens int
}

func BuildRunPlan(cfg RunConfig) (RunPlan, error) {
	if err := cfg.Validate(); err != nil {
		return RunPlan{}, err
	}
	plan := RunPlan{
		CatalogVersion: catalogVersion,
		Profile:        cfg.Profile,
		Level:          cfg.Level,
		BaseURL:        cfg.BaseURL,
		Model:          cfg.Model,
		Routes:         append([]string(nil), cfg.Routes...),
	}
	for _, routeID := range cfg.Routes {
		protocol := protocolByID(routeID)
		for _, definition := range catalogForRoute(routeID) {
			if levelRank(definition.Level) > levelRank(cfg.Level) {
				continue
			}
			item := PlannedCase{
				ID:              definition.ID,
				Name:            definition.Name,
				Description:     definition.Description,
				RouteID:         routeID,
				RouteName:       protocol.Name,
				Capability:      definition.Capability,
				Level:           definition.Level,
				DependsOn:       append([]string(nil), definition.DependsOn...),
				ModelCalls:      definition.ModelCalls,
				MaxOutputTokens: definition.MaxOutputTokens,
			}
			for _, assertion := range definition.Assertions {
				item.Assertions = append(item.Assertions, AssertionPlan{
					ID:       assertion.ID,
					Name:     assertion.Name,
					Severity: assertion.Severity,
				})
			}
			plan.Cases = append(plan.Cases, item)
			plan.AssertionCount += len(item.Assertions)
			plan.ModelCalls += item.ModelCalls
			plan.MaxOutputTokens += item.MaxOutputTokens
		}
	}
	plan.ScenarioCount = len(plan.Cases)
	return plan, nil
}

func catalogForRoute(routeID string) []caseDefinition {
	prefix := routeID + "."
	routeCase := prefix + "route"
	basicCase := prefix + "basic"
	definitions := []caseDefinition{
		{
			ID:          routeCase,
			Name:        "路由存在",
			Description: "用不产生模型输出的最小请求确认声明的路由已注册。",
			Capability:  CapabilityRoute,
			Level:       LevelQuick,
			Kind:        caseRoute,
			Assertions: requiredAssertions(
				assertion("http.route_exists", "路由不是 404/405"),
			),
		},
		{
			ID:              basicCase,
			Name:            "基础响应",
			Description:     "执行最小非流式请求，并同时验证响应结构与 Usage。",
			Capability:      CapabilityBasic,
			Level:           LevelQuick,
			Kind:            caseBasic,
			DependsOn:       []string{routeCase},
			Assertions:      basicAssertions(routeID),
			ModelCalls:      1,
			MaxOutputTokens: 32,
		},
		{
			ID:              prefix + "stream.text",
			Name:            "流式文本",
			Description:     "验证 SSE 语法、协议事件生命周期、文本聚合和成功终态。",
			Capability:      CapabilityStream,
			Level:           LevelStandard,
			Kind:            caseStreamText,
			DependsOn:       []string{basicCase},
			Assertions:      streamAssertions(routeID),
			ModelCalls:      1,
			MaxOutputTokens: 32,
		},
		{
			ID:              prefix + "tools.forced",
			Name:            "强制工具调用",
			Description:     "强制调用 get_weather，并验证调用 ID、类型、名称和参数 Schema。",
			Capability:      CapabilityTools,
			Level:           LevelStandard,
			Kind:            caseToolsForced,
			DependsOn:       []string{basicCase},
			Assertions:      toolAssertions(routeID),
			ModelCalls:      1,
			MaxOutputTokens: 64,
		},
		{
			ID:              prefix + "context.system",
			Name:            "系统指令",
			Description:     "按协议各自的字段携带系统指令，验证请求被接受且返回标准响应。",
			Capability:      CapabilityContext,
			Level:           LevelFull,
			Kind:            caseSystem,
			DependsOn:       []string{basicCase},
			Assertions:      basicAssertions(routeID),
			ModelCalls:      1,
			MaxOutputTokens: 32,
		},
		{
			ID:              prefix + "context.multi_turn",
			Name:            "多轮上下文",
			Description:     "发送包含 user/assistant 历史的多轮输入，验证角色序列和响应结构。",
			Capability:      CapabilityContext,
			Level:           LevelFull,
			Kind:            caseMultiTurn,
			DependsOn:       []string{basicCase},
			Assertions:      basicAssertions(routeID),
			ModelCalls:      1,
			MaxOutputTokens: 32,
		},
	}

	for _, invalid := range []struct {
		kind        caseKind
		id          string
		name        string
		description string
	}{
		{caseMissingModel, "error.missing_model", "缺少 model", "缺少 model 时必须返回协议化参数错误。"},
		{caseMissingInput, "error.missing_input", "缺少输入", "缺少 messages/input 时必须返回协议化参数错误。"},
		{caseInvalidRole, "error.invalid_role", "非法角色", "消息角色或消息结构非法时必须拒绝请求。"},
		{caseInvalidLimit, "error.invalid_limit", "非法 token 上限", "token 上限为负数时必须拒绝请求。"},
	} {
		definitions = append(definitions, caseDefinition{
			ID:          prefix + invalid.id,
			Name:        invalid.name,
			Description: invalid.description,
			Capability:  CapabilityErrors,
			Level:       LevelStandard,
			Kind:        invalid.kind,
			DependsOn:   []string{basicCase},
			Assertions: requiredAssertions(
				assertion("http.error_status", "HTTP 400 参数错误"),
				assertion("http.json", "响应是 JSON"),
				assertion("error.envelope", "错误 envelope 符合协议"),
				assertion("error.type", "error.type 非空"),
				assertion("error.message", "error.message 非空"),
			),
		})
	}
	return definitions
}

func basicAssertions(routeID string) []assertionDefinition {
	common := []assertionDefinition{
		required("http.success", "HTTP 返回 2xx"),
		required("http.json", "Content-Type 是 JSON"),
		required("json.object", "响应是 JSON 对象"),
	}
	switch routeID {
	case RouteChat:
		return append(common, requiredAssertions(
			assertion("response.object", "object 是 chat.completion"),
			assertion("response.id", "响应 ID 非空"),
			assertion("response.choices", "choices 非空"),
			assertion("message.role", "消息角色是 assistant"),
			assertion("message.content", "消息文本非空"),
			assertion("response.finish_reason", "finish_reason 合法"),
			assertion("usage.fields", "Usage 字段是非负整数"),
			assertion("usage.total", "Usage 合计一致"),
		)...)
	case RouteResponses:
		return append(common, requiredAssertions(
			assertion("response.object", "object 是 response"),
			assertion("response.id", "响应 ID 非空"),
			assertion("response.status", "status 是 completed"),
			assertion("response.output_text", "output 包含文本"),
			assertion("usage.fields", "Usage 字段是非负整数"),
			assertion("usage.total", "Usage 合计一致"),
		)...)
	case RouteMessages:
		return append(common, requiredAssertions(
			assertion("response.type", "type 是 message"),
			assertion("response.id", "响应 ID 非空"),
			assertion("message.role", "消息角色是 assistant"),
			assertion("message.content", "content 包含文本"),
			assertion("response.stop_reason", "stop_reason 合法"),
			assertion("usage.fields", "Usage 字段是非负整数"),
		)...)
	default:
		panic(fmt.Sprintf("unknown route %q", routeID))
	}
}

func streamAssertions(routeID string) []assertionDefinition {
	common := []assertionDefinition{
		required("http.success", "HTTP 返回 2xx"),
		required("http.sse", "Content-Type 是 text/event-stream"),
		required("sse.syntax", "SSE 语法可解析"),
	}
	switch routeID {
	case RouteChat:
		return append(common,
			required("stream.chunk", "每个数据块都是 Chat chunk"),
			required("stream.id", "响应 ID 非空且稳定"),
			required("stream.content", "包含文本增量"),
			required("stream.finish", "finish_reason 合法"),
			required("stream.done", "[DONE] 正确终止"),
			advisory("stream.usage", "流式 Usage 可用"),
		)
	case RouteResponses:
		return append(common,
			required("stream.created", "response.created 首先出现"),
			advisory("stream.lifecycle", "item/content 生命周期完整"),
			required("stream.content", "包含 output_text.delta"),
			advisory("stream.sequence", "sequence_number 单调递增"),
			required("stream.completed", "response.completed 正确终止"),
			required("stream.terminal", "没有失败或不完整终态"),
		)
	case RouteMessages:
		return append(common,
			required("stream.message_start", "message_start 首先出现"),
			required("stream.blocks", "内容块生命周期与索引合法"),
			required("stream.content", "包含 text_delta"),
			advisory("stream.usage", "message_delta Usage 可用"),
			required("stream.message_stop", "message_stop 正确终止"),
			required("stream.no_error", "流内没有 error 事件"),
		)
	default:
		panic(fmt.Sprintf("unknown route %q", routeID))
	}
}

func toolAssertions(routeID string) []assertionDefinition {
	items := []assertionDefinition{
		required("http.success", "HTTP 返回 2xx"),
		required("http.json", "Content-Type 是 JSON"),
		required("tool.type", "工具调用类型正确"),
		required("tool.id", "工具调用 ID 非空"),
		required("tool.name", "工具名是 get_weather"),
		required("tool.arguments", "工具参数是 JSON 对象"),
		required("tool.schema", "工具参数符合 JSON Schema"),
		required("tool.finish", "工具调用终止原因正确"),
	}
	if routeID == RouteResponses {
		items = append(items, required("tool.call_id", "call_id 非空"))
	}
	return items
}

func assertion(id, name string) assertionDefinition {
	return assertionDefinition{ID: id, Name: name}
}

func required(id, name string) assertionDefinition {
	return assertionDefinition{ID: id, Name: name, Severity: SeverityRequired}
}

func advisory(id, name string) assertionDefinition {
	return assertionDefinition{ID: id, Name: name, Severity: SeverityAdvisory}
}

func requiredAssertions(items ...assertionDefinition) []assertionDefinition {
	for index := range items {
		items[index].Severity = SeverityRequired
	}
	return items
}

func levelRank(level string) int {
	switch level {
	case LevelQuick:
		return 0
	case LevelStandard:
		return 1
	case LevelFull:
		return 2
	default:
		return 99
	}
}

func findCaseDefinition(routeID, caseID string) (caseDefinition, bool) {
	for _, definition := range catalogForRoute(routeID) {
		if definition.ID == caseID {
			return definition, true
		}
	}
	return caseDefinition{}, false
}
