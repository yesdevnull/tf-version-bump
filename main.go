// Package main provides a CLI tool for updating Terraform module versions across multiple files.
//
// The tool supports two modes of operation:
//  1. Single Module Mode: Update one module at a time via command-line flags
//  2. Config File Mode: Update multiple modules using a YAML configuration file
//
// It uses the official HashiCorp HCL library to safely parse and modify Terraform files
// while preserving formatting and comments.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
	"gopkg.in/yaml.v3"
)

// ModuleUpdate represents a single module source and its target version.
// It is used both for single module updates via CLI flags and for batch
// updates from YAML configuration files.
type ModuleUpdate struct {
	Source  string `yaml:"source"`  // Module source (e.g., "terraform-aws-modules/vpc/aws")
	Version string `yaml:"version"` // Target version (e.g., "5.0.0")
}

// GitConfig represents Git repository settings for batch operations across branches.
type GitConfig struct {
	Repository    string `yaml:"repository"`     // Git repository URL to clone
	BranchFilter  string `yaml:"branch_filter"`  // Regex pattern to filter branches (e.g., "release/.*")
	AuthorName    string `yaml:"author_name"`    // Git commit author name
	AuthorEmail   string `yaml:"author_email"`   // Git commit author email
	SigningKey    string `yaml:"signing_key"`    // Path to SSH key for signing commits
	CommitMessage string `yaml:"commit_message"` // Custom commit message (optional)
	Push          bool   `yaml:"push"`           // Whether to push changes to remote (default: false)
}

// Config represents the structure of a YAML configuration file for batch updates.
// The YAML file should contain a top-level "modules" key with a list of module updates.
//
// Example YAML:
//
//	modules:
//	  - source: "terraform-aws-modules/vpc/aws"
//	    version: "5.0.0"
//	  - source: "terraform-aws-modules/s3-bucket/aws"
//	    version: "4.0.0"
//	git:
//	  repository: "https://github.com/example/terraform-infra.git"
//	  branch_filter: "release/.*"
//	  author_name: "Bot User"
//	  author_email: "bot@example.com"
//	  signing_key: "/path/to/ssh/key"
//	  commit_message: "chore: update terraform module versions"
//	  push: false
type Config struct {
	Modules []ModuleUpdate `yaml:"modules"`
	Git     *GitConfig     `yaml:"git,omitempty"`
}

func main() {
	// Define CLI flags
	pattern := flag.String("pattern", "", "Glob pattern for Terraform files (e.g., '*.tf' or 'modules/**/*.tf')")
	moduleSource := flag.String("module", "", "Source of the module to update (e.g., 'terraform-aws-modules/vpc/aws')")
	version := flag.String("version", "", "Desired version number")
	configFile := flag.String("config", "", "Path to YAML config file with multiple module updates")
	forceAdd := flag.Bool("force-add", false, "Add version attribute to modules that don't have one (default: skip with warning)")
	flag.Parse()

	// Determine operation mode
	var updates []ModuleUpdate
	var gitConfig *GitConfig
	var err error

	if *configFile != "" {
		// Config file mode
		if *moduleSource != "" || *version != "" {
			log.Fatal("Error: Cannot use -config with -module or -version flags")
		}
		if *pattern == "" {
			log.Fatal("Error: -pattern flag is required")
		}
		updates, gitConfig, err = loadConfig(*configFile)
		if err != nil {
			log.Fatalf("Error loading config file: %v", err)
		}
		if len(updates) == 0 {
			log.Fatal("Error: Config file contains no module updates")
		}
	} else {
		// Single module mode
		if *pattern == "" || *moduleSource == "" || *version == "" {
			fmt.Println("Usage:")
			fmt.Println("  Single module:  tf-version-bump -pattern <glob> -module <source> -version <version>")
			fmt.Println("  Config file:    tf-version-bump -pattern <glob> -config <config-file>")
			flag.PrintDefaults()
			os.Exit(1)
		}
		updates = []ModuleUpdate{
			{Source: *moduleSource, Version: *version},
		}
	}

	// If git configuration is provided, use git-based workflow
	if gitConfig != nil && gitConfig.Repository != "" {
		if err := processGitRepository(gitConfig, updates, *pattern, *forceAdd); err != nil {
			log.Fatalf("Error processing git repository: %v", err)
		}
		return
	}

	// Standard file-based workflow
	processLocalFiles(updates, *pattern, *forceAdd)
}

