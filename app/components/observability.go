package components

import (
	"fmt"
	"strings"

	"github.com/vango-go/vai-lite/internal/services"
	"github.com/vango-go/vango"
	. "github.com/vango-go/vango/el"
)

func RenderObservabilityDetail(detail *services.GatewayRequestDetail) *vango.VNode {
	if detail == nil {
		return nil
	}
	metaLines := []string{
		fmt.Sprintf("Request ID: %s", detail.RequestID),
		fmt.Sprintf("Endpoint: %s (%s)", detail.EndpointKind, detail.Path),
		fmt.Sprintf("Model: %s", detail.Model),
		fmt.Sprintf("Provider: %s", firstNonEmpty(detail.Provider, "n/a")),
		fmt.Sprintf("Access credential: %s", accessCredentialLabel(detail.AccessCredential)),
		fmt.Sprintf("Key source: %s", keySourceLabel(detail.KeySource)),
		fmt.Sprintf("Status: %s", observabilityStatusLabel(detail.StatusCode, detail.ErrorSummary != "")),
		fmt.Sprintf("Duration: %d ms", detail.DurationMS),
		fmt.Sprintf("API key: %s (%s)", firstNonEmpty(detail.GatewayAPIKeyName, detail.GatewayAPIKeyPrefix), detail.GatewayAPIKeyPrefix),
	}
	if detail.SessionID != "" {
		metaLines = append(metaLines, fmt.Sprintf("Session: %s", detail.SessionID))
	}
	metaLines = append(metaLines, fmt.Sprintf("Chain: %s", detail.ChainID))
	if detail.ParentRequestID != "" {
		metaLines = append(metaLines, fmt.Sprintf("Parent request: %s", detail.ParentRequestID))
	}
	if detail.InputContextFingerprint != "" {
		metaLines = append(metaLines, fmt.Sprintf("Input fingerprint: %s", detail.InputContextFingerprint))
	}
	if detail.OutputContextFingerprint != "" {
		metaLines = append(metaLines, fmt.Sprintf("Output fingerprint: %s", detail.OutputContextFingerprint))
	}
	if detail.ErrorSummary != "" {
		metaLines = append(metaLines, fmt.Sprintf("Error: %s", detail.ErrorSummary))
	}

	return Div(
		Class("observability-detail"),
		H2(Text("Request detail")),
		Div(
			Class("detail-grid"),
			Article(
				Class("settings-panel inset"),
				H3(Text("Correlation")),
				Pre(Class("code-block"), Text(strings.Join(metaLines, "\n"))),
			),
			Article(
				Class("settings-panel inset"),
				H3(Text("Request summary")),
				Pre(Class("code-block"), Text(detail.RequestSummary)),
			),
			Article(
				Class("settings-panel inset"),
				H3(Text("Response summary")),
				Pre(Class("code-block"), Text(detail.ResponseSummary)),
			),
			Article(
				Class("settings-panel inset"),
				H3(Text("Request body")),
				Pre(Class("code-block code-block-tall"), Text(detail.RequestBody)),
			),
			Article(
				Class("settings-panel inset"),
				H3(Text("Response body")),
				Pre(Class("code-block code-block-tall"), Text(detail.ResponseBody)),
			),
			If(detail.ErrorJSON != "" && detail.ErrorJSON != "{}",
				Article(
					Class("settings-panel inset"),
					H3(Text("Error JSON")),
					Pre(Class("code-block"), Text(detail.ErrorJSON)),
				),
			),
		),
		If(detail.RunTrace != nil,
			Div(
				Class("stack"),
				H2(Text("Run trace")),
				Article(
					Class("settings-panel inset"),
					H3(Text("Run summary")),
					Pre(Class("code-block"), Text(fmt.Sprintf("Stop reason: %s\nTurns: %d\nTool calls: %d\nRun config: %s\nUsage: %s",
						detail.RunTrace.StopReason,
						detail.RunTrace.TurnCount,
						detail.RunTrace.ToolCallCount,
						detail.RunTrace.RunConfig,
						detail.RunTrace.Usage,
					))),
				),
				Div(
					Class("table-stack"),
					RangeKeyed(detail.RunTrace.Steps,
						func(item services.GatewayRunStepDetail) any { return item.ID },
						func(item services.GatewayRunStepDetail) *vango.VNode {
							return Article(
								Class("settings-panel inset"),
								H3(Textf("Step %d", item.StepIndex)),
								P(Textf("Duration: %d ms", item.DurationMS)),
								Pre(Class("code-block"), Text(item.ResponseSummary)),
								If(item.ResponseBody != "",
									Pre(Class("code-block code-block-tall"), Text(item.ResponseBody)),
								),
								If(len(item.ToolCalls) > 0,
									Div(
										Class("stack"),
										H4(Text("Tool calls")),
										RangeKeyed(item.ToolCalls,
											func(tool services.GatewayRunToolCall) any { return tool.ID },
											func(tool services.GatewayRunToolCall) *vango.VNode {
												return Article(
													Class("card-row"),
													Div(
														Strong(Text(tool.Name)),
														P(Textf("Tool call: %s", tool.ToolCallID)),
														If(tool.ErrorSummary != "",
															P(Class("error-copy"), Text(tool.ErrorSummary)),
														),
													),
													Div(
														Class("stack observability-tool-payloads"),
														Pre(Class("code-block"), Text(tool.InputJSON)),
														If(tool.ResultBody != "",
															Pre(Class("code-block"), Text(tool.ResultBody)),
														),
													),
												)
											},
										),
									),
								),
							)
						},
					),
				),
			),
		),
	)
}
