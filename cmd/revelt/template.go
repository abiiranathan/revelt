package main

import (
	"embed"
	"fmt"
	"strings"
)

// FileTemplate defines the target path and file contents.
type FileTemplate struct {
	Path    string
	Content string
}

//go:embed templates/react/*.tpl
var reactTemplatesFS embed.FS

//go:embed templates/svelte/*.tpl
var svelteTemplatesFS embed.FS

//go:embed templates/index.html
var IndexPageBytes []byte

// renderTemplates reads all template files from the embedded filesystem
// renderTemplates reads all template files from the embedded filesystem and
// substitutes {{KEY}} placeholders in each file's content with the
// corresponding value from vars.
func renderTemplates(fs embed.FS, pathMap map[string]string, vars map[string]string) ([]FileTemplate, error) {
	var result []FileTemplate

	for templateFile, outputPath := range pathMap {
		// Substitute vars in the output path too.
		for k, v := range vars {
			outputPath = strings.ReplaceAll(outputPath, "{{"+k+"}}", v)
		}

		templateContent, err := fs.ReadFile(templateFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read template %s: %w", templateFile, err)
		}

		content := string(templateContent)
		for k, v := range vars {
			content = strings.ReplaceAll(content, "{{"+k+"}}", v)
		}

		result = append(result, FileTemplate{
			Path:    outputPath,
			Content: content,
		})
	}

	return result, nil
}

// ReactTemplates returns project files designed for a React architecture.
func ReactTemplates(vars map[string]string) []FileTemplate {
	pathMap := map[string]string{
		"templates/react/revelt.json.tpl":                "revelt.json",
		"templates/react/package.json.tpl":               "{{SOURCE_DIR}}/package.json",
		"templates/react/tsconfig.json.tpl":              "{{SOURCE_DIR}}/tsconfig.json",
		"templates/react/global.d.ts.tpl":                "{{SOURCE_DIR}}/global.d.ts",
		"templates/react/revelt.types.d.ts.tpl":          "{{SOURCE_DIR}}/revelt.types.d.ts",
		"templates/react/components_Counter.tsx.tpl":     "{{SOURCE_DIR}}/{{COMPONENT_DIR}}/Counter.tsx",
		"templates/react/components_Header.tsx.tpl":      "{{SOURCE_DIR}}/{{COMPONENT_DIR}}/Header.tsx",
		"templates/react/components_ClientChart.tsx.tpl": "{{SOURCE_DIR}}/{{COMPONENT_DIR}}/ClientChart.tsx",
		"templates/react/client.tsx.tpl":                 "{{SOURCE_DIR}}/client.tsx",
		"templates/react/render-server.js.tpl":           "{{SOURCE_DIR}}/render-server.js",
		"templates/react/build.mjs.tpl":                  "{{SOURCE_DIR}}/build.mjs",
		"templates/react/main.go.tpl":                    "main.go",
		"templates/react/go.mod.tpl":                     "go.mod",
	}

	templates, err := renderTemplates(reactTemplatesFS, pathMap, vars)
	if err != nil {
		panic(fmt.Sprintf("failed to render react templates: %v", err))
	}

	return templates
}

// SvelteTemplates returns template files designed for Svelte.
func SvelteTemplates(vars map[string]string) []FileTemplate {
	pathMap := map[string]string{
		"templates/svelte/revelt.json.tpl":                   "revelt.json",
		"templates/svelte/package.json.tpl":                  "{{SOURCE_DIR}}/package.json",
		"templates/svelte/tsconfig.json.tpl":                 "{{SOURCE_DIR}}/tsconfig.json",
		"templates/svelte/global.d.ts.tpl":                   "{{SOURCE_DIR}}/global.d.ts",
		"templates/svelte/revelt.types.d.ts.tpl":             "{{SOURCE_DIR}}/revelt.types.d.ts",
		"templates/svelte/components_Counter.svelte.tpl":     "{{SOURCE_DIR}}/{{COMPONENT_DIR}}/Counter.svelte",
		"templates/svelte/components_Header.svelte.tpl":      "{{SOURCE_DIR}}/{{COMPONENT_DIR}}/Header.svelte",
		"templates/svelte/components_ClientChart.svelte.tpl": "{{SOURCE_DIR}}/{{COMPONENT_DIR}}/ClientChart.svelte",
		"templates/svelte/client.ts.tpl":                     "{{SOURCE_DIR}}/client.ts",
		"templates/svelte/render-server.js.tpl":              "{{SOURCE_DIR}}/render-server.js",
		"templates/svelte/build.mjs.tpl":                     "{{SOURCE_DIR}}/build.mjs",
		"templates/svelte/main.go.tpl":                       "main.go",
		"templates/svelte/go.mod.tpl":                        "go.mod",
	}

	templates, err := renderTemplates(svelteTemplatesFS, pathMap, vars)
	if err != nil {
		panic(fmt.Sprintf("failed to render svelte templates: %v", err))
	}

	return templates
}
