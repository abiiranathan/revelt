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

func runInit() {
	initCmd := flag.NewFlagSet("init", flag.ExitOnError)
	frameworkOpt := initCmd.String("framework", "react", "Framework to use: react or svelte")
	dirOpt := initCmd.String("dir", ".", "Directory to initialize the project in")
	sourceDirOpt := initCmd.String("source-dir", "frontend", "Frontend source directory name (relative to --dir)")
	componentDirOpt := initCmd.String("component-dir", "components", "Component subdirectory name (relative to --source-dir)")

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

	// sourceDir is a bare name ("frontend"), but revelt.json wants a relative path.
	sourceDirPath := "./" + sourceDir

	vars := map[string]string{
		"SOURCE_DIR":    sourceDirPath,
		"COMPONENT_DIR": componentDir,
	}

	fmt.Printf("Initializing revelt %s project in: %s\n", framework, targetDir)
	fmt.Printf("  source-dir:    %s\n", sourceDirPath)
	fmt.Printf("  component-dir: %s\n", componentDir)

	if err := os.MkdirAll(sourceDir, 0755); err != nil {
		log.Fatalf("Error creating directory %s: %v\n", sourceDir, err)
	}

	var templates []FileTemplate
	if framework == "react" {
		templates = ReactTemplates(vars)
	} else {
		templates = SvelteTemplates(vars)
	}

	// index.html lives at the root of the source directory.
	err := os.WriteFile(
		filepath.Join(targetDir, sourceDir, "index.html"),
		IndexPageBytes,
		0644,
	)
	if err != nil {
		log.Fatalf("Error writing index file: %s\n", err)
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
