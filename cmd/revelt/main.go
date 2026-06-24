// Command revelt provides a command-line interface to manage revelt projects.
// It resolves all execute paths dynamically using values from revelt.json.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/abiiranathan/revelt/revelt"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	subcommand := os.Args[1]

	switch subcommand {
	case "init":
		runInit()
	case "build":
		runBuildCmd()
	case "dev":
		runDevCmd()
	default:
		fmt.Printf("Unknown subcommand: %s\n", subcommand)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("Usage: revelt <command> [arguments]")
	fmt.Println("\nCommands:")
	fmt.Println("  init   Initialize a new revelt project")
	fmt.Println("  build  Build frontend assets (server and client bundles)")
	fmt.Println("  dev    Start the development environment (watcher + server)")
}

// cmd/revelt/main.go
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

	// Create dist/client directory structure early and write a placeholder template
	// to prevent compile-time go:embed path failures on new project setups.
	distClientDir := filepath.Join(targetDir, sourceDir, "dist", "client")
	if err := os.MkdirAll(distClientDir, 0755); err != nil {
		log.Fatalf("Error creating distribution folders: %v\n", err)
	}
	err := os.WriteFile(
		filepath.Join(distClientDir, "index.html"),
		IndexPageBytes,
		0644,
	)
	if err != nil {
		log.Fatalf("Error writing distribution index file: %s\n", err)
	}

	// index.html lives at the root of the source directory.
	err = os.WriteFile(
		filepath.Join(targetDir, sourceDir, "index.html"),
		IndexPageBytes,
		0644,
	)
	if err != nil {
		log.Fatalf("Error writing template index file: %s\n", err)
	}

	// Create Tailwind folders & files if enabled
	if *tailwindOpt {
		appCssDir := filepath.Join(targetDir, sourceDir, "src")
		_ = os.MkdirAll(appCssDir, 0755)
		err := os.WriteFile(
			filepath.Join(appCssDir, "app.css"),
			[]byte("@import \"tailwindcss\";\n"),
			0644,
		)
		if err != nil {
			log.Fatalf("Error writing app.css file: %s\n", err)
		}
		fmt.Println("  Created src/app.css with Tailwind CSS v4 directive")

		// ONLY create postcss.config.js for React.
		// vite has native support in svelte.
		if framework == "react" {
			postcssConfig := "export default {\n  plugins: {\n    '@tailwindcss/postcss': {},\n  },\n};\n"
			err = os.WriteFile(
				filepath.Join(targetDir, sourceDir, "postcss.config.js"),
				[]byte(postcssConfig),
				0644,
			)
			if err != nil {
				log.Fatalf("Error writing postcss.config.js file: %s\n", err)
			}
			fmt.Println("  Created postcss.config.js for editor autocompletion & style loaders")
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
	fmt.Println("To get started, follow these commands:")
	fmt.Printf("  cd %s/%s\n", targetDir, sourceDir)
	fmt.Println("  npm install")
	fmt.Println("  npm run build")
	fmt.Println("  cd ..")
	fmt.Println("  go run main.go")
}

func runBuildCmd() {
	cfg, err := revelt.LoadConfig("revelt.json")
	if err != nil {
		fmt.Printf("Error: failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Running build within configured source directory: %s\n", cfg.SourceDir)
	cmd := exec.Command("node", "build.mjs")
	cmd.Dir = cfg.SourceDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		fmt.Printf("Build failed: %v\n", err)
		os.Exit(1)
	}
}

func runDevCmd() {
	cfg, err := revelt.LoadConfig("revelt.json")
	if err != nil {
		fmt.Printf("Error: failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Starting watcher within configured source directory: %s\n", cfg.SourceDir)

	watchCmd := exec.Command("node", "build.mjs", "--watch")
	watchCmd.Dir = cfg.SourceDir
	watchCmd.Stdout = os.Stdout
	watchCmd.Stderr = os.Stderr

	if err := watchCmd.Start(); err != nil {
		fmt.Printf("Failed to start watch tool: %v\n", err)
		os.Exit(1)
	}

	goCmd := exec.Command("go", "run", "main.go")
	goCmd.Stdout = os.Stdout
	goCmd.Stderr = os.Stderr

	if err := goCmd.Run(); err != nil {
		fmt.Printf("Go application exited: %v\n", err)
		_ = watchCmd.Process.Kill()
		os.Exit(1)
	}
}