// processLocalFiles handles the standard workflow of processing files in the current directory
func processLocalFiles(updates []ModuleUpdate, pattern string, forceAdd bool) {
	// Find matching files
	files, err := filepath.Glob(pattern)
	if err != nil {
		log.Fatalf("Error matching pattern: %v", err)
	}

	if len(files) == 0 {
		log.Fatalf("No files matched pattern: %s", pattern)
	}

	fmt.Printf("Found %d file(s) matching pattern '%s'\n", len(files), pattern)

	// Process each file with all module updates
	totalUpdates := 0
	for _, file := range files {
		for _, update := range updates {
			updated, err := updateModuleVersion(file, update.Source, update.Version, forceAdd)
			if err != nil {
				log.Printf("Error processing %s: %v", file, err)
				continue
			}
			if updated {
				fmt.Printf("✓ Updated module source '%s' to version '%s' in %s\n", update.Source, update.Version, file)
				totalUpdates++
			}
		}
	}

	if len(updates) > 1 {
		fmt.Printf("\nSuccessfully applied %d update(s) across all files\n", totalUpdates)
	} else {
		fmt.Printf("\nSuccessfully updated %d file(s)\n", totalUpdates)
	}
}

// loadConfig reads and parses a YAML configuration file containing module updates.
// It validates that all required fields (source and version) are present for each module.
//
// Parameters:
//   - filename: Path to the YAML configuration file
//
// Returns:
//   - []ModuleUpdate: List of module updates parsed from the file
//   - *GitConfig: Git configuration if present, nil otherwise
//   - error: Any error encountered during reading, parsing, or validation
func loadConfig(filename string) ([]ModuleUpdate, *GitConfig, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	// Validate module config
	for i, module := range config.Modules {
		if module.Source == "" {
			return nil, nil, fmt.Errorf("module at index %d is missing 'source' field", i)
		}
		if module.Version == "" {
			return nil, nil, fmt.Errorf("module at index %d is missing 'version' field", i)
		}
	}

	// Validate git config if present
	if config.Git != nil {
		if config.Git.Repository != "" {
			if config.Git.AuthorName == "" {
				return nil, nil, fmt.Errorf("git.author_name is required when git.repository is specified")
			}
			if config.Git.AuthorEmail == "" {
				return nil, nil, fmt.Errorf("git.author_email is required when git.repository is specified")
			}
			if config.Git.BranchFilter == "" {
				return nil, nil, fmt.Errorf("git.branch_filter is required when git.repository is specified")
			}
		}
	}

	return config.Modules, config.Git, nil
}

// processGitRepository handles the workflow of cloning a repository, filtering branches,
// and processing each matching branch with the version updates
func processGitRepository(gitCfg *GitConfig, updates []ModuleUpdate, pattern string, forceAdd bool) error {
	fmt.Printf("Cloning repository: %s\n", gitCfg.Repository)

	// Create a temporary directory for the clone
	tmpDir, err := os.MkdirTemp("", "tf-version-bump-*")
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Clone the repository
	repo, err := git.PlainClone(tmpDir, false, &git.CloneOptions{
		URL:      gitCfg.Repository,
		Progress: os.Stdout,
	})
	if err != nil {
		return fmt.Errorf("failed to clone repository: %w", err)
	}

	// Get all remote branches
	remote, err := repo.Remote("origin")
	if err != nil {
		return fmt.Errorf("failed to get remote: %w", err)
	}

	// List all references
	refs, err := remote.List(&git.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list remote branches: %w", err)
	}

	// Compile branch filter regex
	branchRegex, err := regexp.Compile(gitCfg.BranchFilter)
	if err != nil {
		return fmt.Errorf("invalid branch filter regex: %w", err)
	}

	// Filter branches
	var matchingBranches []string
	for _, ref := range refs {
		if ref.Name().IsBranch() {
			branchName := ref.Name().Short()
			if branchRegex.MatchString(branchName) {
				matchingBranches = append(matchingBranches, branchName)
			}
		}
	}

	if len(matchingBranches) == 0 {
		return fmt.Errorf("no branches matched filter: %s", gitCfg.BranchFilter)
	}

	fmt.Printf("\nFound %d matching branch(es):\n", len(matchingBranches))
	for _, branch := range matchingBranches {
		fmt.Printf("  - %s\n", branch)
	}
	fmt.Println()

	// Process each matching branch
	successCount := 0
	for _, branchName := range matchingBranches {
		fmt.Printf("Processing branch: %s\n", branchName)

		if err := processBranch(repo, tmpDir, branchName, gitCfg, updates, pattern, forceAdd); err != nil {
			log.Printf("Error processing branch %s: %v", branchName, err)
			continue
		}

		successCount++
		fmt.Printf("✓ Successfully processed branch: %s\n\n", branchName)
	}

	fmt.Printf("Completed processing %d/%d branch(es)\n", successCount, len(matchingBranches))
	return nil
}

