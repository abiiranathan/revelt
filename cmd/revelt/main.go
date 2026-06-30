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
	case "generate", "g":
		runGenerateCmd()
	case "clean":
		runCleanCmd()
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

// printUsage displays instructions for each of the available subcommands.
func printUsage() {
	fmt.Println("Usage: revelt <command> [arguments]")
	fmt.Println("\nCommands:")
	fmt.Println("  init     Initialize a new revelt project")
	fmt.Println("  build    Build frontend assets (server and client bundles)")
	fmt.Println("  dev      Start the development environment (watcher + auto-reloading server)")
	fmt.Println("  generate Generate new components (shorthand: g)")
	fmt.Println("  clean    Purge compiled frontend assets, build caches, and dev binaries")
	fmt.Println("  update   Update framework files (build.mjs, render-server.js, client entry)")
	fmt.Println("  version  Print the revelt version")
}

// validateConfig validates structural properties of the parsed configuration.
func validateConfig(cfg *revelt.ProjectConfig) error {
	framework := strings.ToLower(cfg.Framework)
	if framework != "react" && framework != "svelte" {
		return fmt.Errorf("unsupported framework %q (must be 'react' or 'svelte')", cfg.Framework)
	}
	if cfg.SourceDir == "" {
		return fmt.Errorf("source_dir cannot be empty")
	}
	if cfg.ComponentDir == "" {
		return fmt.Errorf("component_dir cannot be empty")
	}
	if cfg.Port <= 0 || cfg.Port > 65535 {
		return fmt.Errorf("port %d is out of range (1-65535)", cfg.Port)
	}
	return nil
}

// installDependencies auto-detects and triggers a frontend package installation.
func installDependencies(dir string) {
	fmt.Println("\n[revelt] detecting package manager and installing dependencies...")

	managers := []struct {
		name string
		args []string
	}{
		{"bun", []string{"install"}},
		{"pnpm", []string{"install"}},
		{"yarn", []string{"install"}},
		{"npm", []string{"install"}},
	}

	var chosenManager string
	var chosenArgs []string

	for _, m := range managers {
		if _, err := exec.LookPath(m.name); err == nil {
			chosenManager = m.name
			chosenArgs = m.args
			break
		}
	}

	if chosenManager == "" {
		fmt.Println("[revelt] warning: no package managers (bun, pnpm, yarn, npm) found in PATH. Skipping automated installation.")
		return
	}

	fmt.Printf("[revelt] running '%s %s' in %s...\n", chosenManager, strings.Join(chosenArgs, " "), dir)
	cmd := exec.Command(chosenManager, chosenArgs...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		fmt.Printf("[revelt] warning: dependency installation failed: %v\n", err)
	} else {
		fmt.Println("[revelt] frontend dependencies successfully installed.")
	}
}

