package llm

import (
	"fmt"
	"strings"

	"github.com/sashabaranov/go-openai"
)

// BuildMessages creates the messages for the OpenAI chat completion API.
func BuildMessages(req GenerateRequest) []openai.ChatCompletionMessage {
	allowedPrefixes := strings.Join(req.Constraints.AllowedBaseImagePrefixes, ", ")
	sysMsg := fmt.Sprintf(`You are a senior DevOps engineer specializing in Docker. Your sole purpose is to generate production-ready Dockerfiles for arbitrary code repositories.
You MUST output ONLY a JSON object with this exact schema:
{
  "dockerfile": "<full Dockerfile contents as a single string>",
  "expose_port": <integer>,
  "build_args": { "key": "value" },
  "support_files": { "<relative/path/to/file>": "<full file contents>" },
  "reasoning": "<one short paragraph explaining your choices>"
}
CONSTRAINTS - VIOLATING THESE WILL CAUSE THE BUILD TO BE REJECTED:

Use ONLY base images starting with: %s
NEVER use USER root in the final stage — create a non-root user
NEVER use ADD with http:// or https:// URLs
NEVER use --privileged flag
NEVER curl-pipe-bash or wget-pipe-sh installations
NEVER reference files via COPY or ADD that are not present in either the FILE TREE below OR your "support_files" map. If you need an auxiliary config (e.g. nginx.conf, default.conf, supervisord.conf) that is missing from FILE TREE, you MUST emit its full contents under "support_files" with the SAME relative path you COPY in the Dockerfile. The platform writes "support_files" into the build context before docker build runs.
Keep support_files paths relative to the repo root (no leading "/", no "..").
Always use multi-stage builds when build artifacts can be separated from runtime
Always EXPOSE the port the application listens on
Always include health-check-friendly defaults (HOSTNAME=0.0.0.0, PORT env)

Output ONLY the JSON object — no markdown code blocks, no explanations outside the JSON.`, allowedPrefixes)

	fileTreeList := strings.Join(req.FileTree, "\n")
	var keyFilesContent strings.Builder
	for name, content := range req.KeyFiles {
		keyFilesContent.WriteString(fmt.Sprintf("=== %s ===\n%s\n", name, content))
	}

	userMsg := fmt.Sprintf(`Generate a Dockerfile for this project.
PROJECT INFO:

Language: %s
Framework: %s
Build tool: %s

FILE TREE:
%s

KEY FILES:
%s`, req.ProjectInfo.Language, req.ProjectInfo.Framework, req.ProjectInfo.BuildTool, fileTreeList, keyFilesContent.String())

	// Few-shot examples
	exampleUser1 := "Generate a Dockerfile for this project.\nPROJECT INFO:\nLanguage: node\nFramework: express\nBuild tool: npm"
	exampleAssistant1 := `{"dockerfile": "FROM node:20-alpine\nWORKDIR /app\nCOPY package*.json ./\nRUN npm ci --omit=dev\nCOPY . .\nENV NODE_ENV=production\nENV PORT=3000\nRUN addgroup -g 1001 -S nodejs && adduser -S app -u 1001 && chown -R app:nodejs /app\nUSER app\nEXPOSE 3000\nCMD [\"npm\", \"start\"]", "expose_port": 3000, "build_args": {}, "reasoning": "Standard Node.js setup using multi-stage alpine base. Created non-root user for security."}`

	exampleUser2 := "Generate a Dockerfile for this project.\nPROJECT INFO:\nLanguage: go\nFramework: standard\nBuild tool: go build"
	exampleAssistant2 := `{"dockerfile": "FROM golang:1.22-alpine AS builder\nWORKDIR /build\nCOPY go.mod go.sum ./\nRUN go mod download\nCOPY . .\nRUN CGO_ENABLED=0 GOOS=linux go build -ldflags='-s -w' -o /app .\n\nFROM gcr.io/distroless/static-debian12\nCOPY --from=builder /app /app\nUSER nonroot:nonroot\nEXPOSE 8080\nENTRYPOINT [\"/app\"]", "expose_port": 8080, "build_args": {}, "reasoning": "Uses distroless base for maximum security and minimal attack surface. Multi-stage build handles compilation."}`

	return []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: sysMsg},
		{Role: openai.ChatMessageRoleUser, Content: exampleUser1},
		{Role: openai.ChatMessageRoleAssistant, Content: exampleAssistant1},
		{Role: openai.ChatMessageRoleUser, Content: exampleUser2},
		{Role: openai.ChatMessageRoleAssistant, Content: exampleAssistant2},
		{Role: openai.ChatMessageRoleUser, Content: userMsg},
	}
}
