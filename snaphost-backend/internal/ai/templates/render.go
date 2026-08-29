package templates

import (
	"embed"
	"fmt"
	"strings"
)

//go:embed files/*.tmpl
var TemplateFS embed.FS

// Render applies the provided variables to the template file.
func Render(template *Template, vars map[string]string) (string, error) {
	content, err := TemplateFS.ReadFile("files/" + template.FileName)
	if err != nil {
		return "", fmt.Errorf("read template %s: %w", template.FileName, err)
	}

	result := string(content)
	for k, v := range vars {
		result = strings.ReplaceAll(result, fmt.Sprintf("{{%s}}", k), v)
	}

	return result, nil
}
