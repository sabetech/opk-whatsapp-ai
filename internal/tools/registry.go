package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"opk-whatsapp-ai/internal/llm"
)

type Handler func(ctx context.Context, arguments json.RawMessage) (any, error)

type Tool struct {
	Definition llm.ToolDefinition
	Handler    Handler
}

type Registry struct {
	tools map[string]Tool
}

func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]Tool),
	}
}

func (r *Registry) Register(tool Tool) error {
	name := tool.Definition.Function.Name

	if name == "" {
		return fmt.Errorf("tool name cannot be empty")
	}

	if tool.Handler == nil {
		return fmt.Errorf("tool %q has no handler", name)
	}

	if _, exists := r.tools[name]; exists {
		return fmt.Errorf("tool %q is already registered", name)
	}

	r.tools[name] = tool
	return nil
}

func (r *Registry) Definitions() []llm.ToolDefinition {
	definitions := make([]llm.ToolDefinition, 0, len(r.tools))

	for _, tool := range r.tools {
		definitions = append(definitions, tool.Definition)
	}

	// Stable order keeps prompts deterministic across requests.
	sort.Slice(definitions, func(i, j int) bool {
		return definitions[i].Function.Name < definitions[j].Function.Name
	})

	return definitions
}

func (r *Registry) Execute(
	ctx context.Context,
	name string,
	arguments json.RawMessage,
) (any, error) {
	tool, exists := r.tools[name]
	if !exists {
		return nil, fmt.Errorf("unknown tool: %s", name)
	}

	if len(arguments) == 0 {
		arguments = json.RawMessage(`{}`)
	}

	if !json.Valid(arguments) {
		return nil, fmt.Errorf("invalid JSON arguments for tool %q", name)
	}

	return tool.Handler(ctx, arguments)
}
