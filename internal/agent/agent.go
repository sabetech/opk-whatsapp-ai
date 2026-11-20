package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"opk-whatsapp-ai/internal/llm"
	"opk-whatsapp-ai/internal/tools"
)

const maxToolRounds = 5
const maxToolCallsPerRound = 5

type Agent struct {
	llmClient *llm.Client
	registry  *tools.Registry
}

func New(llmClient *llm.Client, registry *tools.Registry) *Agent {
	return &Agent{
		llmClient: llmClient,
		registry:  registry,
	}
}

func (a *Agent) Run(ctx context.Context, userMessage string) (string, error) {
	messages := []llm.Message{
		{
			Role:    "system",
			Content: SystemPrompt,
		},
		{
			Role:    "user",
			Content: userMessage,
		},
	}

	definitions := a.registry.Definitions()

	for range maxToolRounds {
		response, err := a.llmClient.ChatWithTools(
			ctx,
			messages,
			definitions,
		)
		if err != nil {
			return "", fmt.Errorf("get LLM response: %w", err)
		}

		// The model has finished responding.
		if len(response.ToolCalls) == 0 {
			return response.Content, nil
		}

		// Preserve the assistant's tool-call message in the conversation.
		messages = append(messages, llm.Message{
			Role:      "assistant",
			Content:   response.Content,
			ToolCalls: response.ToolCalls,
		})

		if len(response.ToolCalls) > maxToolCallsPerRound {
			return "", fmt.Errorf("LLM requested too many tool calls in one round")
		}

		// Execute each requested tool.
		for _, call := range response.ToolCalls {
			result, err := a.registry.Execute(
				ctx,
				call.Function.Name,
				json.RawMessage(call.Function.Arguments),
			)

			var toolResult any

			if err != nil {
				toolResult = map[string]string{
					"error": err.Error(),
				}
			} else {
				toolResult = result
			}

			resultJSON, marshalErr := json.Marshal(toolResult)
			if marshalErr != nil {
				resultJSON = []byte(`{"error":"Unable to encode tool result"}`)
			}

			// Send the tool result back to the model.
			messages = append(messages, llm.Message{
				Role:       "tool",
				ToolCallID: call.ID,
				Content:    string(resultJSON),
			})
		}
	}

	return "", fmt.Errorf("agent exceeded maximum tool-calling rounds")
}