// processBranch checks out a branch, applies updates, and commits the changes
func processBranch(repo *git.Repository, repoPath, branchName string, gitCfg *GitConfig, updates []ModuleUpdate, pattern string, forceAdd bool) error {
	// Get worktree
	worktree, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("failed to get worktree: %w", err)
	}

	// Checkout the branch
	branchRef := plumbing.NewRemoteReferenceName("origin", branchName)
	err = worktree.Checkout(&git.CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName(branchName),
		Create: true,
		Force:  true,
		Keep:   false,
	})
	if err != nil {
		// Try to checkout from remote reference
		err = worktree.Checkout(&git.CheckoutOptions{
			Hash:  plumbing.ZeroHash,
			Force: true,
		})
		if err != nil {
			return fmt.Errorf("failed to checkout branch: %w", err)
		}

		// Fetch the remote branch
		err = repo.Fetch(&git.FetchOptions{
			RemoteName: "origin",
			RefSpecs: []config.RefSpec{
				config.RefSpec(fmt.Sprintf("+refs/heads/%s:refs/remotes/origin/%s", branchName, branchName)),
			},
		})
		if err != nil && err != git.NoErrAlreadyUpToDate {
			return fmt.Errorf("failed to fetch branch: %w", err)
		}

		// Get the remote reference
		ref, err := repo.Reference(branchRef, true)
		if err != nil {
			return fmt.Errorf("failed to get reference for branch %s: %w", branchName, err)
		}

		// Checkout the branch
		err = worktree.Checkout(&git.CheckoutOptions{
			Hash:  ref.Hash(),
			Force: true,
		})
		if err != nil {
			return fmt.Errorf("failed to checkout branch hash: %w", err)
		}

		// Create local branch
		headRef := plumbing.NewHashReference(plumbing.NewBranchReferenceName(branchName), ref.Hash())
		err = repo.Storer.SetReference(headRef)
		if err != nil {
			return fmt.Errorf("failed to create local branch: %w", err)
		}
	}

	// Find matching files in the repository
	fullPattern := filepath.Join(repoPath, pattern)
	files, err := filepath.Glob(fullPattern)
	if err != nil {
		return fmt.Errorf("failed to match pattern: %w", err)
	}

	if len(files) == 0 {
		fmt.Printf("  No files matched pattern '%s' in this branch\n", pattern)
		return nil
	}

	fmt.Printf("  Found %d file(s) matching pattern\n", len(files))

	// Process each file with all module updates
	totalUpdates := 0
	for _, file := range files {
		for _, update := range updates {
			updated, err := updateModuleVersion(file, update.Source, update.Version, forceAdd)
			if err != nil {
				log.Printf("  Error processing %s: %v", file, err)
				continue
			}
			if updated {
				relPath, _ := filepath.Rel(repoPath, file)
				fmt.Printf("  ✓ Updated module '%s' to version '%s' in %s\n", update.Source, update.Version, relPath)
				totalUpdates++
			}
		}
	}

	if totalUpdates == 0 {
		fmt.Println("  No updates were made")
		return nil
	}

	// Add all changes
	_, err = worktree.Add(".")
	if err != nil {
		return fmt.Errorf("failed to stage changes: %w", err)
	}

	// Check if there are changes to commit
	status, err := worktree.Status()
	if err != nil {
		return fmt.Errorf("failed to get status: %w", err)
	}

	if status.IsClean() {
		fmt.Println("  No changes to commit")
		return nil
	}

	// Prepare commit message
	commitMsg := gitCfg.CommitMessage
	if commitMsg == "" {
		commitMsg = fmt.Sprintf("Update Terraform module versions\n\nApplied %d update(s) to module versions", totalUpdates)
	}

	// Create commit options
	commitOpts := &git.CommitOptions{
		Author: &object.Signature{
			Name:  gitCfg.AuthorName,
			Email: gitCfg.AuthorEmail,
			When:  time.Now(),
		},
	}

	// Add signing if specified
	if gitCfg.SigningKey != "" {
		// Read the SSH key
		keyData, err := os.ReadFile(gitCfg.SigningKey)
		if err != nil {
			return fmt.Errorf("failed to read signing key: %w", err)
		}

		// Note: go-git v5 has limited SSH signing support
		// For production use, you may need to use git command directly or a newer version
		fmt.Printf("  Note: SSH signing key specified (%s) but go-git has limited SSH signing support\n", gitCfg.SigningKey)
		fmt.Printf("  Consider using git CLI with GIT_SSH_COMMAND for full SSH signing support\n")
		_ = keyData // Suppress unused warning
	}

	// Commit the changes
	hash, err := worktree.Commit(commitMsg, commitOpts)
	if err != nil {
		return fmt.Errorf("failed to commit changes: %w", err)
	}

	fmt.Printf("  Committed changes: %s\n", hash.String()[:8])

	// Push if requested
	if gitCfg.Push {
		fmt.Printf("  Pushing to remote...\n")
		err = repo.Push(&git.PushOptions{
			RemoteName: "origin",
			RefSpecs: []config.RefSpec{
				config.RefSpec(fmt.Sprintf("refs/heads/%s:refs/heads/%s", branchName, branchName)),
			},
		})
		if err != nil {
			return fmt.Errorf("failed to push: %w", err)
		}
		fmt.Printf("  ✓ Pushed to remote\n")
	}

	return nil
}

