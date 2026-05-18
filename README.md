# SeedClaw

A self-growing agent pipeline from a single `seed.md`.

## Design

SeedClaw has two primitives: `shell` and `agent`.

- **`shell`** — execute a whitelisted shell command. Only `curl` is allowed by default. You add what you need.
- **`agent`** — spawn a sub-agent. Sub-agents run independently and return their result.

Every agent in the tree uses these two tools. Nothing else. No built-in file readers, web browsers, or code interpreters. Everything is built from `shell` and `agent`.

Agents are defined by directories. A directory containing `system.md` is an agent. A directory containing `agents/` can spawn sub-agents. Sub-agents can have their own `agents/`. The tree can be arbitrarily deep.

## Quick start

### 1. Install

```bash
git clone https://github.com/yourname/seedclaw.git
cd seedclaw
go build -o seedclaw .
```

### 2. Configure

Create config.toml:
```toml
[llm]
api_key = "sk-your-key-here"
base_url = "https://api.openai.com/v1"
model = "gpt-4o"
max_steps = 10

[agent]
root = "."
workspace = "./workspace"

[shell]
allowed_cmd = [
    "curl"
]
```

### 3. Create your first agent

Write system.md:
```markdown
You are SeedClaw, an autonomous AI agent.

## Available Sub-Agents
{{AGENTS}}
```

### 4. Run

```bash
./seedclaw "What's the weather in Tokyo?"
```

Or interactive mode:

```bash
./seedclaw
> your task here
> /quit
```

### 5. Create a sub-agent

```bash
mkdir -p agents/fetch_url
```

Write agents/fetch_url/api.md:

```markdown
# fetch_url
Fetch content from a URL.
Example:
agent("fetch_url", "https://api.example.com/data")
```

Write agents/fetch_url/system.md:

```markdown
You are an HTTP request specialist. Use curl to fetch URL content.

## Available Sub-Agents
{{AGENTS}}

## Method
curl -s <URL>
Output `fetched content` when done.
```

The sub-agent is available immediately. No restart required.


## Runtime structure

```
your-agent/
├── config.toml
├── system.md              # Agent's system prompt
├── agents/                # Available sub-agents
│   └── fetch_url/
│       ├── api.md         # Public interface
│       ├── system.md      # Sub-agent's own prompt
│       └── agents/        # (optional) Deeper sub-agents
└── workspace/
   └── <session_id>/      # Session workspace
```

## Configuration

| Key | Default | Sample |
| --- | --- | --- |
| llm.api_key       | LLM API key              | (required) |
| llm.base_url      | API endpoint             | https://api.openai.com/v1 |
| llm.model         | Model name               | gpt-4o |
| llm.max_steps     | Max steps per agent loop | 10 | 
| agent.root        | Agent root directory     | . | 
| agent.workspace   | Workspace path           | ./workspace |
| shell.allowed_cmd | Allowed shell commands   | ["curl"] |
