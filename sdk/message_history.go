package vai

import "github.com/vango-go/vai-lite/pkg/core/types"

// AppendMessage appends a single message to history.
func AppendMessage(history []types.Message, role string, content any) []types.Message {
	return append(history, types.Message{Role: role, Content: content})
}

// AppendUserMessage appends a user message to history.
func AppendUserMessage(history []types.Message, content any) []types.Message {
	return AppendMessage(history, "user", content)
}

// AppendAssistantMessage appends an assistant message to history.
func AppendAssistantMessage(history []types.Message, content any) []types.Message {
	return AppendMessage(history, "assistant", content)
}

// ToolResultsToBlocks converts tool execution results to tool_result blocks.
func ToolResultsToBlocks(toolResults []ToolExecutionResult) []types.ContentBlock {
	if len(toolResults) == 0 {
		return nil
	}
	blocks := make([]types.ContentBlock, 0, len(toolResults))
	for _, tr := range toolResults {
		blocks = append(blocks, types.ToolResultBlock{
			Type:      "tool_result",
			ToolUseID: tr.ToolUseID,
			Content:   tr.Content,
			IsError:   tr.Error != nil,
		})
	}
	return blocks
}

// AppendToolResultsMessage appends a user tool_result message to history.
func AppendToolResultsMessage(history []types.Message, toolResults []ToolExecutionResult) []types.Message {
	blocks := ToolResultsToBlocks(toolResults)
	if len(blocks) == 0 {
		return history
	}
	return AppendUserMessage(history, blocks)
}