func runInit() {
	initCmd := flag.NewFlagSet("init", flag.ExitOnError)
	frameworkOpt := initCmd.String("framework", "react", "Framework to use: react or svelte")
	dirOpt := initCmd.String("dir", ".", "Directory to initialize the project in")
	sourceDirOpt := initCmd.String("source-dir", "frontend", "Frontend source directory name (relative to --dir)")
	componentDirOpt := initCmd.String("component-dir", "src/components", "Component subdirectory name (relative to --source-dir)")
	tailwindOpt := initCmd.Bool("tailwind", false, "Set up Tailwind CSS v4")

	var installOpt bool
	initCmd.BoolVar(&installOpt, "install", false, "Automatically install frontend dependencies after project initialization")
	initCmd.BoolVar(&installOpt, "i", false, "Automatically install frontend dependencies after project initialization (shorthand)")

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

	// Ensure SOURCE_DIR is clean of relative "./" prefixes for go:embed compliance
	sourceDirPath := filepath.ToSlash(filepath.Clean(sourceDir))

	// Clean path and dynamically find parent directory of componentDir
	cleanedCompDir := filepath.ToSlash(filepath.Clean(componentDir))
	cssParentDir := filepath.ToSlash(filepath.Dir(cleanedCompDir))

	compParentPath := cssParentDir
	if compParentPath == "." {
		compParentPath = ""
	} else {
		compParentPath = compParentPath + "/"
	}

	tailwindDeps := ""
	tailwindCSSImport := ""
	if *tailwindOpt {
		if framework == "react" {
			tailwindDeps = ",\n    \"tailwindcss\": \"^4.0.0\",\n    \"@tailwindcss/postcss\": \"^4.0.0\",\n    \"postcss\": \"^8.4.38\""
		} else {
			tailwindDeps = ",\n    \"tailwindcss\": \"^4.0.0\",\n    \"@tailwindcss/vite\": \"^4.0.0\""
			if cssParentDir == "." {
				tailwindCSSImport = "import './app.css';\n"
			} else {
				tailwindCSSImport = fmt.Sprintf("import './%s/app.css';\n", cssParentDir)
			}
		}
	}

	vars := map[string]string{
		"SOURCE_DIR":          sourceDirPath,
		"COMPONENT_DIR":       componentDir,
		"TAILWIND":            strconv.FormatBool(*tailwindOpt),
		"TAILWIND_DEPS":       tailwindDeps,
		"TAILWIND_CSS_IMPORT": tailwindCSSImport,
		"COMP_PARENT_PATH":    compParentPath,
	}

	fmt.Printf("Initializing revelt %s project in: %s\n", framework, targetDir)
	fmt.Printf("  source-dir:    %s\n", sourceDirPath)
	fmt.Printf("  component-dir: %s\n", componentDir)

	if err := os.MkdirAll(sourceDir, 0755); err != nil {
		log.Fatalf("Error creating directory %s: %v\n", sourceDir, err)
	}

	// Create dist/client directory structure early and write placeholder
	// templates to prevent compile-time go:embed path failures on fresh setups.
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

	// Create placeholder render-server.cjs inside dist to prevent compile-time
	// go:embed failures on fresh setups before the initial build is ran.
	distDir := filepath.Join(targetDir, sourceDir, "dist")
	placeholderCJS := filepath.Join(distDir, "render-server.cjs")
	if err := os.WriteFile(placeholderCJS, []byte("// placeholder\n"), 0644); err != nil {
		log.Fatalf("Error writing placeholder render-server file: %s\n", err)
	}

	if *tailwindOpt {
		var appCssDir string
		if cssParentDir == "." {
			appCssDir = filepath.Join(targetDir, sourceDir)
		} else {
			appCssDir = filepath.Join(targetDir, sourceDir, cssParentDir)
		}
		_ = os.MkdirAll(appCssDir, 0755)
		if err := os.WriteFile(filepath.Join(appCssDir, "app.css"), []byte("@import \"tailwindcss\";\n"), 0644); err != nil {
			log.Fatalf("Error writing app.css file: %s\n", err)
		}
		if cssParentDir == "." {
			fmt.Println("  Created app.css with Tailwind CSS v4 directive")
		} else {
			fmt.Printf("  Created %s/app.css with Tailwind CSS v4 directive\n", cssParentDir)
		}

		if framework == "react" {
			postcssConfig := "export default {\n  plugins: {\n    '@tailwindcss/postcss': {},\n  },\n};\n"
			if err := os.WriteFile(filepath.Join(targetDir, sourceDir, "postcss.config.js"), []byte(postcssConfig), 0644); err != nil {
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

	if installOpt {
		installDependencies(filepath.Join(targetDir, sourceDir))
	}

	fmt.Println("\nProject successfully initialized!")
	fmt.Println("Next steps:")
	if !installOpt {
		fmt.Printf("  cd %s && npm install\n", filepath.Join(targetDir, sourceDir))
	} else {
		fmt.Printf("  cd %s\n", filepath.Join(targetDir, sourceDir))
	}
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

	if err := validateConfig(cfg); err != nil {
		fmt.Printf("Error: invalid configuration: %v\n", err)
		os.Exit(1)
	}

	// 1. Run 'go mod tidy' automatically if go.sum is missing or revelt is not configured
	var runTidy bool
	if _, err := os.Stat("go.sum"); os.IsNotExist(err) {
		runTidy = true
	} else {
		goModData, err := os.ReadFile("go.mod")
		if err == nil && !strings.Contains(string(goModData), "github.com/abiiranathan/revelt") {
			runTidy = true
		}
	}

	if runTidy {
		fmt.Println("[revelt] resolving dependencies using 'go mod tidy'...")
		tidyCmd := exec.Command("go", "mod", "tidy")
		tidyCmd.Stdout = os.Stdout
		tidyCmd.Stderr = os.Stderr
		if err := tidyCmd.Run(); err != nil {
			fmt.Printf("Error: 'go mod tidy' execution failed: %v\n", err)
			os.Exit(1)
		}
	}

	// 2. Build Frontend (Produces assets inside dist/client/ for embedding)
	fmt.Printf("Building frontend in %s…\n", cfg.SourceDir)
	feCmd := exec.Command("node", "build.mjs")
	feCmd.Dir = cfg.SourceDir
	feCmd.Stdout = os.Stdout
	feCmd.Stderr = os.Stderr
	if err := feCmd.Run(); err != nil {
		fmt.Printf("Error: frontend build failed: %v\n", err)
		os.Exit(1)
	}

	// 3. Build Backend (Safely embeds the compiled frontend assets)
	if cfg.GoBuildCmd != "" {
		parts := strings.Fields(cfg.GoBuildCmd)
		fmt.Printf("Building Go project: %s\n", cfg.GoBuildCmd)
		beCmd := exec.Command(parts[0], parts[1:]...)
		beCmd.Stdout = os.Stdout
		beCmd.Stderr = os.Stderr
		if err := beCmd.Run(); err != nil {
			fmt.Printf("Error: backend build failed: %v\n", err)
			os.Exit(1)
		}
	}

	fmt.Println("Build complete.")
}

func runDevCmd() {
	cfg, err := revelt.LoadConfig("revelt.json")
	if err != nil {
		fmt.Printf("Error: failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	if err := validateConfig(cfg); err != nil {
		fmt.Printf("Error: invalid configuration: %v\n", err)
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

// runGenerateCmd produces components with annotations configured according to system choices.
func runGenerateCmd() {
	genCmd := flag.NewFlagSet("generate", flag.ExitOnError)
	modeOpt := genCmd.String("mode", "hydrate", "Rendering mode: ssr, hydrate, client, or lazy-client")

	if len(os.Args) < 4 {
		fmt.Println("Usage: revelt generate component <ComponentName> [--mode=<mode>]")
		fmt.Println("Shorthand: revelt g component <ComponentName> [--mode=<mode>]")
		os.Exit(1)
	}

	itemType := os.Args[2]
	if itemType != "component" {
		fmt.Printf("Error: unknown generator type %q. Currently only 'component' is supported.\n", itemType)
		os.Exit(1)
	}

	compName := os.Args[3]
	if compName == "" {
		fmt.Println("Error: component name cannot be empty")
		os.Exit(1)
	}
	if strings.ContainsAny(compName, " \t\r\n") {
		fmt.Println("Error: component name cannot contain spaces")
		os.Exit(1)
	}

	if err := genCmd.Parse(os.Args[4:]); err != nil {
		log.Fatalf("Error parsing flags: %v\n", err)
	}

	cfg, err := revelt.LoadConfig("revelt.json")
	if err != nil {
		fmt.Printf("Error: failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	if err := validateConfig(cfg); err != nil {
		fmt.Printf("Error: invalid configuration: %v\n", err)
		os.Exit(1)
	}

	mode := strings.ToLower(*modeOpt)
	if mode != "ssr" && mode != "hydrate" && mode != "client" && mode != "lazy-client" {
		log.Fatalf("Error: invalid mode %q. Must be one of: ssr, hydrate, client, lazy-client\n", mode)
	}

	ext := ".tsx"
	if strings.ToLower(cfg.Framework) == "svelte" {
		ext = ".svelte"
	}

	// Clean component name and ensure first letter is capitalized
	if len(compName) > 0 {
		compName = strings.ToUpper(compName[:1]) + compName[1:]
	}

	targetFile := filepath.Join(cfg.SourceDir, cfg.ComponentDir, compName+ext)

	// Guard to prevent unintended workspace overrides
	if _, err := os.Stat(targetFile); err == nil {
		fmt.Printf("Error: component %s already exists at %s\n", compName, targetFile)
		os.Exit(1)
	}

	if err := os.MkdirAll(filepath.Dir(targetFile), 0755); err != nil {
		log.Fatalf("Error creating component directory: %v\n", err)
	}

	var content string
	if strings.ToLower(cfg.Framework) == "svelte" {
		content = fmt.Sprintf(`<!-- @mode %s -->
<script lang="ts">
  interface Props {
    title?: string;
  }
  let { title = "%s component" }: Props = $props();
</script>

<div class="component-box">
  <h3>{title}</h3>
  <p>Rendered in %s mode.</p>
</div>

<style>
  .component-box {
    border: 1px solid #e2e8f0;
    border-radius: 8px;
    padding: 1rem;
    margin: 0.5rem 0;
  }
</style>
`, mode, compName, mode)
	} else {
		content = fmt.Sprintf(`// @mode %s

interface %sProps {
  title?: string;
}

export default function %s({ title = "%s component" }: %sProps) {
  return (
    <div style={{ border: "1px solid #e2e8f0", borderRadius: "8px", padding: "1rem", margin: "0.5rem 0" }}>
      <h3>{title}</h3>
      <p>Rendered in %s mode.</p>
    </div>
  );
}
`, mode, compName, compName, compName, compName, mode)
	}

	if err := os.WriteFile(targetFile, []byte(content), 0644); err != nil {
		log.Fatalf("Error writing component: %v\n", err)
	}

	fmt.Printf("[revelt] successfully generated component: %s\n", targetFile)
}

// runCleanCmd deletes binary builds and cache files safely.
func runCleanCmd() {
	cfg, err := revelt.LoadConfig("revelt.json")
	if err != nil {
		fmt.Printf("Error: failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	if err := validateConfig(cfg); err != nil {
		fmt.Printf("Error: invalid configuration: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("[revelt] cleaning workspace...")

	// Remove transient dev binaries
	bins := []string{"revelt_bin", "revelt_bin.exe"}
	for _, b := range bins {
		if err := os.Remove(b); err == nil {
			fmt.Printf("  removed temporary binary: %s\n", b)
		}
	}

	// Safely clean target compile distribution
	if cfg.OutDir != "" {
		outPath := filepath.Clean(cfg.OutDir)
		if outPath != "." && outPath != "/" && outPath != ".." {
			if err := os.RemoveAll(outPath); err == nil {
				fmt.Printf("  removed build output directory: %s\n", outPath)
			} else if !os.IsNotExist(err) {
				fmt.Printf("  error removing output directory %s: %v\n", outPath, err)
			}
		}
	}

	// Purge local cache configurations
	cachePath := filepath.Join(cfg.SourceDir, "node_modules", ".vite")
	if err := os.RemoveAll(cachePath); err == nil && !os.IsNotExist(err) {
		fmt.Println("  removed vite cache directory")
	}

	fmt.Println("[revelt] workspace cleaned.")
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

	if err := validateConfig(cfg); err != nil {
		fmt.Printf("Error: invalid configuration: %v\n", err)
		os.Exit(1)
	}

	framework := strings.ToLower(cfg.Framework)
	if framework != "react" && framework != "svelte" {
		fmt.Printf("Error: unsupported framework %q in revelt.json (must be react or svelte)\n", cfg.Framework)
		os.Exit(1)
	}

	// Clean path and dynamically find parent directory of componentDir.
	cleanedCompDir := filepath.ToSlash(filepath.Clean(cfg.ComponentDir))
	cssParentDir := filepath.ToSlash(filepath.Dir(cleanedCompDir))

	compParentPath := cssParentDir
	if compParentPath == "." {
		compParentPath = ""
	} else {
		compParentPath = compParentPath + "/"
	}

	// Dynamically check if the project uses Tailwind by verifying if app.css exists.
	// This prevents updates from erasing the style import inside client.js.
	tailwindCSSImport := ""
	var appCSSPath string
	if cssParentDir == "." {
		appCSSPath = filepath.Join(cfg.SourceDir, "app.css")
	} else {
		appCSSPath = filepath.Join(cfg.SourceDir, cssParentDir, "app.css")
	}

	if _, err := os.Stat(appCSSPath); err == nil {
		if cssParentDir == "." {
			tailwindCSSImport = "import './app.css';\n"
		} else {
			tailwindCSSImport = fmt.Sprintf("import './%s/app.css';\n", cssParentDir)
		}
	}

	vars := map[string]string{
		"SOURCE_DIR":          cfg.SourceDir,
		"COMPONENT_DIR":       cfg.ComponentDir,
		"TAILWIND":            "false",
		"TAILWIND_DEPS":       "",
		"TAILWIND_CSS_IMPORT": tailwindCSSImport,
		"COMP_PARENT_PATH":    compParentPath,
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