// updateModuleVersion parses a Terraform file, finds modules with the specified source,
// updates their version attribute, and writes the modified content back to the file.
//
// The function preserves all formatting, comments, and other HCL structures in the file.
// If a matching module doesn't have a version attribute:
//   - When forceAdd is false (default): a warning is printed and the module is skipped
//   - When forceAdd is true: a version attribute is added to the module
//
// All modules with the same source attribute will be updated to the same version.
//
// Parameters:
//   - filename: Path to the Terraform file to process
//   - moduleSource: The module source to match (e.g., "terraform-aws-modules/vpc/aws")
//   - version: The target version to set (e.g., "5.0.0")
//   - forceAdd: If true, add version attribute to modules that don't have one
//
// Returns:
//   - bool: true if at least one module was updated, false otherwise
//   - error: Any error encountered during file reading, parsing, or writing
func updateModuleVersion(filename, moduleSource, version string, forceAdd bool) (bool, error) {
	// Read the file
	src, err := os.ReadFile(filename)
	if err != nil {
		return false, fmt.Errorf("failed to read file: %w", err)
	}

	// Parse the file with hclwrite
	file, diags := hclwrite.ParseConfig(src, filename, hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return false, fmt.Errorf("failed to parse HCL: %s", diags.Error())
	}

	// Track if we made any changes
	updated := false

	// Iterate through all blocks in the file
	for _, block := range file.Body().Blocks() {
		// Look for module blocks with matching source
		if block.Type() == "module" {
			// Get the source attribute
			sourceAttr := block.Body().GetAttribute("source")
			if sourceAttr != nil {
				// Extract the source value
				sourceTokens := sourceAttr.Expr().BuildTokens(nil)
				sourceValue := string(sourceTokens.Bytes())

				// Remove whitespace and quotes from the source value
				sourceValue = trimQuotes(strings.TrimSpace(sourceValue))

				// Check if this module's source matches
				if sourceValue == moduleSource {
					// Check if the module has a version attribute
					versionAttr := block.Body().GetAttribute("version")
					if versionAttr == nil {
						if !forceAdd {
							// Module doesn't have a version attribute - print warning and skip
							moduleName := ""
							if len(block.Labels()) > 0 {
								moduleName = block.Labels()[0]
							}
							fmt.Fprintf(os.Stderr, "Warning: Module %q in %s (source: %q) has no version attribute, skipping\n",
								moduleName, filename, moduleSource)
							continue
						}
						// forceAdd is true, so we'll add the version attribute below
					}

					// Update or add the version attribute
					block.Body().SetAttributeValue("version", cty.StringVal(version))
					updated = true
				}
			}
		}
	}

	// If we made changes, write the file back
	if updated {
		output := hclwrite.Format(file.Bytes())
		if err := os.WriteFile(filename, output, 0644); err != nil {
			return false, fmt.Errorf("failed to write file: %w", err)
		}
	}

	return updated, nil
}

// trimQuotes removes surrounding single or double quotes from a string.
// If the string doesn't have matching quotes on both ends, it returns the original string.
//
// Parameters:
//   - s: The string to trim quotes from
//
// Returns:
//   - string: The string with quotes removed, or the original string if no matching quotes found
//
// Examples:
//   - `"hello"` returns `hello`
//   - `'hello'` returns `hello`
//   - `hello` returns `hello`
//   - `"hello'` returns `"hello'` (mismatched quotes)
func trimQuotes(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
