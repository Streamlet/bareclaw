package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"seedclaw/core"
	"strings"
)

func main() {
	configPath := flag.String("c", "config.toml", "path to config file")
	flag.Parse()

	cfg, err := core.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	sessionID := core.GenerateSessionID()
	log.Printf("[session=%s] Session started", sessionID)

	rootAgent, err := core.LoadAgent(cfg.Agent.Root, cfg, sessionID)
	if err != nil {
		log.Fatalf("Failed to load root agent: %v", err)
	}

	args := flag.Args()
	if len(args) > 0 {
		// Single-run mode
		task := strings.Join(args, " ")
		result, err := rootAgent.Run(task)
		if err != nil {
			log.Fatalf("Agent error: %v", err)
		}
		fmt.Println(result)
	} else {
		// Interactive mode
		fmt.Printf("SeedClaw interactive mode (session: %s). Type /quit to exit.\n", sessionID)
		scanner := bufio.NewScanner(os.Stdin)
		for {
			fmt.Print("> ")
			if !scanner.Scan() {
				break
			}
			line := strings.TrimSpace(scanner.Text())
			if line == "/quit" {
				break
			}
			if line == "" {
				continue
			}
			result, err := rootAgent.Run(line)
			if err != nil {
				fmt.Printf("Error: %v\n", err)
			} else {
				fmt.Println(result)
			}
		}
	}
}
