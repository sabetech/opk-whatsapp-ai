// Command toolcheck runs agent tools directly against PocketBase, without the
// LLM, for development and debugging.
//
//	go run ./cmd/toolcheck                      # list tools
//	go run ./cmd/toolcheck sales_summary '{"group_by":"product"}'
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"opk-whatsapp-ai/internal/config"
	"opk-whatsapp-ai/internal/pocketbase"
	"opk-whatsapp-ai/internal/tools"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}

	pb := pocketbase.NewClient(cfg.PocketBaseURL)
	env := tools.NewEnv(pb, tools.NewCollectionAccess(cfg.AllowedCollections), cfg.Location)
	registry := tools.NewRegistry()
	for _, t := range tools.All(env) {
		if err := registry.Register(t); err != nil {
			log.Fatal(err)
		}
	}

	if len(os.Args) < 2 {
		for _, d := range registry.Definitions() {
			fmt.Printf("%-28s %s\n", d.Function.Name, d.Function.Description)
		}
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if err := pb.Authenticate(ctx, cfg.PocketBaseEmail, cfg.PocketBasePassword); err != nil {
		log.Fatal(err)
	}

	args := "{}"
	if len(os.Args) > 2 {
		args = os.Args[2]
	}

	start := time.Now()
	result, err := registry.Execute(ctx, os.Args[1], json.RawMessage(args))
	if err != nil {
		log.Fatalf("error: %v", err)
	}

	out, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(out))
	fmt.Fprintf(os.Stderr, "(%s)\n", time.Since(start).Round(time.Millisecond))
}
