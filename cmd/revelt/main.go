// Command revelt provides a command-line interface to manage revelt projects.
// It resolves all execute paths dynamically using values from revelt.json.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/abiiranathan/revelt/revelt"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "init":
		runInit()
	case "build":
		runBuildCmd()
	case "dev":
		runDevCmd()
	case "update":
		runUpdateCmd()
	case "version":
		fmt.Println("revelt v0.1.0")
	default:
		fmt.Printf("Unknown subcommand: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("Usage: revelt <command> [arguments]")
	fmt.Println("\nCommands:")
	fmt.Println("  init     Initialize a new revelt project")
	fmt.Println("  build    Build frontend assets (server and client bundles)")
	fmt.Println("  dev      Start the development environment (watcher + auto-reloading server)")
	fmt.Println("  update   Update framework files (build.mjs, render-server.js, client entry)")
	fmt.Println("  version  Print the revelt version")
}

func runInit() {
	initCmd := flag.NewFlagSet("init", flag.ExitOnError)
	frameworkOpt := initCmd.String("framework", "react", "Framework to use: react or svelte")
	dirOpt := initCmd.String("dir", ".", "Directory to initialize the project in")
	sourceDirOpt := initCmd.String("source-dir", "frontend", "Frontend source directory name (relative to --dir)")
	componentDirOpt := initCmd.String("component-dir", "src/components", "Component subdirectory name (relative to --source-dir)")
	tailwindOpt := initCmd.Bool("tailwind", false, "Set up Tailwind CSS v4")

	if err := initCmd.Parse(os.Args[2:]); err != nil {
		log.Fatalf("Error parsing flags: %v\n", err)
	}

	framework := strings.ToLower(*frameworkOpt)
	if framework != "react" && framework != "svelte" {
		log.Fatalf("Error: framework must be either 'react' or 'svelte'\n")
	}

	targetDir := *dirOpt
	sourceDir := *sourceDirOpt
	componentDir := *componentDirOpt

	sourceDirPath := "./" + sourceDir

	tailwindDeps := ""
	tailwindCSSImport := ""
	if *tailwindOpt {
		if framework == "react" {
			tailwindDeps = ",\n    \"tailwindcss\": \"^4.0.0\",\n    \"@tailwindcss/postcss\": \"^4.0.0\",\n    \"postcss\": \"^8.4.38\""
		} else {
			tailwindDeps = ",\n    \"tailwindcss\": \"^4.0.0\",\n    \"@tailwindcss/vite\": \"^4.0.0\""
			tailwindCSSImport = "import './src/app.css';\n"
		}
	}

	vars := map[string]string{
		"SOURCE_DIR":          sourceDirPath,
		"COMPONENT_DIR":       componentDir,
		"TAILWIND":            strconv.FormatBool(*tailwindOpt),
		"TAILWIND_DEPS":       tailwindDeps,
		"TAILWIND_CSS_IMPORT": tailwindCSSImport,
	}

	fmt.Printf("Initializing revelt %s project in: %s\n", framework, targetDir)
	fmt.Printf("  source-dir:    %s\n", sourceDirPath)
	fmt.Printf("  component-dir: %s\n", componentDir)

	if err := os.MkdirAll(sourceDir, 0755); err != nil {
		log.Fatalf("Error creating directory %s: %v\n", sourceDir, err)
	}

	// Create dist/client directory structure early and write a placeholder
	// template to prevent compile-time go:embed path failures on fresh setups.
	distClientDir := filepath.Join(targetDir, sourceDir, "dist", "client")
	if err := os.MkdirAll(distClientDir, 0755); err != nil {
		log.Fatalf("Error creating distribution folders: %v\n", err)
	}
	if err := os.WriteFile(filepath.Join(distClientDir, "index.html"), IndexPageBytes, 0644); err != nil {
		log.Fatalf("Error writing distribution index file: %s\n", err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, sourceDir, "index.html"), IndexPageBytes, 0644); err != nil {
		log.Fatalf("Error writing template index file: %s\n", err)
	}

	if *tailwindOpt {
		appCssDir := filepath.Join(targetDir, sourceDir, "src")
		_ = os.MkdirAll(appCssDir, 0755)
		if err := os.WriteFile(filepath.Join(appCssDir, "app.css"), []byte("@import \"tailwindcss\";\n"), 0644); err != nil {
			log.Fatalf("Error writing app.css file: %s\n", err)
		}
		fmt.Println("  Created src/app.css with Tailwind CSS v4 directive")

		if framework == "react" {
			postcssConfig := "export default {\n  plugins: {\n    '@tailwindcss/postcss': {},\n  },\n};\n"
			if err := os.WriteFile(filepath.Join(targetDir, sourceDir, "postcss.config.js"), []byte(postcssConfig), 0644); err != nil {
				log.Fatalf("Error writing postcss.config.js file: %s\n", err)
			}
			fmt.Println("  Created postcss.config.js for editor autocompletion & style loaders")
		}
	}

	// Purge stray React configs if Svelte is selected on re-initializations
	if framework == "svelte" {
		strayPostcss := filepath.Join(targetDir, sourceDir, "postcss.config.js")
		if _, err := os.Stat(strayPostcss); err == nil {
			_ = os.Remove(strayPostcss)
			fmt.Println("  Cleaned up stray postcss.config.js for Svelte build compatibility")
		}
	}

	var templates []FileTemplate
	if framework == "react" {
		templates = ReactTemplates(vars)
	} else {
		templates = SvelteTemplates(vars)
	}

	for _, t := range templates {
		fullPath := filepath.Join(targetDir, t.Path)
		dir := filepath.Dir(fullPath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			log.Fatalf("Error creating directory %s: %v\n", dir, err)
		}
		if err := os.WriteFile(fullPath, []byte(t.Content), 0644); err != nil {
			log.Fatalf("Error writing file %s: %v\n", fullPath, err)
		}
		fmt.Printf("  Created %s\n", t.Path)
	}

	fmt.Println("\nProject successfully initialized!")
	fmt.Println("Next steps:")
	fmt.Printf("  cd %s && npm install\n", filepath.Join(targetDir, sourceDir))
	fmt.Println("  npm run build")
	fmt.Println("  cd .. && go mod tidy && go run main.go")
	fmt.Println("\nOr for live development with auto-reload:")
	fmt.Println("  revelt dev")
}

func runBuildCmd() {
	cfg, err := revelt.LoadConfig("revelt.json")
	if err != nil {
		fmt.Printf("Error: failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	type result struct {
		name string
		err  error
	}

	results := make(chan result, 2)

	// Frontend: node build.mjs in the source directory.
	go func() {
		fmt.Printf("Building frontend in %s…\n", cfg.SourceDir)
		cmd := exec.Command("node", "build.mjs")
		cmd.Dir = cfg.SourceDir
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		results <- result{"frontend", cmd.Run()}
	}()

	// Backend: compile the Go project rooted at the working directory.
	go func() {
		if cfg.GoBuildCmd == "" {
			results <- result{"backend", nil} // nothing configured, skip silently
			return
		}

		parts := strings.Fields(cfg.GoBuildCmd)
		fmt.Printf("Building Go project: %s\n", cfg.GoBuildCmd)
		cmd := exec.Command(parts[0], parts[1:]...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		results <- result{"backend", cmd.Run()}
	}()

	var failed bool
	for range 2 {
		if r := <-results; r.err != nil {
			fmt.Printf("%s build failed: %v\n", r.name, r.err)
			failed = true
		}
	}

	if failed {
		os.Exit(1)
	}

	fmt.Println("Build complete.")
}

func runDevCmd() {
	cfg, err := revelt.LoadConfig("revelt.json")
	if err != nil {
		fmt.Printf("Error: failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	fmt.Println("[revelt] starting development environment…")
	fmt.Printf("[revelt] frontend source: %s\n", cfg.SourceDir)
	fmt.Println("[revelt] watching .go files for changes (Ctrl-C to stop)")

	runDev(ctx, cfg.SourceDir)

	fmt.Println("[revelt] development environment stopped.")
}

func runUpdateCmd() {
	updateCmd := flag.NewFlagSet("update", flag.ExitOnError)
	dryRun := updateCmd.Bool("dry-run", false, "Print files that would be updated without writing them")

	if err := updateCmd.Parse(os.Args[2:]); err != nil {
		log.Fatalf("Error parsing flags: %v\n", err)
	}

	cfg, err := revelt.LoadConfig("revelt.json")
	if err != nil {
		fmt.Printf("Error: failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	framework := strings.ToLower(cfg.Framework)
	if framework != "react" && framework != "svelte" {
		fmt.Printf("Error: unsupported framework %q in revelt.json (must be react or svelte)\n", cfg.Framework)
		os.Exit(1)
	}

	// Dynamically check if the project uses Tailwind by verifying if app.css exists.
	// This prevents updates from erasing the style import inside client.js.
	tailwindCSSImport := ""
	appCSSPath := filepath.Join(cfg.SourceDir, "src", "app.css")
	if _, err := os.Stat(appCSSPath); err == nil {
		tailwindCSSImport = "import './src/app.css';\n"
	}

	vars := map[string]string{
		"SOURCE_DIR":          cfg.SourceDir,
		"COMPONENT_DIR":       cfg.ComponentDir,
		"TAILWIND":            "false",
		"TAILWIND_DEPS":       "",
		"TAILWIND_CSS_IMPORT": tailwindCSSImport,
	}

	type frameworkFiles struct {
		templateKey string
		outputPath  string
	}

	var targets []frameworkFiles
	switch framework {
	case "react":
		targets = []frameworkFiles{
			{
				templateKey: "templates/react/build.mjs.tpl",
				outputPath:  filepath.Join(cfg.SourceDir, "build.mjs"),
			},
			{
				templateKey: "templates/react/render-server.js.tpl",
				outputPath:  filepath.Join(cfg.SourceDir, "render-server.js"),
			},
			{
				templateKey: "templates/react/client.tsx.tpl",
				outputPath:  filepath.Join(cfg.SourceDir, "client.tsx"),
			},
			{
				templateKey: "templates/react/revelt.types.d.ts.tpl",
				outputPath:  filepath.Join(cfg.SourceDir, "revelt.types.d.ts"),
			},
		}
	case "svelte":
		targets = []frameworkFiles{
			{
				templateKey: "templates/svelte/build.mjs.tpl",
				outputPath:  filepath.Join(cfg.SourceDir, "build.mjs"),
			},
			{
				templateKey: "templates/svelte/render-server.js.tpl",
				outputPath:  filepath.Join(cfg.SourceDir, "render-server.js"),
			},
			{
				templateKey: "templates/svelte/client.ts.tpl",
				outputPath:  filepath.Join(cfg.SourceDir, "client.ts"),
			},
			{
				templateKey: "templates/svelte/revelt.types.d.ts.tpl",
				outputPath:  filepath.Join(cfg.SourceDir, "revelt.types.d.ts"),
			},
		}
	}

	fs := reactTemplatesFS
	if framework == "svelte" {
		fs = svelteTemplatesFS
	}

	if *dryRun {
		fmt.Println("[revelt] dry run — no files will be written")
	} else {
		fmt.Printf("[revelt] updating %s framework files in %s\n", framework, cfg.SourceDir)
	}

	for _, t := range targets {
		rendered, err := renderTemplates(fs, map[string]string{t.templateKey: t.outputPath}, vars)
		if err != nil {
			log.Fatalf("Error rendering template %s: %v\n", t.templateKey, err)
		}
		file := rendered[0]

		if *dryRun {
			fmt.Printf("  would overwrite %s\n", file.Path)
			continue
		}

		if err := os.WriteFile(file.Path, []byte(file.Content), 0644); err != nil {
			log.Fatalf("Error writing %s: %v\n", file.Path, err)
		}
		fmt.Printf("  updated %s\n", file.Path)
	}

	if *dryRun {
		fmt.Println("[revelt] dry run complete — rerun without --dry-run to apply changes")
	} else {
		fmt.Println("[revelt] update complete")
	}
}
